package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

func parseSFTP(r *http.Request, old DAVConfig) (DAVConfig, error) {
	d := DAVConfig{ID: old.ID, Protocol: "sftp", Name: strings.TrimSpace(r.PostForm.Get("name")), Host: strings.TrimSpace(r.PostForm.Get("host")), RemotePath: strings.TrimSpace(r.PostForm.Get("remote_path")), User: strings.TrimSpace(r.PostForm.Get("user")), Password: r.PostForm.Get("password"), PrivateKey: r.PostForm.Get("private_key"), KnownHosts: strings.TrimSpace(r.PostForm.Get("known_hosts"))}
	if d.Password == "" && d.PrivateKey == "" && old.Protocol == "sftp" {
		d.Password = old.Password
		d.PrivateKey = old.PrivateKey
	}
	if d.KnownHosts == "" && old.Protocol == "sftp" {
		d.KnownHosts = old.KnownHosts
	}
	if d.PrivateKey != "" && r.PostForm.Get("password") == "" {
		d.Password = ""
	}
	if r.PostForm.Get("password") != "" {
		d.PrivateKey = ""
	}
	port, e := strconv.Atoi(r.PostForm.Get("port"))
	if e != nil || port < 1 || port > 65535 {
		return d, errors.New("请输入有效的 SFTP 端口")
	}
	d.Port = port
	if d.Name == "" || len(d.Name) > 120 || d.User == "" || d.Host == "" || strings.ContainsAny(d.Host, " /\\\r\n") || !strings.HasPrefix(d.RemotePath, "/") || d.KnownHosts == "" || (d.Password == "" && d.PrivateKey == "") {
		return d, errors.New("请填写名称、主机、绝对路径、账号、密码或私钥及服务器主机密钥")
	}
	if path.Clean(d.RemotePath) != d.RemotePath || strings.ContainsAny(d.RemotePath, "\r\n") {
		return d, errors.New("SFTP 路径必须是规范的绝对路径")
	}
	if d.PrivateKey != "" {
		if _, e := ssh.ParsePrivateKey([]byte(d.PrivateKey)); e != nil {
			return d, errors.New("私钥无效或带口令；请使用无口令私钥")
		}
	}
	u := url.URL{Scheme: "sftp", Host: net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), Path: d.RemotePath}
	d.URL = u.String()
	return d, nil
}
func openSFTP(ctx context.Context, d DAVConfig) (*sftp.Client, func(), error) {
	f, e := os.CreateTemp("", "cz-known-hosts-")
	if e != nil {
		return nil, nil, e
	}
	defer os.Remove(f.Name())
	if _, e = f.WriteString(d.KnownHosts + "\n"); e != nil {
		f.Close()
		return nil, nil, e
	}
	f.Close()
	check, e := knownhosts.New(f.Name())
	if e != nil {
		return nil, nil, errors.New("服务器主机密钥格式无效，请提供 known_hosts 格式")
	}
	var auth ssh.AuthMethod
	if d.PrivateKey != "" {
		key, e := ssh.ParsePrivateKey([]byte(d.PrivateKey))
		if e != nil {
			return nil, nil, errors.New("无法读取私钥")
		}
		auth = ssh.PublicKeys(key)
	} else {
		auth = ssh.Password(d.Password)
	}
	cfg := &ssh.ClientConfig{User: d.User, Auth: []ssh.AuthMethod{auth}, HostKeyCallback: check, Timeout: 15 * time.Second}
	dial := net.Dialer{Timeout: 15 * time.Second}
	conn, e := dial.DialContext(ctx, "tcp", net.JoinHostPort(d.Host, strconv.Itoa(d.Port)))
	if e != nil {
		return nil, nil, e
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	c, ch, req, e := ssh.NewClientConn(conn, net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), cfg)
	if e != nil {
		conn.Close()
		return nil, nil, e
	}
	client := ssh.NewClient(c, ch, req)
	sc, e := sftp.NewClient(client)
	if e != nil {
		client.Close()
		return nil, nil, e
	}
	closeAll := func() { sc.Close(); client.Close() }
	return sc, closeAll, nil
}
func testSFTP(ctx context.Context, d DAVConfig) error {
	sc, closeAll, e := openSFTP(ctx, d)
	if e != nil {
		return e
	}
	defer closeAll()
	st, e := sc.Stat(d.RemotePath)
	if e != nil {
		return e
	}
	if !st.IsDir() {
		return errors.New("目标不是目录")
	}
	_, e = sc.ReadDir(d.RemotePath)
	return e
}
func (a *App) connectRemote(ctx context.Context, cfg *Config, d DAVConfig, mode string) (*davProxy, error) {
	if mode == "create" {
		if e := ensureRepositoryDirectory(ctx, d, cfg.TargetPath); e != nil {
			return nil, fmt.Errorf("无法创建仓库目标目录：%w", e)
		}
	}
	target := cfg.URL
	runCfg := *cfg
	var proxy *davProxy
	if d.Protocol == "sftp" {
		u, e := url.Parse(cfg.URL)
		if e != nil {
			return nil, e
		}
		opts := map[string]any{"host": d.Host, "port": d.Port, "username": d.User, "password": d.Password, "path": u.Path, "knownHostsData": d.KnownHosts}
		if d.PrivateKey != "" {
			opts["keyData"] = d.PrivateKey
		}
		tokenData, e := json.Marshal(map[string]any{"version": "1", "storage": map[string]any{"type": "sftp", "config": opts}})
		if e != nil {
			return nil, e
		}
		dir, e := a.jobDir(cfg.ID)
		if e != nil {
			return nil, e
		}
		f, e := os.CreateTemp(dir, ".sftp-token-")
		if e != nil {
			return nil, e
		}
		name := f.Name()
		defer os.Remove(name)
		if _, e = f.WriteString(base64.RawURLEncoding.EncodeToString(tokenData)); e != nil {
			f.Close()
			return nil, e
		}
		if e = f.Close(); e != nil {
			return nil, e
		}
		_, e = a.run(ctx, runCfg, "repository", mode, "from-config", "--token-file="+name, "--no-persist-credentials", "--no-check-for-updates")
		if e != nil {
			msg := e.Error()
			if d.PrivateKey != "" {
				msg = strings.ReplaceAll(msg, d.PrivateKey, "[已隐藏]")
			}
			return nil, errors.New(msg)
		}
		return nil, nil
	}
	if cfg.InsecureTLS {
		var e error
		proxy, target, e = newDAVProxy(cfg)
		if e != nil {
			return nil, e
		}
		runCfg = *cfg
		runCfg.User = cfg.ProxyUser
		runCfg.WebDAVPassword = cfg.ProxyPassword
	}
	_, e := a.run(ctx, runCfg, "repository", mode, "webdav", "--url="+target, "--no-persist-credentials", "--no-check-for-updates")
	if e != nil && proxy != nil {
		proxy.Close()
		proxy = nil
	}
	return proxy, e
}
