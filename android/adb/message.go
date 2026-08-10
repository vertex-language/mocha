package adb

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
)

// Command constants for the ADB transport protocol (protocol.txt).
const (
	cmdSYNC uint32 = 0x434e5953
	cmdCNXN uint32 = 0x4e584e43
	cmdOPEN uint32 = 0x4e45504f
	cmdOKAY uint32 = 0x59414b4f
	cmdCLSE uint32 = 0x45534c43
	cmdWRTE uint32 = 0x45545257
	cmdAUTH uint32 = 0x48545541
)

// AUTH arg0 sub-types.
const (
	authToken     uint32 = 1
	authSignature uint32 = 2
	authRSAPubKey uint32 = 3
)

func cmdName(c uint32) string {
	switch c {
	case cmdSYNC:
		return "SYNC"
	case cmdCNXN:
		return "CNXN"
	case cmdOPEN:
		return "OPEN"
	case cmdOKAY:
		return "OKAY"
	case cmdCLSE:
		return "CLSE"
	case cmdWRTE:
		return "WRTE"
	case cmdAUTH:
		return "AUTH"
	default:
		return fmt.Sprintf("0x%08x", c)
	}
}

// message is the 24-byte ADB transport header plus its optional payload.
type message struct {
	command uint32
	arg0    uint32
	arg1    uint32
	payload []byte
}

const messageHeaderSize = 24

// writeMessage serializes and writes a single message to w.
func writeMessage(w io.Writer, m message) error {
	var hdr [messageHeaderSize]byte
	binary.LittleEndian.PutUint32(hdr[0:4], m.command)
	binary.LittleEndian.PutUint32(hdr[4:8], m.arg0)
	binary.LittleEndian.PutUint32(hdr[8:12], m.arg1)
	binary.LittleEndian.PutUint32(hdr[12:16], uint32(len(m.payload)))
	binary.LittleEndian.PutUint32(hdr[16:20], crc32.ChecksumIEEE(m.payload))
	binary.LittleEndian.PutUint32(hdr[20:24], m.command^0xffffffff)
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("adb: write message header: %w", err)
	}
	if len(m.payload) > 0 {
		if _, err := w.Write(m.payload); err != nil {
			return fmt.Errorf("adb: write message payload: %w", err)
		}
	}
	return nil
}

// readMessage reads a single message from r. maxData bounds the payload
// size accepted (mirrors the maxdata negotiated in CNXN); a header
// claiming a longer payload is a protocol violation.
func readMessage(r io.Reader, maxData uint32) (message, error) {
	var hdr [messageHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return message{}, fmt.Errorf("adb: read message header: %w", err)
	}
	m := message{
		command: binary.LittleEndian.Uint32(hdr[0:4]),
		arg0:    binary.LittleEndian.Uint32(hdr[4:8]),
		arg1:    binary.LittleEndian.Uint32(hdr[8:12]),
	}
	dataLen := binary.LittleEndian.Uint32(hdr[12:16])
	dataCRC := binary.LittleEndian.Uint32(hdr[16:20])
	magic := binary.LittleEndian.Uint32(hdr[20:24])

	if magic != m.command^0xffffffff {
		return message{}, fmt.Errorf("%w: bad magic for command %s", ErrProtocol, cmdName(m.command))
	}
	if dataLen > maxData {
		return message{}, fmt.Errorf("%w: payload length %d exceeds max %d", ErrProtocol, dataLen, maxData)
	}
	if dataLen > 0 {
		m.payload = make([]byte, dataLen)
		if _, err := io.ReadFull(r, m.payload); err != nil {
			return message{}, fmt.Errorf("adb: read message payload: %w", err)
		}
		if crc32.ChecksumIEEE(m.payload) != dataCRC {
			return message{}, fmt.Errorf("%w: payload crc mismatch", ErrProtocol)
		}
	}
	return m, nil
}