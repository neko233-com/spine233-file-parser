package spineparser

import (
	"bytes"
	"fmt"
)

var (
	projectV4326AnimationHeaderPrefix = []byte{0x07, 0x0f, 0x01}
	projectV4326AnimationHeaderTail   = []byte{0x12, 0x01, 0x0a}
	projectV4326AnimationKeyPrefix    = []byte{0x01, 0x01}
	projectV4326AnimationValuePrefix  = []byte{0x05, 0x01, 0x09, 0x00, 0x03}
	projectV4326AnimationValueTail    = []byte{0x0a, 0x1e, 0x01}
	projectV4326SlotPrefix            = []byte{0x0d, 0x01, 0x0f, 0x01, 0x01}
	projectV4326SlotValuePrefix       = []byte{0x05, 0x1e, 0x01}
)

// discoverProjectRuntimeModelV4326 owns the 4.3.26 private layout. The shared
// legacy object-graph decoder remains an implementation detail of this adapter.
func discoverProjectRuntimeModelV4326(payload []byte, sourceVersion string) (*ProjectRuntimeModel, error) {
	model, err := discoverLegacyProjectRuntimeModel(
		payload,
		sourceVersion,
		"spine-4.3-legacy-project-v5",
	)
	if err != nil {
		return nil, err
	}
	mergeProjectSlotsV4326(model.Slots, model.Bones.Records, payload)
	animations, err := discoverProjectAnimationsV4326(payload)
	if err != nil {
		return nil, fmt.Errorf("Spine %s animation records were not found: %w", sourceVersion, err)
	}
	model.Layout = "spine-4.3.26-project"
	model.Animations = animations
	return model, nil
}

func mergeProjectSlotsV4326(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
	payload []byte,
) {
	if slots == nil {
		return
	}
	seen := make(map[string]struct{}, len(slots.Records))
	added := false
	for _, slot := range slots.Records {
		seen[slot.Name] = struct{}{}
	}
	for offset := 0; offset+len(projectV4326SlotPrefix) < len(payload); {
		relative := bytes.Index(payload[offset:], projectV4326SlotPrefix)
		if relative < 0 {
			break
		}
		objectOffset := offset + relative
		nameOffset := objectOffset + len(projectV4326SlotPrefix)
		name, nameEnd, ok := decodeProjectASCII(payload, nameOffset)
		if ok && bytes.HasPrefix(payload[nameEnd:], projectV4326SlotValuePrefix) {
			if _, exists := seen[name]; !exists {
				boneName := legacySlotBoneName(trimProjectSlotPrefix(name), bones)
				slots.Records = append(slots.Records, ProjectSlotRecord{
					WireReference: projectFirstWireReference + len(slots.Records),
					Name:          name,
					BoneName:      boneName,
					BoneReference: legacyBoneWireReference(boneName, bones),
					Color:         projectSlotV2DefaultColor,
					Blend:         "normal",
					Offset:        objectOffset,
				})
				seen[name] = struct{}{}
				added = true
			}
		}
		offset = nameOffset
	}
	slots.Format += "+spine-4.3.26-slots"
	slots.Count = len(slots.Records)
	if added {
		slots.ReferencesComplete = false
	}
}

func trimProjectSlotPrefix(name string) string {
	if len(name) > len("slot_") && name[:len("slot_")] == "slot_" {
		return name[len("slot_"):]
	}
	return name
}

func discoverProjectAnimationsV4326(payload []byte) (*ProjectAnimationDirectory, error) {
	candidates := make([]ProjectAnimationDirectory, 0, 1)
	for offset := 0; offset+len(projectV4326AnimationHeaderPrefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], projectV4326AnimationHeaderPrefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(projectV4326AnimationHeaderPrefix))
		if !ok || count < 1 || count > 10_000 ||
			!bytes.HasPrefix(payload[cursor:], projectV4326AnimationHeaderTail) {
			continue
		}
		firstRecord := cursor + len(projectV4326AnimationHeaderTail)
		records := scanProjectAnimationRecordsV4326(payload, firstRecord, count)
		if len(records) != count {
			continue
		}
		candidates = append(candidates, ProjectAnimationDirectory{
			Format:       "kryo-animation-map-v4326",
			HeaderOffset: offset,
			Count:        count,
			Records:      records,
		})
	}
	if len(candidates) == 0 {
		return nil, &ParseError{Code: ErrInvalidProject, Msg: "4.3.26 project animation map was not found"}
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d 4.3.26 animation map candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

func scanProjectAnimationRecordsV4326(
	payload []byte,
	firstOffset int,
	count int,
) []ProjectAnimationRecord {
	records := make([]ProjectAnimationRecord, 0, count)
	for offset := firstOffset; offset+len(projectV4326AnimationKeyPrefix) < len(payload) && len(records) < count; offset++ {
		if !bytes.HasPrefix(payload[offset:], projectV4326AnimationKeyPrefix) {
			continue
		}
		nameOffset := offset + len(projectV4326AnimationKeyPrefix)
		name, end, ok := decodeProjectASCII(payload, nameOffset)
		valueTail := end + len(projectV4326AnimationValuePrefix) + 1
		if !ok || valueTail+len(projectV4326AnimationValueTail) > len(payload) ||
			!bytes.Equal(payload[end:end+len(projectV4326AnimationValuePrefix)], projectV4326AnimationValuePrefix) ||
			(payload[end+len(projectV4326AnimationValuePrefix)] != 0x00 &&
				payload[end+len(projectV4326AnimationValuePrefix)] != 0x01) ||
			!bytes.Equal(payload[valueTail:valueTail+len(projectV4326AnimationValueTail)], projectV4326AnimationValueTail) {
			continue
		}
		records = append(records, ProjectAnimationRecord{Name: name, Offset: nameOffset})
		offset = valueTail + len(projectV4326AnimationValueTail) - 1
	}
	if len(records) != count {
		return nil
	}
	for index := range records {
		if index+1 < len(records) {
			records[index].EndOffset = records[index+1].Offset
		} else {
			records[index].EndOffset = len(payload)
		}
	}
	return records
}
