package spineparser

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestDiscoverProjectBoneTimelineGroupsCompactV2UsesTerminalOwner(t *testing.T) {
	wrapper := []byte{0x13, 0x01, 0x04, 0x07, 0x06, 0x02, 0x0f, 0x01, 0x01}
	payload := append([]byte(nil), wrapper...)
	firstOffset := len(payload)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x07, 0x04, 0x00)
	payload = append(payload, wrapper...)
	secondOffset := len(payload)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x09, 0x04, 0x00)

	groups := discoverProjectBoneTimelineGroupsCompactV2(
		payload,
		0,
		len(payload),
	)
	if len(groups) != 2 ||
		groups[0].Offset != firstOffset || groups[0].BoneReference != 7 ||
		groups[1].Offset != secondOffset || groups[1].BoneReference != 9 {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestDiscoverProjectBoneTimelineGroupsCompactV2AcceptsOwnerSuffixOne(
	t *testing.T,
) {
	wrapper := []byte{0x13, 0x01, 0x04, 0x07, 0x06, 0x02, 0x0f, 0x01, 0x01}
	payload := append([]byte(nil), wrapper...)
	firstOffset := len(payload)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x07, 0x04, 0x01)
	payload = append(payload, wrapper...)
	secondOffset := len(payload)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x09, 0x04, 0x01)

	groups := discoverProjectBoneTimelineGroupsCompactV2(
		payload,
		0,
		len(payload),
	)
	if len(groups) != 2 ||
		groups[0].Offset != firstOffset || groups[0].BoneReference != 7 ||
		groups[1].Offset != secondOffset || groups[1].BoneReference != 9 {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestDiscoverProjectBoneTimelineGroupsCompactV2SkipsNonTransformWrapper(
	t *testing.T,
) {
	wrapper := []byte{0x13, 0x01, 0x04, 0x07, 0x06, 0x02, 0x0f, 0x01, 0x01}
	payload := append([]byte(nil), wrapper...)
	transformOffset := len(payload)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x07, 0x04, 0x00)
	payload = append(payload, wrapper...)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x20, 0x01, 0x01)
	payload = append(payload, 0x01, 0x00, 0x04, 0x01)

	groups := discoverProjectBoneTimelineGroupsCompactV2(
		payload,
		0,
		len(payload),
	)
	if len(groups) != 1 ||
		groups[0].Offset != transformOffset ||
		groups[0].BoneReference != 7 {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestDiscoverProjectBoneTimelineGroupsV2AcceptsBothOwnerFlags(
	t *testing.T,
) {
	payload := append([]byte(nil), projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x07)
	payload = append(payload, 0x04, 0x00, 0x13, 0x01, 0x04, 0x07)
	secondOffset := len(payload) - 4
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x09, 0x04, 0x01)

	groups := discoverProjectBoneTimelineGroupsV2(
		payload,
		0,
		len(payload),
	)
	if len(groups) != 2 ||
		groups[0].Offset != 0 || groups[0].BoneReference != 7 ||
		groups[1].Offset != secondOffset ||
		groups[1].BoneReference != 9 {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestFindProjectV2GroupTerminalReferenceUsesLastOwner(t *testing.T) {
	payload := append([]byte(nil), projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = append(payload, 0x01, 0x42, 0x04, 0x01)
	payload = append(payload, 0x00, 0x00)
	payload = append(payload, 0x01, 0x49, 0x04, 0x00)

	reference := findProjectV2GroupTerminalReference(
		payload,
		0,
		len(payload),
	)
	if reference != 0x49 {
		t.Fatalf("reference = %d", reference)
	}
}

func TestDiscoverAndPatchProjectRotateTimelines(t *testing.T) {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x09)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("attack")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectBoneTimelineGroupPrefix...)
	payload = appendPositiveVarintForTest(payload, 731)
	payload = append(payload, 0x01)
	payload = appendPositiveVarintForTest(payload, 6)
	payload = append(payload, projectBoneTimelineMapPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, projectTimelinePrefix...)
	payload = appendPositiveVarintForTest(payload, 272)
	payload = append(payload, 0x00, 0x01)
	payload = appendPositiveVarintForTest(payload, 2)
	payload = appendRotateKeyForTest(payload, 272, 273, 0, 2.2)
	payload = appendRotateKeyForTest(payload, 272, 273, 2, 13.22)

	directory, err := DiscoverProjectRotateTimelines(payload, "attack")
	if err != nil {
		t.Fatal(err)
	}
	if directory.FrameRate != 30 || len(directory.Timelines) != 1 {
		t.Fatalf("directory = %#v", directory)
	}
	timeline := directory.Timelines[0]
	if timeline.BoneReference != 6 || len(timeline.Keys) != 2 ||
		timeline.Keys[1].Frame != 2 || timeline.Keys[1].Value != float32(13.22) {
		t.Fatalf("timeline = %#v", timeline)
	}

	document := &ProjectDocument{Payload: payload}
	patched, report, err := PatchProjectRotateValues(document, ProjectRotatePatch{
		Animation:       "attack",
		TargetAnimation: "attack-agent",
		Edits: []ProjectRotateValueEdit{
			{BoneReference: 6, KeyIndex: 1, From: 13.22, To: 24},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changes) != 1 || report.Changes[0].Frame != 2 {
		t.Fatalf("report = %#v", report)
	}
	if string(document.Payload) != string(payload) {
		t.Fatal("input document was mutated")
	}
	rediscovered, err := DiscoverProjectRotateTimelines(
		patched.Payload,
		"attack-agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := rediscovered.Timelines[0].Keys[1].Value; got != 24 {
		t.Fatalf("patched value = %v, want 24", got)
	}
}

func TestPatchProjectRotateTimelinesFailsClosed(t *testing.T) {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x09)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("attack")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectBoneTimelineGroupPrefix...)
	payload = appendPositiveVarintForTest(payload, 731)
	payload = append(payload, 0x01, 0x06)
	payload = append(payload, projectBoneTimelineMapPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, projectTimelinePrefix...)
	payload = appendPositiveVarintForTest(payload, 272)
	payload = append(payload, 0x00, 0x01, 0x01)
	payload = appendRotateKeyForTest(payload, 272, 273, 0, 2.2)

	_, _, err := PatchProjectRotateValues(
		&ProjectDocument{Payload: payload},
		ProjectRotatePatch{
			Animation: "attack",
			Edits: []ProjectRotateValueEdit{
				{BoneReference: 6, KeyIndex: 0, From: 99, To: 24},
			},
		},
	)
	if err == nil {
		t.Fatal("expected stale-value error")
	}
}

func appendPositiveVarintForTest(output []byte, value int) []byte {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value == 0 {
			return append(output, current)
		}
		output = append(output, current|0x80)
	}
}

func appendRotateKeyForTest(
	output []byte,
	timelineReference int,
	keyReference int,
	frame float32,
	value float32,
) []byte {
	output = append(output, projectTimelineKeyPrefix...)
	output = appendPositiveVarintForTest(output, timelineReference)
	output = appendPositiveVarintForTest(output, keyReference)
	output = appendFloat32ForTest(output, frame)
	output = appendFloat32ForTest(output, value)
	for range 4 {
		output = appendFloat32ForTest(
			output,
			math.Float32frombits(binary.BigEndian.Uint32([]byte{0x4f, 0, 0, 0})),
		)
	}
	return append(output, 0, 0, 0, 0, 0)
}
