// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package grpcserver

import "google.golang.org/grpc"

// NewSocketServer returns a gRPC server for server's handlers on the Unix
// socket, to every call: whoever the socket's mode lets connect has control
// of the daemon. The audit log names the user and process that made each
// call.
func NewSocketServer(server *Server) *grpc.Server {
	return newGRPCServer(server, nil, unixPeerCredentials{})
}
