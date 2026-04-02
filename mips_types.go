package miot

import (
	"bytes"
	"encoding/binary"
)

type mipsFieldType uint8

const (
	mipsFieldID mipsFieldType = iota
	mipsFieldReplyTopic
	mipsFieldPayload
	mipsFieldFrom
)

// MIPSMessage is the typed local MIoT MIPS envelope.
type MIPSMessage struct {
	ID         uint32
	From       string
	ReplyTopic string
	Payload    []byte
}

type mipsErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type cloudPropertyEnvelope struct {
	Params cloudPropertyPayload `json:"params"`
}

type cloudPropertyPayload struct {
	SIID  int       `json:"siid"`
	PIID  int       `json:"piid"`
	Value SpecValue `json:"value"`
}

type cloudEventEnvelope struct {
	Params cloudEventPayload `json:"params"`
}

type cloudEventPayload struct {
	SIID      int         `json:"siid"`
	EIID      int         `json:"eiid"`
	Arguments []SpecValue `json:"arguments"`
}

type cloudDeviceStateEnvelope struct {
	DeviceID string `json:"device_id"`
	Event    string `json:"event"`
}

type localPropertyPayload struct {
	DID   string    `json:"did"`
	SIID  int       `json:"siid"`
	PIID  int       `json:"piid"`
	Value SpecValue `json:"value"`
}

type localEventPayload struct {
	DID       string      `json:"did"`
	SIID      int         `json:"siid"`
	EIID      int         `json:"eiid"`
	Arguments []SpecValue `json:"arguments"`
}

type localGetPropRequest struct {
	DID  string `json:"did"`
	SIID int    `json:"siid"`
	PIID int    `json:"piid"`
}

type localGetPropResponse struct {
	Value SpecValue         `json:"value"`
	Error *mipsErrorPayload `json:"error,omitempty"`
}

type localRPCEnvelope[T any] struct {
	DID string      `json:"did"`
	RPC localRPC[T] `json:"rpc"`
}

type localRPC[T any] struct {
	ID     uint32 `json:"id"`
	Method string `json:"method"`
	Params T      `json:"params"`
}

type localSetPropResponse struct {
	Result []SetPropertyResult `json:"result"`
	Error  *mipsErrorPayload   `json:"error,omitempty"`
}

type localActionResponse struct {
	Result ActionResult      `json:"result"`
	Error  *mipsErrorPayload `json:"error,omitempty"`
}

type localDeviceListResponse struct {
	DevList map[string]localDeviceListEntry `json:"devList"`
	Error   *mipsErrorPayload               `json:"error,omitempty"`
}

type localActionGroupListResponse struct {
	Result []string          `json:"result"`
	Error  *mipsErrorPayload `json:"error,omitempty"`
}

// ActionGroupExecResult is the typed result returned when a local action group is executed.
type ActionGroupExecResult struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type localActionGroupExecResponse struct {
	Result ActionGroupExecResult `json:"result"`
	Error  *mipsErrorPayload     `json:"error,omitempty"`
}

type localDeviceListEntry struct {
	Name          string `json:"name"`
	URN           string `json:"urn"`
	Model         string `json:"model"`
	Online        bool   `json:"online"`
	SpecV2Access  bool   `json:"specV2Access"`
	PushAvailable bool   `json:"pushAvailable"`
}

// LocalDeviceSummary is the normalized subset returned by local MIPS device-list queries.
type LocalDeviceSummary struct {
	DID           string
	Name          string
	URN           string
	Model         string
	Online        bool
	SpecV2Access  bool
	PushAvailable bool
}

// Pack encodes the message using the MIoT local MIPS field framing.
func (m MIPSMessage) Pack() ([]byte, error) {
	var buf bytes.Buffer

	if err := writeMIPSField(&buf, mipsFieldID, uint32ToBytes(m.ID), false); err != nil {
		return nil, err
	}
	if m.From != "" {
		if err := writeMIPSField(&buf, mipsFieldFrom, []byte(m.From), true); err != nil {
			return nil, err
		}
	}
	if m.ReplyTopic != "" {
		if err := writeMIPSField(&buf, mipsFieldReplyTopic, []byte(m.ReplyTopic), true); err != nil {
			return nil, err
		}
	}
	if err := writeMIPSField(&buf, mipsFieldPayload, m.Payload, true); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnpackMIPSMessage decodes one local MIoT MIPS envelope from bytes.
func UnpackMIPSMessage(data []byte) (MIPSMessage, error) {
	msg := MIPSMessage{}
	for offset := 0; offset < len(data); {
		if len(data[offset:]) < 5 {
			return MIPSMessage{}, &Error{Code: ErrInvalidResponse, Op: "unpack mips message", Msg: "truncated field header"}
		}
		fieldLen := binary.LittleEndian.Uint32(data[offset : offset+4])
		fieldType := mipsFieldType(data[offset+4])
		start := offset + 5
		end := start + int(fieldLen)
		if end > len(data) {
			return MIPSMessage{}, &Error{Code: ErrInvalidResponse, Op: "unpack mips message", Msg: "truncated field payload"}
		}
		switch fieldType {
		case mipsFieldID:
			if int(fieldLen) != 4 {
				return MIPSMessage{}, &Error{Code: ErrInvalidResponse, Op: "unpack mips message", Msg: "invalid id field"}
			}
			msg.ID = binary.LittleEndian.Uint32(data[start:end])
		case mipsFieldReplyTopic:
			field := trimMIPSField(data[start:end])
			msg.ReplyTopic = string(field)
		case mipsFieldPayload:
			field := trimMIPSField(data[start:end])
			msg.Payload = append([]byte(nil), field...)
		case mipsFieldFrom:
			field := trimMIPSField(data[start:end])
			msg.From = string(field)
		}
		offset = end
	}
	return msg, nil
}

func writeMIPSField(buf *bytes.Buffer, fieldType mipsFieldType, data []byte, nulTerminate bool) error {
	field := append([]byte(nil), data...)
	if nulTerminate {
		field = append(field, 0x00)
	}
	if err := binary.Write(buf, binary.LittleEndian, uint32(len(field))); err != nil {
		return err
	}
	if err := buf.WriteByte(byte(fieldType)); err != nil {
		return err
	}
	_, err := buf.Write(field)
	return err
}

func uint32ToBytes(v uint32) []byte {
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, v)
	return data
}

func trimMIPSField(data []byte) []byte {
	return bytes.TrimRight(data, "\x00")
}
