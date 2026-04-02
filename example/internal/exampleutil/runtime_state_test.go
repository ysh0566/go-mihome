package exampleutil

import (
	"context"
	"os"
	"regexp"
	"testing"

	miot "github.com/ysh0566/go-mihome"
)

func TestLoadRuntimeBootstrapStateMissingReturnsZero(t *testing.T) {
	ctx := context.Background()
	store, err := miot.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage returned error: %v", err)
	}

	got, err := LoadRuntimeBootstrapState(ctx, store)
	if err != nil {
		t.Fatalf("LoadRuntimeBootstrapState returned error: %v", err)
	}

	if got != (RuntimeBootstrapState{}) {
		t.Fatalf("LoadRuntimeBootstrapState() = %#v, want zero state", got)
	}
}

func TestRuntimeBootstrapStateRoundTripUsesStorageRoot(t *testing.T) {
	ctx := context.Background()
	store, err := miot.NewStorage(t.TempDir())
	if err != nil {
		t.Fatalf("NewStorage returned error: %v", err)
	}

	want := RuntimeBootstrapState{
		UID:           "100000001",
		CloudMIPSUUID: "cloud-mips-uuid",
		RuntimeDID:    "runtime-did",
	}
	if err := SaveRuntimeBootstrapState(ctx, store, want); err != nil {
		t.Fatalf("SaveRuntimeBootstrapState returned error: %v", err)
	}

	got, err := LoadRuntimeBootstrapState(ctx, store)
	if err != nil {
		t.Fatalf("LoadRuntimeBootstrapState returned error: %v", err)
	}
	if got != want {
		t.Fatalf("LoadRuntimeBootstrapState() = %#v, want %#v", got, want)
	}

	expectedPath := store.Path(runtimeBootstrapStateDomain, runtimeBootstrapStateName, miot.StorageFormatJSON)
	if _, err := os.Stat(expectedPath); err != nil {
		t.Fatalf("runtime bootstrap state path %q not found: %v", expectedPath, err)
	}
}

func TestNormalizeRuntimeCloudMIPSUUIDPreservesPythonLikeUUID(t *testing.T) {
	t.Parallel()

	got, changed, err := NormalizeRuntimeCloudMIPSUUID("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("NormalizeRuntimeCloudMIPSUUID returned error: %v", err)
	}
	if changed {
		t.Fatal("NormalizeRuntimeCloudMIPSUUID unexpectedly reported change")
	}
	if got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("uuid = %q, want original python-like uuid", got)
	}
}

func TestNormalizeRuntimeCloudMIPSUUIDRotatesLegacyFormat(t *testing.T) {
	t.Parallel()

	got, changed, err := NormalizeRuntimeCloudMIPSUUID("go-mihome-runtime-75a6779fd702b23d")
	if err != nil {
		t.Fatalf("NormalizeRuntimeCloudMIPSUUID returned error: %v", err)
	}
	if !changed {
		t.Fatal("NormalizeRuntimeCloudMIPSUUID should rotate legacy uuid format")
	}
	if got == "go-mihome-runtime-75a6779fd702b23d" {
		t.Fatal("NormalizeRuntimeCloudMIPSUUID kept legacy uuid unchanged")
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(got) {
		t.Fatalf("uuid = %q, want 32-char lowercase hex", got)
	}
}
