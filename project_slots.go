package spineparser

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

var (
	projectSlotV2Prefix       = []byte{0x0d, 0x01, 0x0f}
	projectSlotV2BoneField    = []byte{0x03, 0x01, 0x02}
	projectSlotV2ColorField   = []byte{0x05, 0x1e, 0x01}
	projectSlotV2DefaultColor = [4]byte{0xff, 0xff, 0xff, 0xff}
)

const (
	ProjectAttachmentClassRegion      = 0x2b
	ProjectAttachmentClassMesh        = 0x2e
	ProjectAttachmentClassBoundingBox = 0x34
	ProjectAttachmentClassPath        = 0x45
	ProjectAttachmentClassPoint       = 0x53
	ProjectAttachmentClassClipping    = 0x54
)

// ProjectSlotRecord contains one setup slot in draw order.
type ProjectSlotRecord struct {
	WireReference            int     `json:"wireReference,omitempty"`
	Name                     string  `json:"name"`
	NameReference            int     `json:"nameReference,omitempty"`
	BoneName                 string  `json:"boneName"`
	BoneReference            int     `json:"boneReference"`
	Color                    [4]byte `json:"color"`
	Blend                    string  `json:"blend"`
	SetupAttachment          string  `json:"setupAttachment,omitempty"`
	SetupAttachmentClassID   int     `json:"setupAttachmentClassId,omitempty"`
	SetupAttachmentReference int     `json:"setupAttachmentReference,omitempty"`
	Offset                   int     `json:"offset"`
}

// ProjectSlotDirectory is the proven 4.3.23 slot draw order.
type ProjectSlotDirectory struct {
	Format             string              `json:"format"`
	Count              int                 `json:"count"`
	ReferencesComplete bool                `json:"referencesComplete"`
	Records            []ProjectSlotRecord `json:"records"`
}

// DiscoverProjectSlots decodes region-backed setup slots from a 4.3.23
// project. Projects with attachment-less slots or ambiguous region-to-bone
// matches remain unsupported and fail closed.
func DiscoverProjectSlots(payload []byte) (*ProjectSlotDirectory, error) {
	bones, err := DiscoverProjectBones(payload)
	if err != nil {
		return nil, err
	}
	regions, err := DiscoverProjectRegionAttachments(payload)
	if err != nil {
		return nil, err
	}
	if len(bones.Records) == 1 && len(regions.Records) == 1 {
		region := regions.Records[0]
		return &ProjectSlotDirectory{
			Format: "kryo-single-root-region-v2",
			Count:  1,
			Records: []ProjectSlotRecord{
				{
					Name:            region.Name,
					BoneName:        bones.Records[0].Name,
					BoneReference:   1,
					Color:           projectSlotV2DefaultColor,
					Blend:           "normal",
					SetupAttachment: region.Name,
					Offset:          region.Offset,
				},
			},
		}, nil
	}
	if !bones.ReferencesComplete {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "slot decoding requires complete bone wire references",
		}
	}
	matchedReferences, err := matchProjectRegionsToBones(regions, bones)
	if err != nil {
		return nil, err
	}

	slotOffsets := projectObjectOffsets(payload, projectSlotV2Prefix)
	if len(slotOffsets)+1 != len(matchedReferences) {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg: fmt.Sprintf(
				"slot wrapper count %d does not match region-backed slot count %d",
				len(slotOffsets),
				len(matchedReferences),
			),
		}
	}

	wrapped := make([]ProjectSlotRecord, 0, len(slotOffsets))
	usedReferences := make(map[int]struct{}, len(slotOffsets))
	currentBlend := ""
	for index, offset := range slotOffsets {
		end := len(payload)
		if index+1 < len(slotOffsets) {
			end = slotOffsets[index+1]
		}
		if end > offset+512 {
			end = offset + 512
		}
		record, inlineBlend, ok := readProjectSlotV2(
			payload,
			offset,
			end,
			bones,
			currentBlend,
		)
		if !ok {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  fmt.Sprintf("slot wrapper at offset %d is incomplete", offset),
			}
		}
		if inlineBlend != "" {
			currentBlend = inlineBlend
		}
		if _, duplicate := usedReferences[record.BoneReference]; duplicate {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  fmt.Sprintf("duplicate slot bone reference %d", record.BoneReference),
			}
		}
		usedReferences[record.BoneReference] = struct{}{}
		wrapped = append(wrapped, record)
	}

	firstReference := 0
	for _, reference := range matchedReferences {
		if _, used := usedReferences[reference]; !used {
			if firstReference != 0 {
				return nil, &ParseError{
					Code: ErrInvalidProject,
					Msg:  "multiple first-slot candidates remain after wrapper decoding",
				}
			}
			firstReference = reference
		}
	}
	if firstReference == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "first setup slot could not be resolved",
		}
	}
	firstName, ok := bones.BoneNameByWireReference(firstReference)
	if !ok {
		return nil, &ParseError{Code: ErrInvalidProject, Msg: "first slot bone is unknown"}
	}
	records := make([]ProjectSlotRecord, 0, len(matchedReferences))
	records = append(records, ProjectSlotRecord{
		Name:          firstName,
		BoneName:      firstName,
		BoneReference: firstReference,
		Color:         projectSlotV2DefaultColor,
		Blend:         "normal",
		Offset:        regions.Records[0].Offset,
	})
	records = append(records, wrapped...)
	return &ProjectSlotDirectory{
		Format:  "kryo-slot-table-v2",
		Count:   len(records),
		Records: records,
	}, nil
}

// DiscoverProjectSlotRecords decodes the actual 4.3 slot object records in
// object creation order. This is distinct from setup draw order, which Spine
// stores separately and is not yet exposed by this API.
func DiscoverProjectSlotRecords(payload []byte) (*ProjectSlotDirectory, error) {
	return discoverProjectSlotRecords(payload, payload)
}

func discoverProjectSlotRecords(
	payload []byte,
	catalogPayload []byte,
) (*ProjectSlotDirectory, error) {
	bones, err := DiscoverProjectBones(payload)
	if err != nil {
		return nil, err
	}
	records := discoverProjectSlotsV2DirectWithCatalog(payload, bones, catalogPayload)
	if len(records) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported project slot records were not found",
		}
	}
	references, referencesOK := discoverProjectSlotDrawOrderReferences(
		payload,
		len(records),
	)
	if referencesOK {
		records, referencesOK = bindProjectSlotWireReferences(
			records,
			references,
		)
	}
	return &ProjectSlotDirectory{
		Format:             "kryo-slot-records-v2",
		Count:              len(records),
		ReferencesComplete: referencesOK,
		Records:            records,
	}, nil
}

func discoverProjectSlotDrawOrderReferences(
	payload []byte,
	expectedCount int,
) ([]int, bool) {
	if expectedCount < 1 {
		return nil, false
	}
	prefix := []byte{0x02, 0x0d, 0x0f, 0x01}
	var result []int
	for offset := 0; offset+len(prefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], prefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(prefix))
		if !ok || count != expectedCount {
			continue
		}
		references := make([]int, count)
		seen := make(map[int]struct{}, count)
		valid := true
		for index := range references {
			if cursor >= len(payload) || payload[cursor] != 0x0d {
				valid = false
				break
			}
			reference, next, referenceOK := readPositiveVarint(
				payload,
				cursor+1,
			)
			if !referenceOK || reference < projectFirstWireReference {
				valid = false
				break
			}
			if _, duplicate := seen[reference]; duplicate {
				valid = false
				break
			}
			seen[reference] = struct{}{}
			references[index] = reference
			cursor = next
		}
		if !valid || cursor+2 > len(payload) ||
			payload[cursor] != 0x32 ||
			(payload[cursor+1] != 0x00 && payload[cursor+1] != 0x01) {
			continue
		}
		if result != nil {
			return nil, false
		}
		result = references
	}
	return result, result != nil
}

func bindProjectSlotWireReferences(
	records []ProjectSlotRecord,
	drawOrder []int,
) ([]ProjectSlotRecord, bool) {
	if len(records) == 0 || len(records) != len(drawOrder) {
		return records, false
	}
	sortedReferences := append([]int(nil), drawOrder...)
	sort.Ints(sortedReferences)
	for index := 1; index < len(sortedReferences); index++ {
		if sortedReferences[index-1] >= sortedReferences[index] {
			return records, false
		}
	}

	byReference := make(map[int]ProjectSlotRecord, len(records))
	for index := range records {
		referenceIndex := index + 1
		if referenceIndex == len(sortedReferences) {
			referenceIndex = 0
		}
		record := records[index]
		record.WireReference = sortedReferences[referenceIndex]
		byReference[record.WireReference] = record
	}
	for _, record := range append([]ProjectSlotRecord(nil), byReferenceValues(byReference)...) {
		if record.SetupAttachmentReference == 0 {
			continue
		}
		expectedReference := record.SetupAttachmentReference - 1
		if expectedReference < projectFirstWireReference {
			return records, false
		}
		holder, exists := byReference[expectedReference]
		if !exists {
			continue
		}
		if record.WireReference == expectedReference {
			continue
		}
		delete(byReference, record.WireReference)
		recordReference := record.WireReference
		record.WireReference = expectedReference
		holder.WireReference = recordReference
		byReference[record.WireReference] = record
		byReference[holder.WireReference] = holder
	}

	ordered := make([]ProjectSlotRecord, len(records))
	for index, reference := range drawOrder {
		record, ok := byReference[reference]
		if !ok {
			return records, false
		}
		ordered[index] = record
	}
	return ordered, true
}

func byReferenceValues(
	records map[int]ProjectSlotRecord,
) []ProjectSlotRecord {
	result := make([]ProjectSlotRecord, 0, len(records))
	for _, record := range records {
		result = append(result, record)
	}
	sort.Slice(result, func(left int, right int) bool {
		return result[left].Offset < result[right].Offset
	})
	return result
}

func discoverProjectSlotsV2Direct(
	payload []byte,
	bones *ProjectBoneDirectory,
) []ProjectSlotRecord {
	return discoverProjectSlotsV2DirectWithCatalog(payload, bones, payload)
}

func discoverProjectSlotsV2DirectWithCatalog(
	payload []byte,
	bones *ProjectBoneDirectory,
	catalogPayload []byte,
) []ProjectSlotRecord {
	if bones == nil || len(bones.Records) == 0 {
		return nil
	}
	records := make([]ProjectSlotRecord, 0)
	blendEvidence := make([]projectSlotBlendEvidence, 0)
	colorMarker := []byte{0x0b, 0x1e, 0x01}
	for colorOffset := 0; colorOffset+8 < len(payload); colorOffset++ {
		if !bytes.HasPrefix(payload[colorOffset:], colorMarker) ||
			payload[colorOffset+7] != 0x0e {
			continue
		}
		searchEnd := colorOffset + 64
		if searchEnd > len(payload) {
			searchEnd = len(payload)
		}
		boneOffset := bytes.Index(
			payload[colorOffset+8:searchEnd],
			projectSlotV2BoneField,
		)
		if boneOffset < 0 {
			continue
		}
		boneOffset += colorOffset + 8
		reference, afterReference, ok := readPositiveVarint(
			payload,
			boneOffset+len(projectSlotV2BoneField),
		)
		if !ok || afterReference >= searchEnd ||
			payload[afterReference] != 0x07 {
			continue
		}
		boneName, ok := bones.BoneNameByWireReference(reference)
		if !ok {
			if len(bones.Records) != 1 {
				continue
			}
			boneName = bones.Records[0].Name
		}

		recordStart := colorOffset
		name := boneName
		nameReference := 0
		startLimit := colorOffset - 192
		if startLimit < 0 {
			startLimit = 0
		}
		if terminator := bytes.LastIndex(payload[startLimit:colorOffset], []byte{0x7e}); terminator >= 0 {
			startLimit += terminator + 1
		}
		if inlineName, referencedName, inlineOffset, found := readProjectSlotNameField(
			payload,
			startLimit,
			colorOffset,
		); found {
			recordStart = inlineOffset
			nameReference = referencedName
			if inlineName != "" {
				name = inlineName
			}
		}
		attachmentClassID, attachmentReference := readProjectSlotSetupAttachment(
			payload,
			afterReference,
			searchEnd,
		)
		color := projectSlotV2DefaultColor
		if setupColorOffset := bytes.Index(
			payload[afterReference:searchEnd],
			projectSlotV2ColorField,
		); setupColorOffset >= 0 {
			setupColorOffset += afterReference + len(projectSlotV2ColorField)
			if setupColorOffset+len(color) <= searchEnd {
				copy(color[:], payload[setupColorOffset:setupColorOffset+len(color)])
			}
		}
		blend, blendOK := readProjectSlotBlendV2(
			payload,
			afterReference,
			searchEnd,
		)
		if !blendOK {
			continue
		}
		records = append(records, ProjectSlotRecord{
			Name:                     name,
			NameReference:            nameReference,
			BoneName:                 boneName,
			BoneReference:            reference,
			Color:                    color,
			Blend:                    blend.inline,
			SetupAttachmentClassID:   attachmentClassID,
			SetupAttachmentReference: attachmentReference,
			Offset:                   recordStart,
		})
		blendEvidence = append(blendEvidence, blend)
		colorOffset = afterReference
	}
	if !bindProjectSlotBlends(records, blendEvidence) {
		catalog := discoverProjectSlotBlendCatalog(catalogPayload)
		if !bindProjectSlotBlendsFromCatalog(records, blendEvidence, catalog) {
			return nil
		}
	}
	for index := len(records) - 1; index > 0; index-- {
		records[index].Color = records[index-1].Color
	}
	if len(records) != 0 {
		records[0].Color = projectSlotV2DefaultColor
	}
	return records
}

func discoverProjectSlotBlendCatalog(payload []byte) []projectSlotBlendEvidence {
	result := make([]projectSlotBlendEvidence, 0)
	colorMarker := []byte{0x0b, 0x1e, 0x01}
	for colorOffset := 0; colorOffset+8 < len(payload); colorOffset++ {
		if !bytes.HasPrefix(payload[colorOffset:], colorMarker) ||
			payload[colorOffset+7] != 0x0e {
			continue
		}
		searchEnd := colorOffset + 64
		if searchEnd > len(payload) {
			searchEnd = len(payload)
		}
		boneOffset := bytes.Index(
			payload[colorOffset+8:searchEnd],
			projectSlotV2BoneField,
		)
		if boneOffset < 0 {
			continue
		}
		boneOffset += colorOffset + 8
		_, afterReference, ok := readPositiveVarint(
			payload,
			boneOffset+len(projectSlotV2BoneField),
		)
		if !ok || afterReference >= searchEnd ||
			payload[afterReference] != 0x07 {
			continue
		}
		blend, blendOK := readProjectSlotBlendV2(
			payload,
			afterReference,
			searchEnd,
		)
		if blendOK {
			result = append(result, blend)
		}
	}
	return result
}

func bindProjectSlotBlendsFromCatalog(
	records []ProjectSlotRecord,
	evidence []projectSlotBlendEvidence,
	catalog []projectSlotBlendEvidence,
) bool {
	if len(records) == 0 || len(records) != len(evidence) {
		return false
	}
	inline := make([]string, 0)
	referenceSet := make(map[int]struct{})
	for _, item := range catalog {
		if item.inline != "" {
			inline = append(inline, item.inline)
		} else if item.reference >= projectFirstWireReference {
			referenceSet[item.reference] = struct{}{}
		}
	}
	references := make([]int, 0, len(referenceSet))
	for reference := range referenceSet {
		references = append(references, reference)
	}
	sort.Ints(references)
	if len(references) == 0 || len(inline) < len(references) {
		return false
	}
	blendByReference := make(map[int]string, len(references))
	for index, reference := range references {
		blendByReference[reference] = inline[index]
	}
	for index, item := range evidence {
		if item.inline != "" {
			records[index].Blend = item.inline
			continue
		}
		blend := blendByReference[item.reference]
		if blend == "" {
			return false
		}
		records[index].Blend = blend
	}
	return true
}

func readProjectSlotNameField(
	payload []byte,
	start int,
	end int,
) (string, int, int, bool) {
	name, offset, ok := readProjectSlotInlineName(payload, start, end)
	if ok {
		return name, 0, offset, true
	}
	for offset = end - 1; offset >= start; offset-- {
		if payload[offset] != 0x0d {
			continue
		}
		_, cursor, tokenOK := readPositiveVarint(payload, offset+1)
		if !tokenOK || cursor+1 >= end || payload[cursor] != 0x01 {
			continue
		}
		reference, referenceEnd, referenceOK := readPositiveVarint(
			payload,
			cursor+1,
		)
		if referenceOK && reference >= projectFirstWireReference &&
			referenceEnd == end {
			return "", reference, offset, true
		}
	}
	return "", 0, 0, false
}

func readProjectSlotInlineName(
	payload []byte,
	start int,
	end int,
) (string, int, bool) {
	if start < 0 || start >= end || end > len(payload) {
		return "", 0, false
	}
	for offset := end - 1; offset >= start; offset-- {
		if payload[offset] != 0x0d {
			continue
		}
		_, cursor, ok := readPositiveVarint(payload, offset+1)
		if !ok || cursor+2 >= end ||
			payload[cursor] != 0x01 ||
			payload[cursor+1] != 0x01 {
			continue
		}
		name, nameEnd, decoded := decodeProjectASCII(payload, cursor+2)
		if decoded && nameEnd == end &&
			!strings.ContainsAny(name, `/\`) {
			return name, offset, true
		}
	}
	return "", 0, false
}

type projectSlotBlendEvidence struct {
	inline    string
	reference int
}

func readProjectSlotBlendV2(
	payload []byte,
	afterBoneReference int,
	end int,
) (projectSlotBlendEvidence, bool) {
	if afterBoneReference < 0 || afterBoneReference+2 > end ||
		end > len(payload) || payload[afterBoneReference] != 0x07 {
		return projectSlotBlendEvidence{}, false
	}
	cursor := afterBoneReference + 1
	if payload[cursor] != 0x01 {
		reference, _, ok := readPositiveVarint(payload, cursor)
		if !ok || reference < projectFirstWireReference {
			return projectSlotBlendEvidence{}, false
		}
		return projectSlotBlendEvidence{reference: reference}, true
	}
	if cursor+1 >= end {
		return projectSlotBlendEvidence{}, false
	}
	blend := ""
	switch payload[cursor+1] {
	case 0x01:
		blend = "normal"
	case 0x02:
		blend = "additive"
	case 0x03:
		blend = "multiply"
	case 0x04:
		blend = "screen"
	default:
		return projectSlotBlendEvidence{}, false
	}
	return projectSlotBlendEvidence{inline: blend}, true
}

func bindProjectSlotBlends(
	records []ProjectSlotRecord,
	evidence []projectSlotBlendEvidence,
) bool {
	if len(records) == 0 || len(records) != len(evidence) {
		return false
	}
	inline := make([]string, 0)
	referenceSet := make(map[int]struct{})
	for _, item := range evidence {
		if item.inline != "" {
			inline = append(inline, item.inline)
			continue
		}
		if item.reference < projectFirstWireReference {
			return false
		}
		referenceSet[item.reference] = struct{}{}
	}
	references := make([]int, 0, len(referenceSet))
	for reference := range referenceSet {
		references = append(references, reference)
	}
	sort.Ints(references)
	if len(references) == 0 {
		for index, item := range evidence {
			if item.inline == "" {
				return false
			}
			records[index].Blend = item.inline
		}
		return true
	}
	if len(inline) < len(references) {
		return bindProjectSlotBlendsByEnumOrdinal(records, evidence, references)
	}
	blendByReference := make(map[int]string, len(references))
	for index, reference := range references {
		blendByReference[reference] = inline[index]
	}
	for index, item := range evidence {
		if item.inline != "" {
			records[index].Blend = item.inline
			continue
		}
		blend := blendByReference[item.reference]
		if blend == "" {
			return false
		}
		records[index].Blend = blend
	}
	return true
}

func bindProjectSlotBlendsByEnumOrdinal(
	records []ProjectSlotRecord,
	evidence []projectSlotBlendEvidence,
	references []int,
) bool {
	blendNames := []string{"normal", "additive", "multiply", "screen"}
	highestInline := -1
	for _, item := range evidence {
		if item.inline == "" {
			continue
		}
		ordinal := -1
		for index, name := range blendNames {
			if item.inline == name {
				ordinal = index
				break
			}
		}
		if ordinal < 0 {
			return false
		}
		if ordinal > highestInline {
			highestInline = ordinal
		}
	}
	if highestInline < 0 || len(references) != highestInline+1 ||
		len(references) > len(blendNames) {
		return false
	}
	blendByReference := make(map[int]string, len(references))
	for index, reference := range references {
		blendByReference[reference] = blendNames[index]
	}
	for index, item := range evidence {
		if item.inline != "" {
			records[index].Blend = item.inline
			continue
		}
		blend := blendByReference[item.reference]
		if blend == "" {
			return false
		}
		records[index].Blend = blend
	}
	return true
}

func projectObjectOffsets(payload []byte, prefix []byte) []int {
	offsets := make([]int, 0)
	for offset := 0; offset+len(prefix) <= len(payload); offset++ {
		if bytes.HasPrefix(payload[offset:], prefix) {
			offsets = append(offsets, offset)
			offset += len(prefix) - 1
		}
	}
	return offsets
}

func matchProjectRegionsToBones(
	regions *ProjectRegionAttachmentDirectory,
	bones *ProjectBoneDirectory,
) ([]int, error) {
	references := make([]int, 0, len(regions.Records))
	used := make(map[int]struct{}, len(regions.Records))
	for _, region := range regions.Records {
		match := 0
		for _, bone := range bones.Records {
			if _, exists := used[bone.WireReference]; exists ||
				!projectRegionMatchesBone(region.Name, bone.Name) {
				continue
			}
			match = bone.WireReference
			break
		}
		if match == 0 {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  fmt.Sprintf("region %q does not uniquely map to an unused bone", region.Name),
			}
		}
		used[match] = struct{}{}
		references = append(references, match)
	}
	return references, nil
}

func projectRegionMatchesBone(regionName string, boneName string) bool {
	if separator := strings.LastIndexAny(regionName, `/\`); separator >= 0 {
		regionName = regionName[separator+1:]
	}
	if regionName == boneName {
		return true
	}
	if strings.HasPrefix(boneName, regionName) {
		suffix := strings.TrimPrefix(boneName, regionName)
		if suffix != "" && strings.Trim(suffix, "_0123456789") == "" {
			return true
		}
	}
	regionKey := strings.TrimRight(regionName, "_0123456789")
	boneKey := strings.TrimRight(boneName, "_0123456789")
	return regionKey != "" && regionKey == boneKey
}

func readProjectSlotV2(
	payload []byte,
	offset int,
	end int,
	bones *ProjectBoneDirectory,
	previousBlend string,
) (ProjectSlotRecord, string, bool) {
	record := ProjectSlotRecord{
		Color:  projectSlotV2DefaultColor,
		Blend:  previousBlend,
		Offset: offset,
	}
	colorOffset := bytes.Index(payload[offset:end], projectSlotV2ColorField)
	if colorOffset >= 0 {
		colorOffset += offset + len(projectSlotV2ColorField)
		if colorOffset+4 > end {
			return record, "", false
		}
		copy(record.Color[:], payload[colorOffset:colorOffset+4])
	}

	boneOffset := -1
	for cursor := offset; cursor+len(projectSlotV2BoneField) < end; cursor++ {
		if !bytes.HasPrefix(payload[cursor:end], projectSlotV2BoneField) {
			continue
		}
		reference, afterReference, ok := readPositiveVarint(
			payload,
			cursor+len(projectSlotV2BoneField),
		)
		if !ok || afterReference >= end || payload[afterReference] != 0x07 {
			continue
		}
		if _, exists := bones.BoneNameByWireReference(reference); !exists {
			continue
		}
		boneOffset = cursor
		record.BoneReference = reference
		break
	}
	if boneOffset < 0 {
		return record, "", false
	}
	record.BoneName, _ = bones.BoneNameByWireReference(record.BoneReference)
	record.Name = record.BoneName

	_, blendOffset, _ := readPositiveVarint(
		payload,
		boneOffset+len(projectSlotV2BoneField),
	)
	blendOffset++
	if blendOffset >= end {
		return record, "", false
	}
	record.SetupAttachmentClassID, record.SetupAttachmentReference = readProjectSlotSetupAttachment(
		payload,
		blendOffset,
		end,
	)
	inlineBlend := ""
	if payload[blendOffset] == 0x01 {
		if blendOffset+1 >= end {
			return record, "", false
		}
		switch payload[blendOffset+1] {
		case 0x01:
			inlineBlend = "normal"
		case 0x02:
			inlineBlend = "additive"
		case 0x03:
			inlineBlend = "multiply"
		case 0x04:
			inlineBlend = "screen"
		default:
			return record, "", false
		}
		record.Blend = inlineBlend
	} else if record.Blend == "" {
		return record, "", false
	}
	return record, inlineBlend, true
}

// readProjectSlotSetupAttachment reads the registered attachment class and
// existing object reference from a verified V2 slot record. Inline-new fields
// keep their class with reference zero; a zero pair means null or ambiguous.
func readProjectSlotSetupAttachment(
	payload []byte,
	start int,
	end int,
) (int, int) {
	if start < 0 || start >= end || end > len(payload) {
		return 0, 0
	}
	resultClassID := 0
	resultReference := 0
	found := false
	limit := start + 24
	if limit > end {
		limit = end
	}
	for offset := start; offset+2 < limit; offset++ {
		if payload[offset] != 0x09 {
			continue
		}
		if payload[offset+1] == 0x00 {
			return 0, 0
		}
		classID, referenceOffset, classOK := readPositiveVarint(
			payload,
			offset+1,
		)
		if !classOK || !supportedProjectAttachmentClassID(classID) {
			continue
		}
		if referenceOffset < limit && payload[referenceOffset] == 0x01 {
			if found {
				return 0, 0
			}
			resultClassID = classID
			resultReference = 0
			found = true
			continue
		}
		reference, next, ok := readPositiveVarint(payload, referenceOffset)
		if !ok || reference < projectFirstWireReference ||
			next >= end || payload[next] != 0x7e {
			continue
		}
		if found {
			return 0, 0
		}
		resultClassID = classID
		resultReference = reference
		found = true
	}
	return resultClassID, resultReference
}

func supportedProjectAttachmentClassID(classID int) bool {
	switch classID {
	case ProjectAttachmentClassRegion,
		ProjectAttachmentClassMesh,
		ProjectAttachmentClassBoundingBox,
		ProjectAttachmentClassPath,
		ProjectAttachmentClassPoint,
		ProjectAttachmentClassClipping:
		return true
	default:
		return false
	}
}
