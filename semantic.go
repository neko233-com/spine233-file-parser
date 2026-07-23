package spineparser

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ProjectAnalysis is a version-tolerant semantic inventory of exported Spine JSON.
type ProjectAnalysis struct {
	SpineVersion    string         `json:"spineVersion,omitempty"`
	Hash            string         `json:"hash,omitempty"`
	Images          string         `json:"images,omitempty"`
	Audio           string         `json:"audio,omitempty"`
	Bones           []string       `json:"bones,omitempty"`
	Slots           []string       `json:"slots,omitempty"`
	Skins           []string       `json:"skins,omitempty"`
	Animations      []string       `json:"animations,omitempty"`
	Events          []string       `json:"events,omitempty"`
	IKConstraints   []string       `json:"ikConstraints,omitempty"`
	Transform       []string       `json:"transformConstraints,omitempty"`
	PathConstraints []string       `json:"pathConstraints,omitempty"`
	Physics         []string       `json:"physicsConstraints,omitempty"`
	AttachmentTypes map[string]int `json:"attachmentTypes,omitempty"`
	TimelineTypes   map[string]int `json:"timelineTypes,omitempty"`
	Counts          map[string]int `json:"counts"`
}

// ValidationIssue describes one semantic reference problem.
type ValidationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ValidationReport checks stable cross-version Spine JSON invariants.
type ValidationReport struct {
	Valid  bool              `json:"valid"`
	Issues []ValidationIssue `json:"issues,omitempty"`
}

// RotateKey is one Spine bone rotate timeline key.
type RotateKey struct {
	Time  float64 `json:"time,omitempty"`
	Value float64 `json:"value,omitempty"`
	Curve any     `json:"curve,omitempty"`
}

// TranslateKey is one Spine bone translate timeline key.
type TranslateKey struct {
	Time  float64 `json:"time,omitempty"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
	Curve any     `json:"curve,omitempty"`
}

// ScaleKey is one Spine bone scale timeline key.
type ScaleKey struct {
	Time  float64 `json:"time,omitempty"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
	Curve any     `json:"curve,omitempty"`
}

// ShearKey is one Spine bone shear timeline key.
type ShearKey struct {
	Time  float64 `json:"time,omitempty"`
	X     float64 `json:"x,omitempty"`
	Y     float64 `json:"y,omitempty"`
	Curve any     `json:"curve,omitempty"`
}

// BoneMotion replaces selected bone timelines in a cloned animation.
// Nil timeline slices are retained from the source; non-nil slices replace them.
type BoneMotion struct {
	Bone      string         `json:"bone"`
	Rotate    []RotateKey    `json:"rotate,omitempty"`
	Translate []TranslateKey `json:"translate,omitempty"`
	Scale     []ScaleKey     `json:"scale,omitempty"`
	Shear     []ShearKey     `json:"shear,omitempty"`
}

// CloneAnimationOptions creates an editable animation from an existing one.
type CloneAnimationOptions struct {
	Source          string       `json:"source"`
	Target          string       `json:"target"`
	TimeScale       float64      `json:"timeScale,omitempty"`
	BoneMotions     []BoneMotion `json:"boneMotions,omitempty"`
	MarkerEvent     string       `json:"markerEvent,omitempty"`
	ReplaceExisting bool         `json:"replaceExisting,omitempty"`
	Indent          string       `json:"indent,omitempty"`
}

// CloneAnimationResult describes the generated animation.
type CloneAnimationResult struct {
	Source       string   `json:"source"`
	Target       string   `json:"target"`
	Duration     float64  `json:"duration"`
	TimeScale    float64  `json:"timeScale"`
	ChangedBones []string `json:"changedBones,omitempty"`
	MarkerEvent  string   `json:"markerEvent,omitempty"`
}

// AnalyzeJSON inventories all stable and version-specific Spine JSON features.
func AnalyzeJSON(source []byte) (*ProjectAnalysis, error) {
	root, err := decodeSemantic(source)
	if err != nil {
		return nil, err
	}
	analysis := &ProjectAnalysis{
		AttachmentTypes: make(map[string]int),
		TimelineTypes:   make(map[string]int),
		Counts:          make(map[string]int),
	}
	if skeleton, ok := object(root["skeleton"]); ok {
		analysis.SpineVersion, _ = skeleton["spine"].(string)
		analysis.Hash, _ = skeleton["hash"].(string)
		analysis.Images, _ = skeleton["images"].(string)
		analysis.Audio, _ = skeleton["audio"].(string)
	}
	analysis.Bones = namedArray(root["bones"])
	analysis.Slots = namedArray(root["slots"])
	analysis.IKConstraints = namedArray(root["ik"])
	analysis.Transform = namedArray(root["transform"])
	analysis.PathConstraints = namedArray(root["path"])
	analysis.Physics = namedArray(root["physics"])
	analysis.Events = objectKeys(root["events"])
	analysis.Animations = objectKeys(root["animations"])
	analysis.Skins = analyzeSkins(root["skins"], analysis.AttachmentTypes)
	analyzeTimelines(root["animations"], analysis.TimelineTypes)
	analysis.Counts["bones"] = len(analysis.Bones)
	analysis.Counts["slots"] = len(analysis.Slots)
	analysis.Counts["skins"] = len(analysis.Skins)
	analysis.Counts["animations"] = len(analysis.Animations)
	analysis.Counts["events"] = len(analysis.Events)
	analysis.Counts["ikConstraints"] = len(analysis.IKConstraints)
	analysis.Counts["transformConstraints"] = len(analysis.Transform)
	analysis.Counts["pathConstraints"] = len(analysis.PathConstraints)
	analysis.Counts["physicsConstraints"] = len(analysis.Physics)
	for _, count := range analysis.AttachmentTypes {
		analysis.Counts["attachments"] += count
	}
	for _, count := range analysis.TimelineTypes {
		analysis.Counts["timelines"] += count
	}
	return analysis, nil
}

// ValidateSemanticJSON validates bone, slot, constraint, and animation references.
func ValidateSemanticJSON(source []byte) (*ValidationReport, error) {
	root, err := decodeSemantic(source)
	if err != nil {
		return nil, err
	}
	report := &ValidationReport{Valid: true}
	bones := make(map[string]struct{})
	if values, ok := array(root["bones"]); ok {
		for index, raw := range values {
			bone, ok := object(raw)
			if !ok {
				report.add(fmt.Sprintf("/bones/%d", index), "bone must be an object")
				continue
			}
			name, _ := bone["name"].(string)
			if name == "" {
				report.add(fmt.Sprintf("/bones/%d/name", index), "bone name is required")
				continue
			}
			if _, exists := bones[name]; exists {
				report.add(fmt.Sprintf("/bones/%d/name", index), "duplicate bone name")
			}
			bones[name] = struct{}{}
		}
		for index, raw := range values {
			bone, _ := object(raw)
			parent, _ := bone["parent"].(string)
			if parent != "" {
				if _, exists := bones[parent]; !exists {
					report.add(fmt.Sprintf("/bones/%d/parent", index), "parent bone does not exist: "+parent)
				}
			}
		}
	}
	slots := make(map[string]struct{})
	if values, ok := array(root["slots"]); ok {
		for index, raw := range values {
			slot, ok := object(raw)
			if !ok {
				report.add(fmt.Sprintf("/slots/%d", index), "slot must be an object")
				continue
			}
			name, _ := slot["name"].(string)
			if name == "" {
				report.add(fmt.Sprintf("/slots/%d/name", index), "slot name is required")
			} else if _, exists := slots[name]; exists {
				report.add(fmt.Sprintf("/slots/%d/name", index), "duplicate slot name")
			}
			slots[name] = struct{}{}
			bone, _ := slot["bone"].(string)
			if _, exists := bones[bone]; !exists {
				report.add(fmt.Sprintf("/slots/%d/bone", index), "slot bone does not exist: "+bone)
			}
		}
	}
	validateConstraintReferences(root["ik"], "ik", bones, bones, report)
	validateConstraintReferences(root["transform"], "transform", bones, bones, report)
	validateConstraintReferences(root["path"], "path", bones, slots, report)
	validateConstraintReferences(root["physics"], "physics", bones, bones, report)
	if animations, ok := object(root["animations"]); ok {
		for animationName, rawAnimation := range animations {
			animation, ok := object(rawAnimation)
			if !ok {
				report.add("/animations/"+escapePointer(animationName), "animation must be an object")
				continue
			}
			if boneTimelines, ok := object(animation["bones"]); ok {
				for bone := range boneTimelines {
					if _, exists := bones[bone]; !exists {
						report.add("/animations/"+escapePointer(animationName)+"/bones/"+escapePointer(bone), "timeline bone does not exist")
					}
				}
			}
		}
	}
	report.Valid = len(report.Issues) == 0
	return report, nil
}

// CloneAnimation clones, retimes, and replaces selected bone timelines.
func CloneAnimation(source []byte, options CloneAnimationOptions) ([]byte, *CloneAnimationResult, error) {
	if strings.TrimSpace(options.Target) == "" {
		return nil, nil, errors.New("target animation is required")
	}
	root, err := decodeSemantic(source)
	if err != nil {
		return nil, nil, err
	}
	animations, ok := object(root["animations"])
	if !ok {
		animations = make(map[string]any)
		root["animations"] = animations
	}
	if _, exists := animations[options.Target]; exists && !options.ReplaceExisting {
		return nil, nil, fmt.Errorf("target animation already exists: %s", options.Target)
	}
	var animation map[string]any
	if options.Source == "" {
		animation = make(map[string]any)
	} else {
		raw, exists := animations[options.Source]
		if !exists {
			return nil, nil, fmt.Errorf("source animation not found: %s", options.Source)
		}
		cloned, err := cloneSemantic(raw)
		if err != nil {
			return nil, nil, err
		}
		animation, ok = object(cloned)
		if !ok {
			return nil, nil, fmt.Errorf("source animation is not an object: %s", options.Source)
		}
	}
	timeScale := options.TimeScale
	if timeScale == 0 {
		timeScale = 1
	}
	if timeScale <= 0 {
		return nil, nil, errors.New("timeScale must be positive")
	}
	if timeScale != 1 {
		scaleTimelineTimes(animation, timeScale)
	}
	boneNames := make(map[string]struct{})
	for _, name := range namedArray(root["bones"]) {
		boneNames[name] = struct{}{}
	}
	boneTimelines, ok := object(animation["bones"])
	if !ok {
		boneTimelines = make(map[string]any)
		animation["bones"] = boneTimelines
	}
	changed := make([]string, 0, len(options.BoneMotions))
	for _, motion := range options.BoneMotions {
		if _, exists := boneNames[motion.Bone]; !exists {
			return nil, nil, fmt.Errorf("motion bone not found: %s", motion.Bone)
		}
		timelines, ok := object(boneTimelines[motion.Bone])
		if !ok {
			timelines = make(map[string]any)
			boneTimelines[motion.Bone] = timelines
		}
		if motion.Rotate != nil {
			timelines["rotate"] = motion.Rotate
		}
		if motion.Translate != nil {
			timelines["translate"] = motion.Translate
		}
		if motion.Scale != nil {
			timelines["scale"] = motion.Scale
		}
		if motion.Shear != nil {
			timelines["shear"] = motion.Shear
		}
		changed = append(changed, motion.Bone)
	}
	if options.MarkerEvent != "" {
		events, ok := object(root["events"])
		if !ok {
			events = make(map[string]any)
			root["events"] = events
		}
		if _, exists := events[options.MarkerEvent]; !exists {
			events[options.MarkerEvent] = map[string]any{"string": "generated-by-spine233-agent-cli"}
		}
		eventKeys, _ := array(animation["events"])
		eventKeys = append(eventKeys, map[string]any{"name": options.MarkerEvent, "time": 0})
		animation["events"] = eventKeys
	}
	animations[options.Target] = animation
	report := &CloneAnimationResult{
		Source:       options.Source,
		Target:       options.Target,
		Duration:     maxTimelineTime(animation),
		TimeScale:    timeScale,
		ChangedBones: changed,
		MarkerEvent:  options.MarkerEvent,
	}
	indent := options.Indent
	if indent == "" {
		indent = "  "
	}
	encoded, err := json.MarshalIndent(root, "", indent)
	if err != nil {
		return nil, nil, err
	}
	encoded = append(encoded, '\n')
	validation, err := ValidateSemanticJSON(encoded)
	if err != nil {
		return nil, nil, err
	}
	if !validation.Valid {
		return nil, nil, fmt.Errorf("generated animation failed semantic validation: %s", validation.Issues[0].Message)
	}
	return encoded, report, nil
}

func (r *ValidationReport) add(path, message string) {
	r.Issues = append(r.Issues, ValidationIssue{Path: path, Message: message})
}

func validateConstraintReferences(
	raw any,
	group string,
	bones map[string]struct{},
	targets map[string]struct{},
	report *ValidationReport,
) {
	values, ok := array(raw)
	if !ok {
		return
	}
	for index, item := range values {
		constraint, ok := object(item)
		if !ok {
			report.add(fmt.Sprintf("/%s/%d", group, index), "constraint must be an object")
			continue
		}
		if target, ok := constraint["target"].(string); ok && target != "" {
			if _, exists := targets[target]; !exists {
				report.add(fmt.Sprintf("/%s/%d/target", group, index), "constraint target does not exist: "+target)
			}
		}
		if constrained, ok := array(constraint["bones"]); ok {
			for boneIndex, rawBone := range constrained {
				name, _ := rawBone.(string)
				if _, exists := bones[name]; !exists {
					report.add(fmt.Sprintf("/%s/%d/bones/%d", group, index, boneIndex), "constrained bone does not exist: "+name)
				}
			}
		}
	}
}

func analyzeSkins(raw any, attachmentTypes map[string]int) []string {
	names := make([]string, 0)
	if skins, ok := array(raw); ok {
		for _, item := range skins {
			skin, ok := object(item)
			if !ok {
				continue
			}
			name, _ := skin["name"].(string)
			if name != "" {
				names = append(names, name)
			}
			walkAttachments(skin["attachments"], attachmentTypes)
		}
	} else if skins, ok := object(raw); ok {
		for name, attachments := range skins {
			names = append(names, name)
			walkAttachments(attachments, attachmentTypes)
		}
	}
	sort.Strings(names)
	return names
}

func walkAttachments(raw any, types map[string]int) {
	slots, ok := object(raw)
	if !ok {
		return
	}
	for _, rawSlot := range slots {
		attachments, ok := object(rawSlot)
		if !ok {
			continue
		}
		for _, rawAttachment := range attachments {
			attachment, ok := object(rawAttachment)
			if !ok {
				continue
			}
			kind, _ := attachment["type"].(string)
			if kind == "" {
				kind = "region"
			}
			types[kind]++
		}
	}
}

func analyzeTimelines(raw any, timelineTypes map[string]int) {
	animations, ok := object(raw)
	if !ok {
		return
	}
	for _, rawAnimation := range animations {
		animation, ok := object(rawAnimation)
		if !ok {
			continue
		}
		for category, rawCategory := range animation {
			switch category {
			case "drawOrder", "draworder", "events":
				if values, ok := array(rawCategory); ok {
					timelineTypes[category] += len(values)
				}
			default:
				walkTimelineCategory(category, rawCategory, timelineTypes)
			}
		}
	}
}

func walkTimelineCategory(category string, raw any, timelineTypes map[string]int) {
	group, ok := object(raw)
	if !ok {
		return
	}
	for _, rawTarget := range group {
		target, ok := object(rawTarget)
		if !ok {
			continue
		}
		for timeline, rawKeys := range target {
			if values, ok := array(rawKeys); ok {
				timelineTypes[category+"."+timeline] += len(values)
				continue
			}
			if nested, ok := object(rawKeys); ok {
				for nestedTimeline, nestedKeys := range nested {
					if values, ok := array(nestedKeys); ok {
						timelineTypes[category+"."+timeline+"."+nestedTimeline] += len(values)
					}
				}
			}
		}
	}
}

func namedArray(raw any) []string {
	values, ok := array(raw)
	if !ok {
		return nil
	}
	names := make([]string, 0, len(values))
	for _, item := range values {
		value, ok := object(item)
		if !ok {
			continue
		}
		if name, ok := value["name"].(string); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}

func objectKeys(raw any) []string {
	values, ok := object(raw)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func scaleTimelineTimes(value any, scale float64) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if key == "time" {
				if number, ok := numeric(child); ok {
					current[key] = number * scale
				}
				continue
			}
			scaleTimelineTimes(child, scale)
		}
	case []any:
		for _, child := range current {
			scaleTimelineTimes(child, scale)
		}
	}
}

func maxTimelineTime(value any) float64 {
	maximum := 0.0
	var walk func(any)
	walk = func(raw any) {
		switch current := raw.(type) {
		case map[string]any:
			for key, child := range current {
				if key == "time" {
					if number, ok := numeric(child); ok && number > maximum {
						maximum = number
					}
				} else {
					walk(child)
				}
			}
		case []any:
			for _, child := range current {
				walk(child)
			}
		}
	}
	walk(value)
	return maximum
}

func numeric(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		result, err := number.Float64()
		return result, err == nil
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func decodeSemantic(source []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, &ParseError{Code: ErrInvalidJSON, Msg: "invalid Spine JSON", Cause: err}
	}
	if root == nil {
		return nil, &ParseError{Code: ErrInvalidJSON, Msg: "Spine JSON root must be an object"}
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, &ParseError{Code: ErrInvalidJSON, Msg: "multiple JSON values"}
		}
		return nil, &ParseError{Code: ErrInvalidJSON, Msg: "invalid trailing JSON", Cause: err}
	}
	return root, nil
}

func cloneSemantic(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var cloned any
	if err := decoder.Decode(&cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func object(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func array(value any) ([]any, bool) {
	result, ok := value.([]any)
	return result, ok
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
