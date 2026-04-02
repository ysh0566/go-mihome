package miot

import "testing"

func TestValueRangeFromSpec(t *testing.T) {
	vr, err := NewValueRangeFromSpec([]float64{0, 100, 1})
	if err != nil {
		t.Fatalf("NewValueRangeFromSpec() error = %v", err)
	}
	if vr.Min != 0 || vr.Max != 100 || vr.Step != 1 {
		t.Fatalf("value range = %#v", vr)
	}
}

func TestValueListDeduplicatesDescriptions(t *testing.T) {
	valueList, err := NewValueListFromSpec([]SpecValueListItemInput{
		{Name: "mode", Value: NewSpecValueInt(1), Description: "Auto"},
		{Name: "mode", Value: NewSpecValueInt(2), Description: "Auto"},
	})
	if err != nil {
		t.Fatalf("NewValueListFromSpec() error = %v", err)
	}
	if len(valueList.Items) != 2 {
		t.Fatalf("len(Items) = %d", len(valueList.Items))
	}
	if valueList.Items[0].Name != "mode" || valueList.Items[0].Description != "Auto" {
		t.Fatalf("first item = %#v", valueList.Items[0])
	}
	if valueList.Items[1].Name != "mode_2" {
		t.Fatalf("second item name = %q", valueList.Items[1].Name)
	}
	if valueList.Items[1].Description != "Auto_2" {
		t.Fatalf("second item description = %q", valueList.Items[1].Description)
	}
}
