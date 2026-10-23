// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"golang.org/x/crypto/ssh"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

func init() {
	register("migration-server", "Seed and serve disposable reference accounts for Linux migration acceptance", "", migrationServer)
	register("migration-client", "Verify migrated HTTPS/SSH/configuration/LF behavior without installed Git", "", migrationClient)
	register("migration-host", "Run isolated migration client/server containers and clean exact owned resources", "native-package output go", migrationHost)
}
func migrationEnvironment(home, gcm string) []string {
	var env []string
	for _, item := range cleanEnvironment(home) {
		key, _, _ := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		if strings.HasPrefix(upper, "GCM_") || strings.HasPrefix(upper, "SSH_") {
			continue
		}
		env = append(env, item)
	}
	return fixtureEnv(env, map[string]string{"GIT_AUTHOR_NAME": "Fixture", "GIT_COMMITTER_NAME": "Fixture", "GIT_AUTHOR_EMAIL": "fixture@example.invalid", "GIT_COMMITTER_EMAIL": "fixture@example.invalid", "GIT_CONFIG_GLOBAL": filepath.Join(home, ".gitconfig"), "GCM_PROVIDER": "generic", "GCM_INTERACTIVE": "never", "GCM_AUTODETECT_TIMEOUT": "-1", "GCM_CREDENTIAL_STORE": "plaintext", "GCM_PLAINTEXT_STORE_PATH": filepath.Join(home, ".gcm/store"), "PATH": gcm + string(os.PathListSeparator) + os.Getenv("PATH")})
}
func migrationRun(program, cwd string, env []string, input []byte, wantSuccess bool, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	r, e := runProcess(ctx, cwd, env, input, program, args...)
	if e != nil {
		return nil, fmt.Errorf("migration subprocess failed or timed out")
	}
	if (r.code == 0) != wantSuccess {
		return nil, fmt.Errorf("migration command expectation failed: %s exit=%d", filepathBase(program), r.code)
	}
	return r.stdout, nil
}
func migrationJSON(path string, value any) error {
	data, e := jsonBytes(value)
	if e != nil {
		return e
	}
	temporary := path + ".tmp"
	if e = write(temporary, append(data, '\n'), 0600); e != nil {
		return e
	}
	return os.Rename(temporary, path)
}
func migrationSnapshot(root string) (record, error) {
	hashes, e := hashes(root)
	if e != nil {
		return nil, e
	}
	r := record{}
	for name, hash := range hashes {
		r[name] = hash
	}
	return r, nil
}
func migrationServer(a *app, _ options) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("migration server runs only in the owned Linux fixture")
	}
	const root = "/migration"
	const private = "/tmp/reference"
	home := root + "/home"
	gcm := "/tmp/gcm"
	if _, e := newDestination(private); e != nil {
		return e
	}
	if e := os.MkdirAll(home+"/.ssh", 0700); e != nil {
		return e
	}
	if e := copyOwnedTree("/inputs/gcm", gcm); e != nil {
		return e
	}
	if e := os.Chmod(gcm+"/git-credential-manager", 0755); e != nil {
		return e
	}
	env := migrationEnvironment(home, gcm)
	reference, e := exec.LookPath("git")
	if e != nil {
		return e
	}
	git := func(cwd string, args ...string) ([]byte, error) {
		return migrationRun(reference, cwd, env, nil, true, args...)
	}
	target := private + "/repository.git"
	seed := private + "/seed"
	for _, item := range []struct {
		cwd  string
		args []string
	}{{private, []string{"init", "--bare", "-b", "main", target}}, {target, []string{"config", "http.receivepack", "true"}}, {private, []string{"init", "-b", "main", seed}}} {
		if _, e = git(item.cwd, item.args...); e != nil {
			return e
		}
	}
	for name, data := range map[string][]byte{".gitattributes": []byte("* text=auto eol=lf\n*.bytes -text\n"), "scene.unity": []byte("%YAML 1.1\nfixture: original\n"), "data.bytes": {0, '\r', '\n', 255, '\n'}} {
		if e = write(filepath.Join(seed, name), data, 0644); e != nil {
			return e
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "fixture: before migration"}, {"remote", "add", "origin", target}, {"push", "origin", "main"}} {
		if _, e = git(seed, args...); e != nil {
			return e
		}
	}
	initial, e := git(seed, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	execPath, e := git(private, "--exec-path")
	if e != nil {
		return e
	}
	password := fixtureSecret()
	http, e := newGitHTTPFixture(private, filepath.Join(strings.TrimSpace(string(execPath)), "git-http-backend"), env, password)
	if e != nil {
		return e
	}
	defer http.close()
	if e = copyFile(http.CA, root+"/fixture-ca.pem"); e != nil {
		return e
	}
	sshFixture, e := newSSHFixture(private, target, reference, env)
	if e != nil {
		return e
	}
	defer sshFixture.close()
	for source, dest := range map[string]string{sshFixture.KeyFile: home + "/.ssh/id_rsa", sshFixture.Known: home + "/.ssh/known_hosts"} {
		if e = copyFile(source, dest); e != nil {
			return e
		}
	}
	if e = os.Chmod(home+"/.ssh/id_rsa", 0600); e != nil {
		return e
	}
	sshURL, e := url.Parse(sshFixture.URL)
	if e != nil {
		return e
	}
	port := sshURL.Port()
	if e = write(home+"/.ssh/config", []byte("Host fixture\n HostName 127.0.0.1\n User fixture\n Port "+port+"\n IdentitiesOnly yes\n IdentityFile ~/.ssh/id_rsa\n"), 0600); e != nil {
		return e
	}
	wrong, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return e
	}
	signer, e := ssh.NewSignerFromKey(wrong)
	if e != nil {
		return e
	}
	if e = write(root+"/changed-hosts", []byte("[127.0.0.1]:"+port+" "+string(ssh.MarshalAuthorizedKey(signer.PublicKey()))), 0600); e != nil {
		return e
	}
	if e = write(root+"/unknown-hosts", nil, 0600); e != nil {
		return e
	}
	der, e := x509.MarshalPKCS8PrivateKey(wrong)
	if e != nil {
		return e
	}
	if e = write(root+"/wrong-identity", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); e != nil {
		return e
	}
	config := "[user]\n name = Fixture\n email = fixture@example.invalid\n[core]\n autocrlf = input\n eol = lf\n[credential]\n helper = manager\n username = fixture\n[http]\n sslCAInfo = /migration/fixture-ca.pem\n"
	if e = write(home+"/.gitconfig", []byte(config), 0600); e != nil {
		return e
	}
	parsed, e := url.Parse(http.URL)
	if e != nil {
		return e
	}
	credential := []byte("protocol=https\nhost=" + parsed.Host + "\nusername=fixture\npassword=" + password + "\n\n")
	if _, e = migrationRun(gcm+"/git-credential-manager", root, fixtureEnv(env, map[string]string{"GIT_EXEC_PATH": "/usr/bin"}), credential, true, "store"); e != nil {
		return fmt.Errorf("isolated GCM seeding failed")
	}
	credentialFiles, e := files(home + "/.gcm/store")
	if e != nil || len(credentialFiles) == 0 {
		return fmt.Errorf("isolated credential was not saved")
	}
	worktree := root + "/old-worktree"
	if _, e = git(private, "clone", target, worktree); e != nil {
		return e
	}
	for _, args := range [][]string{{"remote", "set-url", "origin", http.URL}, {"remote", "add", "ssh", "ssh://fixture/repository.git"}, {"ls-remote", "origin"}} {
		if _, e = git(worktree, args...); e != nil {
			return e
		}
	}
	profile, e := migrationSnapshot(home)
	if e != nil {
		return e
	}
	working, e := migrationSnapshot(worktree)
	if e != nil {
		return e
	}
	version, e := git(private, "--version")
	if e != nil {
		return e
	}
	if e = migrationJSON(root+"/ready.json", record{"initial_head": strings.TrimSpace(string(initial)), "https_url": http.URL, "ssh_url": "ssh://fixture/repository.git", "profile_before": profile, "worktree_before": working, "reference_version": strings.TrimSpace(string(version)), "ssh_port": port}); e != nil {
		return e
	}
	limit := time.Now().Add(9 * time.Minute)
	for sequence := 1; time.Now().Before(limit); {
		request := fmt.Sprintf("%s/request-%d.json", root, sequence)
		if !exists(request) {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		r, e := load(request)
		if e != nil {
			return e
		}
		action := stringValue(r, "action")
		switch action {
		case "advance":
			for _, args := range [][]string{{"fetch", "origin"}, {"reset", "--hard", "origin/main"}} {
				if _, e = git(seed, args...); e != nil {
					return e
				}
			}
			if e = write(seed+"/from-server.txt", []byte("fixture server advance\n"), 0644); e != nil {
				return e
			}
			for _, args := range [][]string{{"add", "-A"}, {"commit", "-m", "fixture: server advance"}, {"push", "origin", "main"}} {
				if _, e = git(seed, args...); e != nil {
					return e
				}
			}
		case "reject-password":
			http.mu.Lock()
			http.password = fixtureSecret()
			http.mu.Unlock()
		case "restore-password":
			http.mu.Lock()
			http.password = password
			http.mu.Unlock()
		case "inspect", "finish":
		default:
			return fmt.Errorf("unknown migration control action")
		}
		if _, e = git(target, "fsck", "--full"); e != nil {
			return e
		}
		worktrees, e := filepath.Glob(root + "/*worktree*")
		if e != nil {
			return e
		}
		for _, w := range worktrees {
			if fixtureDir(w + "/.git") {
				if _, e = git(w, "fsck", "--full"); e != nil {
					return e
				}
			}
		}
		head, e := git(target, "rev-parse", "refs/heads/main")
		if e != nil {
			return e
		}
		refs, e := git(private, "ls-remote", target)
		if e != nil {
			return e
		}
		lines := strings.Split(strings.TrimSpace(string(refs)), "\n")
		sort.Strings(lines)
		http.mu.Lock()
		authenticated := http.authenticated
		http.mu.Unlock()
		sshStats := sshFixture.statSnapshot()
		if e = migrationJSON(fmt.Sprintf("%s/response-%d.json", root, sequence), record{"head": strings.TrimSpace(string(head)), "refs": lines, "https_authenticated_requests": authenticated, "ssh_authenticated_sessions": sshStats["authenticated"], "ssh_commands": sshStats["commands"], "fsck": "pass"}); e != nil {
			return e
		}
		if action == "finish" {
			return nil
		}
		sequence++
	}
	return fmt.Errorf("migration reference aggregate deadline exceeded")
}

func migrationClient(a *app, _ options) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("migration client runs only in the owned Linux fixture")
	}
	const root = "/migration"
	home := root + "/home"
	worktree := root + "/old-worktree"
	for _, p := range []string{"/usr/bin/git", "/usr/bin/ssh", "/usr/lib/git-core"} {
		if exists(p) {
			return fmt.Errorf("client image includes original Git/OpenSSH")
		}
	}
	for _, name := range []string{"git", "ssh", "python", "python3"} {
		if _, e := exec.LookPath(name); e == nil {
			return fmt.Errorf("client image includes unowned executable: %s", name)
		}
	}
	for _, name := range []string{"git", "git-lfs", "openssh-client", "python3"} {
		ctx, cancel := deadline()
		r, e := runProcess(ctx, root, nil, nil, "dpkg-query", "-W", "-f=${db:Status-Status}", name)
		cancel()
		if e == nil && string(r.stdout) == "installed" {
			return fmt.Errorf("client includes an original runtime package: %s", name)
		}
	}
	native := "/tmp/native"
	if _, e := newDestination(native); e != nil {
		return e
	}
	full := os.Getenv("GIT_NEXT_NATIVE_PACKAGE") == "1"
	gcm := "/tmp/gcm"
	client := native + "/git"
	if full {
		if _, _, e := extractArchive("/inputs/native-package.tar.gz", native, extractPolicy{links: true, maxFiles: 100000, maxBytes: 2 << 30}); e != nil {
			return e
		}
		client = native + "/bin/git"
		gcm = native + "/runtime/usr/lib/git-next/gcm"
	} else {
		for _, name := range []string{"git-next", "git-lfs", "git-next-lfs-filter", "git-terminal"} {
			if e := copyFile("/inputs/native/"+name, native+"/"+name); e != nil {
				return e
			}
			if e := os.Chmod(native+"/"+name, 0755); e != nil {
				return e
			}
		}
		if e := copyFile(native+"/git-next", client); e != nil {
			return e
		}
		if e := copyOwnedTree("/inputs/gcm", gcm); e != nil {
			return e
		}
		if e := os.Chmod(gcm+"/git-credential-manager", 0755); e != nil {
			return e
		}
	}
	locator := record{"schema_version": 1, "product": "git-next", "credential_helper": gcm + "/git-credential-manager", "native_callback_directory": native}
	if e := writeJSON(native+"/git-next-support.json", locator); e != nil {
		return e
	}
	empty := "/tmp/empty-path"
	if e := os.Mkdir(empty, 0700); e != nil {
		return e
	}
	env := fixtureEnv(migrationEnvironment(home, gcm), map[string]string{"GIT_OPTIONAL_LOCKS": "0", "GIT_EXEC_PATH": empty, "PATH": empty, "GIT_SSH": empty + "/absent-ssh", "GIT_SSH_COMMAND": empty + "/absent-ssh"})
	if full {
		filtered := env[:0]
		for _, v := range env {
			k, _, _ := strings.Cut(v, "=")
			if k != "GIT_EXEC_PATH" && k != "GIT_SSH" && k != "GIT_SSH_COMMAND" {
				filtered = append(filtered, v)
			}
		}
		env = fixtureEnv(filtered, map[string]string{"PATH": native + "/bin:" + native + "/runtime/usr/bin:/usr/local/bin:/usr/bin:/bin"})
	}
	ready, e := load(root + "/ready.json")
	if e != nil {
		return e
	}
	var checks []string
	run := func(repo string, success bool, extra map[string]string, args ...string) ([]byte, error) {
		return migrationRun(client, repo, fixtureEnv(env, extra), nil, success, args...)
	}
	must := func(repo string, args ...string) ([]byte, error) { return run(repo, true, nil, args...) }
	sequence := 0
	control := func(action string) (record, error) {
		sequence++
		if e := migrationJSON(fmt.Sprintf("%s/request-%d.json", root, sequence), record{"action": action}); e != nil {
			return nil, e
		}
		response := fmt.Sprintf("%s/response-%d.json", root, sequence)
		limit := time.Now().Add(40 * time.Second)
		for !exists(response) {
			if time.Now().After(limit) {
				return nil, fmt.Errorf("migration control deadline exceeded")
			}
			time.Sleep(20 * time.Millisecond)
		}
		r, e := load(response)
		if e != nil {
			return nil, e
		}
		if stringValue(r, "fsck") != "pass" {
			return nil, fmt.Errorf("reference fsck failed")
		}
		return r, nil
	}
	stable := func() (record, error) {
		r, e := migrationSnapshot(worktree)
		if full {
			delete(r, ".git/FETCH_HEAD")
		}
		return r, e
	}
	preserved := func(extra map[string]string, args ...string) error {
		before, e := stable()
		if e != nil {
			return e
		}
		profile, e := migrationSnapshot(home)
		if e != nil {
			return e
		}
		if _, e = run(worktree, false, extra, args...); e != nil {
			return e
		}
		after, e := stable()
		if e != nil {
			return e
		}
		now, e := migrationSnapshot(home)
		if e != nil {
			return e
		}
		if !equalJSON(before, after) || !equalJSON(profile, now) {
			return fmt.Errorf("failed native operation changed migrated repository or profile")
		}
		locks, _ := filepath.Glob(worktree + "/.git/*.lock")
		if len(locks) > 0 {
			return fmt.Errorf("failed operation left writer locks")
		}
		return nil
	}
	version, e := must(worktree, "--version")
	if e != nil {
		return e
	}
	if !bytes.Contains(version, []byte("git-next")) {
		return fmt.Errorf("unbranded migration client")
	}
	profile, e := migrationSnapshot(home)
	if e != nil {
		return e
	}
	working, e := migrationSnapshot(worktree)
	if e != nil {
		return e
	}
	head, e := must(worktree, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	status, e := must(worktree, "status", "--porcelain=v1", "-z")
	if e != nil {
		return e
	}
	if !equalJSON(profile, ready["profile_before"]) || !equalJSON(working, ready["worktree_before"]) || strings.TrimSpace(string(head)) != stringValue(ready, "initial_head") || len(status) > 0 {
		return fmt.Errorf("original repository/profile snapshot changed")
	}
	now, e := migrationSnapshot(worktree)
	if e != nil {
		return e
	}
	if !equalJSON(now, working) {
		return fmt.Errorf("read-only status changed old repository")
	}
	checks = append(checks, "old Git repository/index/config read directly with branded client and optional locks disabled")
	scene := worktree + "/scene.unity"
	data, e := os.ReadFile(scene)
	if e != nil {
		return e
	}
	if e = write(scene, bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")), 0644); e != nil {
		return e
	}
	status, e = must(worktree, "status", "--porcelain=v1", "-z")
	if e != nil {
		return e
	}
	diff, e := must(worktree, "diff", "--exit-code")
	if e != nil {
		return e
	}
	binary, e := os.ReadFile(worktree + "/data.bytes")
	if e != nil {
		return e
	}
	if len(status) > 0 || len(diff) > 0 || !bytes.Equal(binary, []byte{0, '\r', '\n', 255, '\n'}) {
		return fmt.Errorf("CRLF-only save or binary bytes changed")
	}
	data, e = os.ReadFile(scene)
	if e != nil {
		return e
	}
	if e = write(scene, bytes.ReplaceAll(data, []byte("original"), []byte("real edit")), 0644); e != nil {
		return e
	}
	status, e = must(worktree, "status", "--porcelain=v1", "-z")
	if e != nil {
		return e
	}
	if string(status) != " M scene.unity\x00" {
		return fmt.Errorf("real text edit was hidden")
	}
	if _, e = must(worktree, "add", "scene.unity"); e != nil {
		return e
	}
	staged, e := must(worktree, "show", ":scene.unity")
	if e != nil {
		return e
	}
	if bytes.ContainsRune(staged, '\r') {
		return fmt.Errorf("staged text is not canonical LF")
	}
	if _, e = must(worktree, "commit", "-m", "fixture: native post-migration HTTPS"); e != nil {
		return e
	}
	checks = append(checks, "CRLF-only saves clean; real edits visible; canonical LF staging and exact binary bytes")
	inspect, e := control("inspect")
	if e != nil {
		return e
	}
	refs, e := must(worktree, "ls-remote", stringValue(ready, "https_url"))
	if e != nil {
		return e
	}
	lines := strings.Split(strings.TrimSpace(string(refs)), "\n")
	sort.Strings(lines)
	if !equalJSON(lines, inspect["refs"]) {
		return fmt.Errorf("authenticated HTTPS refs differ")
	}
	if _, e = must(worktree, "push", "origin", "main"); e != nil {
		return e
	}
	head, e = must(worktree, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	inspect, e = control("inspect")
	if e != nil {
		return e
	}
	if stringValue(inspect, "head") != strings.TrimSpace(string(head)) {
		return fmt.Errorf("HTTPS push did not update remote")
	}
	checks = append(checks, "migrated manager helper authenticates private HTTPS reads and push")
	httpsClone := root + "/https-worktree"
	if _, e = must(root, "clone", stringValue(ready, "https_url"), httpsClone); e != nil {
		return e
	}
	cloned, e := must(httpsClone, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	sceneData, e := os.ReadFile(httpsClone + "/scene.unity")
	if e != nil {
		return e
	}
	binary, e = os.ReadFile(httpsClone + "/data.bytes")
	if e != nil {
		return e
	}
	if !bytes.Equal(head, cloned) || bytes.ContainsRune(sceneData, '\r') || !bytes.Equal(binary, []byte{0, '\r', '\n', 255, '\n'}) {
		return fmt.Errorf("HTTPS clone checkout differs")
	}
	checks = append(checks, "authenticated HTTPS clone preserves LF text and exact binary bytes")
	advanced, e := control("advance")
	if e != nil {
		return e
	}
	remoteHead := stringValue(advanced, "head")
	for _, args := range [][]string{{"fetch", "origin"}, {"pull", "--ff-only", "origin"}} {
		if _, e = must(worktree, args...); e != nil {
			return e
		}
	}
	head, e = must(worktree, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	remoteRef, e := must(worktree, "rev-parse", "origin/main")
	if e != nil {
		return e
	}
	fromServer, e := read(worktree + "/from-server.txt")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(head)) != remoteHead || strings.TrimSpace(string(remoteRef)) != remoteHead || fromServer != "fixture server advance\n" {
		return fmt.Errorf("incremental HTTPS fetch/pull differs")
	}
	checks = append(checks, "authenticated incremental fetch and ff-only pull preserve migrated repository")
	inspect, e = control("inspect")
	if e != nil {
		return e
	}
	beforeRemote := inspect["head"]
	loginFiles, e := files(home + "/.gcm/store")
	if e != nil {
		return e
	}
	saved := map[string][]byte{}
	for _, file := range loginFiles {
		data, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		saved[file] = data
	}
	if _, e = control("reject-password"); e != nil {
		return e
	}
	if full {
		before, e := stable()
		if e != nil {
			return e
		}
		for _, args := range [][]string{{"fetch", "origin"}, {"push", "origin", "main"}} {
			if _, e = run(worktree, false, nil, args...); e != nil {
				return e
			}
		}
		after, e := stable()
		if e != nil {
			return e
		}
		if !equalJSON(before, after) {
			return fmt.Errorf("rejected HTTPS login changed repository")
		}
	} else {
		if e = preserved(nil, "fetch", "origin"); e != nil {
			return e
		}
		if e = preserved(nil, "push", "origin", "main"); e != nil {
			return e
		}
	}
	if _, e = control("restore-password"); e != nil {
		return e
	}
	if full {
		for file, data := range saved {
			if e = write(file, data, 0600); e != nil {
				return e
			}
		}
	}
	inspect, e = control("inspect")
	if e != nil {
		return e
	}
	if !equalJSON(beforeRemote, inspect["head"]) {
		return fmt.Errorf("rejected login changed remote ref")
	}
	checks = append(checks, "rejected HTTPS login preserves repository/remote refs; only generated fixture login restored after native helper invalidation")
	if e = preserved(map[string]string{"GCM_PLAINTEXT_STORE_PATH": "/tmp/missing-login"}, "fetch", "origin"); e != nil {
		return e
	}
	checks = append(checks, "missing login refuses without prompting, repository changes or credential deletion")
	if e = preserved(nil, "-c", "http.sslCAInfo=/etc/ssl/certs/ca-certificates.crt", "fetch", "origin"); e != nil {
		return e
	}
	checks = append(checks, "untrusted TLS certificate refuses with verification enabled")
	if full {
		helper := gcm + "/git-credential-manager"
		if e = os.Rename(helper, helper+".fixture-disabled"); e != nil {
			return e
		}
		probeErr := preserved(nil, "fetch", "origin")
		restoreErr := os.Rename(helper+".fixture-disabled", helper)
		if restoreErr != nil {
			return restoreErr
		}
		if probeErr != nil {
			return probeErr
		}
	} else {
		locator["native_callback_directory"] = "/tmp/missing-native-callback"
		if e = writeJSON(native+"/git-next-support.json", locator); e != nil {
			return e
		}
		probeErr := preserved(nil, "fetch", "origin")
		locator["native_callback_directory"] = native
		if e = writeJSON(native+"/git-next-support.json", locator); e != nil {
			return e
		}
		if probeErr != nil {
			return probeErr
		}
	}
	checks = append(checks, "incomplete managed support refuses without original Git fallback or profile/repository writes")
	inspect, e = control("inspect")
	if e != nil {
		return e
	}
	refs, e = must(worktree, "ls-remote", stringValue(ready, "ssh_url"))
	if e != nil {
		return e
	}
	lines = strings.Split(strings.TrimSpace(string(refs)), "\n")
	sort.Strings(lines)
	if !equalJSON(lines, inspect["refs"]) {
		return fmt.Errorf("SSH refs differ")
	}
	sshClone := root + "/ssh-worktree"
	if _, e = must(root, "clone", stringValue(ready, "ssh_url"), sshClone); e != nil {
		return e
	}
	cloned, e = must(sshClone, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	if strings.TrimSpace(string(cloned)) != remoteHead {
		return fmt.Errorf("SSH clone head differs")
	}
	if e = write(sshClone+"/ssh.txt", []byte("native SSH post-migration\n"), 0644); e != nil {
		return e
	}
	for _, args := range [][]string{{"add", "ssh.txt"}, {"commit", "-m", "fixture: native post-migration SSH"}, {"push", "origin", "main"}} {
		if _, e = must(sshClone, args...); e != nil {
			return e
		}
	}
	inspect, e = control("inspect")
	if e != nil {
		return e
	}
	cloned, e = must(sshClone, "rev-parse", "HEAD")
	if e != nil {
		return e
	}
	if stringValue(inspect, "head") != strings.TrimSpace(string(cloned)) {
		return fmt.Errorf("SSH push differs")
	}
	if _, e = must(worktree, "fetch", "ssh"); e != nil {
		return e
	}
	checks = append(checks, "migrated SSH config/private key/known_hosts authenticate read, clone, fetch and push")
	for _, hosts := range []string{"unknown-hosts", "changed-hosts"} {
		if full {
			command := shellQuote(native+"/runtime/usr/bin/ssh") + " -oStrictHostKeyChecking=yes -oUserKnownHostsFile=" + shellQuote(root+"/"+hosts)
			e = preserved(nil, "-c", "core.sshCommand="+command, "fetch", "ssh")
		} else {
			e = preserved(nil, "--ssh-known-hosts", root+"/"+hosts, "fetch", "ssh")
		}
		if e != nil {
			return e
		}
	}
	if full {
		command := shellQuote(native+"/runtime/usr/bin/ssh") + " -F /dev/null -oHostName=127.0.0.1 -l fixture -p " + stringValue(ready, "ssh_port") + " -oIdentitiesOnly=yes -oStrictHostKeyChecking=yes -oUserKnownHostsFile=" + shellQuote(home+"/.ssh/known_hosts") + " -i " + shellQuote(root+"/wrong-identity")
		e = preserved(nil, "-c", "core.sshCommand="+command, "fetch", "ssh")
	} else {
		e = preserved(nil, "--ssh-identity", root+"/wrong-identity", "fetch", "ssh")
	}
	if e != nil {
		return e
	}
	checks = append(checks, "unknown/changed host keys and rejected identity refuse while preserving repository/profile")
	profile, e = migrationSnapshot(home)
	if e != nil {
		return e
	}
	if !equalJSON(profile, ready["profile_before"]) {
		return fmt.Errorf("migration changed original profile bytes")
	}
	lockFound := false
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(p, ".lock") {
			lockFound = true
		}
		return e
	})
	if lockFound {
		return fmt.Errorf("writer lock remains")
	}
	final, e := control("finish")
	if e != nil {
		return e
	}
	if number(final, "https_authenticated_requests") == 0 || number(final, "ssh_authenticated_sessions") == 0 {
		return fmt.Errorf("authenticated transport was not exercised")
	}
	for _, name := range []string{"git-upload-pack", "git-receive-pack"} {
		if number(object(final, "ssh_commands"), name) == 0 {
			return fmt.Errorf("SSH service was not exercised")
		}
	}
	checks = append(checks, "reference fsck validates all four repositories; no writer locks remain")
	trace, e := read("/tmp/client-exec.trace")
	if e != nil {
		return e
	}
	executables := traceExecutables(trace)
	hasClient, hasGCM := false, false
	for _, p := range executables {
		base := filepath.Base(p)
		if wordSet("git git-next git-lfs git-next-lfs-filter git-terminal")[base] && !strings.HasPrefix(p, native+"/") {
			return fmt.Errorf("unowned Git executable in audit")
		}
		if wordSet("ssh git-upload-pack git-receive-pack git-http-backend")[base] && (!full || !strings.HasPrefix(p, native+"/")) {
			return fmt.Errorf("unowned transport executable in audit")
		}
		if base == "python" || strings.HasPrefix(base, "python3") {
			return fmt.Errorf("Python executed in migration client")
		}
		hasClient = hasClient || p == client
		hasGCM = hasGCM || p == gcm+"/git-credential-manager"
	}
	if !hasClient || !hasGCM {
		return fmt.Errorf("native client/GCM absent from execution audit")
	}
	checks = append(checks, "client image excludes installed Git/OpenSSH/Python; execve audit verifies owned core/transport/helper paths")
	gitHash, e := digest(client)
	if e != nil {
		return e
	}
	gcmHash, e := digest(gcm + "/git-credential-manager")
	if e != nil {
		return e
	}
	return a.json(record{"status": "pass", "scope": "Linux x64 isolated Docker migration/authentication/LF subset; generated accounts only", "binary_sha256": gitHash, "gcm_binary_sha256": gcmHash, "native_version": strings.TrimSpace(string(version)), "reference_version": ready["reference_version"], "checks": checks, "https_authenticated_requests": final["https_authenticated_requests"], "ssh_authenticated_sessions": final["ssh_authenticated_sessions"], "ssh_commands": final["ssh_commands"], "client_executables": executables, "profile_bytes_preserved": true, "real_account_accessed": false, "full_git_takeover_accepted": false})
}
