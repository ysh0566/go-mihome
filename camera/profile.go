package camera

import "strings"

var defaultProfiles = []Profile{
	{
		Name:        "mijia.camera.family",
		Prefixes:    []string{"mijia.camera", "xiaomi.camera"},
		ExactModels: []string{"mijia.camera.aw200"},
	},
	{
		Name:     "chuangmi.camera.family",
		Prefixes: []string{"chuangmi.camera"},
	},
}

var genericProfile = Profile{
	Name: "generic",
}

// MatchProfile returns the best built-in camera probe profile for the model.
func MatchProfile(model string) Profile {
	model = strings.TrimSpace(model)
	if model == "" {
		return genericProfile
	}
	for _, profile := range defaultProfiles {
		if profile.matchesExact(model) {
			return profile
		}
	}
	for _, profile := range defaultProfiles {
		if profile.matchesPrefix(model) {
			return profile
		}
	}
	return genericProfile
}

func (profile Profile) matchesExact(model string) bool {
	if strings.TrimSpace(model) == "" {
		return false
	}
	for _, exact := range profile.ExactModels {
		if model == strings.TrimSpace(exact) {
			return true
		}
	}
	return false
}

func (profile Profile) matchesPrefix(model string) bool {
	if strings.TrimSpace(model) == "" {
		return false
	}
	for _, prefix := range profile.Prefixes {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if model == prefix || strings.HasPrefix(model, prefix+".") {
			return true
		}
	}
	return false
}
