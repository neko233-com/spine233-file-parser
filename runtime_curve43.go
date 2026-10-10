package spineparser

import (
	"fmt"
	"math"
)

// Animation43ConstantBoneCurve proves that a known bone curve stays within an
// absolute tolerance of one value for every channel over the whole interval.
// Bezier control points bound its complete value range; sampling is not used.
// Unsupported curve encodings and non-monotone control times stay unchanged.
func Animation43ConstantBoneCurve(t Animation43Timeline, key, next map[string]any, tolerance float64) (bool, error) {
	if !finite43(tolerance) || tolerance < 0 || tolerance > 1e-5 {
		return false, fmt.Errorf("constant curve tolerance must be between 0 and 0.00001")
	}
	if t.Category != "bones" {
		return false, nil
	}
	fields := []string{"value"}
	fallback := 0.0
	switch t.Kind {
	case "translate", "shear":
		fields = []string{"x", "y"}
	case "scale":
		fields = []string{"x", "y"}
		fallback = 1
	case "scalex", "scaley":
		fallback = 1
	case "rotate", "translatex", "translatey", "shearx", "sheary":
	default:
		return false, nil
	}
	value := func(k map[string]any, field string, defaultValue float64) (float64, bool) {
		raw, exists := k[field]
		if !exists {
			return defaultValue, true
		}
		n, ok := numeric(raw)
		return n, ok && finite43(n)
	}
	start, ok := value(key, "time", 0)
	if !ok {
		return false, nil
	}
	end, ok := value(next, "time", 0)
	if !ok || end <= start {
		return false, nil
	}
	curve := key["curve"]
	controls, bezier := curve.([]any)
	if curve != nil && !bezier {
		text, ok := curve.(string)
		if !ok || (text != "linear" && text != "stepped") {
			return false, nil
		}
	}
	if bezier && len(controls) != 4*len(fields) {
		return false, nil
	}
	for i, field := range fields {
		a, ok := value(key, field, fallback)
		if !ok {
			return false, nil
		}
		b, ok := value(next, field, fallback)
		if !ok {
			return false, nil
		}
		lo, hi := math.Min(a, b), math.Max(a, b)
		if bezier {
			x0, ok0 := numeric(controls[4*i])
			y0, ok1 := numeric(controls[4*i+1])
			x1, ok2 := numeric(controls[4*i+2])
			y1, ok3 := numeric(controls[4*i+3])
			if !ok0 || !ok1 || !ok2 || !ok3 || !finite43(x0) || !finite43(y0) || !finite43(x1) || !finite43(y1) {
				return false, nil
			}
			if x0 < start || x1 > end || x1 < x0 {
				return false, nil
			}
			lo, hi = math.Min(lo, math.Min(y0, y1)), math.Max(hi, math.Max(y0, y1))
		}
		if hi-lo > tolerance {
			return false, nil
		}
	}
	return true, nil
}

// NormalizeAnimation43ConstantBoneCurves edits only known curve fields on an
// explicitly caller-owned comparison copy. Times, values, unknown fields and
// nonconstant curves remain untouched. It must not be used to rewrite artwork.
func NormalizeAnimation43ConstantBoneCurves(document map[string]any, tolerance float64) (int, error) {
	count := 0
	animations, _ := document["animations"].(map[string]any)
	for _, raw := range animations {
		animation, ok := raw.(map[string]any)
		if !ok {
			return count, fmt.Errorf("animation must be an object")
		}
		err := VisitAnimation43Timelines(animation, func(t Animation43Timeline) error {
			for i := 0; i+1 < len(t.Keys); i++ {
				key, ok := t.Keys[i].(map[string]any)
				if !ok {
					return fmt.Errorf("%s: key must be an object", t.Path)
				}
				next, ok := t.Keys[i+1].(map[string]any)
				if !ok {
					return fmt.Errorf("%s: key must be an object", t.Path)
				}
				flat, err := Animation43ConstantBoneCurve(t, key, next, tolerance)
				if err != nil {
					return err
				}
				if flat {
					if text, ok := key["curve"].(string); !ok || text != "stepped" {
						key["curve"] = "stepped"
						count++
					}
				}
			}
			return nil
		})
		if err != nil {
			return count, err
		}
	}
	return count, nil
}
