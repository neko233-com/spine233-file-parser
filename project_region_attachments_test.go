package spineparser

import "testing"

func TestReadProjectRegionPathV2(t *testing.T) {
	tests := []struct {
		name          string
		field         []byte
		wantPath      string
		wantReference int
	}{
		{
			name:  "empty",
			field: []byte{0x0d, 0x01, 0x81},
		},
		{
			name:          "reference",
			field:         []byte{0x0d, 0xba, 0x01},
			wantReference: 186,
		},
		{
			name:     "single character",
			field:    []byte{0x0d, 0x01, 0x82, 'b'},
			wantPath: "b",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, reference, ok := readProjectRegionPathV2(
				test.field,
				0,
				len(test.field),
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

func TestResolveProjectRegionReferencedNamesUsesGeometryIdentity(
	t *testing.T,
) {
	records := []ProjectRegionAttachmentRecord{
		{
			Name:   "d31/",
			Width:  145,
			Height: 182,
		},
		{
			NameReference: 188,
			Width:         145,
			Height:        182,
		},
	}
	resolveProjectRegionReferencedNames(records)
	if records[1].Name != "d31/" {
		t.Fatalf("records = %#v", records)
	}
}

func TestResolveProjectRegionReferencedPathsUsesInlineNameEvidence(
	t *testing.T,
) {
	records := []ProjectRegionAttachmentRecord{
		{
			Name:          "glow1/glow1_",
			PathReference: 186,
		},
		{
			Name:          "d31/",
			NameReference: 188,
			PathReference: 186,
		},
	}
	resolveProjectRegionReferencedPaths(records)
	for _, record := range records {
		if record.Path != "glow1/glow1_" {
			t.Fatalf("records = %#v", records)
		}
	}
}
