/* Copyright 2026 git-next contributors. SPDX-License-Identifier: MIT */
#define WIN32_LEAN_AND_MEAN
#include <windows.h>

/* Protected legacy entries dispatch through the caller's own MSI registration. */
static WCHAR self[32768], key[32832], platform[32], root[32768], relative[32768];
static WCHAR target[32768], command[32768], base[32768];
static STARTUPINFOW startup;
static PROCESS_INFORMATION child;

static SIZE_T length(const WCHAR *text)
{
	SIZE_T size = 0;
	while (text[size])
		size++;
	return size;
}

static int append(WCHAR *buffer, SIZE_T capacity, const WCHAR *text)
{
	SIZE_T used = length(buffer), extra = length(text), i;
	if (used + extra >= capacity)
		return 0;
	for (i = 0; i <= extra; i++)
		buffer[used + i] = text[i];
	return 1;
}

static int equal(const WCHAR *a, const WCHAR *b)
{
	while (*a && *a == *b) {
		a++;
		b++;
	}
	return *a == *b;
}

/* One separator between components and no trailing separator: two spellings of the same
   path have to reach the same text before anything compares them. */
static void normalize(WCHAR *path)
{
	WCHAR *read = path, *write = path;
	while (*read) {
		if (*read == L'\\' || *read == L'/') {
			*write++ = L'\\';
			while (*read == L'\\' || *read == L'/')
				read++;
			continue;
		}
		*write++ = *read++;
	}
	*write = 0;
	if (write > path + 3 && write[-1] == L'\\')
		write[-1] = 0;
}

/* The identity is copied field by field on purpose: a struct assignment makes the
   compiler emit a memcpy call, and this binary links without a C runtime. */
static int identify(const WCHAR *path, DWORD *volume, DWORD *high, DWORD *low)
{
	HANDLE file;
	BY_HANDLE_FILE_INFORMATION info;
	file = CreateFileW(path, 0, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
			   NULL, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS, NULL);
	if (file == INVALID_HANDLE_VALUE)
		return 0;
	if (!GetFileInformationByHandle(file, &info)) {
		CloseHandle(file);
		return 0;
	}
	CloseHandle(file);
	*volume = info.dwVolumeSerialNumber;
	*high = info.nFileIndexHigh;
	*low = info.nFileIndexLow;
	return 1;
}

/* Whether two paths name one file. Text equality misses a repeated separator, a short
   name and a hard link, and every one of those makes a forwarder start itself. */
static int same_file(const WCHAR *a, const WCHAR *b)
{
	DWORD av, ah, al, bv, bh, bl;
	if (lstrcmpiW(a, b) == 0)
		return 1;
	if (!identify(a, &av, &ah, &al) || !identify(b, &bv, &bh, &bl))
		return 0;
	return av == bv && ah == bh && al == bl;
}

/* How many forwarders have already run on this command line. A chain that exceeds the
   limit is a cycle: forwarding is only ever meant to happen once. */
#define DEPTH_VARIABLE L"GIT_NEXT_BRIDGE_DEPTH"
#define DEPTH_LIMIT 4

/* A path at or above this length needs the extended-length marker to reach the file. */
#define MAX_PATH_WITHOUT_MARKER 260

static int depth(void)
{
	WCHAR value[16], *cursor;
	int result = 0;
	if (!GetEnvironmentVariableW(DEPTH_VARIABLE, value, 16))
		return 0;
	for (cursor = value; *cursor >= L'0' && *cursor <= L'9'; cursor++)
		result = result * 10 + (*cursor - L'0');
	return result;
}

static void deepen(void)
{
	WCHAR value[16];
	int number = depth() + 1, digits = 0, index;
	do {
		value[digits++] = (WCHAR)(L'0' + number % 10);
		number /= 10;
	} while (number && digits < 15);
	for (index = 0; index < digits / 2; index++) {
		WCHAR swap = value[index];
		value[index] = value[digits - 1 - index];
		value[digits - 1 - index] = swap;
	}
	value[digits] = 0;
	SetEnvironmentVariableW(DEPTH_VARIABLE, value);
}

static int registry(HKEY hive, const WCHAR *path, const WCHAR *name,
		    WCHAR *buffer, DWORD characters)
{
	HKEY handle;
	DWORD bytes = characters * sizeof(WCHAR), kind;
	LSTATUS status;
	if (RegOpenKeyExW(hive, path, 0, KEY_READ | KEY_WOW64_64KEY, &handle))
		return 0;
	status = RegQueryValueExW(handle, name, NULL, &kind, (BYTE *)buffer, &bytes);
	RegCloseKey(handle);
	return !status && kind == REG_SZ && bytes >= sizeof(WCHAR) &&
		bytes <= characters * sizeof(WCHAR) && !(bytes % sizeof(WCHAR)) &&
		buffer[bytes / sizeof(WCHAR) - 1] == 0 &&
		length(buffer) + 1 == bytes / sizeof(WCHAR);
}

__declspec(noreturn) static void fail(void)
{
	static const WCHAR message[] = L"git-next: legacy command migration is unavailable. Repair the git-next MSI installation.\r\n"
		L"git-next：旧命令迁移入口不可用，请修复 git-next MSI 安装。\r\n";
#ifdef GIT_NEXT_GUI_BRIDGE
	MessageBoxW(NULL, message, L"git-next", MB_OK | MB_ICONERROR);
#else
	HANDLE output = GetStdHandle(STD_ERROR_HANDLE);
	DWORD mode, written;
	if (GetConsoleMode(output, &mode))
		WriteConsoleW(output, message, (DWORD)length(message), &written, NULL);
	else {
		char utf8[1024];
		int count = WideCharToMultiByte(CP_UTF8, 0, message, -1, utf8, sizeof(utf8), NULL, NULL);
		if (count > 0)
			WriteFile(output, utf8, count - 1, &written, NULL);
	}
#endif
	ExitProcess(127);
}

/* Refusing here is the whole point: the alternative is that this process starts a copy
   of itself, and the copy does the same, until the machine runs out of memory. */
__declspec(noreturn) static void fail_loop(void)
{
	static const WCHAR message[] = L"git-next: this entry point forwards to itself. Stop it and run the git-next repair tool.\r\n"
		L"git-next：该入口转发到自身，已拒绝执行。请停止并运行 git-next 修复工具。\r\n";
#ifdef GIT_NEXT_GUI_BRIDGE
	MessageBoxW(NULL, message, L"git-next", MB_OK | MB_ICONERROR);
#else
	HANDLE output = GetStdHandle(STD_ERROR_HANDLE);
	DWORD mode, written;
	if (GetConsoleMode(output, &mode))
		WriteConsoleW(output, message, (DWORD)length(message), &written, NULL);
	else {
		char utf8[1024];
		int count = WideCharToMultiByte(CP_UTF8, 0, message, -1, utf8, sizeof(utf8), NULL, NULL);
		if (count > 0)
			WriteFile(output, utf8, count - 1, &written, NULL);
	}
#endif
	ExitProcess(127);
}

static BOOL WINAPI console_control(DWORD event)
{
	/* The child receives the shared console event and supplies the exit status. */
	return event == CTRL_C_EVENT || event == CTRL_BREAK_EVENT;
}

void bridge_entry(void)
{
	const WCHAR *tail;
	WCHAR *cursor;
	DWORD size, exit_code;
	HKEY registration;
	int quoted = 0;
	size = GetModuleFileNameW(NULL, self, 32768);
	if (!size || size >= 32768)
		fail();
	size = GetLongPathNameW(self, key, 32768);
	if (!size || size >= 32768)
		fail();
	self[0] = 0;
	if (!append(self, 32768, key))
		fail();
	normalize(self);
	key[0] = 0;
	if (!append(key, 32832, L"Software\\git-next\\Bridges\\") ||
	    !append(key, 32832, self))
		fail();
	if (registry(HKEY_CURRENT_USER, key, L"Platform", platform, 32) &&
	    registry(HKEY_CURRENT_USER, key, L"RelativePath", relative, 32768))
		registration = HKEY_CURRENT_USER;
	else if (registry(HKEY_LOCAL_MACHINE, key, L"Platform", platform, 32) &&
		 registry(HKEY_LOCAL_MACHINE, key, L"RelativePath", relative, 32768))
		registration = HKEY_LOCAL_MACHINE;
	else
		fail();
	if (!equal(platform, L"windows-x64") && !equal(platform, L"windows-arm64"))
		fail();
	key[0] = 0;
	if (!append(key, 32832, L"Software\\git-next\\MSI\\") ||
	    !append(key, 32832, platform) ||
	    !registry(registration, key, L"InstallRoot", root, 32768))
		fail();
	/* Reject UNC/device roots and traversal even if registration was corrupted. */
	if (!((root[0] >= L'A' && root[0] <= L'Z') || (root[0] >= L'a' && root[0] <= L'z')) ||
	    root[1] != L':' || (root[2] != L'\\' && root[2] != L'/') ||
	    !relative[0] || relative[0] == L'\\' || relative[0] == L'/')
		fail();
	for (cursor = relative; *cursor; cursor++)
		if (*cursor == L':' || *cursor == L'"' || *cursor < L' ' ||
		    (*cursor == L'.' && (cursor == relative || cursor[-1] == L'\\' || cursor[-1] == L'/')))
			fail();
	if (!append(target, 32768, root) || !append(target, 32768, L"\\") ||
	    !append(target, 32768, relative))
		fail();
	/* The recorded root may end with a separator, so the built path can repeat one. The
	   string comparison that used to guard this step therefore saw two different strings
	   for one file and let the forwarder start itself. */
	normalize(target);
	if (same_file(target, self) || depth() >= DEPTH_LIMIT)
		fail_loop();
	/* Beyond MAX_PATH the Win32 layer stops interpreting the path unless it carries the
	   extended-length marker, and the marker cannot be added before normalize() because
	   that would collapse its own separator pair. The path is therefore rebuilt. */
	if (length(target) >= MAX_PATH_WITHOUT_MARKER) {
		/* An extended-length path is not normalised, so a recorded root that ends with a
		   separator would leave a doubled one and the file would not be found. */
		base[0] = 0;
		if (!append(base, 32768, root))
			fail();
		normalize(base);
		target[0] = 0;
		if (!append(target, 32768, L"\\\\?\\") ||
		    !append(target, 32768, base) || !append(target, 32768, L"\\") ||
		    !append(target, 32768, relative))
			fail();
	}
	size = GetFileAttributesW(target);
	if (size == INVALID_FILE_ATTRIBUTES || size & (FILE_ATTRIBUTE_DIRECTORY | FILE_ATTRIBUTE_REPARSE_POINT))
		fail();
	tail = GetCommandLineW();
	while (*tail == L' ' || *tail == L'\t')
		tail++;
	while (*tail) {
		if (*tail == L'"')
			quoted = !quoted;
		else if (!quoted && (*tail == L' ' || *tail == L'\t'))
			break;
		tail++;
	}
	if (!append(command, 32768, L"\"") || !append(command, 32768, target) ||
	    !append(command, 32768, L"\"") || !append(command, 32768, tail))
		fail();
	GetStartupInfoW(&startup);
	startup.cb = sizeof(startup);
	startup.dwFlags |= STARTF_USESTDHANDLES;
	startup.hStdInput = GetStdHandle(STD_INPUT_HANDLE);
	startup.hStdOutput = GetStdHandle(STD_OUTPUT_HANDLE);
	startup.hStdError = GetStdHandle(STD_ERROR_HANDLE);
	SetConsoleCtrlHandler(console_control, TRUE);
	/* The child inherits this environment block, so a second forwarder sees that it is
	   already the second one even when two different files name each other. */
	deepen();
	if (!CreateProcessW(target, command, NULL, NULL, TRUE, 0, NULL, NULL, &startup, &child))
		fail();
	CloseHandle(child.hThread);
	if (WaitForSingleObject(child.hProcess, INFINITE) != WAIT_OBJECT_0 ||
	    !GetExitCodeProcess(child.hProcess, &exit_code))
		fail();
	CloseHandle(child.hProcess);
	ExitProcess(exit_code);
}
