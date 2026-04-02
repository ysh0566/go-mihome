package miot

import (
	"context"
	"os"
	"reflect"
	"slices"
	"testing"
)

type testOAuthCache struct {
	UID    string `json:"uid"`
	Server string `json:"server"`
}

type testAuthInfo struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresTS    int64  `json:"expires_ts"`
}

func TestStorageSaveLoadJSONRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := testOAuthCache{
		UID:    "100000001",
		Server: "cn",
	}
	if err := SaveJSON(ctx, store, "cloud_cache", "oauth", want); err != nil {
		t.Fatal(err)
	}

	got, err := LoadJSON[testOAuthCache](ctx, store, "cloud_cache", "oauth")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("LoadJSON() = %#v, want %#v", got, want)
	}
}

func TestStorageSaveLoadTextRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := "miot text payload"
	if err := store.SaveText(ctx, "cache", "message", want); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadText(ctx, "cache", "message")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("LoadText() = %q, want %q", got, want)
	}
}

func TestStorageSaveLoadBytesRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := []byte("miot-bytes-payload")
	if err := store.SaveBytes(ctx, "cache", "blob", want); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadBytes(ctx, "cache", "blob")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadBytes() = %q, want %q", got, want)
	}
}

func TestStorageSaveLoadEmptyBytesRoundTrip(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	want := []byte{}
	if err := store.SaveBytes(ctx, "cache", "empty", want); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadBytes(ctx, "cache", "empty")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadBytes() = %q, want empty bytes", got)
	}
}

func TestStorageRejectsHashMismatch(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SaveText(ctx, "cache", "message", "miot text payload"); err != nil {
		t.Fatal(err)
	}

	path := store.Path("cache", "message", StorageFormatText)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("saved file is empty")
	}
	data[len(data)-1] ^= 0xff
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := store.LoadText(ctx, "cache", "message"); err == nil {
		t.Fatal("LoadText() error = nil, want hash mismatch error")
	}
}

func TestStorageNamesExistsAndRemove(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SaveBytes(ctx, "cache", "alpha", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBytes(ctx, "cache", "beta", []byte("b")); err != nil {
		t.Fatal(err)
	}

	names, err := store.Names(ctx, "cache", StorageFormatBytes)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	wantNames := []string{"alpha", "beta"}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatalf("Names() = %#v, want %#v", names, wantNames)
	}

	exists, err := store.Exists(ctx, "cache", "alpha", StorageFormatBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("Exists(alpha) = false, want true")
	}

	if err := store.Remove(ctx, "cache", "alpha", StorageFormatBytes); err != nil {
		t.Fatal(err)
	}

	exists, err = store.Exists(ctx, "cache", "alpha", StorageFormatBytes)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("Exists(alpha) = true after Remove, want false")
	}
}

func TestStorageRemoveDomainAndClear(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SaveText(ctx, "alpha", "one", "1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveText(ctx, "beta", "two", "2"); err != nil {
		t.Fatal(err)
	}

	if err := store.RemoveDomain(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}

	exists, err := store.Exists(ctx, "alpha", "one", StorageFormatText)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("Exists(alpha/one) = true after RemoveDomain, want false")
	}

	if err := store.Clear(ctx); err != nil {
		t.Fatal(err)
	}

	exists, err = store.Exists(ctx, "beta", "two", StorageFormatText)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("Exists(beta/two) = true after Clear, want false")
	}
}

func TestStorageUserConfigMerge(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	base := UserConfigDocument{
		Entries: []UserConfigEntry{
			mustUserConfigEntry(t, "auth_info", testAuthInfo{
				AccessToken:  "token-a",
				RefreshToken: "refresh-a",
				ExpiresTS:    100,
			}),
			mustUserConfigEntry(t, "enable_subscribe", true),
		},
	}
	if err := store.UpdateUserConfig(ctx, "100000001", "cn", &base, false); err != nil {
		t.Fatal(err)
	}

	patch := UserConfigDocument{
		Entries: []UserConfigEntry{
			mustUserConfigEntry(t, "auth_info", testAuthInfo{
				AccessToken:  "token-b",
				RefreshToken: "refresh-a",
				ExpiresTS:    200,
			}),
			mustUserConfigEntry(t, "ctrl_mode", "auto"),
		},
	}
	if err := store.UpdateUserConfig(ctx, "100000001", "cn", &patch, false); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadUserConfig(ctx, "100000001", "cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3", len(got.Entries))
	}

	authInfo, err := DecodeUserConfigEntry[testAuthInfo](findConfigEntry(t, got, "auth_info"))
	if err != nil {
		t.Fatal(err)
	}
	if authInfo.AccessToken != "token-b" || authInfo.ExpiresTS != 200 {
		t.Fatalf("auth_info = %#v, want updated token/expires", authInfo)
	}
}

func TestStorageUserConfigReplace(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	base := UserConfigDocument{
		Entries: []UserConfigEntry{
			mustUserConfigEntry(t, "auth_info", testAuthInfo{
				AccessToken:  "token-a",
				RefreshToken: "refresh-a",
				ExpiresTS:    100,
			}),
			mustUserConfigEntry(t, "enable_subscribe", true),
		},
	}
	if err := store.UpdateUserConfig(ctx, "100000001", "cn", &base, false); err != nil {
		t.Fatal(err)
	}

	replaceDoc := UserConfigDocument{
		Entries: []UserConfigEntry{
			mustUserConfigEntry(t, "ctrl_mode", "cloud"),
		},
	}
	if err := store.UpdateUserConfig(ctx, "100000001", "cn", &replaceDoc, true); err != nil {
		t.Fatal(err)
	}

	got, err := store.LoadUserConfig(ctx, "100000001", "cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 {
		t.Fatalf("len(Entries) = %d, want 1", len(got.Entries))
	}
	mode, err := DecodeUserConfigEntry[string](findConfigEntry(t, got, "ctrl_mode"))
	if err != nil {
		t.Fatal(err)
	}
	if mode != "cloud" {
		t.Fatalf("ctrl_mode = %q, want cloud", mode)
	}

	emptyDoc := UserConfigDocument{}
	if err := store.UpdateUserConfig(ctx, "100000001", "cn", &emptyDoc, true); err != nil {
		t.Fatal(err)
	}
	exists, err := store.Exists(ctx, userConfigDomain, "100000001_cn", StorageFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("expected empty replacement document to remain persisted")
	}
	got, err = store.LoadUserConfig(ctx, "100000001", "cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("len(Entries) after empty replace = %d, want 0", len(got.Entries))
	}
}

func TestStorageUserConfigFilterAndRemove(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	doc := UserConfigDocument{
		Entries: []UserConfigEntry{
			mustUserConfigEntry(t, "auth_info", testAuthInfo{
				AccessToken:  "token-a",
				RefreshToken: "refresh-a",
				ExpiresTS:    100,
			}),
			mustUserConfigEntry(t, "ctrl_mode", "auto"),
			mustUserConfigEntry(t, "enable_subscribe", true),
		},
	}
	if err := store.UpdateUserConfig(ctx, "100000001", "cn", &doc, true); err != nil {
		t.Fatal(err)
	}

	filtered, err := store.LoadUserConfig(ctx, "100000001", "cn", "enable_subscribe", "ctrl_mode")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Entries) != 2 {
		t.Fatalf("len(filtered.Entries) = %d, want 2", len(filtered.Entries))
	}
	if _, err := DecodeUserConfigEntry[bool](findConfigEntry(t, filtered, "enable_subscribe")); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeUserConfigEntry[string](findConfigEntry(t, filtered, "ctrl_mode")); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateUserConfig(ctx, "100000001", "cn", nil, false); err != nil {
		t.Fatal(err)
	}
	got, err := store.LoadUserConfig(ctx, "100000001", "cn")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 0 {
		t.Fatalf("len(Entries) = %d, want 0 after remove", len(got.Entries))
	}
}

func mustUserConfigEntry[T any](t *testing.T, key string, value T) UserConfigEntry {
	t.Helper()

	entry, err := NewUserConfigEntry(key, value)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func findConfigEntry(t *testing.T, doc UserConfigDocument, key string) UserConfigEntry {
	t.Helper()

	for _, entry := range doc.Entries {
		if entry.Key == key {
			return entry
		}
	}
	t.Fatalf("entry %q not found", key)
	return UserConfigEntry{}
}
