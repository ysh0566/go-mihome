package exampleutil

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"
)

var runtimeCloudMIPSUUIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// NormalizeRuntimeCloudMIPSUUID preserves Python-compatible UUIDs and rotates legacy/example formats.
func NormalizeRuntimeCloudMIPSUUID(existing string) (string, bool, error) {
	existing = strings.TrimSpace(existing)
	if runtimeCloudMIPSUUIDPattern.MatchString(existing) {
		return existing, false, nil
	}

	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(buf[:]), true, nil
}
