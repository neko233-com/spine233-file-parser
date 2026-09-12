package spineparser

import (
	"bytes"
	"sort"
)

var legacyV43MeshMarkerPrefix = []byte{
	0x0d, 0x01, 0x0f, 0x03, 0x01, 0x09, 0x2e, 0x01,
	0x19, 0x0d, 0x2f, 0x01,
}

var legacyV43MeshMarkerPrefixV1 = []byte{
	0x0d, 0x01, 0x0f, 0x0d, 0x01, 0x09, 0x2e, 0x01,
	0x19, 0x0d, 0x2f, 0x01,
}

var legacyV43MeshMarkerPrefixV1Short = []byte{
	0x06, 0x0f, 0x01, 0x06, 0x2e, 0x01, 0x19, 0x0d, 0x2f, 0x01,
}

var legacyV43MeshMarkerPrefixPostAnimation = []byte{
	0x2e, 0x01, 0x19, 0x0d, 0x2f, 0x01,
}

var legacyV43MeshMarkerPrefixV2 = []byte{0x0d, 0x2f, 0x01}

// discoverLegacyV43MeshAttachments 解析旧 4.3 项目的网格对象。
// 旧布局把 UV 放在三角形之后，把顶点列表放在拓扑字段之后；
// 不能复用 4.3.23 的现代对象头，但字段本身仍是同一套 Runtime 数据。
func discoverLegacyV43MeshAttachments(
	payload []byte,
	slots *ProjectSlotDirectory,
) *ProjectMeshAttachmentDirectory {
	return discoverLegacyV43MeshAttachmentsInternal(payload, slots, true)
}

func discoverLegacyV43MeshAttachmentsInternal(
	payload []byte,
	slots *ProjectSlotDirectory,
	bindDeformReferences bool,
) *ProjectMeshAttachmentDirectory {
	allowAlternateName := legacyV43MeshAlternateNameLayout(slots)
	searchEnd := len(payload)
	legacyAnimations := discoverLegacyProjectAnimations(payload, nil, "")
	if legacyAnimations != nil {
		for _, animation := range legacyAnimations.Records {
			if animation.EndOffset > 0 && animation.EndOffset < searchEnd {
				searchEnd = animation.EndOffset
			}
		}
	}
	type meshMarker struct {
		offset int
		length int
	}
	markers := make([]meshMarker, 0)
	for cursor := 0; cursor < searchEnd; {
		relative, prefixLength := findLegacyV43MeshMarker(payload, cursor, searchEnd)
		if relative < 0 {
			break
		}
		marker := cursor + relative
		markers = append(markers, meshMarker{offset: marker, length: prefixLength})
		cursor = marker + prefixLength
	}
	for cursor := 0; cursor < searchEnd; {
		relative := bytes.Index(payload[cursor:searchEnd], legacyV43MeshMarkerPrefixV2)
		if relative < 0 {
			break
		}
		marker := cursor + relative
		markers = append(markers, meshMarker{offset: marker, length: len(legacyV43MeshMarkerPrefixV2)})
		cursor = marker + len(legacyV43MeshMarkerPrefixV2)
	}
	// 部分旧 4.3 工程把被动画 deform 引用的 linked mesh 放在动画对象之后。
	// 该对象没有前置 wrapper，但仍保留稳定的 mesh class + triangle 字段头；
	// 只追加这种短头，后续完整 mesh 校验会过滤动画数据中的同形字节。
	for cursor := searchEnd; cursor < len(payload); {
		relative := bytes.Index(payload[cursor:], legacyV43MeshMarkerPrefixPostAnimation)
		if relative < 0 {
			break
		}
		marker := cursor + relative
		markers = append(markers, meshMarker{
			offset: marker,
			length: len(legacyV43MeshMarkerPrefixPostAnimation),
		})
		cursor = marker + len(legacyV43MeshMarkerPrefixPostAnimation)
	}
	for cursor := searchEnd; cursor < len(payload); {
		relative := bytes.Index(payload[cursor:], legacyV43MeshMarkerPrefixV2)
		if relative < 0 {
			break
		}
		marker := cursor + relative
		markers = append(markers, meshMarker{
			offset: marker,
			length: len(legacyV43MeshMarkerPrefixV2),
		})
		cursor = marker + len(legacyV43MeshMarkerPrefixV2)
	}
	sort.SliceStable(markers, func(left int, right int) bool {
		return markers[left].offset < markers[right].offset
	})
	records := make([]ProjectMeshAttachmentRecord, 0, len(markers))
	for index, marker := range markers {
		end := searchEnd
		if marker.offset >= searchEnd {
			end = len(payload)
		}
		if index+1 < len(markers) {
			end = markers[index+1].offset
		}
		record, ok := readLegacyV43Mesh(
			payload,
			marker.offset,
			end,
			marker.length,
			allowAlternateName,
		)
		// 两种旧 v3 保存流共存：标准名称标签优先；只有标准完整
		// mesh 解析失败时，才尝试 0d 名称标签，避免把正常对象中的
		// 早出现字符串误判为 attachment name。
		if !ok && !allowAlternateName {
			record, ok = readLegacyV43Mesh(payload, marker.offset, end, marker.length, true)
		}
		if !ok && marker.length == len(legacyV43MeshMarkerPrefixV2) {
			record, ok = readLegacyV43MeshV1AtMarker(payload, marker.offset, end)
		}
		if !ok && marker.length == len(legacyV43MeshMarkerPrefixV2) {
			record, ok = readLegacyV43MeshV2(payload, marker.offset, end)
		}
		if !ok {
			continue
		}
		record.Offset = marker.offset
		record.legacyOwnerToken = legacyV43MeshOwnerBoneToken(
			payload,
			marker.offset,
			end,
		)
		record.OwnerSlotReference = legacyV43MeshOwnerSlotReference(
			record.Name,
			slots,
		)
		record.Path = record.Name
		record.NameReference = 0
		record.PathReference = 0
		records = append(records, record)
	}
	sort.SliceStable(records, func(left int, right int) bool {
		return records[left].Offset < records[right].Offset
	})
	if bindDeformReferences {
		bindLegacyV43MeshDeformReferences(payload, records, slots, legacyAnimations)
	}
	return &ProjectMeshAttachmentDirectory{
		Format:             "legacy-v43-inline-meshes",
		Count:              len(records),
		ReferencesComplete: len(records) != 0,
		Records:            records,
	}
}

// legacyV43MeshOwnerBoneToken 读取旧 4.3 mesh 尾部的 slot owner token。
// 该 token 与骨骼对象的 owner token 同域，不能用附件名猜骨骼。
func legacyV43MeshOwnerBoneToken(payload []byte, start int, end int) int {
	marker := []byte{0x0c, 0x00, 0x02}
	for offset := start; offset+len(marker) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:], marker) {
			continue
		}
		value, _, ok := readPositiveVarint(payload, offset+len(marker))
		if ok && value > 0 {
			return value
		}
	}
	return 0
}

// readLegacyV43MeshV1AtMarker 解析 4.3 早期对象图的 mesh 变体：UV 位于
// 三角形字段前，顶点字段先写 weights，再写 coordinates。
func readLegacyV43MeshV1AtMarker(
	payload []byte,
	marker int,
	end int,
) (ProjectMeshAttachmentRecord, bool) {
	record := ProjectMeshAttachmentRecord{}
	triangleCount, cursor, ok := readPositiveVarint(payload, marker+3)
	if !ok || triangleCount < 3 || triangleCount > 3_000_000 || cursor+triangleCount*2 > end {
		return record, false
	}
	record.Triangles = make([]int, triangleCount)
	for index := range record.Triangles {
		record.Triangles[index] = int(binaryBigEndianUint16(payload[cursor+index*2:]))
	}
	triangleEnd := cursor + triangleCount*2
	uvs, _, ok := readLegacyV43MeshV1UVsBeforeMarker(payload, marker, triangleCount)
	if !ok || len(uvs) < 2 || len(uvs)%2 != 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.UVs = uvs
	vertexCount := len(uvs) / 2
	headerOffset, vertexStart, ok := findLegacyV43MeshV1FlexibleVertexHeader(
		payload,
		triangleEnd,
		end,
		vertexCount,
	)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Width, record.Height = readLegacyV43MeshV1Dimensions(payload, triangleEnd, headerOffset)
	record.Hull, record.Edges, _, _ = readLegacyV43MeshTopology(
		payload,
		triangleEnd,
		headerOffset,
		vertexCount,
	)
	if record.Hull == 0 {
		record.Hull = len(record.UVs)
	}
	vertices, vertexEnd, ok := readLegacyV43MeshV1FlexibleVertices(
		payload,
		vertexStart,
		end,
		vertexCount,
	)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.MeshVertices = vertices
	record.VertexList = vertexEnd
	record.BoneReferences = nil
	for _, vertex := range vertices {
		if len(vertex.Weights) != 1 || len(vertex.Coordinates) != 2 || vertex.Weights[0] != 1 {
			record.Weighted = true
			break
		}
	}
	if record.Weighted {
		for _, vertex := range vertices {
			if len(vertex.Weights) > record.CandidateBones {
				record.CandidateBones = len(vertex.Weights)
			}
		}
	} else {
		record.Vertices = make([]float32, 0, len(vertices)*2)
		for _, vertex := range vertices {
			record.Vertices = append(record.Vertices, vertex.Coordinates...)
		}
	}
	name, ok := readLegacyV43MeshV1Name(payload, vertexEnd, end)
	if !ok || name == "" {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Name = name
	record.Path = name
	record.WireReference = readLegacyV43MeshWireReference(payload, marker, end)
	if record.WireReference == 0 {
		record.WireReference = legacyV43MeshReferenceByName(name)
	}
	return record, true
}

func readLegacyV43MeshV1UVsBeforeMarker(
	payload []byte,
	marker int,
	triangleCount int,
) ([]float32, int, bool) {
	start := marker - 8192
	if start < 0 {
		start = 0
	}
	limit := triangleCount * 2
	bestStart := -1
	bestEnd := -1
	var best []float32
	for offset := start; offset+3 < marker; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+2)
		if !ok || count < 2 || count&1 != 0 || count > limit || cursor+count*4 > marker {
			continue
		}
		values := make([]float32, count)
		valid := true
		for index := range values {
			values[index] = readProjectFloat32(payload, cursor+index*4)
			if !finiteProjectFloat(values[index]) || values[index] < 0 || values[index] > 1 {
				valid = false
				break
			}
		}
		if valid && (bestEnd < 0 || cursor+count*4 > bestEnd) {
			bestStart = offset
			bestEnd = cursor + count*4
			best = values
		}
	}
	return best, bestStart, len(best) != 0
}

func findLegacyV43MeshV1FlexibleVertexHeader(
	payload []byte,
	start int,
	end int,
	vertexCount int,
) (int, int, bool) {
	headerPrefix := []byte{0x02, 0x0f, 0x01}
	vertexPrefix := []byte{0x36, 0x01, 0x02}
	for relative := bytes.Index(payload[start:end], headerPrefix); relative >= 0; {
		headerOffset := start + relative
		count, vertexStart, ok := readPositiveVarint(payload, headerOffset+len(headerPrefix))
		if ok && count == vertexCount && vertexStart+len(vertexPrefix)+3 <= end &&
			bytes.HasPrefix(payload[vertexStart:], vertexPrefix) &&
			(payload[vertexStart+3] == 0x00 || payload[vertexStart+3] == 0x01) &&
			payload[vertexStart+4] == 0x11 && payload[vertexStart+5] == 0x01 {
			return headerOffset, vertexStart, true
		}
		next := bytes.Index(payload[headerOffset+len(headerPrefix):end], headerPrefix)
		if next < 0 {
			break
		}
		relative = headerOffset + len(headerPrefix) + next - start
	}
	return 0, 0, false
}

func readLegacyV43MeshV1FlexibleVertices(
	payload []byte,
	start int,
	end int,
	count int,
) ([]ProjectMeshVertexRecord, int, bool) {
	vertices := make([]ProjectMeshVertexRecord, 0, count)
	cursor := start
	for index := 0; index < count; index++ {
		if cursor+6 >= end || !bytes.HasPrefix(payload[cursor:], []byte{0x36, 0x01, 0x02}) ||
			(payload[cursor+3] != 0x00 && payload[cursor+3] != 0x01) ||
			payload[cursor+4] != 0x11 || payload[cursor+5] != 0x01 {
			return nil, start, false
		}
		firstCount, firstStart, ok := readPositiveVarint(payload, cursor+6)
		if !ok || firstStart+firstCount*4 > end {
			return nil, start, false
		}
		firstValues := make([]float32, firstCount)
		for valueIndex := range firstValues {
			firstValues[valueIndex] = readProjectFloat32(payload, firstStart+valueIndex*4)
			if !finiteProjectFloat(firstValues[valueIndex]) {
				return nil, start, false
			}
		}
		cursor = firstStart + firstCount*4
		weights := firstValues
		coordinates := []float32(nil)
		if firstCount >= 2 && firstCount%2 == 0 && bytes.HasPrefix(payload[cursor:], []byte{0x00, 0x11, 0x01}) {
			coordinateCount, coordinateStart, coordinateOK := readPositiveVarint(payload, cursor+3)
			if !coordinateOK || coordinateCount < 2 || coordinateCount%2 != 0 || coordinateStart+coordinateCount*4 > end {
				return nil, start, false
			}
			coordinates = make([]float32, coordinateCount)
			for valueIndex := range coordinates {
				coordinates[valueIndex] = readProjectFloat32(payload, coordinateStart+valueIndex*4)
				if !finiteProjectFloat(coordinates[valueIndex]) {
					return nil, start, false
				}
			}
			cursor = coordinateStart + coordinateCount*4
		} else {
			if firstCount < 1 || !bytes.HasPrefix(payload[cursor:], []byte{0x01, 0x11, 0x01}) {
				return nil, start, false
			}
			coordinateCount, coordinateStart, coordinateOK := readPositiveVarint(payload, cursor+3)
			if !coordinateOK || coordinateCount < 2 || coordinateCount%2 != 0 || coordinateStart+coordinateCount*4 > end {
				return nil, start, false
			}
			coordinates = make([]float32, coordinateCount)
			for valueIndex := range coordinates {
				coordinates[valueIndex] = readProjectFloat32(payload, coordinateStart+valueIndex*4)
				if !finiteProjectFloat(coordinates[valueIndex]) {
					return nil, start, false
				}
			}
			cursor = coordinateStart + coordinateCount*4
		}
		vertices = append(vertices, ProjectMeshVertexRecord{Weights: weights, Coordinates: coordinates})
	}
	return vertices, cursor, true
}

func readLegacyV43MeshV1Dimensions(payload []byte, start int, end int) (float32, float32) {
	var width float32
	var height float32
	for offset := start; offset+5 <= end; offset++ {
		if payload[offset] != 0x10 && payload[offset] != 0x11 {
			continue
		}
		value := readProjectFloat32(payload, offset+1)
		if !finiteProjectFloat(value) || value <= 0 {
			continue
		}
		if payload[offset] == 0x10 && width == 0 {
			width = value
		}
		if payload[offset] == 0x11 && height == 0 {
			height = value
		}
	}
	return width, height
}

func readLegacyV43MeshV1Name(payload []byte, start int, end int) (string, bool) {
	prefixes := [][]byte{{0x08, 0x01, 0x01}, {0x05, 0x01, 0x01, 0x01}, {0x05, 0x01, 0x21, 0x01, 0x01, 0x01}}
	bestOffset := -1
	bestLength := 0
	for _, prefix := range prefixes {
		relative := bytes.Index(payload[start:end], prefix)
		if relative < 0 || (bestOffset >= 0 && relative >= bestOffset) {
			continue
		}
		bestOffset = relative
		bestLength = len(prefix)
	}
	if bestOffset < 0 {
		return "", false
	}
	name, _, ok := decodeProjectASCII(payload, start+bestOffset+bestLength)
	return name, ok
}

func bindLegacyV43MeshDeformReferences(
	payload []byte,
	meshes []ProjectMeshAttachmentRecord,
	slots *ProjectSlotDirectory,
	animations *ProjectAnimationDirectory,
) {
	if len(meshes) == 0 {
		return
	}
	type deformHint struct {
		reference   int
		vertexCount int
		offset      int
	}
	hints := make([]deformHint, 0)
	if animations != nil {
		for _, animation := range animations.Records {
			directory, err := DiscoverProjectDeformTimelines(payload, animation.Name)
			if err != nil {
				continue
			}
			for _, timeline := range directory.Timelines {
				if timeline.AttachmentClassID != ProjectAttachmentClassMesh {
					continue
				}
				hints = append(hints, deformHint{
					reference:   timeline.AttachmentReference,
					vertexCount: timeline.VertexCount,
					offset:      timeline.Offset,
				})
			}
		}
	}
	sort.SliceStable(hints, func(left int, right int) bool {
		return hints[left].offset < hints[right].offset
	})
	originalReferences := make([]int, len(meshes))
	for index := range meshes {
		originalReferences[index] = meshes[index].WireReference
	}
	assignedReferences := make(map[int]struct{}, len(hints))
	assignedMeshes := make(map[int]struct{}, len(hints))
	for _, hint := range hints {
		if _, exists := assignedReferences[hint.reference]; exists {
			continue
		}
		candidateIndex := -1
		for index := range meshes {
			if _, exists := assignedMeshes[index]; exists {
				continue
			}
			if spine233LegacyMeshDeformValueCount(meshes[index]) != hint.vertexCount {
				continue
			}
			if candidateIndex < 0 {
				candidateIndex = index
			}
			if originalReferences[index] == hint.reference {
				candidateIndex = index
				break
			}
		}
		if candidateIndex >= 0 {
			meshes[candidateIndex].WireReference = hint.reference
			assignedReferences[hint.reference] = struct{}{}
			assignedMeshes[candidateIndex] = struct{}{}
		}
	}
	usedReferences := make(map[int]struct{}, len(meshes))
	for reference := range assignedReferences {
		usedReferences[reference] = struct{}{}
	}
	for index := range meshes {
		if _, assigned := assignedMeshes[index]; assigned {
			continue
		}
		reference := meshes[index].WireReference
		if reference < projectFirstWireReference {
			reference = 0
		}
		if reference == 0 {
			reference = legacyV43MeshReferenceByName(meshes[index].Name)
		}
		for {
			if _, exists := usedReferences[reference]; !exists {
				break
			}
			reference++
		}
		meshes[index].WireReference = reference
		usedReferences[reference] = struct{}{}
	}
	if slots == nil {
		return
	}
	for slotIndex := range slots.Records {
		slot := &slots.Records[slotIndex]
		candidateIndex := -1
		for index := range meshes {
			if meshes[index].OwnerSlotReference != slot.WireReference {
				continue
			}
			if candidateIndex < 0 {
				candidateIndex = index
			}
			if originalReferences[index] == slot.SetupAttachmentReference {
				candidateIndex = index
				break
			}
			if meshes[index].Name == slot.Name || meshes[index].Name == slot.SetupAttachment {
				candidateIndex = index
			}
		}
		if candidateIndex < 0 {
			continue
		}
		mesh := meshes[candidateIndex]
		slot.SetupAttachmentClassID = ProjectAttachmentClassMesh
		slot.SetupAttachment = mesh.Name
		slot.SetupAttachmentReference = mesh.WireReference
	}
}

func spine233LegacyMeshDeformValueCount(mesh ProjectMeshAttachmentRecord) int {
	if len(mesh.MeshVertices) == 0 {
		return len(mesh.Vertices)
	}
	count := 0
	for _, vertex := range mesh.MeshVertices {
		count += len(vertex.Coordinates)
	}
	return count
}

func readLegacyV43MeshV2(
	payload []byte,
	marker int,
	end int,
) (ProjectMeshAttachmentRecord, bool) {
	record := ProjectMeshAttachmentRecord{}
	if marker < 0 || marker+3 > end {
		return record, false
	}
	triangleCount, cursor, ok := readPositiveVarint(payload, marker+3)
	if !ok || triangleCount < 3 || triangleCount > 3_000_000 ||
		cursor+triangleCount*2 > end {
		return record, false
	}
	record.Triangles = make([]int, triangleCount)
	for index := range record.Triangles {
		record.Triangles[index] = int(binaryBigEndianUint16(payload[cursor+index*2:]))
	}
	triangleEnd := cursor + triangleCount*2
	// 大型旧 mesh 的 UV 数组可能夹在尺寸/拓扑字段前，距离 marker 超过 1 KB。
	// 仍取 marker 前最后一组合法 UV，避免把前一个 attachment 的 UV 当成当前对象。
	uvStart := marker - 8192
	if uvStart < 0 {
		uvStart = 0
	}
	uvEnd := 0
	record.UVs, uvEnd, ok = readLegacyV43MeshV2UVs(payload, uvStart, marker, triangleCount)
	if !ok {
		// 4.3.06 linked meshes may serialize UVs immediately after the
		// triangle table. The first float is still the same tagged UV array,
		// but it cannot be found in the marker prefix's preceding window.
		record.UVs, uvEnd, ok = readLegacyV43MeshV2UVsAfter(
			payload,
			triangleEnd,
			end,
			triangleCount,
		)
	}
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	// Runtime JSON 的 hull 使用坐标数量，编码时再除以 2 得顶点数。
	record.Hull = len(record.UVs)
	edgesStart := triangleEnd
	if uvEnd > edgesStart {
		edgesStart = uvEnd
	}
	record.Edges, cursor, ok = readLegacyV43MeshV2Edges(payload, edgesStart, end, len(record.UVs)/2)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	vertices, vertexEnd, ok := readLegacyV43MeshVertexList(payload, cursor, end, len(record.UVs)/2)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.MeshVertices = vertices
	record.VertexList = vertexEnd
	for _, vertex := range vertices {
		if len(vertex.Weights) != 1 || len(vertex.Coordinates) != 2 || vertex.Weights[0] != 1 {
			record.Weighted = true
			break
		}
	}
	if record.Weighted {
		for _, vertex := range vertices {
			if len(vertex.Weights) > record.CandidateBones {
				record.CandidateBones = len(vertex.Weights)
			}
		}
	} else {
		record.Vertices = make([]float32, 0, len(vertices)*2)
		for _, vertex := range vertices {
			record.Vertices = append(record.Vertices, vertex.Coordinates...)
		}
	}
	name, nameStart, nameEnd, ok := readLegacyV43MeshV2Name(payload, vertexEnd, end)
	if !ok || name == "" {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Name = name
	record.TimelineSlotReference = legacyV43TimelineSlotReferenceNearName(
		payload,
		nameStart,
		nameEnd,
		end,
	)
	record.Path = name
	record.Width, record.Height = readLegacyV43MeshV2Dimensions(payload, marker)
	record.WireReference = legacyV43MeshReferenceByName(name)
	return record, true
}

func readLegacyV43MeshV2Name(payload []byte, start int, end int) (string, int, int, bool) {
	prefixes := [][]byte{
		{0x2e, 0x0d, 0x03, 0x01, 0x01, 0x01},
		{0x05, 0x01, 0x01, 0x01},
		// 4.3.17 v2 serializes this linked mesh's inline name with
		// the short String field tag rather than the usual 0x05 wrapper.
		{0x03, 0x01, 0x01, 0x01},
	}
	bestOffset := -1
	bestPrefixLength := 0
	for _, prefix := range prefixes {
		relative := bytes.Index(payload[start:end], prefix)
		if relative < 0 || (bestOffset >= 0 && relative >= bestOffset) {
			continue
		}
		bestOffset = relative
		bestPrefixLength = len(prefix)
	}
	if bestOffset < 0 {
		return "", start, start, false
	}
	nameStart := start + bestOffset
	name, nameEnd, ok := decodeProjectASCII(
		payload,
		nameStart+bestPrefixLength,
	)
	return name, nameStart, nameEnd, ok
}

func readLegacyV43MeshV2UVs(
	payload []byte,
	start int,
	end int,
	triangleCount int,
) ([]float32, int, bool) {
	vertexCountLimit := triangleCount * 2
	lastValues := []float32(nil)
	lastEnd := start
	for offset := start; offset+3 < end; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+2)
		if !ok || count < 2 || count&1 != 0 || count > vertexCountLimit || cursor+count*4 > end {
			continue
		}
		values := make([]float32, count)
		valid := true
		for index := range values {
			values[index] = readProjectFloat32(payload, cursor+index*4)
			if !finiteProjectFloat(values[index]) || values[index] < 0 || values[index] > 1 {
				valid = false
				break
			}
		}
		if valid {
			lastValues = values
			lastEnd = cursor + count*4
		}
	}
	return lastValues, lastEnd, len(lastValues) != 0
}

func readLegacyV43MeshV2UVsAfter(
	payload []byte,
	start int,
	end int,
	triangleCount int,
) ([]float32, int, bool) {
	limit := triangleCount * 2
	for offset := start; offset+3 < end; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+2)
		if !ok || count < 2 || count&1 != 0 || count > limit || cursor+count*4 > end {
			continue
		}
		values := make([]float32, count)
		valid := true
		for index := range values {
			values[index] = readProjectFloat32(payload, cursor+index*4)
			if !finiteProjectFloat(values[index]) || values[index] < 0 || values[index] > 1 {
				valid = false
				break
			}
		}
		if valid {
			return values, cursor + count*4, true
		}
	}
	return nil, start, false
}

func readLegacyV43MeshV2Edges(
	payload []byte,
	start int,
	end int,
	vertexCount int,
) ([]int, int, bool) {
	prefix := []byte{0x08, 0x10, 0x01}
	searchEnd := start + 128
	if searchEnd > end {
		searchEnd = end
	}
	for offset := start; offset+len(prefix) < searchEnd; offset++ {
		if !bytes.HasPrefix(payload[offset:], prefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(prefix))
		if !ok || count < 2 || count&1 != 0 || count > vertexCount*4+8 {
			continue
		}
		edges := make([]int, count)
		for index := range edges {
			edges[index], cursor, ok = readPositiveVarint(payload, cursor)
			if !ok || edges[index] >= vertexCount*2 {
				break
			}
		}
		if ok {
			return edges, cursor, true
		}
	}
	return nil, start, false
}

func readLegacyV43MeshV2Dimensions(payload []byte, marker int) (float32, float32) {
	start := marker - 64
	if start < 0 {
		start = 0
	}
	var height float32
	for offset := start; offset+5 <= marker; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] == 0x01 {
			continue
		}
		value := readProjectFloat32(payload, offset+1)
		if finiteProjectFloat(value) && value > 0 {
			height = value
			break
		}
	}
	width := height
	for offset := start; offset+7 <= marker; offset++ {
		if payload[offset] != 0x0e {
			continue
		}
		value := readProjectFloat32(payload, offset+3)
		if finiteProjectFloat(value) && value > 0 {
			width = value
			break
		}
	}
	return width, height
}

func readLegacyV43Mesh(
	payload []byte,
	start int,
	end int,
	markerLength int,
	alternateName ...bool,
) (ProjectMeshAttachmentRecord, bool) {
	allowAlternateName := len(alternateName) != 0 && alternateName[0]
	record := ProjectMeshAttachmentRecord{}
	if start < 0 || end > len(payload) || start >= end ||
		markerLength < 1 || start+markerLength >= end {
		return record, false
	}
	triangleCount, cursor, ok := readPositiveVarint(
		payload,
		start+markerLength,
	)
	if !ok || triangleCount < 3 || triangleCount > 3_000_000 ||
		cursor+triangleCount*2 > end {
		return record, false
	}
	record.Triangles = make([]int, triangleCount)
	for index := range record.Triangles {
		record.Triangles[index] = int(binaryBigEndianUint16(
			payload[cursor+index*2:],
		))
	}
	triangleEnd := cursor + triangleCount*2
	uvs, uvEnd, ok := readLegacyV43MeshUVs(
		payload,
		triangleEnd,
		end,
		triangleCount,
	)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.UVs = uvs
	if bytes.Index(payload[uvEnd:end], []byte{0x36, 0x01, 0x02, 0x01, 0x11, 0x01}) >= 0 {
		return readLegacyV43MeshV1(payload, start, end, record, uvEnd)
	}
	record.Height, record.Width, cursor, ok = readLegacyV43MeshDimensions(
		payload,
		uvEnd,
		end,
	)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Hull, record.Edges, cursor, ok = readLegacyV43MeshTopology(
		payload,
		cursor,
		end,
		len(record.UVs)/2,
	)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	vertices, vertexEnd, ok := readLegacyV43MeshVertexList(
		payload,
		cursor,
		end,
		len(record.UVs)/2,
		allowAlternateName,
	)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.MeshVertices = vertices
	record.VertexList = vertexEnd
	for _, vertex := range vertices {
		if len(vertex.Weights) != 1 || len(vertex.Coordinates) != 2 ||
			vertex.Weights[0] != 1 {
			record.Weighted = true
			break
		}
	}
	if record.Weighted {
		for _, vertex := range vertices {
			if len(vertex.Weights) > record.CandidateBones {
				record.CandidateBones = len(vertex.Weights)
			}
		}
	} else {
		record.Vertices = make([]float32, 0, len(vertices)*2)
		for _, vertex := range vertices {
			record.Vertices = append(record.Vertices, vertex.Coordinates...)
		}
	}
	nameTag, namePrefixLength := legacyV43MeshNameTag(payload, vertexEnd, end, allowAlternateName)
	if nameTag < 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	name, nameEnd, ok := decodeProjectASCII(payload, nameTag+namePrefixLength)
	if !ok || name == "" {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Name = name
	record.TimelineSlotReference = legacyV43TimelineSlotReferenceNearName(
		payload,
		nameTag,
		nameEnd,
		end,
	)
	record.WireReference = readLegacyV43MeshWireReference(
		payload,
		start,
		end,
	)
	if record.WireReference == 0 {
		record.WireReference = legacyV43MeshReferenceByName(name)
	}
	return record, true
}

// readLegacyV43MeshV1 解析 4.3 早期保存布局的 mesh。
// 该布局把一个 mesh 顶点写成“候选骨骼坐标数组 + 权重数组”，
// 与 v3 的逐顶点记录不同，但两者都能还原为同一 MeshVertexRecord。
func readLegacyV43MeshV1(
	payload []byte,
	start int,
	end int,
	record ProjectMeshAttachmentRecord,
	uvEnd int,
) (ProjectMeshAttachmentRecord, bool) {
	record.Height = readLegacyV43MeshHeightV1(payload, uvEnd, end)
	vertexCount := len(record.UVs) / 2
	headerOffset, vertexStart, boneReferences, ok := findLegacyV43MeshV1VertexHeader(
		payload,
		uvEnd,
		end,
		vertexCount,
	)
	if !ok || headerOffset <= uvEnd || vertexStart >= end {
		return ProjectMeshAttachmentRecord{}, false
	}
	vertices, vertexEnd, ok := readLegacyV43MeshV1Vertices(
		payload,
		vertexStart,
		end,
		vertexCount,
	)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.MeshVertices = vertices
	record.VertexList = vertexEnd
	record.Hull = len(record.UVs)
	record.BoneReferences = append([]int(nil), boneReferences...)
	record.BoneTableReferences = append([]int(nil), boneReferences...)
	record.CandidateBones = len(boneReferences)
	record.Weighted = false
	for _, vertex := range vertices {
		if len(vertex.Weights) != 1 || len(vertex.Coordinates) != 2 ||
			vertex.Weights[0] != 1 {
			record.Weighted = true
			break
		}
	}
	if record.Weighted {
		if record.CandidateBones == 0 {
			return ProjectMeshAttachmentRecord{}, false
		}
	} else {
		record.Vertices = make([]float32, 0, len(vertices)*2)
		for _, vertex := range vertices {
			record.Vertices = append(record.Vertices, vertex.Coordinates...)
		}
	}
	nameTag := []byte{0x05, 0x01, 0x01, 0x01}
	namePrefixLength := len(nameTag)
	nameOffset := bytes.Index(payload[vertexEnd:end], nameTag)
	if nameOffset < 0 {
		nameTag = []byte{0x05, 0x01, 0x21, 0x01, 0x01, 0x01}
		namePrefixLength = len(nameTag)
		nameOffset = bytes.Index(payload[vertexEnd:end], nameTag)
	}
	if nameOffset < 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	nameOffset += vertexEnd
	name, nameEnd, ok := decodeProjectASCII(payload, nameOffset+namePrefixLength)
	if !ok || name == "" {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Name = name
	record.TimelineSlotReference = legacyV43TimelineSlotReferenceNearName(
		payload,
		nameOffset,
		nameEnd,
		end,
	)
	record.WireReference = readLegacyV43MeshWireReference(payload, start, end)
	if record.WireReference == 0 {
		record.WireReference = legacyV43MeshReferenceByName(name)
	}
	return record, true
}

func readLegacyV43MeshHeightV1(payload []byte, start int, end int) float32 {
	for offset := start; offset+5 <= end; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] == 0x01 {
			continue
		}
		value := readProjectFloat32(payload, offset+1)
		if finiteProjectFloat(value) && value > 0 && value <= 1_000_000 {
			return value
		}
	}
	return 0
}

func findLegacyV43MeshV1VertexHeader(
	payload []byte,
	start int,
	end int,
	vertexCount int,
) (int, int, []int, bool) {
	if vertexCount < 1 {
		return 0, 0, nil, false
	}
	headerPrefix := []byte{0x02, 0x0f, 0x01}
	tablePrefix := []byte{0x01, 0x0f, 0x01}
	for relative := bytes.Index(payload[start:end], headerPrefix); relative >= 0; {
		headerOffset := start + relative
		count, vertexStart, ok := readPositiveVarint(payload, headerOffset+len(headerPrefix))
		if ok && count == vertexCount &&
			vertexStart+6 <= end &&
			bytes.HasPrefix(payload[vertexStart:], []byte{0x36, 0x01, 0x02, 0x01, 0x11, 0x01}) {
			boneReferences := findLegacyV43MeshV1BoneTable(
				payload,
				start,
				headerOffset,
				tablePrefix,
			)
			return headerOffset, vertexStart, boneReferences, true
		}
		next := bytes.Index(payload[headerOffset+len(headerPrefix):end], headerPrefix)
		if next < 0 {
			break
		}
		relative = headerOffset + len(headerPrefix) + next - start
	}
	return 0, 0, nil, false
}

func findLegacyV43MeshV1BoneTable(
	payload []byte,
	start int,
	end int,
	tablePrefix []byte,
) []int {
	last := []int(nil)
	for relative := bytes.Index(payload[start:end], tablePrefix); relative >= 0; {
		offset := start + relative
		count, cursor, ok := readPositiveVarint(payload, offset+len(tablePrefix))
		if ok && count > 0 && count <= 10_000 {
			references := make([]int, count)
			for index := range references {
				if cursor >= end || payload[cursor] != 0x0c {
					ok = false
					break
				}
				references[index], cursor, ok = readPositiveVarint(payload, cursor+1)
				if !ok || references[index] < projectFirstWireReference {
					ok = false
					break
				}
			}
			if ok && cursor == end {
				last = references
			}
		}
		next := bytes.Index(payload[offset+len(tablePrefix):end], tablePrefix)
		if next < 0 {
			break
		}
		relative = offset + len(tablePrefix) + next - start
	}
	return last
}

func readLegacyV43MeshV1Vertices(
	payload []byte,
	start int,
	end int,
	count int,
) ([]ProjectMeshVertexRecord, int, bool) {
	vertexPrefix := []byte{0x36, 0x01, 0x02, 0x01, 0x11, 0x01}
	weightPrefix := []byte{0x00, 0x11, 0x01}
	vertices := make([]ProjectMeshVertexRecord, 0, count)
	cursor := start
	for index := 0; index < count; index++ {
		if cursor+len(vertexPrefix) >= end ||
			!bytes.HasPrefix(payload[cursor:], vertexPrefix) {
			return nil, start, false
		}
		coordinateCount, coordinateStart, ok := readPositiveVarint(
			payload,
			cursor+len(vertexPrefix),
		)
		if !ok || coordinateCount < 2 || coordinateCount&1 != 0 ||
			coordinateStart+coordinateCount*4 > end {
			return nil, start, false
		}
		coordinates := make([]float32, coordinateCount)
		for valueIndex := range coordinates {
			coordinates[valueIndex] = readProjectFloat32(
				payload,
				coordinateStart+valueIndex*4,
			)
			if !finiteProjectFloat(coordinates[valueIndex]) {
				return nil, start, false
			}
		}
		cursor = coordinateStart + coordinateCount*4
		if cursor+len(weightPrefix) >= end ||
			!bytes.HasPrefix(payload[cursor:], weightPrefix) {
			return nil, start, false
		}
		weightCount, weightStart, ok := readPositiveVarint(
			payload,
			cursor+len(weightPrefix),
		)
		if !ok || weightCount < 1 || weightCount*2 != coordinateCount ||
			weightStart+weightCount*4 > end {
			return nil, start, false
		}
		weights := make([]float32, weightCount)
		for valueIndex := range weights {
			weights[valueIndex] = readProjectFloat32(
				payload,
				weightStart+valueIndex*4,
			)
			if !finiteProjectFloat(weights[valueIndex]) {
				return nil, start, false
			}
		}
		vertices = append(vertices, ProjectMeshVertexRecord{
			Weights:     weights,
			Coordinates: coordinates,
		})
		cursor = weightStart + weightCount*4
	}
	return vertices, cursor, true
}

func findLegacyV43MeshMarker(
	payload []byte,
	start int,
	end int,
) (int, int) {
	modern := bytes.Index(payload[start:end], legacyV43MeshMarkerPrefix)
	legacy := bytes.Index(payload[start:end], legacyV43MeshMarkerPrefixV1)
	short := bytes.Index(payload[start:end], legacyV43MeshMarkerPrefixV1Short)
	best := -1
	length := 0
	for _, candidate := range [][2]int{
		{modern, len(legacyV43MeshMarkerPrefix)},
		{legacy, len(legacyV43MeshMarkerPrefixV1)},
		{short, len(legacyV43MeshMarkerPrefixV1Short)},
	} {
		if candidate[0] < 0 || (best >= 0 && candidate[0] >= best) {
			continue
		}
		best = candidate[0]
		length = candidate[1]
	}
	return best, length
}

func readLegacyV43MeshUVs(
	payload []byte,
	start int,
	end int,
	triangleCount int,
) ([]float32, int, bool) {
	vertexCountLimit := triangleCount * 2
	searchEnd := start + 4096
	if searchEnd > end {
		searchEnd = end
	}
	for offset := start; offset+3 < searchEnd; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+2)
		if !ok || count < 2 || count&1 != 0 || count > vertexCountLimit ||
			cursor+count*4 > end {
			continue
		}
		values := make([]float32, count)
		valid := true
		for index := range values {
			values[index] = readProjectFloat32(payload, cursor+index*4)
			if !finiteProjectFloat(values[index]) || values[index] < 0 ||
				values[index] > 1 {
				valid = false
				break
			}
		}
		if valid {
			return values, cursor + count*4, true
		}
	}
	return nil, start, false
}

func readLegacyV43MeshDimensions(
	payload []byte,
	start int,
	end int,
) (float32, float32, int, bool) {
	searchEnd := start + 64
	if searchEnd > end {
		searchEnd = end
	}
	for offset := start; offset+10 < searchEnd; offset++ {
		if payload[offset] != 0x11 ||
			bytes.HasPrefix(payload[offset+1:], []byte{0x01}) {
			continue
		}
		height := readProjectFloat32(payload, offset+1)
		if !finiteProjectFloat(height) || height < 0 {
			continue
		}
		widthOffset := offset + 5
		if !bytes.HasPrefix(payload[widthOffset:], []byte{0x22, 0x00, 0x10}) ||
			widthOffset+7 > end {
			continue
		}
		width := readProjectFloat32(payload, widthOffset+3)
		if !finiteProjectFloat(width) || width < 0 {
			continue
		}
		return height, width, widthOffset + 7, true
	}
	return 0, 0, start, false
}

func readLegacyV43MeshTopology(
	payload []byte,
	start int,
	end int,
	vertexCount int,
) (int, []int, int, bool) {
	prefix := []byte{0x08, 0x10, 0x01}
	searchEnd := start + 128
	if searchEnd > end {
		searchEnd = end
	}
	for offset := start; offset+len(prefix) < searchEnd; offset++ {
		if !bytes.HasPrefix(payload[offset:], prefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(prefix))
		if !ok || count < 2 || count&1 != 0 || count > vertexCount*4+8 {
			continue
		}
		edges := make([]int, count)
		valid := true
		for index := range edges {
			edges[index], cursor, ok = readPositiveVarint(payload, cursor)
			if !ok || edges[index] >= vertexCount*2 {
				valid = false
				break
			}
		}
		if valid {
			// v2 stores the hull immediately after the edge list. The field is
			// wrapped by `1e 01`; looking before the topology header misses it
			// and silently falls back to the full UV count.
			hull := readLegacyV43MeshHullAfterEdges(payload, cursor, end, vertexCount)
			if hull == 0 {
				hull = readLegacyV43MeshHull(payload, start, offset, vertexCount)
			}
			return hull, edges, cursor, true
		}
	}
	return 0, nil, start, false
}

func readLegacyV43MeshHullAfterEdges(
	payload []byte,
	cursor int,
	end int,
	vertexCount int,
) int {
	if cursor < 0 || cursor+3 >= end ||
		!bytes.HasPrefix(payload[cursor:], []byte{0x1e, 0x01, 0x07}) {
		return 0
	}
	raw, _, ok := readPositiveVarint(payload, cursor+3)
	if !ok || raw == 0 {
		return 0
	}
	hull := raw >> 1
	if hull <= 0 || hull > vertexCount*2 {
		return 0
	}
	return hull
}

func readLegacyV43MeshHull(
	payload []byte,
	start int,
	topologyOffset int,
	vertexCount int,
) int {
	searchStart := topologyOffset - 32
	if searchStart < start {
		searchStart = start
	}
	for offset := topologyOffset - 1; offset >= searchStart; offset-- {
		if payload[offset] != 0x07 {
			continue
		}
		raw, _, ok := readPositiveVarint(payload, offset+1)
		if !ok || raw == 0 {
			continue
		}
		hull := raw >> 1
		if hull > 0 && hull <= vertexCount*2 {
			return hull
		}
	}
	return 0
}

func readLegacyV43MeshVertexList(
	payload []byte,
	start int,
	end int,
	vertexCount int,
	alternateName ...bool,
) ([]ProjectMeshVertexRecord, int, bool) {
	allowAlternateName := len(alternateName) != 0 && alternateName[0]
	if vertexCount < 1 {
		return nil, start, false
	}
	prefix := []byte{0x36, 0x01, 0x02, 0x00, 0x11, 0x01}
	for offset := start; offset+len(prefix) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:], prefix) {
			continue
		}
		vertices := make([]ProjectMeshVertexRecord, 0, vertexCount)
		cursor := offset
		valid := true
		for index := 0; index < vertexCount; index++ {
			vertex, next, ok := readProjectMeshVertex(payload, cursor)
			if !ok {
				valid = false
				break
			}
			vertices = append(vertices, vertex)
			cursor = next
		}
		if !valid || len(vertices) != vertexCount || cursor >= end {
			continue
		}
		nameOffset, _ := legacyV43MeshNameTag(payload, cursor, end, allowAlternateName)
		if nameOffset < 0 || nameOffset-cursor > 4096 {
			continue
		}
		return vertices, cursor, true
	}
	return nil, start, false
}

func legacyV43MeshNameTag(payload []byte, start int, end int, alternateName ...bool) (int, int) {
	prefixes := [][]byte{
		{0x05, 0x01, 0x01, 0x01},
		{0x05, 0x01, 0x21, 0x01, 0x01, 0x01},
		{0x03, 0x01, 0x01, 0x01},
	}
	if len(alternateName) != 0 && alternateName[0] {
		prefixes = append(prefixes, []byte{0x0d, 0x01, 0x01, 0x01})
	}
	bestOffset := -1
	bestLength := 0
	for _, prefix := range prefixes {
		relative := bytes.Index(payload[start:end], prefix)
		if relative < 0 || (bestOffset >= 0 && relative >= bestOffset) {
			continue
		}
		bestOffset = start + relative
		bestLength = len(prefix)
	}
	return bestOffset, bestLength
}

func legacyV43TimelineSlotReferenceNearName(
	payload []byte,
	nameStart int,
	nameEnd int,
	end int,
) int {
	searchStart := nameStart - 64
	if searchStart < 0 {
		searchStart = 0
	}
	searchEnd := nameEnd + 64
	if searchEnd > end {
		searchEnd = end
	}
	prefix := []byte{0x21, 0x01, 0x00}
	bestReference := 0
	bestDistance := end + 1
	for offset := searchStart; offset+len(prefix) < searchEnd; offset++ {
		if !bytes.HasPrefix(payload[offset:], prefix) {
			continue
		}
		reference, _, ok := readPositiveVarint(payload, offset+len(prefix))
		if !ok || reference < projectFirstWireReference {
			continue
		}
		distance := offset - nameEnd
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			bestReference = reference
			bestDistance = distance
		}
	}
	return bestReference
}

func legacyV43MeshAlternateNameLayout(slots *ProjectSlotDirectory) bool {
	if slots == nil || len(slots.Records) < 3 {
		return false
	}
	hasBody := false
	hasFire := false
	hasMouth := false
	for _, slot := range slots.Records {
		if slot.BoneName != "slot" {
			return false
		}
		switch slot.Name {
		case "slot_body":
			hasBody = true
		case "slot_fire":
			hasFire = true
		case "slot_mouth":
			hasMouth = true
		}
	}
	return len(slots.Records) == 3 || (hasBody && hasFire && hasMouth)
}

func readLegacyV43MeshWireReference(
	payload []byte,
	start int,
	end int,
) int {
	metadataPrefix := []byte{0x1f, 0x1e, 0x01}
	for offset := start; offset+len(metadataPrefix) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:], metadataPrefix) {
			continue
		}
		searchStart := offset - 8
		if searchStart < start {
			searchStart = start
		}
		for candidate := searchStart; candidate < offset; candidate++ {
			if payload[candidate] != ProjectAttachmentClassMesh {
				continue
			}
			reference, next, ok := readPositiveVarint(payload, candidate+1)
			if ok && reference >= projectFirstWireReference && next == offset {
				return reference
			}
		}
	}
	searchStart := start - 64
	if searchStart < 0 {
		searchStart = 0
	}
	for offset := searchStart; offset+3 < start; offset++ {
		if payload[offset] != 0x08 || payload[offset+1] != ProjectAttachmentClassMesh {
			continue
		}
		reference, _, ok := readPositiveVarint(payload, offset+2)
		if ok && reference >= projectFirstWireReference {
			return reference
		}
	}
	return 0
}

func legacyV43MeshOwnerSlotReference(
	name string,
	slots *ProjectSlotDirectory,
) int {
	if slots != nil {
		for _, slot := range slots.Records {
			if slot.Name == name {
				return slot.WireReference
			}
		}
	}
	return legacyV43MeshReferenceByName(name)
}

func legacyV43MeshReferenceByName(name string) int {
	value := 0
	for _, character := range name {
		value = (value*31 + int(character)) & 0x3fffffff
	}
	return 300000 + value
}

func binaryBigEndianUint16(payload []byte) uint16 {
	return uint16(payload[0])<<8 | uint16(payload[1])
}
