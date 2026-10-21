/* Copyright 2026 git-next contributors. SPDX-License-Identifier: GPL-2.0-only */
#ifndef GIT_NEXT_READ_THREADS_H
#define GIT_NEXT_READ_THREADS_H
#include "thread-utils.h"
#ifdef __linux__
#include <sched.h>
#endif

static inline int git_next_read_thread_budget(void)
{
	int cpus = online_cpus();
#ifdef GIT_WINDOWS_NATIVE
	DWORD_PTR process_mask, system_mask;
	if (GetProcessAffinityMask(GetCurrentProcess(), &process_mask, &system_mask) && process_mask) {
		cpus = 0;
		while (process_mask) {
			cpus += process_mask & 1;
			process_mask >>= 1;
		}
	}
#elif defined(__linux__)
	cpu_set_t affinity;
	if (!sched_getaffinity(0, sizeof(affinity), &affinity)) {
		int available = CPU_COUNT(&affinity);
		if (available > 0)
			cpus = available;
	}
#endif
	if (cpus < 1)
		cpus = 1;
	return cpus > INT_MAX / 2 ? INT_MAX : cpus * 2;
}
#endif
