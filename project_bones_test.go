package spineparser

import "testing"

func TestDiscoverProjectBones(t *testing.T) {
	payload := []byte{0x55}
	payload = append(payload, projectBoneTablePrefix...)
	payload = append(payload, 0x03, 0x0c, 0x01, 0x1d, 0x00, 0x03, 0x01)
	payload = append(payload, 0x01)
	payload = append(payload, kryoASCIIForTest("root")...)
	payload = append(payload, 0x02, 0x01, 0x03, 0x00)
	payload = append(payload, 0x01, 0x1d, 0x00, 0x03, 0x01, 0x01)
	payload = append(payload, kryoASCIIForTest("body")...)
	payload = append(payload, 0x02, 0x00, 0x03, 0x04)
	payload = append(payload, 0x01, 0x11, 0x00, 0x03, 0x01, 0x01)
	payload = append(payload, kryoASCIIForTest("hand")...)
	payload = append(payload, 0x02)
	payload = append(payload, 0x01, 0x1d, 0x00, 0x03, 0x01, 0x56)
	payload = append(payload, 0x02, 0x01, 0x03, 0x05)

	directory, err := DiscoverProjectBones(payload)
	if err != nil {
		t.Fatal(err)
	}
	if directory.Count != 3 || directory.ClassID != 0x1d {
		t.Fatalf("directory = %#v", directory)
	}
	if directory.Records[0].Name != "root" ||
		directory.Records[0].ParentToken != 0 ||
		!directory.Records[0].Visible ||
		directory.Records[1].Name != "body" ||
		directory.Records[1].ParentToken != 4 ||
		!directory.Records[1].Visible ||
		directory.Records[2].Name != "hand" ||
		directory.Records[2].ParentToken != 5 ||
		!directory.Records[2].Visible ||
		directory.Records[2].NameEncoding != "wrapper-reference" {
		t.Fatalf("records = %#v", directory.Records)
	}
}

func TestDiscoverProjectBonesRejectsInvalidPayload(t *testing.T) {
	if _, err := DiscoverProjectBones([]byte("not a project")); err == nil {
		t.Fatal("expected unsupported bone-table error")
	}
}

func TestParseProjectBoneSetupV2ReadsColor(t *testing.T) {
	payload := make([]byte, 0, 64)
	payload = append(payload, 0, 0, 0, 0)
	payload = append(payload, 0x17, 0, 0, 0, 0)
	payload = append(payload, 0x22, 0, 0, 0, 0)
	payload = append(payload, 0x21, 0, 0, 0, 0)
	payload = append(payload, 0x11, 0x1e, 0x01, 0xff, 0, 0, 0xff)
	payload = append(payload, 0x19, 0, 0, 0, 0)
	payload = append(payload, 0x0d, 0, 0, 0x80, 0x3f)
	payload = append(payload, 0x0a, 0, 0, 0, 0)
	payload = append(payload, 0x0c, 0, 0, 0, 0)
	payload = append(payload, 0x0b, 0, 0, 0, 0)
	payload = append(payload, 0x0e, 0, 0, 0x80, 0x3f)

	setup, ok := parseProjectBoneSetupV2(payload, 0, len(payload))
	if !ok {
		t.Fatal("bone setup was not parsed")
	}
	if setup.Color != [4]byte{0xff, 0, 0, 0xff} {
		t.Fatalf("color = %x", setup.Color)
	}
}

func TestBindProjectBoneIconsUsesDefaultAndInlineRegistrations(t *testing.T) {
	records := []ProjectBoneRecord{
		{Icon: "ik", iconObject: 0x11, iconInline: true},
		{iconObject: 0x11, iconToken: 0x12},
		{iconObject: 0x11, iconToken: 0x16},
		{iconObject: 0x11, iconToken: 0x16},
		{Icon: "bone", iconObject: 0x11, iconInline: true},
		{iconObject: 0x11, iconToken: 0x7d},
		{Icon: "diamond", iconObject: 0x25, iconInline: true},
		{iconObject: 0x25, iconToken: 0x26},
	}
	bindProjectBoneIcons(records)
	expected := []string{"ik", "", "ik", "ik", "", "", "diamond", ""}
	for index, icon := range expected {
		if records[index].Icon != icon {
			t.Fatalf("records[%d].Icon = %q, want %q", index, records[index].Icon, icon)
		}
	}
}

func TestReadProjectBoneFieldsAcceptsObjectReferenceVariant(t *testing.T) {
	payload := []byte{0xaa, 0x1b, 0x25, 0x04, 0x1c, 0x01, 0x69, 0xeb, 0xbb}
	inherit, icon, object, token, inline, ok := readProjectBoneFields(
		payload,
		0,
		len(payload),
	)
	if !ok || inherit != "noScale" || icon != "ik" ||
		object != 0x25 || token != 0 || !inline {
		t.Fatalf(
			"inherit=%q icon=%q object=%d token=%d inline=%t ok=%t",
			inherit,
			icon,
			object,
			token,
			inline,
			ok,
		)
	}
}

func TestReadProjectBoneFieldsAcceptsLegacyNormalVariant(t *testing.T) {
	payload := []byte{0xaa, 0x1b, 0x25, 0x1c, 0x01, 0x69, 0xeb, 0xbb}
	inherit, icon, _, _, _, ok := readProjectBoneFields(
		payload,
		0,
		len(payload),
	)
	if !ok || inherit != "normal" || icon != "ik" {
		t.Fatalf("inherit=%q icon=%q ok=%t", inherit, icon, ok)
	}
}

func TestBindProjectBoneIconsDoesNotGuessDetachedReference(t *testing.T) {
	records := []ProjectBoneRecord{
		{Icon: "ik", iconObject: 0x25, iconInline: true},
		{iconObject: 0x25, iconToken: 0x26},
		{iconObject: 0x25, iconToken: 0x16},
	}
	bindProjectBoneIcons(records)
	if records[2].Icon != "" {
		t.Fatalf("detached icon reference resolved as %q", records[2].Icon)
	}
}
