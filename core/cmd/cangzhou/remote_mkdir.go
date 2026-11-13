package main

import (
	"context"
	"crypto/tls"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func ensureRepositoryDirectory(ctx context.Context, d DAVConfig, relative string) error {
	base, e := url.Parse(d.URL)
	if e != nil {
		return e
	}
	target, e := repositoryURL(base, relative)
	if e != nil {
		return e
	}
	if d.Protocol == "sftp" {
		u, e := url.Parse(target)
		if e != nil {
			return e
		}
		client, closeAll, e := openSFTP(ctx, d)
		if e != nil {
			return e
		}
		defer closeAll()
		if e = client.MkdirAll(u.Path); e != nil {
			return e
		}
		info, e := client.Stat(u.Path)
		if e != nil {
			return e
		}
		if !info.IsDir() {
			return errors.New("目标路径不是目录")
		}
		return nil
	}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{InsecureSkipVerify: d.InsecureTLS}, TLSHandshakeTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		original := via[0]
		status := 0
		if req.Response != nil {
			status = req.Response.StatusCode
		}
		if len(via) >= 3 || req.URL.Scheme != original.URL.Scheme || !strings.EqualFold(req.URL.Host, original.URL.Host) || req.URL.RawQuery != original.URL.RawQuery || strings.TrimRight(req.URL.EscapedPath(), "/") != strings.TrimRight(original.URL.EscapedPath(), "/") || (status != 301 && status != 302 && status != 307 && status != 308) {
			return errors.New("拒绝不安全的重定向")
		}
		// Go changes PROPFIND/MKCOL to GET on 301/302; preserve the method
		// and request body only for the same directory with a slash change.
		req.Method = original.Method
		req.Header = original.Header.Clone()
		req.ContentLength = original.ContentLength
		if original.GetBody != nil {
			body, e := original.GetBody()
			if e != nil {
				return e
			}
			req.Body = body
		}
		return nil
	}}
	// The saved connection path may itself be the missing directory. Check it
	// first, then create each selected child beneath it when needed.
	paths := []string{d.URL}
	if strings.Trim(relative, "/") != "" {
		var prefix string
		for _, part := range strings.Split(strings.Trim(relative, "/"), "/") {
			if prefix != "" {
				prefix += "/"
			}
			prefix += part
			p, e := repositoryURL(base, prefix)
			if e != nil {
				return e
			}
			paths = append(paths, p)
		}
	}
	for _, target := range paths {
		exists, status, e := webDAVCollection(ctx, client, d, target)
		if e != nil {
			return e
		}
		if exists {
			continue
		}
		if status != http.StatusNotFound {
			return errors.New("目标已存在但不是可访问目录")
		}
		req, e := http.NewRequestWithContext(ctx, "MKCOL", target, nil)
		if e != nil {
			return e
		}
		req.SetBasicAuth(d.User, d.Password)
		resp, e := client.Do(req)
		if e != nil {
			return e
		}
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		if resp.StatusCode == http.StatusCreated {
			continue
		}
		// Another client may have created the directory after our check.
		if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNoContent {
			ok, _, e := webDAVCollection(ctx, client, d, target)
			if e != nil {
				return e
			}
			if ok {
				continue
			}
		}
		return fmt.Errorf("WebDAV 创建目录返回 HTTP %d，请检查父目录及写入权限", resp.StatusCode)
	}
	return nil
}
func webDAVCollection(ctx context.Context, client *http.Client, d DAVConfig, target string) (bool, int, error) {
	req, e := http.NewRequestWithContext(ctx, "PROPFIND", target, strings.NewReader(`<?xml version="1.0"?><d:propfind xmlns:d="DAV:"><d:prop><d:resourcetype/></d:prop></d:propfind>`))
	if e != nil {
		return false, 0, e
	}
	req.SetBasicAuth(d.User, d.Password)
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml")
	resp, e := client.Do(req)
	if e != nil {
		return false, 0, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return false, resp.StatusCode, fmt.Errorf("检查已有目录返回 HTTP %d", resp.StatusCode)
	}
	dec := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	inType := false
	for {
		token, e := dec.Token()
		if e == io.EOF {
			break
		}
		if e != nil {
			return false, 0, e
		}
		switch v := token.(type) {
		case xml.StartElement:
			if v.Name.Space == "DAV:" && v.Name.Local == "resourcetype" {
				inType = true
			} else if inType && v.Name.Space == "DAV:" && v.Name.Local == "collection" {
				return true, resp.StatusCode, nil
			}
		case xml.EndElement:
			if v.Name.Space == "DAV:" && v.Name.Local == "resourcetype" {
				inType = false
			}
		}
	}
	return false, resp.StatusCode, nil
}
