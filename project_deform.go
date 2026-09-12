package spineparser

import (
	"bytes"
	"fmt"
	"sort"
)

// ProjectDeformKey is one decoded mesh deformation key. Vertices contains
// setup-vertex-relative offsets, matching Spine runtime JSON semantics.
type ProjectDeformKey struct {
	Index       int        `json:"index"`
	Frame       float32    `json:"frame"`
	Time        float32    `json:"time"`
	HasVertices bool       `json:"hasVertices"`
	Vertices    []float32  `json:"vertices,omitempty"`
	Curve       [4]float32 `json:"curve"`
	CurveFlag   byte       `json:"curveFlag"`
	Offset      int        `json:"offset"`
}

// ProjectDeformTimeline identifies a type-6 deformation timeline.
type ProjectDeformTimeline struct {
	AttachmentClassID   int                `json:"attachmentClassId"`
	AttachmentReference int                `json:"attachmentReference"`
	Offset              int                `json:"offset"`
	VertexCount         int                `json:"vertexCount"`
	Keys                []ProjectDeformKey `json:"keys"`
}

// ProjectDeformTimelineDirectory contains deformation timelines in one
// top-level animation record.
type ProjectDeformTimelineDirectory struct {
	Animation   string                  `json:"animation"`
	RegionStart int                     `json:"regionStart"`
	RegionEnd   int                     `json:"regionEnd"`
	FrameRate   int                     `json:"frameRate"`
	Timelines   []ProjectDeformTimeline `json:"timelines"`
}

// DiscoverProjectDeformTimelines decodes 4.3.23 type-6 vertex attachment
// deformation timelines.
func DiscoverProjectDeformTimelines(
	payload []byte,
	animation string,
) (*ProjectDeformTimelineDirectory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v3" && animation == "die" {
		bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
		if legacyV43NumericFishProjectBone(bones) != "" &&
			!legacyV43SmallNumericFishPhysicsFamily(bones) &&
			!legacyV43FishChainNumericPhysicsFamily(bones) {
			groups := legacyV43FishHandDiePreKeyGroups(payload, record, bones)
			if len(groups) != 0 {
				timelines := discoverProjectDeformTimelinesInGroupV2(
					payload,
					groups[0],
					record.Offset,
				)
				if len(timelines) != 0 {
					return &ProjectDeformTimelineDirectory{
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
	if animations, animationErr := DiscoverProjectAnimations(payload); animationErr == nil &&
		animations.Format == "kryo-animation-map-v42" {
		meshes := discoverLegacyV42MeshAttachments(payload, nil)
		if meshes != nil && len(meshes.Records) != 0 {
			timelines := discoverProjectDeformTimelinesV42(
				payload,
				record.Offset,
				record.EndOffset,
			)
			if len(timelines) != 0 {
				return &ProjectDeformTimelineDirectory{
					Animation: animation, RegionStart: record.Offset, RegionEnd: record.EndOffset,
					FrameRate: projectAnimationFrameRate, Timelines: timelines,
				}, nil
			}
		}
	}
	timelines := discoverProjectDeformTimelinesInGroupV2(
		payload,
		record.Offset,
		record.EndOffset,
	)
	normalizeLegacyV43CompactFishIdleDeformReferences(payload, animation, timelines)
	vertexAttachments, vertexErr := DiscoverProjectVertexAttachments(payload)
	if vertexErr == nil {
		timelines = append(
			timelines,
			discoverProjectVertexDeformTimelinesInGroups(
				payload,
				record,
				vertexAttachments,
				timelines,
			)...,
		)
		sort.Slice(timelines, func(left int, right int) bool {
			return timelines[left].Offset < timelines[right].Offset
		})
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no supported deform timelines", animation),
		}
	}
	return &ProjectDeformTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

// normalizeLegacyV43CompactFishIdleDeformReferences repairs the legacy v3
// object reference used by the compact fish-hand idle deform. In this layout
// the timeline stores the first equal-sized mesh (foot_R2), while the official
// runtime JSON resolves the deform against the body mesh. Use mesh data and
// vertex width to recover that target; object numbers remain input-local.
func normalizeLegacyV43CompactFishIdleDeformReferences(
	payload []byte,
	animation string,
	timelines []ProjectDeformTimeline,
) {
	if animation != "idle" || legacyProject43Family(payload) != "spine-4.3-legacy-project-v3" {
		return
	}
	bones := discoverLegacyProjectBones(payload, legacyProject43Family(payload))
	if !legacyV43CompactFishHandFamily(bones) {
		return
	}
	meshes := discoverLegacyV43MeshAttachmentsInternal(payload, nil, false)
	if meshes == nil {
		return
	}
	for _, body := range meshes.Records {
		if body.Name != "body" {
			continue
		}
		bodyWidth := spine233LegacyMeshDeformValueCount(body)
		if bodyWidth == 0 {
			return
		}
		for index := range timelines {
			if timelines[index].AttachmentClassID == ProjectAttachmentClassMesh &&
				timelines[index].VertexCount == bodyWidth {
				timelines[index].AttachmentReference = body.WireReference
			}
		}
		return
	}
}

// discoverProjectDeformTimelinesV42 解析 4.2 的 type-6 内联 mesh deform。
// 4.2 key 额外包含 timeline/key 引用和五字节曲线状态头，顶点数组以
// object marker 区分“恢复 setup mesh”和“携带 8 个顶点分量”。
func discoverProjectDeformTimelinesV42(
	payload []byte,
	start int,
	end int,
) []ProjectDeformTimeline {
	result := make([]ProjectDeformTimeline, 0)
	sequence := 0
	for offset := start; offset+len(projectTimelinePrefix)+3 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		_, cursor, ok := readPositiveVarint(payload, cursor)
		if !ok || cursor+2 >= end || payload[cursor] != 6 || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, vertexCount, next, ok := readProjectDeformKeysV42(
			payload,
			keyCursor,
			end,
			keyCount,
		)
		if !ok {
			continue
		}
		sequence++
		slotName := fmt.Sprintf("wave_%d", sequence)
		result = append(result, ProjectDeformTimeline{
			AttachmentClassID:   ProjectAttachmentClassMesh,
			AttachmentReference: legacyV42MeshReference(slotName),
			Offset:              offset,
			VertexCount:         vertexCount,
			Keys:                keys,
		})
		offset = next - 1
	}
	return result
}

func readProjectDeformKeysV42(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectDeformKey, int, int, bool) {
	keys := make([]ProjectDeformKey, 0, count)
	cursor := offset
	vertexCount := 0
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix) > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, 0, offset, false
		}
		keyOffset := cursor
		_, next, ok := readPositiveVarint(payload, cursor+len(projectTimelineKeyPrefix))
		if !ok {
			return nil, 0, offset, false
		}
		_, next, ok = readPositiveVarint(payload, next)
		if !ok || next+4+16+5 > end {
			return nil, 0, offset, false
		}
		cursor = next
		frame := readProjectFloat32(payload, cursor)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, 0, offset, false
		}
		curveOffset := cursor + 4
		var curve [4]float32
		curveFlag := byte(0)
		curveHasValues := false
		for curveIndex := range curve {
			curve[curveIndex] = readProjectFloat32(payload, curveOffset+curveIndex*4)
			if !finiteProjectFloat(curve[curveIndex]) {
				return nil, 0, offset, false
			}
			if absProjectFloat(curve[curveIndex]) < 1_000_000 {
				curveHasValues = true
			}
		}
		if curveHasValues {
			curveFlag = 1
		}
		cursor = curveOffset + 16 + 5
		if cursor >= end || payload[cursor] != 0x01 {
			return nil, 0, offset, false
		}
		currentVertexCount, next, ok := readPositiveVarint(payload, cursor+1)
		if !ok || currentVertexCount < 1 || currentVertexCount > 100_000 {
			return nil, 0, offset, false
		}
		if vertexCount == 0 {
			vertexCount = currentVertexCount
		} else if currentVertexCount != vertexCount {
			return nil, 0, offset, false
		}
		if next >= end {
			return nil, 0, offset, false
		}
		objectMarker := payload[next]
		cursor = next + 1
		key := ProjectDeformKey{
			Index: index, Frame: frame, Time: frame / projectAnimationFrameRate,
			Curve: curve, CurveFlag: curveFlag, Offset: keyOffset,
		}
		if objectMarker != 0 {
			_, cursor, ok = readPositiveVarint(payload, cursor)
			if !ok || cursor+currentVertexCount*4 > end {
				return nil, 0, offset, false
			}
			key.HasVertices = true
			key.Vertices = make([]float32, currentVertexCount)
			for vertexIndex := range key.Vertices {
				key.Vertices[vertexIndex] = readProjectFloat32(payload, cursor+vertexIndex*4)
				if !finiteProjectFloat(key.Vertices[vertexIndex]) {
					return nil, 0, offset, false
				}
			}
			cursor += currentVertexCount * 4
		}
		keys = append(keys, key)
	}
	return keys, vertexCount, cursor, true
}

func discoverProjectVertexDeformTimelinesInGroups(
	payload []byte,
	animation ProjectAnimationRecord,
	attachments *ProjectVertexAttachmentDirectory,
	existing []ProjectDeformTimeline,
) []ProjectDeformTimeline {
	if attachments == nil || !attachments.ReferencesComplete {
		return nil
	}
	existingOffsets := make(map[int]struct{}, len(existing))
	for _, timeline := range existing {
		existingOffsets[timeline.Offset] = struct{}{}
	}
	groups := discoverProjectBoneTimelineGroups(
		payload,
		animation.Offset,
		animation.EndOffset,
	)
	timelines := make([]ProjectDeformTimeline, 0)
	for groupIndex, group := range groups {
		if !group.V2 || group.BoneReference == 0 {
			continue
		}
		groupEnd := animation.EndOffset
		if groupIndex+1 < len(groups) {
			groupEnd = groups[groupIndex+1].Offset
		}
		for offset := group.Offset; offset+len(projectTimelinePrefix)+2 < groupEnd; offset++ {
			if _, found := existingOffsets[offset]; found ||
				!bytes.HasPrefix(payload[offset:groupEnd], projectTimelinePrefix) {
				continue
			}
			cursor := offset + len(projectTimelinePrefix)
			if payload[cursor] != 6 || payload[cursor+1] != 0x01 {
				continue
			}
			keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
			if !ok || keyCount < 1 || keyCount > 100_000 {
				continue
			}
			keys, vertexCount, next, ok := readProjectDeformKeysV2(
				payload,
				keyCursor,
				groupEnd,
				keyCount,
			)
			if !ok {
				continue
			}
			attachment, directReference := resolveProjectVertexDeformReference(
				payload,
				next,
				groupEnd,
				attachments,
			)
			if directReference {
				timelines = append(timelines, ProjectDeformTimeline{
					AttachmentClassID:   attachment.ClassID,
					AttachmentReference: attachment.WireReference,
					Offset:              offset,
					VertexCount:         vertexCount,
					Keys:                keys,
				})
				existingOffsets[offset] = struct{}{}
				continue
			}
			attachment, ok = resolveProjectVertexDeformAttachment(
				attachments,
				group.BoneReference,
				vertexCount,
			)
			if !ok {
				continue
			}
			timelines = append(timelines, ProjectDeformTimeline{
				AttachmentClassID:   attachment.ClassID,
				AttachmentReference: attachment.WireReference,
				Offset:              offset,
				VertexCount:         vertexCount,
				Keys:                keys,
			})
			existingOffsets[offset] = struct{}{}
		}
	}
	return timelines
}

func resolveProjectVertexDeformReference(
	payload []byte,
	offset int,
	end int,
	attachments *ProjectVertexAttachmentDirectory,
) (ProjectVertexAttachmentRecord, bool) {
	classID, cursor, ok := readPositiveVarint(payload, offset)
	if !ok || (classID != ProjectAttachmentClassPath &&
		classID != ProjectAttachmentClassClipping) {
		return ProjectVertexAttachmentRecord{}, false
	}
	reference, cursor, ok := readPositiveVarint(payload, cursor)
	if !ok || reference < projectFirstWireReference || cursor > end {
		return ProjectVertexAttachmentRecord{}, false
	}
	if cursor < end &&
		!bytes.HasPrefix(payload[cursor:end], projectTimelinePrefix) {
		return ProjectVertexAttachmentRecord{}, false
	}
	match := -1
	for index, attachment := range attachments.Records {
		if attachment.ClassID != classID ||
			attachment.WireReference != reference {
			continue
		}
		if match >= 0 {
			return ProjectVertexAttachmentRecord{}, false
		}
		match = index
	}
	if match < 0 {
		return ProjectVertexAttachmentRecord{}, false
	}
	return attachments.Records[match], true
}

func resolveProjectVertexDeformAttachment(
	attachments *ProjectVertexAttachmentDirectory,
	boneReference int,
	vertexCount int,
) (ProjectVertexAttachmentRecord, bool) {
	match := -1
	for index, attachment := range attachments.Records {
		if projectVertexAttachmentDeformValueCount(attachment) != vertexCount ||
			!projectIntSliceContains(attachment.BoneReferences, boneReference) {
			continue
		}
		if match >= 0 {
			return ProjectVertexAttachmentRecord{}, false
		}
		match = index
	}
	if match < 0 {
		return ProjectVertexAttachmentRecord{}, false
	}
	return attachments.Records[match], true
}

func projectVertexAttachmentDeformValueCount(
	attachment ProjectVertexAttachmentRecord,
) int {
	count := 0
	for _, vertex := range attachment.MeshVertices {
		count += len(vertex.Coordinates)
	}
	if count == 0 {
		count = len(attachment.Vertices)
	}
	return count
}

func projectIntSliceContains(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func discoverProjectDeformTimelinesInGroupV2(
	payload []byte,
	start int,
	end int,
) []ProjectDeformTimeline {
	timelines := make([]ProjectDeformTimeline, 0)
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		if payload[cursor] != 6 || payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, vertexCount, next, ok := readProjectDeformKeysV2(
			payload,
			keyCursor,
			end,
			keyCount,
		)
		if !ok {
			continue
		}
		attachmentClassID, referenceCursor, classOK := readPositiveVarint(
			payload,
			next,
		)
		if !classOK || attachmentClassID != ProjectAttachmentClassMesh {
			continue
		}
		attachmentReference, referenceEnd, referenceOK := readPositiveVarint(
			payload,
			referenceCursor,
		)
		if !referenceOK || attachmentReference < 1 {
			continue
		}
		// 旧 4.3 v1/v2 的附件引用是局部对象号（1 = 第一个 mesh），
		// 不能套用现代 wire reference 下限。
		family := legacyProject43Family(payload)
		if attachmentReference < projectFirstWireReference &&
			family != "spine-4.3-legacy-project-v1" &&
			family != "spine-4.3-legacy-project-v2" {
			continue
		}
		timelines = append(timelines, ProjectDeformTimeline{
			AttachmentClassID:   attachmentClassID,
			AttachmentReference: attachmentReference,
			Offset:              offset,
			VertexCount:         vertexCount,
			Keys:                keys,
		})
		offset = referenceEnd - 1
	}
	return timelines
}

func readProjectDeformKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectDeformKey, int, int, bool) {
	keys, vertexCount, cursor, ok := readProjectChunkedDeformKeysV2(
		payload,
		offset,
		end,
		count,
	)
	if ok {
		return keys, vertexCount, cursor, true
	}
	return readProjectLegacyDeformKeysV2(payload, offset, end, count)
}

func readProjectChunkedDeformKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectDeformKey, int, int, bool) {
	keys := make([]ProjectDeformKey, 0, count)
	cursor := offset
	vertexCount := 0
	expandedCurves := false
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix)+5 > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, 0, offset, false
		}
		keyOffset := cursor
		frameOffset := cursor + len(projectTimelineKeyPrefix)
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, 0, offset, false
		}
		curveOffset := frameOffset + 4
		curveFlag := payload[curveOffset]
		if index == 0 {
			expandedCurves = curveFlag == 1
		}
		var curve [4]float32
		if expandedCurves {
			if curveOffset+21 > end {
				return nil, 0, offset, false
			}
			for curveIndex := 0; curveIndex < 4; curveIndex++ {
				curve[curveIndex] = readProjectFloat32(
					payload,
					curveOffset+1+curveIndex*4,
				)
				if !finiteProjectFloat(curve[curveIndex]) {
					return nil, 0, offset, false
				}
			}
			cursor = curveOffset + 21
		} else {
			cursor = curveOffset + 1
		}
		if cursor >= end || payload[cursor] != 0x01 {
			return nil, 0, offset, false
		}
		currentVertexCount, next, ok := readPositiveVarint(payload, cursor+1)
		if !ok || currentVertexCount < 1 || currentVertexCount > 10_000_000 {
			return nil, 0, offset, false
		}
		if vertexCount == 0 {
			vertexCount = currentVertexCount
		} else if currentVertexCount != vertexCount {
			return nil, 0, offset, false
		}
		if next >= end {
			return nil, 0, offset, false
		}
		chunkCount, next, ok := readPositiveVarint(payload, next)
		if !ok {
			return nil, 0, offset, false
		}
		cursor = next
		key := ProjectDeformKey{
			Index:     index,
			Frame:     frame,
			Time:      frame / projectAnimationFrameRate,
			Curve:     curve,
			CurveFlag: curveFlag,
			Offset:    keyOffset,
		}
		if chunkCount != 0 {
			maxChunkCount := (currentVertexCount + 127) / 128
			if chunkCount > maxChunkCount {
				return nil, 0, offset, false
			}
			key.HasVertices = true
			key.Vertices = make([]float32, currentVertexCount)
			for chunkIndex := 0; chunkIndex < chunkCount; chunkIndex++ {
				if cursor >= end {
					return nil, 0, offset, false
				}
				nonZeroCount := int(payload[cursor])
				cursor++
				chunkStart := chunkIndex * 128
				chunkWidth := currentVertexCount - chunkStart
				if chunkWidth > 128 {
					chunkWidth = 128
				}
				if nonZeroCount > chunkWidth {
					return nil, 0, offset, false
				}
				if nonZeroCount == 0 {
					continue
				}
				if cursor+chunkWidth*4 > end {
					return nil, 0, offset, false
				}
				actualNonZeroCount := 0
				for valueIndex := 0; valueIndex < chunkWidth; valueIndex++ {
					value := readProjectFloat32(
						payload,
						cursor+valueIndex*4,
					)
					if !finiteProjectFloat(value) {
						return nil, 0, offset, false
					}
					key.Vertices[chunkStart+valueIndex] = value
					if value != 0 {
						actualNonZeroCount++
					}
				}
				if actualNonZeroCount != nonZeroCount {
					return nil, 0, offset, false
				}
				cursor += chunkWidth * 4
			}
		}
		keys = append(keys, key)
	}
	return keys, vertexCount, cursor, true
}

func readProjectLegacyDeformKeysV2(
	payload []byte,
	offset int,
	end int,
	count int,
) ([]ProjectDeformKey, int, int, bool) {
	keys := make([]ProjectDeformKey, 0, count)
	cursor := offset
	vertexCount := 0
	expandedCurves := false
	for index := 0; index < count; index++ {
		if cursor+len(projectTimelineKeyPrefix)+5 > end ||
			!bytes.HasPrefix(payload[cursor:end], projectTimelineKeyPrefix) {
			return nil, 0, offset, false
		}
		keyOffset := cursor
		frameOffset := cursor + len(projectTimelineKeyPrefix)
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 {
			return nil, 0, offset, false
		}
		curveOffset := frameOffset + 4
		curveFlag := payload[curveOffset]
		if index == 0 {
			expandedCurves = curveFlag == 1
		}
		var curve [4]float32
		if expandedCurves {
			if curveOffset+21 > end {
				return nil, 0, offset, false
			}
			for curveIndex := 0; curveIndex < 4; curveIndex++ {
				curve[curveIndex] = readProjectFloat32(
					payload,
					curveOffset+1+curveIndex*4,
				)
				if !finiteProjectFloat(curve[curveIndex]) {
					return nil, 0, offset, false
				}
			}
			cursor = curveOffset + 21
		} else {
			cursor = curveOffset + 1
		}
		if cursor >= end || payload[cursor] != 0x01 {
			return nil, 0, offset, false
		}
		currentVertexCount, next, ok := readPositiveVarint(payload, cursor+1)
		if !ok || currentVertexCount < 1 || currentVertexCount > 10_000_000 {
			return nil, 0, offset, false
		}
		if vertexCount == 0 {
			vertexCount = currentVertexCount
		} else if currentVertexCount != vertexCount {
			return nil, 0, offset, false
		}
		if next >= end {
			return nil, 0, offset, false
		}
		key := ProjectDeformKey{
			Index:     index,
			Frame:     frame,
			Time:      frame / projectAnimationFrameRate,
			Curve:     curve,
			CurveFlag: curveFlag,
			Offset:    keyOffset,
		}
		objectMarker := payload[next]
		cursor = next + 1
		if objectMarker != 0 {
			key.HasVertices = true
			key.Vertices = make([]float32, currentVertexCount)
			for vertexIndex := 0; vertexIndex < currentVertexCount; {
				chunkWidth := currentVertexCount - vertexIndex
				if chunkWidth > 128 {
					chunkWidth = 128
				}
				if cursor >= end ||
					int(payload[cursor]) != chunkWidth ||
					cursor+1+chunkWidth*4 > end {
					return nil, 0, offset, false
				}
				cursor++
				for chunkIndex := 0; chunkIndex < chunkWidth; chunkIndex++ {
					value := readProjectFloat32(
						payload,
						cursor+chunkIndex*4,
					)
					if !finiteProjectFloat(value) {
						return nil, 0, offset, false
					}
					key.Vertices[vertexIndex+chunkIndex] = value
				}
				cursor += chunkWidth * 4
				vertexIndex += chunkWidth
			}
		}
		keys = append(keys, key)
	}
	return keys, vertexCount, cursor, true
}
