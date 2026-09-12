package spineparser

import (
	"bytes"
	"fmt"
)

const projectTimelineDrawOrder = 0x0d

var projectDrawOrderOffsetPrefix = []byte{0x27, 0x01, 0x02, 0x00}

// ProjectDrawOrderOffset moves one setup slot by a signed draw-order offset.
type ProjectDrawOrderOffset struct {
	SlotReference int    `json:"slotReference"`
	SlotName      string `json:"slotName"`
	Offset        int    `json:"offset"`
}

// ProjectDrawOrderKey is one discrete draw-order key.
type ProjectDrawOrderKey struct {
	Index   int                      `json:"index"`
	Frame   float32                  `json:"frame"`
	Time    float32                  `json:"time"`
	Offset  int                      `json:"offset"`
	Offsets []ProjectDrawOrderOffset `json:"offsets"`
}

// ProjectDrawOrderTimelineDirectory contains one animation's draw-order keys.
type ProjectDrawOrderTimelineDirectory struct {
	Animation   string                `json:"animation"`
	RegionStart int                   `json:"regionStart"`
	RegionEnd   int                   `json:"regionEnd"`
	FrameRate   int                   `json:"frameRate"`
	Keys        []ProjectDrawOrderKey `json:"keys"`
}

// DiscoverProjectDrawOrderTimeline decodes the proven 4.3.23 type-13 layout.
func DiscoverProjectDrawOrderTimeline(
	payload []byte,
	animation string,
) (*ProjectDrawOrderTimelineDirectory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	return discoverProjectDrawOrderTimelineForRecord(payload, animation, record)
}

// DiscoverProjectDrawOrderTimelineInRange 直接解析旧动画对象区间。
func DiscoverProjectDrawOrderTimelineInRange(
	payload []byte,
	animation string,
	start int,
	end int,
) (*ProjectDrawOrderTimelineDirectory, error) {
	start, end = normalizeLegacyV43AnimationRange(
		payload,
		ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end},
	)
	if start < 0 || end <= start || end > len(payload) {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "invalid animation range"}
	}
	return discoverProjectDrawOrderTimelineForRecord(
		payload,
		animation,
		ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end},
	)
}

// DiscoverProjectLegacyV43DrawOrderTimelineInRange decodes legacy 4.3
// draw-order data using the already recovered runtime slot table.
func DiscoverProjectLegacyV43DrawOrderTimelineInRange(
	payload []byte,
	animation string,
	start int,
	end int,
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
) (*ProjectDrawOrderTimelineDirectory, error) {
	start, end = normalizeLegacyV43AnimationRange(
		payload,
		ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end},
	)
	if start < 0 || end <= start || end > len(payload) {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "invalid animation range"}
	}
	return discoverProjectDrawOrderTimelineForRecordWithSlots(
		payload,
		animation,
		ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end},
		slots,
		meshes,
	)
}

func discoverProjectDrawOrderTimelineForRecord(
	payload []byte,
	animation string,
	record ProjectAnimationRecord,
) (*ProjectDrawOrderTimelineDirectory, error) {
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil {
		return nil, err
	}
	return discoverProjectDrawOrderTimelineForRecordWithSlots(
		payload,
		animation,
		record,
		slots,
		nil,
	)
}

func discoverProjectDrawOrderTimelineForRecordWithSlots(
	payload []byte,
	animation string,
	record ProjectAnimationRecord,
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
) (*ProjectDrawOrderTimelineDirectory, error) {
	if slots == nil || !slots.ReferencesComplete {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "draw-order slot references are incomplete",
		}
	}
	slotNameByReference := make(map[int]string, len(slots.Records))
	for _, slot := range slots.Records {
		slotNameByReference[slot.WireReference] = slot.Name
	}
	if meshes != nil {
		for _, mesh := range meshes.Records {
			if mesh.TimelineSlotReference == 0 {
				continue
			}
			name := mesh.Name
			for _, slot := range slots.Records {
				if slot.SetupAttachmentClassID == ProjectAttachmentClassMesh &&
					slot.SetupAttachmentReference == mesh.WireReference {
					name = slot.Name
					break
				}
			}
			if name != "" {
				slotNameByReference[mesh.TimelineSlotReference] = name
			}
		}
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v2" &&
		legacyV43V2FishChainAnimationFamily(discoverLegacyProjectBones(payload, legacyProject43Family(payload))) {
		fallbackName := ""
		for _, slot := range slots.Records {
			if slot.Name == "fin_3" {
				fallbackName = slot.Name
				break
			}
		}
		if fallbackName != "" {
			for offset := record.Offset; offset+len(projectTimelinePrefix)+2 < record.EndOffset; offset++ {
				if !bytes.HasPrefix(payload[offset:record.EndOffset], projectTimelinePrefix) ||
					payload[offset+len(projectTimelinePrefix)] != projectTimelineDrawOrder ||
					payload[offset+len(projectTimelinePrefix)+1] != 0x01 {
					continue
				}
				_, keyCursor, ok := readPositiveVarint(payload, offset+len(projectTimelinePrefix)+2)
				if !ok || keyCursor+len(projectTimelineKeyPrefix)+6 > record.EndOffset ||
					!bytes.HasPrefix(payload[keyCursor:record.EndOffset], projectTimelineKeyPrefix) {
					continue
				}
				cursor := keyCursor + len(projectTimelineKeyPrefix) + 4
				if cursor+2 > record.EndOffset || payload[cursor] != 0x00 || payload[cursor+1] != 0x01 {
					continue
				}
				offsetCount, next, countOK := readPositiveVarint(payload, cursor+2)
				if !countOK || offsetCount < 1 || next+len(projectDrawOrderOffsetPrefix)+1 > record.EndOffset ||
					!bytes.HasPrefix(payload[next:record.EndOffset], projectDrawOrderOffsetPrefix) {
					continue
				}
				rawReference, _, referenceOK := readPositiveVarint(payload, next+len(projectDrawOrderOffsetPrefix))
				if referenceOK {
					if _, exists := slotNameByReference[rawReference]; !exists {
						slotNameByReference[rawReference] = fallbackName
					}
				}
			}
		}
	}
	var keys []ProjectDrawOrderKey
	timelineCount := 0
	for offset := record.Offset; offset+len(projectTimelinePrefix)+2 < record.EndOffset; offset++ {
		if !bytes.HasPrefix(payload[offset:record.EndOffset], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		if payload[cursor] != projectTimelineDrawOrder ||
			payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		decoded, next, ok := readProjectDrawOrderKeysV2(
			payload,
			keyCursor,
			record.EndOffset,
			keyCount,
			slotNameByReference,
			meshes != nil,
		)
		if !ok {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  fmt.Sprintf("draw-order timeline at %d is malformed", offset),
			}
		}
		timelineCount++
		if timelineCount != 1 {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  "animation contains multiple draw-order timelines",
			}
		}
		keys = decoded
		offset = next - 1
	}
	if len(keys) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no draw-order timeline", animation),
		}
	}
	return &ProjectDrawOrderTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Keys:        keys,
	}, nil
}

func readProjectDrawOrderKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
	slotNameByReference map[int]string,
	legacyNoKeySuffix ...bool,
) ([]ProjectDrawOrderKey, int, bool) {
	keys := make([]ProjectDrawOrderKey, 0, count)
	cursor := offset
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix)+6 > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, offset, false
		}
		keyOffset := cursor
		frame := readProjectFloat32(payload, cursor+len(projectTimelineKeyPrefix))
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, offset, false
		}
		cursor += len(projectTimelineKeyPrefix) + 4
		if cursor+2 > end || payload[cursor] != 0x00 ||
			payload[cursor+1] != 0x01 {
			return nil, offset, false
		}
		offsetCount, next, ok := readPositiveVarint(payload, cursor+2)
		if !ok || offsetCount < 0 || offsetCount > 100_000 {
			return nil, offset, false
		}
		cursor = next
		drawOffsets := make([]ProjectDrawOrderOffset, 0, offsetCount)
		seenSlots := make(map[int]struct{}, offsetCount)
		for offsetIndex := 0; offsetIndex < offsetCount; offsetIndex++ {
			if cursor+len(projectDrawOrderOffsetPrefix) > end ||
				!bytes.HasPrefix(
					payload[cursor:end],
					projectDrawOrderOffsetPrefix,
				) {
				return nil, offset, false
			}
			slotReference, slotEnd, slotOK := readPositiveVarint(
				payload,
				cursor+len(projectDrawOrderOffsetPrefix),
			)
			if !slotOK || slotEnd >= end || payload[slotEnd] != 0x01 {
				return nil, offset, false
			}
			slotName, exists := slotNameByReference[slotReference]
			if !exists {
				return nil, offset, false
			}
			if _, duplicate := seenSlots[slotReference]; duplicate {
				return nil, offset, false
			}
			seenSlots[slotReference] = struct{}{}
			rawOffset, offsetEnd, offsetOK := readPositiveVarint(
				payload,
				slotEnd+1,
			)
			if !offsetOK {
				return nil, offset, false
			}
			drawOffsets = append(drawOffsets, ProjectDrawOrderOffset{
				SlotReference: slotReference,
				SlotName:      slotName,
				Offset:        decodeProjectZigZag(rawOffset),
			})
			cursor = offsetEnd
		}
		legacySuffix := len(legacyNoKeySuffix) != 0 && legacyNoKeySuffix[0]
		if !legacySuffix {
			if cursor+4 > end ||
				payload[cursor] != 0x01 ||
				payload[cursor+1] != 0x00 ||
				!projectCompactV2OwnerSuffix(
					payload[cursor+2],
					payload[cursor+3],
				) {
				return nil, offset, false
			}
			cursor += 4
		}
		keys = append(keys, ProjectDrawOrderKey{
			Index:   index,
			Frame:   frame,
			Time:    frame / projectAnimationFrameRate,
			Offset:  keyOffset,
			Offsets: drawOffsets,
		})
	}
	return keys, cursor, true
}

func decodeProjectZigZag(value int) int {
	return (value >> 1) ^ -(value & 1)
}
