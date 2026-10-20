/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#define GIT_NEXT_CURL_IMPLEMENTATION
#include "git-compat-util.h"
#if defined(GIT_WINDOWS_NATIVE) && !defined(NO_CURL)
#include "git-next-curl.h"
#include "git-next-tls-pins.h"
#include "abspath.h"
#include "exec-cmd.h"
#include "gettext.h"
#include "hash.h"
#include "hex.h"
#include "run-command.h"
#include "strbuf.h"

#define GIT_NEXT_CURL_NATIVE(result, name, arguments) curl_##name,
struct git_next_curl_api git_next_curl_api = {
	GIT_NEXT_CURL_FUNCTIONS(GIT_NEXT_CURL_NATIVE)
};
static const struct git_next_curl_api native_api = {
	GIT_NEXT_CURL_FUNCTIONS(GIT_NEXT_CURL_NATIVE)
};
#undef GIT_NEXT_CURL_NATIVE
static int explicit_selection, initialized;
static HMODULE openssl_provider;

static char *provider_path(void)
{
	const char *root = getenv("GIT_NEXT_COMPONENT_HOME");
	if (root && *root) {
		if (!is_absolute_path(root))
			die(_("GIT_NEXT_COMPONENT_HOME must be an absolute directory"));
		return xstrfmt("%s/tls-openssl/%s/%s", root, GIT_NEXT_TLS_PLATFORM,
			       GIT_NEXT_TLS_ARCHIVE_SHA256);
	}
	{
		char *suffix = xstrfmt("components/tls-openssl/%s/%s", GIT_NEXT_TLS_PLATFORM,
				      GIT_NEXT_TLS_ARCHIVE_SHA256);
		char *shared = git_next_machine_path(suffix);
		free(suffix);
		if (shared && is_directory(shared))
			return shared;
		free(shared);
	}
	root = getenv("LOCALAPPDATA");
	if (!root || !is_absolute_path(root))
		die(_("LOCALAPPDATA is unavailable for the configured TLS component"));
	return xstrfmt("%s/git-next/components/tls-openssl/%s/%s", root,
		       GIT_NEXT_TLS_PLATFORM, GIT_NEXT_TLS_ARCHIVE_SHA256);
}

static int regular_owned_file(const char *path)
{
	wchar_t wide[MAX_LONG_PATH];
	DWORD attributes;
	if (xutftowcs_long_path(wide, path) < 0)
		return 0;
	attributes = GetFileAttributesW(wide);
	return attributes != INVALID_FILE_ATTRIBUTES &&
		!(attributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY));
}

static int matches_pin(const char *path, const char *expected)
{
	struct git_hash_ctx context;
	unsigned char raw[GIT_MAX_RAWSZ];
	char hex[GIT_MAX_HEXSZ + 1], buffer[65536];
	ssize_t length;
	size_t total = 0;
	int fd;
	if (!regular_owned_file(path))
		return 0;
	fd = git_open_cloexec(path, O_RDONLY);
	if (fd < 0)
		return 0;
	git_hash_init(&context, &hash_algos[GIT_HASH_SHA256]);
	while ((length = xread(fd, buffer, sizeof(buffer))) > 0) {
		total += length;
		if (total > 32 * 1024 * 1024) {
			close(fd);
			return 0;
		}
		git_hash_update(&context, buffer, length);
	}
	close(fd);
	if (length < 0)
		return 0;
	git_hash_final(raw, &context);
	hash_to_hex_algop_r(hex, raw, &hash_algos[GIT_HASH_SHA256]);
	return !strcmp(hex, expected);
}

static int verify_provider(const char *directory)
{
	size_t i;
	for (i = 0; i < ARRAY_SIZE(git_next_tls_pins); i++) {
		char *path = xstrfmt("%s/%s", directory, git_next_tls_pins[i].name);
		int matches = matches_pin(path, git_next_tls_pins[i].sha256);
		free(path);
		if (!matches)
			return 0;
	}
	return 1;
}

static int prepare_provider(void)
{
	struct child_process install = CHILD_PROCESS_INIT;
	const char *system = getenv("SYSTEMROOT");
	char *script, *powershell;
	int result;
	if (!system || !is_absolute_path(system) ||
	    !strcmp(getenv("GIT_NEXT_TLS_AUTO_INSTALL") ? getenv("GIT_NEXT_TLS_AUTO_INSTALL") : "", "0"))
		return -1;
	powershell = xstrfmt("%s/System32/WindowsPowerShell/v1.0/powershell.exe", system);
	script = system_path("components/install-tls-provider.ps1");
	strvec_pushl(&install.args, powershell, "-NoLogo", "-NoProfile", "-NonInteractive",
		     "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", script,
		     "-Platform", GIT_NEXT_TLS_PLATFORM, "-Quiet", NULL);
	install.no_stdin = install.no_stdout = install.no_stderr = 1;
	install.clean_on_exit = install.wait_after_clean = 1;
	result = run_command(&install);
	free(script);
	free(powershell);
	return result;
}

static void provider_default_environment(const char *name, const wchar_t *wide_name,
					 const char *directory, const char *suffix)
{
	char *path;
	wchar_t wide_path[MAX_LONG_PATH];
	HMODULE runtime;
	union { FARPROC generic; int (__cdecl *putenv)(const wchar_t *, const wchar_t *); } function;
	if (getenv(name))
		return;
	path = NULL;
	if (!strcmp(name, "OPENSSL_CONF")) {
		path = system_path("runtime/" GIT_NEXT_TLS_NATIVE_PREFIX "/etc/ssl/openssl.cnf");
		if (access(path, R_OK)) {
			free(path);
			path = NULL;
		}
	}
	if (!path)
		path = xstrfmt("%s/%s/%s", directory, GIT_NEXT_TLS_NATIVE_PREFIX, suffix);
	setenv(name, path, 0);
	/* Keep the provider CRT's environment consistent with the UTF-16 OS block. */
	runtime = GetModuleHandleW(L"msvcrt.dll");
	function.generic = runtime ? GetProcAddress(runtime, "_wputenv_s") : NULL;
	if (function.generic && xutftowcs_long_path(wide_path, path) >= 0)
		function.putenv(wide_name, wide_path);
	free(path);
}

static void load_openssl_provider(void)
{
	char *directory, *library;
	wchar_t wide[MAX_LONG_PATH];
	struct git_next_curl_api selected;
	HMODULE module;
	if (openssl_provider)
		return;
	directory = provider_path();
	if (!verify_provider(directory)) {
		/* A pre-existing altered component is never silently overwritten. */
		if (is_directory(directory) || prepare_provider() || !verify_provider(directory))
			die(_("The configured OpenSSL TLS component is missing or invalid; prepare it with install-tls-provider.ps1"));
	}
	provider_default_environment("OPENSSL_CONF", L"OPENSSL_CONF", directory, "etc/ssl/openssl.cnf");
	provider_default_environment("OPENSSL_MODULES", L"OPENSSL_MODULES", directory, "lib/ossl-modules");
	library = xstrfmt("%s/%s", directory, GIT_NEXT_TLS_ENTRYPOINT);
	if (xutftowcs_long_path(wide, library) < 0)
		die(_("Could not resolve the configured TLS component"));
	module = LoadLibraryExW(wide, NULL, LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_SYSTEM32);
	free(directory);
	free(library);
	if (!module)
		die(_("Could not load the configured TLS component"));
#define GIT_NEXT_CURL_LOAD(result, name, arguments) do { \
	FARPROC address = GetProcAddress(module, "curl_" #name); \
	if (!address) { FreeLibrary(module); die(_("The configured TLS component has an incompatible API")); } \
	_Static_assert(sizeof(address) == sizeof(selected.name), "Windows function pointer ABI"); \
	memcpy(&selected.name, &address, sizeof(address)); \
} while (0);
	GIT_NEXT_CURL_FUNCTIONS(GIT_NEXT_CURL_LOAD)
#undef GIT_NEXT_CURL_LOAD
	openssl_provider = module;
	git_next_curl_api = selected;
}

CURLsslset git_next_curl_global_sslset(curl_sslbackend id, const char *name,
				     const curl_ssl_backend ***available)
{
	static const curl_ssl_backend native = { CURLSSLBACKEND_SCHANNEL, "schannel" };
	static const curl_ssl_backend external = { CURLSSLBACKEND_OPENSSL, "openssl" };
	static const curl_ssl_backend *backends[] = { &native, &external, NULL };
	if (available)
		*available = backends;
	if (initialized)
		return CURLSSLSET_TOO_LATE;
	if ((name && !strcasecmp(name, "openssl")) || (!name && id == CURLSSLBACKEND_OPENSSL)) {
		load_openssl_provider();
		explicit_selection = 1;
		return git_next_curl_api.global_sslset(id, name, NULL);
	}
	if ((name && !strcasecmp(name, "schannel")) || (!name && id == CURLSSLBACKEND_SCHANNEL)) {
		git_next_curl_api = native_api;
		explicit_selection = 1;
		return native_api.global_sslset(id, name, NULL);
	}
	return CURLSSLSET_UNKNOWN_BACKEND;
}

static void environment_provider(void)
{
	const char *name = getenv("CURL_SSL_BACKEND");
	if (!explicit_selection && name && !strcasecmp(name, "openssl"))
		git_next_curl_global_sslset(CURLSSLBACKEND_OPENSSL, "openssl", NULL);
}

CURLcode git_next_curl_global_init(long flags)
{
	CURLcode result;
	environment_provider();
	result = git_next_curl_api.global_init(flags);
	if (!result)
		initialized = 1;
	return result;
}

CURL *git_next_curl_easy_init(void)
{
	CURL *result;
	environment_provider();
	result = git_next_curl_api.easy_init();
	if (result)
		initialized = 1;
	return result;
}
#endif
