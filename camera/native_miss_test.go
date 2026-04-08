package camera

import (
	"bytes"
	"errors"
	"testing"
)

func TestMiHomeMissAuthorizePayloadUsesEncryptedAuthMode(t *testing.T) {
	t.Parallel()

	payload := miHomeMissAuthorizePayload("client-public", "vendor-sign", "caller-1")
	want := `{"public_key":"client-public","sign":"vendor-sign","uuid":"caller-1","support_encrypt":15}`
	if payload != want {
		t.Fatalf("miHomeMissAuthorizePayload() = %q, want %q", payload, want)
	}
}

func TestMiHomeMissPayloadCodecRoundTripChacha20(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x35}, 32)
	payload := []byte(`{"videoquality":2,"enableaudio":0}`)

	encoded, err := miHomeMissEncodePayload(payload, key, 0)
	if err != nil {
		t.Fatalf("miHomeMissEncodePayload() error = %v", err)
	}
	decoded, err := miHomeMissDecodePayload(encoded, key, 0)
	if err != nil {
		t.Fatalf("miHomeMissDecodePayload() error = %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatalf("decoded payload = %q, want %q", decoded, payload)
	}
}

func TestMiHomeMissPayloadCodecRoundTripAESCBC(t *testing.T) {
	t.Parallel()

	key := bytes.Repeat([]byte{0x5c}, 32)
	payload := []byte(`{"videoquality":2,"enableaudio":0}`)

	encoded, err := miHomeMissEncodePayload(payload, key, 1)
	if err != nil {
		t.Fatalf("miHomeMissEncodePayload() error = %v", err)
	}
	if len(encoded) <= len(payload) {
		t.Fatalf("encoded length = %d, want header/padding expansion", len(encoded))
	}

	decoded, err := miHomeMissDecodePayload(encoded, key, 1)
	if err != nil {
		t.Fatalf("miHomeMissDecodePayload() error = %v", err)
	}
	if !bytes.Equal(decoded, payload) {
		t.Fatalf("decoded payload = %q, want %q", decoded, payload)
	}
}

func TestMiHomeMissOpenConnPrefersTCP(t *testing.T) {
	t.Parallel()

	original := miHomeCS2ConnOpener
	t.Cleanup(func() {
		miHomeCS2ConnOpener = original
	})

	var transports []string
	miHomeCS2ConnOpener = func(host string, transport string) (*miHomeCS2Conn, error) {
		transports = append(transports, transport)
		if transport == "tcp" {
			return nil, errors.New("tcp unavailable")
		}
		return &miHomeCS2Conn{}, nil
	}

	conn, err := miHomeMissOpenConn("192.168.31.75")
	if err != nil {
		t.Fatalf("miHomeMissOpenConn() error = %v", err)
	}
	if conn == nil {
		t.Fatal("miHomeMissOpenConn() = nil")
	}
	if got, want := transports, []string{"tcp", ""}; !equalStringSlices(got, want) {
		t.Fatalf("transports = %#v, want %#v", got, want)
	}
}

func equalStringSlices(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
