package adb

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// LogcatOptions configures a Logcat stream.
type LogcatOptions struct {
	// Package restricts output to a single app's pid via `pidof`.
	Package string
	// Clear wipes the log buffer before streaming (logcat -c) so the
	// stream starts from the current moment.
	Clear bool
}

// LogLine is one parsed line of logcat -v brief output.
type LogLine struct {
	Priority string
	Tag      string
	PID      string
	Message  string
	Raw      string
}

// LogcatStream is a live tail of a device's log buffer.
type LogcatStream struct {
	stream  io.Closer
	scanner *bufio.Scanner
	lines   chan LogLine
	errc    chan error
}

// Logcat starts tailing the device's log buffer, optionally filtered to
// opts.Package.
func (d *Device) Logcat(ctx context.Context, opts LogcatOptions) (*LogcatStream, error) {
	if opts.Clear {
		clearStream, err := d.open(ctx, "shell:logcat -c")
		if err != nil {
			return nil, fmt.Errorf("adb: logcat clear: %w", err)
		}
		io.Copy(io.Discard, clearStream)
		clearStream.Close()
	}

	cmd := "logcat -v brief"
	if opts.Package != "" {
		cmd += " --pid=$(pidof -s " + opts.Package + ")"
	}
	stream, err := d.open(ctx, "shell:"+cmd)
	if err != nil {
		return nil, err
	}

	ls := &LogcatStream{
		stream:  stream,
		scanner: bufio.NewScanner(stream),
		lines:   make(chan LogLine, 64),
		errc:    make(chan error, 1),
	}
	go ls.pump()
	return ls, nil
}

func (ls *LogcatStream) pump() {
	defer close(ls.lines)
	for ls.scanner.Scan() {
		ls.lines <- parseLogLine(ls.scanner.Text())
	}
	if err := ls.scanner.Err(); err != nil {
		select {
		case ls.errc <- err:
		default:
		}
	}
}

// parseLogLine parses a "-v brief" formatted line: "P/Tag(  PID): message".
func parseLogLine(line string) LogLine {
	l := LogLine{Raw: line}
	slash := strings.IndexByte(line, '/')
	paren := strings.IndexByte(line, '(')
	colon := strings.Index(line, "): ")
	if slash > 0 && paren > slash && colon > paren {
		l.Priority = line[:slash]
		l.Tag = strings.TrimSpace(line[slash+1 : paren])
		l.PID = strings.TrimSpace(line[paren+1 : colon])
		l.Message = line[colon+3:]
	} else {
		l.Message = line
	}
	return l
}

// Stream returns the channel of parsed log lines, closed when the
// underlying shell session ends.
func (ls *LogcatStream) Stream() <-chan LogLine { return ls.lines }

// Err returns the error, if any, that ended the stream.
func (ls *LogcatStream) Err() error {
	select {
	case err := <-ls.errc:
		return err
	default:
		return nil
	}
}

func (ls *LogcatStream) Close() error { return ls.stream.Close() }