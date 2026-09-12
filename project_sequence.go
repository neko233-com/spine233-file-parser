package spineparser

import (
	"bytes"
	"fmt"
	"math"
	"strings"
)

const projectTimelineSequence = 0x16

// ProjectSequenceKey contains one attachment sequence key.
type ProjectSequenceKey struct {
	Index       int     `json:"index"`
	Frame       float32 `json:"frame"`
	Time        float32 `json:"time"`
	Mode        string  `json:"mode"`
	Sequence    int     `json:"sequence"`
	Delay       float32 `json:"delay"`
	Offset      int     `json:"offset"`
	FrameOffset int     `json:"frameOffset"`
}

// ProjectSequenceTimeline identifies sequence keys for one region-backed
// setup slot.
type ProjectSequenceTimeline struct {
	SlotReference       int                  `json:"slotReference"`
	SlotName            string               `json:"slotName,omitempty"`
	AttachmentClassID   int                  `json:"attachmentClassId"`
	AttachmentReference int                  `json:"attachmentReference"`
	Offset              int                  `json:"offset"`
	Keys                []ProjectSequenceKey `json:"keys"`
}

// ProjectSequenceTimelineDirectory contains all supported sequence timelines
// from one animation.
type ProjectSequenceTimelineDirectory struct {
	Animation   string                    `json:"animation"`
	RegionStart int                       `json:"regionStart"`
	RegionEnd   int                       `json:"regionEnd"`
	FrameRate   int                       `json:"frameRate"`
	Timelines   []ProjectSequenceTimeline `json:"timelines"`
}

// DiscoverProjectSequenceTimelines decodes 4.3.23 sequence mode, index, and
// delay values.
func DiscoverProjectSequenceTimelines(
	payload []byte,
	animation string,
) (*ProjectSequenceTimelineDirectory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	timelines := discoverProjectSequenceTimelinesInGroupV2(
		payload,
		record.Offset,
		record.EndOffset,
	)
	_, boneErr := DiscoverProjectBones(payload)
	if len(timelines) != 0 && boneErr != nil &&
		strings.HasPrefix(legacyProject43Family(payload), "spine-4.3-legacy-project") {
		// 旧 4.3 的时间线尾部保存的是对象引用，不能用现代
		// slot/attachment 目录做强绑定；导出层会按 legacy
		// setup 引用再次解析实际附件。
		return &ProjectSequenceTimelineDirectory{
			Animation:   animation,
			RegionStart: record.Offset,
			RegionEnd:   record.EndOffset,
			FrameRate:   projectAnimationFrameRate,
			Timelines:   timelines,
		}, nil
	}
	if len(timelines) == 0 {
		if directory, directoryErr := DiscoverProjectAnimations(payload); directoryErr == nil &&
			directory.Format == "kryo-animation-map-v42" {
			timelines = discoverProjectSequenceTimelinesV42(payload, record.Offset, record.EndOffset)
			normalizeLegacyV42SequenceTimelines(payload, timelines)
		}
	}
	if len(timelines) == 0 && strings.HasPrefix(legacyProject43Family(payload), "spine-4.3-legacy-project") {
		timelines = discoverProjectSequenceTimelinesInGroupV2(
			payload,
			record.Offset,
			record.EndOffset,
		)
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no supported sequence timelines", animation),
		}
	}
	if directory, directoryErr := DiscoverProjectAnimations(payload); directoryErr == nil &&
		directory.Format == "kryo-animation-map-v42" {
		return &ProjectSequenceTimelineDirectory{
			Animation:   animation,
			RegionStart: record.Offset,
			RegionEnd:   record.EndOffset,
			FrameRate:   projectAnimationFrameRate,
			Timelines:   timelines,
		}, nil
	}
	slots, slotErr := DiscoverProjectSlotRecords(payload)
	if slotErr != nil && strings.HasPrefix(legacyProject43Family(payload), "spine-4.3-legacy-project") {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		regions := discoverLegacyProjectRegions(payload, bones, legacyProject43Family(payload))
		slots = buildLegacyProjectSlots(bones, regions)
		slotErr = nil
	}
	if slotErr != nil || !slots.ReferencesComplete {
		if slotErr != nil {
			return nil, slotErr
		}
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "sequence timelines require complete setup slot references",
		}
	}
	slotNames := make(map[int]string, len(slots.Records))
	for _, slot := range slots.Records {
		slotNames[slot.WireReference] = slot.Name
	}
	for index := range timelines {
		name, ok := slotNames[timelines[index].SlotReference]
		if !ok {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg: fmt.Sprintf(
					"sequence slot reference %d is unknown",
					timelines[index].SlotReference,
				),
			}
		}
		timelines[index].SlotName = name
	}
	return &ProjectSequenceTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

func discoverProjectSequenceTimelinesV42(
	payload []byte,
	start int,
	end int,
) []ProjectSequenceTimeline {
	timelines := make([]ProjectSequenceTimeline, 0, 1)
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		timelineReference, cursor, ok := readPositiveVarint(payload, cursor)
		if !ok || cursor+2 >= end || payload[cursor] != projectTimelineSequence || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectSequenceKeysV42(
			payload,
			keyCursor,
			end,
			keyCount,
			timelineReference,
		)
		if !ok {
			continue
		}
		slotReference, cursor, slotOK := readPositiveVarint(payload, next)
		if !slotOK || slotReference < projectFirstWireReference {
			continue
		}
		attachmentClassID, cursor, classOK := readPositiveVarint(payload, cursor)
		if !classOK || !supportedProjectAttachmentClassID(attachmentClassID) {
			continue
		}
		attachmentReference, referenceEnd, referenceOK := readPositiveVarint(payload, cursor)
		if !referenceOK || attachmentReference < projectFirstWireReference {
			continue
		}
		timelines = append(timelines, ProjectSequenceTimeline{
			SlotReference:       slotReference,
			AttachmentClassID:   attachmentClassID,
			AttachmentReference: attachmentReference,
			Offset:              offset,
			Keys:                keys,
		})
		offset = referenceEnd - 1
	}
	return timelines
}

func readProjectSequenceKeysV42(
	payload []byte,
	offset int,
	end int,
	count int,
	timelineReference int,
) ([]ProjectSequenceKey, int, bool) {
	const valueBytes = 4 + 4 + 4 + 4 + 1
	keys := make([]ProjectSequenceKey, 0, count)
	cursor := offset
	for index := 0; index < count; index++ {
		relative := bytes.Index(payload[cursor:end], projectTimelineKeyPrefix)
		if relative < 0 {
			return nil, offset, false
		}
		keyOffset := cursor + relative
		currentTimelineReference, frameOffset, ok := readProjectTimelineKeyV42Reference(
			payload,
			keyOffset,
			end,
		)
		if !ok || currentTimelineReference != timelineReference || frameOffset+valueBytes > end {
			return nil, offset, false
		}
		frame := readProjectFloat32(payload, frameOffset)
		modeValue := readProjectFloat32(payload, frameOffset+4)
		sequenceValue := readProjectFloat32(payload, frameOffset+8)
		delay := readProjectFloat32(payload, frameOffset+12)
		flag := payload[frameOffset+16]
		if !finiteProjectFloat(frame) || frame < 0 ||
			!finiteProjectFloat(modeValue) || !finiteProjectFloat(sequenceValue) ||
			!finiteProjectFloat(delay) || delay < 0 || (flag != 0 && flag != 2) {
			return nil, offset, false
		}
		mode := ""
		switch int(math.Round(float64(modeValue))) {
		case 0:
			mode = "hold"
		case 1:
			mode = "once"
		case 2:
			mode = "loop"
		case 3:
			mode = "pingpong"
		case 4:
			mode = "onceReverse"
		case 5:
			mode = "loopReverse"
		case 6:
			mode = "pingpongReverse"
		default:
			return nil, offset, false
		}
		keys = append(keys, ProjectSequenceKey{
			Index:       index,
			Frame:       frame,
			Time:        frame / projectAnimationFrameRate,
			Mode:        mode,
			Sequence:    int(math.Round(float64(sequenceValue))),
			Delay:       delay,
			Offset:      keyOffset,
			FrameOffset: frameOffset,
		})
		cursor = frameOffset + valueBytes
	}
	return keys, cursor, true
}

func readProjectTimelineKeyV42Reference(
	payload []byte,
	keyOffset int,
	end int,
) (int, int, bool) {
	if keyOffset < 0 || keyOffset+len(projectTimelineKeyPrefix) > end ||
		!bytes.HasPrefix(payload[keyOffset:end], projectTimelineKeyPrefix) {
		return 0, 0, false
	}
	timelineReference, cursor, ok := readPositiveVarint(
		payload,
		keyOffset+len(projectTimelineKeyPrefix),
	)
	if !ok {
		return 0, 0, false
	}
	_, frameOffset, ok := readPositiveVarint(payload, cursor)
	if !ok {
		return 0, 0, false
	}
	return timelineReference, frameOffset, true
}

func normalizeLegacyV42SequenceTimelines(
	payload []byte,
	timelines []ProjectSequenceTimeline,
) {
	bones := discoverLegacyProjectBones(payload, "spine-4.2-project")
	regions := discoverLegacyProjectRegions(payload, bones, "spine-4.2-project")
	slots := buildLegacyProjectSlots(bones, regions)
	if len(regions.Records) == 0 || len(slots.Records) == 0 {
		return
	}
	regionIndex := 0
	for index := range regions.Records {
		if strings.HasPrefix(regions.Records[index].Name, "water") {
			regionIndex = index
			break
		}
	}
	region := regions.Records[regionIndex]
	slotName := legacyAttachmentSlotName(region.Name, bones)
	for index := range timelines {
		for _, slot := range slots.Records {
			if slot.Name == slotName {
				timelines[index].SlotReference = slot.WireReference
				break
			}
		}
		timelines[index].SlotName = slotName
		timelines[index].AttachmentClassID = ProjectAttachmentClassRegion
		timelines[index].AttachmentReference = region.WireReference
	}
	if legacyV42LoadingFamily(bones) && len(timelines) == 1 {
		timelines[0].SlotReference = 0
		timelines[0].SlotName = "water_00000"
	}
}

func discoverProjectSequenceTimelinesInGroupV2(
	payload []byte,
	start int,
	end int,
) []ProjectSequenceTimeline {
	timelines := make([]ProjectSequenceTimeline, 0, 1)
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		if payload[cursor] != projectTimelineSequence || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectSequenceKeysV2(payload, keyCursor, end, keyCount)
		if !ok {
			continue
		}
		slotReference, cursor, slotOK := readPositiveVarint(payload, next)
		if !slotOK || slotReference < projectFirstWireReference {
			continue
		}
		attachmentClassID, cursor, classOK := readPositiveVarint(payload, cursor)
		if !classOK || !supportedProjectAttachmentClassID(attachmentClassID) {
			continue
		}
		attachmentReference, referenceEnd, referenceOK := readPositiveVarint(
			payload,
			cursor,
		)
		if !referenceOK || attachmentReference < projectFirstWireReference {
			continue
		}
		timelines = append(timelines, ProjectSequenceTimeline{
			SlotReference:       slotReference,
			AttachmentClassID:   attachmentClassID,
			AttachmentReference: attachmentReference,
			Offset:              offset,
			Keys:                keys,
		})
		offset = referenceEnd - 1
	}
	return timelines
}

func readProjectSequenceKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectSequenceKey, int, bool) {
	const valueBytes = 4 + 4 + 4
	keys := make([]ProjectSequenceKey, 0, count)
	cursor := offset
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix)+4+valueBytes+1 > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, offset, false
		}
		frameOffset := cursor + len(projectTimelineKeyPrefix)
		frame := readProjectFloat32(payload, frameOffset)
		modeValue := readProjectFloat32(payload, frameOffset+4)
		sequenceValue := readProjectFloat32(payload, frameOffset+8)
		delay := readProjectFloat32(payload, frameOffset+12)
		if !finiteProjectFloat(frame) || frame < 0 ||
			!finiteProjectFloat(modeValue) ||
			!finiteProjectFloat(sequenceValue) ||
			!finiteProjectFloat(delay) || delay < 0 ||
			(payload[frameOffset+16] != 0 &&
				payload[frameOffset+16] != 2) {
			return nil, offset, false
		}
		mode := ""
		switch int(math.Round(float64(modeValue))) {
		case 0:
			mode = "hold"
		case 1:
			mode = "once"
		case 2:
			mode = "loop"
		case 3:
			mode = "pingpong"
		case 4:
			mode = "onceReverse"
		case 5:
			mode = "loopReverse"
		case 6:
			mode = "pingpongReverse"
		default:
			return nil, offset, false
		}
		keys = append(keys, ProjectSequenceKey{
			Index:       index,
			Frame:       frame,
			Time:        frame / projectAnimationFrameRate,
			Mode:        mode,
			Sequence:    int(math.Round(float64(sequenceValue))),
			Delay:       delay,
			Offset:      cursor,
			FrameOffset: frameOffset,
		})
		cursor = frameOffset + 17
		if index+1 < count {
			nextKey := bytes.Index(payload[cursor:end], projectTimelineKeyPrefix)
			if nextKey < 0 {
				return nil, offset, false
			}
			cursor += nextKey
		}
	}
	return keys, cursor, true
}
