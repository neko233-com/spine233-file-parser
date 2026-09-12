package spineparser

import (
	"bytes"
	"testing"
)

func TestDiscoverAndPatchProjectEventFrames(t *testing.T) {
	payload := projectEventPayloadForTest()
	directory, err := DiscoverProjectEventTimelines(payload, "run")
	if err != nil {
		t.Fatal(err)
	}
	if len(directory.Timelines) != 1 {
		t.Fatalf("timelines = %#v", directory.Timelines)
	}
	timeline := directory.Timelines[0]
	if timeline.TimelineReference != 42 ||
		timeline.KeyReference != 43 ||
		len(timeline.Keys) != 2 ||
		timeline.Keys[0].Frame != 8 ||
		timeline.Keys[1].Frame != 16 {
		t.Fatalf("timeline = %#v", timeline)
	}

	original := append([]byte(nil), payload...)
	patched, report, err := PatchProjectEventFrames(
		&ProjectDocument{Payload: payload},
		ProjectEventPatch{
			Animation:       "run",
			TargetAnimation: "run-agent",
			Edits: []ProjectEventFrameEdit{{
				TimelineReference: timeline.TimelineReference,
				KeyReference:      timeline.KeyReference,
				TimelineOffset:    timeline.Offset,
				KeyIndex:          1,
				From:              16,
				To:                18,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, original) {
		t.Fatal("input payload was mutated")
	}
	if len(report.Changes) != 1 ||
		report.Changes[0].From != 16 ||
		report.Changes[0].To != 18 ||
		report.TargetAnimation != "run-agent" {
		t.Fatalf("report = %#v", report)
	}
	rediscovered, err := DiscoverProjectEventTimelines(
		patched.Payload,
		"run-agent",
	)
	if err != nil {
		t.Fatal(err)
	}
	if rediscovered.Timelines[0].Keys[1].Frame != 18 {
		t.Fatalf("rediscovered = %#v", rediscovered)
	}
}

func TestPatchProjectEventFramesFailsClosed(t *testing.T) {
	payload := projectEventPayloadForTest()
	directory, err := DiscoverProjectEventTimelines(payload, "run")
	if err != nil {
		t.Fatal(err)
	}
	timeline := directory.Timelines[0]
	base := ProjectEventFrameEdit{
		TimelineReference: timeline.TimelineReference,
		KeyReference:      timeline.KeyReference,
		TimelineOffset:    timeline.Offset,
		KeyIndex:          1,
		From:              16,
		To:                18,
	}
	tests := []struct {
		name string
		edit ProjectEventFrameEdit
	}{
		{
			name: "wrong timeline offset",
			edit: func() ProjectEventFrameEdit {
				edit := base
				edit.TimelineOffset++
				return edit
			}(),
		},
		{
			name: "wrong timeline reference",
			edit: func() ProjectEventFrameEdit {
				edit := base
				edit.TimelineReference++
				return edit
			}(),
		},
		{
			name: "wrong key reference",
			edit: func() ProjectEventFrameEdit {
				edit := base
				edit.KeyReference++
				return edit
			}(),
		},
		{
			name: "stale frame",
			edit: func() ProjectEventFrameEdit {
				edit := base
				edit.From = 15
				return edit
			}(),
		},
		{
			name: "break frame order",
			edit: func() ProjectEventFrameEdit {
				edit := base
				edit.To = 7
				return edit
			}(),
		},
		{
			name: "no change",
			edit: func() ProjectEventFrameEdit {
				edit := base
				edit.To = edit.From
				return edit
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := append([]byte(nil), payload...)
			if _, _, err := PatchProjectEventFrames(
				&ProjectDocument{Payload: payload},
				ProjectEventPatch{
					Animation: "run",
					Edits:     []ProjectEventFrameEdit{test.edit},
				},
			); err == nil {
				t.Fatal("expected event patch error")
			}
			if !bytes.Equal(payload, original) {
				t.Fatal("rejected patch mutated input")
			}
		})
	}
}

func TestDiscoverProjectEventTimelinesRejectsInvalidKeys(t *testing.T) {
	payload := projectEventPayloadForTest()
	directory, err := DiscoverProjectEventTimelines(payload, "run")
	if err != nil {
		t.Fatal(err)
	}
	firstFrame := directory.Timelines[0].Keys[0].FrameOffset
	secondFrame := directory.Timelines[0].Keys[1].FrameOffset
	copy(
		payload[secondFrame:secondFrame+4],
		payload[firstFrame:firstFrame+4],
	)
	if _, err := DiscoverProjectEventTimelines(payload, "run"); err == nil {
		t.Fatal("expected non-increasing event frame rejection")
	}
}

func TestDiscoverProjectEventTimelineV2DefaultPayload(t *testing.T) {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x09)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("skill_0")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, projectTimelineEvent, 0x01, 0x01)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 13)
	payload = append(payload, projectEventDefaultPayloadPrefixV2...)
	payload = append(payload, kryoASCIIForTest("event_skill0")...)
	payload = append(payload, projectEventDefaultPayloadSuffixV2...)

	directory, err := DiscoverProjectEventTimelines(payload, "skill_0")
	if err != nil {
		t.Fatal(err)
	}
	if len(directory.Timelines) != 1 {
		t.Fatalf("timelines = %#v", directory.Timelines)
	}
	timeline := directory.Timelines[0]
	if timeline.TimelineReference != 0 ||
		timeline.KeyReference != 0 ||
		len(timeline.Keys) != 1 ||
		timeline.Keys[0].Name != "event_skill0" ||
		timeline.Keys[0].Frame != 13 {
		t.Fatalf("timeline = %#v", timeline)
	}
}

func TestDiscoverProjectEventTriggerTime(t *testing.T) {
	payload := projectEventPayloadV2ForTest()
	trigger, err := DiscoverProjectEventTriggerTime(payload, "skill_0", "event_skill0")
	if err != nil {
		t.Fatal(err)
	}
	if trigger.MatchCount != 1 || trigger.Time != 13.0/30.0 || trigger.FrameRate != 30 {
		t.Fatalf("trigger = %#v", trigger)
	}
	missing, err := DiscoverProjectEventTriggerTime(payload, "skill_0", "event_missing")
	if err != nil {
		t.Fatal(err)
	}
	if missing.MatchCount != 0 {
		t.Fatalf("missing trigger = %#v", missing)
	}
	if _, err := DiscoverProjectEventTriggerTime(projectEventPayloadForTest(), "run", "event_skill0"); err == nil {
		t.Fatal("expected unnamed event keys to fail closed")
	}
}

func TestDiscoverProjectEventTriggerTimeRejectsDuplicate(t *testing.T) {
	payload := projectEventPayloadV2ForTest()
	// The V2 helper encodes one key. Change the key count to two and append a
	// second event key to verify duplicate matching.
	first := append([]byte(nil), payload...)
	header := bytes.Index(first, []byte{projectTimelineEvent, 0x01, 0x01})
	if header < 0 {
		t.Fatal("event timeline header not found")
	}
	first[header+2] = 0x02
	first = append(first, projectTimelineKeyPrefix...)
	first = appendFloat32ForTest(first, 26)
	first = append(first, projectEventDefaultPayloadPrefixV2...)
	first = append(first, kryoASCIIForTest("event_skill0")...)
	first = append(first, projectEventDefaultPayloadSuffixV2...)
	if _, err := DiscoverProjectEventTriggerTime(first, "skill_0", "event_skill0"); err == nil {
		t.Fatal("expected duplicate event trigger rejection")
	}
}

func TestDiscoverProjectEventTimelineV2RejectsUnknownPayload(t *testing.T) {
	payload := projectEventPayloadV2ForTest()
	directory, err := DiscoverProjectEventTimelines(payload, "skill_0")
	if err != nil {
		t.Fatal(err)
	}
	payload[directory.Timelines[0].Keys[0].FrameOffset+8] = 1
	if _, err := DiscoverProjectEventTimelines(payload, "skill_0"); err == nil {
		t.Fatal("expected unknown V2 event payload rejection")
	}
}

func projectEventPayloadV2ForTest() []byte {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x09)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("skill_0")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, projectTimelineEvent, 0x01, 0x01)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 13)
	payload = append(payload, projectEventDefaultPayloadPrefixV2...)
	payload = append(payload, kryoASCIIForTest("event_skill0")...)
	payload = append(payload, projectEventDefaultPayloadSuffixV2...)
	return payload
}

func projectEventPayloadForTest() []byte {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x09)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("run")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectTimelinePrefix...)
	payload = appendPositiveVarintForTest(payload, 42)
	payload = append(payload, projectTimelineEvent, 0x01, 0x02)
	payload = appendProjectEventKeyForTest(payload, 42, 43, 8, []byte{
		0x00, 0x01, 0x06, 0x01, 0x01, 0x0a, 0x00, 0x03,
	})
	payload = appendProjectEventKeyForTest(payload, 42, 43, 16, []byte{
		0x00, 0x01, 0x06, 0x01, 0x02, 0x00, 0x03, 0x00,
		0x00, 0x00, 0x00,
	})
	return payload
}

func appendProjectEventKeyForTest(
	output []byte,
	timelineReference int,
	keyReference int,
	frame float32,
	opaque []byte,
) []byte {
	output = append(output, projectTimelineKeyPrefix...)
	output = appendPositiveVarintForTest(output, timelineReference)
	output = appendPositiveVarintForTest(output, keyReference)
	output = appendFloat32ForTest(output, frame)
	output = append(output, opaque...)
	return output
}
