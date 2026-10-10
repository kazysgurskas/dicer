// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

package dicer

import (
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"github.com/konradasb/dicer/internal/defaults"
	"github.com/konradasb/dicer/internal/token"
	dicerdv1 "github.com/konradasb/dicer/proto/dicerd/v1"
)

// DefaultAddress is where a daemon serves its API unless configured
// otherwise: its Unix socket, as a gRPC target.
const DefaultAddress = "unix://" + defaults.Socket

// The keepalive a client uses unless WithKeepalive says otherwise.
const (
	// DefaultKeepaliveInterval is how long a connection may carry nothing
	// before the client pings the daemon.
	DefaultKeepaliveInterval = 30 * time.Second

	// DefaultKeepaliveTimeout is how long the client waits for the answer
	// to a ping before giving the connection up.
	DefaultKeepaliveTimeout = 10 * time.Second
)

// Client is a connection to a Dicer daemon. Its calls are grouped by the
// resource they act on, as in c.Instances.Get(ctx, "web"), and those about
// the host as a whole are methods of its own, such as c.Events.
//
// A Client is safe for concurrent use and should be closed when done.
type Client struct {
	// Instances are the calls about instances.
	Instances *Instances

	// Snapshots are the calls about snapshots.
	Snapshots *Snapshots

	// Networks are the calls about networks.
	Networks *Networks

	// Volumes are the calls about volumes.
	Volumes *Volumes

	// Images are the calls about images.
	Images *Images

	// Kernels are the calls about kernels.
	Kernels *Kernels

	// Tokens are the calls about tokens.
	Tokens *Tokens

	api  dicerdv1.DaemonServiceClient
	conn *grpc.ClientConn
}

// options are what NewClient was asked for.
type options struct {
	address           string
	token             string
	tls               *tls.Config
	keepaliveInterval time.Duration
	keepaliveTimeout  time.Duration
	dialOptions       []grpc.DialOption
}

// An Option configures a Client.
type Option func(*options)

// WithAddress sets the daemon's address, as a gRPC target:
// "unix:///path/to/socket", or "host:port" for its TCP listener. The default
// is DefaultAddress. An empty target means the default too, so that an
// address that may be unset can be passed as it is.
func WithAddress(target string) Option {
	return func(o *options) { o.address = target }
}

// WithToken makes every call with a token, which a daemon's TCP listener
// requires. `dicer token create`, on the daemon's host, makes one. The
// client checks the daemon by the fingerprint the token carries. A token
// without a fingerprint is for a daemon whose certificate the host's root
// CAs verify, unless WithTLS says otherwise.
func WithToken(value string) Option {
	return func(o *options) { o.token = value }
}

// WithTLS secures the connection to a daemon's TCP listener with cfg, in
// place of the check the token implies. It is for a daemon served with a
// certificate from an authority the host does not trust, such as a
// company's own: cfg.RootCAs names it.
func WithTLS(cfg *tls.Config) Option {
	return func(o *options) { o.tls = cfg }
}

// WithKeepalive sets how long a connection may carry nothing before the
// client pings the daemon, and how long it waits for the answer before
// giving the connection up, failing the calls in flight on it with
// codes.Unavailable. That is how a daemon that has gone without closing the
// connection, because its host lost power or the network between dropped,
// is noticed; without it, a call waits for as long as its context lets it.
//
// The defaults are DefaultKeepaliveInterval and DefaultKeepaliveTimeout. An
// interval of zero turns the pings off, and a timeout of zero is the
// default. gRPC raises an interval below 10s to 10s, and a daemon closes the
// connection of a client that pings more often than its
// server.keepalive.min_client_interval.
//
// A connection over a Unix socket is never pinged: the kernel closes it if
// the daemon goes.
func WithKeepalive(interval, timeout time.Duration) Option {
	return func(o *options) {
		o.keepaliveInterval, o.keepaliveTimeout = interval, timeout
	}
}

// WithDialOptions adds gRPC dial options, such as interceptors. They are
// applied after WithKeepalive's, so a keepalive given here replaces it.
func WithDialOptions(opts ...grpc.DialOption) Option {
	return func(o *options) { o.dialOptions = append(o.dialOptions, opts...) }
}

// NewClient connects to a daemon, by default the local one:
//
//	c, err := dicer.NewClient()
//
// A daemon's TCP listener, with a token:
//
//	c, err := dicer.NewClient(dicer.WithAddress("host:7443"), dicer.WithToken(token))
//
// Connecting is lazy: an unreachable daemon is reported by the first call.
// A local socket that is not there, a TCP address without a token, and a
// token that is not one are reported here.
func NewClient(opts ...Option) (*Client, error) {
	o := options{
		address:           DefaultAddress,
		keepaliveInterval: DefaultKeepaliveInterval,
		keepaliveTimeout:  DefaultKeepaliveTimeout,
	}
	for _, opt := range opts {
		opt(&o)
	}
	o.address = cmp.Or(o.address, DefaultAddress)

	if path, ok := strings.CutPrefix(o.address, "unix://"); ok {
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("there is no socket at %s; is dicerd running?", path)
		}
	}

	creds, err := o.transportCredentials()
	if err != nil {
		return nil, err
	}

	var dialOptions []grpc.DialOption
	if o.token != "" {
		dialOptions = append(dialOptions, grpc.WithPerRPCCredentials(bearerToken(o.token)))
	}
	if o.keepaliveInterval > 0 && !isSocket(o.address) {
		dialOptions = append(dialOptions, grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                o.keepaliveInterval,
			Timeout:             cmp.Or(o.keepaliveTimeout, DefaultKeepaliveTimeout),
			PermitWithoutStream: true,
		}))
	}

	dialOptions = append(dialOptions, o.dialOptions...)
	dialOptions = append(dialOptions, grpc.WithTransportCredentials(creds))

	conn, err := grpc.NewClient(o.address, dialOptions...)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", o.address, err)
	}

	return newClient(conn, dicerdv1.NewDaemonServiceClient(conn)), nil
}

// newClient returns a Client making its calls through api.
func newClient(conn *grpc.ClientConn, api dicerdv1.DaemonServiceClient) *Client {
	return &Client{
		Instances: &Instances{api: api},
		Snapshots: &Snapshots{api: api},
		Networks:  &Networks{api: api},
		Volumes:   &Volumes{api: api},
		Images:    &Images{api: api},
		Kernels:   &Kernels{api: api},
		Tokens:    &Tokens{api: api},
		api:       api,
		conn:      conn,
	}
}

// transportCredentials returns how the connection is secured. A socket
// needs nothing, since its file permissions guard it. TCP needs TLS, which
// checks the daemon by the token's fingerprint unless WithTLS says
// otherwise.
func (o *options) transportCredentials() (credentials.TransportCredentials, error) {
	if isSocket(o.address) {
		if o.token != "" {
			return nil, fmt.Errorf("a token is for a daemon's TCP listener, not its socket %s: "+
				"give the listener's address with WithAddress", o.address)
		}
		return insecure.NewCredentials(), nil
	}

	if o.token == "" {
		return nil, fmt.Errorf("a daemon's TCP listener, %s, needs a token: give one with WithToken", o.address)
	}
	_, fingerprint, err := token.Parse(o.token)
	if err != nil {
		return nil, err
	}

	if o.tls != nil {
		return credentials.NewTLS(o.tls), nil
	}
	return credentials.NewTLS(pinnedTLSConfig(fingerprint)), nil
}

// pinnedTLSConfig returns the TLS configuration that checks the daemon by a
// fingerprint, or against the host's root CAs if it is empty. A daemon's own
// certificate names no host and no authority: the key the fingerprint pins
// is what proves it is the daemon.
func pinnedTLSConfig(fingerprint string) *tls.Config {
	cfg := &tls.Config{MinVersion: tls.VersionTLS13}
	if fingerprint == "" {
		return cfg
	}

	// VerifyConnection checks the fingerprint instead.
	cfg.InsecureSkipVerify = true
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return errors.New("the daemon presented no certificate")
		}
		if got := token.Fingerprint(cs.PeerCertificates[0]); got != fingerprint {
			return fmt.Errorf("certificate fingerprint mismatch: got %s, want %s", got, fingerprint)
		}
		return nil
	}

	return cfg
}

// bearerToken sends a token with every call, as the daemon's TCP listener
// requires.
type bearerToken string

// GetRequestMetadata returns the token as an authorization header.
func (t bearerToken) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(t)}, nil
}

// RequireTransportSecurity reports that a token is only ever sent over TLS.
func (bearerToken) RequireTransportSecurity() bool { return true }

// isSocket reports whether a gRPC target is a Unix socket.
func isSocket(target string) bool {
	return strings.HasPrefix(target, "unix:") || strings.HasPrefix(target, "unix-abstract:")
}

// Close closes the connection to the daemon. Calls in flight are cancelled.
func (c *Client) Close() error {
	return c.conn.Close()
}
