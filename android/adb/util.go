package adb

import (
	"bytes"
	"io"
	"os"
	"strings"
)

// sizeReader returns r paired with its total byte length, determined
// without buffering into memory when r is an *os.File or otherwise
// implements io.Seeker; falls back to a full in-memory read otherwise.
func sizeReader(r io.Reader) (io.Reader, int64, error) {
	if f, ok := r.(*os.File); ok {
		if info, err := f.Stat(); err == nil {
			return f, info.Size(), nil
		}
	}
	if s, ok := r.(io.Seeker); ok {
		cur, err1 := s.Seek(0, io.SeekCurrent)
		end, err2 := s.Seek(0, io.SeekEnd)
		if err1 == nil && err2 == nil {
			if _, err := s.Seek(cur, io.SeekStart); err != nil {
				return nil, 0, err
			}
			return r, end - cur, nil
		}
	}
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	return bytes.NewReader(buf), int64(len(buf)), nil
}

func bytesContainsSuccess(b []byte) bool { return bytes.Contains(b, []byte("Success")) }

func bytesContainsError(b []byte) bool {
	return bytes.Contains(b, []byte("Error")) || bytes.Contains(b, []byte("Exception"))
}

func bytesTrimSpace(b []byte) string { return string(bytes.TrimSpace(b)) }

func containsDot(s string) bool { return strings.Contains(s, ".") }