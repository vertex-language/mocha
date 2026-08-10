# adb

`package adb` implements the Android Debug Bridge wire protocol natively in Go — the SmartSocket protocol, the ADB transport framing, the SYNC file-transfer protocol, and the shell v2 subprocess protocol — without shelling out to Google's `adb` binary.

```go
import "github.com/vertex-language/mocha/android/adb"
```

```bash
go get github.com/vertex-language/mocha/android/adb
```

---

## Where this package sits

ADB is normally three programs: a **client** (the `adb` CLI), a **server/host** that stays resident and multiplexes connections, and a **daemon** (`adbd`) running on the device. The client and server share one executable and both run on the host machine, while adbd runs on the Android device itself. `package adb` *is* the client — it speaks the same wire protocols a real `adb` binary would, either to a locally running server on port 5037 or directly to a device/emulator over TCP.

It does **not** implement `adbd` or a USB transport; it assumes something (Google's server, or a device listening on TCP) is already on the other end of the socket.

---

## Invariants

**Zero external binary execution.** No `adb` process is spawned via `os/exec`. The package speaks the SmartSockets protocol on port 5037 to a running daemon, or connects directly to emulators/devices over TCP (ports 5554/5555 for emulator pairs, 5555 for wireless debugging) using pure Go network sockets.

**Streaming installation.** APKs are streamed directly into the device's package manager (`pm install -S`) via multiplexed shell streams. The APK is never written to a temporary file on the host's disk or the device's `/data/local/tmp` unless explicitly requested.

**Context-aware lifecycle.** All device connections, shell executions, and logcat streams are bound to `context.Context`. A cancelled context sends a `CLSE` packet to tear down the corresponding stream, preventing hanging shell sessions.

---

## Usage

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/vertex-language/mocha/android/adb"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Connect to the local ADB server (127.0.0.1:5037), or dial a device directly.
	client, err := adb.DialDefault()
	if err != nil {
		panic(fmt.Errorf("failed to connect to adb: %w", err))
	}
	defer client.Close()

	// 2. Discover connected devices
	devices, err := client.ListDevices(ctx)
	if err != nil || len(devices) == 0 {
		panic("no devices found")
	}

	target := devices[0]
	fmt.Printf("Deploying to %s (%s)\n", target.Serial, target.Model)

	// 3. Stream the compiled APK directly to the device's package manager
	apkFile, err := os.Open("out/app.apk")
	if err != nil {
		panic(err)
	}
	defer apkFile.Close()

	if err := target.Install(ctx, apkFile); err != nil {
		panic(fmt.Errorf("install failed: %w", err))
	}

	// 4. Launch via Activity Manager — package/activity come from the parsed manifest
	if err := target.Launch(ctx, "com.example.myapp", ".MainActivity"); err != nil {
		panic(fmt.Errorf("launch failed: %w", err))
	}

	// 5. Tail logcat for this package
	logs, err := target.Logcat(ctx, adb.LogcatOptions{Package: "com.example.myapp", Clear: true})
	if err != nil {
		panic(err)
	}
	for line := range logs.Stream() {
		fmt.Printf("[%s] %s\n", line.Priority, line.Message)
	}
}
```

---

## Protocol

ADB is three protocols stacked on top of each other. Bottom to top: a framed **transport** carries multiplexed streams; a **SmartSocket** convention lets a client ask the server/daemon for a named service on top of that transport; and two services — **sync** and **shell v2** — have their own binary sub-protocols once opened.

```
  client ──(hex4-len + ascii service name)──▶ SmartSocket :5037 ──▶ adb server ──▶ transport (USB/TCP) ──▶ adbd
                                                                                                              │
                                              stream multiplexing (OPEN/OKAY/WRTE/CLSE) ◀───────────────────┘
```

### 1. Transport framing

Every message on the transport consists of a 24-byte header followed by an optional payload, with the header made up of six 32-bit words sent little-endian.

| Field | Size | Meaning |
|---|---|---|
| `command` | 4 bytes | One of `CNXN`, `OPEN`, `OKAY`, `WRTE`, `CLSE`, `AUTH` |
| `arg0` | 4 bytes | Command-specific |
| `arg1` | 4 bytes | Command-specific |
| `data_length` | 4 bytes | Payload length (0 allowed) |
| `data_crc32` | 4 bytes | CRC32 of the payload |
| `magic` | 4 bytes | `command XOR 0xffffffff` |

An invalid header, a corrupt payload, or an unrecognized command must close the connection — the protocol relies on shared state, and any break in the message stream desynchronizes it. `SYNC` (the transport command, not the file-sync *service*) never appears on the wire — it exists only for the internal io pump to discard stale outbound messages while a connection is offline.

### 2. Handshake — CNXN and AUTH

Both sides open with a CONNECT message carrying a protocol version, the maximum payload size the sender will accept, and nothing else may be sent until it's received. The payload is a system identity string of the form `<systemtype>:<serialno>:<banner>`, where systemtype is `bootloader`, `device`, or `host`.

If the device doesn't already trust the host's key, an RSA challenge runs before `CNXN` completes: the server responds to the initial connect with an AUTH(TOKEN) message containing random data, the client signs it with its private key and returns AUTH(SIGNATURE), and if that verifies against a known key the daemon accepts with CNXN — otherwise the client can offer its public key via AUTH(RSAPUBLICKEY), which prompts the device to accept the new key's fingerprint.

### 3. Streams — OPEN / OKAY / WRTE / CLSE

Every logical stream (a shell session, a sync session, a port forward) is one multiplexed conversation over the single transport connection, identified by a `local-id`/`remote-id` pair.

A sender opens a stream by naming a destination — conventions include `shell`, `shell:<command>`, `sync:`, `tcp:<host>:<port>`, and `local-<kind>:<path>` — and the recipient must answer with either a ready message establishing the stream or a close indicating failure. Once ready, a write may not be sent until a ready has been received, and no further write may follow until another ready arrives — a peer that violates this ordering has its connection closed. Close messages that reference a stream the recipient no longer has open are simply ignored, since the stream may have already been torn down while the message was in flight.

### 4. SmartSockets — asking the server for a service

Once transport-connected, the *server* (port 5037) exposes a request/response convention distinct from the raw transport: the client sends a 4-digit hex length followed by the ASCII service name, and the server answers `OKAY`, or `FAIL` followed by a length-prefixed reason.

Relevant host services, from `SERVICES.TXT`:

| Service | Does |
|---|---|
| `host:version` | Returns the server's internal version as a 4-byte hex string, with no OKAY/FAIL framing. |
| `host:devices`, `host:devices-l` | Lists connected devices and their state, `-l` variant including device paths, then closes the connection. |
| `host:track-devices` | Same as `host:devices` but stays open, pushing a fresh list whenever a device is added, removed, or changes state. |
| `host:emulator:<port>` | Announces a newly started emulator's console port so the server knows to track it. |
| `host:transport:<serial>` | Switches the connection so every following request goes straight to the adbd daemon on that device. |
| `host-serial:<serial>:<req>`, `host-usb:`, `host-local:` | Prefix forms for targeting a specific device, the single USB device, or the single running emulator, respectively. |
| `<prefix>:forward:<local>;<remote>` | Asks the server to forward local connections to a remote address on the given device. |

### 5. Local services (once bound to a device)

| Service | Does |
|---|---|
| `shell:<cmd>` | Runs a command on the device and returns its output and error streams; a bare `shell:` opens an interactive session instead. |
| `remount:` | Asks adbd to remount the filesystem read-write, usually a prerequisite for sync operations. |
| `tcp:<port>[:<host>]` | Connects to a TCP port on localhost, or on a named host reachable from the device. |
| `local:<path>` | Connects to a Unix domain socket at the given device-side path. |
| `framebuffer:` | Streams raw framebuffer snapshots: a 16-byte header (depth, size, width, height) followed by one frame per byte the client sends. |
| `jdwp:<pid>` / `track-jdwp` | Attaches to the JDWP thread of a running VM process, or streams the list of debuggable pids. |
| `sync:` | Switches the stream into the binary file-synchronization protocol used by push and pull, detailed below. |

### 6. Sync protocol — push / pull

Opening `sync:` doesn't return to normal SmartSocket framing until the sync session ends: the connection enters a binary mode that persists until explicitly terminated, where client and server exchange eight-byte packets — a 4-character ASCII id followed by a little-endian 4-byte length. The requests actually implemented are LIST, SEND, and RECV, each followed by a length-prefixed UTF-8 remote path; STAT and ULNK exist but are less consistently documented across versions.

- **LIST** — the server streams one directory entry per file as a `DENT` record (mode, size, mtime, name-length, name) and signals the end of the listing with `DONE`.
- **SEND** — the remote path argument is actually `path,mode` split on the last comma, and the file body follows as a sequence of `DATA` chunks capped at 64KB each, with a closing `DONE` whose length field carries the file's mtime instead of a byte count, acknowledged by a single `OKAY`.
- **RECV** — the server answers with the same `DATA`-chunks-then-`DONE` shape, chunks again capped at 64KB.

### 7. Shell protocol v2 (shell_v2)

The plain `shell:` service only returns interleaved stdout+stderr with no exit code. Devices from Android 5.0 onward additionally support a packetized version layered on top of the same opened stream: each piece of data — stdin, stdout, stderr, or control info like the exit code and window-size changes — is wrapped in a small packet of a 1-byte id, a 4-byte length, and the payload itself. The defined ids include stdin (0), stdout (1), stderr (2), and exit (3), plus a close-stdin control packet. Unlike v1, this gives real separated stdout/stderr channels, a genuine exit code, and a real-time stdin stream, at the cost of requiring shell_v2 support on the device side.

---

## Core Types

### `Client` — the server/daemon connection

```go
type Client struct {
	// ... unexported fields
}

func Dial(addr string) (*Client, error)
func DialDefault() (*Client, error) // 127.0.0.1:5037
func (c *Client) ListDevices(ctx context.Context) ([]*Device, error)
func (c *Client) WaitForDevice(ctx context.Context) (*Device, error)
```

### `Device` — a transport-bound target

```go
type Device struct {
	Serial string
	State  string // "device", "offline", "unauthorized"
	Model  string
}

func (d *Device) Shell(ctx context.Context, cmd string) (io.ReadCloser, error)
func (d *Device) Install(ctx context.Context, r io.Reader) error
func (d *Device) Launch(ctx context.Context, pkg, activity string) error
```

### `Sync` — the file-transfer sub-protocol

```go
type Sync struct {
	// ... unexported fields
}

func (d *Device) Sync(ctx context.Context) (*Sync, error)
func (s *Sync) Push(ctx context.Context, r io.Reader, remotePath string, mode os.FileMode) error
func (s *Sync) Pull(ctx context.Context, remotePath string, w io.Writer) error
```

---

## Relationship to Other Packages

- **[`bundle`](../bundle)** — generates the `.apk` that `adb.Device.Install()` streams to the device.
- **[`manifest`](../manifest)** — supplies the `PackageName` and main `<activity>` name `adb.Device.Launch()` needs to start the app via `am start`.