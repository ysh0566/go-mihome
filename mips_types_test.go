package miot

import "testing"

func TestMIPSMessagePackUnpackRoundTrip(t *testing.T) {
	msg := MIPSMessage{
		ID:         42,
		From:       "controller",
		ReplyTopic: "controller/reply",
		Payload:    []byte(`{"ok":true}`),
	}

	wire, err := msg.Pack()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnpackMIPSMessage(wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 42 {
		t.Fatalf("id = %d", got.ID)
	}
	if got.From != "controller" {
		t.Fatalf("from = %q", got.From)
	}
	if got.ReplyTopic != "controller/reply" {
		t.Fatalf("reply topic = %q", got.ReplyTopic)
	}
	if string(got.Payload) != `{"ok":true}` {
		t.Fatalf("payload = %q", got.Payload)
	}
}
