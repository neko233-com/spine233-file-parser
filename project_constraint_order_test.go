package spineparser

import "testing"

func TestResolveProjectConstraintOrderInsertsInlineRecords(t *testing.T) {
	payload := make([]byte, 160)
	copy(payload[56:], []byte{0x07, 0x79, 0x0a})
	copy(payload[104:], []byte{0x03, 0x48, 0x14})
	physics := &ProjectPhysicsConstraintDirectory{
		ReferencesComplete: true,
		Records: []ProjectPhysicsConstraintRecord{
			{Name: "physics-inline", WireReference: 10, Offset: 0, EndOffset: 8},
			{Name: "physics-successor", WireReference: 12, Offset: 48, EndOffset: 56},
		},
	}
	paths := &ProjectPathConstraintDirectory{
		ReferencesComplete: true,
		Records: []ProjectPathConstraintRecord{
			{Name: "path-inline", WireReference: 21, Offset: 16, EndOffset: 24},
			{Name: "path-successor", WireReference: 22, Offset: 96, EndOffset: 104},
		},
		groupOwnerReferences: map[int]int{20: 21},
	}
	order, err := ResolveProjectConstraintOrder(payload, physics, paths)
	if err != nil {
		t.Fatal(err)
	}
	want := []ProjectConstraintOrderRecord{
		{Type: "path", Name: "path-inline"},
		{Type: "path", Name: "path-successor"},
		{Type: "physics", Name: "physics-inline"},
		{Type: "physics", Name: "physics-successor"},
	}
	if len(order) != len(want) {
		t.Fatalf("order = %#v", order)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("order = %#v", order)
		}
	}
}

func TestConstraintInlinePredecessorSupportsObservedWrapperFields(
	t *testing.T,
) {
	for _, fieldTag := range []byte{0x03, 0x07, 0x08, 0x09, 0x0b} {
		payload := []byte{fieldTag, 0x79, 0xe7, 0x29, 0x79, 0x01}
		if !hasProjectConstraintInlinePredecessorReference(
			payload,
			0,
			len(payload),
			0x79,
			5351,
		) {
			t.Fatalf("field tag 0x%02x was not recognized", fieldTag)
		}
	}
}
