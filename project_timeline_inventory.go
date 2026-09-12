package spineparser

import (
	"bytes"
	"fmt"
)

// ProjectTimelineInventory records every 4.3.23 timeline type in one
// animation before semantic timeline decoders run.
type ProjectTimelineInventory struct {
	Animation string        `json:"animation"`
	Total     int           `json:"total"`
	ByType    map[int]int   `json:"byType"`
	Offsets   map[int][]int `json:"offsets"`
}

// DiscoverProjectTimelineInventory provides a completeness guard for
// exporters. It does not claim semantic support for the returned types.
func DiscoverProjectTimelineInventory(
	payload []byte,
	animation string,
) (*ProjectTimelineInventory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	inventory := &ProjectTimelineInventory{
		Animation: animation,
		ByType:    make(map[int]int),
		Offsets:   make(map[int][]int),
	}
	defaultTransformOffsets := discoverProjectDefaultTransformTimelineOffsets(
		payload,
		record,
	)
	for offset := record.Offset; offset+len(projectTimelinePrefix) < record.EndOffset; offset++ {
		if !bytes.HasPrefix(payload[offset:record.EndOffset], projectTimelinePrefix) {
			continue
		}
		timelineType, keyCount, ok := readProjectTimelineHeader(payload, offset, record.EndOffset)
		if !ok {
			continue
		}
		if _, isDefault := defaultTransformOffsets[offset]; isDefault {
			continue
		}
		inventory.Total++
		inventory.ByType[timelineType]++
		inventory.Offsets[timelineType] = append(
			inventory.Offsets[timelineType],
			offset,
		)
		if keyCount > 0 {
			offset++
		}
	}
	if inventory.Total == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no V2 timelines", animation),
		}
	}
	return inventory, nil
}

func discoverProjectDefaultTransformTimelineOffsets(
	payload []byte,
	record ProjectAnimationRecord,
) map[int]struct{} {
	result := make(map[int]struct{})
	groups := discoverProjectBoneTimelineGroups(payload, record.Offset, record.EndOffset)
	for index, group := range groups {
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1].Offset
		}
		for _, timeline := range discoverProjectTransformTimelinesForGroup(payload, group, groupEnd) {
			if projectTransformTimelineIsDefault(timeline) {
				result[timeline.Offset] = struct{}{}
			}
		}
	}
	return result
}

func readProjectTimelineHeader(payload []byte, offset int, end int) (int, int, bool) {
	timelineType, keyCount, _, ok := readProjectTimelineHeaderWithKeyOffset(payload, offset, end)
	return timelineType, keyCount, ok
}

func readProjectTimelineHeaderWithKeyOffset(
	payload []byte,
	offset int,
	end int,
) (int, int, int, bool) {
	typeOffset := offset + len(projectTimelinePrefix)
	if typeOffset+2 >= end {
		return 0, 0, 0, false
	}
	if payload[typeOffset] <= 0x40 && payload[typeOffset+1] == 0x01 {
		keyCount, keyCursor, ok := readPositiveVarint(payload, typeOffset+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			return 0, 0, 0, false
		}
		if keyCursor+len(projectTimelineKeyPrefix) > end ||
			!bytes.HasPrefix(payload[keyCursor:end], projectTimelineKeyPrefix) {
			return 0, 0, 0, false
		}
		return int(payload[typeOffset]), keyCount, keyCursor, true
	}
	_, cursor, ok := readPositiveVarint(payload, typeOffset)
	if !ok || cursor+2 >= end || payload[cursor] > 0x40 || payload[cursor+1] != 0x01 {
		return 0, 0, 0, false
	}
	keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
	if !ok || keyCount < 1 || keyCount > 100_000 {
		return 0, 0, 0, false
	}
	if keyCursor+len(projectTimelineKeyPrefix) > end ||
		!bytes.HasPrefix(payload[keyCursor:end], projectTimelineKeyPrefix) {
		return 0, 0, 0, false
	}
	return int(payload[cursor]), keyCount, keyCursor, true
}
