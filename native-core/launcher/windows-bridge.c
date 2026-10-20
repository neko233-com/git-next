/* Copyright 2026 git-next contributors. SPDX-License-Identifier: MIT */
#define WIN32_LEAN_AND_MEAN
#include <windows.h>

/* Protected legacy entries dispatch through the caller's own MSI registration. */
static WCHAR self[32768], key[32832], platform[32], root[32768], relative[32768];
static WCHAR target[32768], command[32768];
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
	    !append(target, 32768, relative) || lstrcmpiW(target, self) == 0)
		fail();
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
	if (!CreateProcessW(target, command, NULL, NULL, TRUE, 0, NULL, NULL, &startup, &child))
		fail();
	CloseHandle(child.hThread);
	if (WaitForSingleObject(child.hProcess, INFINITE) != WAIT_OBJECT_0 ||
	    !GetExitCodeProcess(child.hProcess, &exit_code))
		fail();
	CloseHandle(child.hProcess);
	ExitProcess(exit_code);
}
