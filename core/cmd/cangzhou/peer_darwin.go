//go:build darwin

package main

import "net"

// Used solely for local macOS development. The distributed Linux build uses
// peer_linux.go and never trusts arbitrary local Unix socket callers.
func trustworthyPeer(c net.Conn) bool { _, ok := c.(*net.UnixConn); return ok }
