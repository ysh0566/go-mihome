package exampleutil

import "testing"

func TestLoadCloudConfigPrefersEnvironment(t *testing.T) {
	t.Setenv("MIOT_CLIENT_ID", "env-client")
	t.Setenv("MIOT_CLOUD_SERVER", "de")
	t.Setenv("MIOT_ACCESS_TOKEN", "env-access")
	t.Setenv("MIOT_REFRESH_TOKEN", "env-refresh")
	t.Setenv("MIOT_STORAGE_DIR", "/tmp/miot-example")

	cfg, err := LoadCloudConfig(CloudConfig{
		ClientID:     "default-client",
		CloudServer:  "cn",
		AccessToken:  "default-access",
		RefreshToken: "default-refresh",
		StorageDir:   "./.miot-example",
	})
	if err != nil {
		t.Fatalf("LoadCloudConfig returned error: %v", err)
	}

	if cfg.ClientID != "env-client" {
		t.Fatalf("ClientID = %q, want env-client", cfg.ClientID)
	}
	if cfg.CloudServer != "de" {
		t.Fatalf("CloudServer = %q, want de", cfg.CloudServer)
	}
	if cfg.AccessToken != "env-access" {
		t.Fatalf("AccessToken = %q, want env-access", cfg.AccessToken)
	}
	if cfg.RefreshToken != "env-refresh" {
		t.Fatalf("RefreshToken = %q, want env-refresh", cfg.RefreshToken)
	}
	if cfg.StorageDir != "/tmp/miot-example" {
		t.Fatalf("StorageDir = %q, want /tmp/miot-example", cfg.StorageDir)
	}
}

func TestLoadPropertySelectionFallsBackToDefaults(t *testing.T) {
	selection, err := LoadPropertySelection(PropertySelection{
		DeviceDID: "123456789",
		SIID:      2,
		PIID:      1,
	})
	if err != nil {
		t.Fatalf("LoadPropertySelection returned error: %v", err)
	}

	if selection.DeviceDID != "123456789" {
		t.Fatalf("DeviceDID = %q, want 123456789", selection.DeviceDID)
	}
	if selection.SIID != 2 {
		t.Fatalf("SIID = %d, want 2", selection.SIID)
	}
	if selection.PIID != 1 {
		t.Fatalf("PIID = %d, want 1", selection.PIID)
	}
}

func TestLoadPropertySelectionRejectsInvalidInteger(t *testing.T) {
	t.Setenv("MIOT_PROPERTY_SIID", "bad")

	if _, err := LoadPropertySelection(PropertySelection{}); err == nil {
		t.Fatal("LoadPropertySelection should reject invalid MIOT_PROPERTY_SIID")
	}
}
