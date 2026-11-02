package client

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// TestDBusReadFailingMidMessageEndsTheConnection checks that a bus connection whose read fails
// while a message is part-read ends with an error, at every point of a message. Before godbus
// v5.2.4 the read of the padding between the header fields and the body was not recovered, so a
// connection closed there panicked the process (ginsys/bronzeward#556, left by
// ginsys/bronzeward#543, which removed the trigger seen with gnome-keyring). The stand-in bus
// completes the authentication, sends one signal cut at a chosen point and closes the socket.
func TestDBusReadFailingMidMessageEndsTheConnection(t *testing.T) {
	var whole []byte
	var headerEnd int
	for n := 1; n <= 8 && whole == nil; n++ {
		msg := &dbus.Message{
			Type: dbus.TypeSignal,
			Headers: map[dbus.HeaderField]dbus.Variant{
				dbus.FieldPath:      dbus.MakeVariant(dbus.ObjectPath("/p")),
				dbus.FieldInterface: dbus.MakeVariant("a.b"),
				dbus.FieldMember:    dbus.MakeVariant(strings.Repeat("m", n)),
				dbus.FieldSignature: dbus.MakeVariant(dbus.SignatureOf("")),
			},
			Body: []any{"body"},
		}
		var buf bytes.Buffer
		if err := msg.EncodeTo(&buf, binary.LittleEndian); err != nil {
			t.Fatal(err)
		}
		if end := 16 + int(binary.LittleEndian.Uint32(buf.Bytes()[12:16])); end%8 != 0 {
			whole, headerEnd = buf.Bytes(), end
		}
	}
	if whole == nil {
		t.Fatal("no signal in the sample has its header fields end off an 8-byte boundary")
	}
	bodyStart := (headerEnd + 7) &^ 7
	for _, c := range []struct {
		name string
		cut  int
	}{
		{"in the fixed header", 8},
		{"in the header fields", 16 + (headerEnd-16)/2},
		{"in the padding before the body", headerEnd},
		{"before the body", bodyStart},
		{"in the body", bodyStart + 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			conn := dialCutDBus(t, whole[:c.cut])
			select {
			case <-conn.Context().Done():
			case <-time.After(10 * time.Second):
				t.Fatal("the connection is still open 10 s after the bus closed it part-way through a message")
			}
		})
	}
}

// dialCutDBus connects to a stand-in bus that authenticates the client, writes the bytes it is
// given and closes the socket. The connection it returns has no Hello made: nothing is sent
// after the authentication.
func dialCutDBus(t *testing.T, sent []byte) *dbus.Conn {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		nc, err := l.Accept()
		if err != nil {
			return
		}
		defer nc.Close()
		r := bufio.NewReader(nc)
		if _, err := r.ReadByte(); err != nil { // the credentials byte
			return
		}
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.TrimSpace(line) == "AUTH": // the client asks which mechanisms are offered
				nc.Write([]byte("REJECTED EXTERNAL\r\n"))
			case strings.HasPrefix(line, "AUTH EXTERNAL"):
				nc.Write([]byte("OK 0123456789abcdef0123456789abcdef\r\n"))
			case strings.HasPrefix(line, "NEGOTIATE_UNIX_FD"):
				nc.Write([]byte("ERROR\r\n"))
			case strings.HasPrefix(line, "BEGIN"):
				nc.Write(sent)
				return
			}
		}
	}()
	nc, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c, err := dbus.NewConn(nc)
	if err != nil {
		nc.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	if err := c.Auth(nil); err != nil {
		t.Fatalf("authentication with the stand-in bus: %v", err)
	}
	return c
}
