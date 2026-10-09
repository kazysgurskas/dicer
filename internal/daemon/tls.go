// Copyright 2026 Dicer Authors
// SPDX-License-Identifier: MIT

//go:build linux

package daemon

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/konradasb/dicer/internal/atomicfile"
	"github.com/konradasb/dicer/internal/token"
)

// The files in data_dir the daemon keeps a certificate of its own in, when
// server.crt_file gives none.
const (
	ownCrtFile = "server.crt"
	ownKeyFile = "server.key"
)

// serverTLSConfig builds the TLS configuration of the TCP listener, and
// returns the fingerprint of its certificate. The certificate is cfg's if it
// gives one, and else the daemon's own, made in dataDir the first time.
func serverTLSConfig(cfg ServerConfig, dataDir string, logger *slog.Logger) (*tls.Config, string, error) {
	crtFile, keyFile := cfg.CrtFile, cfg.KeyFile
	if crtFile == "" {
		crtFile, keyFile = filepath.Join(dataDir, ownCrtFile), filepath.Join(dataDir, ownKeyFile)
		if err := ensureOwnCertificate(crtFile, keyFile); err != nil {
			return nil, "", err
		}
	}

	cert, err := newCertificate(crtFile, keyFile, logger)
	if err != nil {
		return nil, "", err
	}

	return &tls.Config{
		MinVersion: tls.VersionTLS13,

		// GetCertificate picks up a renewed certificate without a restart.
		GetCertificate: cert.current,
	}, token.Fingerprint(cert.cert.Leaf), nil
}

// ensureOwnCertificate makes the daemon's own certificate and its key, unless
// the certificate is there already. Clients check it by the fingerprint of
// its key, which tokens carry, so it names no host and never expires.
func ensureOwnCertificate(crtFile, keyFile string) error {
	if _, err := os.Stat(crtFile); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read the API certificate: %w", err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("make the API certificate's key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("make the API certificate's serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "dicerd"},
		NotBefore:    time.Now().Add(-time.Hour),
		// RFC 5280's date for a certificate with no expiry.
		NotAfter:    time.Date(9999, time.December, 31, 23, 59, 59, 0, time.UTC),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("make the API certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("encode the API certificate's key: %w", err)
	}

	// The key first: the certificate's presence is what says both are
	// there.
	if err := atomicfile.Write(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return fmt.Errorf("write the API certificate's key: %w", err)
	}
	if err := atomicfile.Write(crtFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return fmt.Errorf("write the API certificate: %w", err)
	}
	return nil
}

// certificate is the daemon's certificate, reloaded when its files change.
type certificate struct {
	crtFile, keyFile string
	logger           *slog.Logger

	mu   sync.Mutex
	cert *tls.Certificate
	// certModTime and keyModTime are the files' modification times when
	// they were read.
	certModTime time.Time
	keyModTime  time.Time
	// failureLoggedAt is when a failed reload was last logged.
	failureLoggedAt time.Time
}

// newCertificate loads the certificate.
func newCertificate(crtFile, keyFile string, logger *slog.Logger) (*certificate, error) {
	c := &certificate{crtFile: crtFile, keyFile: keyFile, logger: logger}
	if err := c.load(); err != nil {
		return nil, err
	}

	return c, nil
}

// current returns the certificate to present, reloading it first if the
// files have changed. It is tls.Config's GetCertificate, and safe for
// concurrent use.
func (c *certificate) current(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.changed() {
		return c.cert, nil
	}

	if err := c.load(); err != nil {
		// Keep serving the previous certificate; the renewal may be half
		// written. Logging is rate-limited since every handshake gets here.
		if time.Since(c.failureLoggedAt) > reloadFailureLogInterval {
			c.failureLoggedAt = time.Now()
			c.logger.Error("the API certificate changed but could not be read; still serving the previous one",
				"crt_file", c.crtFile, "error", err)
		}
	}

	return c.cert, nil
}

// reloadFailureLogInterval bounds how often a failing reload is logged.
const reloadFailureLogInterval = time.Minute

// load reads the pair and records its modification times. The caller holds
// the lock, except during construction.
func (c *certificate) load() error {
	certInfo, err := os.Stat(c.crtFile)
	if err != nil {
		return fmt.Errorf("read the API certificate: %w", err)
	}
	keyInfo, err := os.Stat(c.keyFile)
	if err != nil {
		return fmt.Errorf("read the API certificate's key: %w", err)
	}

	pair, err := tls.LoadX509KeyPair(c.crtFile, c.keyFile)
	if err != nil {
		return fmt.Errorf("load the API certificate: %w", err)
	}

	c.cert, c.certModTime, c.keyModTime = &pair, certInfo.ModTime(), keyInfo.ModTime()

	return nil
}

// changed reports whether either file has been modified since it was read. A
// file that cannot be stat'ed counts as unchanged.
func (c *certificate) changed() bool {
	certInfo, err := os.Stat(c.crtFile)
	if err != nil {
		return false
	}
	keyInfo, err := os.Stat(c.keyFile)
	if err != nil {
		return false
	}

	return !certInfo.ModTime().Equal(c.certModTime) || !keyInfo.ModTime().Equal(c.keyModTime)
}
