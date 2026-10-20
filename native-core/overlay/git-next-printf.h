/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#ifndef GIT_NEXT_PRINTF_H
#define GIT_NEXT_PRINTF_H
#if defined(_MSC_VER) && !defined(GIT_NEXT_PRINTF_IMPLEMENTATION)
int git_next_printf(const char *format, ...);
int git_next_fprintf(FILE *stream, const char *format, ...);
int git_next_vprintf(const char *format, va_list arguments);
int git_next_vfprintf(FILE *stream, const char *format, va_list arguments);
int git_next_snprintf(char *buffer, size_t length, const char *format, ...);
int git_next_vsnprintf(char *buffer, size_t length, const char *format, va_list arguments);
#undef printf
#undef fprintf
#undef vprintf
#undef vfprintf
#undef snprintf
#undef vsnprintf
#define printf git_next_printf
#define fprintf git_next_fprintf
#define vprintf git_next_vprintf
#define vfprintf git_next_vfprintf
#define snprintf git_next_snprintf
#define vsnprintf git_next_vsnprintf
#endif
#endif
