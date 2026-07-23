package spineparser

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestPatchProjectAnimationFloat32(t *testing.T) {
	payload := []byte{0x01}
	payload = append(payload, kryoASCIIForTest("attack")...)
	payload = append(payload, 0x02)
	payload = appendFloat32ForTest(payload, 13.22)
	payload = append(payload, 0x03)
	payload = appendFloat32ForTest(payload, 13.22)
	payload = append(payload, kryoASCIIForTest("idle")...)
	payload = appendFloat32ForTest(payload, 13.22)

	document := &ProjectDocument{Payload: payload}
	patched, report, err := PatchProjectAnimationFloat32(document, ProjectAnimationFloatPatch{
		Animation: "attack",
		EndBefore: "idle",
		Edits: []ProjectFloat32Edit{
			{From: 13.22, To: 24, ExpectedMatches: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changes) != 1 || len(report.Changes[0].Offsets) != 2 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if string(document.Payload) != string(payload) {
		t.Fatal("input document was mutated")
	}
	if string(patched.Payload) == string(payload) {
		t.Fatal("patched payload did not change")
	}
	if got := math.Float32frombits(binary.BigEndian.Uint32(
		patched.Payload[report.Changes[0].Offsets[0]:],
	)); got != 24 {
		t.Fatalf("patched value = %v, want 24", got)
	}
}

func TestPatchProjectAnimationFloat32FailsClosed(t *testing.T) {
	payload := append(kryoASCIIForTest("attack"), appendFloat32ForTest(nil, 13.22)...)
	_, _, err := PatchProjectAnimationFloat32(
		&ProjectDocument{Payload: payload},
		ProjectAnimationFloatPatch{
			Animation: "attack",
			Edits: []ProjectFloat32Edit{
				{From: 13.22, To: 24, ExpectedMatches: 2},
			},
		},
	)
	if err == nil {
		t.Fatal("expected match-count error")
	}
}

func kryoASCIIForTest(value string) []byte {
	encoded := []byte(value)
	encoded[len(encoded)-1] |= 0x80
	return encoded
}

func appendFloat32ForTest(output []byte, value float32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], math.Float32bits(value))
	return append(output, encoded[:]...)
}
