package miot

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

const (
	lanPacketMagic  = 0x2131
	lanHeaderLength = 32
	lanPort         = 54321
)

// LANRequest is one typed LAN RPC request payload.
type LANRequest struct {
	ID     uint32          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// LANResponse is one typed LAN RPC response payload.
type LANResponse struct {
	ID     uint32          `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Code   int             `json:"code,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// LANMessage is the decoded JSON message inside one LAN packet.
type LANMessage struct {
	ID     uint32          `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Code   int             `json:"code,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// LANDeviceConfig configures one local MIoT LAN device credential and route.
type LANDeviceConfig struct {
	DID       string
	Token     string
	IP        string
	Interface string
	Offset    uint32
}

// LANDevice owns the packet codec for one MIoT LAN device token.
type LANDevice struct {
	did    string
	token  []byte
	ip     string
	ifName string
	offset uint32
	block  cipher.Block
	iv     []byte
}

// NewLANDevice creates a packet codec for one local device token.
func NewLANDevice(cfg LANDeviceConfig) (*LANDevice, error) {
	token, err := hex.DecodeString(cfg.Token)
	if err != nil {
		return nil, Wrap(ErrInvalidArgument, "new lan device", err)
	}
	if len(token) != 16 {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new lan device", Msg: "token must decode to 16 bytes"}
	}
	key := md5.Sum(token)
	ivSrc := append(append([]byte(nil), key[:]...), token...)
	iv := md5.Sum(ivSrc)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return &LANDevice{
		did:    cfg.DID,
		token:  token,
		ip:     cfg.IP,
		ifName: cfg.Interface,
		offset: cfg.Offset,
		block:  block,
		iv:     iv[:],
	}, nil
}

// DID returns the device identifier.
func (d *LANDevice) DID() string {
	return d.did
}

// Interface returns the last configured network interface name.
func (d *LANDevice) Interface() string {
	return d.ifName
}

// IP returns the configured device address.
func (d *LANDevice) IP() string {
	return d.ip
}

// UpdateRoute records the latest observed device route.
func (d *LANDevice) UpdateRoute(ip, ifName string) {
	if ip != "" {
		d.ip = ip
	}
	if ifName != "" {
		d.ifName = ifName
	}
}

// BuildPacket encrypts one request payload into a MIoT LAN packet.
func (d *LANDevice) BuildPacket(req LANRequest) ([]byte, error) {
	return d.buildPacketMessage(LANMessage{
		ID:     req.ID,
		Method: req.Method,
		Params: req.Params,
	})
}

// BuildResponsePacket encrypts one response payload into a MIoT LAN packet.
func (d *LANDevice) BuildResponsePacket(resp LANResponse) ([]byte, error) {
	return d.buildPacketMessage(LANMessage{
		ID:     resp.ID,
		Result: resp.Result,
		Code:   resp.Code,
		Error:  resp.Error,
	})
}

// ParsePacket decrypts one MIoT LAN packet into its JSON payload.
func (d *LANDevice) ParsePacket(packet []byte) (LANMessage, error) {
	if len(packet) < lanHeaderLength {
		return LANMessage{}, &Error{Code: ErrInvalidResponse, Op: "parse lan packet", Msg: "packet too short"}
	}
	if binary.BigEndian.Uint16(packet[0:2]) != lanPacketMagic {
		return LANMessage{}, &Error{Code: ErrInvalidResponse, Op: "parse lan packet", Msg: "invalid packet magic"}
	}
	packetLen := int(binary.BigEndian.Uint16(packet[2:4]))
	if packetLen > len(packet) || packetLen < lanHeaderLength {
		return LANMessage{}, &Error{Code: ErrInvalidResponse, Op: "parse lan packet", Msg: "invalid packet length"}
	}

	md5Original := append([]byte(nil), packet[16:32]...)
	checkBuffer := append([]byte(nil), packet[:packetLen]...)
	copy(checkBuffer[16:32], d.token)
	checksum := md5.Sum(checkBuffer)
	if !equalBytes(md5Original, checksum[:]) {
		return LANMessage{}, &Error{Code: ErrInvalidResponse, Op: "parse lan packet", Msg: "invalid packet checksum"}
	}

	encrypted := append([]byte(nil), packet[32:packetLen]...)
	if len(encrypted)%aes.BlockSize != 0 {
		return LANMessage{}, &Error{Code: ErrInvalidResponse, Op: "parse lan packet", Msg: "encrypted payload is not block aligned"}
	}
	decrypted := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(d.block, d.iv).CryptBlocks(decrypted, encrypted)
	clear, err := pkcs7Unpad(decrypted, aes.BlockSize)
	if err != nil {
		return LANMessage{}, err
	}
	clear = trimMIPSField(clear)

	var msg LANMessage
	if err := json.Unmarshal(clear, &msg); err != nil {
		return LANMessage{}, Wrap(ErrInvalidResponse, "decode lan packet", err)
	}
	return msg, nil
}

func (d *LANDevice) buildPacketMessage(msg LANMessage) ([]byte, error) {
	payload, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	clear := pkcs7Pad(payload, aes.BlockSize)
	encrypted := make([]byte, len(clear))
	cipher.NewCBCEncrypter(d.block, d.iv).CryptBlocks(encrypted, clear)

	did, err := strconv.ParseUint(d.did, 10, 64)
	if err != nil {
		return nil, Wrap(ErrInvalidArgument, "build lan packet", err)
	}
	packetLen := lanHeaderLength + len(encrypted)
	packet := make([]byte, packetLen)
	binary.BigEndian.PutUint16(packet[0:2], lanPacketMagic)
	binary.BigEndian.PutUint16(packet[2:4], uint16(packetLen))
	binary.BigEndian.PutUint64(packet[4:12], did)
	binary.BigEndian.PutUint32(packet[12:16], d.offset)
	copy(packet[16:32], d.token)
	copy(packet[32:], encrypted)
	checksum := md5.Sum(packet)
	copy(packet[16:32], checksum[:])
	return packet, nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padLen := blockSize - (len(data) % blockSize)
	if padLen == 0 {
		padLen = blockSize
	}
	pad := make([]byte, padLen)
	for i := range pad {
		pad[i] = byte(padLen)
	}
	return append(append([]byte(nil), data...), pad...)
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, &Error{Code: ErrInvalidResponse, Op: "unpad lan packet", Msg: "invalid padded payload length"}
	}
	padLen := int(data[len(data)-1])
	if padLen == 0 || padLen > blockSize || padLen > len(data) {
		return nil, &Error{Code: ErrInvalidResponse, Op: "unpad lan packet", Msg: "invalid padding"}
	}
	for _, b := range data[len(data)-padLen:] {
		if int(b) != padLen {
			return nil, &Error{Code: ErrInvalidResponse, Op: "unpad lan packet", Msg: "invalid padding"}
		}
	}
	return append([]byte(nil), data[:len(data)-padLen]...), nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
