package camera

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

const (
	micoAPICameraBizID              = "micoapi"
	micoAPICameraHostCN             = "mico.api.mijia.tech"
	micoAPICameraUserAgent          = "mico/docker"
	micoAPICameraVendorPath         = "/v2/device/miss_get_vendor"
	micoAPICameraVendorPathCompat   = "/app/v2/device/miss_get_vendor"
	micoAPICameraVendorSecurityPath = "/app/v2/device/miss_get_vendor_security"
)

const micoAPICameraPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAzH220YGgZOlXJ4eSleFb
Beylq4qHsVNzhPTUTy/caDb4a3GzqH6SX4GiYRilZZZrjjU2ckkr8GM66muaIuJw
r8ZB9SSY3Hqwo32tPowpyxobTN1brmqGK146X6JcFWK/QiUYVXZlcHZuMgXLlWyn
zTMVl2fq7wPbzZwOYFxnSRh8YEnXz6edHAqJqLEqZMP00bNFBGP+yc9xmc7ySSyw
OgW/muVzfD09P2iWhl3x8N+fBBWpuI5HjvyQuiX8CZg3xpEeCV8weaprxMxR0epM
3l7T6rJuPXR1D7yhHaEQj2+dyrZTeJO8D8SnOgzV5j4bp1dTunlzBXGYVjqDsRhZ
qQIDAQAB
-----END PUBLIC KEY-----`

type MicoAPITokenCameraCloudClientOptions struct {
	CloudConfig miot.CloudConfig
	Tokens      miot.TokenProvider
	HTTPClient  miot.HTTPDoer
}

type MicoAPITokenCameraCloudClient struct {
	deviceCloud *miot.CloudClient
	httpClient  miot.HTTPDoer
	tokens      miot.TokenProvider
	clientID    string
	cloudServer string
	newSecret   func() ([]byte, error)
}

func NewMicoAPITokenCameraCloudClient(options MicoAPITokenCameraCloudClientOptions) (*MicoAPITokenCameraCloudClient, error) {
	clientID := strings.TrimSpace(options.CloudConfig.ClientID)
	if clientID == "" {
		return nil, &miot.Error{Code: miot.ErrInvalidArgument, Op: "new micoapi camera cloud client", Msg: "client id is empty"}
	}
	if options.Tokens == nil {
		return nil, &miot.Error{Code: miot.ErrInvalidArgument, Op: "new micoapi camera cloud client", Msg: "token provider is nil"}
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	deviceCloud, err := miot.NewCloudClient(options.CloudConfig, miot.WithCloudTokenProvider(options.Tokens), miot.WithCloudHTTPClient(httpClient))
	if err != nil {
		return nil, err
	}
	return &MicoAPITokenCameraCloudClient{
		deviceCloud: deviceCloud,
		httpClient:  httpClient,
		tokens:      options.Tokens,
		clientID:    clientID,
		cloudServer: strings.TrimSpace(options.CloudConfig.CloudServer),
		newSecret: func() ([]byte, error) {
			secret := make([]byte, 16)
			if _, err := rand.Read(secret); err != nil {
				return nil, err
			}
			return secret, nil
		},
	}, nil
}

func (c *MicoAPITokenCameraCloudClient) GetDevice(ctx context.Context, did string) (miot.DeviceInfo, error) {
	if c == nil || c.deviceCloud == nil {
		return miot.DeviceInfo{}, fmt.Errorf("micoapi camera cloud unavailable")
	}
	return c.deviceCloud.GetDevice(ctx, did)
}

func (c *MicoAPITokenCameraCloudClient) GetCameraVendor(ctx context.Context, did string, appPublicKey []byte, supportVendors string, callerUUID string) (miot.CameraVendorInfo, error) {
	if c == nil {
		return miot.CameraVendorInfo{}, fmt.Errorf("micoapi camera cloud unavailable")
	}
	did = strings.TrimSpace(did)
	if did == "" {
		return miot.CameraVendorInfo{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "get micoapi camera vendor", Msg: "did is empty"}
	}
	if len(appPublicKey) == 0 {
		return miot.CameraVendorInfo{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "get micoapi camera vendor", Msg: "app public key is empty"}
	}
	request := map[string]any{
		"app_pubkey":      hex.EncodeToString(appPublicKey),
		"did":             did,
		"support_vendors": firstNonEmpty(supportVendors, miHomeSupportVendors),
		"caller_uuid":     strings.TrimSpace(callerUUID),
	}

	var (
		response json.RawMessage
		lastErr  error
	)
	requestPaths := []string{micoAPICameraVendorPath, micoAPICameraVendorPathCompat}
	for _, requestPath := range requestPaths {
		response = nil
		lastErr = c.postEncryptedJSON(ctx, requestPath, request, &response)
		if lastErr == nil {
			break
		}
		if !isMicoAPINotFound(lastErr) {
			return miot.CameraVendorInfo{}, lastErr
		}
	}
	if lastErr != nil {
		if isMicoAPINotFound(lastErr) {
			return miot.CameraVendorInfo{}, &miot.Error{
				Code: miot.ErrTransportFailure,
				Op:   "get micoapi camera vendor",
				Err:  lastErr,
				Msg:  "status 404 after trying " + strings.Join(requestPaths, ", "),
			}
		}
		return miot.CameraVendorInfo{}, lastErr
	}
	info, err := parseMiHomeCameraVendorInfo(response)
	if err != nil {
		return miot.CameraVendorInfo{}, err
	}
	if info.VendorID == 0 && strings.TrimSpace(info.SupportVendor) == "" {
		return miot.CameraVendorInfo{}, &miot.Error{
			Code: miot.ErrInvalidResponse,
			Op:   "get micoapi camera vendor",
			Msg:  "camera vendor bootstrap response did not include a supported vendor",
		}
	}
	return info, nil
}

func (c *MicoAPITokenCameraCloudClient) GetCameraVendorSecurity(ctx context.Context, did string) (miot.CameraVendorSecurity, error) {
	if c == nil {
		return miot.CameraVendorSecurity{}, fmt.Errorf("micoapi camera cloud unavailable")
	}
	did = strings.TrimSpace(did)
	if did == "" {
		return miot.CameraVendorSecurity{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "get micoapi camera vendor security", Msg: "did is empty"}
	}

	var response miot.CameraVendorSecurity
	if err := c.postEncryptedJSON(ctx, micoAPICameraVendorSecurityPath, map[string]any{
		"did": did,
	}, &response); err != nil {
		return miot.CameraVendorSecurity{}, err
	}
	return response, nil
}

func (c *MicoAPITokenCameraCloudClient) GetCameraVendorBootstrap(ctx context.Context, did string, appPublicKey []byte, appPrivateKey []byte, supportVendors string, callerUUID string) (miot.CameraVendorInfo, error) {
	if c == nil {
		return miot.CameraVendorInfo{}, fmt.Errorf("micoapi camera cloud unavailable")
	}
	did = strings.TrimSpace(did)
	if did == "" {
		return miot.CameraVendorInfo{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "get micoapi camera vendor bootstrap", Msg: "did is empty"}
	}
	if len(appPublicKey) == 0 {
		return miot.CameraVendorInfo{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "get micoapi camera vendor bootstrap", Msg: "app public key is empty"}
	}
	if len(appPrivateKey) == 0 {
		return miot.CameraVendorInfo{}, &miot.Error{Code: miot.ErrInvalidArgument, Op: "get micoapi camera vendor bootstrap", Msg: "app private key is empty"}
	}

	var response json.RawMessage
	if err := c.postEncryptedJSON(ctx, micoAPICameraVendorSecurityPath, map[string]any{
		"app_pubkey":      hex.EncodeToString(appPublicKey),
		"did":             did,
		"support_vendors": firstNonEmpty(supportVendors, miHomeSupportVendors),
		"caller_uuid":     strings.TrimSpace(callerUUID),
	}, &response); err != nil {
		return miot.CameraVendorInfo{}, err
	}
	return micoAPICameraParseVendorBootstrap(response, appPrivateKey)
}

func isMicoAPINotFound(err error) bool {
	var miotErr *miot.Error
	return errors.As(err, &miotErr) && miotErr.Code == miot.ErrTransportFailure && strings.Contains(miotErr.Msg, "status 404")
}

func (c *MicoAPITokenCameraCloudClient) postEncryptedJSON(ctx context.Context, path string, payload any, dest any) error {
	if c == nil {
		return &miot.Error{Code: miot.ErrInvalidArgument, Op: "micoapi camera request", Msg: "client is nil"}
	}
	if c.httpClient == nil {
		return &miot.Error{Code: miot.ErrInvalidArgument, Op: "micoapi camera request", Msg: "http client is nil"}
	}
	if c.tokens == nil {
		return &miot.Error{Code: miot.ErrInvalidArgument, Op: "micoapi camera request", Msg: "token provider is nil"}
	}
	secret, err := c.newSecret()
	if err != nil {
		return miot.Wrap(miot.ErrTransportFailure, "generate micoapi camera secret", err)
	}
	if len(secret) != 16 {
		return &miot.Error{Code: miot.ErrInvalidResponse, Op: "generate micoapi camera secret", Msg: "secret length must be 16 bytes"}
	}
	token, err := c.tokens.AccessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return miot.Wrap(miot.ErrInvalidArgument, "marshal micoapi camera request", err)
	}
	encryptedBody, err := micoAPICameraEncrypt(secret, body)
	if err != nil {
		return err
	}
	clientSecret, err := micoAPICameraClientSecret(secret)
	if err != nil {
		return err
	}

	url := "https://" + c.host() + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(encryptedBody))
	if err != nil {
		return err
	}
	req.Host = c.host()
	req.Header.Set("Authorization", "Bearer"+token)
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("User-Agent", micoAPICameraUserAgent)
	req.Header.Set("X-Client-BizId", micoAPICameraBizID)
	req.Header.Set("X-Encrypt-Type", "1")
	req.Header.Set("X-Client-AppId", c.clientID)
	req.Header.Set("X-Client-Secret", clientSecret)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return miot.Wrap(miot.ErrTransportFailure, "micoapi camera request", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return &miot.Error{Code: miot.ErrInvalidAccessToken, Op: "micoapi camera request", Msg: "unauthorized"}
	}
	if resp.StatusCode != http.StatusOK {
		return &miot.Error{Code: miot.ErrTransportFailure, Op: "micoapi camera request", Msg: fmt.Sprintf("status %d", resp.StatusCode)}
	}

	encryptedResponse, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	plainResponse, err := micoAPICameraDecrypt(secret, string(encryptedResponse))
	if err != nil {
		return err
	}

	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message,omitempty"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(plainResponse, &envelope); err != nil {
		return miot.Wrap(miot.ErrDecodeResponse, "decode micoapi camera response", err)
	}
	if envelope.Code != 0 {
		return fmt.Errorf("micoapi camera request failed: code %d: %s", envelope.Code, strings.TrimSpace(envelope.Message))
	}
	if dest == nil {
		return nil
	}
	switch typed := dest.(type) {
	case *json.RawMessage:
		*typed = append((*typed)[:0], envelope.Result...)
		return nil
	default:
		return json.Unmarshal(envelope.Result, dest)
	}
}

func (c *MicoAPITokenCameraCloudClient) host() string {
	server := strings.ToLower(strings.TrimSpace(c.cloudServer))
	if server == "" || server == "cn" {
		return micoAPICameraHostCN
	}
	return server + "." + micoAPICameraHostCN
}

func micoAPICameraClientSecret(secret []byte) (string, error) {
	block, _ := pem.Decode([]byte(micoAPICameraPublicKeyPEM))
	if block == nil {
		return "", &miot.Error{Code: miot.ErrInvalidResponse, Op: "build micoapi camera client secret", Msg: "invalid Xiaomi camera public key"}
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", miot.Wrap(miot.ErrInvalidResponse, "parse micoapi camera public key", err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return "", &miot.Error{Code: miot.ErrInvalidResponse, Op: "parse micoapi camera public key", Msg: "unexpected public key type"}
	}
	ciphertext, err := rsa.EncryptPKCS1v15(rand.Reader, publicKey, secret)
	if err != nil {
		return "", miot.Wrap(miot.ErrTransportFailure, "encrypt micoapi camera client secret", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func micoAPICameraEncrypt(secret []byte, plain []byte) (string, error) {
	block, err := aes.NewCipher(secret)
	if err != nil {
		return "", miot.Wrap(miot.ErrInvalidArgument, "build micoapi aes cipher", err)
	}
	plain = micoAPICameraPKCS7Pad(plain, aes.BlockSize)
	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, secret).CryptBlocks(ciphertext, plain)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func micoAPICameraDecrypt(secret []byte, encoded string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, miot.Wrap(miot.ErrDecodeResponse, "decode micoapi camera ciphertext", err)
	}
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera ciphertext", Msg: "invalid ciphertext length"}
	}
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, miot.Wrap(miot.ErrInvalidArgument, "build micoapi aes cipher", err)
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, secret).CryptBlocks(plain, ciphertext)
	plain, err = micoAPICameraPKCS7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func micoAPICameraPKCS7Pad(plain []byte, blockSize int) []byte {
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

func micoAPICameraPKCS7Unpad(plain []byte, blockSize int) ([]byte, error) {
	if len(plain) == 0 || len(plain)%blockSize != 0 {
		return nil, &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera plaintext", Msg: "invalid plaintext length"}
	}
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > blockSize || padding > len(plain) {
		return nil, &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera plaintext", Msg: "invalid padding size"}
	}
	for _, value := range plain[len(plain)-padding:] {
		if int(value) != padding {
			return nil, &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera plaintext", Msg: "invalid padding byte"}
		}
	}
	return plain[:len(plain)-padding], nil
}

func micoAPICameraParseVendorBootstrap(payload json.RawMessage, clientPrivateKey []byte) (miot.CameraVendorInfo, error) {
	plainPayload, err := micoAPICameraDecodeVendorBootstrapPayload(payload, clientPrivateKey)
	if err != nil {
		return miot.CameraVendorInfo{}, err
	}
	info, err := parseFlexibleMiHomeCameraVendorInfo(plainPayload)
	if err != nil {
		return miot.CameraVendorInfo{}, err
	}
	if info.VendorID == 0 && strings.TrimSpace(info.SupportVendor) == "" {
		return miot.CameraVendorInfo{}, &miot.Error{
			Code: miot.ErrInvalidResponse,
			Op:   "parse micoapi camera vendor bootstrap",
			Msg:  "camera vendor bootstrap response did not include a supported vendor",
		}
	}
	return info, nil
}

func micoAPICameraDecodeVendorBootstrapPayload(payload json.RawMessage, clientPrivateKey []byte) ([]byte, error) {
	var envelope struct {
		CryptParams json.RawMessage `json:"crypt_params"`
		CryptResult string          `json:"crypt_result"`
		ServerKey   string          `json:"svr_pubkey"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return append([]byte(nil), payload...), nil
	}
	if envelope.CryptResult == "" && len(envelope.CryptParams) != 0 {
		var nested struct {
			CryptResult string `json:"crypt_result"`
			ServerKey   string `json:"svr_pubkey"`
		}
		if err := json.Unmarshal(envelope.CryptParams, &nested); err == nil {
			envelope.CryptResult = firstNonEmpty(envelope.CryptResult, nested.CryptResult)
			envelope.ServerKey = firstNonEmpty(envelope.ServerKey, nested.ServerKey)
		}
	}
	if strings.TrimSpace(envelope.CryptResult) == "" || strings.TrimSpace(envelope.ServerKey) == "" {
		return append([]byte(nil), payload...), nil
	}
	return micoAPICameraDecryptVendorBootstrap(envelope.ServerKey, clientPrivateKey, envelope.CryptResult)
}

func micoAPICameraDecryptVendorBootstrap(serverPublicKey string, clientPrivateKey []byte, encoded string) ([]byte, error) {
	sharedKey, err := miHomeCameraSharedKey(strings.TrimSpace(serverPublicKey), hex.EncodeToString(clientPrivateKey))
	if err != nil {
		return nil, miot.Wrap(miot.ErrDecodeResponse, "decode micoapi camera vendor bootstrap shared key", err)
	}
	if len(sharedKey) < 16 {
		return nil, &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera vendor bootstrap shared key", Msg: "shared key is too short"}
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, miot.Wrap(miot.ErrDecodeResponse, "decode micoapi camera vendor bootstrap ciphertext", err)
	}
	if len(ciphertext) <= aes.BlockSize || (len(ciphertext)-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera vendor bootstrap ciphertext", Msg: "invalid ciphertext length"}
	}

	iv := ciphertext[:aes.BlockSize]
	ciphertext = ciphertext[aes.BlockSize:]

	keyCandidates := [][]byte{append([]byte(nil), sharedKey[:16]...)}
	if len(sharedKey) != 16 {
		keyCandidates = append(keyCandidates, append([]byte(nil), sharedKey...))
	}

	var lastErr error
	for _, key := range keyCandidates {
		block, buildErr := aes.NewCipher(key)
		if buildErr != nil {
			lastErr = miot.Wrap(miot.ErrInvalidArgument, "build micoapi camera vendor bootstrap cipher", buildErr)
			continue
		}
		plain := make([]byte, len(ciphertext))
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ciphertext)

		unpadded, unpadErr := micoAPICameraPKCS7Unpad(plain, aes.BlockSize)
		if unpadErr == nil && json.Valid(unpadded) {
			return unpadded, nil
		}
		trimmed := bytes.TrimRight(plain, "\x00")
		if len(trimmed) != 0 && json.Valid(trimmed) {
			return trimmed, nil
		}
		if unpadErr != nil {
			lastErr = unpadErr
			continue
		}
		lastErr = &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera vendor bootstrap plaintext", Msg: "decrypted payload is not valid JSON"}
	}
	if lastErr == nil {
		lastErr = &miot.Error{Code: miot.ErrDecodeResponse, Op: "decode micoapi camera vendor bootstrap plaintext", Msg: "unable to decode camera vendor bootstrap"}
	}
	return nil, lastErr
}

func parseFlexibleMiHomeCameraVendorInfo(payload json.RawMessage) (miot.CameraVendorInfo, error) {
	candidates := []json.RawMessage{append([]byte(nil), payload...)}

	var wrapped map[string]json.RawMessage
	if err := json.Unmarshal(payload, &wrapped); err == nil {
		if next := bytes.TrimSpace(wrapped["result"]); len(next) != 0 {
			candidates = append(candidates, append([]byte(nil), next...))
		}
		if next := bytes.TrimSpace(wrapped["params"]); len(next) != 0 {
			candidates = append(candidates, append([]byte(nil), next...))
		}
	}

	var lastErr error
	for _, candidate := range candidates {
		info, err := parseFlexibleMiHomeCameraVendorCandidate(candidate)
		if err != nil {
			lastErr = err
			continue
		}
		if info.VendorID != 0 ||
			strings.TrimSpace(info.SupportVendor) != "" ||
			strings.TrimSpace(info.PublicKey) != "" ||
			strings.TrimSpace(info.Sign) != "" {
			return info, nil
		}
	}
	if lastErr != nil {
		return miot.CameraVendorInfo{}, lastErr
	}
	return miot.CameraVendorInfo{}, &miot.Error{Code: miot.ErrInvalidResponse, Op: "parse micoapi camera vendor bootstrap", Msg: "empty camera vendor payload"}
}

func parseFlexibleMiHomeCameraVendorCandidate(payload json.RawMessage) (miot.CameraVendorInfo, error) {
	if len(payload) == 0 {
		return miot.CameraVendorInfo{}, &miot.Error{Code: miot.ErrInvalidResponse, Op: "parse micoapi camera vendor bootstrap", Msg: "empty camera vendor payload"}
	}

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &rawFields); err != nil {
		return miot.CameraVendorInfo{}, miot.Wrap(miot.ErrInvalidResponse, "parse micoapi camera vendor bootstrap", err)
	}

	var raw struct {
		PublicKey     string `json:"public_key"`
		Sign          string `json:"sign"`
		P2PID         string `json:"p2p_id"`
		InitString    string `json:"init_string"`
		SupportVendor string `json:"support_vendor"`
		VendorName    string `json:"vendor_name"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return miot.CameraVendorInfo{}, miot.Wrap(miot.ErrInvalidResponse, "parse micoapi camera vendor bootstrap", err)
	}

	info := miot.CameraVendorInfo{
		PublicKey:     raw.PublicKey,
		Sign:          raw.Sign,
		P2PID:         raw.P2PID,
		InitString:    raw.InitString,
		SupportVendor: firstNonEmpty(raw.SupportVendor, raw.VendorName),
	}

	vendorID := 0
	vendorParams := bytes.TrimSpace(rawFields["vendor_params"])
	vendorField := bytes.TrimSpace(rawFields["vendor"])
	if len(vendorField) != 0 {
		switch vendorField[0] {
		case '{':
			var rawVendor map[string]any
			if err := json.Unmarshal(vendorField, &rawVendor); err == nil {
				info.RawVendor = rawVendor
			}

			var vendor struct {
				Vendor       int             `json:"vendor"`
				VendorParams json.RawMessage `json:"vendor_params"`
			}
			if err := json.Unmarshal(vendorField, &vendor); err == nil {
				vendorID = vendor.Vendor
				if len(vendor.VendorParams) != 0 {
					vendorParams = vendor.VendorParams
				}
			}
		default:
			_ = json.Unmarshal(vendorField, &vendorID)
		}
	}

	info.VendorID = vendorID
	if info.SupportVendor == "" && info.VendorID != 0 {
		info.SupportVendor = miHomeCameraVendorName(info.VendorID)
	}
	if len(vendorParams) == 0 {
		return info, nil
	}

	var rawParams map[string]any
	if err := json.Unmarshal(vendorParams, &rawParams); err == nil {
		info.RawVendorParam = rawParams
	}
	var params struct {
		P2PID      string `json:"p2p_id"`
		InitString string `json:"init_string"`
	}
	if err := json.Unmarshal(vendorParams, &params); err == nil {
		info.P2PID = firstNonEmpty(info.P2PID, params.P2PID)
		info.InitString = firstNonEmpty(info.InitString, params.InitString)
	}
	return info, nil
}
