// Package tlscert serves the agent's TLS certificate from a certificate and
// a key file. Replacing the files (a renewal) takes effect with the next
// connection, without a restart (docs/decisions.md, decision 27).
package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// expiryWarning is how long before its expiry a certificate is warned about.
const expiryWarning = 14 * 24 * time.Hour

// Reloader hands out the certificate from its files, loading them again
// when they change. If changed files don't load (e.g. the certificate was
// replaced, the key not yet), it keeps the certificate it has.
type Reloader struct {
	certFile, keyFile string
	logger            *slog.Logger
	now               func() time.Time

	mu      sync.Mutex
	cert    *tls.Certificate
	stamp   stamp  // of the files cert was loaded from
	failed  stamp  // of files that didn't load, so they aren't read again
	problem string // last problem warned about, so it's warned about once
}

// stamp identifies a version of the two files: modification times (in
// nanoseconds) and sizes.
type stamp struct {
	certMod, keyMod   int64
	certSize, keySize int64
}

// New loads the certificate and key, PEM encoded. It fails if they don't
// load or don't match.
func New(certFile, keyFile string, logger *slog.Logger) (*Reloader, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	r := &Reloader{certFile: certFile, keyFile: keyFile, logger: logger, now: time.Now}
	st, err := r.stat()
	if err == nil {
		err = r.load(st)
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate is for tls.Config.GetCertificate. It checks the files on
// every handshake (two stat calls) and loads them again if they changed.
func (r *Reloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := r.stat()
	if err == nil && st != r.stamp && st != r.failed {
		if err = r.load(st); err != nil {
			r.failed = st
		}
	}
	if err != nil && err.Error() != r.problem {
		r.problem = err.Error()
		r.logger.Warn("tls: keeping the current certificate", "err", err)
	}
	return r.cert, nil
}

func (r *Reloader) stat() (stamp, error) {
	c, err := os.Stat(r.certFile)
	if err != nil {
		return stamp{}, fmt.Errorf("certificate: %w", err)
	}
	k, err := os.Stat(r.keyFile)
	if err != nil {
		return stamp{}, fmt.Errorf("key: %w", err)
	}
	return stamp{
		certMod: c.ModTime().UnixNano(), keyMod: k.ModTime().UnixNano(),
		certSize: c.Size(), keySize: k.Size(),
	}, nil
}

// load reads the files that st describes. If they changed meanwhile, the
// next handshake sees another stamp and loads them again.
func (r *Reloader) load(st stamp) error {
	cert, err := tls.LoadX509KeyPair(r.certFile, r.keyFile)
	if err != nil {
		return fmt.Errorf("loading %s and %s: %w", r.certFile, r.keyFile, err)
	}
	leaf := cert.Leaf
	if leaf == nil {
		if leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return fmt.Errorf("parsing %s: %w", r.certFile, err)
		}
		cert.Leaf = leaf
	}
	r.cert, r.stamp, r.problem = &cert, st, ""

	attrs := []any{"names", names(leaf), "expires", leaf.NotAfter.UTC().Format(time.RFC3339)}
	switch left := leaf.NotAfter.Sub(r.now()); {
	case left <= 0:
		r.logger.Warn("tls: the certificate has expired", attrs...)
	case left < expiryWarning:
		r.logger.Warn("tls: the certificate expires soon", attrs...)
	default:
		r.logger.Info("tls: certificate loaded", attrs...)
	}
	return nil
}

// names lists the host names and addresses a certificate is valid for.
func names(c *x509.Certificate) string {
	n := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		n = append(n, ip.String())
	}
	if len(n) == 0 {
		n = append(n, c.Subject.CommonName)
	}
	return strings.Join(n, ",")
}
