// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type record = map[string]any

func decodeJSON(data []byte, out any) error { return json.Unmarshal(data, out) }
func jsonBytes(v any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if e := encoder.Encode(v); e != nil {
		return nil, e
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
func equalJSON(a, b any) bool {
	x, e := json.Marshal(a)
	if e != nil {
		return false
	}
	y, e := json.Marshal(b)
	return e == nil && bytes.Equal(x, y)
}
func exists(p string) bool          { _, err := os.Stat(p); return err == nil }
func read(p string) (string, error) { b, e := os.ReadFile(p); return string(b), e }
func digest(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func digestBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func write(p string, b []byte, mode fs.FileMode) error {
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return e
	}
	return os.WriteFile(p, b, mode)
}
func writeJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return write(p, append(b, '\n'), 0644)
}
func writeNewJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return e
	}
	file, e := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return e
	}
	_, e = file.Write(append(b, '\n'))
	closeErr := file.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func load(p string) (record, error) {
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var r record
	e = json.Unmarshal(b, &r)
	return r, e
}
func stringValue(m record, k string) string { v, _ := m[k].(string); return v }
func object(m record, k string) record      { r, _ := m[k].(map[string]any); return r }
func array(m record, k string) []any        { r, _ := m[k].([]any); return r }
func truth(m record, k string) bool         { v, _ := m[k].(bool); return v }
func number(m record, k string) int64 {
	switch v := m[k].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	case json.Number:
		n, _ := v.Int64()
		return n
	}
	return 0
}
func absolute(p string) (string, error) {
	v, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	return filepath.Clean(v), nil
}
func inside(root, p string) bool {
	r, e := filepath.Rel(root, p)
	return e == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) && !filepath.IsAbs(r)
}
func confined(root, relative string) (string, error) {
	if relative == "" || strings.ContainsAny(relative, "\\:") || strings.HasPrefix(relative, "/") {
		return "", fmt.Errorf("unsafe relative path %q", relative)
	}
	for _, part := range strings.Split(relative, "/") {
		if part == ".." {
			return "", fmt.Errorf("unsafe relative path %q", relative)
		}
	}
	p := filepath.Join(root, filepath.FromSlash(relative))
	if !inside(root, p) {
		return "", fmt.Errorf("path escapes root: %s", relative)
	}
	for dir := filepath.Dir(p); inside(root, dir); dir = filepath.Dir(dir) {
		if info, e := os.Lstat(dir); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink ancestor: %s", dir)
		}
		if dir == root {
			break
		}
	}
	return p, nil
}
func copyFile(src, dst string) error {
	f, e := os.Open(src)
	if e != nil {
		return e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
		return e
	}
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if e != nil {
		return e
	}
	_, e = io.Copy(out, f)
	closeErr := out.Close()
	if e != nil {
		return e
	}
	return closeErr
}
func files(root string) ([]string, error) {
	var paths []string
	e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink %s", p)
		}
		if !d.IsDir() {
			paths = append(paths, p)
		}
		return nil
	})
	return paths, e
}
func hashes(root string) (map[string]string, error) {
	paths, e := files(root)
	if e != nil {
		return nil, e
	}
	m := map[string]string{}
	for _, p := range paths {
		relative, e := filepath.Rel(root, p)
		if e != nil {
			return nil, e
		}
		h, e := digest(p)
		if e != nil {
			return nil, e
		}
		m[filepath.ToSlash(relative)] = h
	}
	return m, nil
}
func newDestination(p string) (string, error) {
	p, e := absolute(p)
	if e != nil {
		return "", e
	}
	if exists(p) {
		return "", fmt.Errorf("destination must be new: %s", p)
	}
	if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return "", e
	}
	if e = os.Mkdir(p, 0755); e != nil {
		return "", e
	}
	return p, nil
}
