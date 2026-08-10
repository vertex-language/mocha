package adb

import (
	"bufio"
	"encoding/binary"
	"io"
)

// Shell protocol v2 packet ids.
const (
	shellIDStdout byte = 1
	shellIDStderr byte = 2
	shellIDExit   byte = 3
)

// shellV2Reader demultiplexes a shell_v2 stream into a single combined
// stdout+stderr io.Reader, remembering the exit code once it arrives.
type shellV2Reader struct {
	r        *bufio.Reader
	stream   io.Closer
	buf      []byte
	exitCode int
	exitSeen bool
}

func newShellV2Reader(rw io.ReadWriteCloser) *shellV2Reader {
	return &shellV2Reader{r: bufio.NewReader(rw), stream: rw}
}

func (s *shellV2Reader) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		var hdr [5]byte
		if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
			if err == io.EOF {
				return 0, io.EOF
			}
			return 0, err
		}
		id := hdr[0]
		n := binary.LittleEndian.Uint32(hdr[1:5])
		payload := make([]byte, n)
		if n > 0 {
			if _, err := io.ReadFull(s.r, payload); err != nil {
				return 0, err
			}
		}
		switch id {
		case shellIDStdout, shellIDStderr:
			s.buf = payload
		case shellIDExit:
			s.exitSeen = true
			if len(payload) > 0 {
				s.exitCode = int(payload[0])
			}
			return 0, io.EOF
		default:
			// stdin echo, window-size changes, close-stdin: not relevant
			// to a read-only combined stream.
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// ExitCode returns the process's exit code once the stream has been fully
// drained; it returns (0, false) before that.
func (s *shellV2Reader) ExitCode() (int, bool) { return s.exitCode, s.exitSeen }

func (s *shellV2Reader) Close() error { return s.stream.Close() }

var _ io.ReadCloser = (*shellV2Reader)(nil)