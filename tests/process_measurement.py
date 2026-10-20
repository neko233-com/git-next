"""Measure process latency and query Windows peak working set after the timed interval."""
import ctypes
import os
import signal
import subprocess
import time


def peak_working_set(process):
    if os.name != 'nt':
        return None
    from ctypes import wintypes

    class Counters(ctypes.Structure):
        _fields_ = [('cb', wintypes.DWORD), ('PageFaultCount', wintypes.DWORD),
                    *[(name, ctypes.c_size_t) for name in ('PeakWorkingSetSize', 'WorkingSetSize',
                      'QuotaPeakPagedPoolUsage', 'QuotaPagedPoolUsage', 'QuotaPeakNonPagedPoolUsage',
                      'QuotaNonPagedPoolUsage', 'PagefileUsage', 'PeakPagefileUsage')]]
    counters = Counters()
    counters.cb = ctypes.sizeof(counters)
    query = ctypes.WinDLL('kernel32', use_last_error=True).K32GetProcessMemoryInfo
    query.argtypes = [wintypes.HANDLE, ctypes.POINTER(Counters), wintypes.DWORD]
    query.restype = wintypes.BOOL
    if not query(int(process._handle), ctypes.byref(counters), counters.cb):
        return None
    return counters.PeakWorkingSetSize or None


def measured_run(command, *, cwd, env, data, timeout):
    started = time.perf_counter_ns()
    options = dict(creationflags=subprocess.CREATE_NEW_PROCESS_GROUP) if os.name == 'nt' else dict(start_new_session=True)
    with subprocess.Popen(command, cwd=cwd, env=env, stdin=subprocess.PIPE,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, **options) as process:
        try:
            stdout, stderr = process.communicate(input=data, timeout=timeout)
        except subprocess.TimeoutExpired:
            if os.name == 'nt':
                kernel = ctypes.WinDLL('kernel32', use_last_error=True)
                kernel.GetProcessId.argtypes = [ctypes.c_void_p]
                kernel.GetProcessId.restype = ctypes.c_ulong
                if kernel.GetProcessId(int(process._handle)) != process.pid:
                    raise RuntimeError('Timed process ownership could not be verified')
                killer = os.path.join(os.environ['SystemRoot'], 'System32', 'taskkill.exe')
                subprocess.run([killer, '/PID', str(process.pid), '/T', '/F'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
            else:
                try: os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError: pass
            if process.poll() is None: process.kill()
            process.communicate(timeout=5)
            raise
        elapsed = (time.perf_counter_ns()-started)/1_000_000
        peak = peak_working_set(process)
        return subprocess.CompletedProcess(command, process.returncode, stdout, stderr), elapsed, peak
