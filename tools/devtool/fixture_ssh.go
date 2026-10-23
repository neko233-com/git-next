// Copyright 2026 git-next contributors. MIT license.
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type sshFixture struct {
	URL, KeyFile, Known string
	listener            net.Listener
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	mu                  sync.Mutex
	connections         map[net.Conn]bool
	authenticated       int
	commands            map[string]int
	semaphore           chan struct{}
}

func newSSHFixture(root, target, reference string, env []string) (*sshFixture, error) {
	hostKey, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return nil, e
	}
	identity, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return nil, e
	}
	host, e := ssh.NewSignerFromKey(hostKey)
	if e != nil {
		return nil, e
	}
	user, e := ssh.NewSignerFromKey(identity)
	if e != nil {
		return nil, e
	}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(context.Background())
	f := &sshFixture{listener: listener, cancel: cancel, connections: map[net.Conn]bool{}, commands: map[string]int{}, semaphore: make(chan struct{}, 8), KeyFile: filepath.Join(root, "identity"), Known: filepath.Join(root, "known_hosts")}
	keyDER := x509.MarshalPKCS1PrivateKey(identity)
	if e = write(f.KeyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: keyDER}), 0600); e != nil {
		f.close()
		return nil, e
	}
	address := listener.Addr().(*net.TCPAddr)
	f.URL = fmt.Sprintf("ssh://fixture@127.0.0.1:%d/repository.git", address.Port)
	if e = os.WriteFile(f.Known, []byte(fmt.Sprintf("[127.0.0.1]:%d %s", address.Port, ssh.MarshalAuthorizedKey(host.PublicKey()))), 0600); e != nil {
		f.close()
		return nil, e
	}
	config := &ssh.ServerConfig{MaxAuthTries: 3, PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		if c.User() == "fixture" && string(key.Marshal()) == string(user.PublicKey().Marshal()) {
			f.mu.Lock()
			f.authenticated++
			f.mu.Unlock()
			return nil, nil
		}
		return nil, fmt.Errorf("fixture identity rejected")
	}}
	config.AddHostKey(host)
	f.wg.Go(func() {
		for {
			conn, e := listener.Accept()
			if e != nil {
				return
			}
			select {
			case f.semaphore <- struct{}{}:
			default:
				_ = conn.Close()
				continue
			}
			f.mu.Lock()
			f.connections[conn] = true
			f.mu.Unlock()
			f.wg.Go(func() { defer func() { <-f.semaphore }(); f.serve(ctx, conn, config, root, target, reference, env) })
		}
	})
	return f, nil
}
func (f *sshFixture) serve(ctx context.Context, conn net.Conn, config *ssh.ServerConfig, root, target, reference string, env []string) {
	defer conn.Close()
	defer func() { f.mu.Lock(); delete(f.connections, conn); f.mu.Unlock() }()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	server, channels, requests, e := ssh.NewServerConn(conn, config)
	if e != nil {
		return
	}
	defer server.Close()
	go ssh.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			_ = channel.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		stream, requests, e := channel.Accept()
		if e != nil {
			return
		}
		f.wg.Go(func() {
			defer stream.Close()
			sessionEnv := append([]string{}, env...)
			for request := range requests {
				if request.Type == "env" {
					var value struct{ Name, Value string }
					valid := ssh.Unmarshal(request.Payload, &value) == nil && value.Name == "GIT_PROTOCOL" && len(value.Value) <= 1024
					if valid {
						sessionEnv = fixtureEnv(sessionEnv, map[string]string{"GIT_PROTOCOL": value.Value})
					}
					_ = request.Reply(valid, nil)
					continue
				}
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if ssh.Unmarshal(request.Payload, &payload) != nil {
					_ = request.Reply(false, nil)
					return
				}
				command := strings.Fields(payload.Command)
				if len(command) != 2 || (command[0] != "git-receive-pack" && command[0] != "git-upload-pack") || strings.Trim(command[1], "'\"") != "/repository.git" {
					_ = request.Reply(false, nil)
					return
				}
				_ = request.Reply(true, nil)
				f.mu.Lock()
				f.commands[command[0]]++
				f.mu.Unlock()
				code := fixtureSSHProcess(ctx, root, sessionEnv, reference, strings.TrimPrefix(command[0], "git-"), target, stream)
				status := make([]byte, 4)
				binary.BigEndian.PutUint32(status, uint32(code))
				_, _ = stream.SendRequest("exit-status", false, status)
				return
			}
		})
	}
}
func (f *sshFixture) statSnapshot() record {
	f.mu.Lock()
	defer f.mu.Unlock()
	commands := map[string]int{}
	for name, count := range f.commands {
		commands[name] = count
	}
	return record{"authenticated": f.authenticated, "commands": commands}
}
func fixtureSSHProcess(parent context.Context, root string, env []string, reference, service, target string, channel ssh.Channel) int {
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, reference, service, target)
	cmd.Dir = root
	cmd.Env = env
	cmd.Stdin = channel
	cmd.Stdout = channel
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	cleanup, e := prepareProcess(cmd)
	if e != nil {
		return 127
	}
	defer cleanup()
	if e = cmd.Start(); e != nil {
		return 127
	}
	if e = attachProcess(cmd); e != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return 127
	}
	if e = cmd.Wait(); e != nil {
		if exit, ok := e.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return 127
	}
	return 0
}
func (f *sshFixture) close() {
	if f.cancel != nil {
		f.cancel()
	}
	if f.listener != nil {
		_ = f.listener.Close()
	}
	f.mu.Lock()
	for conn := range f.connections {
		_ = conn.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}
