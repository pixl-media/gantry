package tlscert

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pixl-media/gantry/agent/internal/tlscert/tlscerttest"
)

func newReloader(t *testing.T, certFile, keyFile string) (*Reloader, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	r, err := New(certFile, keyFile, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return r, &logs
}

func serial(t *testing.T, r *Reloader) int64 {
	t.Helper()
	c, err := r.GetCertificate(nil)
	if err != nil || c == nil || c.Leaf == nil {
		t.Fatalf("GetCertificate = %v, %v", c, err)
	}
	return c.Leaf.SerialNumber.Int64()
}

func TestReload(t *testing.T) {
	dir := t.TempDir()
	year := time.Now().AddDate(1, 0, 0)
	certFile, keyFile, _ := tlscerttest.Write(t, dir, 1, year)
	r, logs := newReloader(t, certFile, keyFile)
	if s := serial(t, r); s != 1 {
		t.Fatalf("serial = %d, want 1", s)
	}
	if !strings.Contains(logs.String(), `msg="tls: certificate loaded" names=localhost,127.0.0.1`) {
		t.Errorf("log: %s", logs)
	}

	// A renewal: new files, picked up by the next handshake.
	tlscerttest.Write(t, dir, 2, year)
	if s := serial(t, r); s != 2 {
		t.Errorf("after replacing the files: serial = %d, want 2", s)
	}

	// Broken new files keep the current certificate, with one warning.
	os.WriteFile(certFile, []byte("garbage"), 0o600)
	later := time.Now().Add(time.Hour)
	os.Chtimes(certFile, later, later)
	for range 3 {
		if s := serial(t, r); s != 2 {
			t.Errorf("with a broken file: serial = %d, want 2", s)
		}
	}
	if n := strings.Count(logs.String(), "keeping the current certificate"); n != 1 {
		t.Errorf("%d warnings, want 1:\n%s", n, logs)
	}

	// A missing file too.
	os.Remove(keyFile)
	if s := serial(t, r); s != 2 {
		t.Errorf("with a missing key: serial = %d, want 2", s)
	}

	// Fixed files are loaded again.
	tlscerttest.Write(t, dir, 3, year)
	if s := serial(t, r); s != 3 {
		t.Errorf("after fixing the files: serial = %d, want 3", s)
	}
}

func TestNewFails(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	year := time.Now().AddDate(1, 0, 0)
	certA, keyA, _ := tlscerttest.Write(t, a, 1, year)
	_, keyB, _ := tlscerttest.Write(t, b, 2, year)
	for name, files := range map[string][2]string{
		"missing certificate": {filepath.Join(a, "nope.pem"), keyA},
		"missing key":         {certA, filepath.Join(a, "nope.pem")},
		"key of another pair": {certA, keyB},
		"key as certificate":  {keyA, keyA},
	} {
		if _, err := New(files[0], files[1], nil); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestExpiryWarning(t *testing.T) {
	for name, tt := range map[string]struct {
		notAfter time.Time
		want     string
	}{
		"in a week": {time.Now().AddDate(0, 0, 7), `level=WARN msg="tls: the certificate expires soon"`},
		"expired":   {time.Now().Add(-time.Minute), `level=WARN msg="tls: the certificate has expired"`},
	} {
		certFile, keyFile, _ := tlscerttest.Write(t, t.TempDir(), 1, tt.notAfter)
		_, logs := newReloader(t, certFile, keyFile)
		if !strings.Contains(logs.String(), tt.want) {
			t.Errorf("%s: log = %s", name, logs)
		}
	}
}
