package miot

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	specFilterAsset = "specs/spec_filter.yaml"
	boolTransAsset  = "specs/bool_trans.yaml"
	specAddAsset    = "specs/spec_add.json"
	specModifyAsset = "specs/spec_modify.yaml"
)

// FilterRule describes a device-level filter override from spec_filter.yaml.
type FilterRule struct {
	DeviceURN  string
	Services   []string
	Properties []string
	Events     []string
	Actions    []string
}

// BoolTranslationLocale stores one language pair for boolean translations.
type BoolTranslationLocale struct {
	Language  string
	TrueText  string
	FalseText string
}

// BoolTranslation binds one property urn to a named boolean translation table.
type BoolTranslation struct {
	PropertyURN    string
	TranslationKey string
	Locales        []BoolTranslationLocale
}

// SpecPropertyDefinition is a typed property definition embedded in rules and additions.
type SpecPropertyDefinition struct {
	IID         int                 `json:"iid" yaml:"iid"`
	Type        string              `json:"type" yaml:"type"`
	Description string              `json:"description" yaml:"description"`
	Format      string              `json:"format,omitempty" yaml:"format,omitempty"`
	Access      []string            `json:"access,omitempty" yaml:"access,omitempty"`
	Unit        string              `json:"unit,omitempty" yaml:"unit,omitempty"`
	ValueRange  *ValueRange         `json:"value_range,omitempty" yaml:"value-range,omitempty"`
	ValueList   []SpecValueListItem `json:"value_list,omitempty" yaml:"value-list,omitempty"`
	Expr        string              `json:"expr,omitempty" yaml:"expr,omitempty"`
}

// SpecEventDefinition is a typed event definition embedded in rules and additions.
type SpecEventDefinition struct {
	IID         int    `json:"iid" yaml:"iid"`
	Type        string `json:"type" yaml:"type"`
	Description string `json:"description" yaml:"description"`
	Arguments   []int  `json:"arguments,omitempty" yaml:"arguments,omitempty"`
}

// SpecActionDefinition is a typed action definition embedded in rules and additions.
type SpecActionDefinition struct {
	IID         int    `json:"iid" yaml:"iid"`
	Type        string `json:"type" yaml:"type"`
	Description string `json:"description" yaml:"description"`
	In          []int  `json:"in,omitempty" yaml:"in,omitempty"`
	Out         []int  `json:"out,omitempty" yaml:"out,omitempty"`
}

// SpecServiceDefinition is a typed service definition embedded in rules and additions.
type SpecServiceDefinition struct {
	IID         int                      `json:"iid" yaml:"iid"`
	Type        string                   `json:"type" yaml:"type"`
	Description string                   `json:"description" yaml:"description"`
	Properties  []SpecPropertyDefinition `json:"properties,omitempty" yaml:"properties,omitempty"`
	Events      []SpecEventDefinition    `json:"events,omitempty" yaml:"events,omitempty"`
	Actions     []SpecActionDefinition   `json:"actions,omitempty" yaml:"actions,omitempty"`
}

// SpecAddition describes extra service definitions injected for a device URN.
type SpecAddition struct {
	DeviceURN string
	Services  []SpecServiceDefinition
}

// SpecElementKind identifies the modified spec element category.
type SpecElementKind string

const (
	// SpecElementKindProperty reports a property patch.
	SpecElementKindProperty SpecElementKind = "property"
	// SpecElementKindService reports a service patch.
	SpecElementKindService SpecElementKind = "service"
	// SpecElementKindEvent reports an event patch.
	SpecElementKindEvent SpecElementKind = "event"
	// SpecElementKindAction reports an action patch.
	SpecElementKindAction SpecElementKind = "action"
)

// SpecElementPatch stores one typed patch entry from spec_modify.yaml.
type SpecElementPatch struct {
	Kind       SpecElementKind
	Key        string
	Name       string
	Format     string
	Access     []string
	Unit       string
	ValueRange *ValueRange
	ValueList  []SpecValueListItem
	Expr       string
	Icon       string
}

// SpecModification stores all patches and aliases for one device URN.
type SpecModification struct {
	DeviceURN string
	AliasURN  string
	Patches   []SpecElementPatch
}

// SpecRules bundles all embedded rule assets used by the spec parser.
type SpecRules struct {
	Filters          []FilterRule
	BoolTranslations []BoolTranslation
	Additions        []SpecAddition
	Modifications    []SpecModification
}

// LoadSpecRules loads and decodes all embedded spec rule assets.
func LoadSpecRules() (SpecRules, error) {
	filters, err := loadFilterRules()
	if err != nil {
		return SpecRules{}, err
	}
	boolTranslations, err := loadBoolTranslations()
	if err != nil {
		return SpecRules{}, err
	}
	additions, err := loadSpecAdditions()
	if err != nil {
		return SpecRules{}, err
	}
	modifications, err := loadSpecModifications()
	if err != nil {
		return SpecRules{}, err
	}
	return SpecRules{
		Filters:          filters,
		BoolTranslations: boolTranslations,
		Additions:        additions,
		Modifications:    modifications,
	}, nil
}

type rawFilterRule struct {
	Services   []string `yaml:"services"`
	Properties []string `yaml:"properties"`
	Events     []string `yaml:"events"`
	Actions    []string `yaml:"actions"`
}

func loadFilterRules() ([]FilterRule, error) {
	data, err := embeddedAssets.ReadFile(specFilterAsset)
	if err != nil {
		return nil, err
	}
	var raw map[string]rawFilterRule
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	urns := sortedStringKeys(raw)
	rules := make([]FilterRule, 0, len(raw))
	for _, urn := range urns {
		item := raw[urn]
		rules = append(rules, FilterRule{
			DeviceURN:  urn,
			Services:   append([]string(nil), item.Services...),
			Properties: append([]string(nil), item.Properties...),
			Events:     append([]string(nil), item.Events...),
			Actions:    append([]string(nil), item.Actions...),
		})
	}
	return rules, nil
}

type rawBoolTranslations struct {
	Data      map[string]string                       `yaml:"data"`
	Translate map[string]map[string]map[string]string `yaml:"translate"`
}

func loadBoolTranslations() ([]BoolTranslation, error) {
	data, err := embeddedAssets.ReadFile(boolTransAsset)
	if err != nil {
		return nil, err
	}
	var raw rawBoolTranslations
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	propertyURNs := sortedStringKeys(raw.Data)
	items := make([]BoolTranslation, 0, len(propertyURNs))
	for _, propertyURN := range propertyURNs {
		key := raw.Data[propertyURN]
		localeMap := raw.Translate[key]
		languages := sortedStringKeys(localeMap)
		locales := make([]BoolTranslationLocale, 0, len(languages))
		for _, language := range languages {
			pair := localeMap[language]
			locales = append(locales, BoolTranslationLocale{
				Language:  language,
				FalseText: pair["false"],
				TrueText:  pair["true"],
			})
		}
		items = append(items, BoolTranslation{
			PropertyURN:    propertyURN,
			TranslationKey: key,
			Locales:        locales,
		})
	}
	return items, nil
}

func loadSpecAdditions() ([]SpecAddition, error) {
	data, err := embeddedAssets.ReadFile(specAddAsset)
	if err != nil {
		return nil, err
	}
	var raw map[string][]SpecServiceDefinition
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	urns := sortedStringKeys(raw)
	items := make([]SpecAddition, 0, len(urns))
	for _, urn := range urns {
		addition := SpecAddition{DeviceURN: urn}
		for _, service := range raw[urn] {
			normalized, err := normalizeServiceDefinition(service)
			if err != nil {
				return nil, fmt.Errorf("normalize service addition %s: %w", urn, err)
			}
			addition.Services = append(addition.Services, normalized)
		}
		items = append(items, addition)
	}
	return items, nil
}

func loadSpecModifications() ([]SpecModification, error) {
	data, err := embeddedAssets.ReadFile(specModifyAsset)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if len(root.Content) == 0 {
		return nil, nil
	}
	top := root.Content[0]
	items := make([]SpecModification, 0, len(top.Content)/2)
	for i := 0; i < len(top.Content); i += 2 {
		keyNode := top.Content[i]
		valueNode := top.Content[i+1]
		item := SpecModification{DeviceURN: keyNode.Value}
		switch valueNode.Kind {
		case yaml.ScalarNode:
			item.AliasURN = strings.TrimSpace(valueNode.Value)
		case yaml.MappingNode:
			patches, err := decodeSpecPatches(valueNode)
			if err != nil {
				return nil, fmt.Errorf("decode modification %s: %w", item.DeviceURN, err)
			}
			item.Patches = patches
		default:
			return nil, fmt.Errorf("unsupported modification node kind %d for %s", valueNode.Kind, item.DeviceURN)
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeviceURN < items[j].DeviceURN })
	return items, nil
}

type rawSpecElementPatch struct {
	Name       string                   `yaml:"name"`
	Format     string                   `yaml:"format"`
	Access     []string                 `yaml:"access"`
	Unit       string                   `yaml:"unit"`
	ValueRange []float64                `yaml:"value-range"`
	ValueList  []SpecValueListItemInput `yaml:"value-list"`
	Expr       string                   `yaml:"expr"`
	Icon       string                   `yaml:"icon"`
}

func decodeSpecPatches(node *yaml.Node) ([]SpecElementPatch, error) {
	var patches []SpecElementPatch
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		valueNode := node.Content[i+1]
		var rawPatch rawSpecElementPatch
		if err := valueNode.Decode(&rawPatch); err != nil {
			return nil, err
		}
		kind, logicalKey, err := parseSpecPatchKey(key)
		if err != nil {
			return nil, err
		}
		patch := SpecElementPatch{
			Kind:   kind,
			Key:    logicalKey,
			Name:   rawPatch.Name,
			Format: rawPatch.Format,
			Access: append([]string(nil), rawPatch.Access...),
			Unit:   rawPatch.Unit,
			Expr:   rawPatch.Expr,
			Icon:   rawPatch.Icon,
		}
		if len(rawPatch.ValueRange) > 0 {
			valueRange, err := NewValueRangeFromSpec(rawPatch.ValueRange)
			if err != nil {
				return nil, err
			}
			patch.ValueRange = &valueRange
		}
		if len(rawPatch.ValueList) > 0 {
			valueList, err := NewValueListFromSpec(rawPatch.ValueList)
			if err != nil {
				return nil, err
			}
			patch.ValueList = valueList.Items
		}
		patches = append(patches, patch)
	}
	sort.Slice(patches, func(i, j int) bool {
		if patches[i].Kind == patches[j].Kind {
			return patches[i].Key < patches[j].Key
		}
		return patches[i].Kind < patches[j].Kind
	})
	return patches, nil
}

func parseSpecPatchKey(key string) (SpecElementKind, string, error) {
	switch {
	case strings.HasPrefix(key, "prop."):
		return SpecElementKindProperty, strings.TrimPrefix(key, "prop."), nil
	case strings.HasPrefix(key, "service."):
		return SpecElementKindService, strings.TrimPrefix(key, "service."), nil
	case strings.HasPrefix(key, "event."):
		return SpecElementKindEvent, strings.TrimPrefix(key, "event."), nil
	case strings.HasPrefix(key, "action."):
		return SpecElementKindAction, strings.TrimPrefix(key, "action."), nil
	default:
		return "", "", fmt.Errorf("unsupported patch key %q", key)
	}
}

func normalizeServiceDefinition(service SpecServiceDefinition) (SpecServiceDefinition, error) {
	properties := make([]SpecPropertyDefinition, 0, len(service.Properties))
	for _, property := range service.Properties {
		normalized, err := normalizePropertyDefinition(property)
		if err != nil {
			return SpecServiceDefinition{}, err
		}
		properties = append(properties, normalized)
	}
	events := append([]SpecEventDefinition(nil), service.Events...)
	actions := make([]SpecActionDefinition, 0, len(service.Actions))
	for _, action := range service.Actions {
		actions = append(actions, SpecActionDefinition{
			IID:         action.IID,
			Type:        action.Type,
			Description: action.Description,
			In:          append([]int(nil), action.In...),
			Out:         append([]int(nil), action.Out...),
		})
	}
	return SpecServiceDefinition{
		IID:         service.IID,
		Type:        service.Type,
		Description: service.Description,
		Properties:  properties,
		Events:      events,
		Actions:     actions,
	}, nil
}

func normalizePropertyDefinition(property SpecPropertyDefinition) (SpecPropertyDefinition, error) {
	out := property
	if len(property.ValueList) > 0 {
		inputs := make([]SpecValueListItemInput, 0, len(property.ValueList))
		for _, item := range property.ValueList {
			inputs = append(inputs, SpecValueListItemInput{
				Name:        item.Name,
				Value:       item.Value,
				Description: item.Description,
			})
		}
		valueList, err := NewValueListFromSpec(inputs)
		if err != nil {
			return SpecPropertyDefinition{}, err
		}
		out.ValueList = valueList.Items
	}
	return out, nil
}

func sortedStringKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
