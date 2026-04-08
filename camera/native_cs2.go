package camera

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type miHomeCS2Conn struct {
	net.Conn
	isTCP bool

	err    error
	seqCh0 uint16

	channels [4]*miHomeCS2DataChannel

	cmdMu  sync.Mutex
	cmdAck func()
}

const (
	miHomeCS2Magic        = 0xF1
	miHomeCS2MagicDRW     = 0xD1
	miHomeCS2MagicTCP     = 0x68
	miHomeCS2MsgLanSearch = 0x30
	miHomeCS2MsgPunchPkt  = 0x41
	miHomeCS2MsgP2PRdyUDP = 0x42
	miHomeCS2MsgP2PRdyTCP = 0x43
	miHomeCS2MsgDRW       = 0xD0
	miHomeCS2MsgDRWAck    = 0xD1
	miHomeCS2MsgPing      = 0xE0
	miHomeCS2MsgPong      = 0xE1
)

func newMiHomeCS2Conn(host string, transport string) (*miHomeCS2Conn, error) {
	conn, err := miHomeCS2Handshake(host, transport)
	if err != nil {
		return nil, err
	}
	_, isTCP := conn.(*miHomeCS2TCPConn)
	result := &miHomeCS2Conn{
		Conn:  conn,
		isTCP: isTCP,
		channels: [4]*miHomeCS2DataChannel{
			newMiHomeCS2DataChannel(0, 10),
			nil,
			newMiHomeCS2DataChannel(250, 100),
			nil,
		},
	}
	go result.worker()
	return result, nil
}

func miHomeCS2Handshake(host string, transport string) (net.Conn, error) {
	conn, err := newMiHomeCS2UDPConn(host, 32108)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	request := []byte{miHomeCS2Magic, miHomeCS2MsgLanSearch, 0, 0}
	response, err := conn.(*miHomeCS2UDPConn).writeUntil(request, func(response []byte) bool {
		return len(response) >= 2 && response[1] == miHomeCS2MsgPunchPkt
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	var udpReady, tcpReady byte
	if transport == "" || transport == "udp" {
		udpReady = miHomeCS2MsgP2PRdyUDP
	}
	if transport == "" || transport == "tcp" {
		tcpReady = miHomeCS2MsgP2PRdyTCP
	}
	response, err = conn.(*miHomeCS2UDPConn).writeUntil(response, func(response []byte) bool {
		return len(response) >= 2 && (response[1] == udpReady || response[1] == tcpReady)
	})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	_ = conn.SetDeadline(time.Time{})
	if response[1] == miHomeCS2MsgP2PRdyTCP {
		_ = conn.Close()
		return newMiHomeCS2TCPConn(conn.RemoteAddr().String())
	}
	return conn, nil
}

func (c *miHomeCS2Conn) Protocol() string {
	if c.isTCP {
		return "cs2+tcp"
	}
	return "cs2+udp"
}

func (c *miHomeCS2Conn) Version() string {
	return "CS2"
}

func (c *miHomeCS2Conn) Error() error {
	if c.err != nil {
		return c.err
	}
	return io.EOF
}

func (c *miHomeCS2Conn) worker() {
	defer func() {
		c.channels[0].Close()
		c.channels[2].Close()
	}()

	var keepaliveAt time.Time
	buffer := make([]byte, 1200)
	for {
		n, err := c.Conn.Read(buffer)
		if err != nil {
			c.err = fmt.Errorf("cs2: %w", err)
			return
		}
		switch buffer[1] {
		case miHomeCS2MsgDRW:
			channelID := buffer[5]
			channel := c.channels[channelID]
			if channel == nil {
				continue
			}
			if c.isTCP {
				if now := time.Now(); now.After(keepaliveAt) {
					_, _ = c.Conn.Write([]byte{miHomeCS2Magic, miHomeCS2MsgPing, 0, 0})
					keepaliveAt = now.Add(time.Second)
				}
				err = channel.Push(buffer[8:n])
			} else {
				seqHI, seqLO := buffer[6], buffer[7]
				seq := uint16(seqHI)<<8 | uint16(seqLO)
				pushed, pushErr := channel.PushSeq(seq, buffer[8:n])
				err = pushErr
				if pushed >= 0 {
					ack := []byte{miHomeCS2Magic, miHomeCS2MsgDRWAck, 0, 6, miHomeCS2MagicDRW, channelID, 0, 1, seqHI, seqLO}
					_, _ = c.Conn.Write(ack)
				}
			}
			if err != nil {
				c.err = fmt.Errorf("cs2: %w", err)
				return
			}
		case miHomeCS2MsgPing:
			_, _ = c.Conn.Write([]byte{miHomeCS2Magic, miHomeCS2MsgPong, 0, 0})
		case miHomeCS2MsgPong, miHomeCS2MsgP2PRdyUDP, miHomeCS2MsgP2PRdyTCP:
		case miHomeCS2MsgDRWAck:
			if c.cmdAck != nil {
				c.cmdAck()
			}
		}
	}
}

func (c *miHomeCS2Conn) ReadCommand() (uint32, []byte, error) {
	payload, ok := c.channels[0].Pop()
	if !ok {
		return 0, nil, c.Error()
	}
	return binary.LittleEndian.Uint32(payload), payload[4:], nil
}

func (c *miHomeCS2Conn) WriteCommand(command uint32, payload []byte) error {
	c.cmdMu.Lock()
	defer c.cmdMu.Unlock()

	request := miHomeCS2MarshalCommand(0, c.seqCh0, command, payload)
	c.seqCh0++
	c.cmdAck = nil
	_, err := c.Conn.Write(request)
	return err
}

func (c *miHomeCS2Conn) ReadPacket() ([]byte, []byte, error) {
	const headerSize = 32
	payload, ok := c.channels[2].Pop()
	if !ok {
		return nil, nil, c.Error()
	}
	if len(payload) < headerSize {
		return nil, nil, fmt.Errorf("cs2: media packet too small")
	}
	return payload[:headerSize], payload[headerSize:], nil
}

func miHomeCS2MarshalCommand(channel byte, sequence uint16, command uint32, payload []byte) []byte {
	request := make([]byte, 16+len(payload))
	request[0] = miHomeCS2Magic
	request[1] = miHomeCS2MsgDRW
	binary.BigEndian.PutUint16(request[2:], uint16(12+len(payload)))
	request[4] = miHomeCS2MagicDRW
	request[5] = channel
	binary.BigEndian.PutUint16(request[6:], sequence)
	binary.BigEndian.PutUint32(request[8:], uint32(4+len(payload)))
	binary.BigEndian.PutUint32(request[12:], command)
	copy(request[16:], payload)
	return request
}

type miHomeCS2UDPConn struct {
	*net.UDPConn
	addr *net.UDPAddr
}

func newMiHomeCS2UDPConn(host string, port int) (net.Conn, error) {
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	addr, err := net.ResolveUDPAddr("udp", host)
	if err != nil {
		addr = &net.UDPAddr{IP: net.ParseIP(host), Port: port}
	}
	return &miHomeCS2UDPConn{UDPConn: conn, addr: addr}, nil
}

func (c *miHomeCS2UDPConn) Read(buffer []byte) (int, error) {
	for {
		n, addr, err := c.UDPConn.ReadFromUDP(buffer)
		if err != nil {
			return 0, err
		}
		if addr != nil && addr.IP.Equal(c.addr.IP) || n >= 8 {
			return n, nil
		}
	}
}

func (c *miHomeCS2UDPConn) Write(buffer []byte) (int, error) {
	return c.UDPConn.WriteToUDP(buffer, c.addr)
}

func (c *miHomeCS2UDPConn) RemoteAddr() net.Addr {
	return c.addr
}

func (c *miHomeCS2UDPConn) writeUntil(request []byte, accepted func([]byte) bool) ([]byte, error) {
	if _, err := c.Write(request); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				_, _ = c.Write(request)
			}
		}
	}()

	buffer := make([]byte, 1200)
	for {
		n, addr, err := c.UDPConn.ReadFromUDP(buffer)
		if err != nil {
			return nil, err
		}
		if addr == nil || !addr.IP.Equal(c.addr.IP) || n == 0 {
			continue
		}
		if accepted(buffer[:n]) {
			c.addr.Port = addr.Port
			return append([]byte(nil), buffer[:n]...), nil
		}
	}
}

type miHomeCS2TCPConn struct {
	*net.TCPConn
	reader *bufio.Reader
}

func newMiHomeCS2TCPConn(address string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return &miHomeCS2TCPConn{TCPConn: conn.(*net.TCPConn), reader: bufio.NewReader(conn)}, nil
}

func (c *miHomeCS2TCPConn) Read(buffer []byte) (int, error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(header))
	if len(buffer) < n {
		return 0, fmt.Errorf("cs2 tcp: buffer too small")
	}
	_, err := io.ReadFull(c.reader, buffer[:n])
	return n, err
}

func (c *miHomeCS2TCPConn) Write(buffer []byte) (int, error) {
	packet := make([]byte, 8+len(buffer))
	binary.BigEndian.PutUint16(packet, uint16(len(buffer)))
	packet[2] = miHomeCS2MagicTCP
	copy(packet[8:], buffer)
	_, err := c.TCPConn.Write(packet)
	return len(buffer), err
}

type miHomeCS2DataChannel struct {
	waitSeq  uint16
	pushBuf  map[uint16][]byte
	pushSize int

	waitData []byte
	waitSize int
	popBuf   chan []byte
}

func newMiHomeCS2DataChannel(pushSize int, popSize int) *miHomeCS2DataChannel {
	channel := &miHomeCS2DataChannel{}
	if pushSize > 0 {
		channel.pushBuf = make(map[uint16][]byte, pushSize)
		channel.pushSize = pushSize
	}
	if popSize >= 0 {
		channel.popBuf = make(chan []byte, popSize)
	}
	return channel
}

func (c *miHomeCS2DataChannel) Push(buffer []byte) error {
	c.waitData = append(c.waitData, buffer...)
	for len(c.waitData) > 4 {
		if c.waitSize == 0 {
			c.waitSize = int(binary.BigEndian.Uint32(c.waitData))
			c.waitData = c.waitData[4:]
		}
		if c.waitSize > len(c.waitData) {
			break
		}
		select {
		case c.popBuf <- c.waitData[:c.waitSize]:
		default:
			return fmt.Errorf("cs2 pop buffer is full")
		}
		c.waitData = c.waitData[c.waitSize:]
		c.waitSize = 0
	}
	return nil
}

func (c *miHomeCS2DataChannel) PushSeq(sequence uint16, payload []byte) (int, error) {
	diff := int16(sequence - c.waitSeq)
	if diff > 0 {
		if c.pushSize == 0 {
			return -1, nil
		}
		if c.pushBuf[sequence] == nil {
			if len(c.pushBuf) == c.pushSize {
				return -1, nil
			}
			c.pushBuf[sequence] = bytes.Clone(payload)
		}
		return 0, nil
	}
	if diff < 0 {
		return 0, nil
	}
	for count := 1; ; count++ {
		if err := c.Push(payload); err != nil {
			return count, err
		}
		c.waitSeq++
		next := c.pushBuf[c.waitSeq]
		if next == nil {
			return count, nil
		}
		delete(c.pushBuf, c.waitSeq)
		payload = next
	}
}

func (c *miHomeCS2DataChannel) Pop() ([]byte, bool) {
	payload, ok := <-c.popBuf
	return payload, ok
}

func (c *miHomeCS2DataChannel) Close() {
	close(c.popBuf)
}
