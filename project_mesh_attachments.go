package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

var (
	projectMeshAttachmentV2Prefix = []byte{0x2b, 0x00, 0x06, 0x0f, 0x01, 0x01, 0x2e, 0x01}
	projectMeshVertexV2Prefix     = []byte{
		0x36, 0x01, 0x02, 0x00,
		0x11, 0x01, 0x01, 0x3f, 0x80, 0x00, 0x00,
		0x01, 0x11, 0x01, 0x02,
	}
)

// ProjectMeshVertexRecord contains one editor mesh point. Coordinates has two
// values per candidate weight.
type ProjectMeshVertexRecord struct {
	Weights     []float32 `json:"weights"`
	Coordinates []float32 `json:"coordinates"`
}

// ProjectMeshAttachmentRecord contains one proven unweighted 4.3.23 mesh.
type ProjectMeshAttachmentRecord struct {
	WireReference      int `json:"wireReference,omitempty"`
	OwnerSlotReference int `json:"ownerSlotReference,omitempty"`
	// TimelineSlotReference is the legacy 4.3 object reference serialized
	// after the mesh name. RGBA timelines point to this reference, not to the
	// runtime mesh wire reference.
	TimelineSlotReference int                        `json:"timelineSlotReference,omitempty"`
	Name                  string                     `json:"name"`
	NameReference         int                        `json:"nameReference,omitempty"`
	Path                  string                     `json:"path,omitempty"`
	PathReference         int                        `json:"pathReference,omitempty"`
	Offset                int                        `json:"offset"`
	Sequence              *ProjectAttachmentSequence `json:"sequence,omitempty"`
	UVs                   []float32                  `json:"uvs"`
	Triangles             []int                      `json:"triangles"`
	Vertices              []float32                  `json:"vertices,omitempty"`
	MeshVertices          []ProjectMeshVertexRecord  `json:"meshVertices"`
	Weighted              bool                       `json:"weighted"`
	// PreserveWeighted keeps an explicitly weighted legacy mesh weighted even
	// when every vertex has one full-weight influence on the same bone.
	PreserveWeighted    bool    `json:"preserveWeighted,omitempty"`
	CandidateBones      int     `json:"candidateBones"`
	BoneReferences      []int   `json:"boneReferences,omitempty"`
	BoneTableReferences []int   `json:"boneTableReferences,omitempty"`
	Hull                int     `json:"hull"`
	Edges               []int   `json:"edges,omitempty"`
	Width               float32 `json:"width"`
	Height              float32 `json:"height"`
	Color               [4]byte `json:"color,omitempty"`
	VertexList          int     `json:"vertexListOffset"`
	legacyOwnerToken    int
	legacyOwnerBoneName string
	// legacyV42CandidateBoneIndices 保存 4.2 token 串解析出的候选骨骼下标，
	// 供骨骼引用分配阶段优先采用；长度不足 CandidateBones 时视为未解析。
	legacyV42CandidateBoneIndices []int
	// legacyV42CandidateTokenBase 保存区段内 setup 对象的起始 token，
	// 用于把保存流尾部的骨骼顺序 token 列表映射回骨骼。
	legacyV42CandidateTokenBase int
}

// ProjectMeshAttachmentDirectory contains directly decoded unweighted meshes.
type ProjectMeshAttachmentDirectory struct {
	Format             string                        `json:"format"`
	Count              int                           `json:"count"`
	ReferencesComplete bool                          `json:"referencesComplete"`
	Records            []ProjectMeshAttachmentRecord `json:"records"`
}

// DiscoverProjectMeshAttachments decodes the strict unweighted mesh layout.
// Weighted and linked meshes remain fail-closed.
func DiscoverProjectMeshAttachments(
	payload []byte,
) (*ProjectMeshAttachmentDirectory, error) {
	records := make([]ProjectMeshAttachmentRecord, 0)
	vertexHeaderPrefix := []byte{0x47, 0x01, 0x03, 0x02, 0x0f, 0x01}
	for offset := 0; offset+len(vertexHeaderPrefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], vertexHeaderPrefix) {
			continue
		}
		record, ok := readProjectMeshAttachmentFromVertexHeader(payload, offset)
		if !ok {
			continue
		}
		records = append(records, record)
		offset = record.VertexList
	}
	resolveProjectMeshReferencedNames(payload, records)
	resolveProjectMeshReferencedPaths(records)
	resolvedRecords := records[:0]
	for _, record := range records {
		if record.Name != "" {
			resolvedRecords = append(resolvedRecords, record)
		}
	}
	records = resolvedRecords
	if len(records) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported unweighted mesh attachments were not found",
		}
	}
	referencesComplete := assignProjectMeshWireReferences(payload, records)
	return &ProjectMeshAttachmentDirectory{
		Format:             "kryo-unweighted-mesh-v2",
		Count:              len(records),
		ReferencesComplete: referencesComplete,
		Records:            records,
	}, nil
}

// assignProjectMeshWireReferences maps setup attachment object references to
// mesh objects in their Kryo creation order. It deliberately accepts only a
// complete one-to-one mesh set; mixed attachment classes require skin-map
// decoding and remain unsupported.
func assignProjectMeshWireReferences(
	payload []byte,
	records []ProjectMeshAttachmentRecord,
) bool {
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil || !slots.ReferencesComplete {
		return false
	}
	referencesBySlot := make(map[int]map[int]struct{}, len(slots.Records))
	allReferences := make(map[int]struct{}, len(records))
	setupReferenceBySlot := make(map[int]int, len(slots.Records))
	for _, slot := range slots.Records {
		if slot.SetupAttachmentClassID != ProjectAttachmentClassMesh ||
			slot.SetupAttachmentReference == 0 {
			continue
		}
		referencesBySlot[slot.WireReference] = map[int]struct{}{
			slot.SetupAttachmentReference: {},
		}
		setupReferenceBySlot[slot.WireReference] = slot.SetupAttachmentReference
		allReferences[slot.SetupAttachmentReference] = struct{}{}
	}
	animations, animationErr := DiscoverProjectAnimations(payload)
	if animationErr == nil {
		for _, animation := range animations.Records {
			if timelines, timelineErr := DiscoverProjectSlotAttachmentTimelines(
				payload,
				animation.Name,
			); timelineErr == nil {
				for _, timeline := range timelines.Timelines {
					for _, key := range timeline.Keys {
						if !key.HasAttachment ||
							key.AttachmentClassID != ProjectAttachmentClassMesh {
							continue
						}
						addProjectMeshReference(
							referencesBySlot,
							allReferences,
							timeline.SlotReference,
							key.AttachmentReference,
						)
					}
				}
			}
			if sequences, sequenceErr := DiscoverProjectSequenceTimelines(
				payload,
				animation.Name,
			); sequenceErr == nil {
				for _, timeline := range sequences.Timelines {
					if timeline.AttachmentClassID != ProjectAttachmentClassMesh {
						continue
					}
					addProjectMeshReference(
						referencesBySlot,
						allReferences,
						timeline.SlotReference,
						timeline.AttachmentReference,
					)
				}
			}
			if deforms, deformErr := DiscoverProjectDeformTimelines(
				payload,
				animation.Name,
			); deformErr == nil {
				for _, timeline := range deforms.Timelines {
					if timeline.AttachmentClassID == ProjectAttachmentClassMesh {
						allReferences[timeline.AttachmentReference] = struct{}{}
					}
				}
			}
		}
	}

	recordsByOwner := make(map[int][]int)
	ownerlessRecords := make([]int, 0, 1)
	for index, record := range records {
		if record.OwnerSlotReference != 0 {
			recordsByOwner[record.OwnerSlotReference] = append(
				recordsByOwner[record.OwnerSlotReference],
				index,
			)
		} else {
			ownerlessRecords = append(ownerlessRecords, index)
		}
	}
	usedReferences := make(map[int]struct{}, len(records))
	usedRecords := make(map[int]struct{}, len(records))
	for index, record := range records {
		if record.WireReference == 0 {
			continue
		}
		usedReferences[record.WireReference] = struct{}{}
		usedRecords[index] = struct{}{}
	}
	missingSetupOwners := make([]int, 0, len(ownerlessRecords))
	for owner := range setupReferenceBySlot {
		if len(recordsByOwner[owner]) == 0 {
			missingSetupOwners = append(missingSetupOwners, owner)
		}
	}
	if len(ownerlessRecords) > 0 &&
		len(missingSetupOwners) >= len(ownerlessRecords) {
		sort.Slice(missingSetupOwners, func(left int, right int) bool {
			return setupReferenceBySlot[missingSetupOwners[left]] <
				setupReferenceBySlot[missingSetupOwners[right]]
		})
		sort.Slice(ownerlessRecords, func(left int, right int) bool {
			return records[ownerlessRecords[left]].Offset <
				records[ownerlessRecords[right]].Offset
		})
		for index, recordIndex := range ownerlessRecords {
			owner := missingSetupOwners[index]
			reference := setupReferenceBySlot[owner]
			records[recordIndex].OwnerSlotReference = owner
			records[recordIndex].WireReference = reference
			usedReferences[reference] = struct{}{}
			usedRecords[recordIndex] = struct{}{}
		}
	}
	for owner, indices := range recordsByOwner {
		referenceSet := referencesBySlot[owner]
		if len(referenceSet) == 0 || len(referenceSet) != len(indices) {
			continue
		}
		references := make([]int, 0, len(referenceSet))
		for reference := range referenceSet {
			references = append(references, reference)
		}
		sort.Ints(references)
		sort.Slice(indices, func(left int, right int) bool {
			return records[indices[left]].Offset < records[indices[right]].Offset
		})
		for index, reference := range references {
			records[indices[index]].WireReference = reference
			usedReferences[reference] = struct{}{}
			usedRecords[indices[index]] = struct{}{}
		}
	}
	assignProjectMeshReferencesByCreationOrder(
		records,
		allReferences,
		usedReferences,
		usedRecords,
	)

	unmatchedReferences := make([]int, 0)
	for reference := range allReferences {
		if _, used := usedReferences[reference]; !used {
			unmatchedReferences = append(unmatchedReferences, reference)
		}
	}
	unmatchedRecords := make([]int, 0)
	for index := range records {
		if _, used := usedRecords[index]; !used {
			unmatchedRecords = append(unmatchedRecords, index)
		}
	}
	if len(unmatchedReferences) == len(unmatchedRecords) {
		sort.Ints(unmatchedReferences)
		sort.Slice(unmatchedRecords, func(left int, right int) bool {
			return records[unmatchedRecords[left]].Offset <
				records[unmatchedRecords[right]].Offset
		})
		for index, reference := range unmatchedReferences {
			recordIndex := unmatchedRecords[index]
			records[recordIndex].WireReference = reference
		}
	}
	assignProjectMeshOwnersByReferenceEvidence(records, referencesBySlot)

	for _, record := range records {
		if record.WireReference == 0 {
			return false
		}
	}
	return len(records) != 0
}

func assignProjectMeshReferencesByCreationOrder(
	records []ProjectMeshAttachmentRecord,
	allReferences map[int]struct{},
	usedReferences map[int]struct{},
	usedRecords map[int]struct{},
) {
	for recordIndex := range records {
		if records[recordIndex].WireReference != 0 {
			continue
		}
		lowerBound := 0
		for previous := recordIndex - 1; previous >= 0; previous-- {
			if records[previous].WireReference != 0 {
				lowerBound = records[previous].WireReference
				break
			}
		}
		upperBound := int(^uint(0) >> 1)
		for next := recordIndex + 1; next < len(records); next++ {
			if records[next].WireReference != 0 {
				upperBound = records[next].WireReference
				break
			}
		}
		candidate := 0
		for reference := range allReferences {
			if reference <= lowerBound || reference >= upperBound {
				continue
			}
			if _, used := usedReferences[reference]; used {
				continue
			}
			if candidate != 0 {
				candidate = 0
				break
			}
			candidate = reference
		}
		if candidate == 0 {
			continue
		}
		records[recordIndex].WireReference = candidate
		usedReferences[candidate] = struct{}{}
		usedRecords[recordIndex] = struct{}{}
	}
}

func assignProjectMeshOwnersByReferenceEvidence(
	records []ProjectMeshAttachmentRecord,
	referencesBySlot map[int]map[int]struct{},
) {
	for recordIndex := range records {
		record := &records[recordIndex]
		if record.OwnerSlotReference != 0 || record.WireReference == 0 {
			continue
		}
		owner := 0
		for slotReference, references := range referencesBySlot {
			if _, exists := references[record.WireReference]; !exists {
				continue
			}
			if owner != 0 {
				owner = 0
				break
			}
			owner = slotReference
		}
		record.OwnerSlotReference = owner
	}
}

func addProjectMeshReference(
	referencesBySlot map[int]map[int]struct{},
	allReferences map[int]struct{},
	slotReference int,
	attachmentReference int,
) {
	if slotReference == 0 || attachmentReference == 0 {
		return
	}
	references := referencesBySlot[slotReference]
	if references == nil {
		references = make(map[int]struct{})
		referencesBySlot[slotReference] = references
	}
	references[attachmentReference] = struct{}{}
	allReferences[attachmentReference] = struct{}{}
}

func readProjectMeshAttachmentFromVertexHeader(
	payload []byte,
	vertexHeader int,
) (ProjectMeshAttachmentRecord, bool) {
	record := ProjectMeshAttachmentRecord{VertexList: vertexHeader + 6}
	triangleOffset := bytes.LastIndex(
		payload[:vertexHeader],
		[]byte{0x0d, 0x2f, 0x01},
	)
	if triangleOffset < 0 {
		return record, false
	}
	uvOffset := -1
	uvCount := 0
	uvCursor := 0
	searchStart := triangleOffset - 8_000_000
	if searchStart < 0 {
		searchStart = 0
	}
	for candidate := searchStart; candidate+3 < triangleOffset; candidate++ {
		if payload[candidate] != 0x11 || payload[candidate+1] != 0x01 {
			continue
		}
		count, next, ok := readPositiveVarint(payload, candidate+2)
		if ok && count >= 2 && count&1 == 0 &&
			next+count*4 == triangleOffset {
			uvOffset = candidate
			uvCount = count
			uvCursor = next
		}
	}
	if uvOffset < 0 {
		return record, false
	}
	record.Offset = uvOffset
	path, pathReference, pathOK := readProjectMeshAttachmentPath(
		payload,
		uvOffset,
	)
	if !pathOK {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Path = path
	record.PathReference = pathReference
	record.UVs = make([]float32, uvCount)
	for index := range record.UVs {
		value := readProjectFloat32(payload, uvCursor+index*4)
		if !finiteProjectFloat(value) || value < 0 || value > 1 {
			return ProjectMeshAttachmentRecord{}, false
		}
		record.UVs[index] = value
	}

	triangleCount, cursor, ok := readPositiveVarint(payload, triangleOffset+3)
	if !ok || triangleCount < 3 || cursor+triangleCount*2 > vertexHeader {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Triangles = make([]int, triangleCount)
	for index := range record.Triangles {
		record.Triangles[index] = int(binary.BigEndian.Uint16(
			payload[cursor+index*2:],
		))
	}
	cursor += triangleCount * 2
	if cursor+5 > vertexHeader || payload[cursor] != 0x10 {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Width = readProjectFloat32(payload, cursor+1)
	heightTag := findProjectTag(payload, cursor+5, vertexHeader, 0x11, 32)
	if heightTag < 0 || heightTag+5 > vertexHeader {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Height = readProjectFloat32(payload, heightTag+1)
	if !finiteProjectFloat(record.Width) || !finiteProjectFloat(record.Height) ||
		record.Width < 0 || record.Height < 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Sequence = readProjectAttachmentSequence(
		payload,
		record.Offset,
		vertexHeader,
		0x1c,
	)

	vertexCount, cursor, ok := readPositiveVarint(payload, vertexHeader+6)
	if !ok || vertexCount < 1 || vertexCount*2 != uvCount {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.MeshVertices = make([]ProjectMeshVertexRecord, 0, vertexCount)
	record.Vertices = make([]float32, 0, uvCount)
	for index := 0; index < vertexCount; index++ {
		vertex, next, vertexOK := readProjectMeshVertex(payload, cursor)
		if !vertexOK {
			return ProjectMeshAttachmentRecord{}, false
		}
		record.MeshVertices = append(record.MeshVertices, vertex)
		if len(vertex.Weights) == 1 && len(vertex.Coordinates) == 2 &&
			vertex.Weights[0] == 1 {
			record.Vertices = append(record.Vertices, vertex.Coordinates...)
		} else {
			record.Weighted = true
		}
		if len(vertex.Weights) > record.CandidateBones {
			record.CandidateBones = len(vertex.Weights)
		}
		cursor = next
	}
	if record.Weighted {
		record.Vertices = nil
		tableReferences, next, tableOK := readProjectMeshBoneReferenceTable(
			payload,
			cursor,
		)
		if tableOK && len(tableReferences) == record.CandidateBones &&
			projectMeshVerticesUseTableWidth(record.MeshVertices, len(tableReferences)) {
			record.BoneTableReferences = tableReferences
			record.BoneReferences = append([]int(nil), tableReferences...)
			cursor = next
		}
	}
	nameTag := bytes.Index(payload[cursor:], []byte{0x05, 0x01, 0x01})
	if nameTag < 0 || nameTag > 4096 {
		return ProjectMeshAttachmentRecord{}, false
	}
	nameTag += cursor
	nameValue := nameTag + 3
	if nameValue >= len(payload) {
		return ProjectMeshAttachmentRecord{}, false
	}
	nameEnd := nameValue
	if payload[nameValue] == 0x01 {
		name, inlineEnd, nameOK := decodeProjectASCII(payload, nameValue+1)
		if !nameOK {
			return ProjectMeshAttachmentRecord{}, false
		}
		record.Name = name
		nameEnd = inlineEnd
	} else {
		nameReference, referenceEnd, referenceOK := readPositiveVarint(
			payload,
			nameValue,
		)
		if !referenceOK || nameReference < projectFirstWireReference {
			return ProjectMeshAttachmentRecord{}, false
		}
		record.NameReference = nameReference
		nameEnd = referenceEnd
	}
	record.WireReference = readProjectMeshAttachmentWireReference(
		payload,
		cursor,
		nameTag,
	)
	if record.WireReference == 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.OwnerSlotReference = readProjectDefaultAttachmentOwnerReference(
		payload,
		nameEnd,
	)
	record.Hull, record.Edges = readProjectMeshTopologyMetadata(
		payload,
		cursor,
		nameTag,
	)
	if record.Hull == 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	return record, true
}

func readProjectMeshAttachmentPath(
	payload []byte,
	uvOffset int,
) (string, int, bool) {
	if uvOffset < 3 || uvOffset > len(payload) ||
		payload[uvOffset-1] != 0x0c {
		return "", 0, false
	}
	fieldEnd := uvOffset - 1
	searchStart := fieldEnd - 80
	if searchStart < 0 {
		searchStart = 0
	}
	for offset := fieldEnd - 1; offset >= searchStart; offset-- {
		if payload[offset] != 0x0e || offset+1 >= fieldEnd {
			continue
		}
		valueOffset := offset + 1
		if payload[valueOffset] == 0x00 &&
			valueOffset+1 == fieldEnd {
			return "", 0, true
		}
		if payload[valueOffset] == 0x01 {
			if valueOffset+2 == fieldEnd &&
				payload[valueOffset+1] == 0x81 {
				return "", 0, true
			}
			path, next, ok := decodeProjectASCII(
				payload,
				valueOffset+1,
			)
			if ok && next == fieldEnd {
				return path, 0, true
			}
			path, ok = decodeProjectShortASCII(
				payload,
				valueOffset+1,
			)
			if ok && valueOffset+2+len(path) == fieldEnd {
				return path, 0, true
			}
			continue
		}
		reference, next, ok := readPositiveVarint(payload, valueOffset)
		if ok && reference >= projectFirstWireReference &&
			next == fieldEnd {
			return "", reference, true
		}
	}
	return "", 0, false
}

func readProjectMeshAttachmentWireReference(
	payload []byte,
	start int,
	end int,
) int {
	metadataPrefix := []byte{0x1f, 0x1e, 0x01}
	relative := bytes.LastIndex(payload[start:end], metadataPrefix)
	if relative < 0 {
		return 0
	}
	metadata := start + relative
	searchStart := metadata - 6
	if searchStart < start {
		searchStart = start
	}
	for offset := searchStart; offset < metadata; offset++ {
		if payload[offset] != ProjectAttachmentClassMesh {
			continue
		}
		reference, next, ok := readPositiveVarint(payload, offset+1)
		if ok && reference >= projectFirstWireReference && next == metadata {
			return reference
		}
	}
	return 0
}

func resolveProjectMeshReferencedNames(
	payload []byte,
	records []ProjectMeshAttachmentRecord,
) {
	for index := range records {
		if records[index].Name != "" || records[index].NameReference == 0 {
			continue
		}
		name := ""
		ambiguous := false
		for _, candidate := range records {
			if candidate.Name == "" ||
				candidate.Width != records[index].Width ||
				candidate.Height != records[index].Height ||
				!projectFloatSlicesEqual(candidate.UVs, records[index].UVs) {
				continue
			}
			if name != "" && name != candidate.Name {
				ambiguous = true
				break
			}
			name = candidate.Name
		}
		if !ambiguous {
			records[index].Name = name
		}
	}
	namesByReference := make(map[int]string)
	for _, record := range records {
		if record.Name == "" || record.NameReference == 0 {
			continue
		}
		previous := namesByReference[record.NameReference]
		if previous != "" && previous != record.Name {
			delete(namesByReference, record.NameReference)
			continue
		}
		namesByReference[record.NameReference] = record.Name
	}
	for index := range records {
		if records[index].Name == "" {
			records[index].Name = namesByReference[records[index].NameReference]
		}
	}
	resolveProjectMeshNamesByReferenceOrder(records)
	resolveProjectMeshNamesFromDirectBoneStrings(payload, records)
	resolveProjectMeshNamesFromSlotReferences(payload, records)
}

func resolveProjectMeshNamesFromSlotReferences(
	payload []byte,
	records []ProjectMeshAttachmentRecord,
) {
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil {
		return
	}
	bindProjectMeshNamesFromSlotReferences(slots, records)
}

func bindProjectMeshNamesFromSlotReferences(
	slots *ProjectSlotDirectory,
	records []ProjectMeshAttachmentRecord,
) {
	namesByReference := make(map[int]string)
	ambiguousReferences := make(map[int]struct{})
	for _, slot := range slots.Records {
		if slot.NameReference == 0 || slot.Name == "" {
			continue
		}
		previous := namesByReference[slot.NameReference]
		if previous != "" && previous != slot.Name {
			delete(namesByReference, slot.NameReference)
			ambiguousReferences[slot.NameReference] = struct{}{}
			continue
		}
		if _, ambiguous := ambiguousReferences[slot.NameReference]; !ambiguous {
			namesByReference[slot.NameReference] = slot.Name
		}
	}
	for index := range records {
		name := namesByReference[records[index].NameReference]
		if name != "" {
			records[index].Name = name
		}
	}
}

func resolveProjectMeshReferencedPaths(
	records []ProjectMeshAttachmentRecord,
) {
	type geometryGroup struct {
		indices []int
	}
	groups := make([]geometryGroup, 0)
	for index := range records {
		groupIndex := -1
		for candidateIndex, group := range groups {
			if projectMeshNameGeometryEqual(
				records[index],
				records[group.indices[0]],
			) {
				groupIndex = candidateIndex
				break
			}
		}
		if groupIndex < 0 {
			groups = append(groups, geometryGroup{indices: []int{index}})
		} else {
			groups[groupIndex].indices = append(
				groups[groupIndex].indices,
				index,
			)
		}
	}
	for _, group := range groups {
		paths := make([]string, 0)
		seenPaths := make(map[string]struct{})
		references := make([]int, 0)
		seenReferences := make(map[int]struct{})
		for _, index := range group.indices {
			record := records[index]
			if record.PathReference == 0 && record.Path != "" {
				if _, seen := seenPaths[record.Path]; !seen {
					paths = append(paths, record.Path)
					seenPaths[record.Path] = struct{}{}
				}
			}
			if record.PathReference != 0 {
				if _, seen := seenReferences[record.PathReference]; !seen {
					references = append(references, record.PathReference)
					seenReferences[record.PathReference] = struct{}{}
				}
			}
		}
		if len(paths) == 0 || len(paths) != len(references) {
			continue
		}
		sort.Ints(references)
		for index, reference := range references {
			for _, recordIndex := range group.indices {
				if records[recordIndex].PathReference == reference {
					records[recordIndex].Path = paths[index]
				}
			}
		}
	}
}

func resolveProjectMeshNamesFromDirectBoneStrings(
	payload []byte,
	records []ProjectMeshAttachmentRecord,
) {
	bones, err := DiscoverProjectBones(payload)
	if err != nil || !bones.ReferencesComplete || len(bones.Records) == 0 {
		return
	}
	namesByReference := make(map[int]string, len(bones.Records))
	for index, bone := range bones.Records {
		if index > 0 &&
			bone.WireReference != bones.Records[index-1].WireReference+1 {
			return
		}
		nameReference := bone.WireReference*3 + 1
		namesByReference[nameReference] = bone.Name
	}
	for _, record := range records {
		name := namesByReference[record.NameReference]
		if name != "" && record.Name != "" && record.Name != name {
			delete(namesByReference, record.NameReference)
		}
	}
	for index := range records {
		if records[index].Name == "" {
			records[index].Name = namesByReference[records[index].NameReference]
		}
	}
}

func resolveProjectMeshNamesByReferenceOrder(
	records []ProjectMeshAttachmentRecord,
) {
	type geometryGroup struct {
		indices []int
	}
	groups := make([]geometryGroup, 0)
	for index := range records {
		groupIndex := -1
		for candidateIndex, group := range groups {
			if projectMeshNameGeometryEqual(
				records[index],
				records[group.indices[0]],
			) {
				groupIndex = candidateIndex
				break
			}
		}
		if groupIndex < 0 {
			groups = append(groups, geometryGroup{indices: []int{index}})
		} else {
			groups[groupIndex].indices = append(
				groups[groupIndex].indices,
				index,
			)
		}
	}
	for _, group := range groups {
		inlineNames := make([]string, 0)
		seenNames := make(map[string]struct{})
		references := make([]int, 0)
		seenReferences := make(map[int]struct{})
		for _, index := range group.indices {
			record := records[index]
			if record.NameReference == 0 && record.Name != "" {
				if _, seen := seenNames[record.Name]; !seen {
					inlineNames = append(inlineNames, record.Name)
					seenNames[record.Name] = struct{}{}
				}
			}
			if record.NameReference != 0 {
				if _, seen := seenReferences[record.NameReference]; !seen {
					references = append(references, record.NameReference)
					seenReferences[record.NameReference] = struct{}{}
				}
			}
		}
		if len(inlineNames) == 0 || len(inlineNames) != len(references) {
			continue
		}
		sort.Ints(references)
		valid := true
		for index, reference := range references {
			for _, recordIndex := range group.indices {
				record := records[recordIndex]
				if record.NameReference == reference && record.Name != "" &&
					record.Name != inlineNames[index] {
					valid = false
				}
			}
		}
		if !valid {
			continue
		}
		for index, reference := range references {
			for _, recordIndex := range group.indices {
				if records[recordIndex].NameReference == reference {
					records[recordIndex].Name = inlineNames[index]
				}
			}
		}
	}
}

func projectMeshNameGeometryEqual(
	left ProjectMeshAttachmentRecord,
	right ProjectMeshAttachmentRecord,
) bool {
	return left.Width == right.Width &&
		left.Height == right.Height &&
		left.Hull == right.Hull &&
		projectFloatSlicesEqual(left.UVs, right.UVs) &&
		projectIntSlicesEqual(left.Triangles, right.Triangles) &&
		projectMeshVertexSlicesEqual(left.MeshVertices, right.MeshVertices)
}

func projectIntSlicesEqual(left []int, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func projectMeshVertexSlicesEqual(
	left []ProjectMeshVertexRecord,
	right []ProjectMeshVertexRecord,
) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !projectFloatSlicesEqual(left[index].Weights, right[index].Weights) ||
			!projectFloatSlicesEqual(
				left[index].Coordinates,
				right[index].Coordinates,
			) {
			return false
		}
	}
	return true
}

func projectFloatSlicesEqual(left []float32, right []float32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// readProjectMeshBoneReferenceTable reads a table only at the exact object
// boundary after a verified weighted vertex list. Scanning later attachment
// metadata is unsafe because it contains unrelated Kryo lists with the same
// byte shape.
func readProjectMeshBoneReferenceTable(
	payload []byte,
	offset int,
) ([]int, int, bool) {
	prefix := []byte{0x01, 0x0f, 0x01}
	if offset < 0 || offset+len(prefix) >= len(payload) ||
		!bytes.HasPrefix(payload[offset:], prefix) {
		return nil, offset, false
	}
	count, cursor, ok := readPositiveVarint(payload, offset+len(prefix))
	if !ok || count < 1 || count > 100_000 {
		return nil, offset, false
	}
	references := make([]int, count)
	for index := range references {
		if cursor >= len(payload) || payload[cursor] != 0x0c {
			return nil, offset, false
		}
		var referenceOK bool
		references[index], cursor, referenceOK = readPositiveVarint(
			payload,
			cursor+1,
		)
		if !referenceOK || references[index] < projectFirstWireReference {
			return nil, offset, false
		}
	}
	if cursor >= len(payload) || payload[cursor] != 0 {
		return nil, offset, false
	}
	return references, cursor + 1, true
}

func projectMeshVerticesUseTableWidth(
	vertices []ProjectMeshVertexRecord,
	width int,
) bool {
	if width < 1 || len(vertices) == 0 {
		return false
	}
	for _, vertex := range vertices {
		if len(vertex.Weights) != width || len(vertex.Coordinates) != width*2 {
			return false
		}
	}
	return true
}

func readProjectMeshVertex(
	payload []byte,
	offset int,
) (ProjectMeshVertexRecord, int, bool) {
	vertex := ProjectMeshVertexRecord{}
	prefix := []byte{0x36, 0x01, 0x02, 0x00, 0x11, 0x01}
	if offset+len(prefix) >= len(payload) ||
		!bytes.HasPrefix(payload[offset:], prefix) {
		return vertex, offset, false
	}
	weightCount, cursor, ok := readPositiveVarint(payload, offset+len(prefix))
	if !ok || weightCount < 1 || weightCount > 10_000 ||
		cursor+weightCount*4 > len(payload) {
		return vertex, offset, false
	}
	vertex.Weights = make([]float32, weightCount)
	for index := range vertex.Weights {
		vertex.Weights[index] = readProjectFloat32(payload, cursor+index*4)
		if !finiteProjectFloat(vertex.Weights[index]) {
			return ProjectMeshVertexRecord{}, offset, false
		}
	}
	cursor += weightCount * 4
	coordinatePrefix := []byte{0x01, 0x11, 0x01}
	if cursor+len(coordinatePrefix) >= len(payload) ||
		!bytes.HasPrefix(payload[cursor:], coordinatePrefix) {
		return ProjectMeshVertexRecord{}, offset, false
	}
	coordinateCount, next, ok := readPositiveVarint(
		payload,
		cursor+len(coordinatePrefix),
	)
	if !ok || coordinateCount != weightCount*2 ||
		next+coordinateCount*4 > len(payload) {
		return ProjectMeshVertexRecord{}, offset, false
	}
	vertex.Coordinates = make([]float32, coordinateCount)
	for index := range vertex.Coordinates {
		vertex.Coordinates[index] = readProjectFloat32(payload, next+index*4)
		if !finiteProjectFloat(vertex.Coordinates[index]) {
			return ProjectMeshVertexRecord{}, offset, false
		}
	}
	return vertex, next + coordinateCount*4, true
}

func readProjectMeshTopologyMetadata(
	payload []byte,
	start int,
	end int,
) (int, []int) {
	hull := readProjectMeshHullBeforeName(payload, start, end)
	var edges []int
	edgeHeader := bytes.Index(payload[start:end], []byte{0x08, 0x10, 0x01})
	if edgeHeader >= 0 {
		edgeCount, edgeCursor, ok := readPositiveVarint(
			payload,
			start+edgeHeader+3,
		)
		if ok {
			edges, _ = readProjectMeshEdges(
				payload,
				edgeCursor,
				end,
				edgeCount,
			)
		}
	}
	return hull, edges
}

func readProjectMeshEdges(
	payload []byte,
	cursor int,
	end int,
	count int,
) ([]int, bool) {
	if count < 0 || cursor < 0 || cursor > end || end > len(payload) {
		return nil, false
	}
	edges := make([]int, count)
	for index := range edges {
		value, next, ok := readPositiveVarint(payload, cursor)
		if !ok || next > end {
			return nil, false
		}
		edges[index] = value
		cursor = next
	}
	return edges, true
}

func readProjectMeshHullBeforeName(
	payload []byte,
	start int,
	nameTag int,
) int {
	if start < 0 || nameTag > len(payload) || start >= nameTag {
		return 0
	}
	hull := 0
	matches := 0
	for candidate := start; candidate < nameTag; candidate++ {
		if payload[candidate] != 0x07 {
			continue
		}
		value, next, ok := readPositiveVarint(payload, candidate+1)
		if !ok || next != nameTag || value == 0 || value&1 != 0 {
			continue
		}
		hull = value >> 1
		matches++
	}
	if matches != 1 {
		return 0
	}
	return hull
}

func readProjectMeshAttachmentV2(
	payload []byte,
	offset int,
) (ProjectMeshAttachmentRecord, bool) {
	record := ProjectMeshAttachmentRecord{Offset: offset}
	cursor := offset + len(projectMeshAttachmentV2Prefix)
	uvTag := bytes.Index(payload[cursor:], []byte{0x11, 0x01})
	if uvTag < 0 || uvTag > 32 {
		return record, false
	}
	cursor += uvTag + 2
	uvCount, next, ok := readPositiveVarint(payload, cursor)
	if !ok || uvCount < 2 || uvCount > 2_000_000 || uvCount&1 != 0 ||
		next+uvCount*4 > len(payload) {
		return record, false
	}
	record.UVs = make([]float32, uvCount)
	cursor = next
	for index := range record.UVs {
		record.UVs[index] = readProjectFloat32(payload, cursor+index*4)
		if !finiteProjectFloat(record.UVs[index]) ||
			record.UVs[index] < 0 ||
			record.UVs[index] > 1 {
			return ProjectMeshAttachmentRecord{}, false
		}
	}
	cursor += uvCount * 4
	if cursor+3 > len(payload) ||
		payload[cursor] != 0x0d ||
		payload[cursor+1] != 0x2f ||
		payload[cursor+2] != 0x01 {
		return ProjectMeshAttachmentRecord{}, false
	}
	triangleCount, next, ok := readPositiveVarint(payload, cursor+3)
	if !ok || triangleCount < 3 || triangleCount > 3_000_000 ||
		next+triangleCount*2 > len(payload) {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Triangles = make([]int, triangleCount)
	cursor = next
	for index := range record.Triangles {
		record.Triangles[index] = int(binary.BigEndian.Uint16(
			payload[cursor+index*2:],
		))
	}
	cursor += triangleCount * 2
	if cursor+5 > len(payload) || payload[cursor] != 0x10 {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Width = readProjectFloat32(payload, cursor+1)
	heightTag := findProjectTag(payload, cursor+5, len(payload), 0x11, 16)
	if heightTag < 0 || heightTag+5 > len(payload) {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Height = readProjectFloat32(payload, heightTag+1)
	if !finiteProjectFloat(record.Width) || !finiteProjectFloat(record.Height) ||
		record.Width < 0 || record.Height < 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	vertexHeader := bytes.Index(
		payload[heightTag+5:],
		[]byte{0x47, 0x01, 0x03, 0x02, 0x0f, 0x01},
	)
	if vertexHeader < 0 || vertexHeader > 64 {
		return ProjectMeshAttachmentRecord{}, false
	}
	cursor = heightTag + 5 + vertexHeader + 6
	vertexCount, next, ok := readPositiveVarint(payload, cursor)
	if !ok || vertexCount*2 != uvCount || vertexCount > 1_000_000 {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.VertexList = cursor
	record.Sequence = readProjectAttachmentSequence(
		payload,
		record.Offset,
		record.VertexList,
		0x1c,
	)
	record.Vertices = make([]float32, 0, uvCount)
	cursor = next
	for index := 0; index < vertexCount; index++ {
		if cursor+len(projectMeshVertexV2Prefix)+8 > len(payload) ||
			!bytes.HasPrefix(payload[cursor:], projectMeshVertexV2Prefix) {
			return ProjectMeshAttachmentRecord{}, false
		}
		valueOffset := cursor + len(projectMeshVertexV2Prefix)
		x := readProjectFloat32(payload, valueOffset)
		y := readProjectFloat32(payload, valueOffset+4)
		if !finiteProjectFloat(x) || !finiteProjectFloat(y) {
			return ProjectMeshAttachmentRecord{}, false
		}
		record.Vertices = append(record.Vertices, x, y)
		cursor = valueOffset + 8
	}
	nameTag := bytes.Index(payload[cursor:], []byte{0x05, 0x01, 0x01, 0x01})
	if nameTag < 0 || nameTag > 1024 {
		return ProjectMeshAttachmentRecord{}, false
	}
	name, nameEnd, ok := decodeProjectASCII(payload, cursor+nameTag+4)
	if !ok {
		return ProjectMeshAttachmentRecord{}, false
	}
	record.Name = name
	record.OwnerSlotReference = readProjectDefaultAttachmentOwnerReference(
		payload,
		nameEnd,
	)

	record.Hull = readProjectMeshHullBeforeName(
		payload,
		cursor,
		cursor+nameTag,
	)
	edgeHeader := bytes.Index(payload[cursor:cursor+nameTag], []byte{0x08, 0x10, 0x01})
	if edgeHeader >= 0 {
		edgeCount, edgeCursor, edgeOK := readPositiveVarint(
			payload,
			cursor+edgeHeader+3,
		)
		if edgeOK {
			record.Edges, _ = readProjectMeshEdges(
				payload,
				edgeCursor,
				cursor+nameTag,
				edgeCount,
			)
		}
	}
	if record.Hull == 0 {
		return ProjectMeshAttachmentRecord{}, false
	}
	return record, true
}

func readProjectDefaultAttachmentOwnerReference(
	payload []byte,
	offset int,
) int {
	prefix := []byte{0x21, 0x01, 0x20, 0x00, 0x23, 0x00, 0x00}
	if offset < 0 || offset+len(prefix) >= len(payload) ||
		!bytes.HasPrefix(payload[offset:], prefix) {
		return 0
	}
	reference, next, ok := readPositiveVarint(payload, offset+len(prefix))
	if !ok || reference < projectFirstWireReference ||
		next+2 > len(payload) || payload[next] != 0x04 ||
		payload[next+1] != 0x00 {
		return 0
	}
	return reference
}

// MeshAttachmentNameByIndex returns one decoded mesh name.
func (directory *ProjectMeshAttachmentDirectory) MeshAttachmentNameByIndex(
	index int,
) (string, error) {
	if directory == nil || index < 0 || index >= len(directory.Records) {
		return "", fmt.Errorf("mesh attachment index %d is out of range", index)
	}
	return directory.Records[index].Name, nil
}
