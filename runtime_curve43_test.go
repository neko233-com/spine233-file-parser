package spineparser

import "testing"

func TestConstantBoneCurveUsesFullBezierBound(t *testing.T) {
	timeline := Animation43Timeline{Category: "bones", Kind: "translate"}
	key := map[string]any{"x": 0.0, "y": 0.0, "curve": []any{0.2, 1e-6, 0.8, -1e-6, 0.2, 0.0, 0.8, 0.0}}
	next := map[string]any{"time": 1.0, "x": 0.0, "y": 1e-6}
	flat, err := Animation43ConstantBoneCurve(timeline, key, next, 1e-5)
	if err != nil || !flat {
		t.Fatalf("bounded near-constant: %v %v", flat, err)
	}
	for _, change := range []string{"overshoot", "second-channel", "endpoint", "time-control", "unknown"} {
		t.Run(change, func(t *testing.T) {
			curve := append([]any(nil), key["curve"].([]any)...)
			k := map[string]any{"x": 0.0, "y": 0.0, "curve": curve}
			n := map[string]any{"time": 1.0, "x": 0.0, "y": 0.0}
			switch change {
			case "overshoot":
				curve[1] = 0.2
			case "second-channel":
				curve[5] = 0.2
			case "endpoint":
				n["x"] = 0.2
			case "time-control":
				curve[0] = -0.2
			case "unknown":
				k["curve"] = map[string]any{"x": 0.2, "y": 0.0}
			}
			flat, err := Animation43ConstantBoneCurve(timeline, k, n, 1e-5)
			if err != nil || flat {
				t.Fatalf("unsafe curve flattened: %v %v", flat, err)
			}
		})
	}
}

func TestConstantBoneCurveDefaultsAndToleranceCeiling(t *testing.T) {
	timeline := Animation43Timeline{Category: "bones", Kind: "scale"}
	key := map[string]any{"curve": []any{0.2, 1.0, 0.8, 1.0, 0.2, 1.0, 0.8, 1.0}}
	next := map[string]any{"time": 1.0}
	flat, err := Animation43ConstantBoneCurve(timeline, key, next, 0)
	if err != nil || !flat {
		t.Fatalf("scale defaults: %v %v", flat, err)
	}
	if _, err := Animation43ConstantBoneCurve(timeline, key, next, 1e-3); err == nil {
		t.Fatal("loose tolerance accepted")
	}
	timeline.Category = "physics"
	flat, err = Animation43ConstantBoneCurve(timeline, key, next, 1e-5)
	if err != nil || flat {
		t.Fatal("unknown category flattened")
	}
}
