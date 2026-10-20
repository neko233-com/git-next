// Copyright 2026 git-next contributors. MIT license.
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.IO;
using Microsoft.Win32.SafeHandles;

namespace GitNext {
    public static class ScopeNative {
        [StructLayout(LayoutKind.Sequential)]
        private struct SecurityAttributes {
            public uint Length;
            public IntPtr Descriptor;
            public int Inherit;
        }
        [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern bool ConvertStringSecurityDescriptorToSecurityDescriptorW(
            string text, uint revision, out IntPtr descriptor, out uint size);
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern bool CreateDirectoryW(string path, ref SecurityAttributes attributes);
        [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
        private static extern SafeFileHandle CreateFileW(string path, uint access, uint share,
            ref SecurityAttributes attributes, uint creation, uint flags, IntPtr template);
        [DllImport("kernel32.dll")]
        private static extern IntPtr LocalFree(IntPtr memory);

        public static void CreateProtectedDirectory(string path, string sddl) {
            IntPtr descriptor;
            uint size;
            if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl, 1, out descriptor, out size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            try {
                SecurityAttributes attributes = new SecurityAttributes();
                attributes.Length = (uint)Marshal.SizeOf(typeof(SecurityAttributes));
                attributes.Descriptor = descriptor;
                if (!CreateDirectoryW(path, ref attributes)) {
                    int error = Marshal.GetLastWin32Error();
                    if (error != 183) throw new Win32Exception(error);
                }
            } finally { LocalFree(descriptor); }
        }

        public static FileStream CreateProtectedFile(string path, string sddl) {
            IntPtr descriptor;
            uint size;
            if (!ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl, 1, out descriptor, out size))
                throw new Win32Exception(Marshal.GetLastWin32Error());
            try {
                SecurityAttributes attributes = new SecurityAttributes();
                attributes.Length = (uint)Marshal.SizeOf(typeof(SecurityAttributes));
                attributes.Descriptor = descriptor;
                SafeFileHandle handle = CreateFileW(path, 0x40000000, 0, ref attributes, 1, 128, IntPtr.Zero);
                if (handle.IsInvalid) {
                    int error = Marshal.GetLastWin32Error(); handle.Dispose();
                    throw new Win32Exception(error);
                }
                try { return new FileStream(handle, FileAccess.Write, 65536, false); }
                catch { handle.Dispose(); throw; }
            } finally { LocalFree(descriptor); }
        }
    }
}
