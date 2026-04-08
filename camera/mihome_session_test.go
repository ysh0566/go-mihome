package camera

import (
	"context"
	"os"
	"testing"
)

func TestLoadLocalMiHomeSessionFromEnvironment(t *testing.T) {
	t.Setenv(miHomeSessionUserIDEnv, "user-1")
	t.Setenv(miHomeSessionServiceTokenEnv, "service-token")
	t.Setenv(miHomeSessionSsecurityEnv, "c2VjdXJpdHk=")
	t.Setenv(miHomeSessionDeviceIDEnv, "device-1")
	t.Setenv(miHomeSessionRegionEnv, "CN")

	session, err := loadLocalMiHomeSession(context.Background())
	if err != nil {
		t.Fatalf("loadLocalMiHomeSession() error = %v", err)
	}
	if got, want := session.UserID, "user-1"; got != want {
		t.Fatalf("session.UserID = %q, want %q", got, want)
	}
	if got, want := session.CUserID, "user-1"; got != want {
		t.Fatalf("session.CUserID = %q, want %q", got, want)
	}
	if got, want := session.Region, "cn"; got != want {
		t.Fatalf("session.Region = %q, want %q", got, want)
	}
}

func TestLoadLocalMiHomeSessionFromPlistJSON(t *testing.T) {
	for _, key := range []string{
		miHomeSessionUserIDEnv,
		miHomeSessionCUserIDEnv,
		miHomeSessionServiceTokenEnv,
		miHomeSessionSsecurityEnv,
		miHomeSessionDeviceIDEnv,
		miHomeSessionRegionEnv,
		miHomeSessionPlistPathEnv,
	} {
		t.Setenv(key, "")
	}

	original := runMiHomePlutilJSON
	runMiHomePlutilJSON = func(ctx context.Context, path string) ([]byte, error) {
		return []byte(`{
			"userId": "user-2",
			"passport": {
				"serviceToken": "service-token-2",
				"ssecurity": "c2VjdXJpdHktMg==",
				"deviceId": "device-2",
				"serverCode": "DE"
			}
		}`), nil
	}
	t.Cleanup(func() {
		runMiHomePlutilJSON = original
	})

	session, err := loadLocalMiHomeSession(context.Background())
	if err != nil {
		t.Fatalf("loadLocalMiHomeSession() error = %v", err)
	}
	if got, want := session.UserID, "user-2"; got != want {
		t.Fatalf("session.UserID = %q, want %q", got, want)
	}
	if got, want := session.ServiceToken, "service-token-2"; got != want {
		t.Fatalf("session.ServiceToken = %q, want %q", got, want)
	}
	if got, want := session.Region, "de"; got != want {
		t.Fatalf("session.Region = %q, want %q", got, want)
	}
}

func TestRuntimeMiHomeSessionConfigured(t *testing.T) {
	t.Setenv(miHomeSessionUserIDEnv, "user-3")
	t.Setenv(miHomeSessionServiceTokenEnv, "service-token-3")
	t.Setenv(miHomeSessionSsecurityEnv, "c2VjdXJpdHktMw==")
	t.Setenv(miHomeSessionDeviceIDEnv, "device-3")

	if !runtimeMiHomeSessionConfigured(context.Background()) {
		t.Fatal("runtimeMiHomeSessionConfigured() = false, want true")
	}
}

func TestLoadLocalMiHomeSessionRejectsIncompleteInput(t *testing.T) {
	t.Setenv(miHomeSessionUserIDEnv, "user-4")
	t.Setenv(miHomeSessionServiceTokenEnv, "")
	t.Setenv(miHomeSessionSsecurityEnv, "")
	t.Setenv(miHomeSessionDeviceIDEnv, "")

	original := runMiHomePlutilJSON
	runMiHomePlutilJSON = func(ctx context.Context, path string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() {
		runMiHomePlutilJSON = original
	})

	if _, err := loadLocalMiHomeSession(context.Background()); err == nil {
		t.Fatal("loadLocalMiHomeSession() error = nil, want error")
	}
}
