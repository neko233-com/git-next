// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
)

func TestLFSFixtureTransferIntegrityAndLocks(t *testing.T) {
	root := t.TempDir()
	f, e := newLFSFixture(root)
	if e != nil {
		t.Fatal(e)
	}
	defer f.close()
	roots := x509.NewCertPool()
	cert, e := os.ReadFile(f.CA)
	if e != nil {
		t.Fatal(e)
	}
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("fixture CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request := func(method, path string, body []byte, auth bool) (int, []byte) {
		t.Helper()
		req, e := http.NewRequest(method, f.URL+path, bytes.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if auth {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("fixture:"+f.password)))
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		out, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, out
	}
	if status, _ := request("POST", "/objects/batch", []byte(`{"operation":"download","objects":[]}`), false); status != 401 {
		t.Fatal(status)
	}
	data := []byte("\x00owned fixture data\n")
	oid := digestBytes(data)
	if status, _ := request("PUT", "/object/"+oid, []byte("bad"), true); status != 422 {
		t.Fatal("corrupt upload accepted", status)
	}
	if status, _ := request("PUT", "/object/"+oid, data, true); status != 200 {
		t.Fatal(status)
	}
	if status, out := request("GET", "/object/"+oid, nil, true); status != 200 || !bytes.Equal(out, data) {
		t.Fatal("download bytes")
	}
	batch, _ := json.Marshal(record{"operation": "download", "objects": []record{{"oid": oid, "size": len(data)}}})
	for _, failure := range []string{"corrupt", "oversize", "duplicate", "omit", "reject", "downgrade"} {
		f.mu.Lock()
		f.failure = failure
		f.mu.Unlock()
		status, out := request("POST", "/objects/batch", batch, true)
		if status != 200 {
			t.Fatal(status)
		}
		var response record
		if e = json.Unmarshal(out, &response); e != nil {
			t.Fatal(e)
		}
		if failure == "duplicate" && len(array(response, "objects")) != 2 {
			t.Fatal("duplicate mode")
		}
		if failure == "omit" && len(array(response, "objects")) != 0 {
			t.Fatal("omit mode")
		}
		if failure == "corrupt" || failure == "oversize" {
			_, out = request("GET", "/object/"+oid, nil, true)
			if bytes.Equal(out, data) {
				t.Fatal("failure mode unchanged")
			}
		}
	}
	f.mu.Lock()
	f.failure = ""
	f.locksSupported = true
	f.mu.Unlock()
	status, out := request("POST", "/locks", []byte(`{"path":"Content/Hero.uasset"}`), true)
	if status != 201 {
		t.Fatal(status)
	}
	var created record
	if e = json.Unmarshal(out, &created); e != nil {
		t.Fatal(e)
	}
	id := stringValue(object(created, "lock"), "id")
	if status, _ = request("POST", "/locks", []byte(`{"path":"Content/Hero.uasset"}`), true); status != 409 {
		t.Fatal("duplicate lock")
	}
	f.mu.Lock()
	f.locks[id]["owner"].(map[string]any)["name"] = "other-fixture"
	f.mu.Unlock()
	if status, _ = request("POST", "/locks/"+id+"/unlock", []byte(`{}`), true); status != 403 {
		t.Fatal("foreign unlock accepted")
	}
	if status, _ = request("POST", "/locks/"+id+"/unlock", []byte(`{"force":true}`), true); status != 200 {
		t.Fatal("forced unlock failed")
	}
}
