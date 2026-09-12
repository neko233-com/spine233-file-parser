package spineparser

import "testing"

func TestDiscoverProjectAnimationDuration(t *testing.T) {
	payload := append([]byte{}, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x01)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x0a)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("idle")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x00, 0x01, 0x02)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 0)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 45)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, 0x07, 0x01, 0x01)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 60)

	duration, err := DiscoverProjectAnimationDuration(payload, "idle")
	if err != nil {
		t.Fatal(err)
	}
	if duration.TimelineCount != 2 || duration.KeyCount != 3 ||
		duration.LastFrame != 60 || duration.Duration != 2 {
		t.Fatalf("duration = %#v", duration)
	}
}

func TestDiscoverProjectAnimationDurationRejectsIncompleteTimelines(t *testing.T) {
	animationPayload := func() []byte {
		payload := append([]byte{}, modernAnimationHeaderPrefix...)
		payload = append(payload, 0x01)
		payload = append(payload, modernAnimationHeaderSuffix...)
		payload = append(payload, 0x0a)
		payload = append(payload, modernAnimationHeaderTail...)
		payload = append(payload, kryoASCIIForTest("idle")...)
		return append(payload, modernAnimationValuePrefix...)
	}

	t.Run("key count mismatch", func(t *testing.T) {
		payload := append(animationPayload(), projectTimelinePrefix...)
		payload = append(payload, 0x00, 0x01, 0x02)
		payload = append(payload, projectTimelineKeyPrefix...)
		payload = appendFloat32ForTest(payload, 30)
		if _, err := DiscoverProjectAnimationDuration(payload, "idle"); err == nil {
			t.Fatal("expected mismatched key count to fail")
		}
	})

	t.Run("frames out of order", func(t *testing.T) {
		payload := append(animationPayload(), projectTimelinePrefix...)
		payload = append(payload, 0x00, 0x01, 0x02)
		payload = append(payload, projectTimelineKeyPrefix...)
		payload = appendFloat32ForTest(payload, 30)
		payload = append(payload, projectTimelineKeyPrefix...)
		payload = appendFloat32ForTest(payload, 15)
		if _, err := DiscoverProjectAnimationDuration(payload, "idle"); err == nil {
			t.Fatal("expected out-of-order frames to fail")
		}
	})
}
