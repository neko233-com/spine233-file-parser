package spineparser

import "testing"

func TestAssignProjectMeshReferencesByCreationOrderUsesUniqueBoundedReference(
	t *testing.T,
) {
	records := []ProjectMeshAttachmentRecord{
		{Name: "first"},
		{Name: "second", WireReference: 64},
	}
	allReferences := map[int]struct{}{
		44: {},
		64: {},
		78: {},
	}
	usedReferences := map[int]struct{}{64: {}}
	usedRecords := map[int]struct{}{1: {}}

	assignProjectMeshReferencesByCreationOrder(
		records,
		allReferences,
		usedReferences,
		usedRecords,
	)
	if records[0].WireReference != 44 {
		t.Fatalf("records = %#v", records)
	}
}

func TestAssignProjectMeshReferencesByCreationOrderKeepsAmbiguityUnresolved(
	t *testing.T,
) {
	records := []ProjectMeshAttachmentRecord{
		{Name: "first"},
		{Name: "second", WireReference: 64},
	}
	allReferences := map[int]struct{}{
		43: {},
		44: {},
		64: {},
	}

	assignProjectMeshReferencesByCreationOrder(
		records,
		allReferences,
		map[int]struct{}{64: {}},
		map[int]struct{}{1: {}},
	)
	if records[0].WireReference != 0 {
		t.Fatalf("records = %#v", records)
	}
}

func TestAssignProjectMeshOwnersByReferenceEvidenceRequiresUniqueSlot(
	t *testing.T,
) {
	records := []ProjectMeshAttachmentRecord{
		{WireReference: 44},
	}
	assignProjectMeshOwnersByReferenceEvidence(
		records,
		map[int]map[int]struct{}{
			51: {44: {}},
		},
	)
	if records[0].OwnerSlotReference != 51 {
		t.Fatalf("records = %#v", records)
	}
}

func TestReadProjectMeshAttachmentWireReference(t *testing.T) {
	payload := []byte{
		0x00,
		ProjectAttachmentClassMesh,
		0x4e,
		0x1f,
		0x1e,
		0x01,
		0x00,
	}
	reference := readProjectMeshAttachmentWireReference(
		payload,
		0,
		len(payload),
	)
	if reference != 78 {
		t.Fatalf("reference = %d", reference)
	}
}

func TestResolveProjectMeshReferencedNamesUsesUniqueUVGeometry(t *testing.T) {
	records := []ProjectMeshAttachmentRecord{
		{
			Name:   "leg",
			UVs:    []float32{0, 0, 1, 1},
			Width:  20,
			Height: 30,
		},
		{
			NameReference: 99,
			UVs:           []float32{0, 0, 1, 1},
			Width:         20,
			Height:        30,
		},
	}
	resolveProjectMeshReferencedNames(nil, records)
	if records[1].Name != "leg" {
		t.Fatalf("records = %#v", records)
	}
}

func TestResolveProjectMeshReferencedNamesKeepsNameAmbiguityUnresolved(
	t *testing.T,
) {
	records := []ProjectMeshAttachmentRecord{
		{Name: "left", UVs: []float32{0, 0}, Width: 20, Height: 30},
		{Name: "right", UVs: []float32{0, 0}, Width: 20, Height: 30},
		{NameReference: 99, UVs: []float32{0, 0}, Width: 20, Height: 30},
	}
	resolveProjectMeshReferencedNames(nil, records)
	if records[2].Name != "" {
		t.Fatalf("records = %#v", records)
	}
}

func TestBindProjectMeshNamesFromSlotReferencesUsesSharedIdentity(
	t *testing.T,
) {
	slots := &ProjectSlotDirectory{
		Records: []ProjectSlotRecord{
			{Name: "guangsu_01", NameReference: 154},
		},
	}
	records := []ProjectMeshAttachmentRecord{
		{Name: "wrong-heuristic", NameReference: 154},
		{NameReference: 154},
	}
	bindProjectMeshNamesFromSlotReferences(slots, records)
	for _, record := range records {
		if record.Name != "guangsu_01" {
			t.Fatalf("records = %#v", records)
		}
	}
}

func TestReadProjectMeshAttachmentPath(t *testing.T) {
	tests := []struct {
		name          string
		field         []byte
		wantPath      string
		wantReference int
	}{
		{
			name:     "inline",
			field:    []byte{0x0e, 0x01, 'g', 'l', 'o', 0xf7, 0x0c, 0x11},
			wantPath: "glow",
		},
		{
			name:          "reference",
			field:         []byte{0x0e, 0xde, 0x06, 0x0c, 0x11},
			wantReference: 862,
		},
		{
			name:  "empty",
			field: []byte{0x0e, 0x01, 0x81, 0x0c, 0x11},
		},
		{
			name:     "single character",
			field:    []byte{0x0e, 0x01, 0x82, 'b', 0x0c, 0x11},
			wantPath: "b",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, reference, ok := readProjectMeshAttachmentPath(
				test.field,
				len(test.field)-1,
			)
			if !ok || path != test.wantPath ||
				reference != test.wantReference {
				t.Fatalf(
					"path = %q, reference = %d, ok = %v",
					path,
					reference,
					ok,
				)
			}
		})
	}
}

func TestResolveProjectMeshReferencedPathsUsesUniqueGeometry(t *testing.T) {
	records := []ProjectMeshAttachmentRecord{
		{
			Path:   "glow2_00000",
			UVs:    []float32{0, 0, 1, 1},
			Width:  256,
			Height: 256,
		},
		{
			PathReference: 862,
			UVs:           []float32{0, 0, 1, 1},
			Width:         256,
			Height:        256,
		},
	}
	resolveProjectMeshReferencedPaths(records)
	if records[1].Path != "glow2_00000" {
		t.Fatalf("records = %#v", records)
	}
}
