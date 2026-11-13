package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// frame builds one frame of Docker's multiplexed log format.
func frame(stream byte, payload string) []byte {
	h := make([]byte, 8)
	h[0] = stream
	binary.BigEndian.PutUint32(h[4:], uint32(len(payload)))
	return append(h, payload...)
}

func collect(t *testing.T, read func(func(LogEntry) error) error) []LogEntry {
	t.Helper()
	var got []LogEntry
	if err := read(func(e LogEntry) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	return got
}

// oneByteReader returns one byte per Read, so frames arrive in pieces.
type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) { return o.r.Read(p[:1]) }

func TestDemultiplex(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(frame(1, "2026-11-12T12:00:00.000000001Z hello\n"))
	stream.Write(frame(2, "2026-11-12T12:00:00.5Z \x1b[31merror\x1b[0m\r\n"))
	stream.Write(frame(1, "2026-11-12T12:00:01Z \n")) // an empty line

	got := collect(t, func(fn func(LogEntry) error) error {
		return demultiplex(oneByteReader{&stream}, fn)
	})
	if len(got) != 3 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
	want := []LogEntry{
		{Time: time.Date(2026, 11, 12, 12, 0, 0, 1, time.UTC), Stream: "stdout", Line: "hello"},
		{Time: time.Date(2026, 11, 12, 12, 0, 0, 5e8, time.UTC), Stream: "stderr", Line: "\x1b[31merror\x1b[0m"},
		{Time: time.Date(2026, 11, 12, 12, 0, 1, 0, time.UTC), Stream: "stdout", Line: ""},
	}
	for i := range want {
		if !got[i].Time.Equal(want[i].Time) || got[i].Stream != want[i].Stream || got[i].Line != want[i].Line {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestReadLinesTTY(t *testing.T) {
	in := "2026-11-12T12:00:00Z one\r\n2026-11-12T12:00:01Z two\nno timestamp"
	got := collect(t, func(fn func(LogEntry) error) error {
		return readLines(strings.NewReader(in), "stdout", fn)
	})
	if len(got) != 3 || got[0].Line != "one" || got[1].Line != "two" || got[2].Line != "no timestamp" || !got[2].Time.IsZero() {
		t.Errorf("got %+v", got)
	}
}

func TestLongLinesAreCut(t *testing.T) {
	long := "2026-11-12T12:00:00Z " + strings.Repeat("ä", MaxLineLength) + "\n"
	got := collect(t, func(fn func(LogEntry) error) error {
		return readLines(strings.NewReader(long+"2026-11-12T12:00:01Z next\n"), "stdout", fn)
	})
	if len(got) != 2 {
		t.Fatalf("got %d entries", len(got))
	}
	if len(got[0].Line) > MaxLineLength+len(" …") || !strings.HasSuffix(got[0].Line, " …") {
		t.Errorf("long line not cut: %d bytes", len(got[0].Line))
	}
	if !strings.HasPrefix(got[0].Line, "ää") || strings.ContainsRune(got[0].Line, '�') {
		t.Error("cut inside a UTF-8 character")
	}
	if got[1].Line != "next" {
		t.Errorf("line after the long one = %q", got[1].Line)
	}
}

func TestContainerLogsRequest(t *testing.T) {
	var query string
	c := serveUnix(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/containers/abc/json":
			w.Write([]byte(`{"Config":{"Tty":true}}`))
		case "/containers/abc/logs":
			query = r.URL.RawQuery
			w.Write(frame(1, "2026-11-12T12:00:00Z hi\n"))
		default:
			http.NotFound(w, r)
		}
	})

	tty, err := c.ContainerTTY(context.Background(), "abc")
	if err != nil || !tty {
		t.Fatalf("ContainerTTY = %v, %v", tty, err)
	}

	since := time.Date(2026, 11, 12, 12, 0, 0, 123, time.UTC)
	got := collect(t, func(fn func(LogEntry) error) error {
		return c.ContainerLogs(context.Background(), "abc", false, LogOptions{Tail: 50, Since: since, Follow: true}, fn)
	})
	if len(got) != 1 || got[0].Line != "hi" {
		t.Errorf("got %+v", got)
	}
	for _, want := range []string{"follow=1", "stderr=1", "stdout=1", "timestamps=1", "tail=50", "since=" + "1794484800.000000123"} {
		if !strings.Contains(query, want) {
			t.Errorf("query %q lacks %q", query, want)
		}
	}

	if err := c.ContainerLogs(context.Background(), "missing", false, LogOptions{Tail: -1}, func(LogEntry) error { return nil }); err == nil {
		t.Error("missing container: want error")
	}
}
