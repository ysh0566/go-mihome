package camera

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	miot "github.com/ysh0566/go-mihome"
)

func TestMicoAPITokenCameraCloudClientGetCameraVendorUsesEncryptedMicoAPIRequest(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x3c}, 16)
	var seenPath string
	var seenHost string
	var seenHeaders http.Header
	var seenBody string

	client, err := NewMicoAPITokenCameraCloudClient(MicoAPITokenCameraCloudClientOptions{
		CloudConfig: miot.CloudConfig{
			ClientID:    "2882303761520431603",
			CloudServer: "cn",
		},
		Tokens: staticMicoTokenProvider{token: "access-token"},
		HTTPClient: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			seenPath = req.URL.String()
			seenHost = req.Host
			seenHeaders = req.Header.Clone()
			body, readErr := io.ReadAll(req.Body)
			if readErr != nil {
				t.Fatalf("ReadAll(req.Body) error = %v", readErr)
			}
			seenBody = string(body)

			responsePayload, marshalErr := json.Marshal(map[string]any{
				"code": 0,
				"result": map[string]any{
					"public_key":     "vendor-public",
					"sign":           "vendor-sign",
					"support_vendor": "cs2",
					"vendor": map[string]any{
						"vendor": 4,
						"vendor_params": map[string]any{
							"p2p_id":      "p2p-id",
							"init_string": "init-string",
						},
					},
				},
			})
			if marshalErr != nil {
				t.Fatalf("Marshal(responsePayload) error = %v", marshalErr)
			}
			respBody := mustMicoEncryptBase64(t, secret, responsePayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(respBody)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewMicoAPITokenCameraCloudClient() error = %v", err)
	}
	client.newSecret = func() ([]byte, error) {
		return append([]byte(nil), secret...), nil
	}

	publicKey := bytes.Repeat([]byte{0x7a}, 32)
	vendor, err := client.GetCameraVendor(context.Background(), "camera-1", publicKey, "TUTK_CS2_MTP", "caller-1")
	if err != nil {
		t.Fatalf("GetCameraVendor() error = %v", err)
	}

	if got, want := seenPath, "https://mico.api.mijia.tech/v2/device/miss_get_vendor"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := seenHost, "mico.api.mijia.tech"; got != want {
		t.Fatalf("request Host = %q, want %q", got, want)
	}
	if got, want := seenHeaders.Get("Authorization"), "Beareraccess-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := seenHeaders.Get("X-Client-BizId"), "micoapi"; got != want {
		t.Fatalf("X-Client-BizId = %q, want %q", got, want)
	}
	if got, want := seenHeaders.Get("X-Client-AppId"), "2882303761520431603"; got != want {
		t.Fatalf("X-Client-AppId = %q, want %q", got, want)
	}
	if got, want := seenHeaders.Get("X-Encrypt-Type"), "1"; got != want {
		t.Fatalf("X-Encrypt-Type = %q, want %q", got, want)
	}
	if got := seenHeaders.Get("X-Client-Secret"); strings.TrimSpace(got) == "" {
		t.Fatal("X-Client-Secret = empty, want RSA-encrypted client secret")
	}

	plainBody := mustMicoDecryptBase64(t, secret, seenBody)
	var payload map[string]any
	if err := json.Unmarshal(plainBody, &payload); err != nil {
		t.Fatalf("Unmarshal(request payload) error = %v", err)
	}
	if got, want := payload["did"], "camera-1"; got != want {
		t.Fatalf("payload.did = %#v, want %#v", got, want)
	}
	if got, want := payload["support_vendors"], "TUTK_CS2_MTP"; got != want {
		t.Fatalf("payload.support_vendors = %#v, want %#v", got, want)
	}
	if got, want := payload["caller_uuid"], "caller-1"; got != want {
		t.Fatalf("payload.caller_uuid = %#v, want %#v", got, want)
	}
	if got, want := payload["app_pubkey"], hex.EncodeToString(publicKey); got != want {
		t.Fatalf("payload.app_pubkey = %#v, want %#v", got, want)
	}

	if got, want := vendor.PublicKey, "vendor-public"; got != want {
		t.Fatalf("vendor.PublicKey = %q, want %q", got, want)
	}
	if got, want := vendor.Sign, "vendor-sign"; got != want {
		t.Fatalf("vendor.Sign = %q, want %q", got, want)
	}
	if got, want := vendor.SupportVendor, "cs2"; got != want {
		t.Fatalf("vendor.SupportVendor = %q, want %q", got, want)
	}
	if got, want := vendor.VendorID, 4; got != want {
		t.Fatalf("vendor.VendorID = %d, want %d", got, want)
	}
	if got, want := vendor.P2PID, "p2p-id"; got != want {
		t.Fatalf("vendor.P2PID = %q, want %q", got, want)
	}
	if got, want := vendor.InitString, "init-string"; got != want {
		t.Fatalf("vendor.InitString = %q, want %q", got, want)
	}
}

func TestMicoAPITokenCameraCloudClientGetCameraVendorRetriesAppPrefixedPathOn404(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x5a}, 16)
	var seenPaths []string

	client, err := NewMicoAPITokenCameraCloudClient(MicoAPITokenCameraCloudClientOptions{
		CloudConfig: miot.CloudConfig{
			ClientID:    "2882303761520431603",
			CloudServer: "cn",
		},
		Tokens: staticMicoTokenProvider{token: "access-token"},
		HTTPClient: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			seenPaths = append(seenPaths, req.URL.String())
			if len(seenPaths) == 1 {
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader("not found")),
				}, nil
			}

			responsePayload, marshalErr := json.Marshal(map[string]any{
				"code": 0,
				"result": map[string]any{
					"public_key": "vendor-public",
					"sign":       "vendor-sign",
					"vendor": map[string]any{
						"vendor": 4,
						"vendor_params": map[string]any{
							"p2p_id":      "p2p-id",
							"init_string": "init-string",
						},
					},
				},
			})
			if marshalErr != nil {
				t.Fatalf("Marshal(responsePayload) error = %v", marshalErr)
			}
			respBody := mustMicoEncryptBase64(t, secret, responsePayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(respBody)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewMicoAPITokenCameraCloudClient() error = %v", err)
	}
	client.newSecret = func() ([]byte, error) {
		return append([]byte(nil), secret...), nil
	}

	_, err = client.GetCameraVendor(context.Background(), "camera-1", bytes.Repeat([]byte{0x7a}, 32), "TUTK_CS2_MTP", "caller-1")
	if err != nil {
		t.Fatalf("GetCameraVendor() error = %v", err)
	}

	if len(seenPaths) != 2 {
		t.Fatalf("request count = %d, want 2", len(seenPaths))
	}
	if got, want := seenPaths[0], "https://mico.api.mijia.tech/v2/device/miss_get_vendor"; got != want {
		t.Fatalf("first request URL = %q, want %q", got, want)
	}
	if got, want := seenPaths[1], "https://mico.api.mijia.tech/app/v2/device/miss_get_vendor"; got != want {
		t.Fatalf("second request URL = %q, want %q", got, want)
	}
}

func TestMicoAPITokenCameraCloudClientGetCameraVendorSecurityUsesEncryptedMicoAPIRequest(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x2a}, 16)
	var seenPath string
	var seenHeaders http.Header
	var seenBody string

	client, err := NewMicoAPITokenCameraCloudClient(MicoAPITokenCameraCloudClientOptions{
		CloudConfig: miot.CloudConfig{
			ClientID:    "2882303761520431603",
			CloudServer: "cn",
		},
		Tokens: staticMicoTokenProvider{token: "access-token"},
		HTTPClient: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			seenPath = req.URL.String()
			seenHeaders = req.Header.Clone()
			body, readErr := io.ReadAll(req.Body)
			if readErr != nil {
				t.Fatalf("ReadAll(req.Body) error = %v", readErr)
			}
			seenBody = string(body)

			responsePayload, marshalErr := json.Marshal(map[string]any{
				"code": 0,
				"result": map[string]any{
					"public_key":      "vendor-public",
					"token":           "vendor-token",
					"support_vendors": "TUTK_CS2_MTP",
					"req_key":         "req-key",
					"miss_version":    "3.5.1",
					"p2pkey_version":  1,
				},
			})
			if marshalErr != nil {
				t.Fatalf("Marshal(responsePayload) error = %v", marshalErr)
			}
			respBody := mustMicoEncryptBase64(t, secret, responsePayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(respBody)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewMicoAPITokenCameraCloudClient() error = %v", err)
	}
	client.newSecret = func() ([]byte, error) {
		return append([]byte(nil), secret...), nil
	}

	security, err := client.GetCameraVendorSecurity(context.Background(), "camera-1")
	if err != nil {
		t.Fatalf("GetCameraVendorSecurity() error = %v", err)
	}

	if got, want := seenPath, "https://mico.api.mijia.tech/app/v2/device/miss_get_vendor_security"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := seenHeaders.Get("Authorization"), "Beareraccess-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	plainBody := mustMicoDecryptBase64(t, secret, seenBody)
	var payload map[string]any
	if err := json.Unmarshal(plainBody, &payload); err != nil {
		t.Fatalf("Unmarshal(request payload) error = %v", err)
	}
	if got, want := payload["did"], "camera-1"; got != want {
		t.Fatalf("payload.did = %#v, want %#v", got, want)
	}

	if got, want := security.PublicKey, "vendor-public"; got != want {
		t.Fatalf("security.PublicKey = %q, want %q", got, want)
	}
	if got, want := security.Token, "vendor-token"; got != want {
		t.Fatalf("security.Token = %q, want %q", got, want)
	}
	if got, want := security.ReqKey, "req-key"; got != want {
		t.Fatalf("security.ReqKey = %q, want %q", got, want)
	}
	if got, want := security.MissVersion, "3.5.1"; got != want {
		t.Fatalf("security.MissVersion = %q, want %q", got, want)
	}
}

func TestMicoAPITokenCameraCloudClientGetCameraVendorBootstrapDecryptsEncryptedVendorPayload(t *testing.T) {
	t.Parallel()

	secret := bytes.Repeat([]byte{0x44}, 16)
	appPublicKey, appPrivateKey, err := miHomeCameraGenerateKey()
	if err != nil {
		t.Fatalf("miHomeCameraGenerateKey() error = %v", err)
	}
	serverPublicKey, serverPrivateKey, err := miHomeCameraGenerateKey()
	if err != nil {
		t.Fatalf("miHomeCameraGenerateKey() error = %v", err)
	}

	var seenPath string
	var seenBody string

	client, err := NewMicoAPITokenCameraCloudClient(MicoAPITokenCameraCloudClientOptions{
		CloudConfig: miot.CloudConfig{
			ClientID:    "2882303761520251711",
			CloudServer: "cn",
		},
		Tokens: staticMicoTokenProvider{token: "access-token"},
		HTTPClient: httpDoerFunc(func(req *http.Request) (*http.Response, error) {
			seenPath = req.URL.String()
			body, readErr := io.ReadAll(req.Body)
			if readErr != nil {
				t.Fatalf("ReadAll(req.Body) error = %v", readErr)
			}
			seenBody = string(body)

			plainVendorPayload, marshalErr := json.Marshal(map[string]any{
				"result": map[string]any{
					"public_key":     "vendor-public",
					"sign":           "vendor-sign",
					"support_vendor": "cs2",
					"vendor": map[string]any{
						"vendor": 4,
						"vendor_params": map[string]any{
							"p2p_id":      "p2p-id",
							"init_string": "init-string",
						},
					},
				},
			})
			if marshalErr != nil {
				t.Fatalf("Marshal(plainVendorPayload) error = %v", marshalErr)
			}
			sharedKey, sharedErr := miHomeCameraSharedKey(hex.EncodeToString(appPublicKey), hex.EncodeToString(serverPrivateKey))
			if sharedErr != nil {
				t.Fatalf("miHomeCameraSharedKey() error = %v", sharedErr)
			}

			responsePayload, marshalErr := json.Marshal(map[string]any{
				"code": 0,
				"result": map[string]any{
					"svr_pubkey":   hex.EncodeToString(serverPublicKey),
					"crypt_result": mustMicoVendorBootstrapEncryptBase64(t, sharedKey, plainVendorPayload),
				},
			})
			if marshalErr != nil {
				t.Fatalf("Marshal(responsePayload) error = %v", marshalErr)
			}
			respBody := mustMicoEncryptBase64(t, secret, responsePayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(respBody)),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("NewMicoAPITokenCameraCloudClient() error = %v", err)
	}
	client.newSecret = func() ([]byte, error) {
		return append([]byte(nil), secret...), nil
	}

	vendor, err := client.GetCameraVendorBootstrap(context.Background(), "camera-1", appPublicKey, appPrivateKey, "TUTK_CS2_MTP", "caller-1")
	if err != nil {
		t.Fatalf("GetCameraVendorBootstrap() error = %v", err)
	}

	if got, want := seenPath, "https://mico.api.mijia.tech/app/v2/device/miss_get_vendor_security"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	plainBody := mustMicoDecryptBase64(t, secret, seenBody)
	var requestPayload map[string]any
	if err := json.Unmarshal(plainBody, &requestPayload); err != nil {
		t.Fatalf("Unmarshal(request payload) error = %v", err)
	}
	if got, want := requestPayload["did"], "camera-1"; got != want {
		t.Fatalf("payload.did = %#v, want %#v", got, want)
	}
	if got, want := requestPayload["app_pubkey"], hex.EncodeToString(appPublicKey); got != want {
		t.Fatalf("payload.app_pubkey = %#v, want %#v", got, want)
	}
	if got, want := requestPayload["support_vendors"], "TUTK_CS2_MTP"; got != want {
		t.Fatalf("payload.support_vendors = %#v, want %#v", got, want)
	}
	if got, want := requestPayload["caller_uuid"], "caller-1"; got != want {
		t.Fatalf("payload.caller_uuid = %#v, want %#v", got, want)
	}

	if got, want := vendor.PublicKey, "vendor-public"; got != want {
		t.Fatalf("vendor.PublicKey = %q, want %q", got, want)
	}
	if got, want := vendor.Sign, "vendor-sign"; got != want {
		t.Fatalf("vendor.Sign = %q, want %q", got, want)
	}
	if got, want := vendor.SupportVendor, "cs2"; got != want {
		t.Fatalf("vendor.SupportVendor = %q, want %q", got, want)
	}
	if got, want := vendor.VendorID, 4; got != want {
		t.Fatalf("vendor.VendorID = %d, want %d", got, want)
	}
	if got, want := vendor.P2PID, "p2p-id"; got != want {
		t.Fatalf("vendor.P2PID = %q, want %q", got, want)
	}
	if got, want := vendor.InitString, "init-string"; got != want {
		t.Fatalf("vendor.InitString = %q, want %q", got, want)
	}
}

type staticMicoTokenProvider struct {
	token string
}

func (p staticMicoTokenProvider) AccessToken(context.Context) (string, error) {
	return p.token, nil
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (fn httpDoerFunc) Do(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func mustMicoEncryptBase64(t *testing.T, key []byte, plain []byte) string {
	t.Helper()

	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	padded := padPKCS7(plain, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key).CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func mustMicoDecryptBase64(t *testing.T, key []byte, encoded string) []byte {
	t.Helper()

	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key).CryptBlocks(plain, ciphertext)
	unpadded, err := unpadPKCS7(plain, aes.BlockSize)
	if err != nil {
		t.Fatalf("unpadPKCS7() error = %v", err)
	}
	return unpadded
}

func mustMicoVendorBootstrapEncryptBase64(t *testing.T, key []byte, plain []byte) string {
	t.Helper()

	if len(key) < 16 {
		t.Fatalf("vendor bootstrap shared key length = %d, want at least 16", len(key))
	}
	block, err := aes.NewCipher(key[:16])
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	iv := bytes.Repeat([]byte{0x29}, aes.BlockSize)
	padded := padPKCS7(plain, aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	return base64.StdEncoding.EncodeToString(append(append([]byte(nil), iv...), ciphertext...))
}

func padPKCS7(plain []byte, blockSize int) []byte {
	padding := blockSize - (len(plain) % blockSize)
	if padding == 0 {
		padding = blockSize
	}
	padded := make([]byte, len(plain)+padding)
	copy(padded, plain)
	for i := len(plain); i < len(padded); i++ {
		padded[i] = byte(padding)
	}
	return padded
}

func unpadPKCS7(plain []byte, blockSize int) ([]byte, error) {
	if len(plain) == 0 || len(plain)%blockSize != 0 {
		return nil, fmt.Errorf("invalid PKCS7 plaintext length")
	}
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > blockSize || padding > len(plain) {
		return nil, fmt.Errorf("invalid PKCS7 padding size")
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, fmt.Errorf("invalid PKCS7 padding byte")
		}
	}
	return plain[:len(plain)-padding], nil
}
