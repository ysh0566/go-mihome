package miot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"time"
)

const (
	manufacturerDomain = "miot_specs"
	manufacturerName   = "manufacturer"
	manufacturerURL    = "https://cdn.cnbj1.fds.api.mi-img.com/res-conf/xiaomi-home/manufacturer.json"
)

var manufacturerEffectiveTime = 14 * 24 * time.Hour

// ManufacturerCatalogOption configures a ManufacturerCatalog instance.
type ManufacturerCatalogOption func(*ManufacturerCatalog)

// ManufacturerEntry maps a short manufacturer code to a display name.
type ManufacturerEntry struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// ManufacturerCatalog loads, caches, and resolves manufacturer names.
type ManufacturerCatalog struct {
	cache   ByteStore
	http    HTTPDoer
	clock   Clock
	url     string
	entries []ManufacturerEntry
	index   map[string]string
}

type manufacturerCacheDocument struct {
	UpdatedAt int64               `json:"updated_at"`
	Entries   []ManufacturerEntry `json:"entries"`
}

type manufacturerRemoteEntry struct {
	Name string `json:"name"`
}

// WithManufacturerClock injects a test clock into ManufacturerCatalog.
func WithManufacturerClock(clock Clock) ManufacturerCatalogOption {
	return func(c *ManufacturerCatalog) {
		if clock != nil {
			c.clock = clock
		}
	}
}

// WithManufacturerHTTPClient injects a custom HTTP transport into ManufacturerCatalog.
func WithManufacturerHTTPClient(client HTTPDoer) ManufacturerCatalogOption {
	return func(c *ManufacturerCatalog) {
		if client != nil {
			c.http = client
		}
	}
}

// WithManufacturerURL overrides the remote manufacturer catalog URL.
func WithManufacturerURL(url string) ManufacturerCatalogOption {
	return func(c *ManufacturerCatalog) {
		if url != "" {
			c.url = url
		}
	}
}

// NewManufacturerCatalog creates a manufacturer catalog backed by hashed cache storage.
func NewManufacturerCatalog(cache ByteStore, opts ...ManufacturerCatalogOption) (*ManufacturerCatalog, error) {
	if cache == nil {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new manufacturer catalog", Msg: "cache is nil"}
	}
	catalog := &ManufacturerCatalog{
		cache: cache,
		http:  &http.Client{Timeout: 20 * time.Second},
		clock: realClock{},
		url:   manufacturerURL,
		index: map[string]string{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(catalog)
		}
	}
	return catalog, nil
}

// Load initializes the manufacturer catalog from cache or the remote source.
func (c *ManufacturerCatalog) Load(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(c.entries) > 0 {
		return nil
	}

	cacheDoc, cacheFresh, cacheFound, err := c.loadCache(ctx)
	if err != nil {
		return err
	}
	if cacheFound && cacheFresh {
		c.applyEntries(cacheDoc.Entries)
		return nil
	}

	entries, err := c.fetchRemote(ctx)
	if err == nil {
		c.applyEntries(entries)
		_ = c.saveCache(ctx, manufacturerCacheDocument{
			UpdatedAt: c.clock.Now().Unix(),
			Entries:   entries,
		})
		return nil
	}

	if cacheFound {
		c.applyEntries(cacheDoc.Entries)
		return nil
	}
	return err
}

// Close clears the in-memory manufacturer cache.
func (c *ManufacturerCatalog) Close() error {
	c.entries = nil
	c.index = map[string]string{}
	return nil
}

// Name resolves a short manufacturer code to a display name.
func (c *ManufacturerCatalog) Name(shortName string) string {
	if shortName == "" {
		return ""
	}
	if name, ok := c.index[shortName]; ok && name != "" {
		return name
	}
	return shortName
}

func (c *ManufacturerCatalog) loadCache(ctx context.Context) (manufacturerCacheDocument, bool, bool, error) {
	data, err := c.cache.LoadBytes(ctx, manufacturerDomain, manufacturerName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return manufacturerCacheDocument{}, false, false, nil
		}
		return manufacturerCacheDocument{}, false, false, err
	}
	var cacheDoc manufacturerCacheDocument
	if err := json.Unmarshal(data, &cacheDoc); err != nil {
		return manufacturerCacheDocument{}, false, false, Wrap(ErrInvalidResponse, "decode manufacturer cache", err)
	}
	cacheFresh := c.clock.Now().Unix()-cacheDoc.UpdatedAt < int64(manufacturerEffectiveTime.Seconds())
	return cacheDoc, cacheFresh, true, nil
}

func (c *ManufacturerCatalog) fetchRemote(ctx context.Context) ([]ManufacturerEntry, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("manufacturer catalog request failed: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var remote map[string]manufacturerRemoteEntry
	if err := json.Unmarshal(body, &remote); err != nil {
		return nil, Wrap(ErrInvalidResponse, "decode manufacturer catalog", err)
	}
	entries := make([]ManufacturerEntry, 0, len(remote))
	for code, entry := range remote {
		entries = append(entries, ManufacturerEntry{Code: code, Name: entry.Name})
	}
	slices.SortFunc(entries, func(a, b ManufacturerEntry) int {
		if a.Code < b.Code {
			return -1
		}
		if a.Code > b.Code {
			return 1
		}
		return 0
	})
	return entries, nil
}

func (c *ManufacturerCatalog) saveCache(ctx context.Context, cacheDoc manufacturerCacheDocument) error {
	data, err := json.Marshal(cacheDoc)
	if err != nil {
		return err
	}
	return c.cache.SaveBytes(ctx, manufacturerDomain, manufacturerName, data)
}

func (c *ManufacturerCatalog) applyEntries(entries []ManufacturerEntry) {
	c.entries = append([]ManufacturerEntry(nil), entries...)
	c.index = make(map[string]string, len(entries))
	for _, entry := range entries {
		c.index[entry.Code] = entry.Name
	}
}
