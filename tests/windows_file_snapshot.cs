// Copyright 2026 git-next contributors. MIT license.
using System;
using System.Collections.Generic;
using System.Collections.Concurrent;
using System.IO;
using System.Security.Cryptography;
using System.Threading.Tasks;

namespace GitNext.Acceptance {
    public static class Files {
        public static SortedDictionary<string,string> Snapshot(string path, Dictionary<string,string> expected) {
            string root = Path.GetFullPath(path).TrimEnd('\\');
            var pending = new Stack<string>();
            var files = new List<string>();
            pending.Push(root);
            while (pending.Count > 0) {
                string directory = pending.Pop();
                if ((File.GetAttributes(directory) & FileAttributes.ReparsePoint) != 0)
                    throw new IOException("Snapshot directory is a reparse point");
                foreach (string child in Directory.GetDirectories(directory)) pending.Push(child);
                foreach (string file in Directory.GetFiles(directory)) {
                    if ((File.GetAttributes(file) & FileAttributes.ReparsePoint) != 0)
                        throw new IOException("Snapshot file is a reparse point");
                    files.Add(file);
                    if (files.Count > 60000) throw new IOException("Snapshot file limit exceeded");
                }
            }
            var hashes = new ConcurrentDictionary<string,string>(StringComparer.Ordinal);
            Parallel.ForEach(files, new ParallelOptions { MaxDegreeOfParallelism = 8 }, delegate(string file) {
                using (var algorithm = SHA256.Create())
                using (var stream = new FileStream(file, FileMode.Open, FileAccess.Read, FileShare.Read)) {
                    hashes[file.Substring(root.Length + 1).Replace('\\','/')] =
                        BitConverter.ToString(algorithm.ComputeHash(stream)).Replace("-", "").ToLowerInvariant();
                }
            });
            foreach (var entry in expected) {
                string actual;
                if (!hashes.TryGetValue(entry.Key, out actual) || !String.Equals(entry.Value, actual, StringComparison.Ordinal))
                    throw new IOException("Installed manifest checksum mismatch: " + entry.Key);
            }
            return new SortedDictionary<string,string>(hashes, StringComparer.Ordinal);
        }
    }
}
