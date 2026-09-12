package spineparser

import "testing"

func TestReadProjectSequenceKeysV2AcceptsProvenDiscreteFlags(t *testing.T) {
	for _, flag := range []byte{0, 2} {
		payload := append([]byte(nil), projectTimelineKeyPrefix...)
		payload = appendProjectTransformTestFloat(payload, 0)
		payload = appendProjectTransformTestFloat(payload, 2)
		payload = appendProjectTransformTestFloat(payload, 0)
		payload = appendProjectTransformTestFloat(payload, 0.05)
		payload = append(payload, flag)

		keys, next, ok := readProjectSequenceKeysV2(
			payload,
			0,
			len(payload),
			1,
		)
		if !ok || next != len(payload) || len(keys) != 1 {
			t.Fatalf(
				"flag %d: keys = %#v, next = %d, ok = %v",
				flag,
				keys,
				next,
				ok,
			)
		}
		if keys[0].Mode != "loop" || keys[0].Delay != 0.05 {
			t.Fatalf("flag %d: key = %#v", flag, keys[0])
		}
	}
}
