package miot

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
	"unicode"
)

// CalcGroupID returns the first 16 hexadecimal characters of the MIoT group hash.
func CalcGroupID(uid, homeID string) string {
	sum := sha1.Sum([]byte(uid + "central_service" + homeID))
	return hex.EncodeToString(sum[:])[:16]
}

// SlugifyName converts a name into a lowercase underscore-separated slug.
func SlugifyName(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	b.Grow(len(name))
	lastUnderscore := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// SlugifyDID converts a cloud server and device ID into a stable slug.
func SlugifyDID(cloudServer, did string) string {
	return SlugifyName(cloudServer + "_" + did)
}

// MatchMQTTTopic reports whether a topic matches an MQTT wildcard pattern.
func MatchMQTTTopic(pattern, topic string) bool {
	patternParts := strings.Split(pattern, "/")
	topicParts := strings.Split(topic, "/")

	for i := 0; i < len(patternParts); i++ {
		if i >= len(topicParts) {
			return patternParts[i] == "#"
		}

		switch patternParts[i] {
		case "#":
			return true
		case "+":
			continue
		default:
			if patternParts[i] != topicParts[i] {
				return false
			}
		}
	}
	return len(patternParts) == len(topicParts)
}
