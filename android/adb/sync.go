package adb

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

// Sync is the binary file-synchronization sub-protocol opened by
// Device.Sync, used to implement push and pull. Once opened, the
// connection is in this binary mode — the normal SmartSocket/shell
// framing does not apply again until the session is closed.
type Sync struct {
	rw     *bufio.ReadWriter
	closer io.Closer
}

const (
	syncIDLIST = "LIST"
	syncIDSEND = "SEND"
	syncIDRECV = "RECV"
	syncIDDENT = "DENT"
	syncIDDONE = "DONE"
	syncIDDATA = "DATA"
	syncIDOKAY = "OKAY"
	syncIDFAIL = "FAIL"
)

// DirEntry describes one entry returned by List.
type DirEntry struct {
	Name  string
	Mode  os.FileMode
	Size  uint32
	Mtime time.Time
}

func (s *Sync) sendReq(id string, arg uint32) error {
	var hdr [8]byte
	copy(hdr[0:4], id)
	binary.LittleEndian.PutUint32(hdr[4:8], arg)
	_, err := s.rw.Write(hdr[:])
	return err
}

func (s *Sync) sendReqPath(id, path string) error {
	if err := s.sendReq(id, uint32(len(path))); err != nil {
		return err
	}
	if _, err := io.WriteString(s.rw, path); err != nil {
		return err
	}
	return s.rw.Flush()
}

func (s *Sync) readID() (string, uint32, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(s.rw, hdr[:]); err != nil {
		return "", 0, err
	}
	return string(hdr[0:4]), binary.LittleEndian.Uint32(hdr[4:8]), nil
}

// Push streams r to remotePath on the device with the given mode. The
// remote path/mode pair is encoded as "path,mode"; the file body follows
// as a sequence of DATA chunks capped at 64KB, with a closing DONE that
// carries the file's mtime instead of a byte count, acknowledged by OKAY.
func (s *Sync) Push(ctx context.Context, r io.Reader, remotePath string, mode os.FileMode) error {
	arg := fmt.Sprintf("%s,%d", remotePath, mode.Perm())
	if err := s.sendReqPath(syncIDSEND, arg); err != nil {
		return err
	}

	buf := make([]byte, syncChunkMax)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := r.Read(buf)
		if n > 0 {
			if err := s.sendReq(syncIDDATA, uint32(n)); err != nil {
				return err
			}
			if _, err := s.rw.Write(buf[:n]); err != nil {
				return err
			}
			if err := s.rw.Flush(); err != nil {
				return err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("adb: push: read source: %w", rerr)
		}
	}

	if err := s.sendReq(syncIDDONE, uint32(time.Now().Unix())); err != nil {
		return err
	}
	if err := s.rw.Flush(); err != nil {
		return err
	}
	id, arg2, err := s.readID()
	if err != nil {
		return err
	}
	if id != syncIDOKAY {
		return fmt.Errorf("%w: push: %s", ErrServiceFailed, s.readFailReason(id, arg2))
	}
	return nil
}

// Pull streams remotePath from the device into w.
func (s *Sync) Pull(ctx context.Context, remotePath string, w io.Writer) error {
	if err := s.sendReqPath(syncIDRECV, remotePath); err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, arg, err := s.readID()
		if err != nil {
			return err
		}
		switch id {
		case syncIDDATA:
			if _, err := io.CopyN(w, s.rw, int64(arg)); err != nil {
				return fmt.Errorf("adb: pull: write dest: %w", err)
			}
		case syncIDDONE:
			return nil
		case syncIDFAIL:
			reason := make([]byte, arg)
			io.ReadFull(s.rw, reason)
			return fmt.Errorf("%w: pull: %s", ErrServiceFailed, reason)
		default:
			return fmt.Errorf("%w: pull: unexpected id %q", ErrProtocol, id)
		}
	}
}

// List returns the contents of remotePath, a directory on the device.
func (s *Sync) List(ctx context.Context, remotePath string) ([]DirEntry, error) {
	if err := s.sendReqPath(syncIDLIST, remotePath); err != nil {
		return nil, err
	}
	var entries []DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, _, err := s.readID()
		if err != nil {
			return nil, err
		}
		switch id {
		case syncIDDENT:
			var rest [12]byte // mode, size, mtime — 4 bytes each
			if _, err := io.ReadFull(s.rw, rest[:]); err != nil {
				return nil, err
			}
			mode := binary.LittleEndian.Uint32(rest[0:4])
			size := binary.LittleEndian.Uint32(rest[4:8])
			mtime := binary.LittleEndian.Uint32(rest[8:12])

			var nameLenBuf [4]byte
			if _, err := io.ReadFull(s.rw, nameLenBuf[:]); err != nil {
				return nil, err
			}
			name := make([]byte, binary.LittleEndian.Uint32(nameLenBuf[:]))
			if _, err := io.ReadFull(s.rw, name); err != nil {
				return nil, err
			}
			entries = append(entries, DirEntry{
				Name:  string(name),
				Mode:  os.FileMode(mode),
				Size:  size,
				Mtime: time.Unix(int64(mtime), 0),
			})
		case syncIDDONE:
			return entries, nil
		default:
			return nil, fmt.Errorf("%w: list: unexpected id %q", ErrProtocol, id)
		}
	}
}

func (s *Sync) readFailReason(id string, arg uint32) string {
	if id != syncIDFAIL {
		return fmt.Sprintf("unexpected id %q", id)
	}
	reason := make([]byte, arg)
	io.ReadFull(s.rw, reason)
	return string(reason)
}

// Close ends the sync session.
func (s *Sync) Close() error {
	return s.closer.Close()
}