package miot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type testManufacturerCache struct {
	UpdatedAt int64               `json:"updated_at"`
	Entries   []ManufacturerEntry `json:"entries"`
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

func (c fixedClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	ch <- c.now.Add(d)
	return ch
}

func (c fixedClock) NewTicker(d time.Duration) Ticker {
	return fixedTicker{c: make(chan time.Time)}
}

func (c fixedClock) NewTimer(d time.Duration) Timer {
	return &fixedTimer{c: make(chan time.Time), active: true}
}

type fixedTicker struct {
	c chan time.Time
}

func (t fixedTicker) C() <-chan time.Time {
	return t.c
}

func (t fixedTicker) Stop() {}

type fixedTimer struct {
	c      chan time.Time
	active bool
}

func (t *fixedTimer) C() <-chan time.Time {
	return t.c
}

func (t *fixedTimer) Stop() bool {
	wasActive := t.active
	t.active = false
	return wasActive
}

func (t *fixedTimer) Reset(d time.Duration) bool {
	wasActive := t.active
	t.active = true
	return wasActive
}

func TestManufacturerCatalogUsesCache(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	now := time.Unix(1_700_000_000, 0)
	cache := testManufacturerCache{
		UpdatedAt: now.Unix(),
		Entries: []ManufacturerEntry{
			{Code: "yeelink", Name: "Yeelight"},
		},
	}
	cacheBytes, err := json.Marshal(cache)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBytes(ctx, "miot_specs", "manufacturer", cacheBytes); err != nil {
		t.Fatal(err)
	}

	catalog, err := NewManufacturerCatalog(store, WithManufacturerClock(fixedClock{now: now}))
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := catalog.Name("yeelink"); got != "Yeelight" {
		t.Fatalf("Name(yeelink) = %q, want Yeelight", got)
	}
}

func TestManufacturerCatalogFetchesAndCaches(t *testing.T) {
	ctx := context.Background()
	store, err := NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"yeelink":{"name":"Yeelight"},"zhimi":{"name":"Zhimi"}}`)
	}))
	defer server.Close()

	now := time.Unix(1_700_000_000, 0)
	catalog, err := NewManufacturerCatalog(
		store,
		WithManufacturerClock(fixedClock{now: now}),
		WithManufacturerHTTPClient(server.Client()),
		WithManufacturerURL(server.URL),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if got := catalog.Name("zhimi"); got != "Zhimi" {
		t.Fatalf("Name(zhimi) = %q, want Zhimi", got)
	}

	cacheBytes, err := store.LoadBytes(ctx, "miot_specs", "manufacturer")
	if err != nil {
		t.Fatal(err)
	}
	var cache testManufacturerCache
	if err := json.Unmarshal(cacheBytes, &cache); err != nil {
		t.Fatal(err)
	}
	if len(cache.Entries) != 2 {
		t.Fatalf("len(cache.Entries) = %d, want 2", len(cache.Entries))
	}
}
