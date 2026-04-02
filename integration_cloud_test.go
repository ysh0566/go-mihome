//go:build integration

package miot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type integrationAuth struct {
	Region      string `json:"region"`
	AccessToken string `json:"accessToken"`
	DeviceDID   string `json:"deviceDID"`
}

func TestIntegrationCloudProfile(t *testing.T) {
	client := newIntegrationCloudClient(t)
	profile, err := client.GetUserInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if profile.MiliaoNick == "" {
		t.Fatal("expected nickname")
	}
}

func TestIntegrationCloudGetHome(t *testing.T) {
	client := newIntegrationCloudClient(t)
	homes, err := client.GetHomeInfos(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if homes.UID == "" {
		t.Fatal("expected uid")
	}
}

func TestIntegrationCloudDeviceList(t *testing.T) {
	client := newIntegrationCloudClient(t)
	devices, err := client.GetDevices(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices.Devices) == 0 {
		t.Fatal("expected devices")
	}
}

func TestIntegrationCloudGetProp(t *testing.T) {
	auth := loadIntegrationAuth(t)
	if auth.DeviceDID == "" {
		t.Skip("deviceDID not configured")
	}
	client := newIntegrationCloudClient(t)
	prop, err := client.GetProp(context.Background(), PropertyQuery{
		DID:  auth.DeviceDID,
		SIID: 2,
		PIID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if prop.DID == "" {
		t.Fatal("expected did")
	}
}

func newIntegrationCloudClient(t *testing.T) *CloudClient {
	t.Helper()

	auth := loadIntegrationAuth(t)
	client, err := NewCloudClient(
		CloudConfig{
			ClientID:    "2882303761520431603",
			CloudServer: auth.Region,
		},
		WithCloudTokenProvider(staticTokenProvider{token: auth.AccessToken}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func loadIntegrationAuth(t *testing.T) integrationAuth {
	t.Helper()

	paths := []string{
		"auth.json",
		filepath.Clean("../../auth.json"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		var auth integrationAuth
		if err := json.Unmarshal(data, &auth); err != nil {
			t.Fatal(err)
		}
		if auth.Region == "" || auth.AccessToken == "" {
			t.Fatalf("invalid auth file %s", path)
		}
		return auth
	}
	t.Skip("auth.json not found")
	return integrationAuth{}
}
