package spineparser

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateSpine43JSON checks the 4.3 version, ordered bone hierarchy, unified
// constraint references, timeline owners, events, key times and Bezier shape.
// Unknown fields are retained by the codec and are not treated as errors.
func ValidateSpine43JSON(source []byte) (*ValidationReport, error) {
	report, err := ValidateSemanticJSON(source)
	if err != nil {
		return nil, err
	}
	root, err := decodeSemantic(source)
	if err != nil {
		return nil, err
	}
	metadata, _ := object(root["skeleton"])
	version, _ := metadata["spine"].(string)
	if version != "4.3" && !strings.HasPrefix(version, "4.3.") {
		report.add("/skeleton/spine", "expected Spine 4.3 or a 4.3 patch version")
	} else if !versionPattern.MatchString(version) {
		report.add("/skeleton/spine", "invalid Spine version")
	}
	bones := map[string]struct{}{}
	boneValues, _ := array(root["bones"])
	if len(boneValues) == 0 {
		report.add("/bones", "at least one bone is required")
	}
	for i, value := range boneValues {
		bone, _ := object(value)
		name, _ := bone["name"].(string)
		parent, _ := bone["parent"].(string)
		if parent != "" {
			if _, exists := bones[parent]; !exists {
				report.add(fmt.Sprintf("/bones/%d/parent", i), "parent must precede its child")
			}
		}
		bones[name] = struct{}{}
	}
	slots := map[string]struct{}{}
	slotValues, _ := array(root["slots"])
	for _, value := range slotValues {
		slot, _ := object(value)
		name, _ := slot["name"].(string)
		slots[name] = struct{}{}
	}
	constraintNames := map[string]struct{}{}
	if raw, exists := root["constraints"]; exists {
		constraints, ok := array(raw)
		if !ok {
			report.add("/constraints", "constraints must be an array")
		}
		for i, value := range constraints {
			constraint, ok := object(value)
			path := fmt.Sprintf("/constraints/%d", i)
			if !ok {
				report.add(path, "constraint must be an object")
				continue
			}
			name, _ := constraint["name"].(string)
			kind, _ := constraint["type"].(string)
			if name == "" {
				report.add(path+"/name", "constraint name is required")
			}
			if _, exists := constraintNames[name]; exists {
				report.add(path+"/name", "duplicate constraint name")
			}
			constraintNames[name] = struct{}{}
			if targets, ok := array(constraint["bones"]); ok {
				for j, target := range targets {
					name, _ := target.(string)
					if _, exists := bones[name]; !exists {
						report.add(fmt.Sprintf("%s/bones/%d", path, j), "constraint bone does not exist")
					}
				}
			}
			for _, field := range []string{"bone", "source", "target", "slot"} {
				if target, ok := constraint[field].(string); ok {
					owners := bones
					if field == "slot" && kind == "path" {
						owners = slots
					}
					if _, exists := owners[target]; !exists {
						report.add(path+"/"+field, "constraint owner does not exist")
					}
				}
			}
		}
	}
	events, _ := object(root["events"])
	animations, ok := object(root["animations"])
	if raw, exists := root["animations"]; exists && !ok && raw != nil {
		report.add("/animations", "animations must be an object")
	}
	for name, value := range animations {
		animation, _ := object(value)
		path := "/animations/" + escapePointer(name)
		for _, category := range []string{"bones", "slots"} {
			if raw, exists := animation[category]; exists {
				owners, ok := object(raw)
				if !ok {
					report.add(path+"/"+category, "timeline owners must be an object")
					continue
				}
				for owner := range owners {
					available := bones
					if category == "slots" {
						available = slots
					}
					if _, exists := available[owner]; !exists {
						report.add(path+"/"+category+"/"+escapePointer(owner), "timeline owner does not exist")
					}
				}
			}
		}
		if keys, ok := array(animation["events"]); ok {
			for i, value := range keys {
				key, _ := object(value)
				event, _ := key["name"].(string)
				if _, exists := events[event]; !exists {
					report.add(fmt.Sprintf("%s/events/%d/name", path, i), "event definition does not exist")
				}
			}
		}
	}
	validate43Structure(root, report)
	unique43Issues(report)
	sort.SliceStable(report.Issues, func(i, j int) bool {
		if report.Issues[i].Path == report.Issues[j].Path {
			return report.Issues[i].Message < report.Issues[j].Message
		}
		return report.Issues[i].Path < report.Issues[j].Path
	})
	report.Valid = len(report.Issues) == 0
	return report, nil
}
