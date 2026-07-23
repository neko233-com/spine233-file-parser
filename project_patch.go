package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

// ProjectFloat32Edit replaces every exact big-endian float32 match inside an
// explicitly bounded animation record. ExpectedMatches makes the operation
// fail closed when the private project layout differs from the caller's probe.
type ProjectFloat32Edit struct {
	From            float32 `json:"from"`
	To              float32 `json:"to"`
	ExpectedMatches int     `json:"expectedMatches"`
}

// ProjectAnimationFloatPatch bounds low-level edits between two Kryo
// ASCII-optimized animation names. EndBefore may be empty for the final record.
type ProjectAnimationFloatPatch struct {
	Animation       string               `json:"animation"`
	TargetAnimation string               `json:"targetAnimation,omitempty"`
	EndBefore       string               `json:"endBefore,omitempty"`
	Edits           []ProjectFloat32Edit `json:"edits"`
}

// ProjectFloat32Change reports one exact payload edit.
type ProjectFloat32Change struct {
	From    float32 `json:"from"`
	To      float32 `json:"to"`
	Offsets []int   `json:"offsets"`
}

// ProjectAnimationFloatPatchReport is safe to inspect before serialization.
type ProjectAnimationFloatPatchReport struct {
	Animation       string                 `json:"animation"`
	TargetAnimation string                 `json:"targetAnimation,omitempty"`
	EndBefore       string                 `json:"endBefore,omitempty"`
	RegionStart     int                    `json:"regionStart"`
	RegionEnd       int                    `json:"regionEnd"`
	Changes         []ProjectFloat32Change `json:"changes"`
}

// PatchProjectAnimationFloat32 clones a project document, applies exact
// float32 edits, and optionally renames the animation. It never mutates document.
func PatchProjectAnimationFloat32(
	document *ProjectDocument,
	patch ProjectAnimationFloatPatch,
) (*ProjectDocument, ProjectAnimationFloatPatchReport, error) {
	if document == nil || len(document.Payload) == 0 {
		return nil, ProjectAnimationFloatPatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	if len(patch.Edits) == 0 {
		return nil, ProjectAnimationFloatPatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "at least one float32 edit is required"}
	}

	start, end, err := projectAnimationRegion(document.Payload, patch.Animation, patch.EndBefore)
	if err != nil {
		return nil, ProjectAnimationFloatPatchReport{}, err
	}
	if end <= start {
		return nil, ProjectAnimationFloatPatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "invalid animation region"}
	}

	payload := append([]byte(nil), document.Payload...)
	report := ProjectAnimationFloatPatchReport{
		Animation: patch.Animation, TargetAnimation: patch.TargetAnimation,
		EndBefore:   patch.EndBefore,
		RegionStart: start, RegionEnd: end,
		Changes: make([]ProjectFloat32Change, 0, len(patch.Edits)),
	}
	seen := make(map[uint32]struct{}, len(patch.Edits))
	for index, edit := range patch.Edits {
		fromBits := math.Float32bits(edit.From)
		toBits := math.Float32bits(edit.To)
		if math.IsNaN(float64(edit.From)) || math.IsInf(float64(edit.From), 0) ||
			math.IsNaN(float64(edit.To)) || math.IsInf(float64(edit.To), 0) {
			return nil, ProjectAnimationFloatPatchReport{},
				fmt.Errorf("edit %d: float32 values must be finite", index)
		}
		if fromBits == toBits {
			return nil, ProjectAnimationFloatPatchReport{},
				fmt.Errorf("edit %d: from and to must differ", index)
		}
		if edit.ExpectedMatches < 1 {
			return nil, ProjectAnimationFloatPatchReport{},
				fmt.Errorf("edit %d: expectedMatches must be positive", index)
		}
		if _, exists := seen[fromBits]; exists {
			return nil, ProjectAnimationFloatPatchReport{},
				fmt.Errorf("edit %d: duplicate from value %v", index, edit.From)
		}
		seen[fromBits] = struct{}{}

		var from [4]byte
		var to [4]byte
		binary.BigEndian.PutUint32(from[:], fromBits)
		binary.BigEndian.PutUint32(to[:], toBits)
		offsets := findBytesInRange(payload, from[:], start, end)
		if len(offsets) != edit.ExpectedMatches {
			return nil, ProjectAnimationFloatPatchReport{}, fmt.Errorf(
				"edit %d: value %v matched %d times in animation region, expected %d",
				index, edit.From, len(offsets), edit.ExpectedMatches,
			)
		}
		for _, offset := range offsets {
			copy(payload[offset:offset+4], to[:])
		}
		report.Changes = append(report.Changes, ProjectFloat32Change{
			From: edit.From, To: edit.To, Offsets: offsets,
		})
	}
	if strings.TrimSpace(patch.TargetAnimation) != "" &&
		patch.TargetAnimation != patch.Animation {
		existing, err := projectStringOffsets(payload, patch.TargetAnimation)
		if err != nil {
			return nil, ProjectAnimationFloatPatchReport{}, fmt.Errorf("targetAnimation: %w", err)
		}
		if len(existing) != 0 {
			return nil, ProjectAnimationFloatPatchReport{},
				fmt.Errorf("target animation already exists: %s", patch.TargetAnimation)
		}
		sourceName, err := encodeProjectString(patch.Animation)
		if err != nil {
			return nil, ProjectAnimationFloatPatchReport{}, err
		}
		targetName, err := encodeProjectString(patch.TargetAnimation)
		if err != nil {
			return nil, ProjectAnimationFloatPatchReport{}, err
		}
		renamed := make([]byte, 0, len(payload)+len(targetName)-len(sourceName))
		renamed = append(renamed, payload[:start]...)
		renamed = append(renamed, targetName...)
		renamed = append(renamed, payload[start+len(sourceName):]...)
		delta := len(targetName) - len(sourceName)
		for changeIndex := range report.Changes {
			for offsetIndex := range report.Changes[changeIndex].Offsets {
				if report.Changes[changeIndex].Offsets[offsetIndex] > start {
					report.Changes[changeIndex].Offsets[offsetIndex] += delta
				}
			}
		}
		report.RegionEnd += delta
		payload = renamed
	}

	result := &ProjectDocument{
		Inspection: document.Inspection,
		Payload:    payload,
	}
	return result, report, nil
}

func projectAnimationRegion(payload []byte, animation, endBefore string) (int, int, error) {
	if directory, err := DiscoverProjectAnimations(payload); err == nil {
		var selected *ProjectAnimationRecord
		for index := range directory.Records {
			if directory.Records[index].Name == animation {
				selected = &directory.Records[index]
				break
			}
		}
		if selected == nil {
			return 0, 0, fmt.Errorf("animation not found: %s", animation)
		}
		if strings.TrimSpace(endBefore) == "" {
			return selected.Offset, selected.EndOffset, nil
		}
		for _, record := range directory.Records {
			if record.Name == endBefore && record.Offset > selected.Offset {
				return selected.Offset, record.Offset, nil
			}
		}
		return 0, 0, fmt.Errorf("endBefore animation does not occur after %s: %s", animation, endBefore)
	}

	start, err := uniqueProjectStringOffset(payload, animation)
	if err != nil {
		return 0, 0, fmt.Errorf("animation: %w", err)
	}
	end := len(payload)
	if strings.TrimSpace(endBefore) == "" {
		return start, end, nil
	}
	offsets, err := projectStringOffsets(payload, endBefore)
	if err != nil {
		return 0, 0, fmt.Errorf("endBefore: %w", err)
	}
	for _, offset := range offsets {
		if offset > start {
			return start, offset, nil
		}
	}
	return 0, 0, &ParseError{
		Code: ErrInvalidInput,
		Msg:  "endBefore string does not occur after animation",
	}
}

func uniqueProjectStringOffset(payload []byte, value string) (int, error) {
	offsets, err := projectStringOffsets(payload, value)
	if err != nil {
		return 0, err
	}
	if len(offsets) != 1 {
		return 0, &ParseError{
			Code: ErrInvalidInput,
			Msg:  fmt.Sprintf("project string %q matched %d times, expected 1", value, len(offsets)),
		}
	}
	return offsets[0], nil
}

func projectStringOffsets(payload []byte, value string) ([]int, error) {
	encoded, err := encodeProjectString(value)
	if err != nil {
		return nil, err
	}
	return findBytesInRange(payload, encoded, 0, len(payload)), nil
}

func encodeProjectString(value string) ([]byte, error) {
	if value == "" {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project string is empty"}
	}
	encoded := []byte(value)
	for _, current := range encoded {
		if current < 0x20 || current > 0x7e {
			return nil, &ParseError{Code: ErrInvalidInput, Msg: "project string must be printable ASCII"}
		}
	}
	encoded[len(encoded)-1] |= 0x80
	return encoded, nil
}

func findBytesInRange(payload, pattern []byte, start, end int) []int {
	if len(pattern) == 0 || start < 0 || end > len(payload) || start > end {
		return nil
	}
	offsets := make([]int, 0)
	for offset := start; offset+len(pattern) <= end; {
		index := bytes.Index(payload[offset:end], pattern)
		if index < 0 {
			break
		}
		match := offset + index
		offsets = append(offsets, match)
		offset = match + len(pattern)
	}
	return offsets
}
