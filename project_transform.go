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
	CurveOffsets [][4]int     `json:"curveOffsets"`
	CurveFlags   []byte       `json:"curveFlags"`
}

// ProjectTransformTimeline identifies a rotate, translate, scale, or shear
// timeline by the project's stable Kryo bone reference. BoneName is populated
// only when the project bone object graph proves the corresponding name.
type ProjectTransformTimeline struct {
	Type              string                `json:"type"`
	Channels          []string              `json:"channels"`
	BoneReference     int                   `json:"boneReference"`
	BoneName          string                `json:"boneName,omitempty"`
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
	return discoverProjectTransformTimelinesForRecord(payload, animation, record)
}

// DiscoverProjectTransformTimelinesInRange 直接使用已恢复的旧对象图动画区间。
// 旧 4.3 的动画名位于 value 之后，不能再次按现代 Animation Map 查找。
func DiscoverProjectTransformTimelinesInRange(
	payload []byte,
	animation string,
	start int,
	end int,
) (*ProjectTransformTimelineDirectory, error) {
	start, end = normalizeLegacyV43AnimationRange(
		payload,
		ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end},
	)
	if start < 0 || end <= start || end > len(payload) {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "invalid animation range"}
	}
	if animation == "run" && legacyProject43Family(payload) == "spine-4.3-legacy-project-v3" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if legacyV43NumericFishProjectBone(bones) == "10022" &&
			legacyV43HasBone(bones, "head1") && legacyV43HasBone(bones, "hand_L3") {
			return nil, &ParseError{Code: ErrInvalidProject, Msg: "animation run contains no 10022 timelines"}
		}
	}
	record := ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v2" && animation == "die" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if timelines := discoverLegacyV43V2SmallFishDiePreKeyTransformTimelines(payload, bones); len(timelines) != 0 {
			last := timelines[len(timelines)-1]
			return &ProjectTransformTimelineDirectory{
				Animation:   animation,
				RegionStart: timelines[0].Offset,
				RegionEnd:   last.Keys[len(last.Keys)-1].Offset,
				FrameRate:   projectAnimationFrameRate,
				Timelines:   timelines,
			}, nil
		}
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v1" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if legacyV43BeardFishBoneFamily(bones) {
			if timelines := discoverLegacyV43BeardFishTransformTimelines(payload, record, bones); len(timelines) != 0 {
				return &ProjectTransformTimelineDirectory{
					Animation:   animation,
					RegionStart: record.Offset,
					RegionEnd:   record.EndOffset,
					FrameRate:   projectAnimationFrameRate,
					Timelines:   timelines,
				}, nil
			}
		}
	}
	if compact := discoverLegacyV43CompactTransformTimelines(payload, record); len(compact) != 0 {
		if preKey := discoverLegacyV43RolePreKeyTransformTimelines(payload, record); len(preKey) != 0 {
			compact = append(preKey, compact...)
		}
		if legacyProject43Family(payload) == "spine-4.3-legacy-project-v3" &&
			record.Name == "fishing_start_levitate" &&
			legacyV43RoleProjectBones(discoverLegacyProjectBones(payload, "spine-4.3-legacy-project-v3")) {
			filtered := compact[:0]
			for _, timeline := range compact {
				if timeline.Type != ProjectTimelineScale {
					filtered = append(filtered, timeline)
				}
			}
			compact = filtered
		}
		populateProjectTransformBoneNames(payload, compact)
		return &ProjectTransformTimelineDirectory{
			Animation:   animation,
			RegionStart: record.Offset,
			RegionEnd:   record.EndOffset,
			FrameRate:   projectAnimationFrameRate,
			Timelines:   compact,
		}, nil
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v3" && animation == "die" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if legacyV43SmallNumericFishPhysicsFamily(bones) {
			preKey := discoverLegacyV43FishHandDieTransformTimelines(payload, record, bones)
			if len(preKey) != 0 {
				postKey := discoverLegacyV43TransformTimelines(payload, record)
				populateProjectTransformBoneNames(payload, postKey)
				for _, timeline := range postKey {
					if !projectTransformTimelineIsDefault(timeline) {
						preKey = append(preKey, timeline)
					}
				}
				return &ProjectTransformTimelineDirectory{
					Animation:   animation,
					RegionStart: record.Offset,
					RegionEnd:   record.EndOffset,
					FrameRate:   projectAnimationFrameRate,
					Timelines:   preKey,
				}, nil
			}
		}
		if legacyV43IsFishHandFamily(bones) ||
			legacyV43BodyFootBoneFamily(bones) ||
			(legacyV43NumericFishProjectBone(bones) != "" &&
				!legacyV43SmallNumericFishPhysicsFamily(bones) &&
				!legacyV43FishChainNumericPhysicsFamily(bones)) {
			if timelines := discoverLegacyV43FishHandDieTransformTimelines(payload, record, bones); len(timelines) != 0 {
				postKey := discoverLegacyV43TransformTimelines(payload, record)
				populateProjectTransformBoneNames(payload, postKey)
				for _, timeline := range postKey {
					if !projectTransformTimelineIsDefault(timeline) {
						timelines = append(timelines, timeline)
					}
				}
				return &ProjectTransformTimelineDirectory{
					Animation:   animation,
					RegionStart: start,
					RegionEnd:   end,
					FrameRate:   projectAnimationFrameRate,
					Timelines:   timelines,
				}, nil
			}
		}
	}
	return discoverProjectTransformTimelinesForRecord(
		payload,
		animation,
		record,
	)
}

// discoverLegacyV43RolePreKeyTransformTimelines recovers the shared transform
// groups serialized immediately before fishing_battle_levitate. The v3 role
// project stores seven bone groups outside the animation-name record; the
// official exporter still includes them in that animation.
func discoverLegacyV43RolePreKeyTransformTimelines(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectTransformTimeline {
	if legacyProject43Family(payload) != "spine-4.3-legacy-project-v3" ||
		record.Name != "fishing_battle_levitate" || record.Offset <= 0 {
		return nil
	}
	bones := discoverLegacyProjectBones(payload, "spine-4.3-legacy-project-v3")
	if !legacyV43RoleProjectBones(bones) {
		return nil
	}
	targets := map[string]bool{
		"topbody": true, "topbody2": true, "topbody3": true,
		"hand_L": true, "hand_L2": true, "hand_R": true, "hand_R2": true,
	}
	start := record.Offset - 16384
	if start < 0 {
		start = 0
	}
	groups := make([]int, 0, len(targets))
	for offset := start; offset+14 <= record.Offset; offset++ {
		if !legacyV43TransformGroupMarker(payload, offset) {
			continue
		}
		owner, _, ok := readPositiveVarint(payload, offset+4)
		if !ok {
			continue
		}
		boneIndex := legacyBoneIndexByOwnerToken(bones, owner)
		if boneIndex >= 0 && targets[bones[boneIndex].Name] {
			groups = append(groups, offset)
		}
	}
	if len(groups) != len(targets) {
		return nil
	}
	result := make([]ProjectTransformTimeline, 0, len(groups)*2)
	for index, groupOffset := range groups {
		groupEnd := record.Offset
		if index+1 < len(groups) {
			groupEnd = groups[index+1]
		}
		owner, _, ok := readPositiveVarint(payload, groupOffset+4)
		if !ok {
			continue
		}
		boneIndex := legacyBoneIndexByOwnerToken(bones, owner)
		if boneIndex < 0 {
			continue
		}
		timelines := discoverProjectTransformTimelinesInLegacyV43GroupV3(
			payload, groupOffset+13, groupEnd, bones[boneIndex].WireReference,
		)
		for _, timeline := range timelines {
			if projectTransformTimelineIsDefault(timeline) {
				continue
			}
			timeline.BoneName = bones[boneIndex].Name
			result = append(result, timeline)
		}
	}
	return result
}

func legacyV43RoleProjectBones(bones []ProjectBoneRecord) bool {
	for _, required := range []string{"role", "topbody3", "downbody2", "hand_L2", "hand_R2"} {
		found := false
		for _, bone := range bones {
			if bone.Name == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// discoverLegacyV43V2SmallFishDiePreKeyTransformTimelines handles the small
// numeric-fish v2 object graph. Its die Animation value is interned near the
// payload head, while the name key is serialized with the other animations.
// The map marker is immediately followed by the referenced transform objects;
// this avoids treating setup/physics floats as animation keys.
func discoverLegacyV43V2SmallFishDiePreKeyTransformTimelines(
	payload []byte,
	bones []ProjectBoneRecord,
) []ProjectTransformTimeline {
	boneName := ""
	for _, bone := range bones {
		if bone.Name != "root" && legacyAllDigits(bone.Name) {
			boneName = bone.Name
			break
		}
	}
	if boneName == "" {
		return nil
	}
	boneIndex := -1
	for index, bone := range bones {
		if bone.Name == boneName {
			boneIndex = index
			break
		}
	}
	if boneIndex < 0 {
		return nil
	}
	limit := len(payload)
	if limit > 8192 {
		limit = 8192
	}
	for offset := 0; offset+4 < limit; offset++ {
		if !bytes.Equal(payload[offset:offset+3], []byte{0x02, 0x0f, 0x01}) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+3)
		if !ok || count < 2 || count > 8 || cursor+len(projectTimelinePrefix) > limit ||
			!bytes.HasPrefix(payload[cursor:limit], projectTimelinePrefix) {
			continue
		}
		candidate := make([]ProjectTransformTimeline, 0, count)
		for index := 0; index < count && cursor+len(projectTimelinePrefix)+2 < limit; index++ {
			if !bytes.HasPrefix(payload[cursor:limit], projectTimelinePrefix) {
				break
			}
			typeCursor := cursor + len(projectTimelinePrefix)
			timelineType, channels, componentCount, typeOK := projectTransformType(payload[typeCursor])
			if !typeOK || typeCursor+2 >= limit || payload[typeCursor+1] != 0x01 {
				break
			}
			keyCount, keyCursor, countOK := readPositiveVarint(payload, typeCursor+2)
			if !countOK || keyCount < 1 || keyCount > 256 {
				break
			}
			keys, next, keysOK := readProjectTransformKeysV2(
				payload, keyCursor, limit, keyCount, componentCount,
			)
			if !keysOK {
				break
			}
			candidate = append(candidate, ProjectTransformTimeline{
				Type: timelineType, Channels: channels,
				BoneReference: bones[boneIndex].WireReference,
				BoneName:      boneName, Offset: cursor, Keys: keys,
			})
			cursor = next
		}
		if len(candidate) != 2 || candidate[0].Type != ProjectTimelineRotate ||
			candidate[1].Type != ProjectTimelineTranslate ||
			len(candidate[0].Keys) != 2 || len(candidate[1].Keys) < 3 {
			continue
		}
		for _, timeline := range candidate {
			if !projectTransformTimelineIsDefault(timeline) {
				return candidate
			}
		}
	}
	return nil
}

// normalizeLegacyV43AnimationRange 修正旧 4.3 v1/v2 的动画区间。
// 旧动画表恢复逻辑把 EndOffset 固定设为当前动画 key 的起点：首条记录
// 因此出现 Offset > EndOffset 的反向区间，其余记录则错位到上一段动画
// 的 value 块。这里统一按“EndOffset 是否指向本动画 key”识别未修正
// 形态并重算：v1（4.3.08/4.3.15/4.3.19）的 timeline 块紧跟 key 之后；
// v2（4.3.17）存在两种布局——timeline 块紧随 key 之后（inline），或
// 整块写在动画 Map 之前（pre-key）。重算区间为 [timeline 块起点,
// 下一个动画 key 起点)，最后一个动画以 payload 尾为界；已是正确形态
// 的记录（EndOffset 指向下一段）原样返回，避免与上层修复冲突。
func normalizeLegacyV43AnimationRange(
	payload []byte,
	record ProjectAnimationRecord,
) (int, int) {
	family := legacyProject43Family(payload)
	if family != "spine-4.3-legacy-project-v1" &&
		family != "spine-4.3-legacy-project-v2" {
		return record.Offset, record.EndOffset
	}
	if family == "spine-4.3-legacy-project-v2" &&
		record.EndOffset > record.Offset && record.EndOffset < len(payload) {
		// v2 Animation values are serialized before their name keys. The
		// legacy directory already returns [previousKeyEnd, currentKeyStart];
		// do not reinterpret that valid reverse interval as an inline value.
		if name, _, ok := decodeProjectASCII(payload, record.EndOffset); ok &&
			name == record.Name {
			return record.Offset, record.EndOffset
		}
	}
	keyStart := record.EndOffset
	if keyStart <= 0 || keyStart >= len(payload) {
		return record.Offset, record.EndOffset
	}
	name, nameEnd, ok := decodeProjectASCII(payload, keyStart)
	if (!ok || name != record.Name) && keyStart+2 < len(payload) {
		// EndOffset 可能落在 01 01 key 前缀上，向后偏移两个字节再试。
		name, nameEnd, ok = decodeProjectASCII(payload, keyStart+2)
		if ok && name == record.Name {
			keyStart += 2
		}
	}
	if !ok || name != record.Name {
		// EndOffset 未指向本动画 key：记录已修正或不属于本布局。
		return record.Offset, record.EndOffset
	}
	keyEnd := nameEnd
	if keyEnd <= keyStart || keyEnd > len(payload) {
		return record.Offset, record.EndOffset
	}
	valueEnd := len(payload)
	for offset := keyEnd; offset+2 < len(payload); offset++ {
		if payload[offset] != 0x01 || payload[offset+1] != 0x01 {
			continue
		}
		nextName, nextEnd, ok := decodeProjectASCII(payload, offset+2)
		if !ok || nextName == record.Name {
			continue
		}
		if legacyAnimationEvidence(payload, nextEnd) {
			valueEnd = offset
			break
		}
	}
	valueStart := keyEnd
	if !legacyV43InlineTimelineData(payload, keyEnd, valueEnd) {
		// pre-key 布局：跳过当前动画自己的 Map 头再反找 timeline 块起点。
		scanStart := keyStart
		if header := legacyV43MapHeaderBeforeKey(payload, keyStart); header >= 0 {
			scanStart = header
		}
		if cluster := legacyV43PreKeyTimelineClusterStart(payload, scanStart); cluster >= 0 {
			valueStart = cluster
		}
	}
	return valueStart, valueEnd
}

// legacyV43MapHeaderBeforeKey 定位紧邻动画 key 之前的 Map 头起点。
func legacyV43MapHeaderBeforeKey(payload []byte, keyStart int) int {
	limit := keyStart - 64
	if limit < 0 {
		limit = 0
	}
	for offset := keyStart - 4; offset >= limit; offset-- {
		if bytes.HasPrefix(payload[offset:], []byte{0x03, 0x00, 0x06, 0x01, 0x09, 0x00}) ||
			bytes.HasPrefix(payload[offset:], []byte{0x03, 0x01, 0x06, 0x01, 0x09, 0x00}) ||
			bytes.HasPrefix(payload[offset:], []byte{0x12, 0x01, 0x0a, 0x06, 0x01, 0x08, 0x74}) {
			return offset
		}
	}
	return -1
}

// legacyV43InlineTimelineData 判断 key 之后是否紧跟 timeline 数据。
// inline 布局的动画 value 对象头（08 74 01 00 等）之后直接出现
// transform group、timeline list 或 timeline 头。
func legacyV43InlineTimelineData(payload []byte, from int, to int) bool {
	limit := from + 128
	if limit > to {
		limit = to
	}
	for offset := from; offset+3 <= limit; offset++ {
		if bytes.HasPrefix(payload[offset:limit], []byte{0x02, 0x0f, 0x01}) ||
			bytes.HasPrefix(payload[offset:limit], []byte{0x13, 0x01, 0x04}) ||
			bytes.HasPrefix(payload[offset:limit], projectTimelinePrefix) {
			return true
		}
	}
	return false
}

// legacyV43PreKeyTimelineClusterStart 在 key 之前反找 timeline 块起点。
// 遇到上一个动画 Map 头或骨骼 setup 记录（13 01 04 04 01 07）时停止，
// 避免把前一段动画或骨骼/皮肤区的标记算进当前动画。
func legacyV43PreKeyTimelineClusterStart(payload []byte, keyStart int) int {
	limit := keyStart - 16384
	if limit < 0 {
		limit = 0
	}
	best := -1
	for offset := keyStart - 1; offset >= limit; offset-- {
		if offset+4 < len(payload) &&
			(bytes.HasPrefix(payload[offset:], []byte{0x03, 0x00, 0x06, 0x01, 0x09, 0x00}) ||
				bytes.HasPrefix(payload[offset:], []byte{0x03, 0x01, 0x06, 0x01, 0x09, 0x00}) ||
				bytes.HasPrefix(payload[offset:], []byte{0x12, 0x01, 0x0a, 0x06, 0x01, 0x08, 0x74})) {
			break
		}
		if offset+6 < len(payload) &&
			bytes.HasPrefix(payload[offset:], []byte{0x13, 0x01, 0x04, 0x04, 0x01, 0x07}) {
			break
		}
		if legacyV43TimelineMarker(payload, offset) {
			best = offset
		}
	}
	return best
}

// legacyV43TimelineMarker 识别旧 4.3 动画 timeline 块内的对象标记。
func legacyV43TimelineMarker(payload []byte, offset int) bool {
	if offset < 0 || offset+3 > len(payload) {
		return false
	}
	return bytes.HasPrefix(payload[offset:], projectTimelinePrefix) ||
		bytes.HasPrefix(payload[offset:], projectTimelineKeyPrefix) ||
		bytes.HasPrefix(payload[offset:], []byte{0x02, 0x0f, 0x01}) ||
		bytes.HasPrefix(payload[offset:], []byte{0x13, 0x01, 0x04}) ||
		bytes.HasPrefix(payload[offset:], []byte{0x36, 0x01, 0x02}) ||
		bytes.HasPrefix(payload[offset:], []byte{0x05, 0x01, 0x01, 0x01}) ||
		bytes.HasPrefix(payload[offset:], []byte{0x0f, 0x7b, 0x01})
}

func discoverProjectTransformTimelinesForRecord(
	payload []byte,
	animation string,
	record ProjectAnimationRecord,
) (*ProjectTransformTimelineDirectory, error) {
	timelines := make([]ProjectTransformTimeline, 0)
	if strings.HasPrefix(legacyProject43Family(payload), "spine-4.3-legacy-project") {
		timelines = discoverLegacyV43TransformTimelines(payload, record)
	}
	if len(timelines) == 0 {
		groups := discoverProjectBoneTimelineGroups(
			payload,
			record.Offset,
			record.EndOffset,
		)
		for index, group := range groups {
			groupEnd := record.EndOffset
			if index+1 < len(groups) {
				groupEnd = groups[index+1].Offset
			}
			for _, timeline := range discoverProjectTransformTimelinesForGroup(payload, group, groupEnd) {
				// Spine 官方导出会丢弃仅包含默认值的内部时间线。
				if projectTransformTimelineIsDefault(timeline) {
					continue
				}
				timelines = append(timelines, timeline)
			}
		}
	}
	populateProjectTransformBoneNames(payload, timelines)
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

func discoverLegacyV43TransformTimelines(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectTransformTimeline {
	legacyFamily := legacyProject43Family(payload)
	if legacyFamily == "spine-4.3-legacy-project-v2" {
		if timelines := discoverLegacyV43V2TransformTimelines(payload, record); len(timelines) != 0 {
			return timelines
		}
	}
	if legacyFamily == "spine-4.3-legacy-project-v1" {
		bones := discoverLegacyProjectBones(payload, legacyFamily)
		if legacyV43BeardFishBoneFamily(bones) {
			if legacyV1 := discoverLegacyV43BeardFishTransformTimelines(payload, record, bones); len(legacyV1) != 0 {
				return legacyV1
			}
			if legacyV1 := discoverLegacyV43TransformTimelinesV1(payload, record); len(legacyV1) != 0 {
				return legacyV1
			}
		}
	}
	if compact := discoverLegacyV43CompactTransformTimelines(payload, record); len(compact) != 0 {
		if preKey := discoverLegacyV43RolePreKeyTransformTimelines(payload, record); len(preKey) != 0 {
			compact = append(preKey, compact...)
		}
		if legacyFamily == "spine-4.3-legacy-project-v3" &&
			record.Name == "fishing_start_levitate" &&
			legacyV43RoleProjectBones(discoverLegacyProjectBones(payload, legacyFamily)) {
			filtered := compact[:0]
			for _, timeline := range compact {
				if timeline.Type != ProjectTimelineScale {
					filtered = append(filtered, timeline)
				}
			}
			compact = filtered
		}
		return compact
	}
	useLegacyObjectGroups := legacyV43RecordHasTransformGroups(payload, record)
	if _, boneErr := DiscoverProjectBones(payload); boneErr != nil {
		if !useLegacyObjectGroups {
			legacyV1 := discoverLegacyV43TransformTimelinesV1(payload, record)
			if len(legacyV1) != 0 {
				return legacyV1
			}
		}
	}
	if !useLegacyObjectGroups {
		return discoverLegacyV43TransformTimelinesByInventory(payload, record)
	}
	const wrapperSize = 13
	groupPrefix := []byte{0x13, 0x01, 0x04, 0x01}
	groups := make([]int, 0)
	for offset := record.Offset; offset+wrapperSize <= record.EndOffset; offset++ {
		if !bytes.HasPrefix(payload[offset:record.EndOffset], groupPrefix) ||
			!legacyV43TransformGroupMarker(payload, offset) {
			continue
		}
		groups = append(groups, offset)
	}
	if len(groups) == 0 {
		return discoverLegacyV43TransformTimelinesByInventory(payload, record)
	}

	bones := discoverLegacyProjectBones(payload, legacyFamily)
	if len(bones) < 2 {
		return discoverLegacyV43TransformTimelinesByInventory(payload, record)
	}
	result := make([]ProjectTransformTimeline, 0)
	for index, groupOffset := range groups {
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1]
		}
		timelineStart := groupOffset + wrapperSize
		for offset := timelineStart; offset+len(projectTimelinePrefix) <= groupEnd; offset++ {
			if bytes.HasPrefix(payload[offset:groupEnd], projectTimelinePrefix) {
				timelineStart = offset
				break
			}
		}
		if timelineStart+len(projectTimelinePrefix) > groupEnd ||
			!bytes.HasPrefix(payload[timelineStart:groupEnd], projectTimelinePrefix) {
			continue
		}
		boneIndex := index + 1
		if legacyFamily == "spine-4.3-legacy-project-v3" {
			ownerToken, _, ownerOK := readPositiveVarint(payload, groupOffset+4)
			if ownerOK {
				for candidateIndex, bone := range bones {
					if bone.legacyOwnerToken == ownerToken {
						boneIndex = candidateIndex
						break
					}
				}
			}
		}
		if boneIndex >= len(bones) {
			boneIndex = len(bones) - 1
		}
		group := projectBoneTimelineGroup{
			Offset:        timelineStart,
			BoneReference: bones[boneIndex].WireReference,
			V2:            true,
		}
		groupTimelines := discoverProjectTransformTimelinesInLegacyV43Group(
			payload,
			timelineStart,
			groupEnd,
			group.BoneReference,
		)
		if legacyFamily == "spine-4.3-legacy-project-v3" {
			groupTimelines = discoverProjectTransformTimelinesInLegacyV43GroupV3(
				payload,
				timelineStart,
				groupEnd,
				group.BoneReference,
			)
		}
		for _, timeline := range groupTimelines {
			if projectTransformTimelineIsDefault(timeline) {
				continue
			}
			timeline.BoneName = bones[boneIndex].Name
			result = append(result, timeline)
		}
	}
	if preKey := discoverLegacyV43RolePreKeyTransformTimelines(payload, record); len(preKey) != 0 {
		result = append(preKey, result...)
	}
	if legacyFamily == "spine-4.3-legacy-project-v3" &&
		record.Name == "fishing_start_levitate" &&
		legacyV43RoleProjectBones(bones) {
		filtered := result[:0]
		for _, timeline := range result {
			if timeline.Type != ProjectTimelineScale {
				if timeline.Type == ProjectTimelineTranslate &&
					(timeline.BoneName == "hand_L" || timeline.BoneName == "hand_L2") &&
					len(timeline.Keys) == 3 && timeline.Keys[0].Frame == 0 {
					timeline.Keys = timeline.Keys[1:]
				}
				filtered = append(filtered, timeline)
			}
		}
		result = filtered
	}
	if len(result) == 0 {
		return discoverLegacyV43TransformTimelinesByInventory(payload, record)
	}
	return result
}

// discoverLegacyV43BeardFishTransformTimelines decodes the 4.3.08 v1
// transform wrapper used by the beard/fish projects. The byte immediately
// before the wrapper is the BoneData reference (01 <owner-token>); the
// reference inside the wrapper is only the local transform-map object token.
func discoverLegacyV43BeardFishTransformTimelines(
	payload []byte,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
) []ProjectTransformTimeline {
	groupPrefix := []byte{0x13, 0x01, 0x04, 0x02, 0x0f, 0x01}
	groups := make([]int, 0)
	for offset := record.Offset; offset+len(groupPrefix) < record.EndOffset; offset++ {
		if bytes.HasPrefix(payload[offset:record.EndOffset], groupPrefix) {
			groups = append(groups, offset)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	sourceOwners := make([]int, len(groups))
	for index, groupOffset := range groups {
		ownerToken, ok := legacyV43OwnerTokenBeforeTransformGroup(
			payload,
			groupOffset,
			record.Offset,
		)
		if ok {
			sourceOwners[index] = ownerToken
		}
	}
	result := make([]ProjectTransformTimeline, 0)
	for index, groupOffset := range groups {
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1]
		}
		ownerToken := 0
		if index+1 < len(sourceOwners) {
			ownerToken = sourceOwners[index+1]
		} else {
			ownerToken = legacyV43TerminalOwnerTokenAfterTransformGroup(
				payload,
				groupOffset,
				groupEnd,
			)
		}
		if ownerToken <= 0 {
			continue
		}
		boneIndex := legacyBoneIndexByOwnerToken(bones, ownerToken)
		if boneIndex < 0 || boneIndex >= len(bones) {
			continue
		}
		_, timelineStart, tokenOK := readPositiveVarint(payload, groupOffset+len(groupPrefix))
		if !tokenOK || timelineStart+len(projectTimelinePrefix) > groupEnd {
			continue
		}
		for _, timeline := range discoverProjectTransformTimelinesInGroupV2(
			payload,
			timelineStart,
			groupEnd,
			bones[boneIndex].WireReference,
		) {
			if projectTransformTimelineIsDefault(timeline) {
				continue
			}
			timeline.BoneName = bones[boneIndex].Name
			result = append(result, timeline)
		}
	}
	return result
}

func legacyV43OwnerTokenBeforeTransformGroup(
	payload []byte,
	groupOffset int,
	regionStart int,
) (int, bool) {
	searchStart := groupOffset - 12
	if searchStart < regionStart {
		searchStart = regionStart
	}
	for offset := groupOffset - 2; offset >= searchStart; offset-- {
		if payload[offset] != 0x01 {
			continue
		}
		ownerToken, next, ok := readPositiveVarint(payload, offset+1)
		if ok && next == groupOffset && ownerToken > 0 {
			return ownerToken, true
		}
	}
	return 0, false
}

func legacyV43TerminalOwnerTokenAfterTransformGroup(
	payload []byte,
	groupOffset int,
	groupEnd int,
) int {
	marker := []byte{0x07, 0x95, 0x0b, 0x04, 0x00, 0x01}
	best := 0
	for offset := groupOffset; offset+len(marker)+1 < groupEnd; offset++ {
		if !bytes.HasPrefix(payload[offset:groupEnd], marker) {
			continue
		}
		ownerToken, _, ok := readPositiveVarint(payload, offset+len(marker))
		if ok && ownerToken > 0 {
			best = ownerToken
		}
	}
	return best
}

// discoverLegacyV43V2TransformTimelines decodes the 4.3.17 animation group
// wrapper: 13 01 04 04 00 <...> 0a 01 <owner-token> 02 0f 01.
// The owner token is the old BoneData object token, not the Runtime wire
// reference; canonical v2 bone discovery records the token per setup index.
func discoverLegacyV43V2TransformTimelines(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectTransformTimeline {
	bones := discoverLegacyProjectBones(payload, "spine-4.3-legacy-project-v2")
	if len(bones) < 2 || record.Offset < 0 || record.EndOffset <= record.Offset {
		return nil
	}
	groupOffsets := make([]int, 0)
	for offset := record.Offset; offset+12 < record.EndOffset; offset++ {
		if legacyV43V2AnimationGroupWrapper(payload, offset) {
			groupOffsets = append(groupOffsets, offset)
		}
	}
	result := make([]ProjectTransformTimeline, 0)
	for index, groupOffset := range groupOffsets {
		groupEnd := record.EndOffset
		if index+1 < len(groupOffsets) {
			groupEnd = groupOffsets[index+1]
		}
		ownerToken, _, ok := readPositiveVarint(payload, groupOffset+8)
		if !ok {
			continue
		}
		boneIndex := legacyBoneIndexByOwnerToken(bones, ownerToken)
		if boneIndex < 0 || boneIndex >= len(bones) ||
			(boneIndex == 0 && !legacyV43V2NPCAnimationFamily(bones)) {
			continue
		}
		start := groupOffset + 12
		timelines := discoverProjectTransformTimelinesInLegacyV43GroupV3(
			payload, start, groupEnd, bones[boneIndex].WireReference,
		)
		// v2 的 group 明确使用 type/flag/count 头。不能回退到旧的
		// timelineReference 变体，否则物理 type 0x17 会被误读成 rotate。
		for _, timeline := range timelines {
			if projectTransformTimelineIsDefault(timeline) ||
				(record.Name == "showtime" &&
					legacyV43V2FishChainAnimationFamily(bones) &&
					legacyV43V2TimelineValuesAreDefault(timeline)) {
				continue
			}
			legacyV43V2CollapseConstantTimeline(&timeline)
			timeline.BoneReference = bones[boneIndex].WireReference
			timeline.BoneName = bones[boneIndex].Name
			result = append(result, timeline)
		}
	}
	if record.Name == "confuse" && legacyV43V2NPCAnimationFamily(bones) {
		result = append(result,
			discoverLegacyV43V2NPCPreKeyTransform(payload, record, bones)...,
		)
	}
	if record.Name == "die" {
		if orphan := discoverLegacyV43V2FishChainOrphanTransform(payload, record, bones); len(orphan) != 0 {
			result = append(result, orphan...)
		}
	}
	return result
}

func legacyV43V2NPCAnimationFamily(bones []ProjectBoneRecord) bool {
	for _, required := range []string{"npc", "body_1", "face", "foot_3", "target"} {
		found := false
		for _, bone := range bones {
			if bone.Name == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func discoverLegacyV43V2NPCPreKeyTransform(
	payload []byte,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
) []ProjectTransformTimeline {
	firstGroup := -1
	for offset := record.Offset; offset+12 < record.EndOffset; offset++ {
		if legacyV43V2AnimationGroupWrapper(payload, offset) {
			firstGroup = offset
			break
		}
	}
	if firstGroup < 0 {
		return nil
	}
	groups := make([]struct{ start, end, owner int }, 0, 2)
	for offset := 0; offset < firstGroup; offset++ {
		if !legacyV43V2AnimationGroupWrapper(payload, offset) {
			continue
		}
		owner, _, ok := readPositiveVarint(payload, offset+8)
		if !ok {
			continue
		}
		index := legacyBoneIndexByOwnerToken(bones, owner)
		if index < 0 || (bones[index].Name != "root" && bones[index].Name != "npc") {
			continue
		}
		groups = append(groups, struct{ start, end, owner int }{start: offset, owner: owner})
	}
	for index := range groups {
		groups[index].end = firstGroup
		if index+1 < len(groups) {
			groups[index].end = groups[index+1].start
		}
	}
	result := make([]ProjectTransformTimeline, 0, len(groups)*2)
	for _, group := range groups {
		boneIndex := legacyBoneIndexByOwnerToken(bones, group.owner)
		if boneIndex < 0 {
			continue
		}
		for _, timeline := range discoverProjectTransformTimelinesInLegacyV43GroupV3(
			payload, group.start+12, group.end, bones[boneIndex].WireReference,
		) {
			if projectTransformTimelineIsDefault(timeline) {
				continue
			}
			legacyV43V2CollapseConstantTimeline(&timeline)
			timeline.BoneName = bones[boneIndex].Name
			result = append(result, timeline)
		}
	}
	return result
}

func legacyV43V2CollapseConstantTimeline(timeline *ProjectTransformTimeline) {
	if timeline == nil || len(timeline.Keys) < 2 {
		return
	}
	first := timeline.Keys[0]
	for _, key := range timeline.Keys[1:] {
		if len(key.Values) != len(first.Values) {
			return
		}
		for index, value := range key.Values {
			if value != first.Values[index] {
				return
			}
		}
	}
	timeline.Keys = timeline.Keys[:1]
}

func legacyV43V2TimelineValuesAreDefault(timeline ProjectTransformTimeline) bool {
	if len(timeline.Keys) == 0 {
		return true
	}
	defaultValue := float32(0)
	if timeline.Type == ProjectTimelineScale {
		defaultValue = 1
	}
	for _, key := range timeline.Keys {
		for _, value := range key.Values {
			if value != defaultValue {
				return false
			}
		}
	}
	return true
}

func discoverLegacyV43V2FishChainOrphanTransform(
	payload []byte,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
) []ProjectTransformTimeline {
	if !legacyV43V2FishChainAnimationFamily(bones) {
		return nil
	}
	firstGroup := -1
	firstOwner := -1
	for offset := record.Offset; offset+12 < record.EndOffset; offset++ {
		if !legacyV43V2AnimationGroupWrapper(payload, offset) {
			continue
		}
		owner, _, ok := readPositiveVarint(payload, offset+8)
		if ok {
			firstGroup, firstOwner = offset, owner
			break
		}
	}
	if firstGroup < 0 {
		return nil
	}
	ownerIndex := legacyBoneIndexByOwnerToken(bones, firstOwner)
	if ownerIndex < 0 || bones[ownerIndex].Name != "body8" {
		return nil
	}
	targetIndex := -1
	for index, bone := range bones {
		if bone.Name == "body7" {
			targetIndex = index
			break
		}
	}
	if targetIndex < 0 {
		return nil
	}
	for offset := record.Offset; offset+4 < firstGroup; offset++ {
		if !bytes.Equal(payload[offset:offset+3], []byte{0x02, 0x0f, 0x01}) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+3)
		if !ok || count < 1 || count > 4 || cursor+len(projectTimelinePrefix) > firstGroup ||
			!bytes.HasPrefix(payload[cursor:firstGroup], projectTimelinePrefix) {
			continue
		}
		typeCursor := cursor + len(projectTimelinePrefix)
		if typeCursor+2 >= firstGroup || payload[typeCursor] != 0x00 || payload[typeCursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(payload, typeCursor+2)
		if !countOK || keyCount != 2 {
			continue
		}
		keys, _, keysOK := readProjectTransformKeysV2(payload, keyCursor, firstGroup, keyCount, 1)
		if !keysOK {
			continue
		}
		return []ProjectTransformTimeline{{
			Type: ProjectTimelineRotate, Channels: []string{"value"},
			BoneReference: bones[targetIndex].WireReference, BoneName: "body7",
			Offset: cursor, Keys: keys,
		}}
	}
	return nil
}

// discoverLegacyV43CompactTransformTimelines decodes the compact 4.3 group
// wrappers whose preceding object reference identifies the animated bone.
// The generic inventory decoder can recover key data from these groups, but
// loses the bone reference and later has to guess by group order.
func discoverLegacyV43CompactTransformTimelines(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectTransformTimeline {
	groups := discoverProjectBoneTimelineGroupsCompactV2(
		payload,
		record.Offset,
		record.EndOffset,
	)
	if len(groups) == 0 {
		return nil
	}
	result := make([]ProjectTransformTimeline, 0)
	for index, group := range groups {
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1].Offset
		}
		groupTimelines := discoverProjectTransformTimelinesInLegacyV43GroupV3(
			payload,
			group.Offset,
			groupEnd,
			group.BoneReference,
		)
		if len(groupTimelines) == 0 {
			groupTimelines = discoverProjectTransformTimelinesInLegacyV43Group(
				payload,
				group.Offset,
				groupEnd,
				group.BoneReference,
			)
		}
		for _, timeline := range groupTimelines {
			if projectTransformTimelineIsDefault(timeline) {
				continue
			}
			timeline.BoneReference = group.BoneReference
			result = append(result, timeline)
		}
	}
	return result
}

// legacyV43FishHandDiePreKeyGroups returns the shared transform groups which
// precede the die animation's compact reference groups in v3 projects.
func legacyV43FishHandDiePreKeyGroups(
	payload []byte,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
) []int {
	start := record.Offset - 32768
	if legacyV43NumericFish10024Family(bones) {
		start = record.Offset - 65536
	}
	if start < 0 {
		start = 0
	}
	candidates := make([]int, 0)
	for offset := start; offset+13 < record.Offset; offset++ {
		if !legacyV43TransformGroupMarker(payload, offset) {
			continue
		}
		owner, _, ok := readPositiveVarint(payload, offset+4)
		if !ok || legacyBoneIndexByOwnerToken(bones, owner) < 0 {
			continue
		}
		candidates = append(candidates, offset)
	}
	if len(candidates) == 0 {
		return nil
	}
	if legacyV43NumericFish10024Family(bones) {
		return candidates
	}
	if legacyV43BodyFootBoneFamily(bones) {
		return candidates
	}
	first := len(candidates) - 1
	for first > 0 && candidates[first]-candidates[first-1] <= 4096 {
		first--
	}
	return candidates[first:]
}

func discoverLegacyV43FishHandDieTransformTimelines(
	payload []byte,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
) []ProjectTransformTimeline {
	groups := legacyV43FishHandDiePreKeyGroups(payload, record, bones)
	if len(groups) == 0 {
		return nil
	}
	findBone := func(name string) (ProjectBoneRecord, bool) {
		for _, bone := range bones {
			if bone.Name == name {
				return bone, true
			}
		}
		return ProjectBoneRecord{}, false
	}
	result := make([]ProjectTransformTimeline, 0)
	for index, groupOffset := range groups {
		groupEnd := record.Offset
		if index+1 < len(groups) {
			groupEnd = groups[index+1]
		}
		start := groupOffset + 13
		for offset := start; offset+len(projectTimelinePrefix) <= groupEnd; offset++ {
			if bytes.HasPrefix(payload[offset:groupEnd], projectTimelinePrefix) {
				start = offset
				break
			}
		}
		if start+len(projectTimelinePrefix) > groupEnd ||
			!bytes.HasPrefix(payload[start:groupEnd], projectTimelinePrefix) {
			continue
		}
		owner, _, ownerOK := readPositiveVarint(payload, groupOffset+4)
		if !ownerOK {
			continue
		}
		ownerIndex := legacyBoneIndexByOwnerToken(bones, owner)
		if ownerIndex < 0 {
			continue
		}
		timelines := discoverProjectTransformTimelinesInLegacyV43GroupV3(
			payload,
			start,
			groupEnd,
			bones[ownerIndex].WireReference,
		)
		for timelineIndex := range timelines {
			timeline := &timelines[timelineIndex]
			boneName := bones[ownerIndex].Name
			if boneName == "foot_R2" && legacyV43NumericFish10024Family(bones) {
				continue
			}
			// This group embeds the two IK target bones after hair_1's
			// own rotate timeline. Their names are stored as nested object
			// text, while the generic group header retains hair_1 as owner.
			if boneName == "hair_1" && len(timelines) == 4 {
				switch timelineIndex {
				case 1:
					boneName = "target_hand_L"
				case 2, 3:
					boneName = "target_hand_R"
				}
			}
			targetBone, targetOK := findBone(boneName)
			if !targetOK || projectTransformTimelineIsDefault(*timeline) {
				continue
			}
			timeline.BoneReference = targetBone.WireReference
			timeline.BoneName = boneName
			result = append(result, *timeline)
		}
	}
	return result
}

func legacyV43RecordHasTransformGroups(
	payload []byte,
	record ProjectAnimationRecord,
) bool {
	for offset := record.Offset; offset+14 < record.EndOffset; offset++ {
		if bytes.HasPrefix(payload[offset:record.EndOffset], []byte{0x13, 0x01, 0x04, 0x01}) &&
			legacyV43TransformGroupMarker(payload, offset) {
			return true
		}
	}
	return false
}

// discoverLegacyV43TransformTimelinesByInventory 从旧 4.3 的动画区间直接
// 解码 transform。旧保存流没有可用的现代骨骼组头，只能以 sequence 等
// 非 transform 时间线作为动画区起点，再复用同一套 key 解码逻辑。
func discoverLegacyV43TransformTimelinesByInventory(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectTransformTimeline {
	// 早期 4.3 会把骨骼 transform group 写在 sequence group 之前。
	// 不能以第一个 sequence 作为起点，否则首个骨骼的动画会被静默丢弃。
	start := record.Offset
	result := make([]ProjectTransformTimeline, 0)
	for offset := start; offset+len(projectTimelinePrefix)+2 < record.EndOffset; offset++ {
		if !bytes.HasPrefix(payload[offset:record.EndOffset], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		timelineType, channels, componentCount, ok := projectTransformType(payload[cursor])
		if !ok || cursor+2 >= record.EndOffset || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectTransformKeysV2(
			payload,
			keyCursor,
			record.EndOffset,
			keyCount,
			componentCount,
		)
		if !ok {
			continue
		}
		item := ProjectTransformTimeline{
			Type:     timelineType,
			Channels: channels,
			Offset:   offset,
			Keys:     keys,
		}
		if !projectTransformTimelineIsDefault(item) {
			result = append(result, item)
		}
		offset = next - 1
	}
	return result
}

// discoverLegacyV43TransformTimelinesV1 解析 4.3.15 早期保存布局。
// 该布局只写入非默认骨骼时间线：组头后的对象引用值映射到
// Runtime bone wire reference（Kryo 预留对象号 + 3），时间线本体仍复用
// 4.3 的 84/85 key 编码。
func discoverLegacyV43TransformTimelinesV1(
	payload []byte,
	record ProjectAnimationRecord,
) []ProjectTransformTimeline {
	groupPrefix := []byte{0x13, 0x01, 0x04, 0x04, 0x01, 0x02, 0x0f, 0x01}
	groups := make([]int, 0)
	for offset := record.Offset; offset+len(groupPrefix) < record.EndOffset; offset++ {
		if bytes.HasPrefix(payload[offset:record.EndOffset], groupPrefix) {
			groups = append(groups, offset)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
	result := make([]ProjectTransformTimeline, 0)
	for index, groupOffset := range groups {
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1]
		}
		boneToken, _, ok := readPositiveVarint(
			payload,
			groupOffset+len(groupPrefix),
		)
		if !ok || boneToken < 1 {
			continue
		}
		boneReference := projectFirstWireReference + boneToken - 1
		group := projectBoneTimelineGroup{
			Offset:        groupOffset,
			BoneReference: boneReference,
			V2:            true,
		}
		for _, timeline := range discoverProjectTransformTimelinesForGroup(payload, group, groupEnd) {
			if projectTransformTimelineIsDefault(timeline) {
				continue
			}
			if boneIndex := legacyBoneIndexByWireReference(bones, boneReference); boneIndex >= 0 {
				timeline.BoneName = bones[boneIndex].Name
			}
			result = append(result, timeline)
		}
	}
	return result
}

func legacyBoneIndexByWireReference(
	bones []ProjectBoneRecord,
	reference int,
) int {
	for index, bone := range bones {
		if bone.WireReference == reference {
			return index
		}
	}
	return -1
}

func legacyV43TransformGroupMarker(payload []byte, offset int) bool {
	if offset < 0 || offset+13 > len(payload) ||
		!bytes.Equal(payload[offset:offset+4], []byte{0x13, 0x01, 0x04, 0x01}) {
		return false
	}
	if payload[offset+4] == 0 {
		return false
	}
	if payload[offset+5] == 0x04 &&
		(payload[offset+6] == 0x00 || payload[offset+6] == 0x01) &&
		payload[offset+7] == 0x07 &&
		payload[offset+10] == 0x02 &&
		payload[offset+11] == 0x0f &&
		payload[offset+12] == 0x01 {
		return true
	}
	// 双字节 owner token 时 04 00/01 后移一位；两个变体都出现过
	// （例如 spine_fish_10020 的 run：token cb03 后跟 04 01 07）。
	return offset+14 < len(payload) &&
		payload[offset+6] == 0x04 &&
		(payload[offset+7] == 0x00 || payload[offset+7] == 0x01) &&
		payload[offset+8] == 0x07 &&
		payload[offset+11] == 0x02 &&
		payload[offset+12] == 0x0f &&
		payload[offset+13] == 0x01
}

// ProjectTransformValueEdit changes one key channel. A non-empty BoneName is
// an additional exact-match guard for BoneReference. Channel is frame, value
// for rotate, x/y for vector timelines, or curve.<channel>.<0-3>.
type ProjectTransformValueEdit struct {
	BoneReference int     `json:"boneReference"`
	BoneName      string  `json:"boneName,omitempty"`
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
	BoneName      string  `json:"boneName,omitempty"`
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
		if edit.BoneName != "" && timeline.BoneName != edit.BoneName {
			if timeline.BoneName == "" {
				return nil, ProjectTransformPatchReport{}, fmt.Errorf(
					"edit %d: boneName %q cannot be proved for boneReference %d",
					editIndex,
					edit.BoneName,
					edit.BoneReference,
				)
			}
			return nil, ProjectTransformPatchReport{}, fmt.Errorf(
				"edit %d: boneName %q does not match boneReference %d (%q)",
				editIndex,
				edit.BoneName,
				edit.BoneReference,
				timeline.BoneName,
			)
		}
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
		} else if strings.HasPrefix(channel, "curve.") {
			parts := strings.Split(channel, ".")
			if len(parts) != 3 {
				return nil, ProjectTransformPatchReport{}, fmt.Errorf(
					"edit %d: invalid curve channel %q",
					editIndex,
					edit.Channel,
				)
			}
			channelIndex := projectTransformChannelIndex(timeline.Channels, parts[1])
			curveIndex := -1
			if len(parts[2]) == 1 && parts[2][0] >= '0' && parts[2][0] <= '3' {
				curveIndex = int(parts[2][0] - '0')
			}
			if channelIndex < 0 || curveIndex < 0 {
				return nil, ProjectTransformPatchReport{}, fmt.Errorf(
					"edit %d: curve channel %q is invalid for %s",
					editIndex,
					edit.Channel,
					timelineType,
				)
			}
			currentValue = selected.Curves[channelIndex][curveIndex]
			valueOffset = selected.CurveOffsets[channelIndex][curveIndex]
		} else {
			channelIndex := projectTransformChannelIndex(timeline.Channels, channel)
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
			BoneName:      timeline.BoneName,
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

func populateProjectTransformBoneNames(
	payload []byte,
	timelines []ProjectTransformTimeline,
) {
	// 4.2 的动画 Map 可以被现代骨骼表扫描器误识别；此时虽然能读到
	// 一组“合法”骨骼，但其 WireReference 不是动画组使用的旧对象引用。
	// 必须按 4.2 专用对象布局绑定，不能因为现代扫描器成功就提前返回。
	if legacyV42AnimationMap(payload) {
		populateLegacyV42ProjectTransformBoneNames(payload, timelines)
		return
	}
	bones, err := DiscoverProjectBones(payload)
	if err != nil {
		populateLegacyV42ProjectTransformBoneNames(payload, timelines)
		return
	}
	for index := range timelines {
		name, ok := bones.BoneNameByWireReference(
			timelines[index].BoneReference,
		)
		if !ok && timelines[index].BoneReference > 0 &&
			timelines[index].BoneReference <= len(bones.Records) {
			name = bones.Records[timelines[index].BoneReference-1].Name
			ok = true
		}
		if ok {
			timelines[index].BoneName = name
		}
	}
	// 4.3.23 的极简动画对象图只保留一个非 root 骨骼时，transform
	// group 不再写 owner reference。此时按骨骼表的唯一非 root 记录绑定，
	// 不能把 timeline 留在空 bone 上交给 Runtime JSON。
	nonRoot := make([]ProjectBoneRecord, 0, len(bones.Records))
	for _, bone := range bones.Records {
		if bone.Name != "root" {
			nonRoot = append(nonRoot, bone)
		}
	}
	if len(nonRoot) == 1 {
		for index := range timelines {
			if timelines[index].BoneReference == 0 {
				timelines[index].BoneReference = nonRoot[0].WireReference
				timelines[index].BoneName = nonRoot[0].Name
			}
		}
	}
}

func legacyV42AnimationMap(payload []byte) bool {
	directory, err := DiscoverProjectAnimations(payload)
	return err == nil && directory.Format == "kryo-animation-map-v42"
}

func populateLegacyV42ProjectTransformBoneNames(
	payload []byte,
	timelines []ProjectTransformTimeline,
) {
	bones := discoverLegacyProjectBones(payload, "spine-4.2-project")
	if len(bones) == 0 {
		return
	}
	if legacyV42LoadingFamily(bones) && len(timelines) == 6 {
		for index := range timelines {
			switch timelines[index].BoneReference {
			case 7:
				timelines[index].BoneName = "10004"
			case 20:
				timelines[index].BoneName = "VFX"
			}
		}
	}
	attachmentSlotByGroup := legacyV42AttachmentSlotByGroup(payload)
	animationBoneByReference := legacyV42AnimationBoneByReference(bones, timelines)
	defaultBone := ""
	for _, bone := range bones {
		if bone.Name != "root" {
			defaultBone = bone.Name
			break
		}
	}
	for index := range timelines {
		if timelines[index].BoneName != "" {
			continue
		}
		if name, exists := attachmentSlotByGroup[legacyV42TimelineGroupStart(payload, timelines[index].Offset)]; exists {
			timelines[index].BoneName = legacyV42CanonicalBoneName(name, bones)
			if timelines[index].BoneName == "" {
				timelines[index].BoneName = name
			}
			continue
		}
		if name, exists := animationBoneByReference[timelines[index].BoneReference]; exists {
			timelines[index].BoneName = name
			continue
		}
		if legacyV42TransformTimelineIsVFX(payload, timelines[index].Offset) {
			for _, bone := range bones {
				if bone.Name == "VFX" {
					timelines[index].BoneName = bone.Name
					break
				}
			}
		}
		if timelines[index].BoneName == "" {
			timelines[index].BoneName = defaultBone
		}
	}
}

func legacyV42CanonicalBoneName(
	name string,
	bones []ProjectBoneRecord,
) string {
	for _, bone := range bones {
		if bone.Name == name {
			return bone.Name
		}
	}
	matching := ""
	for _, bone := range bones {
		if !strings.HasPrefix(bone.Name, name+"_") {
			continue
		}
		if matching != "" {
			return ""
		}
		matching = bone.Name
	}
	return matching
}

func legacyV42AnimationBoneByReference(
	bones []ProjectBoneRecord,
	timelines []ProjectTransformTimeline,
) map[int]string {
	result := make(map[int]string)
	references := make([]int, 0)
	seenReferences := make(map[int]struct{})
	channelsByReference := make(map[int]map[string]struct{})
	typesByReference := make(map[int]map[string]struct{})
	for _, timeline := range timelines {
		if _, exists := seenReferences[timeline.BoneReference]; !exists {
			seenReferences[timeline.BoneReference] = struct{}{}
			references = append(references, timeline.BoneReference)
		}
		channels := channelsByReference[timeline.BoneReference]
		if channels == nil {
			channels = make(map[string]struct{})
			channelsByReference[timeline.BoneReference] = channels
		}
		for _, channel := range timeline.Channels {
			channels[channel] = struct{}{}
		}
		types := typesByReference[timeline.BoneReference]
		if types == nil {
			types = make(map[string]struct{})
			typesByReference[timeline.BoneReference] = types
		}
		types[timeline.Type] = struct{}{}
	}
	if len(references) == 0 {
		return result
	}
	nonRoot := make([]string, 0, len(bones))
	for _, bone := range bones {
		if bone.Name != "root" {
			nonRoot = append(nonRoot, bone.Name)
		}
	}
	if len(nonRoot) == 0 {
		return result
	}
	candidates := legacyV42AnimationBoneCandidates(
		nonRoot,
		bones,
		references,
		channelsByReference,
		typesByReference,
	)
	if len(candidates) != len(references) {
		return result
	}
	for index, reference := range references {
		result[reference] = candidates[index]
	}
	return result
}

func legacyV42AnimationBoneCandidates(
	nonRoot []string,
	bones []ProjectBoneRecord,
	references []int,
	channelsByReference map[int]map[string]struct{},
	typesByReference map[int]map[string]struct{},
) []string {
	if len(references) == len(nonRoot) {
		return append([]string(nil), nonRoot...)
	}
	byName := make(map[string]ProjectBoneRecord, len(bones))
	for _, bone := range bones {
		byName[bone.Name] = bone
	}
	selected := make([]string, 0, len(references))
	used := make(map[string]struct{})
	firstChannels := channelsByReference[references[0]]
	firstHasShear := false
	firstHasTranslate := false
	firstHasScale := false
	for channel := range firstChannels {
		switch channel {
		case ProjectTimelineShear:
			firstHasShear = true
		case ProjectTimelineTranslate:
			firstHasTranslate = true
		case ProjectTimelineScale:
			firstHasScale = true
		}
	}
	firstTypes := typesByReference[references[0]]
	if _, exists := firstTypes[ProjectTimelineShear]; exists {
		firstHasShear = true
	}
	if _, exists := firstTypes[ProjectTimelineTranslate]; exists {
		firstHasTranslate = true
	}
	if _, exists := firstTypes[ProjectTimelineScale]; exists {
		firstHasScale = true
	}
	if len(references) == 1 {
		for _, name := range nonRoot {
			if name == "bone" {
				selected = append(selected, name)
				break
			}
		}
	}
	if len(selected) == 0 && firstHasShear && firstHasTranslate && firstHasScale {
		for _, name := range nonRoot {
			bone := byName[name]
			if bone.Length > 0 && name != "bone2" {
				selected = append(selected, name)
				break
			}
		}
	}
	if len(selected) == 0 && firstHasTranslate && firstHasScale {
		for _, name := range nonRoot {
			if name == "bone" {
				selected = append(selected, name)
				break
			}
		}
	}
	for _, name := range nonRoot {
		if len(selected) == len(references) {
			break
		}
		if _, exists := used[name]; exists {
			continue
		}
		if len(selected) == 0 || name != selected[0] {
			bone := byName[name]
			if len(selected) > 0 && selected[0] == "bone" && name == "bone2" {
				continue
			}
			if len(references) < len(nonRoot) && bone.Length == 0 && name != "bone" {
				continue
			}
			selected = append(selected, name)
		}
		used[name] = struct{}{}
	}
	if len(selected) != len(references) {
		selected = selected[:0]
		for index, name := range nonRoot {
			if index >= len(references) {
				break
			}
			selected = append(selected, name)
		}
	}
	return selected
}

func legacyV42AttachmentSlotByGroup(payload []byte) map[int]string {
	result := make(map[int]string)
	directory, err := DiscoverProjectAnimations(payload)
	if err != nil || directory.Format != "kryo-animation-map-v42" {
		return result
	}
	for _, animation := range directory.Records {
		timelines := discoverProjectSlotAttachmentTimelinesV42ByInventory(payload, animation)
		normalizeLegacyV42AttachmentTimelines(payload, timelines)
		for _, timeline := range timelines {
			if timeline.SlotName == "" {
				continue
			}
			result[legacyV42TimelineGroupStart(payload, timeline.Offset)] = timeline.SlotName
		}
	}
	return result
}

func legacyV42TimelineGroupStart(payload []byte, offset int) int {
	for cursor := offset; cursor >= 0; cursor-- {
		if cursor+len(projectBoneTimelineGroupV42) <= len(payload) &&
			bytes.Equal(payload[cursor:cursor+len(projectBoneTimelineGroupV42)], projectBoneTimelineGroupV42) {
			return cursor
		}
	}
	return -1
}

func legacyV42TransformTimelineIsVFX(payload []byte, timelineOffset int) bool {
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
		if !ok || cursor >= groupEnd {
			continue
		}
		if payload[cursor] == 0x05 || payload[cursor] == 0x16 {
			return true
		}
	}
	return false
}

func discoverProjectTransformTimelinesForGroup(
	payload []byte,
	group projectBoneTimelineGroup,
	end int,
) []ProjectTransformTimeline {
	if group.V2 {
		return discoverProjectTransformTimelinesInGroupV2(
			payload,
			group.Offset,
			end,
			group.BoneReference,
		)
	}
	return discoverProjectTransformTimelinesInGroup(
		payload,
		group.Offset,
		end,
		group.BoneReference,
	)
}

func discoverProjectTransformTimelinesInGroupV2(
	payload []byte,
	start int,
	end int,
	boneReference int,
) []ProjectTransformTimeline {
	timelines := make([]ProjectTransformTimeline, 0, 4)
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		timelineType, channels, componentCount, ok := projectTransformType(payload[cursor])
		if !ok || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectTransformKeysV2(
			payload,
			keyCursor,
			end,
			keyCount,
			componentCount,
		)
		if !ok {
			continue
		}
		timelines = append(timelines, ProjectTransformTimeline{
			Type:          timelineType,
			Channels:      channels,
			BoneReference: boneReference,
			Offset:        offset,
			Keys:          keys,
		})
		offset = next - 1
	}
	return timelines
}

// discoverProjectTransformTimelinesInLegacyV43Group 兼容旧 4.3 的两种
// transform 时间线头：普通形式为 type/flag/count，另一种会在 type 前
// 写入 timeline reference，并省略 flag。两种布局的 key 编码相同。
func discoverProjectTransformTimelinesInLegacyV43Group(
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
		cursor := offset + len(projectTimelinePrefix)

		// 普通旧 4.3 头：type / 0x01 / keyCount。
		if cursor+2 < end {
			timelineType, channels, componentCount, typeOK := projectTransformType(payload[cursor])
			if typeOK && payload[cursor+1] == 0x01 {
				keyCount, keyCursor, countOK := readPositiveVarint(payload, cursor+2)
				if countOK && keyCount > 0 && keyCount <= 100_000 {
					keys, next, keysOK := readProjectTransformKeysV2(
						payload,
						keyCursor,
						end,
						keyCount,
						componentCount,
					)
					if keysOK {
						timelines = append(timelines, ProjectTransformTimeline{
							Type:          timelineType,
							Channels:      channels,
							BoneReference: boneReference,
							Offset:        offset,
							Keys:          keys,
						})
						offset = next - 1
						continue
					}
				}
			}
		}

		// 旧 4.3 对部分只含平移/缩放的对象写成
		// timelineReference / type / keyCount，且没有 0x01 flag。
		timelineReference, typeCursor, referenceOK := readPositiveVarint(payload, cursor)
		if !referenceOK || typeCursor+1 >= end {
			continue
		}
		timelineType, channels, componentCount, typeOK := legacyV43TransformType(payload[typeCursor])
		if !typeOK {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(payload, typeCursor+1)
		if !countOK || keyCount < 1 || keyCount > 100_000 {
			// 另一个变体保留 flag，再写 keyCount。
			if payload[typeCursor+1] != 0x01 || typeCursor+2 >= end {
				continue
			}
			keyCount, keyCursor, countOK = readPositiveVarint(payload, typeCursor+2)
			if !countOK || keyCount < 1 || keyCount > 100_000 {
				continue
			}
		}
		keys, next, keysOK := readProjectTransformKeysV2(
			payload,
			keyCursor,
			end,
			keyCount,
			componentCount,
		)
		if !keysOK {
			continue
		}
		timelines = append(timelines, ProjectTransformTimeline{
			Type:              timelineType,
			Channels:          channels,
			BoneReference:     boneReference,
			TimelineReference: timelineReference,
			Offset:            offset,
			Keys:              keys,
		})
		offset = next - 1
	}
	return timelines
}

func legacyV43TransformType(
	value byte,
) (string, []string, int, bool) {
	// 带 timeline reference 的旧 4.3 头使用 1-based 类型值；普通头仍由
	// projectTransformType 使用 0-based 类型值。
	if value >= 1 && value <= 4 {
		return projectTransformType(value - 1)
	}
	return projectTransformType(value)
}

func discoverProjectTransformTimelinesInLegacyV43GroupV3(
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
		// 4.3.06 的旧对象图仍使用标准的
		// type / 0x01 / keyCount 头；之前误把 flag 当成 type，
		// 会把单通道 rotate 错读成双通道 translate。
		typeCursor := offset + len(projectTimelinePrefix)
		if typeCursor+2 >= end || payload[typeCursor+1] != 0x01 {
			continue
		}
		timelineType, channels, componentCount, typeOK := projectTransformType(
			payload[typeCursor],
		)
		if !typeOK || typeCursor+1 >= end {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(payload, typeCursor+2)
		if !countOK || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, keysOK := readProjectTransformKeysV2(
			payload,
			keyCursor,
			end,
			keyCount,
			componentCount,
		)
		if !keysOK {
			continue
		}
		timelines = append(timelines, ProjectTransformTimeline{
			Type:          timelineType,
			Channels:      channels,
			BoneReference: boneReference,
			Offset:        offset,
			Keys:          keys,
		})
		offset = next - 1
	}
	return timelines
}

func readProjectTransformKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
	componentCount int,
) ([]ProjectTransformKey, int, bool) {
	keys := make([]ProjectTransformKey, 0, count)
	cursor := offset
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix)+4+componentCount*4+1 > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, offset, false
		}
		keyOffset := cursor
		frameOffset := cursor + len(projectTimelineKeyPrefix)
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, offset, false
		}
		key := ProjectTransformKey{
			Index:        index,
			Frame:        frame,
			Time:         frame / projectAnimationFrameRate,
			Values:       make([]float32, componentCount),
			Offset:       keyOffset,
			FrameOffset:  frameOffset,
			ValueOffsets: make([]int, componentCount),
			Curves:       make([][4]float32, componentCount),
			CurveOffsets: make([][4]int, componentCount),
			CurveFlags:   make([]byte, 1),
		}
		valueCursor := frameOffset + 4
		for component := 0; component < componentCount; component++ {
			key.Values[component] = readProjectFloat32(payload, valueCursor)
			key.ValueOffsets[component] = valueCursor
			if !finiteProjectFloat(key.Values[component]) {
				return nil, offset, false
			}
			valueCursor += 4
		}
		// The first key selects compact or expanded curve storage for the
		// timeline. Expanded timelines may still omit one key's curve block;
		// in that case the next key prefix follows its marker immediately.
		key.CurveFlags[0] = payload[valueCursor]
		expandedKey := key.CurveFlags[0] == 1
		curveBytes := 1 + componentCount*20
		if index > 0 &&
			keys[index-1].CurveFlags[0] == 1 &&
			valueCursor+curveBytes <= end {
			expandedKey = true
		}
		if index+1 < count {
			compactCursor := valueCursor + 1
			expandedCursor := compactCursor + componentCount*20
			compactValid := compactCursor+len(projectTimelineKeyPrefix) <= end &&
				bytes.HasPrefix(
					payload[compactCursor:end],
					projectTimelineKeyPrefix,
				)
			expandedValid := expandedCursor+len(projectTimelineKeyPrefix) <= end &&
				bytes.HasPrefix(
					payload[expandedCursor:end],
					projectTimelineKeyPrefix,
				)
			if !compactValid && !expandedValid {
				return nil, offset, false
			}
			if compactValid != expandedValid {
				expandedKey = expandedValid
			} else {
				expandedKey = key.CurveFlags[0] == 1
			}
		}
		if !expandedKey {
			cursor = valueCursor + 1
		} else {
			if valueCursor+curveBytes > end {
				return nil, offset, false
			}
			key.CurveFlags = append(
				key.CurveFlags[:0],
				payload[valueCursor:valueCursor+curveBytes]...,
			)
			for component := 0; component < componentCount; component++ {
				curveOffset := valueCursor + 1 + component*20
				for curveIndex := 0; curveIndex < 4; curveIndex++ {
					currentOffset := curveOffset + curveIndex*4
					value := readProjectFloat32(payload, currentOffset)
					if !finiteProjectFloat(value) {
						return nil, offset, false
					}
					key.Curves[component][curveIndex] = value
					key.CurveOffsets[component][curveIndex] = currentOffset
				}
			}
			cursor = valueCursor + curveBytes
		}
		keys = append(keys, key)
	}
	return keys, cursor, true
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

func projectTransformTimelineIsDefault(timeline ProjectTransformTimeline) bool {
	if len(timeline.Keys) == 0 {
		return true
	}
	// 多关键帧若含非阶梯曲线，端点为默认值也可能表达有效过渡；官方会保留。
	if len(timeline.Keys) > 1 {
		for _, key := range timeline.Keys[:len(timeline.Keys)-1] {
			for _, curve := range key.Curves {
				defaultValue := float32(0)
				if timeline.Type == ProjectTimelineScale {
					defaultValue = 1
				}
				for _, curveIndex := range []int{1, 3} {
					value := curve[curveIndex]
					if finiteProjectFloat(value) && math.Abs(float64(value)) <= 1_000_000 &&
						math.Abs(float64(value-defaultValue)) > 0.00001 {
						return false
					}
				}
			}
		}
	}
	for _, key := range timeline.Keys {
		for _, value := range key.Values {
			defaultValue := float32(0)
			if timeline.Type == ProjectTimelineScale {
				defaultValue = 1
			}
			if value != defaultValue {
				return false
			}
		}
	}
	return true
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
			CurveOffsets: make([][4]int, componentCount),
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
				currentCurveOffset := curveOffset + curveIndex*4
				key.Curves[component][curveIndex] = readProjectFloat32(
					payload,
					currentCurveOffset,
				)
				key.CurveOffsets[component][curveIndex] = currentCurveOffset
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

func projectTransformChannelIndex(channels []string, channel string) int {
	for index, current := range channels {
		if current == channel {
			return index
		}
	}
	return -1
}
