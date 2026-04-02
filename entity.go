package miot

import (
	"context"
	"fmt"
)

// Entity exposes platform-neutral operations for one spec-backed unit.
type Entity struct {
	deviceID string
	desc     EntityDescriptor
	backend  EntityBackend
}

// Descriptor returns the entity metadata.
func (e *Entity) Descriptor() EntityDescriptor {
	return e.desc
}

// Snapshot returns the entity runtime state.
func (e *Entity) Snapshot(ctx context.Context) (EntityState, error) {
	if e.backend == nil {
		return EntityState{}, &Error{Code: ErrInvalidArgument, Op: "entity snapshot", Msg: "backend is nil"}
	}
	online, err := e.backend.DeviceOnline(ctx, e.deviceID)
	if err != nil {
		return EntityState{}, err
	}
	return EntityState{Online: online}, nil
}

// Get reads the current property value for a property-backed entity.
func (e *Entity) Get(ctx context.Context) (PropertyResult, error) {
	if e.backend == nil {
		return PropertyResult{}, &Error{Code: ErrInvalidArgument, Op: "entity get", Msg: "backend is nil"}
	}
	if e.desc.Kind != EntityKindProperty {
		return PropertyResult{}, fmt.Errorf("entity %s does not support Get", e.desc.Key)
	}
	return e.backend.GetProperty(ctx, PropertyQuery{
		DID:  e.deviceID,
		SIID: e.desc.ServiceIID,
		PIID: e.desc.PropertyIID,
	})
}

// Set writes one property value through the backend.
func (e *Entity) Set(ctx context.Context, value SpecValue) (SetPropertyResult, error) {
	if e.backend == nil {
		return SetPropertyResult{}, &Error{Code: ErrInvalidArgument, Op: "entity set", Msg: "backend is nil"}
	}
	if e.desc.Kind != EntityKindProperty {
		return SetPropertyResult{}, fmt.Errorf("entity %s does not support Set", e.desc.Key)
	}
	return e.backend.SetProperty(ctx, SetPropertyRequest{
		DID:   e.deviceID,
		SIID:  e.desc.ServiceIID,
		PIID:  e.desc.PropertyIID,
		Value: value,
	})
}

// Invoke runs one action through the backend.
func (e *Entity) Invoke(ctx context.Context, input []SpecValue) (ActionResult, error) {
	if e.backend == nil {
		return ActionResult{}, &Error{Code: ErrInvalidArgument, Op: "entity invoke", Msg: "backend is nil"}
	}
	if e.desc.Kind != EntityKindAction {
		return ActionResult{}, fmt.Errorf("entity %s does not support Invoke", e.desc.Key)
	}
	return e.backend.InvokeAction(ctx, ActionRequest{
		DID:   e.deviceID,
		SIID:  e.desc.ServiceIID,
		AIID:  e.desc.ActionIID,
		Input: input,
	})
}

// SubscribeProperty subscribes to updates for a property-backed entity.
func (e *Entity) SubscribeProperty(ctx context.Context, fn PropertyEventHandler) (Subscription, error) {
	if e.backend == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "entity subscribe property", Msg: "backend is nil"}
	}
	if e.desc.Kind != EntityKindProperty {
		return nil, fmt.Errorf("entity %s does not support property subscriptions", e.desc.Key)
	}
	return e.backend.SubscribeProperty(ctx, PropertySubscription{
		DID:  e.deviceID,
		SIID: e.desc.ServiceIID,
		PIID: e.desc.PropertyIID,
	}, fn)
}

// SubscribeEvent subscribes to updates for an event-backed entity.
func (e *Entity) SubscribeEvent(ctx context.Context, fn EventHandler) (Subscription, error) {
	if e.backend == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "entity subscribe event", Msg: "backend is nil"}
	}
	if e.desc.Kind != EntityKindEvent {
		return nil, fmt.Errorf("entity %s does not support event subscriptions", e.desc.Key)
	}
	return e.backend.SubscribeEvent(ctx, EventSubscription{
		DID:  e.deviceID,
		SIID: e.desc.ServiceIID,
		EIID: e.desc.EventIID,
	}, fn)
}

// SubscribeState subscribes to online/offline state changes for the parent device.
func (e *Entity) SubscribeState(ctx context.Context, fn DeviceStateHandler) (Subscription, error) {
	if e.backend == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "entity subscribe state", Msg: "backend is nil"}
	}
	return e.backend.SubscribeDeviceState(ctx, e.deviceID, fn)
}
