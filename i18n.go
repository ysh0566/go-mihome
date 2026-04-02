package miot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

const defaultI18nLanguage = "en"

// TranslationReplacement replaces one placeholder in a translated string.
type TranslationReplacement struct {
	Key   string
	Value string
}

// I18nCatalog resolves flattened translation keys from embedded language files.
type I18nCatalog struct {
	lang  string
	items map[string]string
}

// NewI18nCatalog loads an embedded translation catalog for one language.
func NewI18nCatalog(lang string) (*I18nCatalog, error) {
	if lang == "" {
		lang = defaultI18nLanguage
	}
	items, err := loadI18nItems(lang)
	if err != nil && lang != defaultI18nLanguage {
		items, err = loadI18nItems(defaultI18nLanguage)
	}
	if err != nil {
		return nil, err
	}
	return &I18nCatalog{
		lang:  lang,
		items: items,
	}, nil
}

// Close releases the in-memory translation data.
func (c *I18nCatalog) Close() error {
	c.items = map[string]string{}
	return nil
}

// Translate resolves a dotted translation key and applies string replacements.
func (c *I18nCatalog) Translate(key string, replacements ...TranslationReplacement) (string, bool) {
	if c == nil || key == "" {
		return "", false
	}
	value, ok := c.items[key]
	if !ok {
		return "", false
	}
	for _, replacement := range replacements {
		value = strings.ReplaceAll(value, "{"+replacement.Key+"}", replacement.Value)
	}
	return value, true
}

func loadI18nItems(lang string) (map[string]string, error) {
	data, err := embeddedAssets.ReadFile(path.Join("i18n", lang+".json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("load i18n %s: %w", lang, fs.ErrNotExist)
		}
		return nil, err
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, Wrap(ErrInvalidResponse, "decode i18n catalog", err)
	}
	items := make(map[string]string)
	for key, raw := range root {
		if err := flattenI18n(items, key, raw); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func flattenI18n(dst map[string]string, prefix string, raw json.RawMessage) error {
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		dst[prefix] = value
		return nil
	}

	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return Wrap(ErrInvalidResponse, "flatten i18n catalog", err)
	}
	for key, next := range nested {
		childKey := prefix + "." + key
		if err := flattenI18n(dst, childKey, next); err != nil {
			return err
		}
	}
	return nil
}
