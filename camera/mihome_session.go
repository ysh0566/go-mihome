package camera

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	miHomeSessionUserIDEnv       = "MIBOT_MIHOME_USER_ID"
	miHomeSessionCUserIDEnv      = "MIBOT_MIHOME_CUSER_ID"
	miHomeSessionPassTokenEnv    = "MIBOT_MIHOME_PASS_TOKEN"
	miHomeSessionServiceTokenEnv = "MIBOT_MIHOME_SERVICE_TOKEN"
	miHomeSessionSsecurityEnv    = "MIBOT_MIHOME_SSECURITY"
	miHomeSessionDeviceIDEnv     = "MIBOT_MIHOME_DEVICE_ID"
	miHomeSessionRegionEnv       = "MIBOT_MIHOME_REGION"
	miHomeSessionSIDEnv          = "MIBOT_MIHOME_SID"
	miHomeSessionPlistPathEnv    = "MIBOT_MIHOME_PLIST_PATH"
	defaultMiHomeSessionPlist    = "Library/Group Containers/group.com.xiaomi.mihome/Library/Preferences/group.com.xiaomi.mihome.plist"
	defaultMiHomeSessionSID      = "xiaomiio"
)

var runMiHomePlutilJSON = func(ctx context.Context, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "plutil", "-convert", "json", "-o", "-", path)
	return cmd.Output()
}

type miHomeSession struct {
	UserID       string
	CUserID      string
	PassToken    string
	ServiceToken string
	Ssecurity    string
	DeviceID     string
	Region       string
	LoginSID     string
}

func loadLocalMiHomeSession(ctx context.Context) (miHomeSession, error) {
	session := miHomeSession{
		UserID:       strings.TrimSpace(os.Getenv(miHomeSessionUserIDEnv)),
		CUserID:      strings.TrimSpace(os.Getenv(miHomeSessionCUserIDEnv)),
		PassToken:    strings.TrimSpace(os.Getenv(miHomeSessionPassTokenEnv)),
		ServiceToken: strings.TrimSpace(os.Getenv(miHomeSessionServiceTokenEnv)),
		Ssecurity:    strings.TrimSpace(os.Getenv(miHomeSessionSsecurityEnv)),
		DeviceID:     strings.TrimSpace(os.Getenv(miHomeSessionDeviceIDEnv)),
		Region:       strings.TrimSpace(os.Getenv(miHomeSessionRegionEnv)),
		LoginSID:     strings.TrimSpace(os.Getenv(miHomeSessionSIDEnv)),
	}
	if session.completeDirect() || session.completePassToken() {
		session.normalize()
		return session, nil
	}

	plistPath := strings.TrimSpace(os.Getenv(miHomeSessionPlistPathEnv))
	if plistPath == "" {
		homeDir, _ := os.UserHomeDir()
		plistPath = filepath.Join(homeDir, defaultMiHomeSessionPlist)
	}
	if strings.TrimSpace(plistPath) == "" {
		return miHomeSession{}, fmt.Errorf("mihome plist path is unavailable")
	}

	payload, err := runMiHomePlutilJSON(ctx, plistPath)
	if err != nil {
		return miHomeSession{}, fmt.Errorf("load mihome plist: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return miHomeSession{}, fmt.Errorf("decode mihome plist json: %w", err)
	}
	session = miHomeSession{
		UserID:       firstNonEmpty(session.UserID, miHomeSessionValue(raw, "userId")),
		CUserID:      firstNonEmpty(session.CUserID, miHomeSessionValue(raw, "cUserId")),
		PassToken:    firstNonEmpty(session.PassToken, miHomeSessionValue(raw, "passToken")),
		ServiceToken: firstNonEmpty(session.ServiceToken, miHomeSessionValue(raw, "serviceToken")),
		Ssecurity:    firstNonEmpty(session.Ssecurity, miHomeSessionValue(raw, "ssecurity")),
		DeviceID:     firstNonEmpty(session.DeviceID, miHomeSessionValue(raw, "deviceId")),
		LoginSID:     firstNonEmpty(session.LoginSID, miHomeSessionValue(raw, "loginSID")),
		Region: firstNonEmpty(
			session.Region,
			miHomeSessionValue(raw, "serverCode"),
			miHomeSessionValue(raw, "countryCode"),
		),
	}
	if passport, ok := raw["passport"].(map[string]any); ok {
		session.UserID = firstNonEmpty(session.UserID, miHomeSessionValue(passport, "userId"))
		session.CUserID = firstNonEmpty(session.CUserID, miHomeSessionValue(passport, "cUserId"))
		session.PassToken = firstNonEmpty(session.PassToken, miHomeSessionValue(passport, "passToken"))
		session.ServiceToken = firstNonEmpty(session.ServiceToken, miHomeSessionValue(passport, "serviceToken"))
		session.Ssecurity = firstNonEmpty(session.Ssecurity, miHomeSessionValue(passport, "ssecurity"))
		session.DeviceID = firstNonEmpty(session.DeviceID, miHomeSessionValue(passport, "deviceId"))
		session.LoginSID = firstNonEmpty(session.LoginSID, miHomeSessionValue(passport, "loginSID"))
		session.Region = firstNonEmpty(session.Region, miHomeSessionValue(passport, "serverCode"), miHomeSessionValue(passport, "countryCode"))
	}
	if !session.completeDirect() && !session.completePassToken() {
		return miHomeSession{}, fmt.Errorf("mihome session is incomplete")
	}
	session.normalize()
	return session, nil
}

func (s miHomeSession) completeDirect() bool {
	return strings.TrimSpace(s.UserID) != "" &&
		strings.TrimSpace(s.ServiceToken) != "" &&
		strings.TrimSpace(s.Ssecurity) != "" &&
		strings.TrimSpace(s.DeviceID) != ""
}

func (s miHomeSession) completePassToken() bool {
	return strings.TrimSpace(s.UserID) != "" &&
		strings.TrimSpace(s.PassToken) != "" &&
		strings.TrimSpace(s.DeviceID) != ""
}

func (s *miHomeSession) normalize() {
	if s == nil {
		return
	}
	s.UserID = strings.TrimSpace(s.UserID)
	s.CUserID = strings.TrimSpace(s.CUserID)
	s.PassToken = strings.TrimSpace(s.PassToken)
	s.ServiceToken = strings.TrimSpace(s.ServiceToken)
	s.Ssecurity = strings.TrimSpace(s.Ssecurity)
	s.DeviceID = strings.TrimSpace(s.DeviceID)
	s.Region = strings.ToLower(strings.TrimSpace(s.Region))
	s.LoginSID = strings.TrimSpace(s.LoginSID)
	if s.CUserID == "" {
		s.CUserID = s.UserID
	}
	if s.Region == "" {
		s.Region = "cn"
	}
	if s.LoginSID == "" {
		s.LoginSID = defaultMiHomeSessionSID
	}
}

func miHomeSessionValue(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	return stringValue(raw[key])
}

func runtimeMiHomeSessionConfigured(ctx context.Context) bool {
	_, err := loadLocalMiHomeSession(ctx)
	return err == nil
}
