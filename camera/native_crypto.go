package camera

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/nacl/box"
)

func miHomeCameraGenerateKey() ([]byte, []byte, error) {
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	return publicKey[:], privateKey[:], nil
}

func miHomeCameraSharedKey(devicePublicHex string, clientPrivateHex string) ([]byte, error) {
	var sharedKey, publicKey, privateKey [32]byte
	if _, err := hex.Decode(publicKey[:], []byte(devicePublicHex)); err != nil {
		return nil, err
	}
	if _, err := hex.Decode(privateKey[:], []byte(clientPrivateHex)); err != nil {
		return nil, err
	}
	box.Precompute(&sharedKey, &publicKey, &privateKey)
	return sharedKey[:], nil
}

func miHomeCameraEncode(payload []byte, key []byte) ([]byte, error) {
	result := make([]byte, len(payload)+8)
	if _, err := rand.Read(result[:8]); err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	copy(nonce[4:], result[:8])
	cipher, err := chacha20.NewUnauthenticatedCipher(key, nonce)
	if err != nil {
		return nil, err
	}
	cipher.XORKeyStream(result[8:], payload)
	return result, nil
}

func miHomeCameraDecode(payload []byte, key []byte) ([]byte, error) {
	if len(payload) < 8 {
		return nil, nil
	}
	nonce := make([]byte, 12)
	copy(nonce[4:], payload[:8])
	cipher, err := chacha20.NewUnauthenticatedCipher(key, nonce)
	if err != nil {
		return nil, err
	}
	result := make([]byte, len(payload)-8)
	cipher.XORKeyStream(result, payload[8:])
	return result, nil
}
