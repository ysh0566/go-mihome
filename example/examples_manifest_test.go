package example

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExampleProgramsExist(t *testing.T) {
	t.Parallel()

	required := []string{
		filepath.Join("cloud_profile", "main.go"),
		filepath.Join("cloud_homes", "main.go"),
		filepath.Join("cloud_props", "main.go"),
		filepath.Join("cloud_state_cache", "main.go"),
		filepath.Join("camera_preview", "main.go"),
		filepath.Join("spec_parse", "main.go"),
		filepath.Join("entity_build", "main.go"),
		filepath.Join("mdns_discovery", "main.go"),
		filepath.Join("mips_cloud", "main.go"),
		filepath.Join("lan_control", "main.go"),
		filepath.Join("miot_client_runtime", "main.go"),
		filepath.Join("oauth_token", "main.go"),
	}

	for _, path := range required {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			if _, err := os.Stat(path); err != nil {
				t.Fatalf("example program %q must exist: %v", path, err)
			}
		})
	}
}
