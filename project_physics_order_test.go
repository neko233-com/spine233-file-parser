package spineparser

import "testing"

func TestDiscoverProjectPhysicsConstraintReferences(t *testing.T) {
	payload := []byte{
		0x29, 0x0f, 0x01, 0x03,
		0x79, 0x0c,
		0x79, 0x0a,
		0x79, 0x0d,
		0x00, 0x02, 0x0d, 0x0f, 0x01,
	}
	references, ok := discoverProjectPhysicsConstraintReferences(payload, 3)
	if !ok || len(references) != 3 ||
		references[0] != 12 ||
		references[1] != 10 ||
		references[2] != 13 {
		t.Fatalf("references = %v, ok = %v", references, ok)
	}
}

func TestOrderProjectPhysicsConstraintsUsesInlinePredecessorReference(
	t *testing.T,
) {
	for _, fieldTag := range []byte{0x07, 0x09, 0x0b} {
		payload := make([]byte, 96)
		copy(
			payload[48:],
			[]byte{fieldTag, 0x79, 0x0a, 0x79, 0x01},
		)
		records := []ProjectPhysicsConstraintRecord{
			{Name: "inline", WireReference: 10, Offset: 0, EndOffset: 8},
			{Name: "last", WireReference: 12, Offset: 24, EndOffset: 32},
			{Name: "successor", WireReference: 13, Offset: 40, EndOffset: 48},
		}
		if !orderProjectPhysicsConstraints(payload, records) {
			t.Fatalf(
				"field tag 0x%02x returned false",
				fieldTag,
			)
		}
		if records[0].Name != "inline" ||
			records[1].Name != "successor" ||
			records[2].Name != "last" {
			t.Fatalf(
				"field tag 0x%02x records = %#v",
				fieldTag,
				records,
			)
		}
	}
}

func TestOrderProjectPhysicsConstraintsSupportsTerminalWrapperReference(
	t *testing.T,
) {
	payload := make([]byte, 96)
	copy(payload[48:], []byte{0x07, 0x79, 0x0a, 0x03})
	records := []ProjectPhysicsConstraintRecord{
		{Name: "inline", WireReference: 10, Offset: 0, EndOffset: 8},
		{Name: "last", WireReference: 12, Offset: 24, EndOffset: 32},
		{Name: "successor", WireReference: 13, Offset: 40, EndOffset: 48},
	}
	if !orderProjectPhysicsConstraints(payload, records) {
		t.Fatal("terminal wrapper reference returned false")
	}
	if records[0].Name != "inline" ||
		records[1].Name != "successor" ||
		records[2].Name != "last" {
		t.Fatalf("records = %#v", records)
	}
}

func TestOrderProjectPhysicsConstraintsReversesConsecutiveObjects(t *testing.T) {
	records := []ProjectPhysicsConstraintRecord{
		{Name: "third", WireReference: 10, Offset: 0},
		{Name: "second", WireReference: 11, Offset: 24},
		{Name: "first", WireReference: 12, Offset: 48},
	}
	if !orderProjectPhysicsConstraints(make([]byte, 96), records) {
		t.Fatal("orderProjectPhysicsConstraints returned false")
	}
	if records[0].Name != "first" ||
		records[1].Name != "second" ||
		records[2].Name != "third" {
		t.Fatalf("records = %#v", records)
	}
}
