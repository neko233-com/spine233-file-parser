package spineparser

import (
	"encoding/json"
	"testing"
)

const semanticFixture = `{
  "skeleton":{"spine":"4.3.23","images":"./images/"},
  "bones":[{"name":"root"},{"name":"hip","parent":"root"}],
  "slots":[{"name":"body","bone":"hip"}],
  "ik":[{"name":"leg-ik","target":"root","bones":["hip"]}],
  "skins":[{"name":"default","attachments":{"body":{"body":{"type":"mesh"}}}}],
  "events":{"step":{"int":1}},
  "animations":{"walk":{"bones":{"hip":{"rotate":[{"time":0,"value":0},{"time":1,"value":5}]}},"events":[{"time":0.5,"name":"step"}]}}
}`

func TestAnalyzeJSON(t *testing.T) {
	analysis, err := AnalyzeJSON([]byte(semanticFixture))
	if err != nil {
		t.Fatal(err)
	}
	if analysis.SpineVersion != "4.3.23" || analysis.Counts["bones"] != 2 {
		t.Fatalf("analysis = %#v", analysis)
	}
	if analysis.AttachmentTypes["mesh"] != 1 || analysis.TimelineTypes["bones.rotate"] != 2 {
		t.Fatalf("feature counts = %#v %#v", analysis.AttachmentTypes, analysis.TimelineTypes)
	}
}

func TestValidateSemanticJSON(t *testing.T) {
	report, err := ValidateSemanticJSON([]byte(semanticFixture))
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid {
		t.Fatalf("issues = %#v", report.Issues)
	}
	broken := []byte(`{"bones":[{"name":"root"}],"slots":[{"name":"bad","bone":"missing"}]}`)
	report, err = ValidateSemanticJSON(broken)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Issues) != 1 {
		t.Fatalf("broken report = %#v", report)
	}
}

func TestCloneAnimation(t *testing.T) {
	encoded, result, err := CloneAnimation([]byte(semanticFixture), CloneAnimationOptions{
		Source:      "walk",
		Target:      "agent/bouncy-walk",
		TimeScale:   2,
		MarkerEvent: "agent-generated",
		BoneMotions: []BoneMotion{{
			Bone: "hip",
			Translate: []TranslateKey{
				{Time: 0, Y: 0},
				{Time: 0.5, Y: 8},
				{Time: 1, Y: 0},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Duration != 2 || result.Target != "agent/bouncy-walk" {
		t.Fatalf("result = %#v", result)
	}
	var root map[string]any
	if err := json.Unmarshal(encoded, &root); err != nil {
		t.Fatal(err)
	}
	animations := root["animations"].(map[string]any)
	if _, exists := animations["walk"]; !exists {
		t.Fatal("source animation was removed")
	}
	if _, exists := animations["agent/bouncy-walk"]; !exists {
		t.Fatal("target animation missing")
	}
}

func TestCloneAnimationRejectsUnknownBone(t *testing.T) {
	_, _, err := CloneAnimation([]byte(semanticFixture), CloneAnimationOptions{
		Source: "walk", Target: "bad",
		BoneMotions: []BoneMotion{{Bone: "missing", Rotate: []RotateKey{{Value: 1}}}},
	})
	if err == nil {
		t.Fatal("expected unknown bone error")
	}
}
