package spineparser

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestReadProjectDrawOrderKeysV2(t *testing.T) {
	payload := []byte{0x85, 0x01, 0x01}
	frame := make([]byte, 4)
	binary.BigEndian.PutUint32(frame, math.Float32bits(6))
	payload = append(payload, frame...)
	payload = append(payload,
		0x00, 0x01, 0x02,
		0x27, 0x01, 0x02, 0x00, 0x86, 0x03, 0x01, 0x14,
		0x27, 0x01, 0x02, 0x00, 0x93, 0x04, 0x01, 0x0f,
		0x01, 0x00, 0x04, 0x00,
	)
	for _, suffix := range []byte{0x00, 0x01} {
		payload[len(payload)-1] = suffix
		keys, next, ok := readProjectDrawOrderKeysV2(
			payload,
			0,
			len(payload),
			1,
			map[int]string{390: "R_hand", 531: "R_arm_2"},
		)
		if !ok || next != len(payload) || len(keys) != 1 {
			t.Fatalf("draw-order decode = %#v, next=%d, ok=%v", keys, next, ok)
		}
		if keys[0].Time != 0.2 || len(keys[0].Offsets) != 2 {
			t.Fatalf("draw-order key = %#v", keys[0])
		}
		if keys[0].Offsets[0].SlotName != "R_hand" ||
			keys[0].Offsets[0].Offset != 10 ||
			keys[0].Offsets[1].SlotName != "R_arm_2" ||
			keys[0].Offsets[1].Offset != -8 {
			t.Fatalf("draw-order offsets = %#v", keys[0].Offsets)
		}
	}
}
