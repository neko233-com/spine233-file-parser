package spineparser

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

func validate43Structure(root map[string]any, report *ValidationReport) {
	bones := map[string]bool{}
	slots := map[string]bool{}
	constraints := map[string]string{}
	attachments := map[string]map[string]map[string]map[string]any{}
	for _, field := range []string{"bones", "slots", "skins", "constraints"} {
		if raw, exists := root[field]; exists {
			if _, ok := array(raw); !ok {
				report.add("/"+field, field+" must be an array")
			}
		}
	}
	for _, field := range []string{"skeleton", "events", "animations"} {
		if raw, exists := root[field]; exists {
			if _, ok := object(raw); !ok {
				report.add("/"+field, field+" must be an object")
			}
		}
	}
	boneValues, _ := array(root["bones"])
	for i, raw := range boneValues {
		bone, _ := object(raw)
		name, _ := bone["name"].(string)
		bones[name] = true
		path := fmt.Sprintf("/bones/%d", i)
		validate43Numbers(bone, path, []string{"x", "y", "rotation", "scaleX", "scaleY", "shearX", "shearY", "length"}, report)
		if raw, exists := bone["parent"]; exists {
			if _, ok := raw.(string); !ok {
				report.add(path+"/parent", "parent must be a string")
			}
		}
	}
	slotValues, _ := array(root["slots"])
	for i, raw := range slotValues {
		slot, _ := object(raw)
		name, _ := slot["name"].(string)
		slots[name] = true
		path := fmt.Sprintf("/slots/%d", i)
		validate43Color(slot, "color", 8, path, false, report)
		validate43Color(slot, "dark", 6, path, false, report)
	}
	constraintValues, _ := array(root["constraints"])
	for i, raw := range constraintValues {
		constraint, _ := object(raw)
		name, _ := constraint["name"].(string)
		kind, _ := constraint["type"].(string)
		constraints[name] = kind
		path := fmt.Sprintf("/constraints/%d", i)
		switch kind {
		case "ik", "transform":
			if _, ok := array(constraint["bones"]); !ok {
				report.add(path+"/bones", "constraint bones must be an array")
			}
			field := "target"
			if kind == "transform" {
				field = "source"
			}
			if name, ok := constraint[field].(string); !ok || !bones[name] {
				report.add(path+"/"+field, "constraint bone is required")
			}
		case "path":
			name, ok := constraint["slot"].(string)
			if !ok || !slots[name] {
				report.add(path+"/slot", "path slot is required")
			}
			if _, ok := array(constraint["bones"]); !ok {
				report.add(path+"/bones", "constraint bones must be an array")
			}
		case "physics":
			name, ok := constraint["bone"].(string)
			if !ok || !bones[name] {
				report.add(path+"/bone", "physics bone is required")
			}
		case "slider":
			name, ok := constraint["animation"].(string)
			animations, _ := object(root["animations"])
			if !ok || animations[name] == nil {
				report.add(path+"/animation", "slider animation must exist")
			}
		default:
			report.add(path+"/type", "unsupported Spine 4.3 constraint type")
		}
	}
	skins, _ := array(root["skins"])
	for i, raw := range skins {
		skin, ok := object(raw)
		path := fmt.Sprintf("/skins/%d", i)
		if !ok {
			report.add(path, "skin must be an object")
			continue
		}
		name, ok := skin["name"].(string)
		if !ok || name == "" {
			report.add(path+"/name", "skin name is required")
		}
		if _, exists := attachments[name]; exists {
			report.add(path+"/name", "duplicate skin name")
		}
		attachments[name] = map[string]map[string]map[string]any{}
		if refs, exists := skin["bones"]; exists {
			values, ok := array(refs)
			if !ok {
				report.add(path+"/bones", "skin bones must be an array")
			}
			for j, raw := range values {
				name, _ := raw.(string)
				if !bones[name] {
					report.add(fmt.Sprintf("%s/bones/%d", path, j), "skin bone does not exist")
				}
			}
		}
		for _, kind := range []string{"ik", "transform", "path", "physics", "slider"} {
			if refs, exists := skin[kind]; exists {
				values, ok := array(refs)
				if !ok {
					report.add(path+"/"+kind, "skin constraints must be an array")
				}
				for j, raw := range values {
					name, _ := raw.(string)
					if constraints[name] != kind {
						report.add(fmt.Sprintf("%s/%s/%d", path, kind, j), "skin constraint does not exist or has the wrong type")
					}
				}
			}
		}
		if raw, exists := skin["attachments"]; exists {
			slotMap, ok := object(raw)
			if !ok {
				report.add(path+"/attachments", "skin attachments must be an object")
				continue
			}
			for slot, raw := range slotMap {
				slotPath := path + "/attachments/" + escapePointer(slot)
				if !slots[slot] {
					report.add(slotPath, "attachment slot does not exist")
				}
				entries, ok := object(raw)
				if !ok {
					report.add(slotPath, "slot attachments must be an object")
					continue
				}
				attachments[name][slot] = map[string]map[string]any{}
				for entry, raw := range entries {
					data, ok := object(raw)
					attachmentPath := slotPath + "/" + escapePointer(entry)
					if !ok {
						report.add(attachmentPath, "attachment must be an object")
						continue
					}
					attachments[name][slot][entry] = data
					validate43Numbers(data, attachmentPath, []string{"x", "y", "rotation", "scaleX", "scaleY", "width", "height"}, report)
					kind, _ := data["type"].(string)
					switch kind {
					case "", "region", "mesh", "linkedmesh", "boundingbox", "path", "point", "clipping":
					default:
						report.add(attachmentPath+"/type", "unsupported attachment type")
					}
					if kind == "mesh" && data["source"] == nil {
						validate43Mesh(data, attachmentPath, len(bones), report)
					}
				}
			}
		}
	}
	for i, raw := range slotValues {
		slot, _ := object(raw)
		name, _ := slot["name"].(string)
		if entry, ok := slot["attachment"].(string); ok && entry != "" && !has43Attachment(attachments, name, entry) {
			report.add(fmt.Sprintf("/slots/%d/attachment", i), "setup attachment does not exist in any skin")
		}
	}
	events, _ := object(root["events"])
	for name, raw := range events {
		event, ok := object(raw)
		path := "/events/" + escapePointer(name)
		if !ok {
			report.add(path, "event definition must be an object")
			continue
		}
		validate43Numbers(event, path, []string{"int", "float", "volume", "balance"}, report)
	}
	animations, _ := object(root["animations"])
	for name, raw := range animations {
		animation, ok := object(raw)
		if !ok {
			continue
		}
		path := "/animations/" + escapePointer(name)
		err := VisitAnimation43Timelines(animation, func(t Animation43Timeline) error {
			timelinePath := path + t.Path
			orderSlots := make([]string, 0, len(slotValues))
			for _, raw := range slotValues {
				value, _ := object(raw)
				name, _ := value["name"].(string)
				orderSlots = append(orderSlots, name)
			}
			if t.Category == "drawOrderFolder" {
				orderSlots = t.FolderSlots
				seen := map[string]bool{}
				for _, name := range orderSlots {
					if !slots[name] || seen[name] {
						report.add(timelinePath, "folder requires unique existing slots")
					}
					seen[name] = true
				}
			}
			switch t.Category {
			case "ik", "transform", "path", "physics", "slider":
				if !(t.Category == "physics" && t.Owner == "") && constraints[t.Owner] != t.Category {
					report.add(timelinePath, "timeline constraint does not exist or has the wrong type")
				}
			case "attachments":
				if attachments[t.Skin][t.Slot][t.Attachment] == nil {
					report.add(timelinePath, "timeline attachment does not exist")
				}
			}
			previous := -1.0
			for i, raw := range t.Keys {
				key, _ := object(raw)
				keyPath := fmt.Sprintf("%s/%d", timelinePath, i)
				time := 0.0
				if raw, exists := key["time"]; exists {
					number, ok := numeric(raw)
					if !ok || !finite43(number) || number < 0 {
						report.add(keyPath+"/time", "key time must be finite and non-negative")
					} else {
						time = number
					}
				}
				if time < previous || (time == previous && t.Channels != 0) {
					report.add(keyPath+"/time", "interpolated key times must increase; discrete timelines may share a time")
				}
				previous = time
				validate43Numbers(key, keyPath, []string{"value", "x", "y", "mix", "softness", "mixRotate", "mixX", "mixY", "mixScaleX", "mixScaleY", "mixShearY", "int", "float", "volume", "balance", "delay", "index", "offset"}, report)
				if t.Category == "bones" {
					vector := t.Kind == "translate" || t.Kind == "scale" || t.Kind == "shear"
					for _, field := range []string{"x", "y", "value", "angle", "rotation", "scaleX", "scaleY", "shearX", "shearY"} {
						if _, exists := key[field]; !exists {
							continue
						}
						allowed := vector && (field == "x" || field == "y") || (!vector && t.Kind != "inherit" && field == "value")
						if !allowed {
							report.add(keyPath+"/"+field, "field does not belong to this Spine 4.3 bone timeline")
						}
					}
					if t.Kind == "inherit" {
						if raw, exists := key["inherit"]; exists {
							value, ok := raw.(string)
							valid := map[string]bool{"normal": true, "onlytranslation": true, "norotationorreflection": true, "noscale": true, "noscaleorreflection": true}
							if !ok || !valid[strings.ToLower(value)] {
								report.add(keyPath+"/inherit", "invalid inheritance mode")
							}
						}
					}
				}
				if t.Category == "events" {
					if raw, exists := key["string"]; exists {
						if _, ok := raw.(string); !ok {
							report.add(keyPath+"/string", "event string must be a string")
						}
					}
					if raw, exists := key["int"]; exists {
						number, ok := numeric(raw)
						if !ok || math.Trunc(number) != number {
							report.add(keyPath+"/int", "event int must be an integer")
						}
					}
				}
				if _, exists := key["angle"]; exists && t.Category == "bones" && t.Kind == "rotate" {
					report.add(keyPath+"/angle", "Spine 4.3 rotation uses value")
				}
				if t.Category == "slots" {
					switch t.Kind {
					case "rgba":
						validate43Color(key, "color", 8, keyPath, true, report)
					case "rgb":
						validate43Color(key, "color", 6, keyPath, true, report)
					case "rgba2":
						validate43Color(key, "light", 8, keyPath, true, report)
						validate43Color(key, "dark", 6, keyPath, true, report)
					case "rgb2":
						validate43Color(key, "light", 6, keyPath, true, report)
						validate43Color(key, "dark", 6, keyPath, true, report)
					case "attachment":
						if raw, exists := key["name"]; exists && raw != nil {
							entry, ok := raw.(string)
							if !ok || (entry != "" && !has43Attachment(attachments, t.Owner, entry)) {
								report.add(keyPath+"/name", "attachment name does not exist for slot")
							}
						}
					}
				}
				if raw, exists := key["curve"]; exists {
					if t.Channels == 0 {
						report.add(keyPath+"/curve", "this timeline does not support interpolation curves")
					} else if stepped, ok := raw.(string); !ok || stepped != "stepped" {
						values, ok := array(raw)
						valid := ok && len(values) == 4*t.Channels
						for _, raw := range values {
							value, ok := numeric(raw)
							valid = valid && ok && finite43(value)
						}
						if !valid {
							report.add(keyPath+"/curve", fmt.Sprintf("curve must be stepped or %d finite control values", 4*t.Channels))
						}
					}
				}
				if t.Kind == "drawOrder" {
					offsets, exists := key["offsets"]
					if exists {
						values, ok := array(offsets)
						if !ok {
							report.add(keyPath+"/offsets", "draw order offsets must be an array")
						}
						seen := map[string]bool{}
						positions := map[string]int{}
						for i, slot := range orderSlots {
							positions[slot] = i
						}
						previousPosition := -1
						occupied := map[int]bool{}
						for j, raw := range values {
							offset, _ := object(raw)
							slot, _ := offset["slot"].(string)
							number, ok := numeric(offset["offset"])
							if !slots[slot] || seen[slot] || !ok || math.Trunc(number) != number {
								report.add(fmt.Sprintf("%s/offsets/%d", keyPath, j), "draw order requires unique slots and integer offsets")
							}
							seen[slot] = true
							position, exists := positions[slot]
							if !exists || position <= previousPosition || !ok || !finite43(number) || number < -float64(position) || number >= float64(len(orderSlots)-position) {
								report.add(fmt.Sprintf("%s/offsets/%d", keyPath, j), "draw order offset is outside its slots or not ordered")
							} else {
								destination := position + int(number)
								if occupied[destination] {
									report.add(keyPath+"/offsets", "draw order destinations must be unique")
								}
								occupied[destination] = true
							}
							previousPosition = position
						}
					}
				}
				if t.Kind == "deform" {
					if raw, exists := key["offset"]; exists {
						number, ok := numeric(raw)
						if !ok || number < 0 || math.Trunc(number) != number {
							report.add(keyPath+"/offset", "deform offset must be a non-negative integer")
						}
					}
					if vertices, exists := key["vertices"]; exists {
						validate43NumberArray(vertices, keyPath+"/vertices", report)
					}
				}
			}
			return nil
		})
		if err != nil {
			report.add(path, err.Error())
		}
	}
}

func finite43(number float64) bool { return !math.IsNaN(number) && !math.IsInf(number, 0) }
func validate43Numbers(data map[string]any, path string, fields []string, report *ValidationReport) {
	for _, field := range fields {
		if raw, exists := data[field]; exists {
			number, ok := numeric(raw)
			if !ok || !finite43(number) {
				report.add(path+"/"+field, "value must be a finite number")
			}
		}
	}
}
func validate43NumberArray(raw any, path string, report *ValidationReport) {
	values, ok := array(raw)
	if !ok {
		report.add(path, "expected a numeric array")
		return
	}
	for i, raw := range values {
		number, ok := numeric(raw)
		if !ok || !finite43(number) {
			report.add(fmt.Sprintf("%s/%d", path, i), "value must be a finite number")
		}
	}
}
func validate43Color(data map[string]any, field string, digits int, path string, required bool, report *ValidationReport) {
	raw, exists := data[field]
	if !exists && !required {
		return
	}
	value, ok := raw.(string)
	_, err := hex.DecodeString(value)
	if !ok || len(value) != digits || err != nil {
		report.add(path+"/"+field, fmt.Sprintf("color must contain %d hexadecimal digits", digits))
	}
}
func has43Attachment(skins map[string]map[string]map[string]map[string]any, slot, entry string) bool {
	for _, slots := range skins {
		if slots[slot][entry] != nil {
			return true
		}
	}
	return false
}
func validate43Mesh(data map[string]any, path string, boneCount int, report *ValidationReport) {
	uvs, ok := array(data["uvs"])
	if !ok || len(uvs) == 0 || len(uvs)%2 != 0 {
		report.add(path+"/uvs", "mesh UVs require coordinate pairs")
	}
	validate43NumberArray(data["uvs"], path+"/uvs", report)
	triangles, ok := array(data["triangles"])
	if !ok || len(triangles)%3 != 0 {
		report.add(path+"/triangles", "mesh triangles require index triples")
	}
	for i, raw := range triangles {
		number, ok := numeric(raw)
		if !ok || math.Trunc(number) != number || number < 0 || number >= float64(len(uvs)/2) {
			report.add(fmt.Sprintf("%s/triangles/%d", path, i), "mesh triangle vertex index is invalid")
		}
	}
	vertices, ok := array(data["vertices"])
	if !ok {
		report.add(path+"/vertices", "mesh vertices must be an array")
		return
	}
	validate43NumberArray(vertices, path+"/vertices", report)
	if len(vertices) == len(uvs) {
		return
	}
	cursor := 0
	count := 0
	for cursor < len(vertices) {
		influences, ok := numeric(vertices[cursor])
		cursor++
		if !ok || influences < 1 || math.Trunc(influences) != influences || influences > float64(boneCount) || influences > float64((len(vertices)-cursor)/4) {
			report.add(path+"/vertices", "invalid weighted mesh influence list")
			return
		}
		for j := 0; j < int(influences); j++ {
			bone, ok := numeric(vertices[cursor])
			weight, weightOK := numeric(vertices[cursor+3])
			if !ok || bone < 0 || bone >= float64(boneCount) || math.Trunc(bone) != bone || !weightOK || weight < 0 {
				report.add(path+"/vertices", "invalid weighted mesh bone index or weight")
			}
			cursor += 4
		}
		count++
	}
	if count != len(uvs)/2 {
		report.add(path+"/vertices", "weighted mesh vertex count differs from UV count")
	}
}

// Error paths are stable JSON pointers and duplicate diagnostics are removed.
func unique43Issues(report *ValidationReport) {
	seen := map[string]bool{}
	issues := report.Issues[:0]
	for _, issue := range report.Issues {
		key := issue.Path + "\x00" + issue.Message
		if !seen[key] {
			issues = append(issues, issue)
			seen[key] = true
		}
	}
	report.Issues = issues
}
