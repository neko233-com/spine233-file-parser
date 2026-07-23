package spineparser

import "testing"

func TestDiscoverAndPatchProjectSlotAttachmentTimelines(t *testing.T) {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x09)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("blink")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectBoneTimelineGroupPrefix...)
	payload = appendPositiveVarintForTest(payload, 299)
	payload = append(payload, 0x01)
	payload = appendPositiveVarintForTest(payload, 14)
	payload = append(payload, projectBoneTimelineMapPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, projectTimelinePrefix...)
	payload = appendPositiveVarintForTest(payload, 300)
	payload = append(payload, projectTimelineAttachment, 0x01)
	payload = appendPositiveVarintForTest(payload, 3)
	payload = appendAttachmentKeyForTest(payload, 300, 301, 14, 0, 46, 287)
	payload = appendAttachmentKeyForTest(payload, 300, 301, 16, 0, 0)
	payload = appendAttachmentKeyForTest(payload, 300, 301, 55, 0, 46, 287)

	directory, err := DiscoverProjectSlotAttachmentTimelines(payload, "blink")
	if err != nil {
		t.Fatal(err)
	}
	if len(directory.Timelines) != 1 ||
		directory.Timelines[0].SlotReference != 14 ||
		len(directory.Timelines[0].Keys) != 3 ||
		directory.Timelines[0].Keys[2].Frame != 55 {
		t.Fatalf("directory = %#v", directory)
	}

	document := &ProjectDocument{Payload: payload}
	patched, report, err := PatchProjectSlotAttachmentFrames(
		document,
		ProjectSlotAttachmentPatch{
			Animation:       "blink",
			TargetAnimation: "blink-agent",
			Edits: []ProjectSlotAttachmentFrameEdit{
				{
					SlotReference:     14,
					TimelineReference: 300,
					TimelineOffset:    directory.Timelines[0].Offset,
					KeyIndex:          1,
					From:              16,
					To:                18,
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Changes) != 1 || report.Changes[0].To != 18 {
		t.Fatalf("report = %#v", report)
	}
	if string(document.Payload) != string(payload) {
		t.Fatal("input document was mutated")
	}
	rediscovered, err := DiscoverProjectSlotAttachmentTimelines(
		patched.Payload,
		"blink-agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if rediscovered.Timelines[0].Keys[1].Frame != 18 {
		t.Fatalf("rediscovered = %#v", rediscovered)
	}
}

func TestPatchProjectSlotAttachmentFramesRejectsOrderChange(t *testing.T) {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x09)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("blink")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectBoneTimelineGroupPrefix...)
	payload = appendPositiveVarintForTest(payload, 299)
	payload = append(payload, 0x01, 0x0e)
	payload = append(payload, projectBoneTimelineMapPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, projectTimelinePrefix...)
	payload = appendPositiveVarintForTest(payload, 300)
	payload = append(payload, projectTimelineAttachment, 0x01, 0x02)
	payload = appendAttachmentKeyForTest(payload, 300, 301, 14, 0, 0)
	payload = appendAttachmentKeyForTest(payload, 300, 301, 16, 0, 0)

	_, _, err := PatchProjectSlotAttachmentFrames(
		&ProjectDocument{Payload: payload},
		ProjectSlotAttachmentPatch{
			Animation: "blink",
			Edits: []ProjectSlotAttachmentFrameEdit{
				{
					SlotReference:     14,
					TimelineReference: 300,
					TimelineOffset:    mustAttachmentTimelineOffsetForTest(t, payload),
					KeyIndex:          1,
					From:              16,
					To:                13,
				},
			},
		},
	)
	if err == nil {
		t.Fatal("expected frame-order error")
	}
}

func mustAttachmentTimelineOffsetForTest(t *testing.T, payload []byte) int {
	t.Helper()
	directory, err := DiscoverProjectSlotAttachmentTimelines(payload, "blink")
	if err != nil {
		t.Fatal(err)
	}
	return directory.Timelines[0].Offset
}

func appendAttachmentKeyForTest(
	output []byte,
	timelineReference int,
	keyReference int,
	frame float32,
	firstReference int,
	classID int,
	objectReferences ...int,
) []byte {
	output = append(output, projectTimelineKeyPrefix...)
	output = appendPositiveVarintForTest(output, timelineReference)
	output = appendPositiveVarintForTest(output, keyReference)
	output = appendFloat32ForTest(output, frame)
	output = appendPositiveVarintForTest(output, firstReference)
	output = appendPositiveVarintForTest(output, classID)
	for _, reference := range objectReferences {
		output = appendPositiveVarintForTest(output, reference)
	}
	return output
}
