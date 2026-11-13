package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func sftpTestServer(t *testing.T) (string, int, string, string) {
	t.Helper()
	_, hostKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	hostSigner, e := ssh.NewSignerFromKey(hostKey)
	if e != nil {
		t.Fatal(e)
	}
	_, userKey, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	signer, e := ssh.NewSignerFromKey(userKey)
	if e != nil {
		t.Fatal(e)
	}
	der, e := x509.MarshalPKCS8PrivateKey(userKey)
	if e != nil {
		t.Fatal(e)
	}
	private := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if c.User() == "tester" && string(p) == "sftp-secret" {
			return nil, nil
		}
		return nil, fmt.Errorf("bad password")
	}, PublicKeyCallback: func(c ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if c.User() == "tester" && string(k.Marshal()) == string(signer.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("bad key")
	}}
	config.AddHostKey(hostSigner)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			go func() {
				c, chans, reqs, e := ssh.NewServerConn(conn, config)
				if e != nil {
					conn.Close()
					return
				}
				defer c.Close()
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					if ch.ChannelType() != "session" {
						ch.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					channel, requests, e := ch.Accept()
					if e != nil {
						continue
					}
					go func() {
						for r := range requests {
							if r.Type == "subsystem" && len(r.Payload) >= 4 && string(r.Payload[4:]) == "sftp" {
								r.Reply(true, nil)
								server, e := sftp.NewServer(channel)
								if e == nil {
									server.Serve()
									server.Close()
								}
								return
							}
							r.Reply(false, nil)
						}
					}()
				}
			}()
		}
	}()
	known := fmt.Sprintf("[127.0.0.1]:%d %s", port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))))
	return "127.0.0.1", port, known, private
}
func TestSFTPRealConnectionAndKopia(t *testing.T) {
	binary := os.Getenv("CANGZHOU_TEST_KOPIA")
	if binary == "" {
		t.Skip("requires actual Kopia")
	}
	host, port, known, key := sftpTestServer(t)
	root := t.TempDir()
	for _, private := range []bool{false, true} {
		relative := fmt.Sprintf("repo-%t/nested", private)
		repo := filepath.Join(root, relative)
		v := url.Values{"name": {"sftp"}, "host": {host}, "port": {strconv.Itoa(port)}, "remote_path": {root}, "user": {"tester"}, "known_hosts": {known}}
		if private {
			v.Set("private_key", key)
		} else {
			v.Set("password", "sftp-secret")
		}
		r := httptest.NewRequest("POST", "/", nil)
		r.PostForm = v
		d, e := parseSFTP(r, DAVConfig{})
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		e = testSFTP(ctx, d)
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		_, otherKey, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		otherSigner, e := ssh.NewSignerFromKey(otherKey)
		if e != nil {
			t.Fatal(e)
		}
		wrong := d
		wrong.KnownHosts = fmt.Sprintf("[127.0.0.1]:%d %s", port, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(otherSigner.PublicKey()))))
		badCtx, badCancel := context.WithTimeout(context.Background(), 10*time.Second)
		badErr := testSFTP(badCtx, wrong)
		badCancel()
		if badErr == nil {
			t.Fatal("wrong host key accepted")
		}
		if strings.Contains(cleanDAV(d).PrivateKey, "PRIVATE") {
			t.Fatal("private key leaked")
		}
		dir := t.TempDir()
		a := &App{dir: dir, kopia: binary}
		baseURL, _ := url.Parse(d.URL)
		target, e := repositoryURL(baseURL, relative)
		if e != nil {
			t.Fatal(e)
		}
		cfg := Config{ID: "0123456789abcdef", URL: target, TargetPath: relative, User: d.User, WebDAVPassword: d.Password, RepoPassword: "test-password-123"}
		if _, e = a.prepareJob(cfg.ID); e != nil {
			t.Fatal(e)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 45*time.Second)
		_, e = a.connectRemote(ctx, &cfg, d, "create")
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if b, e := os.ReadFile(filepath.Join(dir, "jobs", cfg.ID, "repository.config")); e != nil || len(b) == 0 {
			t.Fatal(e)
		}
		f, e := os.Open(filepath.Join(repo, "kopia.repository.f"))
		if e != nil {
			f, e = os.Open(filepath.Join(repo, "kopia.repository"))
		}
		if e != nil {
			t.Fatal(e)
		}
		io.Copy(io.Discard, f)
		f.Close()
		source := filepath.Join(root, fmt.Sprintf("source-%t", private))
		if e = os.Mkdir(source, 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(source, "a.txt"), []byte("data"), 0600); e != nil {
			t.Fatal(e)
		}
		if !private {
			payload := make([]byte, 10<<20)
			if _, e = rand.Read(payload); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(source, "large"), payload, 0600); e != nil {
				t.Fatal(e)
			}
			cfg.effectiveUploadKiB = 1024
		}
		started := time.Now()
		ctx, cancel = context.WithTimeout(context.Background(), 45*time.Second)
		_, e = a.run(ctx, cfg, "snapshot", "create", "--json", "--no-progress", "--", source)
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if !private && time.Since(started) < time.Second {
			t.Fatal("SFTP backup did not respect upload limit")
		}

		if private {
			imported := &App{dir: t.TempDir(), kopia: binary, csrf: "token", ctx: context.Background(), roots: []string{root}, proxies: map[string]*davProxy{}, proxyErrors: map[string]string{}}
			did := "1111111111111111"
			jid := "2222222222222222"
			imported.pendingImport = &importDocument{Version: 1, DAVs: []exportDAV{{ID: did, Name: "sftp", URL: d.URL, User: d.User, Protocol: d.Protocol, Host: d.Host, Port: d.Port, RemotePath: d.RemotePath, KnownHosts: d.KnownHosts, PrivateKey: d.PrivateKey}}, Jobs: []exportJob{{ID: jid, Name: "imported", DAVID: did, Source: source, TargetPath: relative, RepoPassword: "test-password-123", UploadLimitKiB: 128}}}
			v := url.Values{"csrf": {"token"}}
			r := httptest.NewRequest("POST", "/import/apply", strings.NewReader(v.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			imported.applyImport(w, r)
			if w.Code != 303 {
				t.Fatal(w.Code, w.Body.String())
			}
			imported.wg.Wait()
			if len(imported.jobs) != 1 || imported.jobs[0].UploadLimitKiB != 128 {
				t.Fatal("SFTP import failed", imported.message)
			}
			if e := imported.startBackup(jid); e != nil {
				t.Fatal(e)
			}
			imported.wg.Wait()
			if imported.jobs[0].LastStatus != "success" {
				t.Fatal("imported SFTP task cannot run", imported.message)
			}
		}
	}
}
