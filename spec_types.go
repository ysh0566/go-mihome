package miot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// SpecValueKind identifies the concrete scalar type carried by SpecValue.
type SpecValueKind string

const (
	// SpecValueKindUnknown reports an unset scalar.
	SpecValueKindUnknown SpecValueKind = "unknown"
	// SpecValueKindBool reports a boolean scalar.
	SpecValueKindBool SpecValueKind = "bool"
	// SpecValueKindInt reports an integer scalar.
	SpecValueKindInt SpecValueKind = "int"
	// SpecValueKindFloat reports a floating-point scalar.
	SpecValueKindFloat SpecValueKind = "float"
	// SpecValueKindString reports a string scalar.
	SpecValueKindString SpecValueKind = "string"
)

// SpecValue stores one typed scalar value used by MIoT specs.
type SpecValue struct {
	kind        SpecValueKind
	boolValue   bool
	intValue    int64
	floatValue  float64
	stringValue string
}

// NewSpecValueBool creates a boolean spec value.
func NewSpecValueBool(v bool) SpecValue {
	return SpecValue{kind: SpecValueKindBool, boolValue: v}
}

// NewSpecValueInt creates an integer spec value.
func NewSpecValueInt(v int64) SpecValue {
	return SpecValue{kind: SpecValueKindInt, intValue: v}
}

// NewSpecValueFloat creates a floating-point spec value.
func NewSpecValueFloat(v float64) SpecValue {
	return SpecValue{kind: SpecValueKindFloat, floatValue: v}
}

// NewSpecValueString creates a string spec value.
func NewSpecValueString(v string) SpecValue {
	return SpecValue{kind: SpecValueKindString, stringValue: v}
}

// Kind returns the scalar kind.
func (v SpecValue) Kind() SpecValueKind {
	return v.kind
}

// Bool returns the stored bool and whether the kind matches.
func (v SpecValue) Bool() (bool, bool) {
	return v.boolValue, v.kind == SpecValueKindBool
}

// Int returns the stored int64 and whether the kind matches.
func (v SpecValue) Int() (int64, bool) {
	return v.intValue, v.kind == SpecValueKindInt
}

// Float returns the stored float64 and whether the kind matches.
func (v SpecValue) Float() (float64, bool) {
	return v.floatValue, v.kind == SpecValueKindFloat
}

// String returns the stored string and whether the kind matches.
func (v SpecValue) String() (string, bool) {
	return v.stringValue, v.kind == SpecValueKindString
}

// MarshalJSON encodes the scalar using its concrete type.
func (v SpecValue) MarshalJSON() ([]byte, error) {
	switch v.kind {
	case SpecValueKindBool:
		return json.Marshal(v.boolValue)
	case SpecValueKindInt:
		return json.Marshal(v.intValue)
	case SpecValueKindFloat:
		return json.Marshal(v.floatValue)
	case SpecValueKindString:
		return json.Marshal(v.stringValue)
	default:
		return []byte("null"), nil
	}
}

// UnmarshalJSON decodes a scalar value from JSON.
func (v *SpecValue) UnmarshalJSON(data []byte) error {
	return v.decodeScalar(bytes.TrimSpace(data))
}

// UnmarshalYAML decodes a scalar value from YAML.
func (v *SpecValue) UnmarshalYAML(node *yaml.Node) error {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return err
	}
	return v.decodeScalar(bytes.TrimSpace(raw))
}

func (v *SpecValue) decodeScalar(data []byte) error {
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*v = SpecValue{}
		return nil
	}

	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		*v = NewSpecValueBool(b)
		return nil
	}

	var i int64
	if err := json.Unmarshal(data, &i); err == nil {
		*v = NewSpecValueInt(i)
		return nil
	}

	var f float64
	if err := json.Unmarshal(data, &f); err == nil {
		*v = NewSpecValueFloat(f)
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*v = NewSpecValueString(s)
		return nil
	}

	return fmt.Errorf("decode spec scalar: %s", string(data))
}

// ValueRange stores the minimum, maximum, and step constraints for numeric values.
type ValueRange struct {
	Min  float64 `json:"min" yaml:"min"`
	Max  float64 `json:"max" yaml:"max"`
	Step float64 `json:"step" yaml:"step"`
}

// NewValueRangeFromSpec converts the MIoT `[min,max,step]` format into ValueRange.
func NewValueRangeFromSpec(spec []float64) (ValueRange, error) {
	if len(spec) != 3 {
		return ValueRange{}, &Error{Code: ErrInvalidArgument, Op: "new value range from spec", Msg: "expected [min,max,step]"}
	}
	return ValueRange{Min: spec[0], Max: spec[1], Step: spec[2]}, nil
}

// SpecValueListItemInput describes one raw value-list entry before normalization.
type SpecValueListItemInput struct {
	Name        string    `json:"name,omitempty" yaml:"name,omitempty"`
	Value       SpecValue `json:"value" yaml:"value"`
	Description string    `json:"description" yaml:"description"`
}

// SpecValueListItem stores one normalized MIoT value-list entry.
type SpecValueListItem struct {
	Name        string    `json:"name" yaml:"name"`
	Value       SpecValue `json:"value" yaml:"value"`
	Description string    `json:"description" yaml:"description"`
}

// ValueList stores the ordered list of normalized value-list items.
type ValueList struct {
	Items []SpecValueListItem `json:"items" yaml:"items"`
}

// NewValueListFromSpec normalizes MIoT value-list items and deduplicates descriptions.
func NewValueListFromSpec(spec []SpecValueListItemInput) (ValueList, error) {
	items := make([]SpecValueListItem, 0, len(spec))
	seenDescriptions := make(map[string]int, len(spec))

	for _, input := range spec {
		if input.Description == "" {
			return ValueList{}, &Error{Code: ErrInvalidArgument, Op: "new value list from spec", Msg: "description is empty"}
		}
		baseName := input.Name
		if baseName == "" {
			baseName = input.Description
		}
		count := seenDescriptions[input.Description] + 1
		seenDescriptions[input.Description] = count

		name := baseName
		description := input.Description
		if count > 1 {
			suffix := "_" + strconv.Itoa(count)
			name += suffix
			description += suffix
		}
		items = append(items, SpecValueListItem{
			Name:        SlugifyName(name),
			Value:       input.Value,
			Description: description,
		})
	}

	return ValueList{Items: items}, nil
}

// SpecInstance is the normalized MIoT spec instance returned by SpecParser.
type SpecInstance struct {
	URN              string        `json:"urn"`
	Name             string        `json:"name"`
	Description      string        `json:"description"`
	DescriptionTrans string        `json:"description_trans"`
	Services         []SpecService `json:"services"`
}

// SpecService is one normalized MIoT service definition.
type SpecService struct {
	IID              int            `json:"iid"`
	Type             string         `json:"type"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	DescriptionTrans string         `json:"description_trans"`
	Proprietary      bool           `json:"proprietary,omitempty"`
	NeedFilter       bool           `json:"need_filter,omitempty"`
	Properties       []SpecProperty `json:"properties"`
	Events           []SpecEvent    `json:"events"`
	Actions          []SpecAction   `json:"actions"`
}

// SpecProperty is one normalized MIoT property definition.
type SpecProperty struct {
	IID              int         `json:"iid"`
	Type             string      `json:"type"`
	Name             string      `json:"name"`
	Description      string      `json:"description"`
	DescriptionTrans string      `json:"description_trans"`
	Format           string      `json:"format"`
	Access           []string    `json:"access"`
	Unit             string      `json:"unit,omitempty"`
	ValueRange       *ValueRange `json:"value_range,omitempty"`
	ValueList        ValueList   `json:"value_list,omitempty"`
	Precision        int         `json:"precision,omitempty"`
	Expr             string      `json:"expr,omitempty"`
	Icon             string      `json:"icon,omitempty"`
	Proprietary      bool        `json:"proprietary,omitempty"`
	NeedFilter       bool        `json:"need_filter,omitempty"`
	Readable         bool        `json:"readable"`
	Writable         bool        `json:"writable"`
	Notifiable       bool        `json:"notifiable"`
}

// SpecEvent is one normalized MIoT event definition.
type SpecEvent struct {
	IID              int    `json:"iid"`
	Type             string `json:"type"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	DescriptionTrans string `json:"description_trans"`
	Arguments        []int  `json:"arguments"`
	Proprietary      bool   `json:"proprietary,omitempty"`
	NeedFilter       bool   `json:"need_filter,omitempty"`
}

// SpecAction is one normalized MIoT action definition.
type SpecAction struct {
	IID              int    `json:"iid"`
	Type             string `json:"type"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	DescriptionTrans string `json:"description_trans"`
	Input            []int  `json:"in"`
	Output           []int  `json:"out"`
	Proprietary      bool   `json:"proprietary,omitempty"`
	NeedFilter       bool   `json:"need_filter,omitempty"`
}
