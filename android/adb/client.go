package adb

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
)

// Client is a connection to an adb server (the host-side program that
// multiplexes access to devices), reached via the SmartSocket protocol on
// port 5037.
type Client struct {
	addr string
}

// Dial connects to an adb server (or anything speaking its SmartSocket
// protocol) at addr.
func Dial(addr string) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, DefaultDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("adb: dial %s: %w", addr, err)
	}
	defer conn.Close()
	// host:version doubles as a reachability check: it responds with a
	// bare 4-byte hex string and no OKAY/FAIL framing (SERVICES.TXT).
	if err := smartSocketSend(conn, "host:version"); err != nil {
		return nil, err
	}
	if _, err := smartSocketReadRawHex4(conn); err != nil {
		return nil, fmt.Errorf("adb: server at %s did not respond to host:version: %w", addr, err)
	}
	return &Client{addr: addr}, nil
}

// DialDefault connects to the adb server on the standard local port,
// 127.0.0.1:5037.
func DialDefault() (*Client, error) {
	return Dial(DefaultServerAddr)
}

// Close releases resources held by the client. Client does not hold a
// persistent connection to the server — every request opens a fresh
// SmartSocket connection — so Close is a no-op kept for API symmetry.
func (c *Client) Close() error { return nil }

func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, fmt.Errorf("adb: dial %s: %w", c.addr, err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	return conn, nil
}

// ListDevices asks the server for every device and emulator it currently
// knows about (host:devices-l).
func (c *Client) ListDevices(ctx context.Context) ([]*Device, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := smartSocketRequest(conn, "host:devices-l"); err != nil {
		return nil, err
	}
	body, err := smartSocketReadHexString(conn)
	if err != nil {
		return nil, fmt.Errorf("adb: read device list: %w", err)
	}
	return parseDeviceList(c, body), nil
}

// parseDeviceList parses the plain-text table returned by host:devices-l:
// one device per line, "<serial> <state> [product:p model:m device:d] ...".
func parseDeviceList(c *Client, body string) []*Device {
	var devices []*Device
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := &Device{
			client: c,
			Serial: fields[0],
			State:  fields[1],
		}
		for _, f := range fields[2:] {
			if model, ok := strings.CutPrefix(f, "model:"); ok {
				d.Model = model
			}
		}
		devices = append(devices, d)
	}
	return devices
}

// WaitForDevice blocks until at least one device is in the "device" state,
// using host:track-devices to watch for state changes rather than polling.
func (c *Client) WaitForDevice(ctx context.Context) (*Device, error) {
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := smartSocketRequest(conn, "host:track-devices"); err != nil {
		return nil, err
	}
	for {
		body, err := smartSocketReadHexString(conn)
		if err != nil {
			return nil, fmt.Errorf("adb: track-devices: %w", err)
		}
		for _, d := range parseDeviceList(c, body) {
			if d.State == "device" {
				return d, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
}

func smartSocketReadRawHex4(r io.Reader) (string, error) {
	var buf [4]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return "", err
	}
	return string(buf[:]), nil
}