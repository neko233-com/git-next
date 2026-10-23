// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Fixture services use generated loopback certificates and bounded reference Git
// subprocesses; none of their credentials or repositories belong to a real user.
type loopbackFixture struct {
	server   *http.Server
	listener net.Listener
	URL, CA  string
}

func (f *loopbackFixture) close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = f.server.Shutdown(ctx)
	_ = f.listener.Close()
}
func startTLSFixture(root string, handler http.Handler) (*loopbackFixture, error) {
	serial := func() *big.Int { v, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120)); return v }
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return nil, e
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "git-next fixture CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		return nil, e
	}
	serverKey, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return nil, e
	}
	cert := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, cert, ca, &serverKey.PublicKey, key)
	if e != nil {
		return nil, e
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(serverKey)
	if e != nil {
		return nil, e
	}
	caPath := filepath.Join(root, "fixture-ca.pem")
	if e = os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0600); e != nil {
		return nil, e
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	pair, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		return nil, e
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	tlsListener := tls.NewListener(listener, &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 64 << 10}
	fixture := &loopbackFixture{server, tlsListener, "https://" + listener.Addr().String(), caPath}
	go func() { _ = server.Serve(tlsListener) }()
	return fixture, nil
}
func fixtureSecret() string {
	var v [24]byte
	if _, e := rand.Read(v[:]); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(v[:])
}
func fixtureDir(path string) bool { info, e := os.Stat(path); return e == nil && info.IsDir() }
func fixtureJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/vnd.git-lfs+json")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}

type lfsFixture struct {
	*loopbackFixture
	mu             sync.Mutex
	password       string
	objects        map[string][]byte
	requests       []string
	verified       []string
	ports          []string
	failure        string
	locksSupported bool
	locks          map[string]map[string]any
}

func newLFSFixture(root string) (*lfsFixture, error) {
	f := &lfsFixture{password: fixtureSecret(), objects: map[string][]byte{}, locks: map[string]map[string]any{}}
	s, e := startTLSFixture(root, http.HandlerFunc(f.handle))
	if e != nil {
		return nil, e
	}
	f.loopbackFixture = s
	f.URL += "/lfs"
	return f, nil
}
func (f *lfsFixture) handle(w http.ResponseWriter, r *http.Request) {
	body, e := io.ReadAll(io.LimitReader(r.Body, (8<<20)+1))
	if e != nil || len(body) > 8<<20 {
		http.Error(w, "bounded fixture request exceeded", 413)
		return
	}
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("fixture:"+f.password))
	authorized := r.Header.Get("Authorization") == expected
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, fmt.Sprintf("%s %s %t", r.Method, r.URL.String(), authorized))
	f.ports = append(f.ports, r.RemoteAddr)
	if !authorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="lfs-fixture"`)
		fixtureJSON(w, 401, nil)
		return
	}
	route := r.URL.Path
	value := map[string]any{}
	if len(body) > 0 {
		if json.Unmarshal(body, &value) != nil && !strings.Contains(route, "/object/") {
			fixtureJSON(w, 400, nil)
			return
		}
	}
	if f.locksSupported && (strings.HasSuffix(route, "/locks") || strings.Contains(route, "/locks/")) {
		if strings.HasSuffix(route, "/locks/verify") {
			ours, theirs := []any{}, []any{}
			for _, item := range f.locks {
				if item["owner"].(map[string]any)["name"] == "fixture" {
					ours = append(ours, item)
				} else {
					theirs = append(theirs, item)
				}
			}
			fixtureJSON(w, 200, map[string]any{"ours": ours, "theirs": theirs})
			return
		}
		if strings.HasSuffix(route, "/unlock") {
			parts := strings.Split(route, "/")
			id := parts[len(parts)-2]
			item := f.locks[id]
			if item == nil {
				fixtureJSON(w, 404, nil)
				return
			}
			if item["owner"].(map[string]any)["name"] != "fixture" && value["force"] != true {
				fixtureJSON(w, 403, nil)
				return
			}
			delete(f.locks, id)
			fixtureJSON(w, 200, map[string]any{"lock": item})
			return
		}
		if r.Method == "POST" {
			for _, item := range f.locks {
				if item["path"] == value["path"] {
					fixtureJSON(w, 409, map[string]any{"lock": item, "message": "already locked"})
					return
				}
			}
			id := fixtureSecret()
			item := map[string]any{"id": id, "path": value["path"], "locked_at": "2026-10-21T00:00:00Z", "owner": map[string]any{"name": "fixture"}}
			f.locks[id] = item
			fixtureJSON(w, 201, map[string]any{"lock": item})
			return
		}
		items := []any{}
		for _, item := range f.locks {
			if p := r.URL.Query().Get("path"); p == "" || item["path"] == p {
				items = append(items, item)
			}
		}
		fixtureJSON(w, 200, map[string]any{"locks": items})
		return
	}
	if strings.HasSuffix(route, "/objects/batch") {
		operation, _ := value["operation"].(string)
		if operation != "upload" && operation != "download" {
			fixtureJSON(w, 400, nil)
			return
		}
		pointers, _ := value["objects"].([]any)
		objects := []any{}
		for _, raw := range pointers {
			pointer, ok := raw.(map[string]any)
			if !ok {
				fixtureJSON(w, 400, nil)
				return
			}
			oid, _ := pointer["oid"].(string)
			if len(oid) != 64 {
				fixtureJSON(w, 400, nil)
				return
			}
			item := map[string]any{"oid": oid, "size": pointer["size"]}
			if f.failure == "reject" {
				item["error"] = map[string]any{"code": 403}
			} else if operation == "upload" {
				if _, ok := f.objects[oid]; !ok {
					item["actions"] = map[string]any{"upload": map[string]any{"href": f.URL + "/object/" + oid}, "verify": map[string]any{"href": f.URL + "/verify"}}
				}
			} else if _, ok := f.objects[oid]; !ok {
				item["error"] = map[string]any{"code": 404}
			} else {
				href := f.URL + "/object/" + oid
				if f.failure == "downgrade" {
					href = strings.Replace(href, "https:", "http:", 1)
				}
				item["actions"] = map[string]any{"download": map[string]any{"href": href}}
			}
			objects = append(objects, item)
		}
		if f.failure == "duplicate" {
			objects = append(objects, objects...)
		}
		if f.failure == "omit" {
			objects = []any{}
		}
		fixtureJSON(w, 200, map[string]any{"transfer": "basic", "objects": objects})
		return
	}
	if strings.HasSuffix(route, "/verify") {
		if oid, ok := value["oid"].(string); ok {
			data, exists := f.objects[oid]
			if !exists || float64(len(data)) != value["size"] {
				fixtureJSON(w, 422, nil)
				return
			}
			f.verified = append(f.verified, oid)
		}
		fixtureJSON(w, 200, nil)
		return
	}
	if strings.Contains(route, "/object/") {
		oid := filepath.Base(route)
		if r.Method == "PUT" {
			sum := sha256.Sum256(body)
			if hex.EncodeToString(sum[:]) != oid {
				fixtureJSON(w, 422, nil)
				return
			}
			f.objects[oid] = body
			fixtureJSON(w, 200, nil)
			return
		}
		data, ok := f.objects[oid]
		if !ok {
			fixtureJSON(w, 404, nil)
			return
		}
		if f.failure == "corrupt" {
			data = []byte(strings.Repeat("x", len(data)))
		}
		if f.failure == "oversize" {
			data = append(append([]byte{}, data...), byte('x'))
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(data)
		return
	}
	fixtureJSON(w, 404, nil)
}

type gitHTTPFixture struct {
	*loopbackFixture
	root, backend string
	env           []string
	password      string
	mu            sync.Mutex
	authenticated int
	afterReceive  func()
}

func newGitHTTPFixture(root, backend string, env []string, password string) (*gitHTTPFixture, error) {
	f := &gitHTTPFixture{root: root, backend: backend, env: env, password: password}
	s, e := startTLSFixture(root, http.HandlerFunc(f.handle))
	if e != nil {
		return nil, e
	}
	f.loopbackFixture = s
	f.URL += "/repository.git"
	return f, nil
}
func (f *gitHTTPFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("fixture:"+f.password))
	f.mu.Unlock()
	if r.Header.Get("Authorization") != expected {
		w.Header().Set("WWW-Authenticate", `Basic realm="isolated-fixture"`)
		w.WriteHeader(401)
		return
	}
	body, e := io.ReadAll(io.LimitReader(r.Body, (16<<20)+1))
	if e != nil || len(body) > 16<<20 {
		http.Error(w, "bounded fixture request exceeded", 413)
		return
	}
	f.mu.Lock()
	f.authenticated++
	f.mu.Unlock()
	env := fixtureEnv(f.env, map[string]string{"GIT_PROJECT_ROOT": f.root, "GIT_HTTP_EXPORT_ALL": "1", "PATH_INFO": r.URL.Path, "QUERY_STRING": r.URL.RawQuery, "REQUEST_METHOD": r.Method, "CONTENT_TYPE": r.Header.Get("Content-Type"), "CONTENT_LENGTH": fmt.Sprint(len(body)), "REMOTE_USER": "fixture", "SERVER_PROTOCOL": "HTTP/1.1", "REMOTE_ADDR": "127.0.0.1"})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, e := runProcess(ctx, f.root, env, body, f.backend)
	if e != nil || result.code != 0 {
		http.Error(w, "fixture HTTP backend failed", 500)
		return
	}
	text := string(result.stdout)
	header, response, ok := strings.Cut(text, "\r\n\r\n")
	if !ok {
		header, response, ok = strings.Cut(text, "\n\n")
	}
	if !ok {
		http.Error(w, "fixture CGI headers missing", 500)
		return
	}
	status := 200
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.EqualFold(key, "Status") {
			_, _ = fmt.Sscan(value, &status)
		} else if !strings.EqualFold(key, "Content-Length") {
			w.Header().Add(key, value)
		}
	}
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/git-receive-pack") {
		f.mu.Lock()
		callback := f.afterReceive
		f.afterReceive = nil
		f.mu.Unlock()
		if callback != nil {
			callback()
		}
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, response)
}
func fixtureEnv(base []string, extra map[string]string) []string {
	env := make([]string, 0, len(base)+len(extra))
	for _, item := range base {
		k, _, _ := strings.Cut(item, "=")
		found := false
		for key := range extra {
			if strings.EqualFold(key, k) {
				found = true
				break
			}
		}
		if !found {
			env = append(env, item)
		}
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
func serveGitHTTP(a *app, o options) error {
	if e := o.require("root"); e != nil {
		return e
	}
	root, e := filepath.Abs(o.get("root"))
	if e != nil {
		return e
	}
	if !fixtureDir(root) {
		return fmt.Errorf("not a directory: %s", root)
	}
	port, e := o.integer("port", 8123)
	if e != nil || port < 0 || port > 65535 {
		return fmt.Errorf("invalid --port")
	}
	git, e := exec.LookPath(o.value("reference", "git"))
	if e != nil {
		return e
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		suffix := "/info/refs"
		args := []string{"upload-pack", "--stateless-rpc"}
		if r.Method == "GET" && r.URL.Query().Get("service") == "git-upload-pack" && strings.HasSuffix(r.URL.Path, suffix) {
			args = append(args, "--advertise-refs")
		} else if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/git-upload-pack") {
			suffix = "/git-upload-pack"
		} else {
			http.NotFound(w, r)
			return
		}
		rel := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, suffix), "/")
		repo, e := confined(root, rel)
		resolved, resolveErr := filepath.EvalSymlinks(repo)
		if e != nil || resolveErr != nil || !inside(root, resolved) || !fixtureDir(repo) {
			http.NotFound(w, r)
			return
		}
		args = append(args, repo)
		body, e := io.ReadAll(io.LimitReader(r.Body, (16<<20)+1))
		if e != nil || len(body) > 16<<20 {
			http.Error(w, "request too large", 413)
			return
		}
		env := fixtureEnv(acceptEnv(filepath.Join(root, ".fixture-home")), map[string]string{"GIT_PROTOCOL": r.Header.Get("Git-Protocol")})
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		started := time.Now()
		value, e := runProcess(ctx, root, env, body, git, args...)
		if os.Getenv("GIT_FIXTURE_TIMING") != "" {
			fmt.Fprintf(a.err, "%s upload-pack %d ms in=%d out=%d\n", r.Method, time.Since(started).Milliseconds(), len(body), len(value.stdout))
		}
		if e != nil || value.code != 0 {
			http.Error(w, "reference upload-pack failed", 500)
			return
		}
		kind := "application/x-git-upload-pack-result"
		if r.Method == "GET" {
			kind = "application/x-git-upload-pack-advertisement"
			value.stdout = append([]byte("001e# service=git-upload-pack\n0000"), value.stdout...)
		}
		w.Header().Set("Content-Type", kind)
		_, _ = w.Write(value.stdout)
	})
	listener, e := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if e != nil {
		return e
	}
	defer listener.Close()
	fmt.Fprintf(a.out, "serving %s on http://%s/\n", root, listener.Addr())
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 2 * time.Minute, WriteTimeout: 2 * time.Minute, MaxHeaderBytes: 64 << 10}
	return server.Serve(listener)
}
func init() {
	register("git-http-fixture", "Read-only bounded loopback smart-HTTP benchmark remote", "root port reference", serveGitHTTP)
}
