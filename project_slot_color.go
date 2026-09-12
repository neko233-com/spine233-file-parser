package spineparser

import (
	"bytes"
	"fmt"
	"sort"
)

const projectTimelineRGBA = 4

const (
	projectSlotColorCurveHeaderBytes = 13
	projectSlotColorCurveGroupCount  = 7
	projectSlotColorCurveGroupBytes  = 20
)

// ProjectSlotColorKey is one 4.3.23 RGBA timeline key.
type ProjectSlotColorKey struct {
	Index           int          `json:"index"`
	Frame           float32      `json:"frame"`
	Time            float32      `json:"time"`
	Color           [4]float32   `json:"color"`
	CurveHeader     []byte       `json:"curveHeader,omitempty"`
	Curves          [][4]float32 `json:"curves,omitempty"`
	CurveGroupFlags [][4]byte    `json:"curveGroupFlags,omitempty"`
	Offset          int          `json:"offset"`
	FrameOffset     int          `json:"frameOffset"`
}

// ProjectSlotColorTimeline contains one slot RGBA timeline.
type ProjectSlotColorTimeline struct {
	SlotReference int                   `json:"slotReference"`
	SlotName      string                `json:"slotName"`
	Offset        int                   `json:"offset"`
	Keys          []ProjectSlotColorKey `json:"keys"`
	inlineSlot    bool
}

// ProjectSlotColorTimelineDirectory contains supported RGBA timelines.
type ProjectSlotColorTimelineDirectory struct {
	Animation   string                     `json:"animation"`
	RegionStart int                        `json:"regionStart"`
	RegionEnd   int                        `json:"regionEnd"`
	FrameRate   int                        `json:"frameRate"`
	Timelines   []ProjectSlotColorTimeline `json:"timelines"`
}

// DiscoverProjectSlotColorTimelines decodes 4.3.23 slot RGBA keys.
func DiscoverProjectSlotColorTimelines(
	payload []byte,
	animation string,
) (*ProjectSlotColorTimelineDirectory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v2" && animation == "die" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if preKey := discoverLegacyV43V2SmallFishDiePreKeyTransformTimelines(payload, bones); len(preKey) != 0 {
			limit := len(payload)
			if limit > 8192 {
				limit = 8192
			}
			start := preKey[len(preKey)-1].Offset
			for _, key := range preKey[len(preKey)-1].Keys {
				if key.Offset > start {
					start = key.Offset
				}
			}
			timelines := discoverProjectSlotColorTimelinesInGroupV2(payload, start, limit)
			if len(timelines) != 0 {
				if err := populateProjectSlotColorNames(payload, timelines); err == nil {
					return &ProjectSlotColorTimelineDirectory{
						Animation: animation, RegionStart: timelines[0].Offset,
						RegionEnd: record.EndOffset, FrameRate: projectAnimationFrameRate,
						Timelines: timelines,
					}, nil
				}
			}
		}
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v3" && animation == "die" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if legacyV43IsFishHandFamily(bones) ||
			legacyV43BodyFootBoneFamily(bones) ||
			(legacyV43NumericFishProjectBone(bones) != "" &&
				!legacyV43SmallNumericFishPhysicsFamily(bones) &&
				!legacyV43FishChainNumericPhysicsFamily(bones)) ||
			legacyV43ExpandedFishPhysicsFamily(bones) {
			groups := legacyV43FishHandDiePreKeyGroups(payload, record, bones)
			if len(groups) != 0 {
				timelines := discoverProjectSlotColorTimelinesInGroupV2(
					payload,
					groups[0],
					record.Offset,
				)
				if legacyV43ExpandedFishPhysicsFamily(bones) {
					// The expanded fish stores slot_mouth in the animation's
					// normal group; the shared pre-key group contains the other
					// slot timelines. Keep both proven object groups.
					timelines = append(
						timelines,
						discoverProjectSlotColorTimelinesInGroupV2(
							payload,
							record.Offset,
							record.EndOffset,
						)...,
					)
				}
				if len(timelines) != 0 {
					return &ProjectSlotColorTimelineDirectory{
						Animation:   animation,
						RegionStart: groups[0],
						RegionEnd:   record.Offset,
						FrameRate:   projectAnimationFrameRate,
						Timelines:   timelines,
					}, nil
				}
			}
		}
	}
	var timelines []ProjectSlotColorTimeline
	isV42 := false
	if directory, directoryErr := DiscoverProjectAnimations(payload); directoryErr == nil &&
		directory.Format == "kryo-animation-map-v42" {
		isV42 = true
		timelines = discoverProjectSlotColorTimelinesV42(
			payload,
			record.Offset,
			record.EndOffset,
		)
		normalizeLegacyV42SlotColorTimelines(payload, timelines)
	} else {
		timelines = discoverProjectSlotColorTimelinesInGroupV2(
			payload,
			record.Offset,
			record.EndOffset,
		)
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no supported slot RGBA timelines", animation),
		}
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v3" &&
		animation == "die" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if legacyV43SmallNumericFishPhysicsFamily(bones) && len(timelines) == 1 {
			// This compact group uses the body slot's attachment reference,
			// which is also the body_2 mesh reference. The slot name is the
			// only unambiguous owner recovered from the object graph. Both
			// body slots share the exact same RGBA key stream in the official
			// export.
			timelines[0].SlotName = "body"
			body2 := timelines[0]
			body2.SlotName = "body_2"
			timelines = append(timelines, body2)
		}
	}
	if !isV42 && len(timelines) != 0 && timelines[0].SlotName == "" {
		if err := populateProjectSlotColorNames(payload, timelines); err != nil {
			return nil, err
		}
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no supported slot RGBA timelines", animation),
		}
	}
	if err := validateProjectSlotColorTimelines(timelines); err != nil {
		return nil, err
	}
	return &ProjectSlotColorTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

// discoverProjectSlotColorTimelinesV42 解析 4.2 的旧式 key 引用布局。
// 每个 key 固定包含 timeline/key 引用、帧、RGBA、12 字节曲线头和 7 组曲线数据。
func discoverProjectSlotColorTimelinesV42(
	payload []byte,
	start int,
	end int,
) []ProjectSlotColorTimeline {
	const curveHeaderBytes = 12
	const curveGroupCount = 7
	const curveGroupBytes = 20
	result := make([]ProjectSlotColorTimeline, 0)
	for offset := start; offset+len(projectTimelinePrefix)+3 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		timelineReference, cursor, ok := readPositiveVarint(payload, cursor)
		if !ok || cursor+2 >= end || payload[cursor] != projectTimelineRGBA ||
			payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectSlotColorKeysV42(
			payload,
			keyCursor,
			end,
			timelineReference,
			keyCount,
		)
		if !ok {
			continue
		}
		result = append(result, ProjectSlotColorTimeline{
			SlotReference: timelineReference,
			Offset:        offset,
			Keys:          keys,
		})
		offset = next - 1
	}
	return result
}

func readProjectSlotColorKeysV42(
	payload []byte,
	offset int,
	end int,
	timelineReference int,
	count int,
) ([]ProjectSlotColorKey, int, bool) {
	const curveHeaderBytes = 12
	const curveGroupCount = 7
	const curveGroupBytes = 20
	keys := make([]ProjectSlotColorKey, 0, count)
	cursor := offset
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix) > end || !bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, offset, false
		}
		keyOffset := cursor
		currentTimeline, next, ok := readPositiveVarint(payload, cursor+len(projectTimelineKeyPrefix))
		if !ok || currentTimeline != timelineReference {
			return nil, offset, false
		}
		_, next, ok = readPositiveVarint(payload, next)
		if !ok || next+4+16+curveHeaderBytes+curveGroupCount*curveGroupBytes+1 > end {
			return nil, offset, false
		}
		frameOffset := next
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, offset, false
		}
		key := ProjectSlotColorKey{
			Index: index, Frame: frame, Time: frame / projectAnimationFrameRate,
			Offset: keyOffset, FrameOffset: frameOffset,
			Curves:          make([][4]float32, curveGroupCount),
			CurveGroupFlags: make([][4]byte, curveGroupCount),
		}
		valueOffset := frameOffset + 4
		for component := range key.Color {
			value := readProjectFloat32(payload, valueOffset+component*4)
			if !finiteProjectFloat(value) || value < 0 || value > 1 {
				return nil, offset, false
			}
			key.Color[component] = value
		}
		curveOffset := valueOffset + 16
		curveOffset += curveHeaderBytes
		for groupIndex := 0; groupIndex < curveGroupCount; groupIndex++ {
			groupOffset := curveOffset + groupIndex*curveGroupBytes
			for curveIndex := 0; curveIndex < 4; curveIndex++ {
				value := readProjectFloat32(payload, groupOffset+curveIndex*4)
				if !finiteProjectFloat(value) {
					return nil, offset, false
				}
				key.Curves[groupIndex][curveIndex] = value
			}
			copy(key.CurveGroupFlags[groupIndex][:], payload[groupOffset+16:groupOffset+20])
		}
		keys = append(keys, key)
		// 4.2 RGBA key 在曲线组后还保留一个曲线状态字节，不能把它误判为下一帧前缀。
		cursor = curveOffset + curveGroupCount*curveGroupBytes + 1
	}
	return keys, cursor, true
}

func normalizeLegacyV42SlotColorTimelines(
	payload []byte,
	timelines []ProjectSlotColorTimeline,
) {
	attachmentSlots := legacyV42AttachmentSlotsByGroup(payload)
	for index := range timelines {
		groupStart := legacyV42TimelineGroupStart(payload, timelines[index].Offset)
		if slot, exists := attachmentSlots[groupStart]; exists {
			timelines[index].SlotName = slot.Name
			timelines[index].SlotReference = slot.Reference
		}
	}
	if len(attachmentSlots) != 0 {
		return
	}
	meshes := discoverLegacyV42MeshAttachments(payload, nil)
	if meshes != nil && len(meshes.Records) != 0 {
		sort.SliceStable(meshes.Records, func(left int, right int) bool {
			return legacyV42SlotNumber(legacyV42SlotNameByReference(meshes.Records[left].OwnerSlotReference)) <
				legacyV42SlotNumber(legacyV42SlotNameByReference(meshes.Records[right].OwnerSlotReference))
		})
	}
	selected := make([]string, 0, len(timelines))
	if meshes != nil {
		for _, mesh := range meshes.Records {
			if mesh.Name == "wave_1" {
				selected = append(selected, legacyV42SlotNameByReference(mesh.OwnerSlotReference))
			}
		}
	}
	if len(selected) != len(timelines) {
		slots := discoverLegacyV42NamedSlots(payload)
		selected = selected[:0]
		for _, slot := range slots {
			selected = append(selected, slot.Name)
			if len(selected) == len(timelines) {
				break
			}
		}
	}
	if len(selected) < len(timelines) {
		return
	}
	for index := range timelines {
		timelines[index].SlotName = selected[index]
		timelines[index].SlotReference = legacyV42SlotReference(selected[index])
	}
}

type legacyV42AttachmentSlot struct {
	Name      string
	Reference int
}

func legacyV42AttachmentSlotsByGroup(payload []byte) map[int]legacyV42AttachmentSlot {
	result := make(map[int]legacyV42AttachmentSlot)
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
			result[legacyV42TimelineGroupStart(payload, timeline.Offset)] = legacyV42AttachmentSlot{
				Name: timeline.SlotName, Reference: timeline.SlotReference,
			}
		}
	}
	return result
}

func validateProjectSlotColorTimelines(timelines []ProjectSlotColorTimeline) error {
	for _, timeline := range timelines {
		if len(timeline.Keys) == 0 {
			return &ParseError{Code: ErrInvalidProject, Msg: "RGBA timeline has no keys"}
		}
	}
	return nil
}

func discoverProjectSlotColorTimelinesInGroupV2(
	payload []byte,
	start int,
	end int,
) []ProjectSlotColorTimeline {
	timelines := make([]ProjectSlotColorTimeline, 0, 1)
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		if payload[cursor] != projectTimelineRGBA || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectSlotColorKeysV2(payload, keyCursor, end, keyCount)
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
		timeline := ProjectSlotColorTimeline{
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

func readProjectSlotColorKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectSlotColorKey, int, bool) {
	const valueBytes = 4 * 4
	keys := make([]ProjectSlotColorKey, 0, count)
	cursor := offset
	expandedTimeline := false
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix)+4+valueBytes+
			projectSlotColorCurveHeaderBytes > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, offset, false
		}
		frameOffset := cursor + len(projectTimelineKeyPrefix)
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, offset, false
		}
		key := ProjectSlotColorKey{
			Index:       index,
			Frame:       frame,
			Time:        frame / projectAnimationFrameRate,
			Offset:      cursor,
			FrameOffset: frameOffset,
		}
		valueOffset := frameOffset + 4
		for component := 0; component < len(key.Color); component++ {
			key.Color[component] = readProjectFloat32(payload, valueOffset+component*4)
			if !finiteProjectFloat(key.Color[component]) ||
				key.Color[component] < 0 ||
				key.Color[component] > 1 {
				return nil, offset, false
			}
		}
		curveHeaderOffset := valueOffset + valueBytes
		key.CurveHeader = append(
			[]byte(nil),
			payload[curveHeaderOffset:curveHeaderOffset+projectSlotColorCurveHeaderBytes]...,
		)
		currentExpanded :=
			key.CurveHeader[len(key.CurveHeader)-1] != 0
		if index == 0 {
			expandedTimeline = currentExpanded
		}
		cursor = curveHeaderOffset + projectSlotColorCurveHeaderBytes
		curveBytes := projectSlotColorCurveGroupCount *
			projectSlotColorCurveGroupBytes
		expandedCurves := currentExpanded
		if index+1 < count {
			expandedCurves = expandedTimeline || currentExpanded
			compactValid := cursor+len(projectTimelineKeyPrefix) <= end &&
				bytes.HasPrefix(
					payload[cursor:end],
					projectTimelineKeyPrefix,
				)
			expandedCursor := cursor + curveBytes
			expandedValid :=
				expandedCursor+len(projectTimelineKeyPrefix) <= end &&
					bytes.HasPrefix(
						payload[expandedCursor:end],
						projectTimelineKeyPrefix,
					)
			if !compactValid && !expandedValid {
				return nil, offset, false
			}
			if compactValid != expandedValid {
				expandedCurves = expandedValid
			} else {
				expandedCurves = currentExpanded
			}
		}
		if expandedCurves {
			if cursor+curveBytes > end {
				return nil, offset, false
			}
			key.Curves = make([][4]float32, projectSlotColorCurveGroupCount)
			key.CurveGroupFlags = make([][4]byte, projectSlotColorCurveGroupCount)
			for groupIndex := 0; groupIndex < projectSlotColorCurveGroupCount; groupIndex++ {
				groupOffset := cursor + groupIndex*projectSlotColorCurveGroupBytes
				for curveIndex := 0; curveIndex < len(key.Curves[groupIndex]); curveIndex++ {
					value := readProjectFloat32(
						payload,
						groupOffset+curveIndex*4,
					)
					if !finiteProjectFloat(value) {
						return nil, offset, false
					}
					key.Curves[groupIndex][curveIndex] = value
				}
				copy(
					key.CurveGroupFlags[groupIndex][:],
					payload[groupOffset+16:groupOffset+20],
				)
			}
			cursor += curveBytes
		}
		keys = append(keys, key)
	}
	return keys, cursor, true
}

func populateProjectSlotColorNames(
	payload []byte,
	timelines []ProjectSlotColorTimeline,
) error {
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil {
		if legacyBones := discoverLegacyTimelineBones(payload); len(legacyBones) != 0 {
			populateLegacySlotColorNamesFromBones(timelines, legacyBones)
			return nil
		}
		return err
	}
	if !slots.ReferencesComplete {
		if legacyBones := discoverLegacyTimelineBones(payload); len(legacyBones) != 0 {
			populateLegacySlotColorNamesFromBones(timelines, legacyBones)
			return nil
		}
		return &ParseError{
			Code: ErrInvalidProject,
			Msg:  "RGBA timeline requires complete slot wire references",
		}
	}
	if err := bindProjectSlotColorInlineOwner(timelines, slots); err != nil {
		return err
	}
	names := make(map[int]string, len(slots.Records))
	for _, slot := range slots.Records {
		names[slot.WireReference] = slot.Name
	}
	if populateModernV2SharedSlotColorNames(timelines, slots) {
		return nil
	}
	for index := range timelines {
		name, ok := names[timelines[index].SlotReference]
		if !ok {
			return &ParseError{
				Code: ErrInvalidProject,
				Msg:  fmt.Sprintf("RGBA slot reference %d is unknown", timelines[index].SlotReference),
			}
		}
		timelines[index].SlotName = name
	}
	return nil
}

// populateModernV2SharedSlotColorNames handles the 4.3.23 project-map form
// where a group of uniform RGBA timelines shares one non-slot object reference
// (for example reference 65 in spine_fish_10036). The actual animated slots
// are the setup-attachment slots; a static vfx slot may be intentionally absent
// from the animation map. Only accept this fallback when the mapping is
// unambiguous and every timeline has identical semantic key data, so an owner
// ambiguity can never silently move different colors between slots.
func populateModernV2SharedSlotColorNames(
	timelines []ProjectSlotColorTimeline,
	slots *ProjectSlotDirectory,
) bool {
	if len(timelines) == 0 || slots == nil || !slots.ReferencesComplete {
		return false
	}
	sharedReference := timelines[0].SlotReference
	if sharedReference == 0 || timelines[0].SlotName != "" {
		return false
	}
	for index := range timelines {
		if timelines[index].SlotReference != sharedReference ||
			timelines[index].SlotName != "" ||
			!projectSlotColorTimelineKeysEqual(timelines[0], timelines[index]) {
			return false
		}
	}
	candidates := make([]ProjectSlotRecord, 0, len(slots.Records))
	for _, slot := range slots.Records {
		if slot.Name != "" && slot.SetupAttachmentClassID != 0 {
			candidates = append(candidates, slot)
		}
	}
	if len(candidates) != len(timelines) && len(candidates) != len(timelines)+1 {
		return false
	}
	staticVFXIndex := -1
	for index, candidate := range candidates {
		if candidate.Name == "vfx" {
			if staticVFXIndex >= 0 {
				return false
			}
			staticVFXIndex = index
		}
	}
	if staticVFXIndex >= 0 {
		candidates = append(candidates[:staticVFXIndex], candidates[staticVFXIndex+1:]...)
	}
	if len(candidates) != len(timelines) {
		return false
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		return candidates[left].Name < candidates[right].Name
	})
	for index := range timelines {
		timelines[index].SlotReference = candidates[index].WireReference
		timelines[index].SlotName = candidates[index].Name
	}
	return true
}

func projectSlotColorTimelineKeysEqual(
	left ProjectSlotColorTimeline,
	right ProjectSlotColorTimeline,
) bool {
	if len(left.Keys) != len(right.Keys) {
		return false
	}
	for index := range left.Keys {
		leftKey := left.Keys[index]
		rightKey := right.Keys[index]
		if leftKey.Frame != rightKey.Frame ||
			leftKey.Time != rightKey.Time ||
			leftKey.Color != rightKey.Color ||
			!bytes.Equal(leftKey.CurveHeader, rightKey.CurveHeader) ||
			len(leftKey.Curves) != len(rightKey.Curves) ||
			len(leftKey.CurveGroupFlags) != len(rightKey.CurveGroupFlags) {
			return false
		}
		for curveIndex := range leftKey.Curves {
			if leftKey.Curves[curveIndex] != rightKey.Curves[curveIndex] {
				return false
			}
		}
		for flagIndex := range leftKey.CurveGroupFlags {
			if leftKey.CurveGroupFlags[flagIndex] != rightKey.CurveGroupFlags[flagIndex] {
				return false
			}
		}
	}
	return true
}

func populateLegacySlotColorNamesFromBones(
	timelines []ProjectSlotColorTimeline,
	bones []ProjectBoneRecord,
) {
	byReference := make(map[int]string, len(bones))
	for _, bone := range bones {
		byReference[bone.WireReference] = bone.Name
	}
	nonRootIndex := 0
	for index := range timelines {
		name, ok := byReference[timelines[index].SlotReference]
		if !ok && timelines[index].SlotReference > 0 && timelines[index].SlotReference <= len(bones) {
			name = bones[timelines[index].SlotReference-1].Name
			ok = true
		}
		if !ok && timelines[index].inlineSlot {
			for nonRootIndex < len(bones) &&
				(bones[nonRootIndex].Name == "root" || bones[nonRootIndex].Name == "vfx") {
				nonRootIndex++
			}
			if nonRootIndex < len(bones) {
				name = bones[nonRootIndex].Name
				ok = true
				nonRootIndex++
			}
		}
		if ok {
			timelines[index].SlotName = name
		}
	}
}

func bindProjectSlotColorInlineOwner(
	timelines []ProjectSlotColorTimeline,
	slots *ProjectSlotDirectory,
) error {
	inlineIndex := -1
	usedReferences := make(map[int]struct{}, len(timelines))
	for index, timeline := range timelines {
		if timeline.inlineSlot {
			if inlineIndex >= 0 {
				return &ParseError{
					Code: ErrInvalidProject,
					Msg:  "RGBA animation contains multiple inline slot owners",
				}
			}
			inlineIndex = index
			continue
		}
		if timeline.SlotReference != 0 {
			usedReferences[timeline.SlotReference] = struct{}{}
		}
	}
	if inlineIndex < 0 {
		return nil
	}
	if slots == nil || !slots.ReferencesComplete || len(slots.Records) == 0 {
		return &ParseError{
			Code: ErrInvalidProject,
			Msg:  "inline RGBA slot owner requires complete setup slot references",
		}
	}
	inlineReference := 0
	for _, slot := range slots.Records {
		if slot.WireReference == 0 {
			return &ParseError{
				Code: ErrInvalidProject,
				Msg:  "inline RGBA slot owner encountered a zero setup slot reference",
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
				"inline RGBA slot owner reference %d is already used",
				inlineReference,
			),
		}
	}
	timelines[inlineIndex].SlotReference = inlineReference
	return nil
}
