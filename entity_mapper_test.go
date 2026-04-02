package miot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func mustLoadSpecFixture(t *testing.T, relPath string) SpecInstance {
	t.Helper()

	data, err := os.ReadFile(filepath.Clean(relPath))
	if err != nil {
		t.Fatal(err)
	}
	var spec SpecInstance
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}
