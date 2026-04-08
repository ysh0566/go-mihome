package camera

import (
	"bytes"
	"context"
	"crypto/rc4"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	miot "github.com/ysh0566/go-mihome"
)

const miHomeSupportVendors = "TUTK_CS2_MTP"
const miHomeAccountLoginURL = "https://account.xiaomi.com/pass/serviceLogin?_json=true&sid="

type miHomeCloudClient struct {
	httpClient *http.Client
	baseURL    string
	cookies    string
	ssecurity  []byte
}

func newMiHomeCloudClient(session miHomeSession, httpClient *http.Client) (*miHomeCloudClient, error) {
	session.normalize()
	if !session.completeDirect() && !session.completePassToken() {
		return nil, fmt.Errorf("mihome session is incomplete")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	client := &miHomeCloudClient{
		httpClient: httpClient,
		baseURL:    miHomeAPIBaseURL(session.Region),
	}
	if session.completePassToken() {
		if err := client.loginWithPassToken(session); err != nil {
			return nil, err
		}
		return client, nil
	}

	ssecurity, err := base64.StdEncoding.DecodeString(session.Ssecurity)
	if err != nil {
		return nil, fmt.Errorf("decode mihome ssecurity: %w", err)
	}
	client.cookies = fmt.Sprintf("userId=%s; cUserId=%s; serviceToken=%s", session.UserID, session.CUserID, session.ServiceToken)
	client.ssecurity = ssecurity
	return client, nil
}

func (c *miHomeCloudClient) GetDevice(ctx context.Context, did string) (miot.DeviceInfo, error) {
	var response struct {
		List []map[string]any `json:"list"`
	}
	if err := c.post(ctx, "/v2/home/device_list_page", map[string]any{
		"limit":            200,
		"get_split_device": true,
		"dids":             []string{strings.TrimSpace(did)},
	}, &response); err != nil {
		return miot.DeviceInfo{}, err
	}

	for _, raw := range response.List {
		device := miHomeDeviceInfo(raw)
		if device.DID == strings.TrimSpace(did) {
			return device, nil
		}
	}
	return miot.DeviceInfo{}, fmt.Errorf("xiaomi device %s not found", did)
}

func (c *miHomeCloudClient) GetCameraVendor(ctx context.Context, did string, appPublicKey []byte, callerUUID string) (miot.CameraVendorInfo, error) {
	var response json.RawMessage
	if err := c.post(ctx, "/v2/device/miss_get_vendor", map[string]any{
		"app_pubkey":      fmt.Sprintf("%x", appPublicKey),
		"did":             strings.TrimSpace(did),
		"support_vendors": miHomeSupportVendors,
		"caller_uuid":     strings.TrimSpace(callerUUID),
	}, &response); err != nil {
		return miot.CameraVendorInfo{}, err
	}
	return parseMiHomeCameraVendorInfo(response)
}

func (c *miHomeCloudClient) GetCameraVendorSecurity(ctx context.Context, did string) (miot.CameraVendorSecurity, error) {
	var response miot.CameraVendorSecurity
	if err := c.post(ctx, "/v2/device/miss_get_vendor_security", map[string]any{
		"did": strings.TrimSpace(did),
	}, &response); err != nil {
		return miot.CameraVendorSecurity{}, err
	}
	return response, nil
}

func (c *miHomeCloudClient) WakeupCamera(ctx context.Context, did string) error {
	var response json.RawMessage
	return c.post(ctx, "/home/rpc/"+strings.TrimSpace(did), map[string]any{
		"id":     1,
		"method": "wakeup",
		"params": map[string]any{"video": "1"},
	}, &response)
}

func (c *miHomeCloudClient) post(ctx context.Context, path string, payload any, dest any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	values := url.Values{"data": {string(body)}}
	nonce := miHomeNonce()
	signedNonce := miHomeSignedNonce(c.ssecurity, nonce)
	values.Set("rc4_hash__", miHomeSignature("POST", path, values, signedNonce))
	for _, value := range values {
		encrypted, err := miHomeRC4Crypt(signedNonce, []byte(value[0]))
		if err != nil {
			return err
		}
		value[0] = base64.StdEncoding.EncodeToString(encrypted)
	}
	values.Set("signature", miHomeSignature("POST", path, values, signedNonce))
	values.Set("_nonce", base64.StdEncoding.EncodeToString(nonce))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", c.cookies)

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		content, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		return fmt.Errorf("xiaomi api %s returned %d: %s", path, res.StatusCode, strings.TrimSpace(string(content)))
	}

	content, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	encrypted, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(content)))
	if err != nil {
		return err
	}
	plain, err := miHomeRC4Crypt(signedNonce, encrypted)
	if err != nil {
		return err
	}

	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(plain, &envelope); err != nil {
		return err
	}
	if envelope.Code != 0 {
		return fmt.Errorf("xiaomi api %s rejected request: %s", path, strings.TrimSpace(envelope.Message))
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

func (c *miHomeCloudClient) loginWithPassToken(session miHomeSession) error {
	req, err := http.NewRequest(http.MethodGet, miHomeAccountLoginURL+session.LoginSID, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cookie", fmt.Sprintf("userId=%s; passToken=%s", session.UserID, session.PassToken))

	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}

	var loginResponse struct {
		Ssecurity []byte `json:"ssecurity"`
		PassToken string `json:"passToken"`
		Location  string `json:"location"`
	}
	if _, err := miHomeReadLoginResponse(res.Body, &loginResponse); err != nil {
		return err
	}
	c.ssecurity = loginResponse.Ssecurity
	return c.finishAuth(loginResponse.Location, session.UserID)
}

func (c *miHomeCloudClient) finishAuth(location string, fallbackUserID string) error {
	res, err := c.httpClient.Get(location)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()

	userID := strings.TrimSpace(fallbackUserID)
	cUserID := ""
	serviceToken := ""
	for current := res; current != nil; current = current.Request.Response {
		for _, cookie := range current.Cookies() {
			switch cookie.Name {
			case "userId":
				userID = cookie.Value
			case "cUserId":
				cUserID = cookie.Value
			case "serviceToken":
				serviceToken = cookie.Value
			}
		}
		if header := current.Header.Get("Extension-Pragma"); header != "" {
			var value struct {
				Ssecurity []byte `json:"ssecurity"`
			}
			if err := json.Unmarshal([]byte(header), &value); err == nil && len(value.Ssecurity) > 0 {
				c.ssecurity = value.Ssecurity
			}
		}
	}

	if cUserID == "" {
		cUserID = userID
	}
	if userID == "" || serviceToken == "" || len(c.ssecurity) == 0 {
		return fmt.Errorf("mihome pass token login returned incomplete auth state")
	}
	c.cookies = fmt.Sprintf("userId=%s; cUserId=%s; serviceToken=%s", userID, cUserID, serviceToken)
	return nil
}

func miHomeAPIBaseURL(region string) string {
	switch strings.ToLower(strings.TrimSpace(region)) {
	case "de", "i2", "ru", "sg", "us":
		return "https://" + strings.ToLower(strings.TrimSpace(region)) + ".api.io.mi.com/app"
	default:
		return "https://api.io.mi.com/app"
	}
}

func parseMiHomeCameraVendorInfo(payload json.RawMessage) (miot.CameraVendorInfo, error) {
	if len(payload) == 0 {
		return miot.CameraVendorInfo{}, fmt.Errorf("empty camera vendor payload")
	}

	var raw struct {
		PublicKey     string          `json:"public_key"`
		Sign          string          `json:"sign"`
		P2PID         string          `json:"p2p_id"`
		InitString    string          `json:"init_string"`
		SupportVendor string          `json:"support_vendor"`
		VendorName    string          `json:"vendor_name"`
		Vendor        json.RawMessage `json:"vendor"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return miot.CameraVendorInfo{}, err
	}

	info := miot.CameraVendorInfo{
		PublicKey:     raw.PublicKey,
		Sign:          raw.Sign,
		P2PID:         raw.P2PID,
		InitString:    raw.InitString,
		SupportVendor: firstNonEmpty(raw.SupportVendor, raw.VendorName),
	}
	if len(raw.Vendor) == 0 {
		return info, nil
	}

	var rawVendor map[string]any
	if err := json.Unmarshal(raw.Vendor, &rawVendor); err == nil {
		info.RawVendor = rawVendor
	}

	var vendor struct {
		Vendor       int             `json:"vendor"`
		VendorParams json.RawMessage `json:"vendor_params"`
	}
	if err := json.Unmarshal(raw.Vendor, &vendor); err != nil {
		return info, nil
	}
	info.VendorID = vendor.Vendor
	if info.SupportVendor == "" {
		info.SupportVendor = miHomeCameraVendorName(vendor.Vendor)
	}
	if len(vendor.VendorParams) == 0 {
		return info, nil
	}

	var rawParams map[string]any
	if err := json.Unmarshal(vendor.VendorParams, &rawParams); err == nil {
		info.RawVendorParam = rawParams
	}
	var params struct {
		P2PID      string `json:"p2p_id"`
		InitString string `json:"init_string"`
	}
	if err := json.Unmarshal(vendor.VendorParams, &params); err == nil {
		info.P2PID = firstNonEmpty(info.P2PID, params.P2PID)
		info.InitString = firstNonEmpty(info.InitString, params.InitString)
	}
	return info, nil
}

func miHomeCameraVendorName(id int) string {
	switch id {
	case 1:
		return "tutk"
	case 3:
		return "agora"
	case 4:
		return "cs2"
	case 6:
		return "mtp"
	default:
		return ""
	}
}

func miHomeDeviceInfo(raw map[string]any) miot.DeviceInfo {
	device := miot.DeviceInfo{
		DID:      stringValue(raw["did"]),
		Name:     stringValue(raw["name"]),
		UID:      stringValue(raw["uid"]),
		Token:    stringValue(raw["token"]),
		LocalIP:  firstNonEmpty(stringValue(raw["local_ip"]), stringValue(raw["localip"])),
		Model:    stringValue(raw["model"]),
		Online:   boolValue(raw["isOnline"]),
		HomeID:   stringValue(raw["home_id"]),
		HomeName: stringValue(raw["home_name"]),
		RoomID:   stringValue(raw["room_id"]),
		RoomName: stringValue(raw["room_name"]),
	}
	if extra, ok := raw["extra"].(map[string]any); ok {
		device.FWVersion = firstNonEmpty(stringValue(extra["fw_version"]), stringValue(extra["fwVersion"]))
		device.PincodeRequired = intValue(extra["isSetPincode"]) != 0
		device.PincodeType = intValue(extra["pincodeType"])
	}
	return device
}

func miHomeNonce() []byte {
	nonce := make([]byte, 12)
	_, _ = rand.Read(nonce[:8])
	binary.BigEndian.PutUint32(nonce[8:], uint32(time.Now().Unix()/60))
	return nonce
}

func miHomeSignedNonce(ssecurity []byte, nonce []byte) []byte {
	hasher := sha256.New()
	hasher.Write(ssecurity)
	hasher.Write(nonce)
	return hasher.Sum(nil)
}

func miHomeRC4Crypt(key []byte, payload []byte) ([]byte, error) {
	cipher, err := rc4.NewCipher(key)
	if err != nil {
		return nil, err
	}
	drop := make([]byte, 1024)
	cipher.XORKeyStream(drop, drop)
	result := make([]byte, len(payload))
	cipher.XORKeyStream(result, payload)
	return result, nil
}

func miHomeSignature(method string, path string, values url.Values, signedNonce []byte) string {
	builder := method + "&" + path + "&data=" + values.Get("data")
	if values.Has("rc4_hash__") {
		builder += "&rc4_hash__=" + values.Get("rc4_hash__")
	}
	builder += "&" + base64.StdEncoding.EncodeToString(signedNonce)
	sum := sha1.Sum([]byte(builder))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func miHomeReadLoginResponse(body io.ReadCloser, dest any) ([]byte, error) {
	defer func() { _ = body.Close() }()
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	payload, ok := bytes.CutPrefix(payload, []byte("&&&START&&&"))
	if !ok {
		return nil, fmt.Errorf("mihome login response prefix missing: %s", strings.TrimSpace(string(payload)))
	}
	return payload, json.Unmarshal(payload, dest)
}
