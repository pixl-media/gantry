// Package tlscerttest writes self-signed certificates for tests. Only tests
// import it.
package tlscerttest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Write writes a self-signed certificate for localhost and 127.0.0.1, valid
// until notAfter, with its key to dir/cert.pem and dir/key.pem. It returns
// the paths and the certificate, which a client can trust as its own root.
func Write(t testing.TB, dir string, serial int64, notAfter time.Time) (certFile, keyFile string, cert *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: "gantry test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              notAfter,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	if cert, err = x509.ParseCertificate(der); err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "PRIVATE KEY", keyDER)
	return certFile, keyFile, cert
}

// writePEM writes the file and moves its modification time on by a second
// each time, so a replaced file always looks changed, even on file systems
// with coarse timestamps.
func writePEM(t testing.TB, file, typ string, der []byte) {
	t.Helper()
	var mod time.Time
	if info, err := os.Stat(file); err == nil {
		mod = info.ModTime().Add(time.Second)
	}
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if !mod.IsZero() {
		if err := os.Chtimes(file, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}
