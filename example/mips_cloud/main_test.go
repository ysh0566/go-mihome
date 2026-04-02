package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	miot "github.com/ysh0566/go-mihome"
)

func TestRunCloudMIPSReadySequenceAwaitsBeforeSubscribing(t *testing.T) {
	t.Parallel()

	var steps []string
	client := &fakeCloudMIPSReadyClient{
		onStart: func(context.Context) error {
			steps = append(steps, "start")
			return nil
		},
		onAwait: func(context.Context) error {
			steps = append(steps, "await")
			return nil
		},
		onSubscribeDeviceState: func(context.Context, string, miot.DeviceStateHandler) (miot.Subscription, error) {
			steps = append(steps, "subscribe_device_state")
			return fakeSubscription{}, nil
		},
		onSubscribeProperty: func(context.Context, miot.PropertySubscription, miot.PropertyEventHandler) (miot.Subscription, error) {
			steps = append(steps, "subscribe_property")
			return fakeSubscription{}, nil
		},
		onSubscribeEvent: func(context.Context, miot.EventSubscription, miot.EventHandler) (miot.Subscription, error) {
			steps = append(steps, "subscribe_event")
			return fakeSubscription{}, nil
		},
	}

	if _, err := runCloudMIPSReadySequence(context.Background(), client, "device.did", 2, 1, cloudMIPSReadyHandlers{}, func() error {
		steps = append(steps, "ready")
		return nil
	}); err != nil {
		t.Fatalf("runCloudMIPSReadySequence returned error: %v", err)
	}

	want := []string{
		"start",
		"await",
		"subscribe_device_state",
		"subscribe_property",
		"subscribe_event",
		"ready",
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("steps = %v, want %v", steps, want)
	}
}

func TestRunCloudMIPSReadySequenceClosesStateSubscriptionWhenPropertySubscribeFails(t *testing.T) {
	t.Parallel()

	stateSub := &trackingSubscription{}
	client := &fakeCloudMIPSReadyClient{
		onStart: func(context.Context) error { return nil },
		onAwait: func(context.Context) error { return nil },
		onSubscribeDeviceState: func(context.Context, string, miot.DeviceStateHandler) (miot.Subscription, error) {
			return stateSub, nil
		},
		onSubscribeProperty: func(context.Context, miot.PropertySubscription, miot.PropertyEventHandler) (miot.Subscription, error) {
			return nil, errors.New("property failed")
		},
	}

	_, err := runCloudMIPSReadySequence(context.Background(), client, "device.did", 2, 1, cloudMIPSReadyHandlers{}, nil)
	if err == nil {
		t.Fatal("runCloudMIPSReadySequence returned nil error, want property failure")
	}
	if !stateSub.closed {
		t.Fatal("state subscription was not closed after property subscription failure")
	}
}

func TestRunCloudMIPSReadySequenceClosesOpenedSubscriptionsWhenEventSubscribeFails(t *testing.T) {
	t.Parallel()

	stateSub := &trackingSubscription{}
	propertySub := &trackingSubscription{}
	client := &fakeCloudMIPSReadyClient{
		onStart: func(context.Context) error { return nil },
		onAwait: func(context.Context) error { return nil },
		onSubscribeDeviceState: func(context.Context, string, miot.DeviceStateHandler) (miot.Subscription, error) {
			return stateSub, nil
		},
		onSubscribeProperty: func(context.Context, miot.PropertySubscription, miot.PropertyEventHandler) (miot.Subscription, error) {
			return propertySub, nil
		},
		onSubscribeEvent: func(context.Context, miot.EventSubscription, miot.EventHandler) (miot.Subscription, error) {
			return nil, errors.New("event failed")
		},
	}

	_, err := runCloudMIPSReadySequence(context.Background(), client, "device.did", 2, 1, cloudMIPSReadyHandlers{}, nil)
	if err == nil {
		t.Fatal("runCloudMIPSReadySequence returned nil error, want event failure")
	}
	if !stateSub.closed {
		t.Fatal("state subscription was not closed after event subscription failure")
	}
	if !propertySub.closed {
		t.Fatal("property subscription was not closed after event subscription failure")
	}
}

func TestRunCloudMIPSReadySequenceClosesOpenedSubscriptionsWhenReadyEmitFails(t *testing.T) {
	t.Parallel()

	stateSub := &trackingSubscription{}
	propertySub := &trackingSubscription{}
	eventSub := &trackingSubscription{}
	client := &fakeCloudMIPSReadyClient{
		onStart: func(context.Context) error { return nil },
		onAwait: func(context.Context) error { return nil },
		onSubscribeDeviceState: func(context.Context, string, miot.DeviceStateHandler) (miot.Subscription, error) {
			return stateSub, nil
		},
		onSubscribeProperty: func(context.Context, miot.PropertySubscription, miot.PropertyEventHandler) (miot.Subscription, error) {
			return propertySub, nil
		},
		onSubscribeEvent: func(context.Context, miot.EventSubscription, miot.EventHandler) (miot.Subscription, error) {
			return eventSub, nil
		},
	}

	_, err := runCloudMIPSReadySequence(context.Background(), client, "device.did", 2, 1, cloudMIPSReadyHandlers{}, func() error {
		return errors.New("ready failed")
	})
	if err == nil {
		t.Fatal("runCloudMIPSReadySequence returned nil error, want ready failure")
	}
	if !stateSub.closed {
		t.Fatal("state subscription was not closed after ready emission failure")
	}
	if !propertySub.closed {
		t.Fatal("property subscription was not closed after ready emission failure")
	}
	if !eventSub.closed {
		t.Fatal("event subscription was not closed after ready emission failure")
	}
}

type fakeCloudMIPSReadyClient struct {
	onStart                func(context.Context) error
	onAwait                func(context.Context) error
	onSubscribeDeviceState func(context.Context, string, miot.DeviceStateHandler) (miot.Subscription, error)
	onSubscribeProperty    func(context.Context, miot.PropertySubscription, miot.PropertyEventHandler) (miot.Subscription, error)
	onSubscribeEvent       func(context.Context, miot.EventSubscription, miot.EventHandler) (miot.Subscription, error)
}

func (f *fakeCloudMIPSReadyClient) Start(ctx context.Context) error {
	if f.onStart != nil {
		return f.onStart(ctx)
	}
	return nil
}

func (f *fakeCloudMIPSReadyClient) AwaitConnection(ctx context.Context) error {
	if f.onAwait != nil {
		return f.onAwait(ctx)
	}
	return nil
}

func (f *fakeCloudMIPSReadyClient) SubscribeDeviceState(ctx context.Context, did string, fn miot.DeviceStateHandler) (miot.Subscription, error) {
	if f.onSubscribeDeviceState != nil {
		return f.onSubscribeDeviceState(ctx, did, fn)
	}
	return fakeSubscription{}, nil
}

func (f *fakeCloudMIPSReadyClient) SubscribeProperty(ctx context.Context, req miot.PropertySubscription, fn miot.PropertyEventHandler) (miot.Subscription, error) {
	if f.onSubscribeProperty != nil {
		return f.onSubscribeProperty(ctx, req, fn)
	}
	return fakeSubscription{}, nil
}

func (f *fakeCloudMIPSReadyClient) SubscribeEvent(ctx context.Context, req miot.EventSubscription, fn miot.EventHandler) (miot.Subscription, error) {
	if f.onSubscribeEvent != nil {
		return f.onSubscribeEvent(ctx, req, fn)
	}
	return fakeSubscription{}, nil
}

type fakeSubscription struct{}

func (fakeSubscription) Close() error { return nil }

type trackingSubscription struct {
	closed bool
}

func (s *trackingSubscription) Close() error {
	s.closed = true
	return nil
}
