package camera

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestMiHomeCS2UDPConnWriteUntilAcceptsShortPunchPacket(t *testing.T) {
	t.Parallel()

	server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(server) error = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	clientConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP(client) error = %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })

	serverAddr := server.LocalAddr().(*net.UDPAddr)
	loopbackAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: serverAddr.Port}

	conn := &miHomeCS2UDPConn{
		UDPConn: clientConn,
		addr:    loopbackAddr,
	}
	if err := conn.SetDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("SetDeadline() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buffer := make([]byte, 32)
		n, addr, readErr := server.ReadFromUDP(buffer)
		if readErr != nil {
			return
		}
		if n == 0 {
			return
		}
		_, _ = server.WriteToUDP([]byte{miHomeCS2Magic, miHomeCS2MsgPunchPkt, 0x00, 0x00}, addr)
	}()

	response, err := conn.writeUntil([]byte{miHomeCS2Magic, miHomeCS2MsgLanSearch, 0x00, 0x00}, func(response []byte) bool {
		return len(response) >= 2 && response[1] == miHomeCS2MsgPunchPkt
	})
	if err != nil {
		t.Fatalf("writeUntil() error = %v", err)
	}
	<-done

	if got, want := len(response), 4; got != want {
		t.Fatalf("len(response) = %d, want %d", got, want)
	}
	if got, want := response[1], byte(miHomeCS2MsgPunchPkt); got != want {
		t.Fatalf("response[1] = 0x%02x, want 0x%02x", got, want)
	}
}

func TestMiHomeCS2ConnWriteCommandUDPDoesNotWaitForAck(t *testing.T) {
	fake := &fakeCS2NetConn{}
	conn := &miHomeCS2Conn{
		Conn: fake,
	}

	done := make(chan error, 1)
	go func() {
		done <- conn.WriteCommand(miHomeMissCmdEncoded, []byte("payload"))
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteCommand() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("WriteCommand() blocked waiting for a UDP ack")
	}

	if got, want := len(fake.writes), 1; got != want {
		t.Fatalf("writes = %d, want %d", got, want)
	}
}

type fakeCS2NetConn struct {
	mu     sync.Mutex
	writes [][]byte
}

func (f *fakeCS2NetConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (f *fakeCS2NetConn) Close() error                     { return nil }
func (f *fakeCS2NetConn) LocalAddr() net.Addr              { return fakeCS2Addr("local") }
func (f *fakeCS2NetConn) RemoteAddr() net.Addr             { return fakeCS2Addr("remote") }
func (f *fakeCS2NetConn) SetDeadline(time.Time) error      { return nil }
func (f *fakeCS2NetConn) SetReadDeadline(time.Time) error  { return nil }
func (f *fakeCS2NetConn) SetWriteDeadline(time.Time) error { return nil }

func (f *fakeCS2NetConn) Write(buffer []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes = append(f.writes, append([]byte(nil), buffer...))
	return len(buffer), nil
}

type fakeCS2Addr string

func (a fakeCS2Addr) Network() string { return "udp" }
func (a fakeCS2Addr) String() string  { return string(a) }
