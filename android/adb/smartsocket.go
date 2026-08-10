package adb

import (
	"fmt"
	"io"
	"strconv"
)

// smartSocketRequest sends a single length-prefixed SmartSocket service
// request on conn and consumes the OKAY/FAIL response. On FAIL it reads
// and returns the length-prefixed reason as an error.
func smartSocketRequest(conn io.ReadWriter, service string) error {
	if err := smartSocketSend(conn, service); err != nil {
		return err
	}
	return smartSocketRecvStatus(conn)
}

func smartSocketSend(w io.Writer, service string) error {
	if len(service) > 0xffff {
		return fmt.Errorf("adb: service name too long: %d bytes", len(service))
	}
	header := fmt.Sprintf("%04x", len(service))
	if _, err := io.WriteString(w, header+service); err != nil {
		return fmt.Errorf("adb: send service request: %w", err)
	}
	return nil
}

func smartSocketRecvStatus(r io.Reader) error {
	var status [4]byte
	if _, err := io.ReadFull(r, status[:]); err != nil {
		return fmt.Errorf("adb: read service status: %w", err)
	}
	switch string(status[:]) {
	case "OKAY":
		return nil
	case "FAIL":
		reason, err := smartSocketReadHexString(r)
		if err != nil {
			return fmt.Errorf("adb: read failure reason: %w", err)
		}
		return fmt.Errorf("%w: %s", ErrServiceFailed, reason)
	default:
		return fmt.Errorf("%w: unexpected status %q", ErrProtocol, status[:])
	}
}

// smartSocketReadHexString reads a 4-digit hex length prefix followed by
// that many bytes of payload — the framing used throughout the SmartSocket
// convention (host:devices, FAIL reasons, sync file paths, and so on).
func smartSocketReadHexString(r io.Reader) (string, error) {
	var lenHdr [4]byte
	if _, err := io.ReadFull(r, lenHdr[:]); err != nil {
		return "", err
	}
	n, err := strconv.ParseUint(string(lenHdr[:]), 16, 32)
	if err != nil {
		return "", fmt.Errorf("%w: bad hex length %q", ErrProtocol, lenHdr[:])
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}