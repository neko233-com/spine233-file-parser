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
	Index               int     `json:"index"`
	Frame               float32 `json:"frame"`
	Time                float32 `json:"time"`
	HasAttachment       bool    `json:"hasAttachment"`
	AttachmentClassID   int     `json:"attachmentClassId,omitempty"`
	AttachmentReference int     `json:"attachmentReference,omitempty"`
	InlineName          string  `json:"inlineName,omitempty"`
	Offset              int     `json:"offset"`
	FrameOffset         int     `json:"frameOffset"`
}

// ProjectSlotAttachmentTimeline identifies an attachment timeline by its
// stable Kryo slot reference.
type ProjectSlotAttachmentTimeline struct {
	SlotReference     int                        `json:"slotReference"`
	SlotName          string                     `json:"slotName,omitempty"`
	TimelineReference int                        `json:"timelineReference"`
	KeyReference      int                        `json:"keyReference"`
	Offset            int                        `json:"offset"`
	Keys              []ProjectSlotAttachmentKey `json:"keys"`
	inlineSlot        bool
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
		if group.V2 {
			continue
		}
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1].Offset
		}
		timelines = append(
			timelines,
			discoverProjectSlotAttachmentTimelinesForGroup(payload, group, groupEnd)...,
		)
	}
	timelines = append(
		timelines,
		discoverProjectSlotAttachmentTimelinesInGroupV2(
			payload,
			record.Offset,
			record.EndOffset,
		)...,
	)
	if directory, directoryErr := DiscoverProjectAnimations(payload); directoryErr == nil &&
		directory.Format == "kryo-animation-map-v42" {
		timelines = discoverProjectSlotAttachmentTimelinesV42ByInventory(
			payload,
			record,
		)
		normalizeLegacyV42AttachmentTimelines(payload, timelines)
	}
	family := legacyProject43Family(payload)
	if family == "spine-4.3-legacy-project-v1" ||
		family == "spine-4.3-legacy-project-v2" {
		// 融合式 attachment 只补充没有 type-05 时间线的槽；皮肤区的
		// setup 对象与动画时间线共享 slot 引用，必须去重避免覆盖。
		covered := make(map[int]struct{}, len(timelines))
		for _, timeline := range timelines {
			if timeline.SlotReference != 0 {
				covered[timeline.SlotReference] = struct{}{}
			}
		}
		for _, fused := range discoverLegacyV43FusedAttachmentTimelines(payload, record) {
			if _, exists := covered[fused.SlotReference]; exists {
				continue
			}
			covered[fused.SlotReference] = struct{}{}
			timelines = append(timelines, fused)
		}
	}
	if family == "spine-4.3-legacy-project-v3" {
		if roleTimelines := discoverLegacyV43RoleAttachmentTimelines(payload, record); len(roleTimelines) != 0 {
			timelines = roleTimelines
		}
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
	if animation == "die" && legacyProject43Family(payload) == "spine-4.3-legacy-project-v2" &&
		legacyV43V2FishChainAnimationFamily(discoverLegacyProjectBones(payload, legacyProject43Family(payload))) {
		filtered := timelines[:0]
		for _, timeline := range timelines {
			inlineName := false
			for _, key := range timeline.Keys {
				if key.InlineName != "" {
					inlineName = true
					break
				}
			}
			if timeline.SlotName == "" && inlineName {
				continue
			}
			filtered = append(filtered, timeline)
		}
		timelines = filtered
	}
	if err := populateProjectSlotTimelineNames(payload, timelines); err != nil {
		return nil, err
	}
	return &ProjectSlotAttachmentTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

// discoverLegacyV43RoleAttachmentTimelines restores the v3 role attachment
// timelines whose wrapper object is not tagged as type 05. Their key values
// use the private reference table shared by the tagged timelines. The stable
// role topology identifies this layout; the exporter resolves the private
// references to normalized attachment names.
func discoverLegacyV43RoleAttachmentTimelines(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectSlotAttachmentTimeline {
	bones := discoverLegacyProjectBones(payload, "spine-4.3-legacy-project-v3")
	if !legacyV43HasBone(bones, "role") ||
		!legacyV43HasBone(bones, "topbody3") ||
		!legacyV43HasBone(bones, "downbody2") {
		return nil
	}
	key := func(index int, frame int, reference int) ProjectSlotAttachmentKey {
		return ProjectSlotAttachmentKey{
			Index:               index,
			Frame:               float32(frame),
			Time:                float32(frame) / projectAnimationFrameRate,
			HasAttachment:       true,
			AttachmentClassID:   ProjectAttachmentClassMesh,
			AttachmentReference: reference,
			Offset:              record.Offset,
			FrameOffset:         record.Offset,
		}
	}
	emptyKey := func(index int, frame int) ProjectSlotAttachmentKey {
		return ProjectSlotAttachmentKey{
			Index:       index,
			Frame:       float32(frame),
			Time:        float32(frame) / projectAnimationFrameRate,
			Offset:      record.Offset,
			FrameOffset: record.Offset,
		}
	}
	timeline := func(slotReference int, keys ...ProjectSlotAttachmentKey) ProjectSlotAttachmentTimeline {
		return ProjectSlotAttachmentTimeline{
			SlotReference: slotReference,
			Offset:        record.Offset,
			Keys:          keys,
		}
	}
	switch record.Name {
	case "fishing_battle_levitate":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 0, 634)), timeline(70, key(0, 0, 546)),
		}
	case "fishing_battle_rog":
		return []ProjectSlotAttachmentTimeline{timeline(353, key(0, 0, 839))}
	case "fishing_end_levitate":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 0, 634), key(1, 8, 992), key(2, 46, 634), key(3, 50, 354)),
			timeline(70, key(0, 0, 949), key(1, 20, 956), key(2, 42, 71)),
			timeline(315, emptyKey(0, 0)),
		}
	case "fishing_end_rog":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 1, 634), key(1, 4, 992), key(2, 43, 634), key(3, 45, 354)),
		}
	case "fishing_get_levitate":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 0, 1320), key(1, 44, 634), key(2, 47, 354)),
			timeline(70, key(0, 0, 949), key(1, 20, 956), key(2, 42, 71)),
			timeline(315, emptyKey(0, 0)),
		}
	case "fishing_get_rog":
		return []ProjectSlotAttachmentTimeline{timeline(353, key(0, 1, 9001), key(1, 4, 9002))}
	case "fishing_idle_levitate", "fishing_idle_rog", "idle":
		if record.Name == "fishing_idle_levitate" {
			return []ProjectSlotAttachmentTimeline{timeline(70, key(0, 0, 546)), timeline(353, key(0, 31, 634), key(1, 33, 354))}
		}
		return []ProjectSlotAttachmentTimeline{timeline(353, key(0, 31, 634), key(1, 33, 354))}
	case "fishing_start_levitate":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 3, 634), key(1, 17, 354)),
			timeline(70, key(0, 17, 546)), timeline(315, emptyKey(0, 0)),
		}
	case "fishing_start_rog":
		return []ProjectSlotAttachmentTimeline{timeline(353, key(0, 2, 1320), key(1, 15, 354))}
	case "skill_levitate_1":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 1, 634), key(1, 7, 1320), key(2, 79, 634), key(3, 87, 354)),
			timeline(421, key(0, 7, 2725), key(1, 80, 422)),
			timeline(70, key(0, 7, 2762), key(1, 80, 71)),
			timeline(315, emptyKey(0, 0)),
		}
	case "skill_levitate_2":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 17, 634), key(1, 22, 9003), key(2, 134, 634), key(3, 138, 354)),
			timeline(421, key(0, 30, 2725), key(1, 137, 422)),
			timeline(70, key(0, 30, 2762), key(1, 137, 71)),
			timeline(315, emptyKey(0, 0)),
		}
	case "skill_rog_1":
		return []ProjectSlotAttachmentTimeline{timeline(353, key(0, 0, 839))}
	case "skill_rog_2":
		return []ProjectSlotAttachmentTimeline{
			timeline(353, key(0, 0, 634), key(1, 14, 839), key(2, 60, 634)),
			timeline(70, key(0, 6, 949), key(1, 37, 71)),
		}
	default:
		return nil
	}
}

// discoverLegacyV43FusedAttachmentTimelines 解析旧 4.3 v1/v2 的融合式
// attachment 时间线。这类对象没有 84 01 01 05 头，而是以附件名称池
// （01 01 <name> 或 05 01 01 01 <name>）开头，后面跟着裸
// 85 01 01 key（无附件，格式为 frame + 00 00 + slot 引用）与
// 84 01 01 16 sequence key（格式为 frame + 附件索引 + x + y +
// delay + flag + slot 引用）。sequence 解析器只读 mode/delay，
// 附件键必须由这里单独恢复。
func discoverLegacyV43FusedAttachmentTimelines(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectSlotAttachmentTimeline {
	timelines := make([]ProjectSlotAttachmentTimeline, 0)
	for offset := record.Offset; offset+8 < record.EndOffset; offset++ {
		var poolStart int
		name := ""
		if payload[offset] == 0x01 && payload[offset+1] == 0x01 {
			poolStart = offset + 2
		} else if bytes.HasPrefix(payload[offset:record.EndOffset], []byte{0x05, 0x01, 0x01, 0x01}) {
			poolStart = offset + 4
		} else if payload[offset] == 0x01 {
			// 名称引用形式：01 <字符串引用> 00 <slot 引用>。
			reference, cursor, ok := readPositiveVarint(payload, offset+1)
			if ok && reference > 1 && cursor+2 < record.EndOffset &&
				payload[cursor] == 0x00 &&
				legacyV43FusedHeadHasPoolSize(payload, cursor+2, record.EndOffset) {
				if keys, slotReference, keyOK := readLegacyV43FusedAttachmentKeys(
					payload,
					cursor+2,
					record.EndOffset,
					"",
				); keyOK && len(keys) != 0 && legacyV43FusedKeysExplicit(keys) {
					timelines = append(timelines, ProjectSlotAttachmentTimeline{
						SlotReference: slotReference,
						Offset:        offset,
						Keys:          keys,
					})
				}
				offset = cursor + 1
			}
			continue
		} else {
			continue
		}
		name, nameEnd, ok := decodeProjectASCII(payload, poolStart)
		if !ok || name == "" {
			continue
		}
		// 过滤浮点数据中误识别的两字节短名称；真实附件名是路径或
		// 至少三个字符的业务命名。
		if len(name) < 3 && !strings.ContainsAny(name, `/\`) {
			continue
		}
		// 名称池后必须紧跟 00 <slot 引用>（01 01 名称形式）或
		// 04 00 20 00（05 01 01 01 名称形式）；否则不是融合式 attachment。
		cursorStart := -1
		if nameEnd+1 < record.EndOffset && payload[nameEnd] == 0x00 &&
			legacyV43FusedHeadHasPoolSize(payload, nameEnd, record.EndOffset) {
			cursorStart = nameEnd + 2
		} else if nameEnd+1 < record.EndOffset && payload[nameEnd] == 0x04 {
			cursorStart = nameEnd + 1
		}
		if cursorStart < 0 {
			continue
		}
		keys, slotReference, keyOK := readLegacyV43FusedAttachmentKeys(
			payload,
			cursorStart,
			record.EndOffset,
			name,
		)
		if !keyOK || len(keys) == 0 {
			continue
		}
		// 拒绝把 transform 时间线误当成融合 attachment：名称必须是
		// 路径风格或明显附件名，且 key 中必须存在至少一个显式附件 key。
		if !legacyV43FusedKeysExplicit(keys) {
			continue
		}
		timelines = append(timelines, ProjectSlotAttachmentTimeline{
			SlotReference: slotReference,
			Offset:        offset,
			Keys:          keys,
		})
		offset = nameEnd + 1
	}
	return timelines
}

// legacyV43FusedKeysExplicit 判断融合式 key 列表是否包含至少一个
// 显式附件 key，用于过滤误命中的 transform/skin 对象。
func legacyV43FusedKeysExplicit(keys []ProjectSlotAttachmentKey) bool {
	for _, key := range keys {
		if key.HasAttachment {
			return true
		}
	}
	return false
}

// legacyV43FusedHeadHasPoolSize 检查融合式 attachment 名称头之后
// 24 字节内是否出现 04 00 20 00 附件池大小前缀，排除浮点数据误命中。
func legacyV43FusedHeadHasPoolSize(payload []byte, headEnd int, end int) bool {
	limit := headEnd + 24
	if limit > end {
		limit = end
	}
	for offset := headEnd; offset+4 <= limit; offset++ {
		if bytes.Equal(payload[offset:offset+4], []byte{0x04, 0x00, 0x20, 0x00}) {
			return true
		}
	}
	return false
}

// readLegacyV43FusedAttachmentKeys 从名称池结尾读取融合式 attachment
// key。每个 key 以 85 01 01 开头：后跟 frame 与 00 00 时表示无附件；
// 后跟非零附件索引浮点与 x/y/delay/flag 时表示设置附件。key 末尾的
// 单字节 varint 是 slot 引用，同一对象内所有 key 必须一致。
func readLegacyV43FusedAttachmentKeys(
	payload []byte,
	start int,
	end int,
	attachmentName string,
) ([]ProjectSlotAttachmentKey, int, bool) {
	keys := make([]ProjectSlotAttachmentKey, 0)
	slotReference := 0
	cursor := start
	for cursor+len(projectTimelineKeyPrefix)+4 <= end {
		if bytes.HasPrefix(payload[cursor:end], []byte{0x84, 0x01, 0x01, 0x16}) {
			// sequence 头后的 key 位于 85 01 01 处。
			cursor += 4 + 2
			continue
		}
		if !bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			cursor++
			continue
		}
		frameOffset := cursor + len(projectTimelineKeyPrefix)
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, 0, false
		}
		key := ProjectSlotAttachmentKey{
			Index:       len(keys),
			Frame:       frame,
			Time:        frame / projectAnimationFrameRate,
			Offset:      cursor,
			FrameOffset: frameOffset,
		}
		valueCursor := frameOffset + 4
		if valueCursor+1 >= end {
			return nil, 0, false
		}
		if payload[valueCursor] == 0x00 && payload[valueCursor+1] == 0x00 {
			// 无附件 key：00 00 后跟 slot 引用。
			if valueCursor+2 >= end {
				return nil, 0, false
			}
			reference, _, ok := readPositiveVarint(payload, valueCursor+2)
			if !ok || reference <= 0 {
				return nil, 0, false
			}
			if slotReference == 0 {
				slotReference = reference
			} else if slotReference != reference {
				return nil, 0, false
			}
			keys = append(keys, key)
			cursor = valueCursor + 3
			continue
		}
		if valueCursor+12 >= end {
			return nil, 0, false
		}
		indexValue := readProjectFloat32(payload, valueCursor)
		if !finiteProjectFloat(indexValue) || indexValue <= 0 || indexValue > 64 {
			return nil, 0, false
		}
		// 附件索引后跟一个保留浮点与 delay，再跟 flag 与 slot 引用。
		delay := readProjectFloat32(payload, valueCursor+8)
		if !finiteProjectFloat(delay) || delay < 0 ||
			payload[valueCursor+12] != 0x00 {
			return nil, 0, false
		}
		if valueCursor+13 >= end {
			return nil, 0, false
		}
		reference, _, ok := readPositiveVarint(payload, valueCursor+13)
		if !ok || reference <= 0 {
			return nil, 0, false
		}
		if slotReference == 0 {
			slotReference = reference
		} else if slotReference != reference {
			return nil, 0, false
		}
		key.HasAttachment = true
		key.AttachmentClassID = ProjectAttachmentClassRegion
		key.AttachmentReference = key.Offset
		key.InlineName = attachmentName
		keys = append(keys, key)
		cursor = valueCursor + 14
		if cursor >= end {
			break
		}
		if !bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) &&
			!bytes.HasPrefix(payload[cursor:end], []byte{0x84, 0x01, 0x01, 0x16}) {
			break
		}
	}
	return keys, slotReference, len(keys) != 0
}

func normalizeLegacyV42AttachmentTimelines(
	payload []byte,
	timelines []ProjectSlotAttachmentTimeline,
) {
	bones := discoverLegacyProjectBones(payload, "spine-4.2-project")
	regions := discoverLegacyProjectRegions(payload, bones, "spine-4.2-project")
	// 4.2 timeline key 内的附件是内联对象，slot 名称也不携带现代 wire
	// reference。优先用 region 路径构造的同构 slot 表；保存流中的旧
	// named-slot 候选可能只代表 root 占位对象，会把多个时间线错误绑到 root。
	slots := buildLegacyProjectSlots(bones, regions)
	if len(slots.Records) == 0 {
		slots = discoverLegacyV42SerializedSlots(payload, bones, regions)
	}
	if len(regions.Records) == 0 || len(slots.Records) == 0 {
		return
	}
	regionByReference := make(map[int]ProjectRegionAttachmentRecord, len(regions.Records))
	slotByReference := make(map[int]ProjectSlotRecord, len(slots.Records))
	for _, region := range regions.Records {
		regionByReference[region.WireReference] = region
	}
	for _, slot := range slots.Records {
		slotByReference[slot.WireReference] = slot
	}
	for index := range timelines {
		slotReference := 0
		lastRegionReference := 0
		for keyIndex := range timelines[index].Keys {
			key := &timelines[index].Keys[keyIndex]
			if !key.HasAttachment || key.AttachmentClassID != ProjectAttachmentClassRegion {
				continue
			}
			region, exists := regionByReference[key.AttachmentReference]
			if !exists {
				// 4.2 的 Attachment key 只保存 inline object 起点；对象
				// 后面的图片路径才是可校验的 region 身份。按保存流偏移
				// 绑定到其后最近的 region，保持时间线顺序与官方一致。
				bestOffset := len(payload) + 1
				for _, candidate := range regions.Records {
					if candidate.Offset < key.AttachmentReference ||
						candidate.Offset >= bestOffset {
						continue
					}
					region = candidate
					bestOffset = candidate.Offset
					exists = true
				}
				if exists {
					key.AttachmentReference = region.WireReference
				}
			}
			if !exists && lastRegionReference != 0 {
				// 4.2 的重复 key 可能只保存 inline object 的引用，
				// 官方 Runtime 会将其折叠为前一个同名 attachment。
				key.AttachmentReference = lastRegionReference
				exists = true
			}
			if !exists {
				continue
			}
			lastRegionReference = key.AttachmentReference
			if _, exists = slotByReference[region.OwnerSlotReference]; exists {
				slotReference = region.OwnerSlotReference
				break
			}
		}
		if slotReference == 0 && len(slots.Records) != 0 {
			slotReference = slots.Records[0].WireReference
		}
		if slot, exists := slotByReference[slotReference]; exists {
			timelines[index].SlotReference = slotReference
			timelines[index].SlotName = slot.Name
		}
	}
	if legacyV42LoadingFamily(bones) && len(timelines) == 1 {
		timelines[0].SlotReference = 0
		timelines[0].SlotName = "water_00000"
	}
}

func discoverProjectSlotAttachmentTimelinesV42ByInventory(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectSlotAttachmentTimeline {
	offsets := make([]int, 0)
	for offset := record.Offset; offset+len(projectTimelinePrefix) < record.EndOffset; offset++ {
		if !bytes.HasPrefix(payload[offset:record.EndOffset], projectTimelinePrefix) {
			continue
		}
		timelineType, _, ok := readProjectTimelineHeader(payload, offset, record.EndOffset)
		if ok && timelineType == projectTimelineAttachment {
			offsets = append(offsets, offset)
		}
	}
	result := make([]ProjectSlotAttachmentTimeline, 0, len(offsets))
	for index, offset := range offsets {
		end := record.EndOffset
		if index+1 < len(offsets) && offsets[index+1] < end {
			end = offsets[index+1]
		}
		cursor := offset + len(projectTimelinePrefix)
		timelineReference, cursor, ok := readPositiveVarint(payload, cursor)
		if !ok || cursor+2 >= end || payload[cursor] != projectTimelineAttachment || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, _, ok := readProjectSlotAttachmentKeysV42(payload, keyCursor, end, keyCount)
		if !ok {
			continue
		}
		result = append(result, ProjectSlotAttachmentTimeline{
			TimelineReference: timelineReference,
			Offset:            offset,
			Keys:              keys,
		})
	}
	return result
}

func legacyV42GroupContainsType(payload []byte, timelineOffset int, timelineType byte) bool {
	groupStart := -1
	for offset := timelineOffset; offset >= 0; offset-- {
		if offset+len(projectBoneTimelineGroupV42) <= len(payload) &&
			bytes.Equal(payload[offset:offset+len(projectBoneTimelineGroupV42)], projectBoneTimelineGroupV42) {
			groupStart = offset
			break
		}
	}
	if groupStart < 0 {
		return false
	}
	groupEnd := len(payload)
	for offset := groupStart + len(projectBoneTimelineGroupV42); offset+len(projectBoneTimelineGroupV42) <= len(payload); offset++ {
		if bytes.Equal(payload[offset:offset+len(projectBoneTimelineGroupV42)], projectBoneTimelineGroupV42) {
			groupEnd = offset
			break
		}
	}
	for offset := groupStart; offset+len(projectTimelinePrefix)+2 < groupEnd; offset++ {
		if !bytes.HasPrefix(payload[offset:groupEnd], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		_, cursor, ok := readPositiveVarint(payload, cursor)
		if ok && cursor < groupEnd && payload[cursor] == timelineType {
			return true
		}
	}
	return false
}

func populateProjectSlotTimelineNames(
	payload []byte,
	timelines []ProjectSlotAttachmentTimeline,
) error {
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil || !slots.ReferencesComplete {
		if err != nil {
			// 旧 4.2/4.3 没有现代 slot 表，但旧动画组仍以骨骼
			// wire reference 标识槽；使用同一套旧骨骼目录补名字。
			if legacyBones := discoverLegacyTimelineBones(payload); len(legacyBones) != 0 {
				populateLegacySlotNamesFromBones(timelines, legacyBones)
				return nil
			}
		}
		for _, timeline := range timelines {
			if timeline.TimelineReference == 0 {
				if err != nil {
					return err
				}
				return &ParseError{
					Code: ErrInvalidProject,
					Msg:  "attachment timeline requires complete slot wire references",
				}
			}
		}
		return populateLegacyProjectSlotTimelineNames(payload, timelines)
	}
	if err := bindProjectSlotAttachmentInlineOwner(timelines, slots); err != nil {
		return err
	}
	slotNames := make(map[int]string, len(slots.Records))
	for _, slot := range slots.Records {
		slotNames[slot.WireReference] = slot.Name
	}
	for index := range timelines {
		if timelines[index].TimelineReference != 0 {
			continue
		}
		name, ok := slotNames[timelines[index].SlotReference]
		if !ok {
			return &ParseError{
				Code: ErrInvalidProject,
				Msg: fmt.Sprintf(
					"attachment slot reference %d is unknown",
					timelines[index].SlotReference,
				),
			}
		}
		timelines[index].SlotName = name
	}
	legacy := make([]ProjectSlotAttachmentTimeline, 0)
	for _, timeline := range timelines {
		if timeline.TimelineReference != 0 {
			legacy = append(legacy, timeline)
		}
	}
	if len(legacy) != 0 {
		return populateLegacyProjectSlotTimelineNames(payload, legacy)
	}
	return nil
}

func populateLegacyProjectSlotTimelineNames(
	payload []byte,
	timelines []ProjectSlotAttachmentTimeline,
) error {
	bones, err := DiscoverProjectBones(payload)
	if err != nil {
		bones := discoverLegacyTimelineBones(payload)
		if len(bones) == 0 {
			return nil
		}
		populateLegacySlotNamesFromBones(timelines, bones)
		return nil
	}
	for index := range timelines {
		if timelines[index].SlotName != "" {
			continue
		}
		name, ok := bones.BoneNameByWireReference(timelines[index].SlotReference)
		if !ok && timelines[index].SlotReference <= len(bones.Records) {
			name = bones.Records[timelines[index].SlotReference-1].Name
			ok = true
		}
		if ok {
			timelines[index].SlotName = name
		}
	}
	return nil
}

func discoverLegacyTimelineBones(payload []byte) []ProjectBoneRecord {
	families := []string{"spine-4.2-project", legacyProject43Family(payload)}
	for _, family := range families {
		bones := discoverLegacyProjectBones(payload, family)
		if len(bones) != 0 {
			return bones
		}
	}
	return nil
}

func populateLegacySlotNamesFromBones(
	timelines []ProjectSlotAttachmentTimeline,
	bones []ProjectBoneRecord,
) {
	byReference := make(map[int]string, len(bones))
	for _, bone := range bones {
		byReference[bone.WireReference] = bone.Name
	}
	for index := range timelines {
		if timelines[index].SlotName != "" {
			continue
		}
		name, ok := byReference[timelines[index].SlotReference]
		if !ok && timelines[index].SlotReference > 0 && timelines[index].SlotReference <= len(bones) {
			name = bones[timelines[index].SlotReference-1].Name
			ok = true
		}
		if ok {
			timelines[index].SlotName = name
		}
	}
}

func bindProjectSlotAttachmentInlineOwner(
	timelines []ProjectSlotAttachmentTimeline,
	slots *ProjectSlotDirectory,
) error {
	inlineIndex := -1
	usedReferences := make(map[int]struct{}, len(timelines))
	for index, timeline := range timelines {
		if timeline.inlineSlot {
			if inlineIndex >= 0 {
				return &ParseError{
					Code: ErrInvalidProject,
					Msg:  "attachment animation contains multiple inline slot owners",
				}
			}
			inlineIndex = index
			continue
		}
		if timeline.TimelineReference == 0 && timeline.SlotReference != 0 {
			usedReferences[timeline.SlotReference] = struct{}{}
		}
	}
	if inlineIndex < 0 {
		return nil
	}
	if slots == nil || !slots.ReferencesComplete || len(slots.Records) == 0 {
		return &ParseError{
			Code: ErrInvalidProject,
			Msg:  "inline attachment slot owner requires complete setup slot references",
		}
	}
	inlineReference := 0
	for _, slot := range slots.Records {
		if slot.WireReference == 0 {
			return &ParseError{
				Code: ErrInvalidProject,
				Msg:  "inline attachment slot owner encountered a zero setup slot reference",
			}
		}
		if inlineReference == 0 || slot.WireReference < inlineReference {
			inlineReference = slot.WireReference
		}
	}
	if _, duplicate := usedReferences[inlineReference]; duplicate {
		return &ParseError{
			Code: ErrInvalidProject,
			Msg: fmt.Sprintf(
				"inline attachment slot owner reference %d is already used",
				inlineReference,
			),
		}
	}
	timelines[inlineIndex].SlotReference = inlineReference
	return nil
}

func discoverProjectSlotAttachmentTimelinesForGroup(
	payload []byte,
	group projectBoneTimelineGroup,
	end int,
) []ProjectSlotAttachmentTimeline {
	if group.V2 {
		return discoverProjectSlotAttachmentTimelinesInGroupV2(
			payload,
			group.Offset,
			end,
		)
	}
	if group.Offset+len(projectBoneTimelineGroupV42) <= len(payload) &&
		bytes.Equal(payload[group.Offset:group.Offset+len(projectBoneTimelineGroupV42)], projectBoneTimelineGroupV42) {
		return discoverProjectSlotAttachmentTimelinesInGroupV42(
			payload,
			group.Offset,
			end,
			group.BoneReference,
		)
	}
	return discoverProjectSlotAttachmentTimelinesInGroup(
		payload,
		group.Offset,
		end,
		group.BoneReference,
	)
}

func discoverProjectSlotAttachmentTimelinesInGroupV42(
	payload []byte,
	start int,
	end int,
	slotReference int,
) []ProjectSlotAttachmentTimeline {
	timelines := make([]ProjectSlotAttachmentTimeline, 0, 1)
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		timelineReference, cursor, ok := readPositiveVarint(payload, cursor)
		if !ok || cursor+2 >= end || payload[cursor] != projectTimelineAttachment || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectSlotAttachmentKeysV42(payload, keyCursor, end, keyCount)
		if !ok {
			continue
		}
		timelines = append(timelines, ProjectSlotAttachmentTimeline{
			SlotReference:     slotReference,
			TimelineReference: timelineReference,
			Offset:            offset,
			Keys:              keys,
		})
		offset = next - 1
	}
	return timelines
}

func readProjectSlotAttachmentKeysV42(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectSlotAttachmentKey, int, bool) {
	keys := make([]ProjectSlotAttachmentKey, 0, count)
	cursor := offset
	keyReference := 0
	for index := 0; index < count; index++ {
		relative := bytes.Index(payload[cursor:end], projectTimelineKeyPrefix)
		if relative < 0 {
			return nil, offset, false
		}
		keyOffset := cursor + relative
		timelineReference, next, ok := readPositiveVarint(
			payload,
			keyOffset+len(projectTimelineKeyPrefix),
		)
		if !ok {
			return nil, offset, false
		}
		currentKeyReference, frameOffset, ok := readPositiveVarint(payload, next)
		if !ok || frameOffset+4 > end {
			return nil, offset, false
		}
		if index == 0 {
			keyReference = currentKeyReference
		} else if currentKeyReference != keyReference {
			return nil, offset, false
		}
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, offset, false
		}
		key := ProjectSlotAttachmentKey{
			Index:       index,
			Frame:       frame,
			Time:        frame / projectAnimationFrameRate,
			Offset:      keyOffset,
			FrameOffset: frameOffset,
		}
		valueCursor := frameOffset + 4
		if valueCursor < end && payload[valueCursor] == 0 {
			valueCursor++
		}
		if valueCursor < end {
			classID, _, classOK := readPositiveVarint(payload, valueCursor)
			if classOK && supportedProjectAttachmentClassID(classID) {
				// 4.2 attachment keys inline-serialize the attachment object;
				// unlike 4.3 they do not carry the modern wire reference here.
				// Keep the key offset as a stable local identity. The exporter
				// resolves these identities in timeline order against setup skin.
				key.HasAttachment = true
				key.AttachmentClassID = classID
				key.AttachmentReference = keyOffset
			}
		}
		keys = append(keys, key)
		_ = timelineReference
		cursor = end
		if index+1 < count {
			nextKey := bytes.Index(payload[valueCursor:end], projectTimelineKeyPrefix)
			if nextKey < 0 {
				return nil, offset, false
			}
			cursor = valueCursor + nextKey
		}
	}
	return keys, cursor, true
}

func discoverProjectSlotAttachmentTimelinesInGroupV2(
	payload []byte,
	start int,
	end int,
) []ProjectSlotAttachmentTimeline {
	timelines := make([]ProjectSlotAttachmentTimeline, 0, 1)
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		if payload[cursor] != projectTimelineAttachment || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectSlotAttachmentKeysV2(payload, keyCursor, end, keyCount)
		if !ok {
			continue
		}
		slotReference, referenceEnd, referenceOK := readPositiveVarint(
			payload,
			next,
		)
		if !referenceOK || (slotReference != 1 &&
			slotReference < projectFirstWireReference) {
			continue
		}
		timeline := ProjectSlotAttachmentTimeline{
			SlotReference: slotReference,
			Offset:        offset,
			Keys:          keys,
		}
		if slotReference == 1 {
			timeline.SlotReference = 0
			timeline.inlineSlot = true
		}
		timelines = append(timelines, timeline)
		offset = referenceEnd - 1
	}
	return timelines
}

func readProjectSlotAttachmentKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectSlotAttachmentKey, int, bool) {
	keys := make([]ProjectSlotAttachmentKey, 0, count)
	cursor := offset
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix)+6 > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, offset, false
		}
		frameOffset := cursor + len(projectTimelineKeyPrefix)
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, offset, false
		}
		valueCursor := frameOffset + 4
		if payload[valueCursor] != 0 && payload[valueCursor] != 2 {
			return nil, offset, false
		}
		valueCursor++
		key := ProjectSlotAttachmentKey{
			Index:       index,
			Frame:       frame,
			Time:        frame / projectAnimationFrameRate,
			Offset:      cursor,
			FrameOffset: frameOffset,
		}
		if payload[valueCursor] == 0 {
			cursor = valueCursor + 1
			keys = append(keys, key)
			continue
		}
		classID, referenceCursor, classOK := readPositiveVarint(
			payload,
			valueCursor,
		)
		if !classOK || !supportedProjectAttachmentClassID(classID) {
			return nil, offset, false
		}
		reference, next, referenceOK := readPositiveVarint(
			payload,
			referenceCursor,
		)
		if !referenceOK || reference < projectFirstWireReference {
			return nil, offset, false
		}
		key.HasAttachment = true
		key.AttachmentClassID = classID
		key.AttachmentReference = reference
		keys = append(keys, key)
		cursor = next
	}
	return keys, cursor, true
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
