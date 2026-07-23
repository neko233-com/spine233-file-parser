package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

const projectTimelineAttachment = 5

// ProjectSlotAttachmentKey is one attachment-switch key. Frame is the editor
// frame number; Time is Frame / 30.
type ProjectSlotAttachmentKey struct {
	Index       int     `json:"index"`
	Frame       float32 `json:"frame"`
	Time        float32 `json:"time"`
	Offset      int     `json:"offset"`
	FrameOffset int     `json:"frameOffset"`
}

// ProjectSlotAttachmentTimeline identifies an attachment timeline by its
// stable Kryo slot reference.
type ProjectSlotAttachmentTimeline struct {
	SlotReference     int                        `json:"slotReference"`
	TimelineReference int                        `json:"timelineReference"`
	KeyReference      int                        `json:"keyReference"`
	Offset            int                        `json:"offset"`
	Keys              []ProjectSlotAttachmentKey `json:"keys"`
}

// ProjectSlotAttachmentTimelineDirectory contains attachment timelines from
// one top-level animation record.
type ProjectSlotAttachmentTimelineDirectory struct {
	Animation   string                          `json:"animation"`
	RegionStart int                             `json:"regionStart"`
	RegionEnd   int                             `json:"regionEnd"`
	FrameRate   int                             `json:"frameRate"`
	Timelines   []ProjectSlotAttachmentTimeline `json:"timelines"`
}

// DiscoverProjectSlotAttachmentTimelines decodes fixed-topology attachment
// keys without resolving proprietary attachment object references.
func DiscoverProjectSlotAttachmentTimelines(
	payload []byte,
	animation string,
) (*ProjectSlotAttachmentTimelineDirectory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	groups := discoverProjectBoneTimelineGroups(
		payload,
		record.Offset,
		record.EndOffset,
	)
	timelines := make([]ProjectSlotAttachmentTimeline, 0)
	for index, group := range groups {
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1].Offset
		}
		timelines = append(
			timelines,
			discoverProjectSlotAttachmentTimelinesInGroup(
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
			Msg: fmt.Sprintf(
				"animation %q contains no supported slot attachment timelines",
				animation,
			),
		}
	}
	return &ProjectSlotAttachmentTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

// ProjectSlotAttachmentFrameEdit retimes one existing attachment key.
type ProjectSlotAttachmentFrameEdit struct {
	SlotReference     int     `json:"slotReference"`
	TimelineReference int     `json:"timelineReference"`
	TimelineOffset    int     `json:"timelineOffset"`
	KeyIndex          int     `json:"keyIndex"`
	From              float32 `json:"from"`
	To                float32 `json:"to"`
}

// ProjectSlotAttachmentPatch controls attachment-key retiming and optional
// animation renaming.
type ProjectSlotAttachmentPatch struct {
	Animation       string                           `json:"animation"`
	TargetAnimation string                           `json:"targetAnimation,omitempty"`
	Edits           []ProjectSlotAttachmentFrameEdit `json:"edits"`
}

// ProjectSlotAttachmentFrameChange reports one exact attachment-key edit.
type ProjectSlotAttachmentFrameChange struct {
	SlotReference     int     `json:"slotReference"`
	TimelineReference int     `json:"timelineReference"`
	TimelineOffset    int     `json:"timelineOffset"`
	KeyIndex          int     `json:"keyIndex"`
	From              float32 `json:"from"`
	To                float32 `json:"to"`
	Offset            int     `json:"offset"`
}

// ProjectSlotAttachmentPatchReport is safe to inspect before serialization.
type ProjectSlotAttachmentPatchReport struct {
	Animation       string                             `json:"animation"`
	TargetAnimation string                             `json:"targetAnimation,omitempty"`
	RegionStart     int                                `json:"regionStart"`
	RegionEnd       int                                `json:"regionEnd"`
	Changes         []ProjectSlotAttachmentFrameChange `json:"changes"`
}

// PatchProjectSlotAttachmentFrames clones a project and retimes explicitly
// selected attachment keys. It never mutates document.
func PatchProjectSlotAttachmentFrames(
	document *ProjectDocument,
	patch ProjectSlotAttachmentPatch,
) (*ProjectDocument, ProjectSlotAttachmentPatchReport, error) {
	if document == nil || len(document.Payload) == 0 {
		return nil, ProjectSlotAttachmentPatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	if len(patch.Edits) == 0 {
		return nil, ProjectSlotAttachmentPatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "at least one attachment edit is required"}
	}
	directory, err := DiscoverProjectSlotAttachmentTimelines(
		document.Payload,
		patch.Animation,
	)
	if err != nil {
		return nil, ProjectSlotAttachmentPatchReport{}, err
	}
	byOffset := make(map[int][]ProjectSlotAttachmentTimeline)
	for _, timeline := range directory.Timelines {
		byOffset[timeline.Offset] = append(
			byOffset[timeline.Offset],
			timeline,
		)
	}
	payload := append([]byte(nil), document.Payload...)
	report := ProjectSlotAttachmentPatchReport{
		Animation:       patch.Animation,
		TargetAnimation: patch.TargetAnimation,
		RegionStart:     directory.RegionStart,
		RegionEnd:       directory.RegionEnd,
		Changes:         make([]ProjectSlotAttachmentFrameChange, 0, len(patch.Edits)),
	}
	seen := make(map[[2]int]struct{}, len(patch.Edits))
	for editIndex, edit := range patch.Edits {
		if !finiteProjectFloat(edit.From) || !finiteProjectFloat(edit.To) ||
			edit.To < 0 {
			return nil, ProjectSlotAttachmentPatchReport{}, fmt.Errorf(
				"edit %d: attachment frames must be finite and target non-negative",
				editIndex,
			)
		}
		if math.Float32bits(edit.From) == math.Float32bits(edit.To) {
			return nil, ProjectSlotAttachmentPatchReport{},
				fmt.Errorf("edit %d: from and to must differ", editIndex)
		}
		selection := [2]int{edit.TimelineOffset, edit.KeyIndex}
		if _, duplicate := seen[selection]; duplicate {
			return nil, ProjectSlotAttachmentPatchReport{},
				fmt.Errorf(
					"edit %d: duplicate timelineOffset/keyIndex",
					editIndex,
				)
		}
		seen[selection] = struct{}{}
		matches := byOffset[edit.TimelineOffset]
		if len(matches) != 1 {
			return nil, ProjectSlotAttachmentPatchReport{}, fmt.Errorf(
				"edit %d: timelineOffset %d matched %d attachment timelines",
				editIndex,
				edit.TimelineOffset,
				len(matches),
			)
		}
		timeline := matches[0]
		if timeline.SlotReference != edit.SlotReference ||
			timeline.TimelineReference != edit.TimelineReference {
			return nil, ProjectSlotAttachmentPatchReport{}, fmt.Errorf(
				"edit %d: timeline identity is slotReference %d timelineReference %d, expected %d/%d",
				editIndex,
				timeline.SlotReference,
				timeline.TimelineReference,
				edit.SlotReference,
				edit.TimelineReference,
			)
		}
		if edit.KeyIndex < 0 || edit.KeyIndex >= len(timeline.Keys) {
			return nil, ProjectSlotAttachmentPatchReport{}, fmt.Errorf(
				"edit %d: keyIndex %d is outside [0,%d)",
				editIndex,
				edit.KeyIndex,
				len(timeline.Keys),
			)
		}
		key := timeline.Keys[edit.KeyIndex]
		if math.Float32bits(key.Frame) != math.Float32bits(edit.From) {
			return nil, ProjectSlotAttachmentPatchReport{}, fmt.Errorf(
				"edit %d: key frame is %v, expected %v",
				editIndex,
				key.Frame,
				edit.From,
			)
		}
		binary.BigEndian.PutUint32(
			payload[key.FrameOffset:key.FrameOffset+4],
			math.Float32bits(edit.To),
		)
		report.Changes = append(report.Changes, ProjectSlotAttachmentFrameChange{
			SlotReference:     edit.SlotReference,
			TimelineReference: edit.TimelineReference,
			TimelineOffset:    edit.TimelineOffset,
			KeyIndex:          edit.KeyIndex,
			From:              edit.From,
			To:                edit.To,
			Offset:            key.FrameOffset,
		})
	}
	if err := validateProjectSlotAttachmentFrameOrder(payload, directory); err != nil {
		return nil, ProjectSlotAttachmentPatchReport{}, err
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
			return nil, ProjectSlotAttachmentPatchReport{}, err
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

func discoverProjectSlotAttachmentTimelinesInGroup(
	payload []byte,
	start int,
	end int,
	slotReference int,
) []ProjectSlotAttachmentTimeline {
	timelines := make([]ProjectSlotAttachmentTimeline, 0, 1)
	for offset := start; offset+len(projectTimelinePrefix) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		timelineReference, cursor, ok := readPositiveVarint(
			payload,
			offset+len(projectTimelinePrefix),
		)
		if !ok || cursor+2 >= end ||
			payload[cursor] != projectTimelineAttachment ||
			payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, keyReference, next, ok := readProjectSlotAttachmentKeys(
			payload,
			keyCursor,
			end,
			timelineReference,
			keyCount,
		)
		if !ok {
			continue
		}
		timelines = append(timelines, ProjectSlotAttachmentTimeline{
			SlotReference:     slotReference,
			TimelineReference: timelineReference,
			KeyReference:      keyReference,
			Offset:            offset,
			Keys:              keys,
		})
		offset = next - 1
	}
	return timelines
}

func readProjectSlotAttachmentKeys(
	payload []byte,
	offset int,
	end int,
	timelineReference int,
	count int,
) ([]ProjectSlotAttachmentKey, int, int, bool) {
	keys := make([]ProjectSlotAttachmentKey, 0, count)
	keyReference := 0
	cursor := offset
	for index := 0; index < count; index++ {
		keyOffset, currentKeyReference, frameOffset, ok :=
			findProjectSlotAttachmentKey(
				payload,
				cursor,
				end,
				timelineReference,
				keyReference,
			)
		if !ok {
			return nil, 0, offset, false
		}
		if index == 0 {
			keyReference = currentKeyReference
		}
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, 0, offset, false
		}
		keys = append(keys, ProjectSlotAttachmentKey{
			Index:       index,
			Frame:       frame,
			Time:        frame / projectAnimationFrameRate,
			Offset:      keyOffset,
			FrameOffset: frameOffset,
		})
		cursor = frameOffset + 4
	}
	return keys, keyReference, cursor, true
}

func findProjectSlotAttachmentKey(
	payload []byte,
	start int,
	end int,
	timelineReference int,
	keyReference int,
) (int, int, int, bool) {
	for cursor := start; cursor+len(projectTimelineKeyPrefix) < end; {
		relative := bytes.Index(payload[cursor:end], projectTimelineKeyPrefix)
		if relative < 0 {
			break
		}
		keyOffset := cursor + relative
		currentTimelineReference, next, ok := readPositiveVarint(
			payload,
			keyOffset+len(projectTimelineKeyPrefix),
		)
		if !ok || currentTimelineReference != timelineReference {
			cursor = keyOffset + 1
			continue
		}
		currentKeyReference, frameOffset, ok := readPositiveVarint(payload, next)
		if !ok || frameOffset+4 > end ||
			(keyReference != 0 && currentKeyReference != keyReference) {
			cursor = keyOffset + 1
			continue
		}
		return keyOffset, currentKeyReference, frameOffset, true
	}
	return 0, 0, 0, false
}

func validateProjectSlotAttachmentFrameOrder(
	payload []byte,
	directory *ProjectSlotAttachmentTimelineDirectory,
) error {
	for _, timeline := range directory.Timelines {
		var previous float32
		for index, key := range timeline.Keys {
			frame := readProjectFloat32(payload, key.FrameOffset)
			if !finiteProjectFloat(frame) || frame < 0 {
				return fmt.Errorf(
					"attachment slotReference %d key %d has invalid frame %v",
					timeline.SlotReference,
					index,
					frame,
				)
			}
			if index > 0 && frame <= previous {
				return fmt.Errorf(
					"attachment slotReference %d frames are not strictly increasing at key %d: %v <= %v",
					timeline.SlotReference,
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
