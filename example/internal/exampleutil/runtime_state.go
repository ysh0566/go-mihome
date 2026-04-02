package exampleutil

import (
	"context"
	"errors"
	"io/fs"

	miot "github.com/ysh0566/go-mihome"
)

const (
	runtimeBootstrapStateDomain = "example_runtime"
	runtimeBootstrapStateName   = "bootstrap_state"
)

// RuntimeBootstrapState stores the example-owned runtime identity persisted across launches.
type RuntimeBootstrapState struct {
	UID           string `json:"uid"`
	CloudMIPSUUID string `json:"cloud_mips_uuid"`
	RuntimeDID    string `json:"runtime_did"`
}

// LoadRuntimeBootstrapState loads the persisted runtime bootstrap state, returning a zero state when missing.
func LoadRuntimeBootstrapState(ctx context.Context, storage *miot.Storage) (RuntimeBootstrapState, error) {
	state, err := miot.LoadJSON[RuntimeBootstrapState](ctx, storage, runtimeBootstrapStateDomain, runtimeBootstrapStateName)
	if errors.Is(err, fs.ErrNotExist) {
		return RuntimeBootstrapState{}, nil
	}
	if err != nil {
		return RuntimeBootstrapState{}, err
	}
	return state, nil
}

// SaveRuntimeBootstrapState persists the runtime bootstrap state as a dedicated JSON payload.
func SaveRuntimeBootstrapState(ctx context.Context, storage *miot.Storage, state RuntimeBootstrapState) error {
	return miot.SaveJSON(ctx, storage, runtimeBootstrapStateDomain, runtimeBootstrapStateName, state)
}

