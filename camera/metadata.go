package camera

import (
	_ "embed"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed metadata.yaml
var metadataYAML []byte

type supportMetadata struct {
	SupportClasses []string                    `yaml:"support_classes"`
	ExtraInfo      map[string]supportInfoEntry `yaml:"extra_info"`
	Blacklist      []string                    `yaml:"blacklist"`
}

type supportInfoEntry struct {
	ChannelCount int    `yaml:"channel_count"`
	Name         string `yaml:"name"`
	Vendor       string `yaml:"vendor"`
}

var (
	metadataOnce sync.Once
	metadataData supportMetadata
	metadataErr  error
)

// LookupSupport reports whether the provided model is a supported Xiaomi camera.
func LookupSupport(model string) (SupportInfo, bool, error) {
	metadata, err := loadMetadata()
	if err != nil {
		return SupportInfo{}, false, err
	}
	return lookupSupport(model, metadata), isSupportedModel(model, metadata), nil
}

func loadMetadata() (supportMetadata, error) {
	metadataOnce.Do(func() {
		metadataErr = yaml.Unmarshal(metadataYAML, &metadataData)
	})
	return metadataData, metadataErr
}

func isSupportedModel(model string, metadata supportMetadata) bool {
	_, ok := lookupSupportInfo(model, metadata)
	return ok
}

func lookupSupport(model string, metadata supportMetadata) SupportInfo {
	info, _ := lookupSupportInfo(model, metadata)
	return info
}

func lookupSupportInfo(model string, metadata supportMetadata) (SupportInfo, bool) {
	model = strings.TrimSpace(model)
	if model == "" {
		return SupportInfo{}, false
	}
	for _, entry := range metadata.Blacklist {
		if model == strings.TrimSpace(entry) {
			return SupportInfo{}, false
		}
	}
	parts := strings.Split(model, ".")
	if len(parts) < 2 {
		return SupportInfo{}, false
	}
	supported := false
	for _, className := range metadata.SupportClasses {
		if strings.EqualFold(strings.TrimSpace(className), parts[1]) {
			supported = true
			break
		}
	}
	if !supported {
		return SupportInfo{}, false
	}
	item, ok := metadata.ExtraInfo[model]
	if !ok {
		return SupportInfo{ChannelCount: 1}, true
	}
	if item.ChannelCount <= 0 {
		item.ChannelCount = 1
	}
	return SupportInfo{
		ChannelCount: item.ChannelCount,
		Name:         item.Name,
		Vendor:       item.Vendor,
	}, true
}
