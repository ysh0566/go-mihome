package camera

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

const (
	miHomeMissCodecH264 = 4
	miHomeMissCodecH265 = 5
)

const (
	miHomeMissCmdAuthReq     = 0x100
	miHomeMissCmdVideoStart  = 0x102
	miHomeMissCmdVideoStop   = 0x103
	miHomeMissCmdEncoded     = 0x1001
	miHomeMissSupportEncrypt = 15
)

type miHomeMissPacket struct {
	CodecID   uint32
	Sequence  uint32
	Flags     uint32
	Timestamp uint64
	Payload   []byte
}

type miHomeCameraMediaClient interface {
	StartMedia(string, string, string) error
	StopMedia() error
	ReadPacket() (*miHomeMissPacket, error)
	SetDeadline(time.Time) error
	Close() error
}

type miHomeCameraCloud interface {
	GetDevice(context.Context, string) (miot.DeviceInfo, error)
	GetCameraVendor(context.Context, string, []byte, string) (miot.CameraVendorInfo, error)
	GetCameraVendorSecurity(context.Context, string) (miot.CameraVendorSecurity, error)
	WakeupCamera(context.Context, string) error
}

type miHomeMissClient struct {
	conn        *miHomeCS2Conn
	key         []byte
	encryptType int
	model       string
}

var miHomeCS2ConnOpener = newMiHomeCS2Conn

func newMiHomeMissClient(host string, vendor miot.CameraVendorInfo, model string, callerUUID string, clientPublic []byte, clientPrivate []byte) (*miHomeMissClient, error) {
	if strings.TrimSpace(vendor.SupportVendor) != "cs2" && vendor.VendorID != 4 {
		return nil, fmt.Errorf("xiaomi native vendor %q is unsupported", vendor.SupportVendor)
	}
	if len(clientPublic) == 0 || len(clientPrivate) == 0 {
		return nil, fmt.Errorf("xiaomi native client keypair is required")
	}

	sharedKey, err := miHomeCameraSharedKey(vendor.PublicKey, hex.EncodeToString(clientPrivate))
	if err != nil {
		return nil, err
	}
	conn, err := miHomeMissOpenConn(strings.TrimSpace(host))
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	encryptType, err := miHomeMissAuthorize(conn, hex.EncodeToString(clientPublic), vendor.Sign, callerUUID)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &miHomeMissClient{
		conn:        conn,
		key:         sharedKey,
		encryptType: encryptType,
		model:       strings.TrimSpace(model),
	}, nil
}

func miHomeMissOpenConn(host string) (*miHomeCS2Conn, error) {
	if miHomeCS2ConnOpener == nil {
		return nil, fmt.Errorf("xiaomi native cs2 opener is unavailable")
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("xiaomi native host is empty")
	}

	conn, err := miHomeCS2ConnOpener(host, "tcp")
	if err == nil {
		return conn, nil
	}
	return miHomeCS2ConnOpener(host, "")
}

func miHomeMissAuthorize(conn *miHomeCS2Conn, clientPublicHex string, sign string, callerUUID string) (int, error) {
	payload := miHomeMissAuthorizePayload(clientPublicHex, sign, callerUUID)
	if err := conn.WriteCommand(miHomeMissCmdAuthReq, []byte(payload)); err != nil {
		return 0, err
	}
	_, response, err := conn.ReadCommand()
	if err != nil {
		return 0, err
	}

	var auth struct {
		Result      string `json:"result"`
		EncryptType int    `json:"encrypt_type"`
	}
	if err := json.Unmarshal(response, &auth); err != nil {
		return 0, fmt.Errorf("xiaomi native auth decode failed: %w", err)
	}
	if strings.TrimSpace(auth.Result) != "success" {
		return 0, fmt.Errorf("xiaomi native auth failed: %s", strings.TrimSpace(string(response)))
	}
	return auth.EncryptType, nil
}

func miHomeMissAuthorizePayload(clientPublicHex string, sign string, callerUUID string) string {
	return fmt.Sprintf(`{"public_key":"%s","sign":"%s","uuid":"%s","support_encrypt":%d}`,
		strings.TrimSpace(clientPublicHex),
		strings.TrimSpace(sign),
		strings.TrimSpace(callerUUID),
		miHomeMissSupportEncrypt,
	)
}

func (c *miHomeMissClient) StartMedia(channel string, quality string, audio string) error {
	if c == nil || c.conn == nil {
		return errors.New("xiaomi native media client unavailable")
	}
	switch quality {
	case "", "hd":
		switch c.model {
		case "chuangmi.camera.046c04", "chuangmi.camera.72ac1":
			quality = "3"
		default:
			quality = "2"
		}
	case "sd":
		quality = "1"
	case "auto":
		quality = "0"
	}
	if audio == "" {
		audio = "0"
	}

	request := binary.BigEndian.AppendUint32(nil, miHomeMissCmdVideoStart)
	switch channel {
	case "", "0":
		request = fmt.Appendf(request, `{"videoquality":%s,"enableaudio":%s}`, quality, audio)
	default:
		request = fmt.Appendf(request, `{"videoquality":-1,"videoquality2":%s,"enableaudio":%s}`, quality, audio)
	}
	return c.writeCommand(request)
}

func (c *miHomeMissClient) StopMedia() error {
	if c == nil || c.conn == nil {
		return nil
	}
	request := binary.BigEndian.AppendUint32(nil, miHomeMissCmdVideoStop)
	return c.writeCommand(request)
}

func (c *miHomeMissClient) writeCommand(payload []byte) error {
	encoded, err := miHomeMissEncodePayload(payload, c.key, c.encryptType)
	if err != nil {
		return err
	}
	return c.conn.WriteCommand(miHomeMissCmdEncoded, encoded)
}

func (c *miHomeMissClient) ReadPacket() (*miHomeMissPacket, error) {
	if c == nil || c.conn == nil {
		return nil, errors.New("xiaomi native media client unavailable")
	}
	header, payload, err := c.conn.ReadPacket()
	if err != nil {
		return nil, fmt.Errorf("xiaomi native read media: %w", err)
	}
	payload, err = miHomeMissDecodePayload(payload, c.key, c.encryptType)
	if err != nil {
		return nil, err
	}
	if len(header) < 32 {
		return nil, fmt.Errorf("xiaomi native packet header too small")
	}

	packet := &miHomeMissPacket{
		CodecID:  binary.LittleEndian.Uint32(header[4:]),
		Sequence: binary.LittleEndian.Uint32(header[8:]),
		Flags:    binary.LittleEndian.Uint32(header[12:]),
		Payload:  payload,
	}
	switch c.model {
	case "isa.camera.df3", "isa.camera.isc5c1", "loock.cateye.v02":
		packet.Timestamp = uint64(time.Now().UnixMilli())
	default:
		packet.Timestamp = binary.LittleEndian.Uint64(header[16:])
	}
	return packet, nil
}

func (c *miHomeMissClient) SetDeadline(deadline time.Time) error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.SetDeadline(deadline)
}

func (c *miHomeMissClient) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func miHomeMissEncodePayload(payload []byte, key []byte, encryptType int) ([]byte, error) {
	if encryptType == 0 {
		return miHomeCameraEncode(payload, key)
	}

	aesKey, err := miHomeMissAESKey(key)
	if err != nil {
		return nil, err
	}
	paddedLen := miHomeMissPaddedLength(len(payload))
	paddingLen := paddedLen - len(payload)
	result := make([]byte, 20+paddedLen)
	binary.LittleEndian.PutUint32(result[:4], uint32(paddingLen))
	iv := result[4:20]
	if _, err := rand.Read(iv); err != nil {
		return nil, err
	}
	copy(result[20:], payload)
	for i := len(payload); i < paddedLen; i++ {
		result[20+i] = byte(paddingLen)
	}
	if err := miHomeMissAESCrypt(result[20:], aesKey, iv, encryptType, true); err != nil {
		return nil, err
	}
	return result, nil
}

func miHomeMissDecodePayload(payload []byte, key []byte, encryptType int) ([]byte, error) {
	if encryptType == 0 {
		return miHomeCameraDecode(payload, key)
	}
	if len(payload) < 20 {
		return nil, fmt.Errorf("xiaomi native encrypted payload too small")
	}

	aesKey, err := miHomeMissAESKey(key)
	if err != nil {
		return nil, err
	}
	paddingLen := int(binary.LittleEndian.Uint32(payload[:4]))
	iv := append([]byte(nil), payload[4:20]...)
	plain := append([]byte(nil), payload[20:]...)
	if err := miHomeMissAESCrypt(plain, aesKey, iv, encryptType, false); err != nil {
		return nil, err
	}
	if paddingLen < 0 || paddingLen > len(plain) {
		return nil, fmt.Errorf("xiaomi native encrypted payload padding %d exceeds payload length %d", paddingLen, len(plain))
	}
	return plain[:len(plain)-paddingLen], nil
}

func miHomeMissPaddedLength(length int) int {
	return ((length + 15) &^ 15) + 16
}

func miHomeMissAESKey(key []byte) ([]byte, error) {
	if len(key) < 16 {
		return nil, fmt.Errorf("xiaomi native shared key length %d is too short for AES", len(key))
	}
	return append([]byte(nil), key[:16]...), nil
}

func miHomeMissAESCrypt(payload []byte, key []byte, iv []byte, encryptType int, encrypt bool) error {
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	switch encryptType {
	case 1:
		if len(payload)%aes.BlockSize != 0 {
			return fmt.Errorf("xiaomi native AES-CBC payload length %d is not block aligned", len(payload))
		}
		if encrypt {
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(payload, payload)
		} else {
			cipher.NewCBCDecrypter(block, iv).CryptBlocks(payload, payload)
		}
		return nil
	case 2:
		if len(payload)%aes.BlockSize != 0 {
			return fmt.Errorf("xiaomi native AES-ECB payload length %d is not block aligned", len(payload))
		}
		for offset := 0; offset < len(payload); offset += aes.BlockSize {
			blockPayload := payload[offset : offset+aes.BlockSize]
			if encrypt {
				block.Encrypt(blockPayload, blockPayload)
			} else {
				block.Decrypt(blockPayload, blockPayload)
			}
		}
		return nil
	case 3:
		cipher.NewCTR(block, iv).XORKeyStream(payload, payload)
		return nil
	default:
		return fmt.Errorf("xiaomi native unsupported encrypt type %d", encryptType)
	}
}
