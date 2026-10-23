// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"github.com/ulikunitz/xz"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func downloadVariable(raw, expected, target string, maximum int64) (string, error) {
	if len(expected) != 64 {
		return "", fmt.Errorf("invalid SHA-256 pin")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil {
		return "", fmt.Errorf("pinned download requires HTTPS without credentials")
	}
	request, e := http.NewRequest("GET", raw, nil)
	if e != nil {
		return "", e
	}
	request.Header.Set("User-Agent", "git-next-build-inputs")
	response, e := (&http.Client{Timeout: 10 * time.Minute}).Do(request)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Request.URL.Scheme != "https" {
		return "", fmt.Errorf("invalid asset response")
	}
	if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
		return "", e
	}
	file, e := os.CreateTemp(filepath.Dir(target), ".asset-")
	if e != nil {
		return "", e
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	n, copyErr := io.Copy(file, io.LimitReader(response.Body, maximum+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > maximum {
		return "", fmt.Errorf("download exceeds size limit")
	}
	hash, e := digest(temporary)
	if e != nil {
		return "", e
	}
	if hash != expected {
		return "", fmt.Errorf("download checksum mismatch")
	}
	if e = os.Rename(temporary, target); e != nil {
		return "", e
	}
	return target, nil
}

type archiveMember struct {
	name, link                   string
	mode                         os.FileMode
	size                         int64
	directory, symlink, hardlink bool
	modified                     time.Time
}

func eachArchive(name string, visit func(archiveMember, io.Reader) error) error {
	file, e := os.Open(name)
	if e != nil {
		return e
	}
	defer file.Close()
	header := make([]byte, 4)
	if _, e = io.ReadFull(file, header); e != nil {
		return e
	}
	if _, e = file.Seek(0, 0); e != nil {
		return e
	}
	if string(header[:2]) == "PK" {
		z, e := zip.OpenReader(name)
		if e != nil {
			return e
		}
		defer z.Close()
		for _, item := range z.File {
			r, e := item.Open()
			if e != nil {
				return e
			}
			member := archiveMember{name: item.Name, mode: item.Mode(), size: int64(item.UncompressedSize64), directory: item.FileInfo().IsDir(), symlink: item.Mode()&os.ModeSymlink != 0, modified: item.Modified}
			if !member.directory && !member.symlink && member.mode.Perm() == 0 {
				member.mode = 0644
			}
			if member.symlink {
				b, e := io.ReadAll(io.LimitReader(r, 65537))
				r.Close()
				if e != nil {
					return e
				}
				if len(b) > 65536 {
					return fmt.Errorf("oversized ZIP link")
				}
				member.link = string(b)
				e = visit(member, strings.NewReader(""))
			} else {
				e = visit(member, r)
				r.Close()
			}
			if e != nil {
				return e
			}
		}
		return nil
	}
	buffered := bufio.NewReaderSize(file, 1<<20)
	var input io.Reader = buffered
	if header[0] == 0xfd && string(header[1:4]) == "7zX" {
		xzReader, e := xz.NewReader(buffered)
		if e != nil {
			return e
		}
		input = xzReader
	}
	if string(header[:3]) == "BZh" {
		input = bzip2.NewReader(buffered)
	}
	if header[0] == 0x1f && header[1] == 0x8b {
		gz, e := gzip.NewReader(buffered)
		if e != nil {
			return e
		}
		defer gz.Close()
		input = gz
	}
	r := tar.NewReader(input)
	for {
		h, e := r.Next()
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		member := archiveMember{name: h.Name, link: h.Linkname, mode: os.FileMode(h.Mode), size: h.Size, directory: h.Typeflag == tar.TypeDir, symlink: h.Typeflag == tar.TypeSymlink, hardlink: h.Typeflag == tar.TypeLink, modified: h.ModTime}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA && !member.directory && !member.symlink && !member.hardlink {
			return fmt.Errorf("unsupported archive entry type for %s", h.Name)
		}
		if e = visit(member, r); e != nil {
			return e
		}
	}
}

type extractPolicy struct {
	roots         map[string]bool
	links         bool
	maxFiles      int
	maxBytes      int64
	strip         string
	skipWorkflows bool
	materialize   bool
}

func extractArchive(archive, destination string, p extractPolicy) (map[string]string, []string, error) {
	root, e := absolute(destination)
	if e != nil {
		return nil, nil, e
	}
	if e = os.MkdirAll(root, 0755); e != nil {
		return nil, nil, e
	}
	seen := map[string]bool{}
	written := map[string]string{}
	var skipped []string
	var links []archiveMember
	var total int64
	e = eachArchive(archive, func(member archiveMember, reader io.Reader) error {
		relative := member.name
		if p.strip != "" {
			if !strings.HasPrefix(relative, p.strip) {
				return fmt.Errorf("unexpected archive root: %s", relative)
			}
			relative = strings.TrimPrefix(relative, p.strip)
		}
		relative = strings.TrimPrefix(relative, "./")
		if (relative == "" || relative == ".") && member.directory {
			return nil
		}
		target, e := confined(root, relative)
		if e != nil {
			return e
		}
		relative = path.Clean(relative)
		if p.roots != nil && !p.roots[strings.Split(relative, "/")[0]] {
			return fmt.Errorf("unexpected archive root: %s", relative)
		}
		if p.skipWorkflows && strings.Contains("/"+relative+"/", "/.github/workflows/") {
			skipped = append(skipped, relative)
			return nil
		}
		if seen[relative] {
			return fmt.Errorf("duplicate archive path: %s", relative)
		}
		seen[relative] = true
		total += member.size
		if len(seen) > p.maxFiles || member.size < 0 || total > p.maxBytes {
			return fmt.Errorf("archive exceeds file/byte limits")
		}
		if member.symlink || member.hardlink {
			if !p.links {
				return fmt.Errorf("links are not permitted: %s", relative)
			}
			link := member.link
			if strings.HasPrefix(link, "/") || strings.ContainsAny(link, "\\:") {
				return fmt.Errorf("archive link has unsafe target: %s", relative)
			}
			if member.symlink {
				link = path.Join(path.Dir(relative), link)
			}
			if _, e = confined(root, link); e != nil {
				return fmt.Errorf("archive link escapes root: %s", relative)
			}
			if p.roots != nil && !p.roots[strings.Split(path.Clean(link), "/")[0]] {
				return fmt.Errorf("archive link escapes allowed roots")
			}
			member.name = relative
			links = append(links, member)
			return nil
		}
		if member.directory {
			return os.MkdirAll(target, 0755)
		}
		if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
			return e
		}
		mode := member.mode.Perm()
		out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if e != nil {
			return e
		}
		n, copyErr := io.Copy(out, io.LimitReader(reader, member.size+1))
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if n != member.size {
			return fmt.Errorf("archive size mismatch: %s", relative)
		}
		if !member.modified.IsZero() {
			if e = os.Chtimes(target, member.modified, member.modified); e != nil {
				return e
			}
		}
		hash, e := digest(target)
		if e != nil {
			return e
		}
		written[relative] = hash
		return nil
	})
	if e != nil {
		return nil, skipped, e
	}
	for len(links) > 0 {
		var remaining []archiveMember
		progress := false
		for _, link := range links {
			target, e := confined(root, link.name)
			if e != nil {
				return nil, skipped, e
			}
			relative := link.link
			if link.symlink {
				relative = path.Join(path.Dir(link.name), relative)
			}
			source, e := confined(root, relative)
			if e != nil {
				return nil, skipped, e
			}
			if !exists(source) && (p.materialize || link.hardlink) {
				remaining = append(remaining, link)
				continue
			}
			if exists(target) {
				return nil, skipped, fmt.Errorf("link destination occupied")
			}
			if e = os.MkdirAll(filepath.Dir(target), 0755); e != nil {
				return nil, skipped, e
			}
			if p.materialize {
				info, e := os.Stat(source)
				if e != nil {
					return nil, skipped, e
				}
				if info.IsDir() {
					paths, e := files(source)
					if e != nil {
						return nil, skipped, e
					}
					for _, item := range paths {
						relative, _ := filepath.Rel(source, item)
						dest := filepath.Join(target, relative)
						if e = copyFile(item, dest); e != nil {
							return nil, skipped, e
						}
						hash, e := digest(dest)
						if e != nil {
							return nil, skipped, e
						}
						key, _ := filepath.Rel(root, dest)
						written[filepath.ToSlash(key)] = hash
					}
				} else {
					if e = copyFile(source, target); e != nil {
						return nil, skipped, e
					}
					hash, e := digest(target)
					if e != nil {
						return nil, skipped, e
					}
					written[link.name] = hash
				}
			} else if link.symlink {
				e = os.Symlink(link.link, target)
			} else {
				e = os.Link(source, target)
			}
			if e != nil {
				return nil, skipped, e
			}
			progress = true
		}
		if !progress {
			return nil, skipped, fmt.Errorf("unresolved archive links")
		}
		links = remaining
	}
	return written, skipped, nil
}
func downloadPinned(pin record, cache string) (string, error) {
	asset := stringValue(pin, "asset")
	if filepath.Base(asset) != asset || asset == "" {
		return "", fmt.Errorf("invalid pinned asset name")
	}
	expected := stringValue(pin, "sha256")
	size := number(pin, "bytes")
	if len(expected) != 64 || size <= 0 {
		return "", fmt.Errorf("invalid asset pin")
	}
	if e := os.MkdirAll(cache, 0755); e != nil {
		return "", e
	}
	target := filepath.Join(cache, asset)
	verify := func(p string) error {
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		hash, e := digest(p)
		if e != nil {
			return e
		}
		if info.Size() != size || hash != expected {
			return fmt.Errorf("asset differs from pinned size/SHA-256: %s", asset)
		}
		return nil
	}
	if exists(target) {
		return target, verify(target)
	}
	u, e := url.Parse(stringValue(pin, "url"))
	if e != nil || u.Scheme != "https" || u.User != nil {
		return "", fmt.Errorf("asset URL must use HTTPS without credentials")
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	request, e := http.NewRequest("GET", u.String(), nil)
	if e != nil {
		return "", e
	}
	request.Header.Set("User-Agent", "git-next-build-inputs")
	response, e := client.Do(request)
	if e != nil {
		return "", e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("asset HTTP %d", response.StatusCode)
	}
	temp, e := os.CreateTemp(cache, ".download-")
	if e != nil {
		return "", e
	}
	name := temp.Name()
	defer os.Remove(name)
	_, copyErr := io.Copy(temp, io.LimitReader(response.Body, size+1))
	closeErr := temp.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if e = verify(name); e != nil {
		return "", e
	}
	if e = os.Rename(name, target); e != nil {
		return "", e
	}
	return target, nil
}
func init() {
	register("extract-build-archive", "Extract bounded build inputs into an owned staging volume", "", func(a *app, o options) error {
		if len(o.args) != 2 {
			return fmt.Errorf("expected ARCHIVE DESTINATION")
		}
		_, _, e := extractArchive(o.args[0], o.args[1], extractPolicy{roots: map[string]bool{"core-stage": true, "core-build": true, "dependencies": true, "tls": true}, links: true, maxFiles: 50000, maxBytes: 2 << 30})
		return e
	})
	register("extract-runtime-archive", "Extract confined data-only runtime SDK files", "", func(a *app, o options) error {
		if len(o.args) != 2 {
			return fmt.Errorf("expected ARCHIVE DESTINATION")
		}
		_, _, e := extractArchive(o.args[0], o.args[1], extractPolicy{maxFiles: 50000, maxBytes: 1536 << 20})
		return e
	})
	register("prepare-llvm-mingw", "Prepare a hash-pinned isolated compiler SDK", "destination cache", prepareLLVM)
}
func prepareLLVM(a *app, o options) error {
	if e := o.require("destination", "cache"); e != nil {
		return e
	}
	pin, e := load(a.path("packaging/llvm-mingw.lock.json"))
	if e != nil {
		return e
	}
	archive, e := downloadPinned(pin, o.get("cache"))
	if e != nil {
		return e
	}
	dest, e := newDestination(o.get("destination"))
	if e != nil {
		return e
	}
	h, skipped, e := extractArchive(archive, dest, extractPolicy{maxFiles: 100000, maxBytes: 4 << 30, strip: strings.TrimSuffix(stringValue(pin, "asset"), ".zip") + "/", skipWorkflows: true})
	if e != nil {
		return e
	}
	for _, name := range []string{"aarch64-w64-mingw32-clang.exe", "aarch64-w64-mingw32-windres.exe", "llvm-ar.exe"} {
		if h["bin/"+name] == "" {
			return fmt.Errorf("required compiler missing: %s", name)
		}
	}
	if skipped == nil {
		skipped = []string{}
	}
	receipt := record{"schema_version": 1, "status": "isolated-compiler-prepared", "source": pin, "files": h, "excluded_workflows": skipped, "product_executed": false, "tests_executed": false, "compiler_executed": false, "installed_into_product": false}
	if e = writeJSON(filepath.Join(dest, "receipt.json"), receipt); e != nil {
		return e
	}
	return a.json(record{"status": receipt["status"], "files": len(h), "product_executed": false})
}
