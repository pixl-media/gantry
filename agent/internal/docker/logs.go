package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxLineLength is where log lines are cut off.
const MaxLineLength = 16 << 10

// maxFrameSize guards against corrupt frame headers.
const maxFrameSize = 1 << 20

// LogEntry is one line of container output.
type LogEntry struct {
	Time   time.Time // when Docker received the line; zero if unknown
	Stream string    // "stdout" or "stderr"
	Line   string    // without the trailing newline
}

// LogOptions select which part of a container's log to read.
type LogOptions struct {
	// Tail is the number of lines from the end; negative means all.
	Tail int
	// Since starts after this time; zero means from the beginning.
	Since time.Time
	// Follow keeps the stream open for new lines until the container stops.
	Follow bool
}

// ContainerTTY reports whether a container runs with a TTY. Its logs are
// then plain text; otherwise Docker multiplexes stdout and stderr in frames.
func (c *Client) ContainerTTY(ctx context.Context, id string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/containers/"+url.PathEscape(id)+"/json", nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("inspect container: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, statusError("inspect container", resp)
	}
	var info struct {
		Config struct{ Tty bool }
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return false, fmt.Errorf("inspect container: decode response: %w", err)
	}
	return info.Config.Tty, nil
}

// ContainerLogs reads a container's logs and calls fn for every line, until
// the log ends (with Follow: until the container stops), ctx ends, or fn
// returns an error.
func (c *Client) ContainerLogs(ctx context.Context, id string, tty bool, opts LogOptions, fn func(LogEntry) error) error {
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}}
	if opts.Follow {
		q.Set("follow", "1")
	}
	if opts.Tail >= 0 {
		q.Set("tail", strconv.Itoa(opts.Tail))
	} else {
		q.Set("tail", "all")
	}
	if !opts.Since.IsZero() {
		q.Set("since", fmt.Sprintf("%d.%09d", opts.Since.Unix(), opts.Since.Nanosecond()))
	}
	u := "http://docker/containers/" + url.PathEscape(id) + "/logs?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return fmt.Errorf("container logs: %w", unwrapURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError("container logs", resp)
	}

	var readErr error
	if tty {
		readErr = readLines(resp.Body, "stdout", fn)
	} else {
		readErr = demultiplex(resp.Body, fn)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return readErr
}

// demultiplex reads Docker's multiplexed log format: frames with an 8-byte
// header (stream type, 3 zero bytes, big-endian payload length).
func demultiplex(r io.Reader, fn func(LogEntry) error) error {
	br := bufio.NewReader(r)
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, header); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		stream := "stdout"
		if header[0] == 2 {
			stream = "stderr"
		}
		size := binary.BigEndian.Uint32(header[4:])
		if size > maxFrameSize {
			return fmt.Errorf("log frame of %d bytes is too large", size)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(br, payload); err != nil {
			return err
		}
		// A frame normally holds one line; split defensively anyway.
		for line := range bytes.Lines(payload) {
			if err := fn(parseEntry(stream, line)); err != nil {
				return err
			}
		}
	}
}

// readLines reads a TTY's plain log stream line by line.
func readLines(r io.Reader, stream string, fn func(LogEntry) error) error {
	br := bufio.NewReaderSize(r, 64<<10)
	for {
		line, err := readLine(br)
		if len(line) > 0 {
			if ferr := fn(parseEntry(stream, line)); ferr != nil {
				return ferr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLine reads up to a newline, keeping at most a little more than
// MaxLineLength bytes; parseEntry cuts the rest.
func readLine(br *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(line) < MaxLineLength+64 {
			line = append(line, chunk...)
		}
		if err != nil || !isPrefix {
			return line, err
		}
	}
}

// parseEntry splits Docker's RFC 3339 timestamp from a line and cuts off
// overlong text at a valid UTF-8 boundary.
func parseEntry(stream string, raw []byte) LogEntry {
	s := strings.TrimRight(string(raw), "\r\n")
	e := LogEntry{Stream: stream, Line: s}
	if ts, rest, ok := strings.Cut(s, " "); ok {
		if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			e.Time, e.Line = t, rest
		}
	} else if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		e.Time, e.Line = t, "" // an empty line
	}
	if len(e.Line) > MaxLineLength {
		cut := MaxLineLength
		for cut > 0 && !utf8.RuneStart(e.Line[cut]) {
			cut--
		}
		e.Line = e.Line[:cut] + " …"
	}
	return e
}

func statusError(what string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("%s: %s: %s", what, resp.Status, strings.TrimSpace(string(body)))
}

// unwrapURLError drops the internal request URL from transport errors.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
