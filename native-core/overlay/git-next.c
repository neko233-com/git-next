/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#include "git-compat-util.h"
#include "git-next.h"
#include "abspath.h"
#include "exec-cmd.h"
#include "gettext.h"
#include "lockfile.h"
#include "path.h"
#include "run-command.h"
#include "strbuf.h"
#include "utf8.h"
#include "version.h"
#include "yyjson.h"
#ifdef GIT_WINDOWS_NATIVE
#include <shlobj.h>
#endif

#ifdef __APPLE__
#include <CoreFoundation/CoreFoundation.h>
#endif

#include "git-next-identity.h"
#include "git-next-catalog.h"

int git_next_lf_defaults = 1;

#ifndef GIT_WINDOWS_NATIVE
static const char *git_next_child_env(const struct strvec *env, const char *name)
{
	const char *value = getenv(name);
	size_t length = strlen(name), i;
	for (i = 0; i < env->nr; i++) {
		if (!strncmp(env->v[i], name, length) && env->v[i][length] == '=')
			value = env->v[i] + length + 1;
		else if (!strcmp(env->v[i], name))
			value = NULL;
	}
	return value;
}

static int git_next_interpreter_environment(struct strvec *env)
{
	char data[16385], *line, *next, *path = system_path("runtime/interpreter-environment");
	struct stat info;
	struct strbuf perl = STRBUF_INIT;
	const char *inherited = git_next_child_env(env, "PERL5LIB");
	int fd = open(path, O_RDONLY | O_NONBLOCK), failed = 0;
	ssize_t length;
	free(path);
	if (fd < 0)
		return -1;
	if (fstat(fd, &info) || !S_ISREG(info.st_mode) || info.st_size <= 0 || info.st_size > 16384) {
		close(fd);
		errno = EINVAL;
		return -1;
	}
	length = read_in_full(fd, data, info.st_size);
	close(fd);
	if (length != info.st_size) {
		errno = EIO;
		return -1;
	}
	data[length] = '\0';
	if (inherited && *inherited)
		strbuf_addstr(&perl, inherited);
	next = data;
	while ((line = strsep(&next, "\n"))) {
		char *relative = strchr(line, '=');
		if (!*line)
			continue;
		if (!relative) { failed = 1; break; }
		*relative++ = '\0';
		if (!starts_with(relative, "runtime/") || strchr(relative, ':') || strchr(relative, '\r') ||
		    strstr(relative, "/../") || ends_with(relative, "/..")) { failed = 1; break; }
		path = system_path(relative);
		if (!strcmp(line, "PERL5LIB")) {
			if (perl.len)
				strbuf_addch(&perl, ':');
			strbuf_addstr(&perl, path);
		} else if (!strcmp(line, "TCL_LIBRARY") || !strcmp(line, "TK_LIBRARY")) {
			const char *explicit = git_next_child_env(env, line);
			if (!explicit || !*explicit)
				strvec_pushf(env, "%s=%s", line, path);
		} else { failed = 1; }
		free(path);
		if (failed)
			break;
	}
	if (!failed && perl.len)
		strvec_pushf(env, "PERL5LIB=%s", perl.buf);
	strbuf_release(&perl);
	if (failed)
		errno = EINVAL;
	return failed ? -1 : 0;
}
#endif

int git_next_prepare_interpreter(struct strvec *args, struct strvec *env)
{
#ifndef GIT_WINDOWS_NATIVE
	char header[256], *token, *flags, *base;
	char *native = system_path(GIT_NEXT_RUNTIME_NATIVE_PREFIX "/"), *public = system_path("bin/");
	char *interpreter;
	const char *program = args->v[1], *selected = NULL;
	struct strvec command = STRVEC_INIT;
	struct stat info;
	ssize_t count;
	int owned = starts_with(program, native) || starts_with(program, public), fd;
	free(native);
	free(public);
	if (!owned)
		return 0;
	fd = open(program, O_RDONLY | O_NONBLOCK);
	if (fd < 0)
		return -1;
	if (fstat(fd, &info) || !S_ISREG(info.st_mode)) {
		close(fd);
		errno = EINVAL;
		return -1;
	}
	count = read(fd, header, sizeof(header) - 1);
	close(fd);
	if (count < 0)
		return -1;
	header[count] = '\0';
	if (!starts_with(header, "#!"))
		return 0;
	flags = strchr(header, '\n');
	if (flags)
		*flags = '\0';
	token = header + 2;
	while (isspace(*token)) token++;
	flags = token;
	while (*flags && !isspace(*flags)) flags++;
	if (*flags) *flags++ = '\0';
	while (isspace(*flags)) flags++;
	base = strrchr(token, '/');
	base = base ? base + 1 : token;
	if (!strcmp(base, "env")) {
		token = flags;
		while (*flags && !isspace(*flags)) flags++;
		if (*flags) *flags++ = '\0';
		while (isspace(*flags)) flags++;
		base = token;
	}
	if (!strcmp(base, "perl")) selected = "perl";
	else if (!strcmp(base, "sh") || !strcmp(base, "bash")) selected = base;
	else if (!strcmp(base, "wish") || !strcmp(base, "tclsh")) selected = base;
	if (!selected)
		return 0;
	native = xstrfmt("runtime/usr/bin/%s", selected);
	interpreter = system_path(native);
	free(native);
	if (access(interpreter, X_OK) || git_next_interpreter_environment(env)) {
		free(interpreter);
		return -1;
	}
	strvec_push(&command, args->v[0]);
	strvec_push(&command, interpreter);
	if (*flags)
		strvec_push(&command, flags);
	strvec_pushv(&command, args->v + 1);
	strvec_clear(args);
	*args = command;
	free(interpreter);
#else
	(void)args;
	(void)env;
#endif
	return 0;
}

static int chinese;
static int skip_preferences;
static const char *configured = "auto";
static const char *selection_source = "system";

static const char *selection(const char *value)
{
	if (!strcasecmp(value, "auto") || !strcasecmp(value, "system"))
		return "auto";
	if (!strcasecmp(value, "english") || !strcasecmp(value, "en") ||
	    !strcasecmp(value, "en-us") || !strcasecmp(value, "en_us"))
		return "english";
	if (!strcasecmp(value, "chinese") || !strcasecmp(value, "zh") ||
	    !strcasecmp(value, "zh-cn") || !strcasecmp(value, "zh_cn") ||
	    !strcasecmp(value, "zh-hans") || !strcmp(value, "中文"))
		return "chinese";
	die(_("unsupported language; use chinese, english or auto"));
}

static int locale_language(const char *value, size_t length)
{
	if (!length)
		return -1;
	if ((length == 1 && *value == 'C') ||
	    (length == 5 && !strncasecmp(value, "POSIX", 5)) ||
	    (length > 1 && value[0] == 'C' && value[1] == '.'))
		return 0;
	if (length >= 2 && (length == 2 || strchr("-_.@", value[2]))) {
		if (!strncasecmp(value, "zh", 2))
			return 1;
		if (!strncasecmp(value, "en", 2))
			return 0;
	}
	return -1;
}

static int system_language(void)
{
	static const char *keys[] = { "LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG" };
	size_t i;
	for (i = 0; i < ARRAY_SIZE(keys); i++) {
		const char *value = getenv(keys[i]);
		if (value && *value) {
			while (*value) {
				size_t length = strcspn(value, ":");
				int found = locale_language(value, length);
				if (found >= 0)
					return found;
				value += length;
				if (*value)
					value++;
			}
			return 0;
		}
	}
#ifdef GIT_WINDOWS_NATIVE
	{
		ULONG count = 0, length = 0;
		WCHAR *languages, *p;
		GetUserPreferredUILanguages(MUI_LANGUAGE_NAME, &count, NULL, &length);
		if (!length || length > 65536)
			return 0;
		languages = xcalloc(length, sizeof(WCHAR));
		if (GetUserPreferredUILanguages(MUI_LANGUAGE_NAME, &count, languages, &length)) {
			for (p = languages; *p; p += wcslen(p) + 1) {
				char text[256];
				int n = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, p, -1,
						   text, sizeof(text), NULL, NULL);
				int found = n > 0 ? locale_language(text, n - 1) : -1;
				if (found >= 0) {
					free(languages);
					return found;
				}
			}
		}
		free(languages);
	}
#elif defined(__APPLE__)
	{
		CFArrayRef languages = CFLocaleCopyPreferredLanguages();
		CFIndex i, count = languages ? CFArrayGetCount(languages) : 0;
		for (i = 0; i < count; i++) {
			char text[256];
			CFStringRef value = CFArrayGetValueAtIndex(languages, i);
			if (CFStringGetCString(value, text, sizeof(text), kCFStringEncodingUTF8)) {
				int found = locale_language(text, strlen(text));
				if (found >= 0) {
					CFRelease(languages);
					return found;
				}
			}
		}
		if (languages)
			CFRelease(languages);
	}
#endif
	return 0;
}

static char *settings_path(void)
{
	const char *value = getenv("GIT_NEXT_CONFIG_HOME");
	if (value && *value) {
		if (!is_absolute_path(value))
			die(_("GIT_NEXT_CONFIG_HOME must be an absolute directory"));
		return xstrfmt("%s/settings.json", value);
	}
	value = getenv("XDG_CONFIG_HOME");
	if (value && *value) {
		if (!is_absolute_path(value))
			die(_("language settings require an absolute user configuration directory"));
		return xstrfmt("%s/git-next/settings.json", value);
	}
	value = getenv("HOME");
	if (!value)
		value = getenv("USERPROFILE");
	if (!value)
		return NULL;
	if (!is_absolute_path(value))
		die(_("language settings require an absolute user configuration directory"));
	return xstrfmt("%s/.config/git-next/settings.json", value);
}

char *git_next_machine_path(const char *suffix)
{
#ifdef GIT_WINDOWS_NATIVE
	wchar_t wide[MAX_PATH];
	char utf8[MAX_PATH * 4];
	if (SHGetFolderPathW(NULL, CSIDL_COMMON_APPDATA, NULL, SHGFP_TYPE_CURRENT, wide) != S_OK ||
	    xwcstoutf(utf8, wide, sizeof(utf8)) < 0)
		die(_("could not resolve git-next machine state directory"));
	return xstrfmt("%s/git-next/%s", utf8, suffix);
#elif defined(__APPLE__)
	return xstrfmt("/Library/Application Support/git-next/%s", suffix);
#elif defined(__linux__)
	return xstrfmt("/var/lib/git-next/%s", suffix);
#else
	(void)suffix;
	return NULL;
#endif
}

static char *existing_migration_config(char *path)
{
	if (!path)
		return NULL;
	if (access(path, F_OK)) {
		if (errno != ENOENT && errno != ENOTDIR)
			die_errno(_("could not access migrated git-next system configuration"));
		free(path);
		return NULL;
	}
	return path;
}

char *git_next_migration_config(void)
{
	char *settings = settings_path(), *path = NULL;
	if (settings) {
		size_t length = strlen(settings) - strlen("settings.json");
		path = xstrfmt("%.*ssystem.gitconfig", (int)length, settings);
		free(settings);
		path = existing_migration_config(path);
	}
	return path ? path : existing_migration_config(git_next_machine_path("config/system.gitconfig"));
}

static yyjson_doc *read_settings(const char *path)
{
	int fd;
	ssize_t length;
	char buffer[65537];
	yyjson_doc *doc;
	yyjson_val *root, *schema, *language, *key, *value;
	size_t index, count;
	int schema_count = 0, language_count = 0;
	if (!path)
		return NULL;
	fd = git_open_cloexec(path, O_RDONLY);
	if (fd < 0) {
		if (errno == ENOENT)
			return NULL;
		die_errno(_("could not read git-next language settings"));
	}
	length = read_in_full(fd, buffer, sizeof(buffer));
	close(fd);
	if (length < 0)
		die_errno(_("could not read git-next language settings"));
	if (length > 65536)
		die(_("git-next language settings exceed the size limit"));
	/* Raw numbers retain all unknown future values, including integers above 2^64. */
	doc = yyjson_read(buffer, length, YYJSON_READ_NUMBER_AS_RAW);
	if (!doc)
		die(_("invalid git-next language settings; existing file was preserved"));
	root = yyjson_doc_get_root(doc);
	schema = yyjson_obj_get(root, "schema_version");
	language = yyjson_obj_get(root, "language");
	if (!yyjson_is_obj(root) || !yyjson_is_raw(schema) ||
	    strcmp(yyjson_get_raw(schema), "1") || !yyjson_is_str(language) ||
	    strlen(yyjson_get_str(language)) != yyjson_get_len(language))
		die(_("unsupported git-next settings schema"));
	yyjson_obj_foreach(root, index, count, key, value) {
		if (yyjson_equals_str(key, "schema_version"))
			schema_count++;
		if (yyjson_equals_str(key, "language"))
			language_count++;
	}
	if (schema_count != 1 || language_count != 1)
		die(_("duplicate git-next settings keys; existing file was preserved"));
	selection(yyjson_get_str(language));
	return doc;
}

static void activate(const char *value)
{
	configured = selection(value);
	chinese = !strcmp(configured, "auto") ? system_language() :
		!strcmp(configured, "chinese");
}

void git_next_language_override(const char *value)
{
	activate(value);
	selection_source = "command-line";
	setenv("GIT_NEXT_LANGUAGE", configured, 1);
}

void git_next_prepare(const char **argv)
{
	const char **arg = argv + 1;
	const char *value = NULL;
	while (*arg) {
		if (!strcmp(*arg, "--language")) {
			if (!*++arg)
				die(_("--language requires a value"));
			value = *arg;
		} else if (skip_prefix(*arg, "--language=", &value)) {
			/* Only global options are interpreted; command data stays untouched. */
		} else if (!strcmp(*arg, "-C") || !strcmp(*arg, "-c") ||
			   !strcmp(*arg, "--git-dir") || !strcmp(*arg, "--work-tree") ||
			   !strcmp(*arg, "--namespace") || !strcmp(*arg, "--config-env")) {
			if (!*++arg)
				break;
		} else if (!strcmp(*arg, "--version") || !strcmp(*arg, "-v") ||
			   !strcmp(*arg, "version")) {
			skip_preferences = 1;
			break;
		} else if ((*arg)[0] != '-' || !strcmp(*arg, "--")) {
			break;
		}
		arg++;
	}
	if (value) {
		git_next_language_override(value);
		return;
	}
	if (skip_preferences)
		return;
	value = getenv("GIT_NEXT_LANGUAGE");
	if (value) {
		activate(value);
		selection_source = "environment";
		return;
	}
	if (!skip_preferences) {
		char *path = settings_path();
		yyjson_doc *doc = read_settings(path);
		if (doc) {
			value = yyjson_get_str(yyjson_obj_get(yyjson_doc_get_root(doc), "language"));
			activate(value);
			selection_source = !strcmp(configured, "auto") ? "system" : "settings";
			yyjson_doc_free(doc);
		} else {
			activate("auto");
		}
		free(path);
	}
}

void git_next_setup_gettext(void)
{
	/* Selection is initialized before the ordinary Git gettext entrypoint. */
}

static const char *translated(const char *message)
{
	size_t first = 0, last = ARRAY_SIZE(git_next_catalog);
	while (first < last) {
		size_t middle = first + (last - first) / 2;
		int cmp = strcmp(message, git_next_catalog[middle].english);
		if (!cmp)
			return git_next_catalog[middle].chinese;
		if (cmp < 0)
			last = middle;
		else
			first = middle + 1;
	}
	/* Platform-dependent PRI macros cannot share the build-time sort order. */
	for (first = 0; first < ARRAY_SIZE(git_next_portable_catalog); first++)
		if (!strcmp(message, git_next_portable_catalog[first].english))
			return git_next_portable_catalog[first].chinese;
	return NULL;
}

const char *git_next_gettext(const char *message)
{
	const char *result = chinese && *message ? translated(message) : NULL;
	return result ? result : message;
}

int git_next_gettext_width(const char *message)
{
	return utf8_strwidth(message);
}

const char *git_next_preferred_languages(void)
{
	return chinese ? "zh_CN" : "en";
}

const char *git_next_ngettext(const char *singular, const char *plural,
			    unsigned long count)
{
	const char *result = chinese ? translated(singular) : NULL;
	return result ? result : count == 1 ? singular : plural;
}

static int language_command(int argc, const char **argv)
{
	if (argc == 2 && !strcmp(argv[1], "list")) {
		printf("english\tEnglish\nchinese\t简体中文\nauto\t%s\n",
		       chinese ? "跟随系统语言" : "Follow the system language");
		return 0;
	}
	if ((argc == 2 || (argc == 3 && !strcmp(argv[2], "--json"))) &&
	    !strcmp(argv[1], "get")) {
		if (argc == 2)
			puts(chinese ? "chinese" : "english");
		else
			printf("{\"schema_version\":1,\"language\":\"%s\",\"configured\":\"%s\","
			       "\"source\":\"%s\",\"supported\":[\"english\",\"chinese\",\"auto\"]}\n",
			       chinese ? "chinese" : "english", configured, selection_source);
		return 0;
	}
	if (argc == 3 && !strcmp(argv[1], "set")) {
		const char *saved = selection(argv[2]);
		char *path = settings_path(), *data;
		size_t length;
		yyjson_doc *previous;
		yyjson_mut_doc *next;
		yyjson_mut_val *root, *key, *value;
		struct lock_file lock = LOCK_INIT;
		int fd;
		if (!path)
			die(_("HOME/USERPROFILE is unset"));
		if (safe_create_leading_directories_const(NULL, path))
			die_errno(_("could not create git-next settings directory"));
		fd = hold_lock_file_for_update_mode(&lock, path, LOCK_DIE_ON_ERROR, 0600);
		/* Read after locking, so a concurrent preference writer cannot lose fields. */
		previous = read_settings(path);
		next = previous ? yyjson_doc_mut_copy(previous, NULL) : yyjson_mut_doc_new(NULL);
		if (!next)
			die(_("out of memory writing git-next settings"));
		if (previous)
			yyjson_doc_free(previous);
		root = yyjson_mut_doc_get_root(next);
		if (!root) {
			root = yyjson_mut_obj(next);
			yyjson_mut_doc_set_root(next, root);
			if (!yyjson_mut_obj_add_uint(next, root, "schema_version", 1))
				die(_("out of memory writing git-next settings"));
		}
		key = yyjson_mut_str(next, "language");
		value = yyjson_mut_str(next, saved);
		if (!key || !value || !yyjson_mut_obj_put(root, key, value))
			die(_("out of memory writing git-next settings"));
		data = yyjson_mut_write(next, YYJSON_WRITE_PRETTY | YYJSON_WRITE_NEWLINE_AT_END, &length);
		if (!data || length > 65536)
			die(_("git-next language settings exceed the size limit"));
		if (write_in_full(fd, data, length) != length || commit_lock_file(&lock))
			die_errno(_("could not save git-next language settings"));
		free(data);
		free(path);
		yyjson_mut_doc_free(next);
		activate(saved);
		puts(!strcmp(saved, "auto") ?
		     (chinese ? "语言已设置为跟随系统（auto）。" : "Language now follows the system (auto).") :
		     (chinese ? "语言已设置为中文（chinese）。" : "Language set to English (english)."));
		return 0;
	}
	printf("%s: git language get [--json] | set chinese|english|auto | list\n",
	       chinese ? "用法" : "usage");
	return argc == 2 && (!strcmp(argv[1], "--help") || !strcmp(argv[1], "-h")) ? 0 : 129;
}

int git_next_version_json(void)
{
	puts(GIT_NEXT_VERSION_JSON);
	return 0;
}

int cmd_git_next_language(int argc, const char **argv,
			 const char *prefix UNUSED, struct repository *repo UNUSED)
{
	return language_command(argc, argv);
}

int cmd_git_next_gettext(int argc, const char **argv,
			 const char *prefix UNUSED, struct repository *repo UNUSED)
{
	const char *message;
	if (argc == 3 && !strcmp(argv[1], "--"))
		message = git_next_gettext(argv[2]);
	else if (argc == 6 && !strcmp(argv[1], "--plural") && !strcmp(argv[3], "--")) {
		char *end;
		unsigned long count;
		if (!*argv[2] || *argv[2] == '-')
			return 129;
		errno = 0;
		count = strtoul(argv[2], &end, 10);
		if (errno || *end)
			return 129;
		message = git_next_ngettext(argv[4], argv[5], count);
	} else
		return 129;
	return fputs(message, stdout) == EOF ? 1 : 0;
}

int cmd_git_next_eol(int argc, const char **argv,
		    const char *prefix UNUSED, struct repository *repo UNUSED)
{
	return git_next_eol_command(argc, argv);
}

int cmd_git_next_report(int argc, const char **argv,
		       const char *prefix UNUSED, struct repository *repo UNUSED)
{
	int result = git_next_command(argc, argv);
	if (result < 0) {
		fprintf(stderr, "usage: git %s [--json]\n", argv[0]);
		return 129;
	}
	return result;
}

int cmd_git_next_update(int argc, const char **argv,
		       const char *prefix UNUSED, struct repository *repo UNUSED)
{
	struct child_process child = CHILD_PROCESS_INIT;
	const char *mode = "Check";
	int legacy = !strcmp(argv[0], "update-git-for-windows");
	int yes = 0, quiet = 0, gui = 0, json = 0;
	char *updater;
	int i, result;

	if (legacy)
		mode = "Legacy";
	for (i = 1; i < argc; i++) {
		if (!strcmp(argv[i], "--help") || !strcmp(argv[i], "-h")) {
			puts(legacy ? _("usage: git update-git-for-windows [-y|--yes] [-g|--gui] [--quiet]") :
			     _("usage: git update [check|apply|status|enable|disable] [--json]"));
			return legacy ? 1 : 0;
		}
		if (legacy) {
			if (!strcmp(argv[i], "--yes") || !strcmp(argv[i], "-y"))
				yes = 1;
			else if (!strcmp(argv[i], "--gui") || !strcmp(argv[i], "-g"))
				gui = 1;
			else if (!strcmp(argv[i], "--quiet"))
				quiet = 1;
			else
				return error(_("invalid update arguments; use git update --help"));
			continue;
		}
		if (!strcmp(argv[i], "--json")) {
			json = 1;
			continue;
		}
		if (i != 1)
			return error(_("invalid update arguments; use git update --help"));
		if (!strcmp(argv[i], "check"))
			mode = "Check";
		else if (!strcmp(argv[i], "apply"))
			mode = "RequestApply";
		else if (!strcmp(argv[i], "status"))
			mode = "Status";
		else if (!strcmp(argv[i], "enable"))
			mode = "Enable";
		else if (!strcmp(argv[i], "disable"))
			mode = "Disable";
		else
			return error(_("invalid update arguments; use git update --help"));
	}
#ifdef GIT_WINDOWS_NATIVE
	{
		WCHAR directory[MAX_PATH + 1];
		char utf8[3 * MAX_PATH + 1];
		UINT length = GetSystemDirectoryW(directory, ARRAY_SIZE(directory));
		if (!length || length >= ARRAY_SIZE(directory) ||
		    xwcstoutf(utf8, directory, ARRAY_SIZE(utf8)) < 0)
			return error(_("could not locate the operating system updater host"));
		strvec_pushf(&child.args, "%s/WindowsPowerShell/v1.0/powershell.exe", utf8);
	}
	updater = system_path("updater/update.ps1");
	strvec_pushl(&child.args, "-NoLogo", "-NoProfile", "-NonInteractive",
		     "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden",
		     "-File", updater, "-Mode", mode, NULL);
	if (json)
		strvec_push(&child.args, "-Json");
	if (legacy) {
		if (!gui && !yes) {
			/* Read-Host requires an interactive host for the legacy prompt. */
			for (i = 0; i < child.args.nr; i++)
				if (!strcmp(child.args.v[i], "-NonInteractive")) {
					strvec_remove(&child.args, i);
					break;
				}
		}
		if (yes)
			strvec_push(&child.args, "-Yes");
		if (quiet)
			strvec_push(&child.args, "-Quiet");
		if (gui)
			strvec_push(&child.args, "-Gui");
	}
#else
	updater = system_path("libexec/git-next/updater");
	strvec_pushl(&child.args, updater, mode, NULL);
	if (json)
		strvec_push(&child.args, "--json");
#endif
	if (access(updater, R_OK)) {
		free(updater);
		child_process_clear(&child);
		return error(_("this installation has no owned git-next updater"));
	}
	strvec_pushf(&child.env, "GIT_NEXT_LANGUAGE=%s", chinese ? "chinese" : "english");
	result = run_command(&child);
	free(updater);
	return result < 0 ? 1 : result;
}

int git_next_command(int argc, const char **argv)
{
	if (!strcmp(argv[0], "language"))
		return language_command(argc, argv);
	if (!strcmp(argv[0], "eol"))
		return git_next_eol_command(argc, argv);
	if ((!strcmp(argv[0], "compatibility") || !strcmp(argv[0], "doctor")) &&
	    (argc == 1 || (argc == 2 && !strcmp(argv[1], "--json")))) {
		if (argc == 2)
			puts(GIT_NEXT_COMPATIBILITY_JSON);
		else
			puts(chinese ? (GIT_NEXT_COMPLETE_GIT_PARITY ?
			     "git-next：完整 Git 兼容性已通过验收。" : "git-next：完整接管尚未通过验收。") :
			     (GIT_NEXT_COMPLETE_GIT_PARITY ? "git-next: complete Git compatibility is accepted." :
			      "git-next: complete replacement is not yet accepted."));
		return 0;
	}
	return -1;
}
