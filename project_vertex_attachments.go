package spineparser

import (
	"bytes"
	"fmt"
)

// ProjectVertexAttachmentRecord contains one path or clipping attachment.
type ProjectVertexAttachmentRecord struct {
	ClassID             int                       `json:"classId"`
	WireReference       int                       `json:"wireReference"`
	OwnerSlotReference  int                       `json:"ownerSlotReference"`
	EndSlotReference    int                       `json:"endSlotReference,omitempty"`
	EndSlotName         string                    `json:"endSlotName,omitempty"`
	Name                string                    `json:"name"`
	NameReference       int                       `json:"nameReference,omitempty"`
	Offset              int                       `json:"offset"`
	Color               [4]byte                   `json:"color"`
	Lengths             []float32                 `json:"lengths,omitempty"`
	Closed              bool                      `json:"closed,omitempty"`
	ConstantSpeed       bool                      `json:"constantSpeed"`
	Convex              bool                      `json:"convex,omitempty"`
	Vertices            []float32                 `json:"vertices,omitempty"`
	MeshVertices        []ProjectMeshVertexRecord `json:"meshVertices"`
	Weighted            bool                      `json:"weighted"`
	CandidateBones      int                       `json:"candidateBones"`
	BoneReferences      []int                     `json:"boneReferences,omitempty"`
	BoneTableReferences []int                     `json:"boneTableReferences,omitempty"`
}

// ProjectVertexAttachmentDirectory contains proven path and clipping records.
type ProjectVertexAttachmentDirectory struct {
	Format             string                          `json:"format"`
	Count              int                             `json:"count"`
	ReferencesComplete bool                            `json:"referencesComplete"`
	Records            []ProjectVertexAttachmentRecord `json:"records"`
}

// DiscoverProjectVertexAttachments decodes path and clipping attachments.
func DiscoverProjectVertexAttachments(
	payload []byte,
) (*ProjectVertexAttachmentDirectory, error) {
	records := make([]ProjectVertexAttachmentRecord, 0)
	for offset := 0; offset+5 < len(payload); offset++ {
		classID := int(payload[offset])
		if classID != ProjectAttachmentClassPath &&
			classID != ProjectAttachmentClassClipping {
			continue
		}
		reference, metadata, ok := readPositiveVarint(payload, offset+1)
		if !ok || reference < projectFirstWireReference ||
			metadata+7 > len(payload) ||
			!bytes.Equal(
				payload[metadata:metadata+3],
				[]byte{0x1f, 0x1e, 0x01},
			) {
			continue
		}
		record, readOK := readProjectVertexAttachment(
			payload,
			offset,
			metadata,
			classID,
			reference,
		)
		if !readOK {
			continue
		}
		records = append(records, record)
		offset = metadata + 2
	}
	if len(records) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported project vertex attachments were not found",
		}
	}
	if !resolveProjectVertexAttachmentNames(records) {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "vertex attachment name references are ambiguous",
		}
	}
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil || !slots.ReferencesComplete {
		if err != nil {
			return nil, err
		}
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "vertex attachments require complete slot references",
		}
	}
	referencesComplete := true
	for index := range records {
		owner := 0
		for _, slot := range slots.Records {
			if slot.SetupAttachmentClassID != records[index].ClassID ||
				slot.SetupAttachmentReference !=
					records[index].WireReference {
				continue
			}
			if owner != 0 && owner != slot.WireReference {
				owner = 0
				break
			}
			owner = slot.WireReference
		}
		if records[index].OwnerSlotReference != 0 {
			if owner != 0 && owner != records[index].OwnerSlotReference {
				referencesComplete = false
			}
			owner = records[index].OwnerSlotReference
		}
		records[index].OwnerSlotReference = owner
		if owner == 0 {
			referencesComplete = false
		}
		if records[index].ClassID == ProjectAttachmentClassClipping {
			ownerIndex := indexOfProjectSlotReference(slots.Records, owner)
			if ownerIndex < 0 {
				referencesComplete = false
				continue
			}
			endSlotIndex, found := readProjectClippingEndSlotIndex(
				payload,
				records[index].WireReference,
				slots.Records[ownerIndex],
				len(slots.Records),
			)
			if !found || endSlotIndex < 0 ||
				endSlotIndex >= len(slots.Records) {
				referencesComplete = false
			} else {
				endSlot := slots.Records[endSlotIndex]
				records[index].EndSlotReference = endSlot.WireReference
				records[index].EndSlotName = endSlot.Name
			}
		}
	}
	return &ProjectVertexAttachmentDirectory{
		Format:             "kryo-vertex-attachment-v2",
		Count:              len(records),
		ReferencesComplete: referencesComplete,
		Records:            records,
	}, nil
}

func readProjectVertexAttachment(
	payload []byte,
	classOffset int,
	metadata int,
	classID int,
	reference int,
) (ProjectVertexAttachmentRecord, bool) {
	record := ProjectVertexAttachmentRecord{
		ClassID:       classID,
		WireReference: reference,
		Offset:        classOffset,
		ConstantSpeed: true,
	}
	copy(record.Color[:], payload[metadata+3:metadata+7])
	collectionOffset, vertices, vertexEnd, ok :=
		readProjectVertexCollectionBefore(payload, classOffset)
	if !ok {
		return ProjectVertexAttachmentRecord{}, false
	}
	record.MeshVertices = vertices
	record.Vertices = make([]float32, 0, len(vertices)*2)
	for _, vertex := range vertices {
		if len(vertex.Weights) == 1 &&
			len(vertex.Coordinates) == 2 &&
			vertex.Weights[0] == 1 {
			record.Vertices = append(record.Vertices, vertex.Coordinates...)
		} else {
			record.Weighted = true
		}
		if len(vertex.Weights) > record.CandidateBones {
			record.CandidateBones = len(vertex.Weights)
		}
	}
	if record.Weighted {
		record.Vertices = nil
	}
	tableReferences, tableEnd, tableOK :=
		readProjectMeshBoneReferenceTable(payload, vertexEnd)
	if !tableOK || tableEnd != classOffset {
		return ProjectVertexAttachmentRecord{}, false
	}
	record.BoneTableReferences = tableReferences
	record.BoneReferences = append([]int(nil), tableReferences...)

	switch classID {
	case ProjectAttachmentClassPath:
		lengths, closed, constantSpeed, pathOK :=
			readProjectPathAttachmentMetadata(
				payload,
				collectionOffset,
			)
		if !pathOK {
			return ProjectVertexAttachmentRecord{}, false
		}
		record.Lengths = lengths
		record.Closed = closed
		record.ConstantSpeed = constantSpeed
	case ProjectAttachmentClassClipping:
		convex, clippingOK := readProjectClippingAttachmentMetadata(
			payload,
			collectionOffset,
		)
		if !clippingOK {
			return ProjectVertexAttachmentRecord{}, false
		}
		record.Convex = convex
	}

	name, nameReference, nameEnd, nameOK :=
		readProjectVertexAttachmentName(payload, metadata+7)
	if !nameOK {
		return ProjectVertexAttachmentRecord{}, false
	}
	record.Name = name
	record.NameReference = nameReference
	fieldReference := readProjectDefaultAttachmentOwnerReference(
		payload,
		nameEnd,
	)
	record.OwnerSlotReference = fieldReference
	if fieldReference == 0 {
		return ProjectVertexAttachmentRecord{}, false
	}
	return record, true
}

func indexOfProjectSlotReference(
	slots []ProjectSlotRecord,
	reference int,
) int {
	for index, slot := range slots {
		if slot.WireReference == reference {
			return index
		}
	}
	return -1
}

func readProjectClippingEndSlotIndex(
	payload []byte,
	attachmentReference int,
	owner ProjectSlotRecord,
	slotCount int,
) (int, bool) {
	if owner.Offset < 0 || owner.Offset >= len(payload) ||
		attachmentReference < projectFirstWireReference || slotCount < 1 {
		return 0, false
	}
	limit := owner.Offset + 192
	if limit > len(payload) {
		limit = len(payload)
	}
	for offset := owner.Offset; offset+8 < limit; offset++ {
		if payload[offset] != 0x09 {
			continue
		}
		classID, referenceOffset, classOK := readPositiveVarint(
			payload,
			offset+1,
		)
		if !classOK || classID != ProjectAttachmentClassClipping {
			continue
		}
		reference, suffix, referenceOK := readPositiveVarint(
			payload,
			referenceOffset,
		)
		if !referenceOK || reference != attachmentReference ||
			suffix+7 >= limit ||
			!bytes.Equal(
				payload[suffix:suffix+7],
				[]byte{0x7e, 0x01, 0x0d, 0x01, 0x0f, 0x00, 0x03},
			) {
			continue
		}
		endSlotIndex, end, indexOK := readPositiveVarint(
			payload,
			suffix+7,
		)
		if !indexOK || endSlotIndex < 0 || endSlotIndex >= slotCount ||
			end >= limit || payload[end] != 0x00 {
			return 0, false
		}
		return endSlotIndex, true
	}
	return 0, false
}

func readProjectVertexCollectionBefore(
	payload []byte,
	end int,
) (int, []ProjectMeshVertexRecord, int, bool) {
	prefix := []byte{0x01, 0x03, 0x02, 0x0f, 0x01}
	start := end - 32_768
	if start < 0 {
		start = 0
	}
	matchOffset := -1
	var matchVertices []ProjectMeshVertexRecord
	matchEnd := 0
	for offset := start; offset+len(prefix) < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], prefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(
			payload,
			offset+len(prefix),
		)
		if !ok || count < 1 || count > 1_000_000 {
			continue
		}
		vertices := make([]ProjectMeshVertexRecord, 0, count)
		valid := true
		for index := 0; index < count; index++ {
			vertex, next, vertexOK := readProjectMeshVertex(payload, cursor)
			if !vertexOK {
				valid = false
				break
			}
			vertices = append(vertices, vertex)
			cursor = next
		}
		if !valid {
			continue
		}
		_, tableEnd, tableOK := readProjectMeshBoneReferenceTable(
			payload,
			cursor,
		)
		if !tableOK || tableEnd != end {
			continue
		}
		if matchOffset >= 0 {
			return 0, nil, 0, false
		}
		matchOffset = offset
		matchVertices = vertices
		matchEnd = cursor
	}
	return matchOffset, matchVertices, matchEnd, matchOffset >= 0
}

func readProjectPathAttachmentMetadata(
	payload []byte,
	collectionOffset int,
) ([]float32, bool, bool, bool) {
	start := collectionOffset - 96
	if start < 0 {
		start = 0
	}
	prefix := []byte{0x13, 0x0f, 0x01}
	match := -1
	for offset := start; offset+len(prefix) < collectionOffset; offset++ {
		if bytes.HasPrefix(payload[offset:collectionOffset], prefix) {
			match = offset
		}
	}
	if match < 0 {
		return nil, false, false, false
	}
	encodedCount, cursor, ok := readPositiveVarint(
		payload,
		match+len(prefix),
	)
	count := encodedCount - 1
	if !ok || count < 1 || count > 100_000 ||
		cursor+count*4+4 > collectionOffset {
		return nil, false, false, false
	}
	lengths := make([]float32, count)
	for index := range lengths {
		lengths[index] = readProjectFloat32(payload, cursor+index*4)
		if !finiteProjectFloat(lengths[index]) || lengths[index] < 0 {
			return nil, false, false, false
		}
	}
	cursor += count * 4
	if cursor+4 > collectionOffset ||
		payload[cursor] != 0x0c ||
		payload[cursor+2] != 0x0d ||
		payload[cursor+1] > 1 ||
		payload[cursor+3] > 1 {
		return nil, false, false, false
	}
	return lengths, payload[cursor+1] != 0, payload[cursor+3] != 0, true
}

func readProjectClippingAttachmentMetadata(
	payload []byte,
	collectionOffset int,
) (bool, bool) {
	start := collectionOffset - 24
	if start < 0 {
		start = 0
	}
	for offset := collectionOffset - 2; offset >= start; offset-- {
		if payload[offset] == 0x0e && payload[offset+1] <= 1 {
			return payload[offset+1] == 0, true
		}
	}
	return false, false
}

func readProjectVertexAttachmentName(
	payload []byte,
	start int,
) (string, int, int, bool) {
	end := start + 192
	if end > len(payload) {
		end = len(payload)
	}
	prefix := []byte{0x05, 0x01, 0x01}
	relative := bytes.Index(payload[start:end], prefix)
	if relative < 0 {
		return "", 0, start, false
	}
	cursor := start + relative + len(prefix)
	if cursor >= end {
		return "", 0, start, false
	}
	if payload[cursor] == 0x01 {
		name, next, ok := decodeProjectASCII(payload, cursor+1)
		return name, 0, next, ok
	}
	reference, next, ok := readPositiveVarint(payload, cursor)
	if !ok || reference < projectFirstWireReference {
		return "", 0, start, false
	}
	return "", reference, next, true
}

func resolveProjectVertexAttachmentNames(
	records []ProjectVertexAttachmentRecord,
) bool {
	for index := range records {
		if records[index].Name != "" || records[index].NameReference == 0 {
			continue
		}
		name := ""
		ambiguous := false
		for _, candidate := range records {
			if candidate.Name == "" ||
				!projectVertexAttachmentGeometryEqual(
					records[index],
					candidate,
				) {
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
	for index := range records {
		if records[index].Name == "" || records[index].NameReference == 0 {
			continue
		}
		previous := namesByReference[records[index].NameReference]
		if previous != "" && previous != records[index].Name {
			return false
		}
		namesByReference[records[index].NameReference] = records[index].Name
	}
	for index := range records {
		if records[index].Name != "" {
			continue
		}
		if records[index].NameReference == 0 ||
			namesByReference[records[index].NameReference] == "" {
			return false
		}
		records[index].Name = namesByReference[records[index].NameReference]
	}
	return true
}

func projectVertexAttachmentGeometryEqual(
	left ProjectVertexAttachmentRecord,
	right ProjectVertexAttachmentRecord,
) bool {
	if left.ClassID != right.ClassID ||
		left.Closed != right.Closed ||
		left.ConstantSpeed != right.ConstantSpeed ||
		left.Convex != right.Convex ||
		!projectFloatSlicesEqual(left.Lengths, right.Lengths) ||
		len(left.MeshVertices) != len(right.MeshVertices) {
		return false
	}
	for index := range left.MeshVertices {
		if !projectFloatSlicesEqual(
			left.MeshVertices[index].Weights,
			right.MeshVertices[index].Weights,
		) || !projectFloatSlicesEqual(
			left.MeshVertices[index].Coordinates,
			right.MeshVertices[index].Coordinates,
		) {
			return false
		}
	}
	return true
}

// FindByReference resolves one unique vertex attachment.
func (directory *ProjectVertexAttachmentDirectory) FindByReference(
	reference int,
) (ProjectVertexAttachmentRecord, bool) {
	if directory == nil || reference == 0 {
		return ProjectVertexAttachmentRecord{}, false
	}
	match := -1
	for index, record := range directory.Records {
		if record.WireReference != reference {
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
	return directory.Records[match], true
}

func validateProjectVertexAttachment(
	record ProjectVertexAttachmentRecord,
) error {
	if record.Name == "" || record.WireReference == 0 ||
		record.OwnerSlotReference == 0 || len(record.MeshVertices) == 0 {
		return fmt.Errorf("vertex attachment is incomplete")
	}
	return nil
}
