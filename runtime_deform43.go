package spineparser

import (
	"fmt"
	"math"
	"sort"
)

// Animation43DeformBinding describes deform offsets without changing the input.
// Unweighted attachments have a Count and nil OffsetOrder/Influences. For a
// weighted attachment, each Influence array is one original vertex ordered by
// named bone; OffsetOrder maps canonical XY offsets to their source positions.
// Influences retain the original numeric values for x, y and weight.
type Animation43DeformBinding struct {
	Count       int
	OffsetOrder []int
	Influences  []any
}

// ResolveAnimation43DeformBinding resolves linked meshes and validates weighted
// vertex/influence counts and bone references. Duplicate influences for the
// same bone within one vertex are rejected because their identity is ambiguous.
// A caller must still compare every deform key, including time and curve.
func ResolveAnimation43DeformBinding(document map[string]any, skin, slot, attachment string) (*Animation43DeformBinding, error) {
	count, order, influences, err := attachmentDeformLayout43(document, skin, slot, attachment, map[string]bool{})
	if err != nil {
		return nil, err
	}
	return &Animation43DeformBinding{Count: count, OffsetOrder: order, Influences: influences}, nil
}

// Spine may renumber bones and sort each weighted vertex's influences during
// JSON import. Its deform offsets follow that influence order. Canonicalize
// both by bone name within each original vertex, never across vertices.
func attachmentDeformLayout43(document map[string]any, skinName, slotName, attachmentName string, seen map[string]bool) (int, []int, []any, error) {
	identity := skinName + "/" + slotName + "/" + attachmentName
	if seen[identity] {
		return 0, nil, nil, fmt.Errorf("cyclic linked mesh: %s", identity)
	}
	seen[identity] = true
	skins, _ := document["skins"].([]any)
	for _, raw := range skins {
		skin, _ := raw.(map[string]any)
		if skin["name"] != skinName {
			continue
		}
		attachments, _ := skin["attachments"].(map[string]any)
		slot, _ := attachments[slotName].(map[string]any)
		attachment, _ := slot[attachmentName].(map[string]any)
		if source, ok := attachment["source"].(string); ok && source != "" {
			sourceSkin, _ := attachment["skin"].(string)
			if sourceSkin == "" {
				sourceSkin = "default"
			}
			sourceSlot, _ := attachment["slot"].(string)
			if sourceSlot == "" {
				sourceSlot = slotName
			}
			return attachmentDeformLayout43(document, sourceSkin, sourceSlot, source, seen)
		}
		uvs, _ := attachment["uvs"].([]any)
		length := len(uvs)
		if length == 0 {
			vertices, ok := numeric(attachment["vertexCount"])
			if !ok || !finite43(vertices) || vertices < 1 || math.Trunc(vertices) != vertices || vertices > 1e7 {
				return 0, nil, nil, fmt.Errorf("%s: deform requires a vertex attachment", identity)
			}
			length = int(vertices) * 2
		}
		vertices, _ := attachment["vertices"].([]any)
		if len(vertices) == length {
			return length, nil, nil, nil
		}
		bones, _ := document["bones"].([]any)
		permutation := []int{}
		binding := []any{}
		influenceCount := 0
		for cursor := 0; cursor < len(vertices); {
			count, ok := numeric(vertices[cursor])
			cursor++
			if !ok || !finite43(count) || count < 1 || math.Trunc(count) != count || count > float64((len(vertices)-cursor)/4) {
				return 0, nil, nil, fmt.Errorf("%s: malformed weighted vertices", identity)
			}
			type influence struct {
				bone     string
				original int
				value    map[string]any
			}
			point := make([]influence, int(count))
			seenBone := map[string]bool{}
			for j := range point {
				index, ok := numeric(vertices[cursor])
				if !ok || !finite43(index) || index < 0 || math.Trunc(index) != index || index >= float64(len(bones)) {
					return 0, nil, nil, fmt.Errorf("%s: invalid weighted bone index", identity)
				}
				bone, _ := bones[int(index)].(map[string]any)
				name, _ := bone["name"].(string)
				if name == "" || seenBone[name] {
					return 0, nil, nil, fmt.Errorf("%s: ambiguous duplicate weighted bone influence %q", identity, name)
				}
				seenBone[name] = true
				point[j] = influence{bone: name, original: influenceCount * 2, value: map[string]any{"bone": name, "x": vertices[cursor+1], "y": vertices[cursor+2], "weight": vertices[cursor+3]}}
				influenceCount++
				cursor += 4
			}
			sort.Slice(point, func(i, j int) bool { return point[i].bone < point[j].bone })
			ordered := make([]any, len(point))
			for j, influence := range point {
				permutation = append(permutation, influence.original, influence.original+1)
				ordered[j] = influence.value
			}
			binding = append(binding, ordered)
		}
		if influenceCount == 0 || len(binding)*2 != length {
			return 0, nil, nil, fmt.Errorf("%s: weighted vertex count differs from attachment size", identity)
		}
		return influenceCount * 2, permutation, binding, nil
	}
	return 0, nil, nil, fmt.Errorf("deform skin %q does not exist", skinName)
}
