package miot

import "testing"

func TestRuleLoaderReadsSpecFilter(t *testing.T) {
	rules, err := LoadSpecRules()
	if err != nil {
		t.Fatalf("LoadSpecRules() error = %v", err)
	}
	if len(rules.Filters) == 0 {
		t.Fatal("expected filter rules")
	}
	first := rules.Filters[0]
	if first.DeviceURN == "" {
		t.Fatalf("first filter = %#v", first)
	}
	if len(rules.BoolTranslations) == 0 {
		t.Fatal("expected bool translations")
	}
	if len(rules.Additions) == 0 {
		t.Fatal("expected additions")
	}
	if len(rules.Modifications) == 0 {
		t.Fatal("expected modifications")
	}
}
