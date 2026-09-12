package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

var projectSkeletonBoundsFieldV2 = []byte{
	0x16, 0x0f, 0x01, 0x00, 0x18, 0x01, 0x05,
}

// ProjectSkeletonBounds contains the setup-pose export bounds persisted by
// Spine 4.3 in the modern skeleton object.
type ProjectSkeletonBounds struct {
	Offset int     `json:"offset"`
	X      float32 `json:"x"`
	Y      float32 `json:"y"`
	Width  float32 `json:"width"`
	Height float32 `json:"height"`
}

type projectSkeletonBoundsCandidate struct {
	bounds        ProjectSkeletonBounds
	exportEnabled bool
	hasExportFlag bool
	end           int
}

// DiscoverProjectSkeletonBounds decodes Spine's persisted export bounds.
func DiscoverProjectSkeletonBounds(
	payload []byte,
) (*ProjectSkeletonBounds, error) {
	candidates := discoverProjectSkeletonBoundsCandidates(payload)
	if len(candidates) == 1 {
		return &candidates[0].bounds, nil
	}
	active := activeProjectSkeletonBoundsCandidate(candidates)
	if active < 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg: fmt.Sprintf(
				"project contains %d modern skeleton bounds candidates",
				len(candidates),
			),
		}
	}
	return &candidates[active].bounds, nil
}

func discoverProjectSkeletonBoundsCandidates(
	payload []byte,
) []projectSkeletonBoundsCandidate {
	candidates := make([]projectSkeletonBoundsCandidate, 0, 1)
	for fieldOffset := 0; ; {
		relative := bytes.Index(
			payload[fieldOffset:],
			projectSkeletonBoundsFieldV2,
		)
		if relative < 0 {
			break
		}
		fieldOffset += relative
		valueOffset := fieldOffset + len(projectSkeletonBoundsFieldV2)
		if valueOffset+16 <= len(payload) {
			values := [4]float32{}
			valid := true
			for index := range values {
				offset := valueOffset + index*4
				values[index] = math.Float32frombits(
					binary.BigEndian.Uint32(payload[offset : offset+4]),
				)
				if math.IsNaN(float64(values[index])) ||
					math.IsInf(float64(values[index]), 0) {
					valid = false
					break
				}
			}
			if valid && values[2] >= 0 && values[3] >= 0 {
				exportEnabled, hasExportFlag, candidateEnd :=
					readProjectSkeletonBoundsExportFlag(payload, valueOffset+16)
				if !hasExportFlag {
					candidateEnd = valueOffset + 16
				}
				candidates = append(candidates, projectSkeletonBoundsCandidate{
					bounds: ProjectSkeletonBounds{
						Offset: valueOffset,
						X:      values[0],
						Y:      values[1],
						Width:  values[2],
						Height: values[3],
					},
					exportEnabled: exportEnabled,
					hasExportFlag: hasExportFlag,
					end:           candidateEnd,
				})
			}
		}
		fieldOffset++
	}
	return candidates
}

func activeProjectSkeletonBoundsCandidate(
	candidates []projectSkeletonBoundsCandidate,
) int {
	active := -1
	for index, candidate := range candidates {
		if !candidate.hasExportFlag || !candidate.exportEnabled {
			continue
		}
		if active >= 0 {
			active = -1
			break
		}
		active = index
	}
	return active
}

func discoverProjectRuntimePayload(payload []byte) ([]byte, error) {
	candidates := discoverProjectSkeletonBoundsCandidates(payload)
	if len(candidates) <= 1 {
		return payload, nil
	}
	active := activeProjectSkeletonBoundsCandidate(candidates)
	if active < 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg: fmt.Sprintf(
				"project contains %d modern skeleton bounds candidates",
				len(candidates),
			),
		}
	}
	start := 0
	if active > 0 {
		start = candidates[active-1].bounds.Offset + 16
	}
	end := candidates[active].end
	if start < 0 || start >= end || end > len(payload) {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "active modern skeleton payload range is invalid",
		}
	}
	return payload[start:end], nil
}

func readProjectSkeletonBoundsExportFlag(
	payload []byte,
	offset int,
) (bool, bool, int) {
	prefix := []byte{0x34, 0x01, 0x2f, 0x01, 0x2d, 0x6c}
	if offset < 0 || offset+len(prefix) >= len(payload) ||
		!bytes.HasPrefix(payload[offset:], prefix) {
		return false, false, offset
	}
	_, cursor, ok := readPositiveVarint(payload, offset+len(prefix))
	if !ok || cursor+4 > len(payload) ||
		!bytes.Equal(payload[cursor:cursor+3], []byte{0x23, 0x01, 0x7e}) ||
		(payload[cursor+3] != 0 && payload[cursor+3] != 1) {
		return false, false, offset
	}
	return payload[cursor+3] == 1, true, cursor + 4
}
