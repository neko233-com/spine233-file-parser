package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
)

const projectAnimationFrameRate = 30

var (
	projectBoneTimelineGroupPrefix = []byte{0x13, 0x01, 0x05, 0x00}
	projectBoneTimelineMapPrefix   = []byte{0x02, 0x0f, 0x01}
	projectBoneTimelineGroupV2     = []byte{0x04, 0x01, 0x13, 0x01, 0x04, 0x07}
	projectTimelinePrefix          = []byte{0x84, 0x01, 0x01}
	projectTimelineKeyPrefix       = []byte{0x85, 0x01, 0x01}
)

// ProjectRotateKey is one directly decoded rotate key in a modern .spine
// project. Frame is the editor frame number; Time is Frame / 30.
type ProjectRotateKey struct {
	Index       int        `json:"index"`
	Frame       float32    `json:"frame"`
	Time        float32    `json:"time"`
	Value       float32    `json:"value"`
	Offset      int        `json:"offset"`
	ValueOffset int        `json:"valueOffset"`
	Curve       [4]float32 `json:"curve"`
	Flags       [5]byte    `json:"flags"`
}

// ProjectRotateTimeline identifies a rotate timeline by the project's stable
// Kryo bone reference. Bone-name decoding is intentionally not guessed.
type ProjectRotateTimeline struct {
	BoneReference     int                `json:"boneReference"`
	TimelineReference int                `json:"timelineReference"`
	KeyReference      int                `json:"keyReference"`
	Offset            int                `json:"offset"`
	Keys              []ProjectRotateKey `json:"keys"`
}

// ProjectRotateTimelineDirectory contains all rotate timelines in one
// top-level animation record.
type ProjectRotateTimelineDirectory struct {
	Animation   string                  `json:"animation"`
	RegionStart int                     `json:"regionStart"`
	RegionEnd   int                     `json:"regionEnd"`
	FrameRate   int                     `json:"frameRate"`
	Timelines   []ProjectRotateTimeline `json:"timelines"`
}

// DiscoverProjectRotateTimelines decodes modern Spine Pro rotate timelines
// without launching Spine Editor.
func DiscoverProjectRotateTimelines(
	payload []byte,
	animation string,
) (*ProjectRotateTimelineDirectory, error) {
	transforms, err := DiscoverProjectTransformTimelines(payload, animation)
	if err != nil {
		return nil, err
	}
	timelines := make([]ProjectRotateTimeline, 0)
	for _, transform := range transforms.Timelines {
		if transform.Type != ProjectTimelineRotate {
			continue
		}
		keys := make([]ProjectRotateKey, 0, len(transform.Keys))
		for _, key := range transform.Keys {
			var flags [5]byte
			copy(flags[:], key.CurveFlags)
			keys = append(keys, ProjectRotateKey{
				Index:       key.Index,
				Frame:       key.Frame,
				Time:        key.Time,
				Value:       key.Values[0],
				Offset:      key.Offset,
				ValueOffset: key.ValueOffsets[0],
				Curve:       key.Curves[0],
				Flags:       flags,
			})
		}
		timelines = append(timelines, ProjectRotateTimeline{
			BoneReference:     transform.BoneReference,
			TimelineReference: transform.TimelineReference,
			KeyReference:      transform.KeyReference,
			Offset:            transform.Offset,
			Keys:              keys,
		})
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no supported rotate timelines", animation),
		}
	}
	return &ProjectRotateTimelineDirectory{
		Animation:   animation,
		RegionStart: transforms.RegionStart,
		RegionEnd:   transforms.RegionEnd,
		FrameRate:   transforms.FrameRate,
		Timelines:   timelines,
	}, nil
}

// ProjectRotateValueEdit changes one key selected by bone reference and index.
// From is checked exactly so stale agent plans fail closed.
type ProjectRotateValueEdit struct {
	BoneReference int     `json:"boneReference"`
	KeyIndex      int     `json:"keyIndex"`
	From          float32 `json:"from"`
	To            float32 `json:"to"`
}

// ProjectRotatePatch controls direct semantic rotate-key edits and optional
// animation renaming.
type ProjectRotatePatch struct {
	Animation       string                   `json:"animation"`
	TargetAnimation string                   `json:"targetAnimation,omitempty"`
	Edits           []ProjectRotateValueEdit `json:"edits"`
}

// ProjectRotateValueChange reports one semantic key edit.
type ProjectRotateValueChange struct {
	BoneReference int     `json:"boneReference"`
	KeyIndex      int     `json:"keyIndex"`
	Frame         float32 `json:"frame"`
	From          float32 `json:"from"`
	To            float32 `json:"to"`
	Offset        int     `json:"offset"`
}

// ProjectRotatePatchReport is safe to inspect before serialization.
type ProjectRotatePatchReport struct {
	Animation       string                     `json:"animation"`
	TargetAnimation string                     `json:"targetAnimation,omitempty"`
	RegionStart     int                        `json:"regionStart"`
	RegionEnd       int                        `json:"regionEnd"`
	Changes         []ProjectRotateValueChange `json:"changes"`
}

// PatchProjectRotateValues clones a project and modifies explicitly selected
// rotate keys. It never mutates document.
func PatchProjectRotateValues(
	document *ProjectDocument,
	patch ProjectRotatePatch,
) (*ProjectDocument, ProjectRotatePatchReport, error) {
	if document == nil || len(document.Payload) == 0 {
		return nil, ProjectRotatePatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	if len(patch.Edits) == 0 {
		return nil, ProjectRotatePatchReport{},
			&ParseError{Code: ErrInvalidInput, Msg: "at least one rotate edit is required"}
	}
	directory, err := DiscoverProjectRotateTimelines(document.Payload, patch.Animation)
	if err != nil {
		return nil, ProjectRotatePatchReport{}, err
	}
	timelineByBone := make(map[int][]ProjectRotateTimeline)
	for _, timeline := range directory.Timelines {
		timelineByBone[timeline.BoneReference] = append(
			timelineByBone[timeline.BoneReference],
			timeline,
		)
	}

	payload := append([]byte(nil), document.Payload...)
	report := ProjectRotatePatchReport{
		Animation:       patch.Animation,
		TargetAnimation: patch.TargetAnimation,
		RegionStart:     directory.RegionStart,
		RegionEnd:       directory.RegionEnd,
		Changes:         make([]ProjectRotateValueChange, 0, len(patch.Edits)),
	}
	seen := make(map[[2]int]struct{}, len(patch.Edits))
	for editIndex, edit := range patch.Edits {
		if math.IsNaN(float64(edit.From)) || math.IsInf(float64(edit.From), 0) ||
			math.IsNaN(float64(edit.To)) || math.IsInf(float64(edit.To), 0) {
			return nil, ProjectRotatePatchReport{},
				fmt.Errorf("edit %d: rotate values must be finite", editIndex)
		}
		if math.Float32bits(edit.From) == math.Float32bits(edit.To) {
			return nil, ProjectRotatePatchReport{},
				fmt.Errorf("edit %d: from and to must differ", editIndex)
		}
		key := [2]int{edit.BoneReference, edit.KeyIndex}
		if _, exists := seen[key]; exists {
			return nil, ProjectRotatePatchReport{},
				fmt.Errorf("edit %d: duplicate boneReference/keyIndex", editIndex)
		}
		seen[key] = struct{}{}
		matches := timelineByBone[edit.BoneReference]
		if len(matches) == 0 {
			return nil, ProjectRotatePatchReport{}, fmt.Errorf(
				"edit %d: rotate timeline not found for boneReference %d",
				editIndex,
				edit.BoneReference,
			)
		}
		if len(matches) != 1 {
			return nil, ProjectRotatePatchReport{}, fmt.Errorf(
				"edit %d: boneReference %d matched %d rotate timelines",
				editIndex,
				edit.BoneReference,
				len(matches),
			)
		}
		timeline := matches[0]
		if edit.KeyIndex < 0 || edit.KeyIndex >= len(timeline.Keys) {
			return nil, ProjectRotatePatchReport{}, fmt.Errorf(
				"edit %d: keyIndex %d is outside [0,%d)",
				editIndex,
				edit.KeyIndex,
				len(timeline.Keys),
			)
		}
		selected := timeline.Keys[edit.KeyIndex]
		if math.Float32bits(selected.Value) != math.Float32bits(edit.From) {
			return nil, ProjectRotatePatchReport{}, fmt.Errorf(
				"edit %d: key value is %v, expected %v",
				editIndex,
				selected.Value,
				edit.From,
			)
		}
		binary.BigEndian.PutUint32(
			payload[selected.ValueOffset:selected.ValueOffset+4],
			math.Float32bits(edit.To),
		)
		report.Changes = append(report.Changes, ProjectRotateValueChange{
			BoneReference: edit.BoneReference,
			KeyIndex:      edit.KeyIndex,
			Frame:         selected.Frame,
			From:          edit.From,
			To:            edit.To,
			Offset:        selected.ValueOffset,
		})
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
			return nil, ProjectRotatePatchReport{}, err
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

type projectBoneTimelineGroup struct {
	Offset        int
	BoneReference int
	V2            bool
}

func uniqueProjectAnimationRecord(
	payload []byte,
	animation string,
) (ProjectAnimationRecord, error) {
	directory, err := DiscoverProjectAnimations(payload)
	if err != nil {
		// 旧 4.2/4.3 保存布局没有现代动画 Map 头，但动画记录本身
		// 仍可由对象边界恢复；时间线解码器统一复用这份记录目录。
		families := []string{"spine-4.2-project", legacyProject43Family(payload)}
		for _, family := range families {
			bones := discoverLegacyProjectBones(payload, family)
			if len(bones) == 0 {
				continue
			}
			legacyDirectory := discoverLegacyProjectAnimations(
				payload,
				bones,
				"legacy",
			)
			if len(legacyDirectory.Records) != 0 {
				directory = legacyDirectory
				break
			}
		}
		if directory == nil {
			return ProjectAnimationRecord{}, err
		}
	}
	// 现代骨骼表无法解码时，优先使用旧 4.2/4.3 的动画对象边界。
	// 旧 payload 偶尔会误命中现代动画 Map 头；若先取现代记录，
	// Offset 可能落在 Map 头部，后续所有时间线都会被判成空。
	if _, boneErr := DiscoverProjectBones(payload); boneErr != nil {
		families := []string{"spine-4.2-project", legacyProject43Family(payload)}
		for _, family := range families {
			bones := discoverLegacyProjectBones(payload, family)
			if len(bones) == 0 {
				continue
			}
			legacyDirectory := discoverLegacyProjectAnimations(payload, bones, "legacy")
			for _, record := range legacyDirectory.Records {
				if record.Name == animation {
					record.Offset, record.EndOffset = normalizeLegacyV43AnimationRange(
						payload,
						record,
					)
					return record, nil
				}
			}
		}
	}
	matches := make([]ProjectAnimationRecord, 0, 1)
	for _, record := range directory.Records {
		if record.Name == animation {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		// 某些旧 4.3 payload 会误命中一个现代动画 Map 头，
		// 但目标动画实际仍使用旧版 01 01 <name> 入口；
		// 目标名未命中时再尝试旧布局，避免时间线被错误区间吞掉。
		families := []string{"spine-4.2-project", legacyProject43Family(payload)}
		for _, family := range families {
			bones := discoverLegacyProjectBones(payload, family)
			if len(bones) == 0 {
				continue
			}
			legacyDirectory := discoverLegacyProjectAnimations(payload, bones, "legacy")
			for _, record := range legacyDirectory.Records {
				if record.Name == animation {
					matches = append(matches, record)
				}
			}
			if len(matches) != 0 {
				break
			}
		}
	}
	if len(matches) == 0 {
		return ProjectAnimationRecord{}, fmt.Errorf("animation not found: %s", animation)
	}
	if len(matches) != 1 {
		return ProjectAnimationRecord{}, fmt.Errorf(
			"animation name is ambiguous: %s matched %d records",
			animation,
			len(matches),
		)
	}
	matches[0].Offset, matches[0].EndOffset = normalizeLegacyV43AnimationRange(
		payload,
		matches[0],
	)
	return matches[0], nil
}

func discoverProjectBoneTimelineGroups(
	payload []byte,
	start int,
	end int,
) []projectBoneTimelineGroup {
	groups := make([]projectBoneTimelineGroup, 0)
	for offset := start; offset+len(projectBoneTimelineGroupPrefix) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectBoneTimelineGroupPrefix) {
			continue
		}
		_, cursor, ok := readPositiveVarint(
			payload,
			offset+len(projectBoneTimelineGroupPrefix),
		)
		if !ok || cursor >= end || payload[cursor] != 0x01 {
			continue
		}
		boneReference, cursor, ok := readPositiveVarint(payload, cursor+1)
		if !ok || boneReference < 1 ||
			cursor+len(projectBoneTimelineMapPrefix) > end ||
			!bytes.Equal(
				payload[cursor:cursor+len(projectBoneTimelineMapPrefix)],
				projectBoneTimelineMapPrefix,
			) {
			continue
		}
		groups = append(groups, projectBoneTimelineGroup{
			Offset:        offset,
			BoneReference: boneReference,
		})
		offset = cursor + len(projectBoneTimelineMapPrefix) - 1
	}
	v2Groups := discoverProjectBoneTimelineGroupsV2(payload, start, end)
	if len(v2Groups) == 0 {
		v2Groups = discoverProjectBoneTimelineGroupsCompactV2(payload, start, end)
	}
	if len(v2Groups) != 0 {
		groups = append(groups, v2Groups...)
	}
	v42Groups := discoverProjectBoneTimelineGroupsV42(payload, start, end)
	if len(v42Groups) != 0 {
		groups = append(groups, v42Groups...)
	}
	for left := 0; left < len(groups); left++ {
		for right := left + 1; right < len(groups); right++ {
			if groups[right].Offset < groups[left].Offset {
				groups[left], groups[right] = groups[right], groups[left]
			}
		}
	}
	return groups
}

var projectBoneTimelineGroupV42 = []byte{0x13, 0x01, 0x05, 0x02, 0x0f, 0x01}

func discoverProjectBoneTimelineGroupsV42(
	payload []byte,
	start int,
	end int,
) []projectBoneTimelineGroup {
	groups := make([]projectBoneTimelineGroup, 0)
	for offset := start; offset+len(projectBoneTimelineGroupV42) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectBoneTimelineGroupV42) {
			continue
		}
		cursor := offset + len(projectBoneTimelineGroupV42)
		if cursor >= end || payload[cursor] == 0 {
			continue
		}
		_, timelineStart, ok := readPositiveVarint(payload, cursor)
		if !ok || timelineStart+len(projectTimelinePrefix) > end ||
			!bytes.HasPrefix(payload[timelineStart:end], projectTimelinePrefix) {
			continue
		}
		boneReference := findProjectV42ReferenceBeforeGroup(payload, offset, start)
		if boneReference == 0 {
			continue
		}
		groups = append(groups, projectBoneTimelineGroup{
			Offset:        offset,
			BoneReference: boneReference,
		})
		offset = timelineStart + len(projectTimelinePrefix) - 1
	}
	return groups
}

func findProjectV42ReferenceBeforeGroup(payload []byte, group int, start int) int {
	searchStart := group - 8
	if searchStart < start {
		searchStart = start
	}
	for offset := searchStart; offset < group; offset++ {
		if payload[offset] != 0x01 {
			continue
		}
		value, next, ok := readPositiveVarint(payload, offset+1)
		if !ok || value == 0 {
			continue
		}
		if next == group || next+2 == group {
			return value
		}
	}
	return 0
}

func discoverProjectBoneTimelineGroupsCompactV2(
	payload []byte,
	start int,
	end int,
) []projectBoneTimelineGroup {
	prefix := []byte{0x13, 0x01, 0x04, 0x07}
	wrappers := make([]int, 0)
	for wrapper := start; wrapper+len(prefix) < end; wrapper++ {
		if !bytes.HasPrefix(payload[wrapper:end], prefix) {
			continue
		}
		wrappers = append(wrappers, wrapper)
		wrapper += len(prefix) - 1
	}
	groups := make([]projectBoneTimelineGroup, 0, len(wrappers))
	for index, wrapper := range wrappers {
		timelineOffset := -1
		searchEnd := wrapper + 24
		if searchEnd > end {
			searchEnd = end
		}
		for offset := wrapper + len(prefix); offset+len(projectTimelinePrefix) <= searchEnd; offset++ {
			if bytes.HasPrefix(payload[offset:searchEnd], projectTimelinePrefix) {
				timelineOffset = offset
				break
			}
		}
		if timelineOffset < 0 {
			continue
		}
		typeOffset := timelineOffset + len(projectTimelinePrefix)
		if typeOffset+1 >= end || payload[typeOffset] > 3 ||
			payload[typeOffset+1] != 0x01 {
			continue
		}
		reference := 0
		if index+1 < len(wrappers) {
			reference = findProjectCompactV2ReferenceBeforeWrapper(
				payload,
				wrappers[index+1],
			)
		} else {
			reference = findProjectCompactV2GroupTerminalReference(
				payload,
				timelineOffset,
				end,
			)
		}
		if reference == 0 {
			return nil
		}
		groups = append(groups, projectBoneTimelineGroup{
			Offset:        timelineOffset,
			BoneReference: reference,
			V2:            true,
		})
	}
	return groups
}

func findProjectCompactV2ReferenceBeforeWrapper(
	payload []byte,
	wrapper int,
) int {
	start := wrapper - 8
	if start < 0 {
		start = 0
	}
	for offset := start; offset < wrapper; offset++ {
		if payload[offset] != 0x01 {
			continue
		}
		value, next, ok := readPositiveVarint(payload, offset+1)
		if ok && value > 0 && next+2 == wrapper &&
			projectCompactV2OwnerSuffix(payload[next], payload[next+1]) {
			return value
		}
	}
	return 0
}

func findProjectCompactV2GroupTerminalReference(
	payload []byte,
	start int,
	end int,
) int {
	lastTimeline := -1
	for offset := start; offset+len(projectTimelinePrefix) <= end; offset++ {
		if bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			lastTimeline = offset
		}
	}
	if lastTimeline < 0 {
		return 0
	}
	for offset := lastTimeline + len(projectTimelinePrefix); offset < end; offset++ {
		if payload[offset] != 0x01 {
			continue
		}
		reference, next, ok := readPositiveVarint(payload, offset+1)
		if !ok || reference < 1 || next+2 > end ||
			!projectCompactV2OwnerSuffix(payload[next], payload[next+1]) {
			continue
		}
		return reference
	}
	return 0
}

func projectCompactV2OwnerSuffix(first byte, second byte) bool {
	return first == 0x04 && (second == 0x00 || second == 0x01)
}

func discoverProjectBoneTimelineGroupsV2(
	payload []byte,
	start int,
	end int,
) []projectBoneTimelineGroup {
	wrappers := make([]int, 0)
	for offset := start; offset+len(projectBoneTimelineGroupV2) <= end; offset++ {
		if isProjectBoneTimelineGroupV2(payload, offset, end) {
			wrappers = append(wrappers, offset)
			offset += len(projectBoneTimelineGroupV2) - 1
		}
	}
	groupStarts := make([]int, 0, len(wrappers)+1)
	groupStarts = append(groupStarts, start)
	for _, wrapper := range wrappers {
		groupStarts = append(groupStarts, wrapper+2)
	}
	groups := make([]projectBoneTimelineGroup, 0, len(groupStarts))
	for index, groupStart := range groupStarts {
		groupEnd := end
		reference := 0
		if index < len(wrappers) {
			groupEnd = wrappers[index]
			reference = findProjectV2ReferenceBeforeWrapper(
				payload,
				wrappers[index],
			)
		} else {
			reference = findProjectV2GroupTerminalReference(
				payload,
				groupStart,
				groupEnd,
			)
		}
		if reference == 0 {
			return nil
		}
		groups = append(groups, projectBoneTimelineGroup{
			Offset:        groupStart,
			BoneReference: reference,
			V2:            true,
		})
	}
	return groups
}

func isProjectBoneTimelineGroupV2(
	payload []byte,
	offset int,
	end int,
) bool {
	return offset >= 0 &&
		offset+len(projectBoneTimelineGroupV2) <= end &&
		end <= len(payload) &&
		payload[offset] == 0x04 &&
		(payload[offset+1] == 0x00 || payload[offset+1] == 0x01) &&
		bytes.Equal(
			payload[offset+2:offset+len(projectBoneTimelineGroupV2)],
			projectBoneTimelineGroupV2[2:],
		)
}

func findProjectV2ReferenceBeforeWrapper(
	payload []byte,
	wrapper int,
) int {
	start := wrapper - 6
	if start < 0 {
		start = 0
	}
	for offset := start; offset+1 < wrapper; offset++ {
		if payload[offset] != 0x01 {
			continue
		}
		value, next, ok := readPositiveVarint(payload, offset+1)
		if !ok || next != wrapper {
			continue
		}
		if value > 0 {
			return value
		}
	}
	return 0
}

func findProjectV2GroupTerminalReference(
	payload []byte,
	start int,
	end int,
) int {
	lastTimeline := -1
	for offset := start; offset+len(projectTimelinePrefix) <= end; offset++ {
		if bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			lastTimeline = offset
		}
	}
	if lastTimeline < 0 {
		return 0
	}
	reference := 0
	for offset := lastTimeline + len(projectTimelinePrefix); offset < end; offset++ {
		if payload[offset] != 0x01 {
			continue
		}
		value, next, ok := readPositiveVarint(payload, offset+1)
		if !ok || next+2 > end ||
			!projectCompactV2OwnerSuffix(payload[next], payload[next+1]) {
			continue
		}
		if value > 0 {
			reference = value
		}
	}
	return reference
}

func renameProjectAnimationRecord(
	payload []byte,
	start int,
	source string,
	target string,
) ([]byte, error) {
	existing, err := projectStringOffsets(payload, target)
	if err != nil {
		return nil, fmt.Errorf("targetAnimation: %w", err)
	}
	if len(existing) != 0 {
		return nil, fmt.Errorf("target animation already exists: %s", target)
	}
	sourceName, err := encodeProjectString(source)
	if err != nil {
		return nil, err
	}
	targetName, err := encodeProjectString(target)
	if err != nil {
		return nil, err
	}
	if start < 0 || start+len(sourceName) > len(payload) ||
		!bytes.Equal(payload[start:start+len(sourceName)], sourceName) {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "animation record does not start with its encoded name",
		}
	}
	renamed := make([]byte, 0, len(payload)+len(targetName)-len(sourceName))
	renamed = append(renamed, payload[:start]...)
	renamed = append(renamed, targetName...)
	renamed = append(renamed, payload[start+len(sourceName):]...)
	return renamed, nil
}
