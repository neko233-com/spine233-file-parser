package spineparser

import "testing"

func TestReadProjectSlotColorKeysV2LinearCurves(t *testing.T) {
	payload := appendProjectSlotColorKeyForTest(nil, 0, false, 0)
	payload = appendProjectSlotColorKeyForTest(payload, 2, false, 0)
	payload = append(payload, 0xb6, 0x03)
	keys, next, ok := readProjectSlotColorKeysV2(
		payload,
		0,
		len(payload),
		2,
	)
	if !ok || len(keys) != 2 || next != len(payload)-2 {
		t.Fatalf("keys = %d next = %d ok = %v", len(keys), next, ok)
	}
	if len(keys[0].CurveHeader) != projectSlotColorCurveHeaderBytes ||
		len(keys[0].Curves) != 0 || keys[1].Frame != 2 {
		t.Fatalf("keys = %#v", keys)
	}
}

func TestReadProjectSlotColorKeysV2ExpandedCurves(t *testing.T) {
	payload := appendProjectSlotColorKeyForTest(nil, 0, true, 3)
	payload = appendProjectSlotColorKeyForTest(payload, 9, true, 6)
	payload = append(payload, 0x07)
	keys, next, ok := readProjectSlotColorKeysV2(
		payload,
		0,
		len(payload),
		2,
	)
	if !ok || len(keys) != 2 || next != len(payload)-1 {
		t.Fatalf("keys = %d next = %d ok = %v", len(keys), next, ok)
	}
	if len(keys[0].Curves) != projectSlotColorCurveGroupCount ||
		keys[0].Curves[0][0] != 3 ||
		keys[1].Curves[0][0] != 6 {
		t.Fatalf("curves = %#v / %#v", keys[0].Curves, keys[1].Curves)
	}
}

func TestReadProjectSlotColorKeysV2MixedCurves(t *testing.T) {
	payload := appendProjectSlotColorKeyForTest(nil, 0, false, 0)
	payload = appendProjectSlotColorKeyForTest(payload, 2, true, 6)
	keys, next, ok := readProjectSlotColorKeysV2(
		payload,
		0,
		len(payload),
		2,
	)
	if !ok || len(keys) != 2 || next != len(payload) {
		t.Fatalf("keys = %d next = %d ok = %v", len(keys), next, ok)
	}
	if len(keys[0].Curves) != 0 ||
		len(keys[1].Curves) != projectSlotColorCurveGroupCount ||
		keys[1].Curves[0][0] != 6 {
		t.Fatalf("curves = %#v / %#v", keys[0].Curves, keys[1].Curves)
	}
}

func TestReadProjectSlotColorKeysV2DoesNotInheritExpandedLastKey(t *testing.T) {
	payload := appendProjectSlotColorKeyForTest(nil, 0, true, 3)
	payload = appendProjectSlotColorKeyForTest(payload, 9, false, 0)
	payload = append(payload, 0xb6, 0x03)
	keys, next, ok := readProjectSlotColorKeysV2(
		payload,
		0,
		len(payload),
		2,
	)
	if !ok || len(keys) != 2 || next != len(payload)-2 {
		t.Fatalf("keys = %d next = %d ok = %v", len(keys), next, ok)
	}
	if len(keys[0].Curves) != projectSlotColorCurveGroupCount ||
		len(keys[1].Curves) != 0 {
		t.Fatalf("curves = %#v / %#v", keys[0].Curves, keys[1].Curves)
	}
}

func TestDiscoverProjectSlotColorTimelinesReadsTrailingSlotReference(t *testing.T) {
	payload := append([]byte(nil), projectTimelinePrefix...)
	payload = append(payload, projectTimelineRGBA, 0x01, 0x01)
	payload = appendProjectSlotColorKeyForTest(payload, 0, false, 0)
	payload = append(payload, 0xb6, 0x03, 0x01, 0x1b, 0x04, 0x00)
	timelines := discoverProjectSlotColorTimelinesInGroupV2(
		payload,
		0,
		len(payload),
	)
	if len(timelines) != 1 || timelines[0].SlotReference != 438 ||
		len(timelines[0].Keys) != 1 {
		t.Fatalf("timelines = %#v", timelines)
	}
}

func TestBindProjectSlotColorInlineOwnerUsesFirstCreatedSlot(t *testing.T) {
	timelines := []ProjectSlotColorTimeline{
		{inlineSlot: true},
		{SlotReference: 15},
	}
	slots := &ProjectSlotDirectory{
		ReferencesComplete: true,
		Records: []ProjectSlotRecord{
			{WireReference: 15, Name: "s1"},
			{WireReference: 13, Name: "s0"},
		},
	}
	if err := bindProjectSlotColorInlineOwner(timelines, slots); err != nil {
		t.Fatal(err)
	}
	if timelines[0].SlotReference != 13 {
		t.Fatalf("inline reference = %d, want 13", timelines[0].SlotReference)
	}
}

func TestBindProjectSlotColorInlineOwnerRejectsDuplicateReference(t *testing.T) {
	timelines := []ProjectSlotColorTimeline{
		{inlineSlot: true},
		{SlotReference: 13},
	}
	slots := &ProjectSlotDirectory{
		ReferencesComplete: true,
		Records: []ProjectSlotRecord{
			{WireReference: 13, Name: "s0"},
		},
	}
	if err := bindProjectSlotColorInlineOwner(timelines, slots); err == nil {
		t.Fatal("duplicate inline owner reference was accepted")
	}
}

func appendProjectSlotColorKeyForTest(
	output []byte,
	frame float32,
	expanded bool,
	curveValue float32,
) []byte {
	output = append(output, projectTimelineKeyPrefix...)
	output = appendFloat32ForTest(output, frame)
	for component := 0; component < 4; component++ {
		output = appendFloat32ForTest(output, 1)
	}
	header := make([]byte, projectSlotColorCurveHeaderBytes)
	if expanded {
		header[len(header)-1] = 1
	}
	output = append(output, header...)
	if !expanded {
		return output
	}
	for groupIndex := 0; groupIndex < projectSlotColorCurveGroupCount; groupIndex++ {
		for curveIndex := 0; curveIndex < 4; curveIndex++ {
			output = appendFloat32ForTest(output, curveValue+float32(curveIndex))
		}
		output = append(output, 0, 0, 0, 0)
	}
	return output
}
