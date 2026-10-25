// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var gameLFSObjectID = regexp.MustCompile(`^[a-f0-9]{64}$`)

const gameLFSBufferBytes = 32 << 10

type diskLFSObject struct {
	size int64
	path string
}

type diskLFSFixture struct {
	*loopbackFixture
	directory, password string
	mu                  sync.Mutex
	objects             map[string]diskLFSObject
	pending             map[string]int64
	failure             string
	active, maximum     int
	transferred         int64
}

func newDiskLFSFixture(root string) (*diskLFSFixture, error) {
	directory := filepath.Join(root, "streamed-lfs-objects")
	if e := os.MkdirAll(directory, 0700); e != nil {
		return nil, e
	}
	f := &diskLFSFixture{directory: directory, password: fixtureSecret(), objects: map[string]diskLFSObject{}, pending: map[string]int64{}}
	s, e := startTLSFixture(root, http.HandlerFunc(f.handle))
	if e != nil {
		return nil, e
	}
	f.loopbackFixture = s
	f.URL += "/lfs"
	return f, nil
}

func (f *diskLFSFixture) authorization() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("fixture:"+f.password))
}

func (f *diskLFSFixture) setFailure(value string) {
	f.mu.Lock()
	f.failure = value
	f.mu.Unlock()
}

func (f *diskLFSFixture) stats() record {
	f.mu.Lock()
	defer f.mu.Unlock()
	return record{"objects": len(f.objects), "active_transfers": f.active, "maximum_active_transfers": f.maximum, "transferred_bytes": f.transferred, "stream_buffer_bytes": gameLFSBufferBytes, "objects_buffered_in_memory": false}
}

func (f *diskLFSFixture) transfer() func(int64) {
	f.mu.Lock()
	f.active++
	if f.active > f.maximum {
		f.maximum = f.active
	}
	f.mu.Unlock()
	return func(n int64) {
		f.mu.Lock()
		f.active--
		f.transferred += n
		f.mu.Unlock()
	}
}

func (f *diskLFSFixture) handle(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != f.authorization() {
		w.Header().Set("WWW-Authenticate", `Basic realm="streamed-lfs-fixture"`)
		fixtureJSON(w, http.StatusUnauthorized, nil)
		return
	}
	f.mu.Lock()
	failure := f.failure
	f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/objects/batch") && r.Method == http.MethodPost {
		var request struct {
			Operation string `json:"operation"`
			Objects   []struct {
				OID  string `json:"oid"`
				Size int64  `json:"size"`
			} `json:"objects"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		if decoder.Decode(&request) != nil || len(request.Objects) > 32 || (request.Operation != "upload" && request.Operation != "download") {
			fixtureJSON(w, http.StatusBadRequest, nil)
			return
		}
		objects := []record{}
		for _, object := range request.Objects {
			if !gameLFSObjectID.MatchString(object.OID) || object.Size < 0 || object.Size > 256<<20 {
				fixtureJSON(w, http.StatusBadRequest, nil)
				return
			}
			item := record{"oid": object.OID, "size": object.Size}
			f.mu.Lock()
			stored, exists := f.objects[object.OID]
			if request.Operation == "upload" && !exists {
				f.pending[object.OID] = object.Size
			}
			f.mu.Unlock()
			action := record{"href": f.URL + "/object/" + object.OID, "header": record{"Authorization": f.authorization()}}
			if failure == "reject" {
				item["error"] = record{"code": 403, "message": "injected transfer refusal"}
			} else if request.Operation == "upload" && !exists {
				item["actions"] = record{"upload": action, "verify": record{"href": f.URL + "/verify", "header": record{"Authorization": f.authorization()}}}
			} else if request.Operation == "download" && (!exists || stored.size != object.Size) {
				item["error"] = record{"code": 404, "message": "object absent"}
			} else if request.Operation == "download" {
				item["actions"] = record{"download": action}
			}
			objects = append(objects, item)
		}
		fixtureJSON(w, http.StatusOK, record{"transfer": "basic", "objects": objects})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/verify") && r.Method == http.MethodPost {
		var value struct {
			OID  string `json:"oid"`
			Size int64  `json:"size"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&value) != nil {
			fixtureJSON(w, http.StatusBadRequest, nil)
			return
		}
		f.mu.Lock()
		object, exists := f.objects[value.OID]
		f.mu.Unlock()
		if !exists || object.size != value.Size {
			fixtureJSON(w, http.StatusUnprocessableEntity, nil)
			return
		}
		fixtureJSON(w, http.StatusOK, nil)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/lfs/object/") {
		fixtureJSON(w, http.StatusNotFound, nil)
		return
	}
	oid := strings.TrimPrefix(r.URL.Path, "/lfs/object/")
	if !gameLFSObjectID.MatchString(oid) {
		fixtureJSON(w, http.StatusBadRequest, nil)
		return
	}
	if r.Method == http.MethodPut {
		f.upload(w, r, oid)
		return
	}
	if r.Method != http.MethodGet {
		fixtureJSON(w, http.StatusMethodNotAllowed, nil)
		return
	}
	f.mu.Lock()
	object, exists := f.objects[oid]
	f.mu.Unlock()
	if !exists {
		fixtureJSON(w, http.StatusNotFound, nil)
		return
	}
	file, e := os.Open(object.path)
	if e != nil {
		fixtureJSON(w, http.StatusInternalServerError, nil)
		return
	}
	defer file.Close()
	finished := f.transfer()
	var copied int64
	defer func() { finished(copied) }()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprint(object.size))
	buffer := make([]byte, gameLFSBufferBytes)
	if failure == "corrupt" && object.size > 0 {
		var first [1]byte
		if _, e = io.ReadFull(file, first[:]); e != nil {
			return
		}
		first[0] ^= 1
		n, e := w.Write(first[:])
		copied += int64(n)
		if e != nil {
			return
		}
	}
	n, _ := io.CopyBuffer(w, struct{ io.Reader }{file}, buffer)
	copied += n
}

func (f *diskLFSFixture) upload(w http.ResponseWriter, r *http.Request, oid string) {
	f.mu.Lock()
	expected, exists := f.pending[oid]
	f.mu.Unlock()
	if !exists {
		fixtureJSON(w, http.StatusConflict, nil)
		return
	}
	file, e := os.CreateTemp(f.directory, ".incoming-")
	if e != nil {
		fixtureJSON(w, http.StatusInternalServerError, nil)
		return
	}
	defer os.Remove(file.Name())
	defer file.Close()
	finished := f.transfer()
	hash := sha256.New()
	n, e := io.CopyBuffer(io.MultiWriter(file, hash), io.LimitReader(r.Body, expected+1), make([]byte, gameLFSBufferBytes))
	finished(n)
	if e != nil || n != expected || hex.EncodeToString(hash.Sum(nil)) != oid {
		fixtureJSON(w, http.StatusUnprocessableEntity, nil)
		return
	}
	if file.Sync() != nil || file.Close() != nil {
		fixtureJSON(w, http.StatusInternalServerError, nil)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.objects[oid]; !exists {
		path := filepath.Join(f.directory, oid)
		if _, e = os.Lstat(path); !os.IsNotExist(e) {
			fixtureJSON(w, http.StatusConflict, nil)
			return
		}
		if e = os.Rename(file.Name(), path); e != nil {
			fixtureJSON(w, http.StatusInternalServerError, nil)
			return
		}
		f.objects[oid] = diskLFSObject{n, path}
	}
	fixtureJSON(w, http.StatusOK, nil)
}
