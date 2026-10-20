/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#ifndef GIT_NEXT_ICONV_H
#define GIT_NEXT_ICONV_H
#include <stddef.h>
typedef void *iconv_t;
iconv_t git_next_iconv_open(const char *to, const char *from);
size_t git_next_iconv(iconv_t converter, char **input, size_t *input_left,
                      char **output, size_t *output_left);
int git_next_iconv_close(iconv_t converter);
#define iconv_open git_next_iconv_open
#define iconv git_next_iconv
#define iconv_close git_next_iconv_close
#endif
