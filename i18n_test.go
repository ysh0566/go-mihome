package miot

import "testing"

func TestI18nCatalogLoadsLanguage(t *testing.T) {
	catalog, err := NewI18nCatalog("zh-Hans")
	if err != nil {
		t.Fatal(err)
	}

	got, ok := catalog.Translate("error.common.-10000")
	if !ok || got != "未知错误" {
		t.Fatalf("Translate() = %q, %t", got, ok)
	}
}

func TestI18nCatalogMissingKey(t *testing.T) {
	catalog, err := NewI18nCatalog("en")
	if err != nil {
		t.Fatal(err)
	}

	if got, ok := catalog.Translate("missing.translation.key"); ok || got != "" {
		t.Fatalf("Translate() = %q, %t, want empty result and false", got, ok)
	}
}
