/* Copyright 2026 git-next contributors. SPDX-License-Identifier: MIT
 * Host proof that the CRT-free helpers are correct and do not call themselves.
 * A large struct return forces the compiler to emit memcpy; the process then
 * exits through a raw syscall so this file does not pull libc.
 */
#include <stddef.h>

struct block {
	unsigned char bytes[4096];
};

void *memcpy(void *destination, const void *source, size_t count);
void *memset(void *destination, int value, size_t count);
void *memmove(void *destination, const void *source, size_t count);
int memcmp(const void *left, const void *right, size_t count);

static struct block copy_block(struct block value)
{
	return value;
}

static int fail(int code)
{
	return code;
}

static int run(void)
{
	unsigned char source[32], dest[32], window[8];
	struct block block, copied;
	size_t index;

	memset(source, 0, sizeof source);
	memset(dest, 0, sizeof dest);
	if (memcpy(dest, source, 0) != dest)
		return fail(2);
	for (index = 0; index < sizeof source; index++)
		source[index] = (unsigned char)(index + 3);
	memcpy(dest, source, sizeof source);
	if (memcmp(dest, source, sizeof source) != 0)
		return fail(3);
	if (memcmp(dest, source, sizeof source - 1) != 0 || memcmp("a", "b", 1) >= 0 || memcmp("b", "a", 1) <= 0)
		return fail(4);

	memset(window, 0, sizeof window);
	window[0] = 'a'; window[1] = 'b'; window[2] = 'c'; window[3] = 'd';
	window[4] = 'e'; window[5] = 'f'; window[6] = 'g'; window[7] = 'h';
	memmove(window + 2, window, 4);
	if (memcmp(window, "ababcdgh", 8) != 0)
		return fail(5);
	window[0] = 'a'; window[1] = 'b'; window[2] = 'c'; window[3] = 'd';
	window[4] = 'e'; window[5] = 'f'; window[6] = 'g'; window[7] = 'h';
	memmove(window, window + 2, 4);
	if (memcmp(window, "cdefefgh", 8) != 0)
		return fail(6);

	memset(dest, 0x5a, sizeof dest);
	for (index = 0; index < sizeof dest; index++)
		if (dest[index] != 0x5a)
			return fail(7);
	source[0] = 1;
	memcpy(dest + 1, source, 1);
	if (dest[1] != 1)
		return fail(8);

	memset(&block, 0, sizeof block);
	block.bytes[0] = 7;
	block.bytes[4095] = 9;
	copied = copy_block(block);
	if (copied.bytes[0] != 7 || copied.bytes[4095] != 9 || copied.bytes[1] != 0)
		return fail(9);
	return 0;
}

#if defined(__linux__) && defined(__x86_64__)
/* The kernel enters the image with the stack 16-byte aligned. A normal
   function expects the 8-byte return slot a call would have pushed, so the
   entry has to realign before it calls anything that uses the C ABI. */
__attribute__((force_align_arg_pointer, noreturn)) void _start(void)
{
	long status = run();
	/* "a" is rax and "D" is rdi. A generic register constraint would leave
	   the exit syscall number in the wrong register. */
	__asm__ volatile("syscall" : : "a"(60L), "D"(status) : "rcx", "r11", "memory");
	__builtin_unreachable();
}
#endif
