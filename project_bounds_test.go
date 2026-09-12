package spineparser

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

func TestDiscoverProjectSkeletonBounds(t *testing.T) {
	payload := []byte{0xaa}
	payload = append(payload, projectSkeletonBoundsFieldV2...)
	for _, value := range []float32{-363.00427, -230.79347, 720.87134, 614.3903} {
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], math.Float32bits(value))
		payload = append(payload, encoded[:]...)
	}
	payload = append(payload, 0xbb)

	bounds, err := DiscoverProjectSkeletonBounds(payload)
	if err != nil {
		t.Fatal(err)
	}
	if bounds.Offset != 1+len(projectSkeletonBoundsFieldV2) ||
		math.Float32bits(bounds.X) != 0xc3b5808c ||
		math.Float32bits(bounds.Y) != 0xc366cb21 ||
		math.Float32bits(bounds.Width) != 0x443437c4 ||
		math.Float32bits(bounds.Height) != 0x441998fb {
		t.Fatalf("bounds = %#v", bounds)
	}
}

func TestDiscoverProjectSkeletonBoundsSelectsOnlyExportEnabledSkeleton(t *testing.T) {
	payload := appendProjectBoundsCandidateForTest(nil, 10, false)
	payload = append(payload, []byte("active-skeleton-data")...)
	payload = appendProjectBoundsCandidateForTest(payload, 20, true)
	payload = append(payload, []byte("inactive-trailing-data")...)
	payload = appendProjectBoundsCandidateForTest(payload, 30, false)

	bounds, err := DiscoverProjectSkeletonBounds(payload)
	if err != nil {
		t.Fatal(err)
	}
	if bounds.X != 20 {
		t.Fatalf("bounds = %#v", bounds)
	}
	runtimePayload, err := discoverProjectRuntimePayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(runtimePayload, []byte("active-skeleton-data")) ||
		bytes.Contains(runtimePayload, []byte("inactive-trailing-data")) {
		t.Fatalf("runtime payload = %q", runtimePayload)
	}
}

func appendProjectBoundsCandidateForTest(
	payload []byte,
	x float32,
	exportEnabled bool,
) []byte {
	payload = append(payload, projectSkeletonBoundsFieldV2...)
	for _, value := range []float32{x, 0, 1, 1} {
		var encoded [4]byte
		binary.BigEndian.PutUint32(encoded[:], math.Float32bits(value))
		payload = append(payload, encoded[:]...)
	}
	payload = append(payload, 0x34, 0x01, 0x2f, 0x01, 0x2d, 0x6c)
	payload = append(payload, 0x01, 0x23, 0x01, 0x7e)
	if exportEnabled {
		return append(payload, 0x01)
	}
	return append(payload, 0x00)
}

func TestDiscoverProjectSkeletonBoundsRejectsLooseFloats(t *testing.T) {
	payload := make([]byte, 16)
	if _, err := DiscoverProjectSkeletonBounds(payload); err == nil {
		t.Fatal("expected bounds structure error")
	}
}

func TestDiscoverProjectSkeletonBoundsRejectsAmbiguousFields(t *testing.T) {
	field := append([]byte(nil), projectSkeletonBoundsFieldV2...)
	field = append(field, make([]byte, 16)...)
	payload := append(append([]byte(nil), field...), field...)
	if _, err := DiscoverProjectSkeletonBounds(payload); err == nil {
		t.Fatal("expected ambiguous bounds error")
	}
}
