package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

const (
	ProjectTimelineRotate    = "rotate"
	ProjectTimelineTranslate = "translate"
	ProjectTimelineScale     = "scale"
	ProjectTimelineShear     = "shear"
)

// ProjectTransformKey is one directly decoded bone transform key.
type ProjectTransformKey struct {
	Index        int          `json:"index"`
	Frame        float32      `json:"frame"`
	Time         float32      `json:"time"`
	Values       []float32    `json:"values"`
	Offset       int          `json:"offset"`
	FrameOffset  int          `json:"frameOffset"`
	ValueOffsets []int        `json:"valueOffsets"`
	Curves       [][4]float32 `json:"curves"`
	CurveFlags   []byte       `json:"curveFlags"`
}

// ProjectTransformTimeline identifies a rotate, translate, scale, or shear
// timeline by the project's stable Kryo bone reference.
type ProjectTransformTimeline struct {
	Type              string                `json:"type"`
	Channels          []string              `json:"channels"`
	BoneReference     int                   `json:"boneReference"`
	TimelineReference int                   `json:"timelineReference"`
	KeyReference      int                   `json:"keyReference"`
	Offset            int                   `json:"offset"`
	Keys              []ProjectTransformKey `json:"keys"`
}

// ProjectTransformTimelineDirectory contains supported bone transform
// timelines in one top-level animation record.
type ProjectTransformTimelineDirectory struct {
	Animation   string                     `json:"animation"`
	RegionStart int                        `json:"regionStart"`
	RegionEnd   int                        `json:"regionEnd"`
	FrameRate   int                        `json:"frameRate"`
	Timelines   []ProjectTransformTimeline `json:"timelines"`
}

// DiscoverProjectTransformTimelines decodes rotate, translate, scale, and
// shear timelines from a modern Spine Pro project.
func DiscoverProjectTransformTimelines(
	payload []byte,
	animation string,
) (*ProjectTransformTimelineDirectory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	groups := discoverProjectBoneTimelineGroups(
		payload,
		record.Offset,
		record.EndOffset,
	)
	timelines := make([]ProjectTransformTimeline, 0)
	for index, group := range groups {
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1].Offset
		}
		timelines = append(
			timelines,
			discoverProjectTransformTimelinesInGroup(
				payload,
				group.Offset,
				groupEnd,
				group.BoneReference,
			)...,
		)
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no supported transform timelines", animation),
		}
	}
	return &ProjectTransformTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

// ProjectTransformValueEdit changes one key channel. Channel is frame, value
// for rotate, or x/y for translate, scale, and shear.
type ProjectTransformValueEdit struct {
	BoneReference int     `json:"boneReference"`
	Timeline      string  `json:"timeline"`
	KeyIndex      int     `json:"keyIndex"`
	Channel       string  `json:"channel"`
	From          float32 `json:"from"`
	To            float32 `json:"to"`
}

// ProjectTransformPatch controls semantic bone transform edits and optional
// animation renaming.
type ProjectTransformPatch struct {
	Animation       string                      `json:"animation"`
	TargetAnimation string                      `json:"targetAnimation,omitempty"`
	Edits           []ProjectTransformValueEdit `json:"edits"`
}

// ProjectTransformValueChange reports one semantic channel edit.
type ProjectTransformValueChange struct {
	BoneReference int     `json:"boneReference"`
	Timeline      string  `json:"timeline"`
	KeyIndex      int     `json:"keyIndex"`
	Channel       string  `json:"channel"`
	Frame         float32 `json:"frame"`
	From          float32 `json:"from"`
	To            float32 `json:"to"`
	Offset        int     `json:"offset"`
}

// ProjectTransformPatchReport is safe to inspect before serialization.
type ProjectTransformPatchReport struct {
	Animation       string                        `json:"animation"`
	TargetAnimation string                        `json:"targetAnimation,omitempty"`
	RegionStart     int                           `json:"regionStart"`
	RegionEnd       int                           `json:"regionEnd"`
	Changes         []ProjectTransformValueChange `json:"changes"`
}

// PatchProjectTransformValues clones a project and modifies explicitly
// selected transform channels. It never mutates document.
func PatchProjectTransformValues(
	document *ProjectDocument,
	patch ProjectTransformPatch,
) (*ProjectDocument, ProjectTransformPatchReport, error) {
	if document == nil || len(document.Payload) == 0 {
		return nil, ProjectTransformPatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	if len(patch.Edits) == 0 {
		return nil, ProjectTransformPatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "at least one transform edit is required"}
	}
	directory, err := DiscoverProjectTransformTimelines(
		document.Payload,
		patch.Animation,
	)
	if err != nil {
		return nil, ProjectTransformPatchReport{}, err
	}
	type timelineKey struct {
		BoneReference int
		Type          string
	}
	timelineByKey := make(map[timelineKey][]ProjectTransformTimeline)
	for _, timeline := range directory.Timelines {
		key := timelineKey{
			BoneReference: timeline.BoneReference,
			Type:          timeline.Type,
		}
		timelineByKey[key] = append(timelineByKey[key], timeline)
	}

	payload := append([]byte(nil), document.Payload...)
	report := ProjectTransformPatchReport{
		Animation:       patch.Animation,
		TargetAnimation: patch.TargetAnimation,
		RegionStart:     directory.RegionStart,
		RegionEnd:       directory.RegionEnd,
		Changes:         make([]ProjectTransformValueChange, 0, len(patch.Edits)),
	}
	seen := make(map[string]struct{}, len(patch.Edits))
	for editIndex, edit := range patch.Edits {
		if math.IsNaN(float64(edit.From)) || math.IsInf(float64(edit.From), 0) ||
			math.IsNaN(float64(edit.To)) || math.IsInf(float64(edit.To), 0) {
			return nil, ProjectTransformPatchReport{},
				fmt.Errorf("edit %d: transform values must be finite", editIndex)
		}
		if math.Float32bits(edit.From) == math.Float32bits(edit.To) {
			return nil, ProjectTransformPatchReport{},
				fmt.Errorf("edit %d: from and to must differ", editIndex)
		}
		timelineType := strings.ToLower(strings.TrimSpace(edit.Timeline))
		channel := strings.ToLower(strings.TrimSpace(edit.Channel))
		selection := fmt.Sprintf(
			"%d/%s/%d/%s",
			edit.BoneReference,
			timelineType,
			edit.KeyIndex,
			channel,
		)
		if _, exists := seen[selection]; exists {
			return nil, ProjectTransformPatchReport{},
				fmt.Errorf("edit %d: duplicate transform key channel", editIndex)
		}
		seen[selection] = struct{}{}
		matches := timelineByKey[timelineKey{
			BoneReference: edit.BoneReference,
			Type:          timelineType,
		}]
		if len(matches) == 0 {
			return nil, ProjectTransformPatchReport{}, fmt.Errorf(
				"edit %d: %s timeline not found for boneReference %d",
				editIndex,
				timelineType,
				edit.BoneReference,
			)
		}
		if len(matches) != 1 {
			return nil, ProjectTransformPatchReport{}, fmt.Errorf(
				"edit %d: boneReference %d matched %d %s timelines",
				editIndex,
				edit.BoneReference,
				len(matches),
				timelineType,
			)
		}
		timeline := matches[0]
		if edit.KeyIndex < 0 || edit.KeyIndex >= len(timeline.Keys) {
			return nil, ProjectTransformPatchReport{}, fmt.Errorf(
				"edit %d: keyIndex %d is outside [0,%d)",
				editIndex,
				edit.KeyIndex,
				len(timeline.Keys),
			)
		}
		selected := timeline.Keys[edit.KeyIndex]
		currentValue := selected.Frame
		valueOffset := selected.FrameOffset
		if channel == "frame" {
			if edit.To < 0 {
				return nil, ProjectTransformPatchReport{},
					fmt.Errorf("edit %d: frame must be non-negative", editIndex)
			}
		} else {
			channelIndex := -1
			for index, current := range timeline.Channels {
				if current == channel {
					channelIndex = index
					break
				}
			}
			if channelIndex < 0 {
				return nil, ProjectTransformPatchReport{}, fmt.Errorf(
					"edit %d: channel %q is invalid for %s",
					editIndex,
					edit.Channel,
					timelineType,
				)
			}
			currentValue = selected.Values[channelIndex]
			valueOffset = selected.ValueOffsets[channelIndex]
		}
		if math.Float32bits(currentValue) != math.Float32bits(edit.From) {
			return nil, ProjectTransformPatchReport{}, fmt.Errorf(
				"edit %d: key channel value is %v, expected %v",
				editIndex,
				currentValue,
				edit.From,
			)
		}
		binary.BigEndian.PutUint32(
			payload[valueOffset:valueOffset+4],
			math.Float32bits(edit.To),
		)
		report.Changes = append(report.Changes, ProjectTransformValueChange{
			BoneReference: edit.BoneReference,
			Timeline:      timelineType,
			KeyIndex:      edit.KeyIndex,
			Channel:       channel,
			Frame:         selected.Frame,
			From:          edit.From,
			To:            edit.To,
			Offset:        valueOffset,
		})
	}
	if err := validateProjectTransformFrameOrder(payload, directory); err != nil {
		return nil, ProjectTransformPatchReport{}, err
	}

	if strings.TrimSpace(patch.TargetAnimation) != "" &&
		patch.TargetAnimation != patch.Animation {
		payload, err = renameProjectAnimationRecord(
			payload,
			directory.RegionStart,
			patch.Animation,
			patch.TargetAnimation,
		)
		if err != nil {
			return nil, ProjectTransformPatchReport{}, err
		}
		sourceName, _ := encodeProjectString(patch.Animation)
		targetName, _ := encodeProjectString(patch.TargetAnimation)
		delta := len(targetName) - len(sourceName)
		for index := range report.Changes {
			report.Changes[index].Offset += delta
		}
		report.RegionEnd += delta
	}

	return &ProjectDocument{
		Inspection: document.Inspection,
		Payload:    payload,
	}, report, nil
}

func validateProjectTransformFrameOrder(
	payload []byte,
	directory *ProjectTransformTimelineDirectory,
) error {
	for _, timeline := range directory.Timelines {
		var previous float32
		for index, key := range timeline.Keys {
			frame := readProjectFloat32(payload, key.FrameOffset)
			if !finiteProjectFloat(frame) || frame < 0 {
				return fmt.Errorf(
					"%s boneReference %d key %d has invalid frame %v",
					timeline.Type,
					timeline.BoneReference,
					index,
					frame,
				)
			}
			if index > 0 && frame <= previous {
				return fmt.Errorf(
					"%s boneReference %d frames are not strictly increasing at key %d: %v <= %v",
					timeline.Type,
					timeline.BoneReference,
					index,
					frame,
					previous,
				)
			}
			previous = frame
		}
	}
	return nil
}

func discoverProjectTransformTimelinesInGroup(
	payload []byte,
	start int,
	end int,
	boneReference int,
) []ProjectTransformTimeline {
	timelines := make([]ProjectTransformTimeline, 0, 4)
	for offset := start; offset+len(projectTimelinePrefix) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		timelineReference, cursor, ok := readPositiveVarint(
			payload,
			offset+len(projectTimelinePrefix),
		)
		if !ok || cursor+2 >= end || payload[cursor+1] != 0x01 {
			continue
		}
		timelineType, channels, componentCount, ok := projectTransformType(
			payload[cursor],
		)
		if !ok {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, keyReference, next, ok := readProjectTransformKeys(
			payload,
			keyCursor,
			end,
			timelineReference,
			keyCount,
			componentCount,
		)
		if !ok {
			continue
		}
		timelines = append(timelines, ProjectTransformTimeline{
			Type:              timelineType,
			Channels:          channels,
			BoneReference:     boneReference,
			TimelineReference: timelineReference,
			KeyReference:      keyReference,
			Offset:            offset,
			Keys:              keys,
		})
		offset = next - 1
	}
	return timelines
}

func projectTransformType(
	value byte,
) (string, []string, int, bool) {
	switch value {
	case 0:
		return ProjectTimelineRotate, []string{"value"}, 1, true
	case 1:
		return ProjectTimelineTranslate, []string{"x", "y"}, 2, true
	case 2:
		return ProjectTimelineScale, []string{"x", "y"}, 2, true
	case 3:
		return ProjectTimelineShear, []string{"x", "y"}, 2, true
	default:
		return "", nil, 0, false
	}
}

func readProjectTransformKeys(
	payload []byte,
	offset int,
	end int,
	timelineReference int,
	count int,
	componentCount int,
) ([]ProjectTransformKey, int, int, bool) {
	keys := make([]ProjectTransformKey, 0, count)
	keyReference := 0
	cursor := offset
	valueAndCurveBytes := 5 + 24*componentCount
	for index := 0; index < count; index++ {
		keyOffset := cursor
		if cursor+len(projectTimelineKeyPrefix) > end ||
			!bytes.Equal(
				payload[cursor:cursor+len(projectTimelineKeyPrefix)],
				projectTimelineKeyPrefix,
			) {
			return nil, 0, offset, false
		}
		currentTimelineReference, next, ok := readPositiveVarint(
			payload,
			cursor+len(projectTimelineKeyPrefix),
		)
		if !ok || currentTimelineReference != timelineReference {
			return nil, 0, offset, false
		}
		currentKeyReference, next, ok := readPositiveVarint(payload, next)
		if !ok || next+valueAndCurveBytes > end {
			return nil, 0, offset, false
		}
		if index == 0 {
			keyReference = currentKeyReference
		} else if currentKeyReference != keyReference {
			return nil, 0, offset, false
		}
		frame := readProjectFloat32(payload, next)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, 0, offset, false
		}
		key := ProjectTransformKey{
			Index:        index,
			Frame:        frame,
			Time:         frame / projectAnimationFrameRate,
			Values:       make([]float32, componentCount),
			Offset:       keyOffset,
			FrameOffset:  next,
			ValueOffsets: make([]int, componentCount),
			Curves:       make([][4]float32, componentCount),
			CurveFlags:   make([]byte, 4*componentCount+1),
		}
		for component := 0; component < componentCount; component++ {
			valueOffset := next + 4 + component*4
			key.Values[component] = readProjectFloat32(payload, valueOffset)
			key.ValueOffsets[component] = valueOffset
			if !finiteProjectFloat(key.Values[component]) {
				return nil, 0, offset, false
			}
		}
		curveBase := next + 4 + componentCount*4
		flagCursor := 0
		for component := 0; component < componentCount; component++ {
			curveOffset := curveBase + component*20
			for curveIndex := 0; curveIndex < 4; curveIndex++ {
				key.Curves[component][curveIndex] = readProjectFloat32(
					payload,
					curveOffset+curveIndex*4,
				)
				if !finiteProjectFloat(key.Curves[component][curveIndex]) {
					return nil, 0, offset, false
				}
			}
			flagCount := 4
			if component == componentCount-1 {
				flagCount = 5
			}
			copy(
				key.CurveFlags[flagCursor:flagCursor+flagCount],
				payload[curveOffset+16:curveOffset+16+flagCount],
			)
			flagCursor += flagCount
		}
		keys = append(keys, key)
		cursor = next + valueAndCurveBytes
	}
	return keys, keyReference, cursor, true
}

func readProjectFloat32(payload []byte, offset int) float32 {
	return math.Float32frombits(binary.BigEndian.Uint32(payload[offset:]))
}

func finiteProjectFloat(value float32) bool {
	return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
}
