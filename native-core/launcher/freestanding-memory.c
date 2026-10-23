/* Copyright 2026 git-next contributors. SPDX-License-Identifier: MIT
 *
 * MSVC 14.44 still emits memcpy, memset, memmove and memcmp from a CRT-free
 * image compiled at full /O2. Passing /Ob1 hides those calls by turning off
 * the inlining the legacy forwarder relies on, so the helpers live here
 * instead. The bodies must not be rewritten into a call to themselves:
 * MSVC treats these names as intrinsics, and GCC can do the same under -O2.
 */
#include <stddef.h>

void *memcpy(void *destination, const void *source, size_t count);
void *memset(void *destination, int value, size_t count);
void *memmove(void *destination, const void *source, size_t count);
int memcmp(const void *left, const void *right, size_t count);

#if defined(_MSC_VER)
#pragma function(memcpy)
#pragma function(memset)
#pragma function(memmove)
#pragma function(memcmp)
#pragma optimize("", off)
#endif

#if defined(__clang__)
#define GN_NOOPT __attribute__((optnone))
#elif defined(__GNUC__)
#define GN_NOOPT __attribute__((optimize("O0"), noinline))
#else
#define GN_NOOPT
#endif

GN_NOOPT void *memcpy(void *destination, const void *source, size_t count)
{
	unsigned char *out = destination;
	const unsigned char *in = source;
	size_t index;

	for (index = 0; index < count; index++) {
		volatile unsigned char byte = in[index];
		out[index] = byte;
	}
	return destination;
}

GN_NOOPT void *memset(void *destination, int value, size_t count)
{
	unsigned char *out = destination;
	unsigned char byte = (unsigned char)value;
	size_t index;

	for (index = 0; index < count; index++) {
		volatile unsigned char stored = byte;
		out[index] = stored;
	}
	return destination;
}

GN_NOOPT void *memmove(void *destination, const void *source, size_t count)
{
	unsigned char *out = destination;
	const unsigned char *in = source;
	size_t index;

	if (out == in || count == 0)
		return destination;
	if (out < in) {
		for (index = 0; index < count; index++) {
			volatile unsigned char byte = in[index];
			out[index] = byte;
		}
	} else {
		index = count;
		while (index--) {
			volatile unsigned char byte = in[index];
			out[index] = byte;
		}
	}
	return destination;
}

GN_NOOPT int memcmp(const void *left, const void *right, size_t count)
{
	const unsigned char *a = left;
	const unsigned char *b = right;
	size_t index;

	for (index = 0; index < count; index++) {
		int first = a[index];
		int second = b[index];
		if (first != second)
			return first - second;
	}
	return 0;
}

#if defined(_MSC_VER)
#pragma optimize("", on)
#endif
