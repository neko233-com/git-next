// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestDiskLFSRejectsIncompleteAndWrongDigestWithoutPublishing(t *testing.T) {
	data := bytes.Repeat([]byte("asset-data"), 10000)
	sum := sha256.Sum256(data)
	oid := hex.EncodeToString(sum[:])
	f := &diskLFSFixture{directory: t.TempDir(), password: "fixture-only", objects: map[string]diskLFSObject{}, pending: map[string]int64{oid: int64(len(data))}}
	for _, body := range [][]byte{data[:len(data)-1], append([]byte{'x'}, data[1:]...), append(append([]byte{}, data...), 'x')} {
		r := httptest.NewRequest(http.MethodPut, "/lfs/object/"+oid, bytes.NewReader(body))
		r.Header.Set("Authorization", f.authorization())
		w := httptest.NewRecorder()
		f.handle(w, r)
		if w.Code != http.StatusUnprocessableEntity || len(f.objects) != 0 {
			t.Fatalf("rejected object became visible: status=%d", w.Code)
		}
		entries, e := os.ReadDir(f.directory)
		if e != nil || len(entries) != 0 {
			t.Fatalf("failed upload left material: %v", e)
		}
	}
	r := httptest.NewRequest(http.MethodPut, "/lfs/object/"+oid, bytes.NewReader(data))
	r.Header.Set("Authorization", f.authorization())
	w := httptest.NewRecorder()
	f.handle(w, r)
	stored, e := os.ReadFile(filepath.Join(f.directory, oid))
	if w.Code != http.StatusOK || e != nil || !bytes.Equal(stored, data) || len(f.objects) != 1 {
		t.Fatalf("verified upload failed: status=%d, error=%v", w.Code, e)
	}
	if f.active != 0 {
		t.Fatal("transfer remained active")
	}
}

func TestDiskLFSRejectsUnsolicitedAndUnsafeObjectPaths(t *testing.T) {
	f := &diskLFSFixture{directory: t.TempDir(), password: "fixture-only", objects: map[string]diskLFSObject{}, pending: map[string]int64{}}
	for _, path := range []string{"/lfs/object/not-an-oid", "/lfs/object/../outside", "/lfs/object/" + string(bytes.Repeat([]byte{'a'}, 64))} {
		r := httptest.NewRequest(http.MethodPut, path, bytes.NewReader([]byte("no")))
		r.Header.Set("Authorization", f.authorization())
		w := httptest.NewRecorder()
		f.handle(w, r)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusConflict {
			t.Fatalf("unsafe or unsolicited object accepted: %d", w.Code)
		}
	}
}

func TestDiskLFSConcurrentPublicationAndCorruptDownload(t *testing.T) {
	data := bytes.Repeat([]byte("unity-unreal-fixture"), 10000)
	sum := sha256.Sum256(data)
	oid := hex.EncodeToString(sum[:])
	f := &diskLFSFixture{directory: t.TempDir(), password: "fixture-only", objects: map[string]diskLFSObject{}, pending: map[string]int64{oid: int64(len(data))}}
	var wait sync.WaitGroup
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			r := httptest.NewRequest(http.MethodPut, "/lfs/object/"+oid, bytes.NewReader(data))
			r.Header.Set("Authorization", f.authorization())
			w := httptest.NewRecorder()
			f.handle(w, r)
			if w.Code != http.StatusOK {
				t.Errorf("concurrent verified publication failed: %d", w.Code)
			}
		}()
	}
	wait.Wait()
	entries, e := os.ReadDir(f.directory)
	if e != nil || len(entries) != 1 || entries[0].Name() != oid {
		t.Fatalf("concurrent publication left duplicate/partial objects: %v", e)
	}
	for _, mode := range []string{"corrupt", ""} {
		f.setFailure(mode)
		r := httptest.NewRequest(http.MethodGet, "/lfs/object/"+oid, nil)
		r.Header.Set("Authorization", f.authorization())
		w := httptest.NewRecorder()
		f.handle(w, r)
		want := append([]byte{}, data...)
		if mode == "corrupt" {
			want[0] ^= 1
		}
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), want) {
			t.Fatalf("download mode %q changed length/content unexpectedly", mode)
		}
	}
	if f.active != 0 || len(f.objects) != 1 {
		t.Fatal("transfer or publication state leaked")
	}
}
