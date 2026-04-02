package miot

import (
	"strconv"
	"strings"
)

var deviceSemanticAliases = map[string]string{
	"air-condition-outlet": "air-conditioner",
	"speaker":              "wifi-speaker",
	"tv-box":               "television",
	"watch":                "device_tracker",
}

var deviceDescriptorHints = map[string]DeviceDescriptor{
	"humidifier":      {Category: "humidifier", SemanticType: "humidifier", IconHint: "humidifier"},
	"dehumidifier":    {Category: "dehumidifier", SemanticType: "dehumidifier", IconHint: "dehumidifier"},
	"vacuum":          {Category: "vacuum", SemanticType: "vacuum", IconHint: "vacuum"},
	"air-conditioner": {Category: "climate", SemanticType: "air-conditioner", IconHint: "air-conditioner"},
	"thermostat":      {Category: "climate", SemanticType: "thermostat", IconHint: "thermostat"},
	"heater":          {Category: "heater", SemanticType: "heater", IconHint: "heater"},
	"bath-heater":     {Category: "heater", SemanticType: "bath-heater", IconHint: "bath-heater"},
	"electric-blanket": {
		Category:     "heater",
		SemanticType: "electric-blanket",
		IconHint:     "electric-blanket",
	},
	"wifi-speaker":   {Category: "media_player", SemanticType: "wifi-speaker", IconHint: "speaker"},
	"television":     {Category: "media_player", SemanticType: "television", IconHint: "television"},
	"device_tracker": {Category: "device_tracker", SemanticType: "device_tracker", IconHint: "device_tracker"},
}

var serviceSemanticAliases = map[string]string{
	"ambient-light":        "light",
	"night-light":          "light",
	"white-light":          "light",
	"fan-control":          "fan",
	"ceiling-fan":          "fan",
	"air-fresh":            "fan",
	"air-purifier":         "fan",
	"air-condition-outlet": "air-conditioner",
	"window-opener":        "curtain",
	"motor-controller":     "curtain",
	"airer":                "curtain",
}

type serviceEntityRule struct {
	category   string
	properties []propertyRequirement
	actions    []string
}

var serviceEntityRules = map[string]serviceEntityRule{
	"light": {
		category: "light",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
		},
	},
	"indicator-light": {
		category: "light",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
		},
	},
	"fan": {
		category: "fan",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
			{name: "fan-level", access: []string{"read", "write"}},
		},
	},
	"water-heater": {
		category: "water_heater",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
		},
	},
	"curtain": {
		category: "cover",
		properties: []propertyRequirement{
			{name: "motor-control", access: []string{"write"}},
		},
	},
	"air-conditioner": {
		category: "climate",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
			{name: "mode", access: []string{"read", "write"}},
			{name: "target-temperature", access: []string{"read", "write"}},
		},
	},
	"switch": {
		category: "switch",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
		},
	},
	"humidifier": {
		category: "humidifier",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
		},
	},
	"dehumidifier": {
		category: "dehumidifier",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
		},
	},
	"thermostat": {
		category: "climate",
		properties: []propertyRequirement{
			{name: "on", access: []string{"read", "write"}},
		},
	},
	"vacuum": {
		category: "vacuum",
		actions:  []string{"start-sweep", "stop-sweeping"},
	},
}

var propertyAliasHints = map[string]string{
	"mode-a":                "mode",
	"mode-b":                "mode",
	"mode-c":                "mode",
	"fan-level-a":           "fan-level",
	"fan-level-b":           "fan-level",
	"fan-level-c":           "fan-level",
	"fan-level-ventilation": "fan-level",
	"ac-fan-level":          "fan-level",
	"current-position-a":    "current-position",
	"current-position-b":    "current-position",
	"status-a":              "status",
	"status-b":              "status",
}

type propertyRule struct {
	category string
	semantic string
	icon     string
}

var propertyRules = map[string]propertyRule{
	"submersion-state":      {category: "binary_sensor", semantic: "moisture", icon: "moisture"},
	"contact-state":         {category: "binary_sensor", semantic: "door", icon: "door"},
	"occupancy-status":      {category: "binary_sensor", semantic: "occupancy", icon: "occupancy"},
	"temperature":           {category: "sensor", semantic: "temperature", icon: "temperature"},
	"indoor-temperature":    {category: "sensor", semantic: "temperature", icon: "temperature"},
	"target-temperature":    {category: "number", semantic: "temperature", icon: "temperature"},
	"relative-humidity":     {category: "sensor", semantic: "humidity", icon: "humidity"},
	"air-quality-index":     {category: "sensor", semantic: "aqi", icon: "aqi"},
	"pm2.5-density":         {category: "sensor", semantic: "pm25", icon: "pm25"},
	"pm10-density":          {category: "sensor", semantic: "pm10", icon: "pm10"},
	"pm1":                   {category: "sensor", semantic: "pm1", icon: "pm1"},
	"atmospheric-pressure":  {category: "sensor", semantic: "pressure", icon: "pressure"},
	"tvoc-density":          {category: "sensor", semantic: "tvoc", icon: "tvoc"},
	"voc-density":           {category: "sensor", semantic: "voc", icon: "voc"},
	"battery-level":         {category: "sensor", semantic: "battery", icon: "battery"},
	"voltage":               {category: "sensor", semantic: "voltage", icon: "voltage"},
	"electric-current":      {category: "sensor", semantic: "current", icon: "current"},
	"illumination":          {category: "sensor", semantic: "illuminance", icon: "illuminance"},
	"no-one-determine-time": {category: "sensor", semantic: "duration", icon: "duration"},
	"has-someone-duration":  {category: "sensor", semantic: "duration", icon: "duration"},
	"no-one-duration":       {category: "sensor", semantic: "duration", icon: "duration"},
	"electric-power":        {category: "sensor", semantic: "power", icon: "power"},
	"surge-power":           {category: "sensor", semantic: "power", icon: "power"},
	"power-consumption":     {category: "sensor", semantic: "energy", icon: "energy"},
	"power":                 {category: "sensor", semantic: "power", icon: "power"},
	"on":                    {category: "switch", semantic: "power", icon: "power"},
	"brightness":            {category: "number", semantic: "brightness", icon: "brightness"},
}

var eventSemanticHints = map[string]string{
	"click":           "button",
	"double-click":    "button",
	"long-press":      "button",
	"motion-detected": "motion",
	"no-motion":       "motion",
	"doorbell-ring":   "doorbell",
}

func describeDevice(spec SpecInstance) DeviceDescriptor {
	semantic := matchDeviceSemantic(spec)
	if semantic == "" {
		semantic = canonicalDeviceSemantic(normalizeDescriptorName(spec.Name))
	}
	if semantic == "" {
		semantic = canonicalDeviceSemantic(deviceSemanticFromURN(spec.URN))
	}
	if semantic == "" {
		semantic = canonicalDeviceSemantic(deviceSemanticFromServices(spec.Services))
	}

	desc, ok := deviceDescriptorHints[semantic]
	if !ok {
		if semantic == "" {
			semantic = "device"
		}
		desc = DeviceDescriptor{
			Category:     "device",
			SemanticType: semantic,
			IconHint:     semantic,
		}
	}
	desc.Name = firstNonEmptyText(spec.DescriptionTrans, spec.Description, spec.Name, semantic)
	desc.Description = firstNonEmptyText(spec.DescriptionTrans, spec.Description, desc.Name)
	return desc
}

func describeServiceEntity(service SpecService) EntityDescriptor {
	category := "service"
	if matched := matchServiceCategory(service); matched != "" {
		category = matched
	}
	return EntityDescriptor{
		Key:          serviceEntityKey(service.IID),
		Kind:         EntityKindService,
		Category:     category,
		SemanticType: service.Name,
		IconHint:     category,
		Name:         service.Name,
		Description:  service.DescriptionTrans,
		ServiceIID:   service.IID,
	}
}

func describePropertyEntity(service SpecService, property SpecProperty) EntityDescriptor {
	semantic := propertySemanticType(property)
	return EntityDescriptor{
		Key:           propertyEntityKey(service.IID, property.IID),
		Kind:          EntityKindProperty,
		Category:      propertyCategory(property),
		SemanticType:  semantic,
		CanonicalUnit: canonicalUnit(property.Unit),
		IconHint:      propertyIconHint(property, semantic),
		Name:          property.Name,
		Description:   property.DescriptionTrans,
		Readable:      property.Readable,
		Writable:      property.Writable,
		Notifiable:    property.Notifiable,
		ValueRange:    property.ValueRange,
		ValueList:     property.ValueList,
		ServiceIID:    service.IID,
		PropertyIID:   property.IID,
	}
}

func describeEventEntity(service SpecService, event SpecEvent) EntityDescriptor {
	semantic := eventSemanticType(event)
	return EntityDescriptor{
		Key:          eventEntityKey(service.IID, event.IID),
		Kind:         EntityKindEvent,
		Category:     "event",
		SemanticType: semantic,
		IconHint:     semantic,
		Name:         event.Name,
		Description:  event.DescriptionTrans,
		ServiceIID:   service.IID,
		EventIID:     event.IID,
	}
}

func describeActionEntity(service SpecService, action SpecAction) EntityDescriptor {
	return EntityDescriptor{
		Key:          actionEntityKey(service.IID, action.IID),
		Kind:         EntityKindAction,
		Category:     "button",
		SemanticType: action.Name,
		IconHint:     action.Name,
		Name:         action.Name,
		Description:  action.DescriptionTrans,
		ServiceIID:   service.IID,
		ActionIID:    action.IID,
	}
}

func propertyCategory(property SpecProperty) string {
	if rule, ok := propertyRules[normalizeDescriptorName(property.Name)]; ok && rule.category != "" {
		return rule.category
	}
	switch {
	case property.Writable && property.Format == "bool":
		return "switch"
	case property.Readable && !property.Writable && (property.Format == "bool" || property.Format == "int"):
		return "binary_sensor"
	case property.Writable && property.Name == "target-temperature":
		return "number"
	case property.Writable && len(property.ValueList.Items) > 0:
		return "select"
	case property.Writable && property.Format == "string":
		return "text"
	case property.Writable:
		return "number"
	default:
		return "sensor"
	}
}

func propertySemanticType(property SpecProperty) string {
	name := normalizeDescriptorName(property.Name)
	if rule, ok := propertyRules[name]; ok && rule.semantic != "" {
		return rule.semantic
	}
	switch name {
	case "on":
		return "power"
	case "temperature", "target-temperature", "indoor-temperature":
		return "temperature"
	case "relative-humidity":
		return "humidity"
	case "pm2.5-density":
		return "pm25"
	case "brightness":
		return "brightness"
	default:
		return name
	}
}

func canonicalUnit(unit string) string {
	switch unit {
	case "celsius":
		return "C"
	case "kelvin":
		return "K"
	case "percentage":
		return "%"
	case "volt":
		return "V"
	case "ampere":
		return "A"
	case "pascal":
		return "Pa"
	case "lux":
		return "lx"
	case "watt":
		return "W"
	case "kWh", "kilowatt-hour", "kilowatt_hour":
		return "kWh"
	case "ppm":
		return "ppm"
	case "mg/m3", "mg/m^3":
		return "mg/m3"
	case "μg/m3", "ug/m3", "μg/m^3", "ug/m^3":
		return "ug/m3"
	default:
		return unit
	}
}

func propertyIconHint(property SpecProperty, semantic string) string {
	if property.Icon != "" {
		return property.Icon
	}
	name := normalizeDescriptorName(property.Name)
	if rule, ok := propertyRules[name]; ok && rule.icon != "" {
		return rule.icon
	}
	return semantic
}

func eventSemanticType(event SpecEvent) string {
	if semantic := eventSemanticHints[normalizeDescriptorName(event.Name)]; semantic != "" {
		return semantic
	}
	return normalizeDescriptorName(event.Name)
}

func normalizeDescriptorName(name string) string {
	if canonical := propertyAliasHints[name]; canonical != "" {
		return canonical
	}
	for _, suffix := range []string{"-a", "-b", "-c", "-d"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func canonicalDeviceSemantic(name string) string {
	name = normalizeDescriptorName(name)
	if alias := deviceSemanticAliases[name]; alias != "" {
		return alias
	}
	return name
}

func canonicalServiceSemantic(name string) string {
	name = normalizeDescriptorName(name)
	if alias := serviceSemanticAliases[name]; alias != "" {
		return alias
	}
	return name
}

func deviceSemanticFromURN(urn string) string {
	parts := strings.Split(urn, ":")
	if len(parts) < 4 {
		return ""
	}
	return normalizeDescriptorName(parts[3])
}

func deviceSemanticFromServices(services []SpecService) string {
	for _, service := range services {
		if semantic := canonicalDeviceSemantic(service.Name); semantic != "" {
			if _, ok := deviceDescriptorHints[semantic]; ok {
				return semantic
			}
		}
	}
	return ""
}

func matchServiceCategory(service SpecService) string {
	semantic := canonicalServiceSemantic(service.Name)
	rule, ok := serviceEntityRules[semantic]
	if !ok {
		return ""
	}
	if !serviceMatchesRule(service, rule) {
		return ""
	}
	return rule.category
}

func matchDeviceSemantic(spec SpecInstance) string {
	switch {
	case serviceHasProperty(findService(spec.Services, "humidifier"), "on", "read", "write"):
		return "humidifier"
	case serviceHasProperty(findService(spec.Services, "dehumidifier"), "on", "read", "write"):
		return "dehumidifier"
	case serviceHasActions(findService(spec.Services, "vacuum"), "start-sweep", "stop-sweeping"):
		return "vacuum"
	case serviceHasProperties(findService(spec.Services, "air-conditioner", "air-condition-outlet"), []propertyRequirement{
		{name: "on", access: []string{"read", "write"}},
		{name: "mode", access: []string{"read", "write"}},
		{name: "target-temperature", access: []string{"read", "write"}},
	}):
		return "air-conditioner"
	case serviceHasProperty(findService(spec.Services, "thermostat"), "on", "read", "write"):
		return "thermostat"
	case serviceHasProperty(findService(spec.Services, "heater"), "on", "read", "write"):
		return "heater"
	case serviceHasProperty(findService(spec.Services, "ptc-bath-heater"), "mode", "read", "write"):
		return "bath-heater"
	case serviceHasProperties(findService(spec.Services, "electric-blanket"), []propertyRequirement{
		{name: "on", access: []string{"read", "write"}},
		{name: "target-temperature", access: []string{"read", "write"}},
	}):
		return "electric-blanket"
	case serviceHasProperty(findService(spec.Services, "speaker"), "volume", "read", "write") &&
		serviceHasProperty(findService(spec.Services, "play-control"), "playing-state", "read") &&
		serviceHasActions(findService(spec.Services, "play-control"), "play"):
		return "wifi-speaker"
	case serviceHasProperty(findService(spec.Services, "speaker"), "volume", "read", "write") &&
		serviceHasActions(findService(spec.Services, "television"), "turn-off"):
		return "television"
	case serviceHasProperty(findService(spec.Services, "speaker"), "volume", "read", "write") &&
		serviceHasActions(findService(spec.Services, "tv-box"), "turn-off"):
		return "television"
	case serviceHasProperties(findService(spec.Services, "watch"), []propertyRequirement{
		{name: "longitude", access: []string{"read"}},
		{name: "latitude", access: []string{"read"}},
	}):
		return "device_tracker"
	default:
		return ""
	}
}

type propertyRequirement struct {
	name   string
	access []string
}

func findService(services []SpecService, names ...string) *SpecService {
	for i := range services {
		serviceName := normalizeDescriptorName(services[i].Name)
		for _, name := range names {
			if serviceName == normalizeDescriptorName(name) {
				return &services[i]
			}
		}
	}
	return nil
}

func serviceHasProperties(service *SpecService, properties []propertyRequirement) bool {
	if service == nil {
		return false
	}
	for _, property := range properties {
		if !serviceHasProperty(service, property.name, property.access...) {
			return false
		}
	}
	return true
}

func serviceHasProperty(service *SpecService, name string, access ...string) bool {
	if service == nil {
		return false
	}
	name = normalizeDescriptorName(name)
	for _, property := range service.Properties {
		if normalizeDescriptorName(property.Name) != name {
			continue
		}
		ok := true
		for _, one := range access {
			if !propertyHasAccess(property, one) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func propertyHasAccess(property SpecProperty, access string) bool {
	access = strings.ToLower(access)
	switch access {
	case "read":
		if property.Readable {
			return true
		}
	case "write":
		if property.Writable {
			return true
		}
	case "notify":
		if property.Notifiable {
			return true
		}
	}
	for _, item := range property.Access {
		if strings.EqualFold(item, access) {
			return true
		}
	}
	return false
}

func serviceHasActions(service *SpecService, names ...string) bool {
	if service == nil {
		return false
	}
	for _, name := range names {
		name = normalizeDescriptorName(name)
		found := false
		for _, action := range service.Actions {
			if normalizeDescriptorName(action.Name) == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func serviceMatchesRule(service SpecService, rule serviceEntityRule) bool {
	if len(rule.properties) > 0 && !serviceHasProperties(&service, rule.properties) {
		return false
	}
	if len(rule.actions) > 0 && !serviceHasActions(&service, rule.actions...) {
		return false
	}
	return true
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func serviceEntityKey(siid int) string {
	return "s:" + itoa(siid)
}

func propertyEntityKey(siid, piid int) string {
	return "p:" + itoa(siid) + ":" + itoa(piid)
}

func eventEntityKey(siid, eiid int) string {
	return "e:" + itoa(siid) + ":" + itoa(eiid)
}

func actionEntityKey(siid, aiid int) string {
	return "a:" + itoa(siid) + ":" + itoa(aiid)
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
