package spineparser

import "testing"

func TestReadProjectGlobalPhysicsTimelineOwner(t *testing.T) {
	payload := []byte{0x01, 0x00, 0x04, 0x01}
	end, ok := readProjectGlobalPhysicsTimelineOwner(
		payload,
		0,
		len(payload),
	)
	if !ok || end != len(payload) {
		t.Fatalf("end = %d, ok = %v", end, ok)
	}
}

func TestReadProjectGlobalPhysicsTimelineOwnerRejectsNonzeroReference(
	t *testing.T,
) {
	payload := []byte{0x01, 0x01, 0x04, 0x01}
	if _, ok := readProjectGlobalPhysicsTimelineOwner(
		payload,
		0,
		len(payload),
	); ok {
		t.Fatal("nonzero global owner reference accepted")
	}
}

func TestHasProjectPhysicsInlinePredecessorReferenceAcceptsObservedWrappers(
	t *testing.T,
) {
	for _, payload := range [][]byte{
		{0x08, 0x79, 0xc4, 0x0b, 0x79, 0x01},
		{0x08, 0x79, 0xc4, 0x0b, 0x03},
	} {
		if !hasProjectPhysicsInlinePredecessorReference(
			payload,
			0,
			len(payload),
			1476,
		) {
			t.Fatalf("wrapper %x was rejected", payload)
		}
	}
}
