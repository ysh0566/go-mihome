package miot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	specCacheDomain       = "miot_specs"
	specInstanceEndpoint  = "https://miot-spec.org/miot-spec-v2/instance"
	specMultiLangEndpoint = "https://miot-spec.org/instance/v2/multiLanguage"
	defaultSpecLanguage   = "en"
)

// SpecParserOption configures a SpecParser.
type SpecParserOption func(*SpecParser)

// SpecParser fetches, normalizes, caches, and refreshes MIoT spec instances.
type SpecParser struct {
	http              HTTPDoer
	cache             ByteStore
	rules             SpecRules
	language          string
	instanceEndpoint  string
	multiLangEndpoint string
	localMultiLang    map[string]map[string]map[string]string
}

// WithSpecHTTPClient injects a custom HTTP client into SpecParser.
func WithSpecHTTPClient(client HTTPDoer) SpecParserOption {
	return func(p *SpecParser) {
		if client != nil {
			p.http = client
		}
	}
}

// WithSpecRules injects a preloaded rule set into SpecParser.
func WithSpecRules(rules SpecRules) SpecParserOption {
	return func(p *SpecParser) {
		p.rules = rules
	}
}

// NewSpecParser creates a MIoT spec parser.
func NewSpecParser(language string, cache ByteStore, opts ...SpecParserOption) (*SpecParser, error) {
	if language == "" {
		language = defaultSpecLanguage
	}
	rules, err := LoadSpecRules()
	if err != nil {
		return nil, err
	}
	localMultiLang, err := loadEmbeddedMultiLang()
	if err != nil {
		return nil, err
	}
	parser := &SpecParser{
		http:              &http.Client{},
		cache:             cache,
		rules:             rules,
		language:          language,
		instanceEndpoint:  specInstanceEndpoint,
		multiLangEndpoint: specMultiLangEndpoint,
		localMultiLang:    localMultiLang,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(parser)
		}
	}
	if parser.http == nil {
		parser.http = &http.Client{}
	}
	return parser, nil
}

// Close releases parser resources.
func (p *SpecParser) Close() error {
	return nil
}

// Parse loads one MIoT spec instance, optionally from cache.
func (p *SpecParser) Parse(ctx context.Context, urn string) (SpecInstance, error) {
	return p.parse(ctx, urn, false)
}

// Refresh reparses the provided URNs and updates the cache.
func (p *SpecParser) Refresh(ctx context.Context, urns []string) (int, error) {
	successes := 0
	for _, urn := range urns {
		if urn == "" {
			continue
		}
		if _, err := p.parse(ctx, urn, true); err != nil {
			return successes, err
		}
		successes++
	}
	return successes, nil
}

func (p *SpecParser) parse(ctx context.Context, urn string, skipCache bool) (SpecInstance, error) {
	if urn == "" {
		return SpecInstance{}, &Error{Code: ErrInvalidArgument, Op: "parse spec", Msg: "urn is empty"}
	}
	if !skipCache {
		if cached, ok, err := p.loadCachedSpec(ctx, urn); err != nil {
			return SpecInstance{}, err
		} else if ok {
			return cached, nil
		}
	}

	raw, err := p.fetchInstance(ctx, urn)
	if err != nil {
		return SpecInstance{}, err
	}
	spec, err := p.normalizeInstance(ctx, urn, raw)
	if err != nil {
		return SpecInstance{}, err
	}
	if err := p.saveCachedSpec(ctx, urn, spec); err != nil {
		return SpecInstance{}, err
	}
	return spec, nil
}

type rawSpecInstance struct {
	Type        string                  `json:"type"`
	Description string                  `json:"description"`
	Services    []SpecServiceDefinition `json:"services"`
}

func (p *SpecParser) fetchInstance(ctx context.Context, urn string) (rawSpecInstance, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.instanceEndpoint, nil)
	if err != nil {
		return rawSpecInstance{}, err
	}
	query := req.URL.Query()
	query.Set("type", urn)
	req.URL.RawQuery = query.Encode()
	resp, err := p.http.Do(req)
	if err != nil {
		return rawSpecInstance{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return rawSpecInstance{}, fmt.Errorf("spec instance request failed: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return rawSpecInstance{}, err
	}
	var instance rawSpecInstance
	if err := json.Unmarshal(body, &instance); err != nil {
		return rawSpecInstance{}, Wrap(ErrInvalidResponse, "decode spec instance", err)
	}
	if instance.Type == "" || instance.Description == "" {
		return rawSpecInstance{}, &Error{Code: ErrInvalidResponse, Op: "fetch spec instance", Msg: "invalid instance payload"}
	}
	return instance, nil
}

func (p *SpecParser) normalizeInstance(ctx context.Context, urn string, raw rawSpecInstance) (SpecInstance, error) {
	baseURN := normalizeDeviceURN(urn)
	translations := p.loadTranslations(ctx, urn)
	addition := p.findAddition(urn)
	filter := p.findFilter(baseURN)
	modification := p.findModification(urn)

	services := make([]SpecServiceDefinition, 0, len(raw.Services)+len(addition.Services))
	services = append(services, raw.Services...)
	services = append(services, addition.Services...)

	spec := SpecInstance{
		URN:              urn,
		Name:             urnSegment(urn, 3),
		Description:      raw.Description,
		DescriptionTrans: raw.Description,
		Services:         []SpecService{},
	}
	if translated := translations["device"]; translated != "" {
		spec.DescriptionTrans = translated
	}

	originalCount := len(raw.Services)
	for idx, serviceDef := range services {
		added := idx >= originalCount
		serviceTypeName := urnSegment(serviceDef.Type, 3)
		if serviceTypeName == "device-information" {
			continue
		}
		if !added && serviceFiltered(filter, serviceDef.IID) {
			continue
		}
		service := SpecService{
			IID:              serviceDef.IID,
			Type:             serviceDef.Type,
			Name:             serviceTypeName,
			Description:      serviceDef.Description,
			DescriptionTrans: firstNonEmpty(translations[serviceTranslationKey(serviceDef.IID)], serviceDef.Description, serviceTypeName),
			Proprietary:      !isStandardSpecType(serviceDef.Type),
			Properties:       []SpecProperty{},
			Events:           []SpecEvent{},
			Actions:          []SpecAction{},
		}

		for _, propertyDef := range serviceDef.Properties {
			if !added && propertyFiltered(filter, serviceDef.IID, propertyDef.IID) {
				continue
			}
			property, err := p.normalizeProperty(translations, modification, service, propertyDef)
			if err != nil {
				return SpecInstance{}, err
			}
			service.Properties = append(service.Properties, property)
		}

		for _, eventDef := range serviceDef.Events {
			if !added && eventFiltered(filter, serviceDef.IID, eventDef.IID) {
				continue
			}
			service.Events = append(service.Events, SpecEvent{
				IID:              eventDef.IID,
				Type:             eventDef.Type,
				Name:             urnSegment(eventDef.Type, 3),
				Description:      eventDef.Description,
				DescriptionTrans: firstNonEmpty(translations[eventTranslationKey(serviceDef.IID, eventDef.IID)], eventDef.Description, urnSegment(eventDef.Type, 3)),
				Arguments:        append([]int(nil), eventDef.Arguments...),
				Proprietary:      service.Proprietary || !isStandardSpecType(eventDef.Type),
			})
		}

		for _, actionDef := range serviceDef.Actions {
			if !added && actionFiltered(filter, serviceDef.IID, actionDef.IID) {
				continue
			}
			service.Actions = append(service.Actions, SpecAction{
				IID:              actionDef.IID,
				Type:             actionDef.Type,
				Name:             urnSegment(actionDef.Type, 3),
				Description:      actionDef.Description,
				DescriptionTrans: firstNonEmpty(translations[actionTranslationKey(serviceDef.IID, actionDef.IID)], actionDef.Description, urnSegment(actionDef.Type, 3)),
				Input:            append([]int(nil), actionDef.In...),
				Output:           append([]int(nil), actionDef.Out...),
				Proprietary:      service.Proprietary || !isStandardSpecType(actionDef.Type),
			})
		}

		spec.Services = append(spec.Services, service)
	}
	return spec, nil
}

func (p *SpecParser) normalizeProperty(
	translations map[string]string,
	modification *SpecModification,
	service SpecService,
	propertyDef SpecPropertyDefinition,
) (SpecProperty, error) {
	propertyTypeName := urnSegment(propertyDef.Type, 3)
	name := propertyTypeName
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && patch.Name != "" {
		name = patch.Name
	}

	format := propertyDef.Format
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && patch.Format != "" {
		format = patch.Format
	}

	access := append([]string(nil), propertyDef.Access...)
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && len(patch.Access) > 0 {
		access = append([]string(nil), patch.Access...)
	}

	unit := propertyDef.Unit
	if unit == "none" {
		unit = ""
	}
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && patch.Unit != "" {
		unit = patch.Unit
	}

	expr := propertyDef.Expr
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && patch.Expr != "" {
		expr = patch.Expr
	}

	icon := ""
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && patch.Icon != "" {
		icon = patch.Icon
	}

	valueRange := propertyDef.ValueRange
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && patch.ValueRange != nil {
		cloned := *patch.ValueRange
		valueRange = &cloned
	}

	valueInputs := make([]SpecValueListItemInput, 0)
	if patch := findPropertyPatch(modification, service.IID, propertyDef.IID); patch != nil && len(patch.ValueList) > 0 {
		for _, item := range patch.ValueList {
			valueInputs = append(valueInputs, SpecValueListItemInput{
				Name:        item.Name,
				Value:       item.Value,
				Description: item.Description,
			})
		}
	} else {
		for _, item := range propertyDef.ValueList {
			valueInputs = append(valueInputs, SpecValueListItemInput{
				Name:        item.Name,
				Value:       item.Value,
				Description: item.Description,
			})
		}
	}
	if len(valueInputs) == 0 && isBoolFormat(format) {
		if boolValueInputs := p.boolValueInputs(propertyDef.Type); len(boolValueInputs) > 0 {
			valueInputs = boolValueInputs
		}
	}
	for idx := range valueInputs {
		if valueInputs[idx].Description == "" {
			valueInputs[idx].Description = "v_" + propertyValueLabel(valueInputs[idx].Value)
		}
		if valueInputs[idx].Name == "" {
			valueInputs[idx].Name = valueInputs[idx].Description
		}
		if translated := translations[valueTranslationKey(service.IID, propertyDef.IID, idx)]; translated != "" {
			valueInputs[idx].Description = translated
		}
	}
	valueList, err := NewValueListFromSpec(valueInputs)
	if err != nil {
		return SpecProperty{}, err
	}

	property := SpecProperty{
		IID:              propertyDef.IID,
		Type:             propertyDef.Type,
		Name:             name,
		Description:      propertyDef.Description,
		DescriptionTrans: firstNonEmpty(translations[propertyTranslationKey(service.IID, propertyDef.IID)], propertyDef.Description, propertyTypeName),
		Format:           format,
		Access:           access,
		Unit:             unit,
		ValueRange:       valueRange,
		ValueList:        valueList,
		Precision:        precisionFromRange(valueRange),
		Expr:             expr,
		Icon:             icon,
		Proprietary:      service.Proprietary || !isStandardSpecType(propertyDef.Type),
	}
	property.Readable = containsStringValue(property.Access, "read")
	property.Writable = containsStringValue(property.Access, "write")
	property.Notifiable = containsStringValue(property.Access, "notify")
	return property, nil
}

func (p *SpecParser) boolValueInputs(propertyType string) []SpecValueListItemInput {
	translation := p.findBoolTranslation(normalizePropertyURN(propertyType))
	if translation == nil {
		return nil
	}
	falseText := "False"
	trueText := "True"
	for _, locale := range translation.Locales {
		if locale.Language == p.language {
			falseText = locale.FalseText
			trueText = locale.TrueText
			break
		}
		if locale.Language == defaultSpecLanguage {
			falseText = locale.FalseText
			trueText = locale.TrueText
		}
	}
	return []SpecValueListItemInput{
		{Name: "false", Value: NewSpecValueBool(false), Description: falseText},
		{Name: "true", Value: NewSpecValueBool(true), Description: trueText},
	}
}

func (p *SpecParser) loadTranslations(ctx context.Context, urn string) map[string]string {
	baseURN := normalizeDeviceURN(urn)
	var selected map[string]string
	var fallback map[string]string

	if remote := p.fetchRemoteTranslations(ctx, urn); len(remote) > 0 {
		selected = mergeStringMaps(selected, selectTranslationLanguage(remote, p.language))
		if len(selected) == 0 {
			fallback = mergeStringMaps(fallback, selectTranslationLanguage(remote, defaultSpecLanguage))
		}
	}

	local := p.localMultiLang[baseURN]
	selected = mergeStringMaps(selected, local[p.language])
	if len(selected) == 0 {
		fallback = mergeStringMaps(fallback, local[defaultSpecLanguage])
	}

	active := selected
	if len(active) == 0 {
		active = fallback
	}
	return compileTranslationMap(active)
}

func (p *SpecParser) fetchRemoteTranslations(ctx context.Context, urn string) map[string]map[string]string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.multiLangEndpoint, nil)
	if err != nil {
		return nil
	}
	query := req.URL.Query()
	query.Set("urn", urn)
	req.URL.RawQuery = query.Encode()
	resp, err := p.http.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	var payload struct {
		Data map[string]map[string]string `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	return payload.Data
}

func loadEmbeddedMultiLang() (map[string]map[string]map[string]string, error) {
	data, err := embeddedAssets.ReadFile(filepath.ToSlash("specs/multi_lang.json"))
	if err != nil {
		return nil, err
	}
	var out map[string]map[string]map[string]string
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func compileTranslationMap(raw map[string]string) map[string]string {
	out := make(map[string]string, len(raw)+1)
	for key, value := range raw {
		if strings.TrimSpace(value) == "" {
			continue
		}
		parts := strings.Split(key, ":")
		switch len(parts) {
		case 2:
			if parts[0] == "device" {
				out["device"] = value
			}
			if parts[0] == "service" {
				out["s:"+parseTranslationNumber(parts[1])] = value
			}
		case 4:
			serviceID := parseTranslationNumber(parts[1])
			elementID := parseTranslationNumber(parts[3])
			switch parts[2] {
			case "property":
				out["p:"+serviceID+":"+elementID] = value
			case "event":
				out["e:"+serviceID+":"+elementID] = value
			case "action":
				out["a:"+serviceID+":"+elementID] = value
			}
		case 6:
			out["v:"+parseTranslationNumber(parts[1])+":"+parseTranslationNumber(parts[3])+":"+parseTranslationNumber(parts[5])] = value
		}
	}
	return out
}

func parseTranslationNumber(raw string) string {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return raw
	}
	return strconv.Itoa(n)
}

func serviceTranslationKey(siid int) string {
	return "s:" + strconv.Itoa(siid)
}

func propertyTranslationKey(siid, piid int) string {
	return "p:" + strconv.Itoa(siid) + ":" + strconv.Itoa(piid)
}

func eventTranslationKey(siid, eiid int) string {
	return "e:" + strconv.Itoa(siid) + ":" + strconv.Itoa(eiid)
}

func actionTranslationKey(siid, aiid int) string {
	return "a:" + strconv.Itoa(siid) + ":" + strconv.Itoa(aiid)
}

func valueTranslationKey(siid, piid, index int) string {
	return "v:" + strconv.Itoa(siid) + ":" + strconv.Itoa(piid) + ":" + strconv.Itoa(index)
}

func isBoolFormat(format string) bool {
	return format == "bool"
}

func isStandardSpecType(value string) bool {
	return strings.Contains(value, "urn:miot-spec-v2:")
}

func precisionFromRange(valueRange *ValueRange) int {
	if valueRange == nil {
		return 0
	}
	step := strconv.FormatFloat(valueRange.Step, 'f', -1, 64)
	if idx := strings.IndexByte(step, '.'); idx >= 0 {
		return len(step[idx+1:])
	}
	return 0
}

func propertyValueLabel(value SpecValue) string {
	switch value.Kind() {
	case SpecValueKindBool:
		if raw, _ := value.Bool(); raw {
			return "true"
		}
		return "false"
	case SpecValueKindInt:
		raw, _ := value.Int()
		return strconv.FormatInt(raw, 10)
	case SpecValueKindFloat:
		raw, _ := value.Float()
		return strconv.FormatFloat(raw, 'f', -1, 64)
	case SpecValueKindString:
		raw, _ := value.String()
		return raw
	default:
		return "unknown"
	}
}

func normalizeDeviceURN(urn string) string {
	parts := strings.Split(urn, ":")
	if len(parts) >= 6 {
		return strings.Join(parts[:6], ":")
	}
	return urn
}

func normalizePropertyURN(urn string) string {
	parts := strings.Split(urn, ":")
	if len(parts) >= 5 {
		return strings.Join(parts[:5], ":")
	}
	return urn
}

func urnSegment(value string, index int) string {
	parts := strings.Split(value, ":")
	if index >= 0 && index < len(parts) {
		return parts[index]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func containsStringValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func mergeStringMaps(base, overlay map[string]string) map[string]string {
	if len(base) == 0 && len(overlay) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(base)+len(overlay))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range overlay {
		out[key] = value
	}
	return out
}

func selectTranslationLanguage(translations map[string]map[string]string, language string) map[string]string {
	if language == "zh-Hans" {
		if data := translations["zh_cn"]; len(data) > 0 {
			return data
		}
	}
	if language == "zh-Hant" {
		if data := translations["zh_hk"]; len(data) > 0 {
			return data
		}
		if data := translations["zh_tw"]; len(data) > 0 {
			return data
		}
	}
	return translations[language]
}

func (p *SpecParser) findFilter(baseURN string) *FilterRule {
	for _, filter := range p.rules.Filters {
		if filter.DeviceURN == baseURN {
			filterCopy := filter
			return &filterCopy
		}
	}
	return nil
}

func (p *SpecParser) findAddition(urn string) *SpecAddition {
	for _, addition := range p.rules.Additions {
		if addition.DeviceURN == urn {
			additionCopy := addition
			return &additionCopy
		}
	}
	return &SpecAddition{}
}

func (p *SpecParser) findModification(urn string) *SpecModification {
	index := make(map[string]SpecModification, len(p.rules.Modifications))
	for _, modification := range p.rules.Modifications {
		index[modification.DeviceURN] = modification
	}
	return resolveModification(index, urn, map[string]struct{}{})
}

func resolveModification(index map[string]SpecModification, urn string, seen map[string]struct{}) *SpecModification {
	modification, ok := index[urn]
	if !ok {
		return nil
	}
	if modification.AliasURN == "" {
		modCopy := modification
		return &modCopy
	}
	if _, loop := seen[urn]; loop {
		modCopy := modification
		return &modCopy
	}
	seen[urn] = struct{}{}
	resolved := resolveModification(index, modification.AliasURN, seen)
	if resolved == nil {
		modCopy := modification
		return &modCopy
	}
	return resolved
}

func findPropertyPatch(modification *SpecModification, siid, piid int) *SpecElementPatch {
	if modification == nil {
		return nil
	}
	key := strconv.Itoa(siid) + "." + strconv.Itoa(piid)
	for _, patch := range modification.Patches {
		if patch.Kind == SpecElementKindProperty && patch.Key == key {
			patchCopy := patch
			return &patchCopy
		}
	}
	return nil
}

func (p *SpecParser) findBoolTranslation(propertyURN string) *BoolTranslation {
	for _, translation := range p.rules.BoolTranslations {
		if translation.PropertyURN == propertyURN {
			copy := translation
			return &copy
		}
	}
	return nil
}

func serviceFiltered(filter *FilterRule, siid int) bool {
	if filter == nil {
		return false
	}
	key := strconv.Itoa(siid)
	return containsStringValue(filter.Services, key) || containsStringValue(filter.Services, "*")
}

func propertyFiltered(filter *FilterRule, siid, piid int) bool {
	if filter == nil {
		return false
	}
	key := strconv.Itoa(siid) + "." + strconv.Itoa(piid)
	return containsStringValue(filter.Properties, key) || containsStringValue(filter.Properties, strconv.Itoa(siid)+".*")
}

func eventFiltered(filter *FilterRule, siid, eiid int) bool {
	if filter == nil {
		return false
	}
	key := strconv.Itoa(siid) + "." + strconv.Itoa(eiid)
	return containsStringValue(filter.Events, key) || containsStringValue(filter.Events, strconv.Itoa(siid)+".*")
}

func actionFiltered(filter *FilterRule, siid, aiid int) bool {
	if filter == nil {
		return false
	}
	key := strconv.Itoa(siid) + "." + strconv.Itoa(aiid)
	return containsStringValue(filter.Actions, key) || containsStringValue(filter.Actions, strconv.Itoa(siid)+".*")
}

func (p *SpecParser) loadCachedSpec(ctx context.Context, urn string) (SpecInstance, bool, error) {
	if p.cache == nil {
		return SpecInstance{}, false, nil
	}
	data, err := p.cache.LoadBytes(ctx, specCacheDomain, specCacheName(urn, p.language))
	if errors.Is(err, fs.ErrNotExist) {
		return SpecInstance{}, false, nil
	}
	if err != nil {
		return SpecInstance{}, false, err
	}
	var spec SpecInstance
	if err := json.Unmarshal(data, &spec); err != nil {
		return SpecInstance{}, false, Wrap(ErrInvalidResponse, "decode cached spec", err)
	}
	return spec, true, nil
}

func (p *SpecParser) saveCachedSpec(ctx context.Context, urn string, spec SpecInstance) error {
	if p.cache == nil {
		return nil
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return p.cache.SaveBytes(ctx, specCacheDomain, specCacheName(urn, p.language), data)
}

func specCacheName(urn, language string) string {
	replacer := strings.NewReplacer(":", "_", "/", "_")
	return replacer.Replace(urn) + "_" + language
}
