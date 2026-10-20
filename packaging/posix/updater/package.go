// Copyright 2026 git-next contributors. MIT license.
//go:build linux || darwin

package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var regexpRelease = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+-[a-f0-9]{16}$`)

type packageFile struct {
	Kind   string      `json:"kind"`
	SHA    string      `json:"sha256"`
	Bytes  int64       `json:"bytes"`
	Mode   os.FileMode `json:"mode"`
	Target string      `json:"target"`
}

type packageManifest struct {
	Schema   int                    `json:"schema_version"`
	Product  string                 `json:"product"`
	Engine   string                 `json:"engine"`
	Kind     string                 `json:"install_kind"`
	Version  string                 `json:"version"`
	Platform string                 `json:"platform"`
	Commit   string                 `json:"source_commit"`
	Tree     string                 `json:"source_tree_sha256"`
	Parity   bool                   `json:"complete_git_parity"`
	Eligible bool                   `json:"release_eligible"`
	Files    map[string]packageFile `json:"files"`
}

func memberName(value string) bool {
	return value != "" && fs.ValidPath(value) && value != "." && !strings.ContainsAny(value, "\\:\x00\r\n")
}

func linkTarget(name, target string) bool {
	return target != "" && !path.IsAbs(target) && !strings.ContainsAny(target, "\\:\x00\r\n") && memberName(path.Clean(path.Join(path.Dir(name), target)))
}

func loadPackage(directory string, verify bool) (packageManifest, error) {
	var pkg packageManifest
	root, err := os.OpenRoot(directory)
	if err != nil {
		return pkg, err
	}
	defer root.Close()
	if err := readJSON(root, "package.json", 32<<20, &pkg); err != nil {
		return pkg, err
	}
	_, preview, err := versionParts(pkg.Version)
	if err != nil || preview != "" || pkg.Schema != 1 || pkg.Product != "git-next" || pkg.Engine != "source-built-git-next-native-core" || pkg.Kind != "tar" || pkg.Platform != platformID() || !commitPattern.MatchString(pkg.Commit) || !digestPattern.MatchString(pkg.Tree) || !pkg.Parity || !pkg.Eligible || len(pkg.Files) < 100 || len(pkg.Files) > 75000 {
		return pkg, fault("invalid-package")
	}
	for name, item := range pkg.Files {
		if !memberName(name) || name == "package.json" || item.Mode&^0777 != 0 {
			return pkg, fault("invalid-package")
		}
		switch item.Kind {
		case "file":
			if !digestPattern.MatchString(item.SHA) || item.Bytes < 0 || item.Bytes > 1<<30 || item.Mode&0022 != 0 {
				return pkg, fault("invalid-package")
			}
		case "symlink":
			if !linkTarget(name, item.Target) {
				return pkg, fault("invalid-package")
			}
		default:
			return pkg, fault("invalid-package")
		}
		if verify {
			if err := verifyMember(root, name, item); err != nil {
				return pkg, err
			}
		}
	}
	for _, required := range []string{"bin/git", "libexec/git-next/updater", "runtime/usr/bin/ssh", "runtime/usr/bin/sh", "runtime/usr/bin/git-lfs", "runtime/usr/bin/git-credential-manager", "runtime/etc/gitconfig"} {
		if _, ok := pkg.Files[required]; !ok {
			return pkg, fault("invalid-package")
		}
	}
	if verify {
		err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || name == "package.json" || name == "git-next-distribution.json" {
				return nil
			}
			if _, ok := pkg.Files[name]; !ok {
				return fault("invalid-package")
			}
			return nil
		})
	}
	return pkg, err
}

func verifyMember(root *os.Root, name string, item packageFile) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if item.Kind == "symlink" {
		if info.Mode()&os.ModeSymlink == 0 {
			return fault("invalid-package")
		}
		target, err := root.Readlink(name)
		if err != nil || target != item.Target {
			return fault("invalid-package")
		}
		if _, err := root.Stat(name); err != nil {
			return fault("invalid-package")
		}
		return nil
	}
	if !info.Mode().IsRegular() || info.Size() != item.Bytes || info.Mode().Perm() != item.Mode {
		return fault("invalid-package")
	}
	file, err := root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, io.LimitReader(file, item.Bytes+1))
	if err != nil || size != item.Bytes || hex.EncodeToString(hash.Sum(nil)) != item.SHA {
		return fault("invalid-package")
	}
	return nil
}

func extractPackage(archive, destination, expectedRoot string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	compressed.Multistream(false)
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer root.Close()
	reader := tar.NewReader(io.LimitReader(compressed, 4<<30))
	seen := make(map[string]bool)
	links := make(map[string]string)
	var total int64
	for count := 0; ; count++ {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil || count > 100000 {
			return fault("invalid-package")
		}
		full := strings.TrimSuffix(header.Name, "/")
		if full == expectedRoot && header.Typeflag == tar.TypeDir {
			continue
		}
		if !strings.HasPrefix(full, expectedRoot+"/") {
			return fault("invalid-package")
		}
		name := strings.TrimPrefix(full, expectedRoot+"/")
		if !memberName(name) || seen[name] || header.Mode&^0777 != 0 || (header.Typeflag != tar.TypeSymlink && header.Mode&0022 != 0) {
			return fault("invalid-package")
		}
		seen[name] = true
		if err := root.MkdirAll(path.Dir(name), 0755); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			total += header.Size
			if header.Size < 0 || header.Size > 1<<30 || total > 4<<30 {
				return fault("invalid-package")
			}
			output, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(output, reader, header.Size)
			if copyErr == nil {
				copyErr = output.Chmod(os.FileMode(header.Mode))
			}
			if copyErr == nil {
				copyErr = output.Sync()
			}
			closeErr := output.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			if !linkTarget(name, header.Linkname) {
				return fault("invalid-package")
			}
			links[name] = header.Linkname
		default:
			return fault("invalid-package")
		}
	}
	// No extracted link can become a parent while regular files are being written.
	for name, target := range links {
		if err := root.Symlink(target, name); err != nil {
			return err
		}
	}
	for name := range links {
		if _, err := root.Stat(name); err != nil {
			return fault("invalid-package")
		}
	}
	if err := syncDir(root); err != nil {
		return err
	}
	return os.Chmod(destination, 0755)
}

func copyPackage(source, destination string) (packageManifest, error) {
	pkg, err := loadPackage(source, true)
	if err != nil {
		return pkg, err
	}
	if err := os.Mkdir(destination, 0755); err != nil {
		return pkg, err
	}
	input, err := os.OpenRoot(source)
	if err != nil {
		return pkg, err
	}
	defer input.Close()
	output, err := os.OpenRoot(destination)
	if err != nil {
		return pkg, err
	}
	defer output.Close()
	var names []string
	for name := range pkg.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range append(names, "package.json") {
		item := pkg.Files[name]
		if item.Kind == "symlink" {
			continue
		}
		if err := output.MkdirAll(path.Dir(name), 0755); err != nil {
			return pkg, err
		}
		in, err := input.Open(name)
		if err != nil {
			return pkg, err
		}
		mode := item.Mode
		if name == "package.json" {
			mode = 0644
		}
		out, err := output.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			_ = in.Close()
			return pkg, err
		}
		_, copyErr := io.Copy(out, in)
		_ = in.Close()
		if copyErr == nil {
			copyErr = out.Chmod(mode)
		}
		if copyErr == nil {
			copyErr = out.Sync()
		}
		closeErr := out.Close()
		if copyErr != nil {
			return pkg, copyErr
		}
		if closeErr != nil {
			return pkg, closeErr
		}
	}
	for _, name := range names {
		item := pkg.Files[name]
		if item.Kind == "symlink" {
			if err := output.MkdirAll(path.Dir(name), 0755); err != nil {
				return pkg, err
			}
			if err := output.Symlink(item.Target, name); err != nil {
				return pkg, err
			}
		}
	}
	if err := syncDir(output); err != nil {
		return pkg, err
	}
	return loadPackage(destination, true)
}

func packageIdentity(pkg packageManifest) string {
	value, _ := json.Marshal(pkg)
	hash := sha256.Sum256(value)
	return "releases/" + pkg.Version + "-" + hex.EncodeToString(hash[:8])
}

func packagePath(base, relative string) string {
	return filepath.Join(base, filepath.FromSlash(relative))
}
