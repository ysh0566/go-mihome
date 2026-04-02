package miot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	storageDirectoryPerm = 0o755
	storageFilePerm      = 0o644
	storageHashSize      = sha256.Size
	userConfigDomain     = "miot_config"
)

// StorageFormat identifies a persisted payload encoding.
type StorageFormat string

const (
	// StorageFormatBytes stores a hashed raw byte payload.
	StorageFormatBytes StorageFormat = "bytes"
	// StorageFormatText stores a hashed UTF-8 text payload.
	StorageFormatText StorageFormat = "text"
	// StorageFormatJSON stores a hashed JSON payload.
	StorageFormatJSON StorageFormat = "json"
)

// StorageOption configures a Storage instance.
type StorageOption func(*Storage)

// Storage persists typed payloads under a root directory.
type Storage struct {
	root string
	fs   FileStore
}

// UserConfigDocument stores user configuration entries keyed by logical name.
type UserConfigDocument struct {
	Entries []UserConfigEntry `json:"entries"`
}

// UserConfigEntry stores one typed user configuration value.
type UserConfigEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// WithFileStore installs a custom filesystem backend for Storage.
func WithFileStore(store FileStore) StorageOption {
	return func(s *Storage) {
		if store != nil {
			s.fs = store
		}
	}
}

// NewStorage creates a typed storage rooted at the provided path.
func NewStorage(root string, opts ...StorageOption) (*Storage, error) {
	if root == "" {
		return nil, &Error{Code: ErrInvalidArgument, Op: "new storage", Msg: "root path is empty"}
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, Wrap(ErrInvalidArgument, "resolve storage root", err)
	}

	store := &Storage{
		root: absRoot,
		fs:   osFileStore{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(store)
		}
	}
	if err := store.fs.MkdirAll(absRoot, storageDirectoryPerm); err != nil {
		return nil, fmt.Errorf("create storage root: %w", err)
	}
	return store, nil
}

// SaveBytes writes a hashed byte payload.
func (s *Storage) SaveBytes(ctx context.Context, domain, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.writeHashed(domain, name, StorageFormatBytes, data)
}

// LoadBytes reads and verifies a hashed byte payload.
func (s *Storage) LoadBytes(ctx context.Context, domain, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.readHashed(domain, name, StorageFormatBytes)
}

// SaveText writes a hashed UTF-8 text payload.
func (s *Storage) SaveText(ctx context.Context, domain, name, data string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.writeHashed(domain, name, StorageFormatText, []byte(data))
}

// LoadText reads and verifies a hashed UTF-8 text payload.
func (s *Storage) LoadText(ctx context.Context, domain, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	data, err := s.readHashed(domain, name, StorageFormatText)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SaveJSON marshals and writes a hashed JSON payload.
func SaveJSON[T any](ctx context.Context, store *Storage, domain, name string, v T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if store == nil {
		return &Error{Code: ErrInvalidArgument, Op: "save json", Msg: "storage is nil"}
	}
	data, err := json.Marshal(v)
	if err != nil {
		return Wrap(ErrInvalidArgument, "marshal json", err)
	}
	return store.writeHashed(domain, name, StorageFormatJSON, data)
}

// LoadJSON reads, verifies, and unmarshals a hashed JSON payload.
func LoadJSON[T any](ctx context.Context, store *Storage, domain, name string) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if store == nil {
		return zero, &Error{Code: ErrInvalidArgument, Op: "load json", Msg: "storage is nil"}
	}
	data, err := store.readHashed(domain, name, StorageFormatJSON)
	if err != nil {
		return zero, err
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		return zero, Wrap(ErrInvalidResponse, "decode json", err)
	}
	return out, nil
}

// Remove deletes a typed payload file if it exists.
func (s *Storage) Remove(ctx context.Context, domain, name string, format StorageFormat) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStorageTarget(domain, name, format); err != nil {
		return err
	}
	err := s.fs.Remove(s.Path(domain, name, format))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// RemoveDomain deletes a whole storage domain if it exists.
func (s *Storage) RemoveDomain(ctx context.Context, domain string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateStorageDomain(domain); err != nil {
		return err
	}
	err := s.fs.RemoveAll(filepath.Join(s.root, domain))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Names lists typed payload names under a domain.
func (s *Storage) Names(ctx context.Context, domain string, format StorageFormat) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateStorageDomain(domain); err != nil {
		return nil, err
	}
	if err := validateStorageFormat(format); err != nil {
		return nil, err
	}
	entries, err := s.fs.ReadDir(filepath.Join(s.root, domain))
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}

	suffix := "." + string(format)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, suffix) {
			names = append(names, strings.TrimSuffix(name, suffix))
		}
	}
	slices.Sort(names)
	return names, nil
}

// Exists reports whether a typed payload file exists.
func (s *Storage) Exists(ctx context.Context, domain, name string, format StorageFormat) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := validateStorageTarget(domain, name, format); err != nil {
		return false, err
	}
	_, err := s.fs.Stat(s.Path(domain, name, format))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Clear removes every file and directory stored under the root path.
func (s *Storage) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := s.fs.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		fullPath := filepath.Join(s.root, entry.Name())
		if entry.IsDir() {
			if err := s.fs.RemoveAll(fullPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			continue
		}
		if err := s.fs.Remove(fullPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Path returns the absolute file path for a typed payload.
func (s *Storage) Path(domain, name string, format StorageFormat) string {
	return filepath.Join(s.root, domain, fmt.Sprintf("%s.%s", name, format))
}

// RootPath returns the absolute root directory used by Storage.
func (s *Storage) RootPath() string {
	return s.root
}

// RawFiles exposes the backing raw filesystem for package-level adapters.
func (s *Storage) RawFiles() FileStore {
	return s.fs
}

// NewUserConfigEntry encodes a typed user configuration value.
func NewUserConfigEntry[T any](key string, value T) (UserConfigEntry, error) {
	if key == "" {
		return UserConfigEntry{}, &Error{Code: ErrInvalidArgument, Op: "new user config entry", Msg: "key is empty"}
	}
	data, err := json.Marshal(value)
	if err != nil {
		return UserConfigEntry{}, Wrap(ErrInvalidArgument, "marshal user config entry", err)
	}
	return UserConfigEntry{
		Key:   key,
		Value: json.RawMessage(data),
	}, nil
}

// DecodeUserConfigEntry decodes a typed user configuration value.
func DecodeUserConfigEntry[T any](entry UserConfigEntry) (T, error) {
	var out T
	if entry.Key == "" {
		return out, &Error{Code: ErrInvalidArgument, Op: "decode user config entry", Msg: "key is empty"}
	}
	if err := json.Unmarshal(entry.Value, &out); err != nil {
		return out, Wrap(ErrInvalidResponse, "decode user config entry", err)
	}
	return out, nil
}

// UpdateUserConfig updates, replaces, or removes a user configuration document.
func (s *Storage) UpdateUserConfig(ctx context.Context, uid, cloudServer string, patch *UserConfigDocument, replace bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	domain, name, err := userConfigLocation(uid, cloudServer)
	if err != nil {
		return err
	}
	if patch == nil {
		return s.Remove(ctx, domain, name, StorageFormatJSON)
	}
	if len(patch.Entries) == 0 && !replace {
		return nil
	}
	if replace {
		return SaveJSON(ctx, s, domain, name, patch)
	}

	current, err := s.LoadUserConfig(ctx, uid, cloudServer)
	if err != nil {
		return err
	}
	merged := mergeUserConfigDocuments(current, *patch)
	return SaveJSON(ctx, s, domain, name, merged)
}

// LoadUserConfig loads a user configuration document or selected entries.
func (s *Storage) LoadUserConfig(ctx context.Context, uid, cloudServer string, keys ...string) (UserConfigDocument, error) {
	if err := ctx.Err(); err != nil {
		return UserConfigDocument{}, err
	}
	domain, name, err := userConfigLocation(uid, cloudServer)
	if err != nil {
		return UserConfigDocument{}, err
	}
	doc, err := LoadJSON[UserConfigDocument](ctx, s, domain, name)
	if errors.Is(err, fs.ErrNotExist) {
		return UserConfigDocument{}, nil
	}
	if err != nil {
		return UserConfigDocument{}, err
	}
	if len(keys) == 0 {
		return doc, nil
	}
	return filterUserConfigDocument(doc, keys), nil
}

func (s *Storage) writeHashed(domain, name string, format StorageFormat, payload []byte) error {
	if err := validateStorageTarget(domain, name, format); err != nil {
		return err
	}
	fullPath := s.Path(domain, name, format)
	if err := s.fs.MkdirAll(filepath.Dir(fullPath), storageDirectoryPerm); err != nil {
		return err
	}
	sum := sha256.Sum256(payload)
	data := make([]byte, 0, len(payload)+len(sum))
	data = append(data, payload...)
	data = append(data, sum[:]...)
	return s.fs.WriteFile(fullPath, data, storageFilePerm)
}

func (s *Storage) readHashed(domain, name string, format StorageFormat) ([]byte, error) {
	if err := validateStorageTarget(domain, name, format); err != nil {
		return nil, err
	}
	data, err := s.fs.ReadFile(s.Path(domain, name, format))
	if err != nil {
		return nil, err
	}
	if len(data) < storageHashSize {
		return nil, &Error{Code: ErrInvalidResponse, Op: "load storage payload", Msg: "payload is too short"}
	}
	payload := data[:len(data)-storageHashSize]
	wantHash := data[len(data)-storageHashSize:]
	sum := sha256.Sum256(payload)
	if !slices.Equal(sum[:], wantHash) {
		return nil, &Error{Code: ErrInvalidResponse, Op: "load storage payload", Msg: "payload hash mismatch"}
	}
	return payload, nil
}

func validateStorageTarget(domain, name string, format StorageFormat) error {
	if err := validateStorageDomain(domain); err != nil {
		return err
	}
	if name == "" {
		return &Error{Code: ErrInvalidArgument, Op: "validate storage target", Msg: "name is empty"}
	}
	return validateStorageFormat(format)
}

func validateStorageDomain(domain string) error {
	if domain == "" {
		return &Error{Code: ErrInvalidArgument, Op: "validate storage domain", Msg: "domain is empty"}
	}
	return nil
}

func validateStorageFormat(format StorageFormat) error {
	switch format {
	case StorageFormatBytes, StorageFormatText, StorageFormatJSON:
		return nil
	default:
		return &Error{Code: ErrInvalidArgument, Op: "validate storage format", Msg: "invalid storage format"}
	}
}

func userConfigLocation(uid, cloudServer string) (string, string, error) {
	if uid == "" {
		return "", "", &Error{Code: ErrInvalidArgument, Op: "user config path", Msg: "uid is empty"}
	}
	if cloudServer == "" {
		return "", "", &Error{Code: ErrInvalidArgument, Op: "user config path", Msg: "cloud server is empty"}
	}
	return userConfigDomain, uid + "_" + cloudServer, nil
}

func mergeUserConfigDocuments(current, patch UserConfigDocument) UserConfigDocument {
	if len(current.Entries) == 0 {
		return patch
	}

	entries := append([]UserConfigEntry(nil), current.Entries...)
	indexByKey := make(map[string]int, len(entries))
	for i, entry := range entries {
		indexByKey[entry.Key] = i
	}
	for _, entry := range patch.Entries {
		if idx, ok := indexByKey[entry.Key]; ok {
			entries[idx] = entry
			continue
		}
		indexByKey[entry.Key] = len(entries)
		entries = append(entries, entry)
	}
	return UserConfigDocument{Entries: entries}
}

func filterUserConfigDocument(doc UserConfigDocument, keys []string) UserConfigDocument {
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	filtered := UserConfigDocument{
		Entries: make([]UserConfigEntry, 0, len(doc.Entries)),
	}
	for _, entry := range doc.Entries {
		if _, ok := allowed[entry.Key]; ok {
			filtered.Entries = append(filtered.Entries, entry)
		}
	}
	return filtered
}

type osFileStore struct{}

func (osFileStore) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (osFileStore) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (osFileStore) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (osFileStore) Remove(name string) error {
	return os.Remove(name)
}

func (osFileStore) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (osFileStore) ReadDir(name string) ([]fs.DirEntry, error) {
	return os.ReadDir(name)
}

func (osFileStore) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}
