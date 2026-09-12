package spineparser

import (
	"reflect"
	"testing"
)

func TestDiscoverProjectAnimationsV4326(t *testing.T) {
	payload := []byte{
		0x07, 0x0f, 0x01, 0x02, 0x12, 0x01, 0x0a,
		0x01, 0x01, 'i', 'd', 'l', 'e' | 0x80,
		0x05, 0x01, 0x09, 0x00, 0x03, 0x00, 0x0a, 0x1e, 0x01, 0x7e,
		0x01, 0x01, 'r', 'u', 'n' | 0x80,
		0x05, 0x01, 0x09, 0x00, 0x03, 0x01, 0x0a, 0x1e, 0x01,
	}
	directory, err := discoverProjectAnimationsV4326(payload)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(directory.Records))
	for _, record := range directory.Records {
		got = append(got, record.Name)
	}
	if want := []string{"idle", "run"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("animation names = %#v, want %#v", got, want)
	}
}

func TestMergeProjectSlotsV4326(t *testing.T) {
	payload := []byte{
		0x0d, 0x01, 0x0f, 0x01, 0x01, 's', 'l', 'o', 't', '_', 'b', 'o', 'd', 'y' | 0x80,
		0x05, 0x1e, 0x01,
		0x0d, 0x01, 0x0f, 0x01, 0x01, 's', 'l', 'o', 't', '_', 'v', 'f', 'x' | 0x80,
		0x05, 0x1e, 0x01,
	}
	bones := []ProjectBoneRecord{
		{Name: "root", WireReference: projectFirstWireReference},
		{Name: "body", WireReference: projectFirstWireReference + 1},
	}
	slots := &ProjectSlotDirectory{Records: []ProjectSlotRecord{{Name: "body"}}}
	mergeProjectSlotsV4326(slots, bones, payload)
	if slots.Count != 3 || slots.ReferencesComplete ||
		slots.Records[1].Name != "slot_body" || slots.Records[1].BoneName != "body" ||
		slots.Records[2].Name != "slot_vfx" || slots.Records[2].BoneName != "root" {
		t.Fatalf("slots = %#v", slots.Records)
	}
}

func TestLegacyV4326BoneRecordTail(t *testing.T) {
	payload := []byte{
		0x1b, 0x01, 0x04, 0x02, 0x01, 0x1c,
		0x01, 'b', 'o', 'n', 'e' | 0x80, 0x1d, 0x00,
	}
	if !legacyV4326BoneRecordTail(payload, 0) {
		t.Fatal("4.3.26 bone record tail was not recognized")
	}
}
