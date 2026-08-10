// Package adb implements the Android Debug Bridge (ADB) wire protocol
// natively in Go: the SmartSocket protocol used to talk to a local adb
// server, the ADB transport framing used to talk directly to a device or
// emulator, the SYNC file-transfer protocol, and the shell v2 subprocess
// protocol. It never shells out to a system adb binary.
package adb

import (
	"errors"
	"time"
)

// Default addresses and timeouts.
const (
	// DefaultServerAddr is the address of a locally running adb server.
	DefaultServerAddr = "127.0.0.1:5037"

	// DefaultDialTimeout bounds the time spent establishing a TCP
	// connection to a server or device.
	DefaultDialTimeout = 10 * time.Second

	// connectVersion is the ADB transport protocol version sent in CNXN.
	connectVersion uint32 = 0x01000000

	// connectMaxData is the maximum message payload size this package is
	// willing to accept, advertised in CNXN.
	connectMaxData uint32 = 1024 * 1024

	// syncChunkMax is the maximum size, in bytes, of a single DATA chunk
	// in the sync protocol.
	syncChunkMax = 64 * 1024
)

// Errors returned by this package.
var (
	ErrNoDevices     = errors.New("adb: no devices found")
	ErrClosed        = errors.New("adb: connection closed")
	ErrProtocol      = errors.New("adb: protocol violation")
	ErrAuthRequired  = errors.New("adb: device requires authorization")
	ErrUnauthorized  = errors.New("adb: device did not accept host key")
	ErrServiceFailed = errors.New("adb: service request failed")
	ErrStreamClosed  = errors.New("adb: stream closed by peer")
)