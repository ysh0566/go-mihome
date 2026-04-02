package exampleutil

import (
	"testing"

	miot "github.com/ysh0566/go-mihome"
)

func TestBuildRuntimeLocalRouteCandidatesFiltersUnmatchedGroups(t *testing.T) {
	homes := []miot.MIoTClientHome{
		{HomeID: "home-1", HomeName: "Primary Home", GroupID: "group-1"},
		{HomeID: "home-2", HomeName: "Guest Home", GroupID: "group-2"},
	}
	services := map[string]miot.MIPSServiceInfo{
		"group-1": {
			GroupID:   "group-1",
			Addresses: []string{"192.168.1.10"},
			Port:      443,
		},
		"group-3": {
			GroupID:   "group-3",
			Addresses: []string{"192.168.1.30"},
			Port:      443,
		},
	}

	got := BuildRuntimeLocalRouteCandidates(homes, services, "runtime-did")

	if len(got) != 1 {
		t.Fatalf("len(BuildRuntimeLocalRouteCandidates()) = %d, want 1", len(got))
	}
	if got[0].HomeID != "home-1" || got[0].HomeName != "Primary Home" || got[0].GroupID != "group-1" {
		t.Fatalf("candidate = %#v, want home-1/Primary Home/group-1", got[0])
	}
	if got[0].ClientDID != "runtime-did" {
		t.Fatalf("ClientDID = %q, want runtime-did", got[0].ClientDID)
	}
}

func TestBuildRuntimeLocalRouteCandidatesUsesRuntimeDIDAndPrimaryAddress(t *testing.T) {
	homes := []miot.MIoTClientHome{
		{HomeID: "home-1", HomeName: "Primary Home", GroupID: "group-1"},
	}
	services := map[string]miot.MIPSServiceInfo{
		"group-1": {
			GroupID:   "group-1",
			Addresses: []string{"", "192.168.1.11", "192.168.1.12"},
			Port:      5443,
		},
	}

	got := BuildRuntimeLocalRouteCandidates(homes, services, "stable-runtime-did")

	if len(got) != 1 {
		t.Fatalf("len(BuildRuntimeLocalRouteCandidates()) = %d, want 1", len(got))
	}
	if got[0].ClientDID != "stable-runtime-did" {
		t.Fatalf("ClientDID = %q, want stable-runtime-did", got[0].ClientDID)
	}
	if got[0].Host != "192.168.1.11" {
		t.Fatalf("Host = %q, want 192.168.1.11", got[0].Host)
	}
	if got[0].Port != 5443 {
		t.Fatalf("Port = %d, want 5443", got[0].Port)
	}
}
