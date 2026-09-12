package spineparser

import (
	"bytes"
	"fmt"
	"sort"
)

var projectRegionAttachmentV2Prefix = []byte{0x2b, 0x01, 0x11}

// ProjectRegionAttachmentRecord contains setup-pose data directly decoded
// from one 4.3.23 region attachment object.
type ProjectRegionAttachmentRecord struct {
	WireReference      int                        `json:"wireReference,omitempty"`
	OwnerSlotReference int                        `json:"ownerSlotReference,omitempty"`
	Name               string                     `json:"name"`
	NameReference      int                        `json:"nameReference,omitempty"`
	Path               string                     `json:"path,omitempty"`
	PathReference      int                        `json:"pathReference,omitempty"`
	Offset             int                        `json:"offset"`
	Sequence           *ProjectAttachmentSequence `json:"sequence,omitempty"`
	X                  float32                    `json:"x"`
	Y                  float32                    `json:"y"`
	Rotation           float32                    `json:"rotation"`
	ScaleX             float32                    `json:"scaleX"`
	ScaleY             float32                    `json:"scaleY"`
	Width              float32                    `json:"width"`
	Height             float32                    `json:"height"`
}

// ProjectRegionAttachmentDirectory is the proven region attachment sequence
// from a 4.3.23 project payload.
type ProjectRegionAttachmentDirectory struct {
	Format             string                          `json:"format"`
	Count              int                             `json:"count"`
	ReferencesComplete bool                            `json:"referencesComplete"`
	Records            []ProjectRegionAttachmentRecord `json:"records"`
}

// DiscoverProjectRegionAttachments decodes the 4.3.23 tagged region layout.
// Other attachment classes and unresolved first-name references fail closed.
func DiscoverProjectRegionAttachments(
	payload []byte,
) (*ProjectRegionAttachmentDirectory, error) {
	if len(payload) == 0 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	records := make([]ProjectRegionAttachmentRecord, 0)
	lastInlineName := ""
	for offset := 0; offset+len(projectRegionAttachmentV2Prefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], projectRegionAttachmentV2Prefix) {
			continue
		}
		record, ok := readProjectRegionAttachmentV2(
			payload,
			offset,
			lastInlineName,
		)
		if !ok {
			continue
		}
		if record.NameReference == 0 && record.Name != "" {
			lastInlineName = record.Name
		}
		records = append(records, record)
	}
	resolveProjectRegionReferencedNames(records)
	resolveProjectRegionReferencedPaths(records)
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
			Msg:  "supported project region attachments were not found",
		}
	}
	referencesComplete := assignProjectRegionWireReferences(payload, records)
	return &ProjectRegionAttachmentDirectory{
		Format:             "kryo-region-attachment-v2",
		Count:              len(records),
		ReferencesComplete: referencesComplete,
		Records:            records,
	}, nil
}

func readProjectRegionAttachmentV2(
	payload []byte,
	offset int,
	previousName string,
) (ProjectRegionAttachmentRecord, bool) {
	record := ProjectRegionAttachmentRecord{Offset: offset}
	searchStart := offset + len(projectRegionAttachmentV2Prefix)
	searchEnd := searchStart + 64
	if searchEnd > len(payload) {
		searchEnd = len(payload)
	}
	geometryOffset := -1
	for candidate := searchStart; candidate+10 < searchEnd; candidate++ {
		if payload[candidate] == 0x0a && payload[candidate+5] == 0x06 &&
			payload[candidate+10] == 0x0d {
			geometryOffset = candidate
			break
		}
	}
	if geometryOffset < 0 {
		return record, false
	}
	record.Sequence = readProjectAttachmentSequence(
		payload,
		searchStart,
		geometryOffset+1,
		0x0a,
	)
	record.Rotation = projectFloat32(payload[geometryOffset+1 : geometryOffset+5])
	record.X = projectFloat32(payload[geometryOffset+6 : geometryOffset+10])

	scaleOffset := findProjectTag(payload, geometryOffset+11, searchEnd, 0x08, 8)
	if scaleOffset < 0 || scaleOffset+26 > len(payload) ||
		payload[scaleOffset+5] != 0x0b ||
		payload[scaleOffset+10] != 0x07 ||
		payload[scaleOffset+15] != 0x09 ||
		payload[scaleOffset+20] != 0x0c ||
		payload[scaleOffset+25] != 0x0e {
		return record, false
	}
	path, pathReference, pathOK := readProjectRegionPathV2(
		payload,
		geometryOffset+10,
		scaleOffset,
	)
	if !pathOK {
		return record, false
	}
	record.Path = path
	record.PathReference = pathReference
	record.ScaleX = projectFloat32(payload[scaleOffset+1 : scaleOffset+5])
	record.Width = projectFloat32(payload[scaleOffset+6 : scaleOffset+10])
	record.Y = projectFloat32(payload[scaleOffset+11 : scaleOffset+15])
	record.ScaleY = projectFloat32(payload[scaleOffset+16 : scaleOffset+20])
	record.Height = projectFloat32(payload[scaleOffset+21 : scaleOffset+25])

	// Region color uses the registered color serializer: tag, class, new/ref,
	// RGBA. The following tag 5 is the attachment name.
	nameTag := scaleOffset + 32
	if nameTag >= len(payload) || payload[nameTag] != 0x05 {
		return record, false
	}
	name, nameReference, nameEnd, ok := readProjectRegionNameV2(
		payload,
		nameTag+1,
		previousName,
	)
	if !ok {
		return record, false
	}
	record.Name = name
	record.NameReference = nameReference
	record.OwnerSlotReference = readProjectDefaultAttachmentOwnerReference(
		payload,
		nameEnd,
	)
	return record, true
}

func readProjectRegionNameV2(
	payload []byte,
	offset int,
	previousName string,
) (string, int, int, bool) {
	if offset+3 > len(payload) || payload[offset] != 0x01 ||
		payload[offset+1] != 0x01 {
		return "", 0, offset, false
	}
	if payload[offset+2] == 0x01 {
		name, end, ok := decodeProjectASCII(payload, offset+3)
		if !ok {
			return "", 0, offset, false
		}
		return name, 0, end, true
	}
	reference, end, ok := readPositiveVarint(payload, offset+2)
	if !ok || reference < projectFirstWireReference || previousName == "" {
		return "", 0, offset, false
	}
	return previousName, reference, end, true
}

func readProjectRegionPathV2(
	payload []byte,
	offset int,
	end int,
) (string, int, bool) {
	if offset < 0 || offset+2 > end || end > len(payload) ||
		payload[offset] != 0x0d {
		return "", 0, false
	}
	valueOffset := offset + 1
	if payload[valueOffset] == 0x00 && valueOffset+1 == end {
		return "", 0, true
	}
	if payload[valueOffset] == 0x01 {
		if valueOffset+2 == end && payload[valueOffset+1] == 0x81 {
			return "", 0, true
		}
		path, next, ok := decodeProjectASCII(payload, valueOffset+1)
		if ok && next == end {
			return path, 0, true
		}
		path, ok = decodeProjectShortASCII(payload, valueOffset+1)
		if ok && valueOffset+2+len(path) == end {
			return path, 0, true
		}
		return "", 0, false
	}
	reference, next, ok := readPositiveVarint(payload, valueOffset)
	if !ok || reference < projectFirstWireReference || next != end {
		return "", 0, false
	}
	return "", reference, true
}

func resolveProjectRegionReferencedNames(
	records []ProjectRegionAttachmentRecord,
) {
	type geometryGroup struct {
		indices []int
	}
	groups := make([]geometryGroup, 0)
	for index := range records {
		groupIndex := -1
		for candidateIndex, group := range groups {
			if projectRegionGeometryEqual(
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
		names := make([]string, 0)
		seenNames := make(map[string]struct{})
		references := make([]int, 0)
		seenReferences := make(map[int]struct{})
		for _, index := range group.indices {
			record := records[index]
			if record.NameReference == 0 && record.Name != "" {
				if _, seen := seenNames[record.Name]; !seen {
					names = append(names, record.Name)
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
		if len(names) == 0 || len(names) != len(references) {
			continue
		}
		sort.Ints(references)
		for index, reference := range references {
			for _, recordIndex := range group.indices {
				if records[recordIndex].NameReference == reference {
					records[recordIndex].Name = names[index]
				}
			}
		}
	}
}

func resolveProjectRegionReferencedPaths(
	records []ProjectRegionAttachmentRecord,
) {
	candidatesByReference := make(map[int]map[string]struct{})
	for _, record := range records {
		if record.PathReference == 0 ||
			record.NameReference != 0 ||
			record.Name == "" {
			continue
		}
		candidates := candidatesByReference[record.PathReference]
		if candidates == nil {
			candidates = make(map[string]struct{})
			candidatesByReference[record.PathReference] = candidates
		}
		candidates[record.Name] = struct{}{}
	}
	for index := range records {
		if records[index].PathReference == 0 ||
			records[index].Path != "" {
			continue
		}
		candidates := candidatesByReference[records[index].PathReference]
		if len(candidates) != 1 {
			continue
		}
		for path := range candidates {
			records[index].Path = path
		}
	}
}

func projectRegionGeometryEqual(
	left ProjectRegionAttachmentRecord,
	right ProjectRegionAttachmentRecord,
) bool {
	return left.Rotation == right.Rotation &&
		left.X == right.X &&
		left.Y == right.Y &&
		left.ScaleX == right.ScaleX &&
		left.ScaleY == right.ScaleY &&
		left.Width == right.Width &&
		left.Height == right.Height
}

func assignProjectRegionWireReferences(
	payload []byte,
	records []ProjectRegionAttachmentRecord,
) bool {
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil || !slots.ReferencesComplete {
		return false
	}
	referencesBySlot := make(map[int]map[int]struct{}, len(slots.Records))
	for _, slot := range slots.Records {
		if slot.SetupAttachmentClassID != ProjectAttachmentClassRegion ||
			slot.SetupAttachmentReference == 0 {
			continue
		}
		referencesBySlot[slot.WireReference] = map[int]struct{}{
			slot.SetupAttachmentReference: {},
		}
	}
	animations, animationErr := DiscoverProjectAnimations(payload)
	if animationErr == nil {
		for _, animation := range animations.Records {
			timelines, timelineErr := DiscoverProjectSlotAttachmentTimelines(
				payload,
				animation.Name,
			)
			if timelineErr != nil {
				continue
			}
			for _, timeline := range timelines.Timelines {
				for _, key := range timeline.Keys {
					if !key.HasAttachment ||
						key.AttachmentClassID != ProjectAttachmentClassRegion {
						continue
					}
					references := referencesBySlot[timeline.SlotReference]
					if references == nil {
						references = make(map[int]struct{})
						referencesBySlot[timeline.SlotReference] = references
					}
					references[key.AttachmentReference] = struct{}{}
				}
			}
		}
	}

	recordsByOwner := make(map[int][]int)
	ownerless := make([]int, 0, 1)
	for index, record := range records {
		if record.OwnerSlotReference == 0 {
			ownerless = append(ownerless, index)
		} else {
			recordsByOwner[record.OwnerSlotReference] = append(
				recordsByOwner[record.OwnerSlotReference],
				index,
			)
		}
	}
	usedReferences := make(map[int]struct{}, len(records))
	complete := true
	for owner, indices := range recordsByOwner {
		referenceSet := referencesBySlot[owner]
		if len(referenceSet) != len(indices) {
			complete = false
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
		}
	}

	unmatchedReferences := make([]int, 0, len(ownerless))
	ownerByReference := make(map[int]int, len(ownerless))
	for owner, referenceSet := range referencesBySlot {
		if len(recordsByOwner[owner]) != 0 {
			continue
		}
		for reference := range referenceSet {
			if _, matched := usedReferences[reference]; !matched {
				if previousOwner, duplicate := ownerByReference[reference]; duplicate &&
					previousOwner != owner {
					return false
				}
				unmatchedReferences = append(unmatchedReferences, reference)
				ownerByReference[reference] = owner
			}
		}
	}
	if len(unmatchedReferences) != len(ownerless) {
		complete = false
	} else {
		sort.Ints(unmatchedReferences)
		sort.Slice(ownerless, func(left int, right int) bool {
			return records[ownerless[left]].Offset < records[ownerless[right]].Offset
		})
		for index, reference := range unmatchedReferences {
			records[ownerless[index]].WireReference = reference
			records[ownerless[index]].OwnerSlotReference = ownerByReference[reference]
		}
	}
	for _, record := range records {
		if record.WireReference == 0 {
			complete = false
		}
	}
	return len(records) != 0 && complete
}

// RegionAttachmentNameByIndex returns one decoded attachment name.
func (directory *ProjectRegionAttachmentDirectory) RegionAttachmentNameByIndex(
	index int,
) (string, error) {
	if directory == nil || index < 0 || index >= len(directory.Records) {
		return "", fmt.Errorf("region attachment index %d is out of range", index)
	}
	return directory.Records[index].Name, nil
}
