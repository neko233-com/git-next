// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var lfsAttributeHeader = []byte("\n#\n# Git LFS (see https://git-lfs.github.com/)\n#\n")

func (d *driver) prepareLargeFiles(ref string) error {
	d.largeFiles = map[string]bool{}
	d.baseAttributes = nil
	kind := d.cfg("git-p4.largeFileSystem")
	if kind == "" {
		return nil
	}
	if kind != "GitLFS" && kind != "MockLFS" {
		return fmt.Errorf("unknown git-p4.largeFileSystem %s", kind)
	}
	if kind == "GitLFS" {
		if b, e := d.gitCall(nil, nil, "show", ref+":.gitattributes"); e == nil {
			base, generated, found := bytes.Cut(b, lfsAttributeHeader)
			d.baseAttributes = base
			if found {
				for _, line := range strings.Split(string(generated), "\n") {
					if strings.HasPrefix(line, "/") {
						name, _, ok := strings.Cut(strings.TrimPrefix(line, "/"), " filter=lfs ")
						if ok {
							d.largeFiles[strings.ReplaceAll(name, "[[:space:]]", " ")] = true
						}
					}
				}
			}
		}
	}
	return nil
}
func (d *driver) largeExtension(name string) bool {
	for _, ext := range d.cfgAll("git-p4.largeFileExtensions") {
		if strings.HasSuffix(name, "."+ext) {
			return true
		}
	}
	return false
}
func (d *driver) attributes() []byte {
	data := append([]byte{}, d.baseAttributes...)
	data = append(data, lfsAttributeHeader...)
	extensions := append([]string{}, d.cfgAll("git-p4.largeFileExtensions")...)
	sort.Strings(extensions)
	for _, ext := range extensions {
		data = append(data, []byte("*."+strings.ReplaceAll(ext, " ", "[[:space:]]")+" filter=lfs diff=lfs merge=lfs -text\n")...)
	}
	var files []string
	for name := range d.largeFiles {
		if !d.largeExtension(name) {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	for _, name := range files {
		data = append(data, []byte("/"+strings.ReplaceAll(name, " ", "[[:space:]]")+" filter=lfs diff=lfs merge=lfs -text\n")...)
	}
	return data
}
func (d *driver) writeAttributes(w io.Writer) error {
	if d.cfg("git-p4.largeFileSystem") != "GitLFS" {
		return nil
	}
	data := d.attributes()
	if _, e := fmt.Fprintf(w, "M 100644 inline .gitattributes\ndata %d\n", len(data)); e != nil {
		return e
	}
	if _, e := w.Write(data); e != nil {
		return e
	}
	_, e := io.WriteString(w, "\n")
	return e
}
func (d *driver) largeContent(w io.Writer, mode, name string, data []byte) (string, []byte, error) {
	kind := d.cfg("git-p4.largeFileSystem")
	if kind == "" || mode == "120000" {
		return mode, data, nil
	}
	if kind == "GitLFS" && name == ".gitattributes" {
		d.baseAttributes = append([]byte{}, data...)
		return mode, d.attributes(), nil
	}
	large := d.largeExtension(name)
	threshold, e := strconv.ParseInt(d.cfg("git-p4.largeFileThreshold"), 10, 64)
	if e == nil && threshold > 0 && int64(len(data)) > threshold {
		large = true
	}
	compressed, e := strconv.ParseInt(d.cfg("git-p4.largeFileCompressedThreshold"), 10, 64)
	if !large && e == nil && compressed > 0 && int64(len(data)) > compressed {
		var b bytes.Buffer
		zw, e := flate.NewWriter(&b, flate.DefaultCompression)
		if e != nil {
			return "", nil, e
		}
		if _, e = zw.Write(data); e != nil {
			return "", nil, e
		}
		if e = zw.Close(); e != nil {
			return "", nil, e
		}
		large = int64(b.Len()) > compressed
	}
	if !large || len(data) == 0 {
		return mode, data, nil
	}
	gitdir, e := d.gitText("rev-parse", "--absolute-git-dir")
	if e != nil {
		return "", nil, e
	}
	storage := ""
	pointer := []byte{}
	if kind == "MockLFS" {
		first, _, _ := bytes.Cut(data, []byte{'\n'})
		filename := string(first)
		if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, "/\\:\x00") {
			return "", nil, fmt.Errorf("unsafe mock large-file storage key")
		}
		storage = filepath.Join(gitdir, "mock-storage/local", filename)
		line := append(append([]byte("pointer-"), first...), '\n')
		pointer = line
	} else {
		hash := sha256.Sum256(data)
		oid := hex.EncodeToString(hash[:])
		root := d.cfg("lfs.storage")
		if root == "" {
			root = "lfs"
		}
		if !filepath.IsAbs(root) {
			root = filepath.Join(gitdir, root)
		}
		storage = filepath.Join(root, "objects", oid[:2], oid[2:4], oid)
		pointer = lfsPointer(data)
	}
	if e = os.MkdirAll(filepath.Dir(storage), 0755); e != nil {
		return "", nil, e
	}
	if old, e := os.ReadFile(storage); e == nil {
		if !bytes.Equal(old, data) {
			return "", nil, fmt.Errorf("large-file object collision")
		}
	} else if !os.IsNotExist(e) {
		return "", nil, e
	} else {
		if e = os.WriteFile(storage, data, 0644); e != nil {
			return "", nil, e
		}
	}
	d.largeFiles[name] = true
	if d.cfgBool("git-p4.largeFilePush") {
		if kind == "MockLFS" {
			remote := filepath.Join(gitdir, "mock-storage/remote", filepath.Base(storage))
			if e = os.MkdirAll(filepath.Dir(remote), 0755); e != nil {
				return "", nil, e
			}
			if e = os.WriteFile(remote, data, 0644); e != nil {
				return "", nil, e
			}
		} else {
			if _, e = d.gitCall(nil, nil, "lfs", "push", "--object-id", "origin", filepath.Base(storage)); e != nil {
				return "", nil, e
			}
		}
	}
	if e = d.writeAttributes(w); e != nil {
		return "", nil, e
	}
	return "100644", pointer, nil
}
