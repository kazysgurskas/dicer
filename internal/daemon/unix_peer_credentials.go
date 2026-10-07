// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"

	"golang.org/x/sys/unix"
	"google.golang.org/grpc/credentials"

	"github.com/konradasb/dicer/internal/grpcapi"
)

// unixPeerCredentials are the credentials of the API served on its Unix
// socket. They do no handshake. They only ask the kernel which user and
// process connected, so that the audit can say who made each call.
type unixPeerCredentials struct{}

// ServerHandshake returns conn as it is, with the peer it reaches.
func (unixPeerCredentials) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, nil, fmt.Errorf("unix peer credentials on a %T connection", conn)
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return nil, nil, err
	}

	var ucred *unix.Ucred
	var ucredErr error
	if err := raw.Control(func(fd uintptr) {
		ucred, ucredErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return nil, nil, err
	}
	if ucredErr != nil {
		return nil, nil, fmt.Errorf("read peer credentials: %w", ucredErr)
	}

	// Only those the socket's mode lets in can connect, and nothing on the
	// way can read or change what they send.
	common := credentials.CommonAuthInfo{SecurityLevel: credentials.PrivacyAndIntegrity}
	return conn, grpcapi.UnixPeer{CommonAuthInfo: common, UID: int(ucred.Uid), PID: int(ucred.Pid)}, nil
}

// ClientHandshake fails: these credentials are a server's.
func (unixPeerCredentials) ClientHandshake(context.Context, string, net.Conn) (net.Conn, credentials.AuthInfo, error) {
	return nil, nil, errors.New("unix peer credentials are for the server only")
}

// Info describes the credentials.
func (unixPeerCredentials) Info() credentials.ProtocolInfo {
	return credentials.ProtocolInfo{SecurityProtocol: "unix"}
}

// Clone returns the credentials, which hold nothing to copy.
func (c unixPeerCredentials) Clone() credentials.TransportCredentials { return c }

// OverrideServerName does nothing: there is no server name to check.
func (unixPeerCredentials) OverrideServerName(string) error { return nil }
