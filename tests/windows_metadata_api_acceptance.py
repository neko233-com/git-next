"""Compare directory-only Win32 metadata with the prior query and live NTFS metadata."""
import ctypes
from ctypes import wintypes as w
import hashlib
import json
import os
from pathlib import Path
import tempfile


def main():
    if os.name != "nt":
        raise SystemExit("Windows-only API contract fixture; run product fixtures separately")
    import _winapi

    common = [("NextEntryOffset", w.DWORD), ("FileIndex", w.DWORD),
              *[(name, ctypes.c_int64) for name in ("CreationTime", "LastAccessTime", "LastWriteTime", "ChangeTime", "EndOfFile", "AllocationSize")],
              ("FileAttributes", w.DWORD), ("FileNameLength", w.DWORD), ("EaSize", w.DWORD)]

    class Full(ctypes.Structure):
        _fields_ = [*common, ("FileName", w.WCHAR * 1)]

    class Both(ctypes.Structure):
        _fields_ = [*common, ("ShortNameLength", ctypes.c_byte), ("ShortName", w.WCHAR * 12),
                    ("FileId", ctypes.c_int64), ("FileName", w.WCHAR * 1)]

    assert Full.FileName.offset == 68 and Both.FileName.offset == 104
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.CreateFileW.argtypes = [w.LPCWSTR, w.DWORD, w.DWORD, ctypes.c_void_p, w.DWORD, w.DWORD, w.HANDLE]
    kernel.CreateFileW.restype = w.HANDLE
    kernel.GetFileInformationByHandleEx.argtypes = [w.HANDLE, ctypes.c_int, ctypes.c_void_p, w.DWORD]
    kernel.GetFileInformationByHandleEx.restype = w.BOOL
    kernel.CloseHandle.argtypes = [w.HANDLE]
    kernel.CloseHandle.restype = w.BOOL

    def enumerate_metadata(directory, information_class, shape):
        handle = kernel.CreateFileW(str(directory), 0x00100001, 7, None, 3, 0x02000000, None)
        if handle == ctypes.c_void_p(-1).value:
            raise ctypes.WinError(ctypes.get_last_error())
        entries = {}
        buffer = (ctypes.c_uint64 * 8192)()
        try:
            while kernel.GetFileInformationByHandleEx(handle, information_class, buffer, ctypes.sizeof(buffer)):
                offset = 0
                while True:
                    assert offset + ctypes.sizeof(shape) <= ctypes.sizeof(buffer)
                    address = ctypes.addressof(buffer) + offset
                    info = shape.from_address(address)
                    assert info.FileNameLength % 2 == 0
                    assert offset + shape.FileName.offset + info.FileNameLength <= ctypes.sizeof(buffer)
                    name = ctypes.wstring_at(address + shape.FileName.offset, info.FileNameLength // 2)
                    if name not in (".", ".."):
                        entries[name] = {key: getattr(info, key) for key in
                                         ("CreationTime", "LastWriteTime", "EndOfFile", "FileAttributes", "EaSize")}
                    if not info.NextEntryOffset:
                        break
                    offset += info.NextEntryOffset
            assert ctypes.get_last_error() == 18, ctypes.get_last_error()
        finally:
            assert kernel.CloseHandle(handle)
        return entries

    with tempfile.TemporaryDirectory(prefix="git-next-metadata-api-") as temporary:
        root = Path(temporary).resolve()
        (root / "plain.txt").write_bytes(b"content\n")
        (root / "empty.txt").write_bytes(b"")
        (root / "nämé-🚀.txt").write_bytes(b"unicode\n")
        (root / "target").mkdir()
        (root / "target/nested.txt").write_bytes(b"nested\n")
        _winapi.CreateJunction(str(root / "target"), str(root / "junction"))
        assert root in (root / "junction").resolve().parents
        os.link(root / "plain.txt", root / "hardlink.txt")
        symlink_tested = False
        try:
            os.symlink(root / "plain.txt", root / "symlink.txt")
            symlink_tested = True
        except OSError as error:
            if error.winerror != 1314:
                raise
        full = enumerate_metadata(root, 14, Full)
        both = enumerate_metadata(root, 10, Both)
        assert full == both, "required metadata differs between directory-only and MFT queries"
        for name, values in full.items():
            live = (root / name).lstat()
            assert values["FileAttributes"] == live.st_file_attributes, name
            assert values["EaSize"] == live.st_reparse_tag, name
            assert (values["CreationTime"] - 116444736000000000) * 100 == live.st_birthtime_ns, name
            assert (values["LastWriteTime"] - 116444736000000000) * 100 == live.st_mtime_ns, name
            if not live.st_reparse_tag:
                assert values["EndOfFile"] == live.st_size, name
        assert full["junction"]["EaSize"] == 0xA0000003
        print(json.dumps({"status": "pass", "scope": "Windows filesystem API metadata equivalence only; product conformance is separate",
                          "harness_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
                          "entries_compared": len(full), "full_matches_prior_query_and_live_metadata": True,
                          "unicode_empty_regular_directory_hardlink": True, "actual_junction_reparse_tag": True,
                          "actual_symlink_tested": symlink_tested, "user_files_modified": False}, indent=2))


if __name__ == "__main__":
    main()
