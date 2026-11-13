package main

import (
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// davProxy is exclusively for an explicitly unverified HTTPS connection.
// Kopia authenticates locally with separate random credentials; the real
// WebDAV credentials never pass over the loopback connection.
type davProxy struct {
	server   *http.Server
	listener net.Listener
}

func newDAVProxy(c *Config) (*davProxy, string, error) {
	target, err := url.Parse(c.URL)
	if err != nil || target.Scheme != "https" || target.Host == "" {
		return nil, "", fmt.Errorf("无效的 HTTPS WebDAV 地址")
	}
	if c.ProxyUser == "" {
		c.ProxyUser = "local-" + random(12)
		c.ProxyPassword = random(24)
	}
	addr := "127.0.0.1:" + strconv.Itoa(c.ProxyPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", fmt.Errorf("本地 WebDAV 代理无法启动: %w", err)
	}
	c.ProxyPort = ln.Addr().(*net.TCPAddr).Port
	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = &http.Transport{Proxy: nil, DisableKeepAlives: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 90 * time.Second}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "WebDAV 连接失败", http.StatusBadGateway)
	}
	rp.Director = func(r *http.Request) {
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host
		r.Host = target.Host
		r.SetBasicAuth(c.User, c.WebDAVPassword)
		if d := r.Header.Get("Destination"); d != "" {
			if parsed, e := url.Parse(d); e == nil && parsed.Host == ln.Addr().String() {
				parsed.Scheme = target.Scheme
				parsed.Host = target.Host
				r.Header.Set("Destination", parsed.String())
			}
		}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(c.ProxyUser)) != 1 || subtle.ConstantTimeCompare([]byte(p), []byte(c.ProxyPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="cangzhou-local"`)
			http.Error(w, "需要认证", 401)
			return
		}
		root := strings.TrimRight(target.EscapedPath(), "/")
		path := r.URL.EscapedPath()
		if root != "" && path != root && !strings.HasPrefix(path, root+"/") {
			http.Error(w, "路径不属于仓库", 403)
			return
		}
		rp.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(ln) }()
	local := *target
	local.Scheme = "http"
	local.Host = ln.Addr().String()
	return &davProxy{server: server, listener: ln}, local.String(), nil
}
func (p *davProxy) Close() {
	if p != nil {
		_ = p.server.Close()
	}
}
