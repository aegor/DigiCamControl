package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
	"unicode/utf16"
)

const maxPacket = 16 * 1024 * 1024

var le = binary.LittleEndian

func u32(v uint32) []byte { b := make([]byte, 4); le.PutUint32(b, v); return b }
func packet(w io.Writer, kind uint32, body []byte) error {
	b := make([]byte, 8+len(body))
	le.PutUint32(b, uint32(len(b)))
	le.PutUint32(b[4:], kind)
	copy(b[8:], body)
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func readPacket(r io.Reader) (uint32, []byte, error) {
	var h [8]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := le.Uint32(h[:])
	if n < 8 || n > maxPacket {
		return 0, nil, fmt.Errorf("invalid PTP packet size %d", n)
	}
	b := make([]byte, int(n)-8)
	_, err := io.ReadFull(r, b)
	return le.Uint32(h[4:]), b, err
}
func icatchName(s string) []byte {
	units := append(utf16.Encode([]rune(s)), 0)
	b := make([]byte, 2*len(units))
	for i, v := range units {
		le.PutUint16(b[2*i:], v)
	}
	return b
}
func ptpString(b []byte) (string, int, error) {
	if len(b) == 0 {
		return "", 0, io.ErrUnexpectedEOF
	}
	n := int(b[0])
	size := 1 + 2*n
	if len(b) < size {
		return "", 0, io.ErrUnexpectedEOF
	}
	units := make([]uint16, 0, n)
	for i := 0; i < n; i++ {
		v := le.Uint16(b[1+2*i:])
		if v == 0 {
			break
		}
		units = append(units, v)
	}
	return string(utf16.Decode(units)), size, nil
}

// PTP transactions are serialized by Worker.commandMu. Both connections are
// closed by context cancellation to interrupt blocked network reads promptly.
type camera struct {
	cmd, events net.Conn
	tx          uint32
	stopCancel  func() bool
}

func connectCamera(ctx context.Context, address, bind string) (*camera, error) {
	dialer := net.Dialer{Timeout: 3 * time.Second}
	if bind != "" {
		ip := net.ParseIP(bind)
		if ip == nil {
			return nil, fmt.Errorf("invalid bind IP %q", bind)
		}
		dialer.LocalAddr = &net.TCPAddr{IP: ip}
	}
	cmd, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	c := &camera{cmd: cmd}
	// Install separate cancellation callbacks before each handshake; no shared
	// connection pointer is mutated by the cancellation goroutine.
	stopCmd := context.AfterFunc(ctx, func() { cmd.Close() })
	cleanup := true
	defer func() {
		if cleanup {
			stopCmd()
			c.close()
		}
	}()
	cmd.SetDeadline(time.Now().Add(3 * time.Second))
	guid := make([]byte, 16)
	if _, err = rand.Read(guid); err != nil {
		return nil, err
	}
	body := append(guid, icatchName("localhost")...)
	body = append(body, u32(0x10000)...)
	if err = packet(cmd, 1, body); err != nil {
		return nil, err
	}
	kind, ack, err := readPacket(cmd)
	if err != nil {
		return nil, err
	}
	if kind != 2 || len(ack) < 4 {
		return nil, fmt.Errorf("PTP handshake rejected: type=%d body=%x", kind, ack)
	}
	events, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	c.events = events
	stopEvents := context.AfterFunc(ctx, func() { events.Close() })
	c.stopCancel = func() bool { a := stopCmd(); b := stopEvents(); return a && b }
	events.SetDeadline(time.Now().Add(3 * time.Second))
	if err = packet(events, 3, ack[:4]); err != nil {
		return nil, err
	}
	kind, _, err = readPacket(events)
	if err != nil {
		return nil, err
	}
	if kind != 4 {
		return nil, fmt.Errorf("PTP event handshake: type=%d", kind)
	}
	rc, _, err := c.raw(0x1002, []uint32{1}, nil)
	if err == nil && (rc == 0x201e || rc == 0x2023) {
		_, _, _ = c.raw(0x1003, nil, nil)
		rc, _, err = c.raw(0x1002, []uint32{1}, nil)
	}
	if err != nil {
		return nil, err
	}
	if rc != 0x2001 {
		return nil, fmt.Errorf("OpenSession: PTP 0x%04x", rc)
	}
	events.SetDeadline(time.Time{})
	go func() {
		for {
			if _, _, err := readPacket(events); err != nil {
				return
			}
		}
	}()
	cleanup = false
	return c, nil
}
func (c *camera) close() {
	if c.stopCancel != nil {
		c.stopCancel()
	}
	if c.cmd != nil {
		c.cmd.Close()
	}
	if c.events != nil {
		c.events.Close()
	}
}
func (c *camera) raw(op uint16, params []uint32, data []byte) (uint16, []byte, error) {
	if c.cmd == nil {
		return 0, nil, errors.New("camera is not connected")
	}
	c.cmd.SetDeadline(time.Now().Add(3 * time.Second))
	c.tx++
	tx := c.tx
	if op == 0x1002 {
		tx = 0
		c.tx = 0
	}
	body := make([]byte, 10)
	phase := uint32(1)
	if len(data) > 0 {
		phase = 2
	}
	le.PutUint32(body, phase)
	le.PutUint16(body[4:], op)
	le.PutUint32(body[6:], tx)
	for _, v := range params {
		body = append(body, u32(v)...)
	}
	if err := packet(c.cmd, 6, body); err != nil {
		return 0, nil, err
	}
	if len(data) > 0 {
		start := make([]byte, 12)
		le.PutUint32(start, tx)
		le.PutUint64(start[4:], uint64(len(data)))
		if err := packet(c.cmd, 9, start); err != nil {
			return 0, nil, err
		}
		if err := packet(c.cmd, 12, append(u32(tx), data...)); err != nil {
			return 0, nil, err
		}
	}
	var received []byte
	for {
		kind, b, err := readPacket(c.cmd)
		if err != nil {
			return 0, nil, err
		}
		switch kind {
		case 9:
			if len(b) < 12 || le.Uint32(b) != tx || le.Uint64(b[4:]) > maxPacket {
				return 0, nil, errors.New("invalid PTP StartData")
			}
		case 10, 12:
			if len(b) < 4 || le.Uint32(b) != tx || len(received)+len(b)-4 > maxPacket {
				return 0, nil, errors.New("invalid PTP data")
			}
			received = append(received, b[4:]...)
		case 7:
			if len(b) < 6 || le.Uint32(b[2:]) != tx {
				return 0, nil, errors.New("invalid PTP response transaction")
			}
			return le.Uint16(b), received, nil
		case 8: // Unsolicited event on command channel.
		default:
			return 0, nil, fmt.Errorf("unexpected PTP packet %d", kind)
		}
	}
}
func (c *camera) op(op uint16, params []uint32, data []byte) ([]byte, error) {
	rc, b, err := c.raw(op, params, data)
	if err != nil {
		return nil, err
	}
	if rc != 0x2001 {
		return nil, fmt.Errorf("PTP operation 0x%04x failed: 0x%04x", op, rc)
	}
	return b, nil
}
func (c *camera) setRecording(on bool) error {
	mode := uint16(1)
	if on {
		mode = 17
	}
	b := make([]byte, 2)
	le.PutUint16(b, mode)
	_, err := c.op(0x1016, []uint32{0xd604}, b)
	return err
}
func (c *camera) mode() (uint16, error) {
	b, err := c.op(0x1015, []uint32{0xd604}, nil)
	if err != nil {
		return 0, err
	}
	if len(b) < 2 {
		return 0, io.ErrUnexpectedEOF
	}
	return le.Uint16(b), nil
}
func (c *camera) firmware() string {
	b, err := c.op(0x1015, []uint32{0x501f}, nil)
	if err != nil {
		return ""
	}
	s, _, _ := ptpString(b)
	return s
}
func (c *camera) model() string {
	b, err := c.op(0x1001, nil, nil)
	if err != nil || len(b) < 8 {
		return "V11"
	}
	b = b[8:]
	_, n, err := ptpString(b)
	if err != nil || len(b) < n+2 {
		return "V11"
	}
	b = b[n+2:]
	for i := 0; i < 5; i++ {
		if len(b) < 4 {
			return "V11"
		}
		n := uint64(le.Uint32(b))*2 + 4
		if n > uint64(len(b)) {
			return "V11"
		}
		b = b[n:]
	}
	_, n, err = ptpString(b)
	if err != nil {
		return "V11"
	}
	s, _, err := ptpString(b[n:])
	if err != nil || s == "" {
		return "V11"
	}
	return s
}
