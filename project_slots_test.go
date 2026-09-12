package spineparser

import "testing"

func TestReadProjectSlotInlineNameUsesRecordField(t *testing.T) {
	payload := []byte{
		0x05, 0x01, 0x01, 0x01, 'f', 'i', 's', 0xe8,
		0x0d, 0x08, 0x01, 0x01, 's', 'l', 'o', 't', 0xb2,
	}
	name, offset, ok := readProjectSlotInlineName(
		payload,
		0,
		len(payload),
	)
	if !ok || name != "slot2" || offset != 8 {
		t.Fatalf("name = %q, offset = %d, ok = %v", name, offset, ok)
	}
}

func TestReadProjectSlotNameFieldReadsKryoReference(t *testing.T) {
	payload := []byte{0x0d, 0x01, 0x01, 0x9b, 0x02}
	name, reference, offset, ok := readProjectSlotNameField(
		payload,
		0,
		len(payload),
	)
	if !ok || name != "" || reference != 283 || offset != 0 {
		t.Fatalf(
			"name = %q, reference = %d, offset = %d, ok = %v",
			name,
			reference,
			offset,
			ok,
		)
	}
}

func TestReadProjectSlotBlendV2TracksInlineEnum(t *testing.T) {
	blend, ok := readProjectSlotBlendV2(
		[]byte{0x07, 0x01, 0x02},
		0,
		3,
	)
	if !ok || blend.inline != "additive" || blend.reference != 0 {
		t.Fatalf("blend = %#v, ok = %v", blend, ok)
	}
	blend, ok = readProjectSlotBlendV2(
		[]byte{0x07, 0x80, 0x01},
		0,
		3,
	)
	if !ok || blend.inline != "" || blend.reference != 128 {
		t.Fatalf("blend = %#v, ok = %v", blend, ok)
	}
}

func TestBindProjectSlotBlendsUsesMonotonicKryoReferences(t *testing.T) {
	records := make([]ProjectSlotRecord, 4)
	evidence := []projectSlotBlendEvidence{
		{inline: "normal"},
		{reference: 20},
		{inline: "additive"},
		{reference: 40},
	}
	if !bindProjectSlotBlends(records, evidence) {
		t.Fatal("bindProjectSlotBlends returned false")
	}
	want := []string{"normal", "normal", "additive", "additive"}
	for index := range want {
		if records[index].Blend != want[index] {
			t.Fatalf("records = %#v", records)
		}
	}
}

func TestBindProjectSlotBlendsAllowsUnreferencedInlineEnums(t *testing.T) {
	records := make([]ProjectSlotRecord, 3)
	evidence := []projectSlotBlendEvidence{
		{inline: "additive"},
		{reference: 52},
		{inline: "normal"},
	}
	if !bindProjectSlotBlends(records, evidence) {
		t.Fatal("bindProjectSlotBlends returned false")
	}
	want := []string{"additive", "additive", "normal"}
	for index := range want {
		if records[index].Blend != want[index] {
			t.Fatalf("records = %#v", records)
		}
	}
}

func TestBindProjectSlotBlendsUsesFullProjectEnumCatalog(t *testing.T) {
	records := make([]ProjectSlotRecord, 3)
	evidence := []projectSlotBlendEvidence{
		{inline: "additive"},
		{reference: 511},
		{reference: 2189},
	}
	catalog := []projectSlotBlendEvidence{
		{inline: "normal"},
		{reference: 511},
		{inline: "additive"},
		{reference: 2189},
	}
	if !bindProjectSlotBlendsFromCatalog(records, evidence, catalog) {
		t.Fatal("bindProjectSlotBlendsFromCatalog returned false")
	}
	want := []string{"additive", "normal", "additive"}
	for index := range want {
		if records[index].Blend != want[index] {
			t.Fatalf("records = %#v", records)
		}
	}
}

func TestBindProjectSlotBlendsRecoversLeadingEnumValuesByOrdinal(t *testing.T) {
	records := make([]ProjectSlotRecord, 3)
	evidence := []projectSlotBlendEvidence{
		{inline: "additive"},
		{reference: 511},
		{reference: 2189},
	}
	if !bindProjectSlotBlends(records, evidence) {
		t.Fatal("bindProjectSlotBlends returned false")
	}
	want := []string{"additive", "normal", "additive"}
	for index := range want {
		if records[index].Blend != want[index] {
			t.Fatalf("records = %#v", records)
		}
	}
}

func TestDiscoverProjectSlotDrawOrderReferences(t *testing.T) {
	for _, suffix := range []byte{0x00, 0x01} {
		payload := []byte{
			0x02, 0x0d, 0x0f, 0x01, 0x02,
			0x0d, 0x07,
			0x0d, 0x09,
			0x32, suffix,
		}
		references, ok := discoverProjectSlotDrawOrderReferences(payload, 2)
		if !ok || len(references) != 2 ||
			references[0] != 7 || references[1] != 9 {
			t.Fatalf("suffix %d references = %v, %v", suffix, references, ok)
		}
	}
}

func TestBindProjectSlotWireReferencesUsesDrawOrder(t *testing.T) {
	records := []ProjectSlotRecord{
		{Name: "second"},
		{Name: "first"},
	}
	ordered, ok := bindProjectSlotWireReferences(records, []int{7, 9})
	if !ok || len(ordered) != 2 ||
		ordered[0].Name != "first" || ordered[0].WireReference != 7 ||
		ordered[1].Name != "second" || ordered[1].WireReference != 9 {
		t.Fatalf("ordered = %#v, %v", ordered, ok)
	}
}

func TestBindProjectSlotWireReferencesUsesSetupAttachmentRegistration(t *testing.T) {
	records := []ProjectSlotRecord{
		{Name: "inline", Offset: 10},
		{
			Name:                     "owner",
			Offset:                   20,
			SetupAttachmentReference: 9,
		},
		{Name: "first", Offset: 30},
	}
	ordered, ok := bindProjectSlotWireReferences(records, []int{8, 10, 7})
	if !ok || len(ordered) != 3 ||
		ordered[0].Name != "owner" || ordered[0].WireReference != 8 ||
		ordered[1].Name != "inline" || ordered[1].WireReference != 10 ||
		ordered[2].Name != "first" || ordered[2].WireReference != 7 {
		t.Fatalf("ordered = %#v, %v", ordered, ok)
	}
}

func TestReadProjectSlotSetupAttachment(t *testing.T) {
	tests := []struct {
		name          string
		payload       []byte
		wantClassID   int
		wantReference int
	}{
		{
			name:          "mesh reference",
			payload:       []byte{0x07, 0x8e, 0x06, 0x09, 0x2e, 0xe6, 0x01, 0x7e},
			wantClassID:   ProjectAttachmentClassMesh,
			wantReference: 230,
		},
		{
			name:          "region reference",
			payload:       []byte{0x07, 0x09, 0x2b, 0x06, 0x7e},
			wantClassID:   ProjectAttachmentClassRegion,
			wantReference: 6,
		},
		{
			name:    "null",
			payload: []byte{0x07, 0x8e, 0x06, 0x09, 0x00, 0x7e},
		},
		{
			name:    "invalid reference",
			payload: []byte{0x07, 0x09, 0x2e, 0x03, 0x7e},
		},
		{
			name:        "inline new object",
			payload:     []byte{0x07, 0x09, 0x2b, 0x01, 0x11, 0x0f},
			wantClassID: ProjectAttachmentClassRegion,
		},
		{
			name:    "unknown class",
			payload: []byte{0x07, 0x09, 0x55, 0x06, 0x7e},
		},
		{
			name:    "ambiguous",
			payload: []byte{0x09, 0x2e, 0xe6, 0x01, 0x7e, 0x09, 0x2e, 0xe2, 0x02, 0x7e},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			classID, reference := readProjectSlotSetupAttachment(
				testCase.payload,
				0,
				len(testCase.payload),
			)
			if classID != testCase.wantClassID ||
				reference != testCase.wantReference {
				t.Fatalf(
					"attachment = class %d reference %d, want class %d reference %d",
					classID,
					reference,
					testCase.wantClassID,
					testCase.wantReference,
				)
			}
		})
	}
}

func TestReadProjectClippingEndSlotIndex(t *testing.T) {
	payload := []byte{
		0x0d, 0x01, 0x01, 0x81,
		0x09, 0x54, 0x3b,
		0x7e, 0x01, 0x0d, 0x01, 0x0f, 0x00, 0x03, 0x0c, 0x00,
	}
	index, ok := readProjectClippingEndSlotIndex(
		payload,
		59,
		ProjectSlotRecord{Offset: 0},
		13,
	)
	if !ok || index != 12 {
		t.Fatalf("index = %d, ok = %v", index, ok)
	}
}
