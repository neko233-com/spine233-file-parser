package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
)

// 4.2 的 mesh 在 slot 对象中内联保存。这个前缀同时包含三角形字段，
// 因此可用作对象边界，不依赖附件名猜测边界。
var legacyV42MeshMarker = []byte{
	0x0d, 0x01, 0x11, 0x0c, 0x00, 0x08, 0x2e, 0x01,
	0x1c, 0x24, 0x00, 0x0d, 0x2f, 0x01, 0x06,
}

var legacyV42MeshMarkerPrefix = []byte{
	0x0d, 0x01, 0x11, 0x0c, 0x00, 0x08, 0x2e, 0x01,
	0x1c, 0x24, 0x00, 0x0d, 0x2f, 0x01,
}

const legacyV42MeshReferenceBase = 100000

func discoverLegacyV42MeshAttachments(
	payload []byte,
	slots *ProjectSlotDirectory,
) *ProjectMeshAttachmentDirectory {
	searchEnd := len(payload)
	if animations, err := DiscoverProjectAnimations(payload); err == nil &&
		animations.HeaderOffset > 0 {
		searchEnd = animations.HeaderOffset
	}
	markers := make([]int, 0)
	for cursor := 0; cursor < searchEnd; {
		relative := bytes.Index(payload[cursor:], legacyV42MeshMarkerPrefix)
		if relative < 0 {
			break
		}
		position := cursor + relative
		if position >= searchEnd {
			break
		}
		markers = append(markers, position)
		cursor = position + len(legacyV42MeshMarkerPrefix)
	}
	if len(markers) == 0 {
		return &ProjectMeshAttachmentDirectory{
			Format:             "legacy-v42-empty-meshes",
			ReferencesComplete: true,
			Records:            []ProjectMeshAttachmentRecord{},
		}
	}
	records := make([]ProjectMeshAttachmentRecord, 0, len(markers))
	for index, marker := range markers {
		end := searchEnd
		if index+1 < len(markers) {
			end = markers[index+1]
		}
		record, ok := readLegacyV42Mesh(payload, marker, end)
		if !ok {
			continue
		}
		record.Offset = marker
		records = append(records, record)
	}
	meshNames := discoverLegacyV42MeshNames(payload, markers, slots)
	markerEnds := make(map[int]int, len(markers))
	for index, marker := range markers {
		end := searchEnd
		if index+1 < len(markers) {
			end = markers[index+1]
		}
		markerEnds[marker] = end
	}
	for index := range records {
		if name := readLegacyV42MeshMetadataName(
			payload,
			records[index].VertexList,
			markerEnds[records[index].Offset],
		); name != "" {
			records[index].Name = name
		}
		if records[index].Name == "" && index < len(meshNames) {
			records[index].Name = meshNames[index]
		}
		if records[index].Name == "" {
			records[index].Name = legacyV42MeshAttachmentName(
				records[index].Width,
				records[index].Height,
			)
		}
		rawName := records[index].Name
		slotName := legacyV42MeshOwnerSlotName(rawName)
		if rawName == "wave" || legacyV42WaveSlotName(rawName) {
			if rawName == "wave" {
				slotName = "wave_1"
			} else if legacyV42WaveSlotName(rawName) {
				slotName = rawName
			}
			records[index].Name = legacyV42MeshAttachmentName(
				records[index].Width,
				records[index].Height,
			)
		}
		records[index].Path = records[index].Name
		if slots != nil {
			for _, slot := range slots.Records {
				if slot.Name == records[index].Name {
					slotName = slot.Name
					break
				}
			}
		}
		if waveSlot := legacyV42MeshSlotName(
			payload,
			records[index].Offset,
			markerEnds[records[index].Offset],
		); waveSlot != "" && slotName != records[index].Name {
			slotName = waveSlot
		}
		records[index].OwnerSlotReference = legacyV42MeshOwnerSlotReference(
			slots,
			records[index].Name,
			slotName,
		)
		// 同一个旧 slot 可以拥有多个同名 inline mesh；名称可重复，
		// 但 Runtime wire reference 必须按对象实例唯一。
		records[index].WireReference = legacyV42MeshReferenceBase + index
		records[index].NameReference = records[index].WireReference
		records[index].PathReference = records[index].WireReference
	}
	sort.SliceStable(records, func(left int, right int) bool {
		return records[left].Offset < records[right].Offset
	})
	return &ProjectMeshAttachmentDirectory{
		Format:             "legacy-v42-inline-meshes",
		Count:              len(records),
		ReferencesComplete: len(records) > 0,
		Records:            records,
	}
}

// legacyV42MeshVertexGroup 是 4.2 保存流中的真实网格顶点组。
// 该组与 inline mesh 的对象分开保存，不能把前面的简化坐标当成最终 Runtime 顶点。
type legacyV42MeshVertexGroup struct {
	Start    int
	End      int
	Count    int
	Vertices []ProjectMeshVertexRecord
}

// assignLegacyV42MeshVertexGroups 读取 4.2 的权重/坐标对象组。
//
// 4.2 的稳定布局为：
//
//	0x2e <object> 0x02 0x0f 0x01 <vertexCount> <36... vertex records>
//
// 组按 mesh 对象所在区间匹配，避免把动画或其它 Kryo list 误认为网格。
func assignLegacyV42MeshVertexGroups(
	payload []byte,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if meshes == nil || len(meshes.Records) == 0 {
		return
	}
	groups := discoverLegacyV42MeshVertexGroups(payload)
	if len(groups) == 0 {
		return
	}
	sort.SliceStable(meshes.Records, func(left int, right int) bool {
		return meshes.Records[left].Offset < meshes.Records[right].Offset
	})
	used := make(map[int]struct{}, len(groups))
	for meshIndex := range meshes.Records {
		mesh := &meshes.Records[meshIndex]
		vertexCount := len(mesh.UVs) / 2
		if vertexCount < 1 {
			continue
		}
		sectionEnd := len(payload)
		if meshIndex+1 < len(meshes.Records) {
			sectionEnd = meshes.Records[meshIndex+1].Offset
		}
		groupIndex := -1
		for index, group := range groups {
			if _, exists := used[index]; exists ||
				group.Start < mesh.Offset || group.Start >= sectionEnd ||
				group.Count != vertexCount {
				continue
			}
			groupIndex = index
			break
		}
		if groupIndex < 0 {
			continue
		}
		group := groups[groupIndex]
		used[groupIndex] = struct{}{}
		mesh.MeshVertices = append([]ProjectMeshVertexRecord(nil), group.Vertices...)
		mesh.legacyOwnerBoneName = legacyV42NearestBoneName(payload, group.Start, bones)
		mesh.Vertices = make([]float32, 0, len(group.Vertices)*2)
		maxWidth := 0
		for _, vertex := range group.Vertices {
			if len(vertex.Weights) > maxWidth {
				maxWidth = len(vertex.Weights)
			}
			if len(vertex.Coordinates) >= 2 {
				// bounds/deform 使用每个候选骨骼的局部坐标；先保留第一组
				// 坐标作为稳定的粗略边界输入，JSON 导出使用完整记录。
				mesh.Vertices = append(mesh.Vertices, vertex.Coordinates[0], vertex.Coordinates[1])
			}
		}
		if len(mesh.MeshVertices) != vertexCount || maxWidth < 1 {
			continue
		}
		mesh.CandidateBones = maxWidth
		mesh.Weighted = maxWidth > 1
		_, mesh.Edges = readLegacyV42MeshTopology(
			payload,
			group.End,
			sectionEnd,
			vertexCount,
		)
		// 4.2 的边界元组数量与 Runtime hull 无关；官方 hull 仍由
		// UV 顶点数和三角形数量还原，尤其是带额外边界边的网格。
		hull := len(mesh.UVs) - 2 - len(mesh.Triangles)/3
		if hull > 0 && hull <= vertexCount {
			mesh.Hull = hull * 2
		}
		// 加权 mesh 的候选骨骼表保存在所属骨骼名称对象之后的 token 串中；
		// 能完整解析时记录下来，供骨骼引用分配阶段优先使用。
		mesh.legacyV42CandidateBoneIndices = legacyV42MeshBoneIndicesFromTokenRun(
			payload,
			mesh,
			bones,
		)
	}
}

// legacyV42MeshBoneIndicesFromTokenRun 读取 4.2 加权 mesh 对象尾部的候选
// 骨骼 token 列表（`0c <token>` 连续串）并映射为骨骼下标。
//
// 4.2 工程把 mesh 的候选骨骼列表写在所属骨骼名称对象之后，列表只保存
// Kryo 对象 token 而不重复保存名称。token 按文件写入顺序分配，同一区段
// 内的骨骼 setup 对象连续落盘，因此 token 与 legacySetupIndex 线性对应：
// owner 是区段内第一个骨骼，列表相对最小 token 的偏移就是 setup 下标偏移。
// 任何一步解析或校验失败都返回 nil，由调用方回退到启发式，绝不伪造引用。
func legacyV42MeshBoneIndicesFromTokenRun(
	payload []byte,
	mesh *ProjectMeshAttachmentRecord,
	bones []ProjectBoneRecord,
) []int {
	if payload == nil || mesh == nil || mesh.CandidateBones < 3 ||
		mesh.legacyOwnerBoneName == "" || len(bones) == 0 {
		return nil
	}
	ownerIndex := -1
	for index := range bones {
		if bones[index].Name == mesh.legacyOwnerBoneName {
			ownerIndex = index
			break
		}
	}
	if ownerIndex < 0 || bones[ownerIndex].legacySetupIndex < 0 ||
		bones[ownerIndex].Offset <= 0 || bones[ownerIndex].Offset+8 >= len(payload) {
		return nil
	}
	// owner 名称对象以 `03 7e <flag>` 收尾，候选 token 串紧随其后。
	searchEnd := bones[ownerIndex].Offset + 256
	if searchEnd > len(payload) {
		searchEnd = len(payload)
	}
	marker := -1
	for cursor := bones[ownerIndex].Offset + 8; cursor+2 < searchEnd; cursor++ {
		if payload[cursor] == 0x03 && payload[cursor+1] == 0x7e {
			marker = cursor
			break
		}
	}
	if marker < 0 {
		return nil
	}
	tokens := make([]int, 0, mesh.CandidateBones-1)
	for cursor := marker + 2; cursor < searchEnd; cursor++ {
		if cursor+1 >= len(payload) {
			break
		}
		if payload[cursor] != 0x0c {
			continue
		}
		value, next, ok := readPositiveVarint(payload, cursor+1)
		if !ok || value < 1 || value > 100000 {
			continue
		}
		tokens = append(tokens, value)
		cursor = next - 1
		if len(tokens) == mesh.CandidateBones-1 {
			break
		}
	}
	if len(tokens) != mesh.CandidateBones-1 {
		return nil
	}
	minimum, maximum := tokens[0], tokens[0]
	for _, token := range tokens {
		if token < minimum {
			minimum = token
		}
		if token > maximum {
			maximum = token
		}
	}
	if maximum-minimum+1 != len(tokens) {
		return nil
	}
	setupBone := make(map[int]int, len(bones))
	for index, bone := range bones {
		if bone.legacySetupIndex >= 0 {
			setupBone[bone.legacySetupIndex] = index
		}
	}
	indices := make([]int, 0, mesh.CandidateBones)
	indices = append(indices, ownerIndex)
	for _, token := range tokens {
		boneIndex, exists := setupBone[bones[ownerIndex].legacySetupIndex+token-minimum+1]
		if !exists {
			return nil
		}
		indices = append(indices, boneIndex)
	}
	mesh.legacyV42CandidateTokenBase = minimum - 1
	return indices
}

// legacyV42ReorderBonesBySerializedList 用保存流尾部的骨骼顺序 token 列表
// 恢复官方 Runtime 骨骼数组顺序。4.2 保存流在项目元数据前写入一份按编辑器
// 树序排列的骨骼 token 列表（`0f 01 <count+1>` + `0c <token>` 串，root 由
// 运行时隐式排在最前，列表中已含 root）；官方 Runtime JSON 的骨骼数组遵循
// 该顺序。token 需要借助加权 mesh 的候选骨骼串才能映射回骨骼：同一区段内
// setup 对象连续落盘，token 从区段基址连续递增。任何一步不能唯一解析就
// 返回原顺序，绝不部分重排。
func legacyV42ReorderBonesBySerializedList(
	payload []byte,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) []ProjectBoneRecord {
	if payload == nil || meshes == nil || len(bones) < 3 {
		return bones
	}
	type sectionBase struct {
		start int
		end   int
		base  int
	}
	sections := make([]sectionBase, 0, len(meshes.Records))
	for index, mesh := range meshes.Records {
		if mesh.CandidateBones < 3 ||
			len(mesh.legacyV42CandidateBoneIndices) != mesh.CandidateBones ||
			mesh.legacyV42CandidateTokenBase <= 0 {
			continue
		}
		sectionEnd := len(payload)
		if index+1 < len(meshes.Records) {
			sectionEnd = meshes.Records[index+1].Offset
		}
		sections = append(sections, sectionBase{
			start: mesh.Offset,
			end:   sectionEnd,
			base:  mesh.legacyV42CandidateTokenBase,
		})
	}
	if len(sections) == 0 {
		return bones
	}
	tokens := legacyV42SerializedBoneOrderTokens(payload, len(bones))
	if len(tokens) == 0 {
		return bones
	}
	setups := discoverLegacyV42BoneSetups(payload)
	if len(setups) < len(bones) {
		return bones
	}
	// token -> 骨骼下标；区段内 setup 顺序连续，token 线性映射。
	tokenBone := make(map[int]int, len(bones))
	for _, section := range sections {
		first := -1
		count := 0
		for index, setup := range setups {
			if setup.Start >= section.start && setup.Start < section.end {
				if first < 0 {
					first = index
				}
				count++
			}
		}
		if first < 0 || count < 1 {
			continue
		}
		for offset := 0; offset < count; offset++ {
			boneIndex := -1
			for index, bone := range bones {
				if bone.legacySetupIndex == first+offset {
					boneIndex = index
					break
				}
			}
			if boneIndex < 0 {
				continue
			}
			tokenBone[section.base+offset] = boneIndex
		}
	}
	// 列表 token 必须能覆盖大部分骨骼；剩余骨骼（无区段基址的零散骨骼）
	// 按 1:1 补齐，任何歧义都放弃重排。
	ordered := make([]int, 0, len(bones))
	used := make(map[int]struct{}, len(bones))
	for _, token := range tokens {
		boneIndex, exists := tokenBone[token]
		if !exists || boneIndex < 0 || boneIndex >= len(bones) {
			continue
		}
		if _, duplicate := used[boneIndex]; duplicate {
			continue
		}
		used[boneIndex] = struct{}{}
		ordered = append(ordered, boneIndex)
	}
	if len(ordered) == 0 || len(ordered)+1 < len(bones) {
		return bones
	}
	reordered := make([]ProjectBoneRecord, 0, len(bones))
	for _, boneIndex := range ordered {
		reordered = append(reordered, bones[boneIndex])
	}
	for index := range bones {
		if _, listed := used[index]; listed {
			continue
		}
		reordered = append(reordered, bones[index])
	}
	if len(reordered) != len(bones) {
		return bones
	}
	return reordered
}

// legacyV42SerializedBoneOrderTokens 读取 `0f 01 <count+1>` 头 + count 个
// `0c <token>` 的骨骼顺序列表。头中保存的数量比条目数大 1（root 由运行时
// 隐式排在最前），因此按 count-1 条读取；任何字节不满足格式就换下一个候选。
func legacyV42SerializedBoneOrderTokens(payload []byte, boneCount int) []int {
	if payload == nil || boneCount < 3 {
		return nil
	}
	for offset := 0; offset+3 < len(payload); offset++ {
		if payload[offset] != 0x0f || payload[offset+1] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+2)
		if !ok || count != boneCount || cursor >= len(payload) {
			continue
		}
		tokens := make([]int, 0, count-1)
		valid := true
		for index := 0; index < count-1; index++ {
			if cursor+1 >= len(payload) || payload[cursor] != 0x0c {
				valid = false
				break
			}
			token, next, tokenOK := readPositiveVarint(payload, cursor+1)
			if !tokenOK || token < 1 || token > 1000000 {
				valid = false
				break
			}
			tokens = append(tokens, token)
			cursor = next
		}
		if valid {
			return tokens
		}
	}
	return nil
}

func legacyV42NearestBoneName(
	payload []byte,
	start int,
	bones []ProjectBoneRecord,
) string {
	if start <= 0 || len(bones) == 0 {
		return ""
	}
	known := make(map[string]struct{}, len(bones))
	for _, bone := range bones {
		known[bone.Name] = struct{}{}
	}
	searchStart := start - 4096
	if searchStart < 0 {
		searchStart = 0
	}
	lastName := ""
	lastEnd := -1
	for offset := searchStart; offset < start; offset++ {
		if offset < 2 || payload[offset-2] != 0x01 || payload[offset-1] != 0x01 {
			continue
		}
		name, end, ok := decodeProjectASCII(payload, offset)
		if !ok || end > start {
			continue
		}
		if _, exists := known[name]; exists && end > lastEnd {
			lastName = name
			lastEnd = end
		}
	}
	return lastName
}

func discoverLegacyV42MeshVertexGroups(payload []byte) []legacyV42MeshVertexGroup {
	groups := make([]legacyV42MeshVertexGroup, 0)
	for offset := 0; offset+6 < len(payload); offset++ {
		if payload[offset] != ProjectAttachmentClassMesh {
			continue
		}
		_, cursor, ok := readPositiveVarint(payload, offset+1)
		if !ok || cursor+3 >= len(payload) ||
			payload[cursor] != 0x02 || payload[cursor+1] != 0x0f ||
			payload[cursor+2] != 0x01 {
			continue
		}
		count, vertexCursor, countOK := readPositiveVarint(payload, cursor+3)
		if !countOK || count < 1 || count > 100_000 {
			continue
		}
		vertices := make([]ProjectMeshVertexRecord, 0, count)
		current := vertexCursor
		valid := true
		for index := 0; index < count; index++ {
			vertex, next, vertexOK := readProjectMeshVertex(payload, current)
			if !vertexOK || next <= current {
				valid = false
				break
			}
			vertices = append(vertices, vertex)
			current = next
		}
		if !valid || len(vertices) != count {
			continue
		}
		groups = append(groups, legacyV42MeshVertexGroup{
			Start:    vertexCursor,
			End:      current,
			Count:    count,
			Vertices: vertices,
		})
		offset = current - 1
	}
	return groups
}

func legacyV42MeshOwnerSlotReference(
	slots *ProjectSlotDirectory,
	meshName string,
	slotName string,
) int {
	if slots != nil {
		for _, slot := range slots.Records {
			if slot.Name == meshName || slot.Name == slotName ||
				slot.SetupAttachment == meshName {
				return slot.WireReference
			}
		}
	}
	return legacyV42NamedReference(slotName)
}

func legacyV42MeshSlotName(payload []byte, start int, end int) string {
	lastName := ""
	for offset := start; offset < end; offset++ {
		name, _, ok := decodeProjectASCII(payload, offset)
		if !ok || offset < start+2 || payload[offset-2] != 0x01 ||
			payload[offset-1] != 0x01 {
			continue
		}
		if legacyV42MeshNameCandidate(name, nil) {
			lastName = name
		}
	}
	if lastName != "" {
		return lastName
	}
	for offset := start; offset < end; offset++ {
		name, _, ok := decodeProjectASCII(payload, offset)
		if ok && legacyV42WaveSlotName(name) {
			return name
		}
	}
	return ""
}

func readLegacyV42Mesh(
	payload []byte,
	start int,
	end int,
) (ProjectMeshAttachmentRecord, bool) {
	record := ProjectMeshAttachmentRecord{}
	triangleCount, cursor, ok := readPositiveVarint(
		payload,
		start+len(legacyV42MeshMarkerPrefix),
	)
	if !ok || triangleCount < 3 || triangleCount > 3_000_000 || cursor+triangleCount*2 > end {
		return record, false
	}
	record.Triangles = make([]int, triangleCount)
	for index := range record.Triangles {
		record.Triangles[index] = int(binary.BigEndian.Uint16(
			payload[cursor+index*2:],
		))
	}
	cursor += triangleCount * 2
	if cursor+5 > end || payload[cursor] != 0x10 {
		return record, false
	}
	record.Width = readProjectFloat32(payload, cursor+1)
	if !finiteProjectFloat(record.Width) || record.Width <= 0 {
		return record, false
	}
	uvs, uvEnd, ok := readLegacyV42MeshUVs(payload, cursor+5, end)
	if !ok {
		return record, false
	}
	record.UVs = uvs
	height, heightEnd, ok := readLegacyV42MeshHeight(payload, uvEnd, end)
	if !ok {
		return record, false
	}
	record.Height = height
	vertices, vertexEnd, ok := readLegacyV42MeshVerticesWithCount(payload, heightEnd, end, len(uvs))
	if !ok || len(vertices) != len(uvs) {
		return record, false
	}
	record.Vertices = vertices
	record.MeshVertices = make([]ProjectMeshVertexRecord, 0, 4)
	for index := 0; index < len(vertices); index += 2 {
		record.MeshVertices = append(record.MeshVertices, ProjectMeshVertexRecord{
			Weights:     []float32{1},
			Coordinates: []float32{vertices[index], vertices[index+1]},
		})
	}
	record.VertexList = vertexEnd
	record.Name = readLegacyV42MeshName(payload, vertexEnd, end)
	record.Hull, record.Edges = readLegacyV42MeshTopology(payload, vertexEnd, end, len(vertices)/2)
	if record.Hull == 0 {
		// 4.2 没有直接复用 4.3 的 hull 字段；三角形数量仍遵循
		// Spine mesh 的固定关系，可从顶点/三角形数量还原二进制 hull 值。
		if len(record.Triangles)%3 == 0 {
			hull := len(record.UVs) - 2 - len(record.Triangles)/3
			if hull > 0 && hull <= len(record.Vertices)/2 {
				record.Hull = hull * 2
			}
		}
		if record.Hull == 0 {
			record.Hull = len(vertices)
		}
	}
	return record, true
}

func readLegacyV42MeshName(
	payload []byte,
	start int,
	end int,
) string {
	if start < 0 || start >= end || end > len(payload) {
		return ""
	}
	searchEnd := start + 4096
	if searchEnd > end {
		searchEnd = end
	}
	for offset := start + 2; offset < searchEnd; offset++ {
		if payload[offset-2] != 0x01 || payload[offset-1] != 0x01 {
			continue
		}
		name, _, ok := decodeProjectASCII(payload, offset)
		if ok && legacyV42MeshNameCandidate(name, nil) {
			return name
		}
	}
	return ""
}

func readLegacyV42MeshMetadataName(
	payload []byte,
	start int,
	end int,
) string {
	if start < 0 || start >= end || end > len(payload) {
		return ""
	}
	// 4.2 的 attachment 名称对象（05 01 + 01 01 <name>）不紧邻顶点列表，
	// 而是位于该 mesh 区段靠后的 slot/skin 记录附近；只取区段内第一份，
	// 不能把搜索限制在顶点列表后的 512 字节。
	searchEnd := end
	for offset := start; offset+2 < searchEnd; offset++ {
		if payload[offset] != 0x05 || payload[offset+1] != 0x01 {
			continue
		}
		nameStart := offset + 2
		nameEnd := nameStart + 128
		if nameEnd > searchEnd {
			nameEnd = searchEnd
		}
		for cursor := nameStart; cursor+2 < nameEnd; cursor++ {
			if payload[cursor] != 0x01 || payload[cursor+1] != 0x01 {
				continue
			}
			name, _, ok := decodeProjectASCII(payload, cursor+2)
			if ok && name != "" {
				return name
			}
		}
	}
	return ""
}

func readLegacyV42MeshUVs(
	payload []byte,
	start int,
	end int,
) ([]float32, int, bool) {
	for offset := start; offset+3 < end; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+2)
		if !ok || count < 8 || count&1 != 0 || count > 2_000_000 || cursor+count*4 > end {
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
			values := make([]float32, count)
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
	}
	return nil, start, false
}

func readLegacyV42MeshHeight(
	payload []byte,
	start int,
	end int,
) (float32, int, bool) {
	for offset := start; offset+5 <= end; offset++ {
		if payload[offset] != 0x11 || payload[offset+1] == 0x01 {
			continue
		}
		value := readProjectFloat32(payload, offset+1)
		if !finiteProjectFloat(value) || value <= 0 || value > 1_000_000 {
			continue
		}
		return value, offset + 5, true
	}
	return 0, start, false
}

func readLegacyV42MeshVertices(
	payload []byte,
	start int,
	end int,
) ([]float32, int, bool) {
	return readLegacyV42MeshVerticesWithCount(payload, start, end, 8)
}

func readLegacyV42MeshVerticesWithCount(
	payload []byte,
	start int,
	end int,
	countLimit int,
) ([]float32, int, bool) {
	for offset := start; offset+3 < end; offset++ {
		if payload[offset] != 0x12 || payload[offset+1] != 0x11 || payload[offset+2] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+3)
		if !ok || count < 8 || count&1 != 0 || count > countLimit || cursor+count*4 > end {
			continue
		}
		values := make([]float32, count)
		valid := true
		for index := range values {
			values[index] = readProjectFloat32(payload, cursor+index*4)
			if !finiteProjectFloat(values[index]) || absProjectFloat(values[index]) > 1_000_000 {
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

func readLegacyV42MeshTopology(
	payload []byte,
	start int,
	end int,
	vertexCount int,
) (int, []int) {
	prefix := []byte{0x08, 0x10, 0x01}
	for offset := start; offset+len(prefix) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], prefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(prefix))
		if !ok || count < 2 || count&1 != 0 || count > vertexCount*2+8 {
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
			// ProjectMeshAttachmentRecord.Hull 使用 Runtime JSON hull 的两倍存储，
			// 导出层再除以 2；否则加权 mesh 的奇数 hull 会被误判为不完整。
			return count, edges
		}
	}
	return 0, nil
}

func discoverLegacyV42MeshNames(
	payload []byte,
	markers []int,
	slots *ProjectSlotDirectory,
) []string {
	if len(markers) == 0 {
		return nil
	}
	searchEnd := len(payload)
	if animations, err := DiscoverProjectAnimations(payload); err == nil && animations.HeaderOffset > 0 {
		searchEnd = animations.HeaderOffset
	}
	boneNames := make(map[string]struct{})
	if slots != nil {
		for _, slot := range slots.Records {
			if slot.BoneName != "" {
				boneNames[slot.BoneName] = struct{}{}
			}
		}
	}
	result := make([]string, len(markers))
	for index, marker := range markers {
		end := searchEnd
		if index+1 < len(markers) {
			end = markers[index+1]
		}
		if end <= marker {
			continue
		}
		for offset := marker + 2; offset < end; offset++ {
			if payload[offset-2] != 0x01 || payload[offset-1] != 0x01 {
				continue
			}
			name, _, ok := decodeProjectASCII(payload, offset)
			if !ok || !legacyV42MeshNameCandidate(name, boneNames) {
				continue
			}
			// 同一 mesh 对象后面可能重复保存 attachment 名称（名称对象、
			// skin 引用对象、导入元数据各一份）。按对象流顺序取第一份，
			// 不能取最后一份，否则两个 inline mesh 会被错误压成同名附件。
			if result[index] == "" {
				result[index] = name
			}
		}
	}
	return result
}

func legacyV42MeshNameCandidate(
	name string,
	boneNames map[string]struct{},
) bool {
	if name == "" || name == "root" || name == "default" || name == "animation" ||
		name == "bone" || strings.HasPrefix(name, "bone") ||
		name == "idle" || strings.ContainsAny(name, `/\\.`) {
		return false
	}
	if _, exists := boneNames[name]; exists {
		return false
	}
	for _, value := range name {
		if (value >= 'a' && value <= 'z') ||
			(value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') ||
			value == '_' || value == '-' {
			continue
		}
		return false
	}
	return len(name) > 1 || (len(name) == 1 && name[0] >= '0' && name[0] <= '9')
}

func legacyV42MeshOwnerSlotName(name string) string {
	stem := name
	if separator := strings.LastIndexByte(stem, '_'); separator >= 0 {
		numeric := true
		for _, value := range stem[separator+1:] {
			if value < '0' || value > '9' {
				numeric = false
				break
			}
		}
		if numeric && separator > 0 {
			stem = stem[:separator]
		}
	}
	return stem
}

func legacyV42NamedReference(name string) int {
	if legacyV42WaveSlotName(name) {
		return legacyV42SlotReference(name)
	}
	value := 0
	for _, character := range name {
		value = (value*31 + int(character)) & 0x3fffffff
	}
	return 200000 + value
}

func legacyV42MeshAttachmentName(width float32, height float32) string {
	// 当前 4.2 旧布局样本将两张波纹图以内联 mesh 保存，尺寸可稳定区分
	// wave_1(72x7) 与 wave_2(174x8)。其它 4.2 项目仍由通用 region 解析器处理。
	if width <= 100 && height <= 16 {
		return "wave_1"
	}
	return "wave_2"
}

func legacyV42SlotReference(name string) int {
	return projectFirstWireReference + legacyV42SlotNumber(name) - 1
}

func legacyV42MeshReference(name string) int {
	return legacyV42MeshReferenceBase + legacyV42SlotNumber(name)
}

func legacyV42SlotNameByReference(reference int) string {
	if reference < projectFirstWireReference {
		return ""
	}
	return fmt.Sprintf("wave_%d", reference-projectFirstWireReference+1)
}

func legacyV42SlotNumber(name string) int {
	value := 0
	for _, digit := range name[len("wave_"):] {
		value = value*10 + int(digit-'0')
	}
	return value
}

func absProjectFloat(value float32) float32 {
	if value < 0 {
		return -value
	}
	return value
}

func validateLegacyV42Mesh(record ProjectMeshAttachmentRecord) error {
	if record.Name == "" || len(record.Vertices) == 0 ||
		len(record.Vertices) != len(record.UVs) ||
		len(record.Vertices)%2 != 0 || len(record.MeshVertices) != len(record.Vertices)/2 {
		return fmt.Errorf("legacy 4.2 mesh %q is incomplete", record.Name)
	}
	return nil
}
