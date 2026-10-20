/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#include "git-compat-util.h"

#ifdef GIT_WINDOWS_NATIVE
#include "git-next-iconv.h"

__declspec(dllimport) iconv_t libiconv_open(const char *to, const char *from);
__declspec(dllimport) size_t libiconv(iconv_t converter, char **input,
				   size_t *input_left, char **output, size_t *output_left);
__declspec(dllimport) int libiconv_close(iconv_t converter);

static INIT_ONCE errno_once = INIT_ONCE_STATIC_INIT;
static int *(__cdecl *library_errno)(void);

static BOOL CALLBACK find_library_errno(PINIT_ONCE once UNUSED,
				       PVOID argument UNUSED, PVOID *context UNUSED)
{
	HMODULE library = GetModuleHandleW(L"msvcrt.dll");
	union { FARPROC generic; int *(__cdecl *getter)(void); } address;
	address.generic = library ? GetProcAddress(library, "_errno") : NULL;
	library_errno = address.getter;
	return TRUE;
}

static void copy_library_errno(void)
{
	/* The pinned GNU DLL uses MSVCRT; the native core may use UCRT. */
	InitOnceExecuteOnce(&errno_once, find_library_errno, NULL, NULL);
	errno = library_errno ? *library_errno() : EIO;
}

iconv_t git_next_iconv_open(const char *to, const char *from)
{
	iconv_t result = libiconv_open(to, from);
	if (result == (iconv_t)-1)
		copy_library_errno();
	return result;
}

size_t git_next_iconv(iconv_t converter, char **input, size_t *input_left,
		      char **output, size_t *output_left)
{
	size_t result = libiconv(converter, input, input_left, output, output_left);
	if (result == (size_t)-1)
		copy_library_errno();
	return result;
}

int git_next_iconv_close(iconv_t converter)
{
	int result = libiconv_close(converter);
	if (result < 0)
		copy_library_errno();
	return result;
}
#endif
