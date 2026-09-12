package spineparser

import (
	"encoding/binary"
	"testing"
)

func TestReadProjectMeshHullBeforeNameConsumesWholeVarint(t *testing.T) {
	payload := []byte{
		0x07, 0x80, 0x07,
		0x05, 0x01, 0x01,
	}
	hull := readProjectMeshHullBeforeName(payload, 0, 3)
	if hull != 448 {
		t.Fatalf("hull = %d", hull)
	}
}

func TestReadProjectMeshHullBeforeNameRejectsAmbiguousTag(t *testing.T) {
	payload := []byte{
		0x07, 0x80, 0x07,
		0x05, 0x01, 0x01,
	}
	hull := readProjectMeshHullBeforeName(payload, 2, 3)
	if hull != 0 {
		t.Fatalf("hull = %d", hull)
	}
}

func TestProjectMeshTriangleIndicesUseBigEndian(t *testing.T) {
	source := []byte{0x01, 0x17, 0x00, 0xff}
	got := []int{
		int(binary.BigEndian.Uint16(source[0:2])),
		int(binary.BigEndian.Uint16(source[2:4])),
	}
	if got[0] != 279 || got[1] != 255 {
		t.Fatalf("triangles = %#v", got)
	}
}

func TestReadProjectMeshEdgesUsesPositiveVarints(t *testing.T) {
	payload := []byte{
		0x00,
		0xce, 0x03,
		0x80, 0x01,
		0x02,
	}
	edges, ok := readProjectMeshEdges(payload, 0, len(payload), 4)
	if !ok {
		t.Fatal("readProjectMeshEdges returned false")
	}
	want := []int{0, 462, 128, 2}
	for index := range want {
		if edges[index] != want[index] {
			t.Fatalf("edges = %#v", edges)
		}
	}
}

func TestReadProjectMeshEdgesRejectsTruncatedVarint(t *testing.T) {
	if edges, ok := readProjectMeshEdges(
		[]byte{0x80},
		0,
		1,
		1,
	); ok || edges != nil {
		t.Fatalf("edges = %#v, ok = %v", edges, ok)
	}
}
