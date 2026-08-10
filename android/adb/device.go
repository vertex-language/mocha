package adb

import (
	"bufio"
	"context"
	"fmt"
	"io"
)

// Device is a single ADB target — a physical device, an emulator, or a
// wireless-debugging endpoint — reachable either through an adb server
// (the common case, produced by Client.ListDevices / WaitForDevice) or by
// dialing it directly over TCP (DialDevice).
type Device struct {
	Serial string
	State  string // "device", "offline", "unauthorized"
	Model  string

	client *Client       // set when reached through a server
	direct *muxTransport // set when connected to directly
}

// DialDevice connects directly to a device or emulator listening on addr
// (e.g. wireless debugging on port 5555, or an emulator's odd console
// port), bypassing any local adb server. A per-user RSA key is loaded from
// (or generated into) ~/.android/adbkey, matching the real adb client's
// convention; devices that don't already trust it will reject the
// connection until the user accepts an on-device authorization prompt.
func DialDevice(ctx context.Context, addr string) (*Device, error) {
	key, err := loadOrCreateHostKey()
	if err != nil {
		return nil, err
	}
	t, err := dialDeviceTransport(ctx, addr, key)
	if err != nil {
		return nil, err
	}
	return &Device{Serial: addr, State: "device", direct: t}, nil
}

// Close releases the device's direct transport, if any. Devices reached
// through a server hold no persistent connection and need no closing.
func (d *Device) Close() error {
	if d.direct != nil {
		return d.direct.close()
	}
	return nil
}

// open starts a new logical stream naming the given local service (see
// SERVICES.TXT, "LOCAL SERVICES"), such as "shell:pm list packages" or
// "sync:".
func (d *Device) open(ctx context.Context, service string) (io.ReadWriteCloser, error) {
	if d.direct != nil {
		return d.direct.open(ctx, service)
	}
	if d.client == nil {
		return nil, fmt.Errorf("adb: device %s has no transport", d.Serial)
	}
	conn, err := d.client.dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := smartSocketRequest(conn, "host:transport:"+d.Serial); err != nil {
		conn.Close()
		return nil, err
	}
	if err := smartSocketRequest(conn, service); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Shell runs cmd on the device via shell protocol v2 (separated
// stdout/stderr, a real exit code) when the daemon supports it, falling
// back to the plain v1 shell service — which interleaves stdout/stderr and
// carries no exit code — otherwise.
func (d *Device) Shell(ctx context.Context, cmd string) (io.ReadCloser, error) {
	stream, err := d.open(ctx, "shell,v2,raw:"+cmd)
	if err != nil {
		stream, err = d.open(ctx, "shell:"+cmd)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(stream), nil
	}
	return newShellV2Reader(stream), nil
}

// Install streams an APK directly into the device's package manager via
// `pm install -S <size>` over a binary-safe exec: stream, never touching
// disk on either end. If r is an *os.File or otherwise implements
// io.Seeker, its size is determined without buffering it into memory.
func (d *Device) Install(ctx context.Context, r io.Reader) error {
	src, size, err := sizeReader(r)
	if err != nil {
		return fmt.Errorf("adb: determine apk size: %w", err)
	}

	stream, err := d.open(ctx, fmt.Sprintf("exec:pm install -S %d", size))
	if err != nil {
		return err
	}
	defer stream.Close()

	copyErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(stream, src)
		if cw, ok := stream.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
		copyErr <- err
	}()

	out, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("adb: install: %w", err)
	}
	if err := <-copyErr; err != nil {
		return fmt.Errorf("adb: install: stream apk: %w", err)
	}
	if !bytesContainsSuccess(out) {
		return fmt.Errorf("adb: install failed: %s", bytesTrimSpace(out))
	}
	return nil
}

// Launch starts pkg/activity via the Activity Manager, equivalent to
// `am start -n pkg/activity`.
func (d *Device) Launch(ctx context.Context, pkg, activity string) error {
	if activity != "" && !containsDot(activity) {
		activity = "." + activity
	}
	stream, err := d.open(ctx, fmt.Sprintf("shell:am start -n %s/%s", pkg, activity))
	if err != nil {
		return err
	}
	defer stream.Close()
	out, err := io.ReadAll(stream)
	if err != nil {
		return fmt.Errorf("adb: launch: %w", err)
	}
	if bytesContainsError(out) {
		return fmt.Errorf("adb: launch failed: %s", bytesTrimSpace(out))
	}
	return nil
}

// Sync opens the binary file-synchronization sub-protocol used by push
// and pull. Opening "sync:" switches the stream into this binary mode
// until the session ends.
func (d *Device) Sync(ctx context.Context) (*Sync, error) {
	stream, err := d.open(ctx, "sync:")
	if err != nil {
		return nil, err
	}
	return &Sync{
		rw:     bufio.NewReadWriter(bufio.NewReader(stream), bufio.NewWriter(stream)),
		closer: stream,
	}, nil
}