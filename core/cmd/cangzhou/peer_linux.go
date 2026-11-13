//go:build linux

package main

import (
	"net"
	"os/user"
	"strconv"
	"syscall"
)

// Unix SO_PEERCRED is kernel-supplied. Trust root or fnOS's www-data gateway
// account; no request header can turn a different local UID into this UID.
// www-data is a shared account, so other local services running as www-data
// are inside this trust boundary as well.
func trustworthyPeer(c net.Conn) bool {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return false
	}
	raw, e := u.SyscallConn()
	if e != nil {
		return false
	}
	var cred *syscall.Ucred
	var inner error
	if e = raw.Control(func(fd uintptr) {
		cred, inner = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); e != nil || inner != nil || cred == nil {
		return false
	}
	if cred.Uid == 0 {
		return true
	}
	account, e := user.Lookup("www-data")
	if e != nil {
		return false
	}
	uid, e := strconv.ParseUint(account.Uid, 10, 32)
	return e == nil && uint64(cred.Uid) == uid
}
