/* Copyright 2026 git-next contributors. MIT license. */
#define WIN32_LEAN_AND_MEAN
#include <windows.h>

static WCHAR application[32768], directory[32768], command[32768];
static const WCHAR *modes[] = {
    L"Register", L"Unregister", L"RollbackRegistration", L"GuardRemoval",
    L"InitializeRuntimeConfiguration", L"RollbackRuntimeConfiguration", L"CommitRuntimeConfiguration", L"Scheduled"
};
static const WCHAR *requests[] = { L"Auto", L"Check", L"Apply", L"Enable", L"Disable" };
static WCHAR arguments[4][64];
static const WCHAR *selected_request = NULL;

static SIZE_T length(const WCHAR *value)
{
    SIZE_T size = 0;
    while (value[size]) size++;
    return size;
}

static BOOL space(WCHAR value)
{
    return value == L' ' || value == L'\t';
}

static BOOL append(WCHAR *target, SIZE_T *used, const WCHAR *value)
{
    SIZE_T size = length(value), index;
    if (*used + size >= 32767) return FALSE;
    for (index = 0; index < size; index++) target[(*used)++] = value[index];
    target[*used] = 0;
    return TRUE;
}

static BOOL equal(const WCHAR *first, const WCHAR *second)
{
    SIZE_T at = 0;
    while (first[at] && first[at] == second[at]) at++;
    return first[at] == second[at];
}

static const WCHAR *mode_argument(void)
{
    const WCHAR *text = GetCommandLineW(), *mode = NULL;
    SIZE_T index, count = 0;
    if (*text == L'"') {
        text++;
        while (*text && *text != L'"') text++;
        if (*text) text++;
    } else {
        while (*text && !space(*text)) text++;
    }
    while (*text) {
        SIZE_T used = 0;
        BOOL quoted;
        while (space(*text)) text++;
        if (!*text) break;
        if (count == 4) return NULL;
        quoted = *text == L'"';
        if (quoted) text++;
        while (*text && (quoted ? *text != L'"' : !space(*text))) {
            if (used == 63 || *text == L'"') return NULL;
            arguments[count][used++] = *text++;
        }
        arguments[count++][used] = 0;
        if (quoted) {
            if (*text != L'"') return NULL;
            text++;
            if (*text && !space(*text)) return NULL;
        }
    }
    if ((count != 2 && count != 4) || !equal(arguments[0], L"-Mode")) return NULL;
    for (index = 0; index < sizeof(modes) / sizeof(modes[0]); index++) {
        if (equal(arguments[1], modes[index])) mode = modes[index];
    }
    if (!mode) return NULL;
    if (count == 4) {
        if (!equal(mode, L"Scheduled") || !equal(arguments[2], L"-Request")) return NULL;
        for (index = 0; index < sizeof(requests) / sizeof(requests[0]); index++) {
            if (equal(arguments[3], requests[index])) selected_request = requests[index];
        }
        if (!arguments[3][0]) selected_request = requests[0];
        if (!selected_request) return NULL;
    } else if (equal(mode, L"Scheduled")) selected_request = requests[0];
    return mode;
}

void WINAPI worker_entry(void)
{
    const WCHAR *mode = mode_argument();
    DWORD size, status = ERROR_INVALID_PARAMETER, attempt;
    SIZE_T used = 0, index;
    static STARTUPINFOW startup;
    static PROCESS_INFORMATION process;
    if (!mode) ExitProcess(status);
    SetErrorMode(SEM_FAILCRITICALERRORS | SEM_NOGPFAULTERRORBOX);
    size = GetModuleFileNameW(NULL, directory, 32768);
    if (!size || size >= 32767) ExitProcess(ERROR_INSUFFICIENT_BUFFER);
    for (index = size; index && directory[index - 1] != L'\\'; index--) {}
    if (!index) ExitProcess(ERROR_BAD_PATHNAME);
    directory[index] = 0;
    size = GetSystemDirectoryW(application, 32768);
    if (!size || size >= 32767) ExitProcess(ERROR_INSUFFICIENT_BUFFER);
    used = size;
    if (!append(application, &used, L"\\WindowsPowerShell\\v1.0\\powershell.exe")) ExitProcess(ERROR_INSUFFICIENT_BUFFER);
    startup.cb = sizeof(startup);
    startup.dwFlags = STARTF_USESHOWWINDOW;
    startup.wShowWindow = SW_HIDE;
    for (attempt = 0; attempt < 2; attempt++) {
        used = 0;
        if (!append(command, &used, L"\"") || !append(command, &used, application) ||
            !append(command, &used, L"\" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File \"") ||
            !append(command, &used, directory) || !append(command, &used, L"update.ps1\" -Mode ") ||
            !append(command, &used, mode)) ExitProcess(ERROR_INSUFFICIENT_BUFFER);
        if (selected_request && (!append(command, &used, L" -Request ") || !append(command, &used, selected_request))) ExitProcess(ERROR_INSUFFICIENT_BUFFER);
        /* No console or terminal handoff is allowed inside Windows Installer. */
        if (!CreateProcessW(application, command, NULL, NULL, FALSE, CREATE_NO_WINDOW | CREATE_UNICODE_ENVIRONMENT,
                            NULL, directory, &startup, &process)) {
            status = GetLastError();
            if (status != ERROR_DLL_INIT_FAILED || attempt) break;
        } else {
            DWORD wait = WaitForSingleObject(process.hProcess, 300000);
            if (wait == WAIT_OBJECT_0) {
                if (!GetExitCodeProcess(process.hProcess, &status)) status = GetLastError();
            } else {
                status = wait == WAIT_TIMEOUT ? ERROR_TIMEOUT : GetLastError();
                TerminateProcess(process.hProcess, status);
                WaitForSingleObject(process.hProcess, 10000);
            }
            CloseHandle(process.hThread);
            CloseHandle(process.hProcess);
            if (status != 0xc0000142UL || attempt) break;
        }
        /* DLL startup failure precedes script execution; script failures are never retried. */
        Sleep(200);
    }
    ExitProcess(status);
}
