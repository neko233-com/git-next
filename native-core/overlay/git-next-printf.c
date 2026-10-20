/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#define GIT_NEXT_PRINTF_IMPLEMENTATION
#include "git-compat-util.h"

#ifdef _MSC_VER
static int positional(const char *format)
{
	const char *p;
	for (p = format; *p; p++) {
		if (*p != '%')
			continue;
		if (p[1] == '%') {
			p++;
			continue;
		}
		if (p[1] >= '1' && p[1] <= '9') {
			const char *n = p + 2;
			while (*n >= '0' && *n <= '9')
				n++;
			if (*n == '$')
				return 1;
		}
	}
	return 0;
}

int git_next_vfprintf(FILE *stream, const char *format, va_list arguments)
{
	return positional(format) ? _vfprintf_p(stream, format, arguments) :
		vfprintf(stream, format, arguments);
}

int git_next_vprintf(const char *format, va_list arguments)
{
	return git_next_vfprintf(stdout, format, arguments);
}

int git_next_printf(const char *format, ...)
{
	int result;
	va_list arguments;
	va_start(arguments, format);
	result = git_next_vfprintf(stdout, format, arguments);
	va_end(arguments);
	return result;
}

int git_next_fprintf(FILE *stream, const char *format, ...)
{
	int result;
	va_list arguments;
	va_start(arguments, format);
	result = git_next_vfprintf(stream, format, arguments);
	va_end(arguments);
	return result;
}

int git_next_vsnprintf(char *buffer, size_t length, const char *format,
		      va_list arguments)
{
	int result;
	va_list copy;
	if (!positional(format))
		return vsnprintf(buffer, length, format, arguments);
	va_copy(copy, arguments);
	result = _vscprintf_p(format, copy);
	va_end(copy);
	if (result < 0 || !length)
		return result;
	/* The positional CRT truncation API does not have C99 return semantics. */
	if ((size_t)result < length) {
		_vsprintf_p(buffer, length, format, arguments);
	} else {
		char *complete = xmalloc(st_add((size_t)result, 1));
		_vsprintf_p(complete, (size_t)result + 1, format, arguments);
		memcpy(buffer, complete, length - 1);
		buffer[length - 1] = '\0';
		free(complete);
	}
	return result;
}

int git_next_snprintf(char *buffer, size_t length, const char *format, ...)
{
	int result;
	va_list arguments;
	va_start(arguments, format);
	result = git_next_vsnprintf(buffer, length, format, arguments);
	va_end(arguments);
	return result;
}
#endif
