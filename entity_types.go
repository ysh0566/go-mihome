package miot

// EntityKind identifies the generic entity role.
type EntityKind string

const (
	// EntityKindService is a service-level aggregate entity.
	EntityKindService EntityKind = "service"
	// EntityKindProperty is a property-backed entity.
	EntityKindProperty EntityKind = "property"
	// EntityKindEvent is an event-backed entity.
	EntityKindEvent EntityKind = "event"
	// EntityKindAction is an action-backed entity.
	EntityKindAction EntityKind = "action"
)

// EntityDescriptor describes one generic entity projection.
type EntityDescriptor struct {
	Key           string
	Kind          EntityKind
	Category      string
	SemanticType  string
	CanonicalUnit string
	IconHint      string
	Name          string
	Description   string
	Readable      bool
	Writable      bool
	Notifiable    bool
	ValueRange    *ValueRange
	ValueList     ValueList
	ServiceIID    int
	PropertyIID   int
	EventIID      int
	ActionIID     int
}

// DeviceDescriptor describes the platform-neutral role of one MIoT device.
type DeviceDescriptor struct {
	Category     string
	SemanticType string
	IconHint     string
	Name         string
	Description  string
}

// Device aggregates parsed spec entities for one MIoT device.
type Device struct {
	Descriptor DeviceDescriptor
	Info       DeviceInfo
	Spec       SpecInstance
	Entities   []*Entity
}

// EntityState captures the minimal runtime state exposed by a generic entity.
type EntityState struct {
	Online bool
}
