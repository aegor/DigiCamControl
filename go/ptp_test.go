package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"
)

func TestPacketFraming(t *testing.T) {
	var b bytes.Buffer
	if err := packet(&b, 6, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(b.Bytes()); got != "0a000000060000000102" {
		t.Fatal(got)
	}
	k, p, err := readPacket(&b)
	if err != nil || k != 6 || !bytes.Equal(p, []byte{1, 2}) {
		t.Fatal(k, p, err)
	}
	for _, n := range []uint32{0, 7, maxPacket + 1} {
		_, _, err = readPacket(bytes.NewReader(append(u32(n), u32(6)...)))
		if err == nil {
			t.Fatalf("accepted length %d", n)
		}
	}
	_, _, err = readPacket(bytes.NewReader([]byte{1}))
	if err == nil {
		t.Fatal("accepted truncated header")
	}
}
func TestICatchNameAndPTPString(t *testing.T) {
	if got := hex.EncodeToString(icatchName("localhost")); got != "6c006f00630061006c0068006f00730074000000" {
		t.Fatal(got)
	}
	raw := append([]byte{4}, icatchName("A😀")...)
	s, n, err := ptpString(raw)
	if err != nil || s != "A😀" || n != 9 {
		t.Fatal(s, n, err)
	}
	if _, _, err = ptpString([]byte{5, 0}); err == nil {
		t.Fatal("accepted truncated string")
	}
}
func response(w io.Writer, tx uint32, rc uint16) error {
	b := make([]byte, 6)
	le.PutUint16(b, rc)
	le.PutUint32(b[2:], tx)
	return packet(w, 7, b)
}

func TestRecordWireProtocol(t *testing.T) {
	for _, on := range []bool{true, false} {
		t.Run(map[bool]string{true: "start", false: "stop"}[on], func(t *testing.T) {
			client, server := net.Pipe()
			defer client.Close()
			defer server.Close()
			errs := make(chan error, 1)
			go func() {
				k, b, err := readPacket(server)
				if err != nil {
					errs <- err
					return
				}
				if k != 6 || len(b) != 14 || le.Uint32(b) != 2 || le.Uint16(b[4:]) != 0x1016 || le.Uint32(b[10:]) != 0xd604 {
					errs <- io.ErrUnexpectedEOF
					return
				}
				tx := le.Uint32(b[6:])
				k, b, err = readPacket(server)
				if err != nil {
					errs <- err
					return
				}
				if k != 9 || len(b) != 12 || le.Uint64(b[4:]) != 2 {
					errs <- io.ErrUnexpectedEOF
					return
				}
				k, b, err = readPacket(server)
				if err != nil {
					errs <- err
					return
				}
				mode := uint16(1)
				if on {
					mode = 17
				}
				if k != 12 || len(b) != 6 || le.Uint32(b) != tx || le.Uint16(b[4:]) != mode {
					errs <- io.ErrUnexpectedEOF
					return
				}
				errs <- response(server, tx, 0x2001)
			}()
			c := &camera{cmd: client}
			if err := c.setRecording(on); err != nil {
				t.Fatal(err)
			}
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		})
	}
}
func serveCameraOps(conn net.Conn) {
	defer conn.Close()
	for {
		kind, b, err := readPacket(conn)
		if err != nil {
			return
		}
		if kind != 6 || len(b) < 10 {
			return
		}
		op := le.Uint16(b[4:])
		tx := le.Uint32(b[6:])
		var data []byte
		switch op {
		case 0x1015:
			if len(b) >= 14 && le.Uint32(b[10:]) == 0x501f {
				data = append([]byte{5}, icatchName("test")...)
			} else {
				data = []byte{1, 0}
			}
		case 0x1001: // Minimal valid DeviceInfo with empty arrays and strings.
			data = make([]byte, 8)
			data = append(data, 0, 0, 0)
			data = append(data, make([]byte, 20)...)
			data = append(data, 0, 0, 0, 0)
		}
		if le.Uint32(b) == 2 {
			if _, _, err = readPacket(conn); err != nil {
				return
			}
			if _, _, err = readPacket(conn); err != nil {
				return
			}
		}
		if data != nil {
			if err = packet(conn, 12, append(u32(tx), data...)); err != nil {
				return
			}
		}
		if err = response(conn, tx, 0x2001); err != nil {
			return
		}
	}
}
func TestConnectHandshakeAndBusyRecovery(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	errors := make(chan error, 1)
	go func() {
		cmd, err := listener.Accept()
		if err != nil {
			errors <- err
			return
		}
		defer cmd.Close()
		cmd.SetDeadline(time.Now().Add(5 * time.Second))
		kind, b, err := readPacket(cmd)
		if err != nil {
			errors <- err
			return
		}
		if kind != 1 || !bytes.Equal(b[16:len(b)-4], icatchName("localhost")) {
			errors <- io.ErrUnexpectedEOF
			return
		}
		if err = packet(cmd, 2, u32(123)); err != nil {
			errors <- err
			return
		}
		events, err := listener.Accept()
		if err != nil {
			errors <- err
			return
		}
		defer events.Close()
		kind, b, err = readPacket(events)
		if err != nil || kind != 3 || !bytes.Equal(b, u32(123)) {
			errors <- io.ErrUnexpectedEOF
			return
		}
		packet(events, 4, nil)
		for i, op := range []uint16{0x1002, 0x1003, 0x1002} {
			kind, b, err = readPacket(cmd)
			if err != nil || kind != 6 || len(b) < 10 || le.Uint16(b[4:]) != op {
				errors <- io.ErrUnexpectedEOF
				return
			}
			rc := uint16(0x2001)
			if i == 0 {
				rc = 0x201e
			}
			if err = response(cmd, le.Uint32(b[6:]), rc); err != nil {
				errors <- err
				return
			}
		}
		errors <- nil
		var buf [1]byte
		cmd.Read(buf[:])
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, err := connectCamera(ctx, listener.Addr().String(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.close()
	if err = <-errors; err != nil {
		t.Fatal(err)
	}
	cancel()
	c.cmd.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = readPacket(c.cmd); err == nil {
		t.Fatal("cancel did not close socket")
	}
}
func TestPTPRejectsWrongTransaction(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	go func() { readPacket(server); response(server, 99, 0x2001) }()
	c := &camera{cmd: client}
	if _, err := c.mode(); err == nil {
		t.Fatal("accepted wrong transaction")
	}
}
