package spineparser

import (
	"bytes"
	"fmt"
	"sort"
)

const (
	projectTimelinePathPosition = 0x09
	projectTimelinePathSpacing  = 0x0a
)

// ProjectPathConstraintRecord contains one proven 4.3.23 path constraint.
type ProjectPathConstraintRecord struct {
	WireReference       int      `json:"wireReference,omitempty"`
	Name                string   `json:"name"`
	TargetSlotReference int      `json:"targetSlotReference"`
	TargetSlotName      string   `json:"targetSlotName"`
	BoneReferences      []int    `json:"boneReferences"`
	BoneNames           []string `json:"boneNames"`
	Position            float32  `json:"position"`
	Spacing             float32  `json:"spacing"`
	Offset              int      `json:"offset"`
	EndOffset           int      `json:"endOffset"`
}

// ProjectPathConstraintDirectory contains all proven path constraints.
type ProjectPathConstraintDirectory struct {
	Format               string                        `json:"format"`
	Count                int                           `json:"count"`
	ReferencesComplete   bool                          `json:"referencesComplete"`
	Records              []ProjectPathConstraintRecord `json:"records"`
	groupOwnerReferences map[int]int
}

// ProjectPathTimeline identifies one animated path property.
type ProjectPathTimeline struct {
	Property            string                `json:"property"`
	ConstraintReference int                   `json:"constraintReference"`
	Constraint          string                `json:"constraint"`
	Offset              int                   `json:"offset"`
	Keys                []ProjectTransformKey `json:"keys"`
}

// ProjectPathTimelineDirectory contains all supported path timelines.
type ProjectPathTimelineDirectory struct {
	Animation   string                `json:"animation"`
	RegionStart int                   `json:"regionStart"`
	RegionEnd   int                   `json:"regionEnd"`
	FrameRate   int                   `json:"frameRate"`
	Timelines   []ProjectPathTimeline `json:"timelines"`
}

// DiscoverProjectPathConstraints decodes the fixed 4.3.23 path setup layout.
func DiscoverProjectPathConstraints(
	payload []byte,
) (*ProjectPathConstraintDirectory, error) {
	bones, err := DiscoverProjectBones(payload)
	if err != nil {
		return nil, err
	}
	slots, err := DiscoverProjectSlotRecords(payload)
	if err != nil {
		return nil, err
	}
	if !bones.ReferencesComplete || !slots.ReferencesComplete {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "path constraints require complete bone and slot references",
		}
	}
	records := make([]ProjectPathConstraintRecord, 0)
	prefix := []byte{0x01, 0x16, 0x09}
	for offset := 0; offset+len(prefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], prefix) {
			continue
		}
		record, end, ok := readProjectPathConstraint(payload, offset)
		if !ok {
			continue
		}
		targetName := ""
		for _, slot := range slots.Records {
			if slot.WireReference == record.TargetSlotReference {
				targetName = slot.Name
				break
			}
		}
		if targetName == "" {
			continue
		}
		record.TargetSlotName = targetName
		record.BoneNames = make([]string, len(record.BoneReferences))
		valid := true
		for index, reference := range record.BoneReferences {
			name, found := bones.BoneNameByWireReference(reference)
			if !found {
				valid = false
				break
			}
			record.BoneNames[index] = name
		}
		if !valid {
			continue
		}
		records = append(records, record)
		offset = end - 1
	}
	if len(records) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported project path constraints were not found",
		}
	}
	seenNames := make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, duplicate := seenNames[record.Name]; duplicate {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  fmt.Sprintf("path constraint name %q is duplicated", record.Name),
			}
		}
		seenNames[record.Name] = struct{}{}
	}
	referencesComplete := assignProjectPathConstraintWireReferences(
		payload,
		records,
	)
	directory := &ProjectPathConstraintDirectory{
		Format:             "kryo-path-constraint-v2",
		Count:              len(records),
		ReferencesComplete: referencesComplete,
		Records:            records,
	}
	if referencesComplete {
		directory.groupOwnerReferences =
			discoverProjectPathGroupOwnerReferences(payload, directory)
	}
	return directory, nil
}

func readProjectPathConstraint(
	payload []byte,
	offset int,
) (ProjectPathConstraintRecord, int, bool) {
	record := ProjectPathConstraintRecord{Offset: offset}
	cursor := offset + 2
	var ok bool
	_, cursor, ok = readProjectPathFloat(payload, cursor, 0x09)
	if !ok {
		return ProjectPathConstraintRecord{}, offset, false
	}
	_, cursor, ok = readProjectPathFloat(payload, cursor, 0x0a)
	if !ok || cursor >= len(payload) || payload[cursor] != 0x03 {
		return ProjectPathConstraintRecord{}, offset, false
	}
	record.TargetSlotReference, cursor, ok = readPositiveVarint(
		payload,
		cursor+1,
	)
	if !ok || record.TargetSlotReference < projectFirstWireReference ||
		cursor >= len(payload) || payload[cursor] != 0x0e {
		return ProjectPathConstraintRecord{}, offset, false
	}
	cursor, ok = readProjectPathObjectTokenToTag(
		payload,
		cursor+1,
		0x0f,
	)
	if !ok {
		return ProjectPathConstraintRecord{}, offset, false
	}
	cursor, ok = readProjectPathObjectTokenToTag(
		payload,
		cursor+1,
		0x08,
	)
	if !ok {
		return ProjectPathConstraintRecord{}, offset, false
	}
	for _, tag := range []byte{0x08, 0x05, 0x06} {
		_, cursor, ok = readProjectPathFloat(payload, cursor, tag)
		if !ok {
			return ProjectPathConstraintRecord{}, offset, false
		}
	}
	if cursor >= len(payload) || payload[cursor] != 0x10 {
		return ProjectPathConstraintRecord{}, offset, false
	}
	cursor, ok = readProjectPathObjectTokenToTag(
		payload,
		cursor+1,
		0x18,
	)
	if !ok {
		return ProjectPathConstraintRecord{}, offset, false
	}
	_, cursor, ok = readProjectPathFloat(payload, cursor, 0x18)
	if !ok || cursor+2 > len(payload) || payload[cursor] != 0x0b ||
		payload[cursor+1] > 1 {
		return ProjectPathConstraintRecord{}, offset, false
	}
	cursor += 2
	_, cursor, ok = readProjectPathFloat(payload, cursor, 0x07)
	if !ok {
		return ProjectPathConstraintRecord{}, offset, false
	}
	record.Position, cursor, ok = readProjectPathFloat(
		payload,
		cursor,
		0x04,
	)
	if !ok || cursor+3 > len(payload) || payload[cursor] != 0x02 ||
		payload[cursor+1] != 0x0f || payload[cursor+2] != 0x01 {
		return ProjectPathConstraintRecord{}, offset, false
	}
	boneCount, next, ok := readPositiveVarint(payload, cursor+3)
	if !ok || boneCount < 1 || boneCount > 100_000 {
		return ProjectPathConstraintRecord{}, offset, false
	}
	cursor = next
	record.BoneReferences = make([]int, boneCount)
	for index := range record.BoneReferences {
		if cursor >= len(payload) || payload[cursor] != 0x0c {
			return ProjectPathConstraintRecord{}, offset, false
		}
		record.BoneReferences[index], cursor, ok = readPositiveVarint(
			payload,
			cursor+1,
		)
		if !ok || record.BoneReferences[index] < projectFirstWireReference {
			return ProjectPathConstraintRecord{}, offset, false
		}
	}
	_, cursor, ok = readProjectPathFloat(payload, cursor, 0x17)
	if !ok {
		return ProjectPathConstraintRecord{}, offset, false
	}
	record.Spacing, cursor, ok = readProjectPathFloat(
		payload,
		cursor,
		0x0c,
	)
	if !ok {
		return ProjectPathConstraintRecord{}, offset, false
	}
	_, cursor, ok = readProjectPathFloat(payload, cursor, 0x0d)
	if !ok || cursor+2 > len(payload) || payload[cursor] != 0x01 ||
		payload[cursor+1] != 0x01 {
		return ProjectPathConstraintRecord{}, offset, false
	}
	record.Name, cursor, ok = decodeProjectASCII(payload, cursor+2)
	if !ok || record.Name == "" {
		return ProjectPathConstraintRecord{}, offset, false
	}
	record.EndOffset = cursor
	return record, cursor, true
}

func readProjectPathFloat(
	payload []byte,
	offset int,
	tag byte,
) (float32, int, bool) {
	if offset+5 > len(payload) || payload[offset] != tag {
		return 0, offset, false
	}
	value := readProjectFloat32(payload, offset+1)
	if !finiteProjectFloat(value) {
		return 0, offset, false
	}
	return value, offset + 5, true
}

func readProjectPathObjectTokenToTag(
	payload []byte,
	offset int,
	nextTag byte,
) (int, bool) {
	if offset >= len(payload) {
		return offset, false
	}
	if payload[offset] == 0x01 {
		if offset+2 >= len(payload) || payload[offset+2] != nextTag {
			return offset, false
		}
		return offset + 2, true
	}
	_, cursor, ok := readPositiveVarint(payload, offset)
	if !ok || cursor >= len(payload) || payload[cursor] != nextTag {
		return offset, false
	}
	return cursor, true
}

func assignProjectPathConstraintWireReferences(
	payload []byte,
	records []ProjectPathConstraintRecord,
) bool {
	references := make(map[int]struct{}, len(records))
	inlineRecordIndices := make(map[int]struct{}, 1)
	for offset := 0; offset+len(projectTimelinePrefix)+2 < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		if (payload[cursor] != projectTimelinePathPosition &&
			payload[cursor] != projectTimelinePathSpacing) ||
			payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		_, next, ok := readProjectTransformKeysV2(
			payload,
			keyCursor,
			len(payload),
			keyCount,
			1,
		)
		if !ok {
			continue
		}
		if recordIndex := recordIndexByProjectPathOffset(
			records,
			next,
		); recordIndex >= 0 {
			inlineRecordIndices[recordIndex] = struct{}{}
			continue
		}
		reference, _, ok := readPositiveVarint(payload, next)
		if ok && reference >= projectFirstWireReference {
			references[reference] = struct{}{}
		}
	}
	return assignProjectPathConstraintReferenceSet(
		records,
		references,
		inlineRecordIndices,
	)
}

func assignProjectPathConstraintReferenceSet(
	records []ProjectPathConstraintRecord,
	references map[int]struct{},
	inlineRecordIndices map[int]struct{},
) bool {
	if len(references)+len(inlineRecordIndices) < len(records) {
		return false
	}
	sortedReferences := make([]int, 0, len(references))
	for reference := range references {
		sortedReferences = append(sortedReferences, reference)
	}
	sort.Ints(sortedReferences)
	sort.Slice(records, func(left int, right int) bool {
		return records[left].Offset < records[right].Offset
	})
	if len(inlineRecordIndices) == 0 {
		for index := range records {
			records[index].WireReference = sortedReferences[index]
		}
		return true
	}
	if len(inlineRecordIndices) != 1 || len(sortedReferences) == 0 {
		return false
	}
	inlineIndex := -1
	for index := range inlineRecordIndices {
		inlineIndex = index
	}
	firstExplicitIndex := 0
	if firstExplicitIndex == inlineIndex {
		firstExplicitIndex++
	}
	baseReference := 0
	for _, reference := range sortedReferences {
		candidateBase := reference - firstExplicitIndex
		if candidateBase < projectFirstWireReference {
			continue
		}
		valid := true
		for recordIndex := range records {
			if recordIndex == inlineIndex {
				continue
			}
			if _, exists := references[candidateBase+recordIndex]; !exists {
				valid = false
				break
			}
		}
		if !valid {
			continue
		}
		if baseReference != 0 && baseReference != candidateBase {
			return false
		}
		baseReference = candidateBase
	}
	if baseReference == 0 {
		return false
	}
	for index := range records {
		records[index].WireReference = baseReference + index
	}
	return true
}

func recordIndexByProjectPathOffset(
	records []ProjectPathConstraintRecord,
	offset int,
) int {
	for index, record := range records {
		if record.Offset == offset {
			return index
		}
	}
	return -1
}

// DiscoverProjectPathTimelines decodes position and spacing timelines.
func DiscoverProjectPathTimelines(
	payload []byte,
	animation string,
) (*ProjectPathTimelineDirectory, error) {
	animationRecord, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	constraints, err := DiscoverProjectPathConstraints(payload)
	if err != nil {
		return nil, err
	}
	timelines := make([]ProjectPathTimeline, 0)
	for offset := animationRecord.Offset; offset+len(projectTimelinePrefix)+2 < animationRecord.EndOffset; offset++ {
		if !bytes.HasPrefix(
			payload[offset:animationRecord.EndOffset],
			projectTimelinePrefix,
		) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		property := ""
		switch payload[cursor] {
		case projectTimelinePathPosition:
			property = "position"
		case projectTimelinePathSpacing:
			property = "spacing"
		default:
			continue
		}
		if payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(payload, cursor+2)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectTransformKeysV2(
			payload,
			keyCursor,
			animationRecord.EndOffset,
			keyCount,
			1,
		)
		if !ok {
			continue
		}
		constraint, ownerEnd, ownerErr := resolveProjectPathTimelineOwner(
			payload,
			offset,
			next,
			animationRecord.EndOffset,
			constraints,
		)
		if ownerErr != nil {
			return nil, ownerErr
		}
		timelines = append(timelines, ProjectPathTimeline{
			Property:            property,
			ConstraintReference: constraint.WireReference,
			Constraint:          constraint.Name,
			Offset:              offset,
			Keys:                keys,
		})
		if ownerEnd > next {
			offset = ownerEnd - 1
		} else {
			offset = next - 1
		}
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no supported path timelines", animation),
		}
	}
	return &ProjectPathTimelineDirectory{
		Animation:   animation,
		RegionStart: animationRecord.Offset,
		RegionEnd:   animationRecord.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

func resolveProjectPathTimelineOwner(
	payload []byte,
	timelineOffset int,
	start int,
	end int,
	constraints *ProjectPathConstraintDirectory,
) (ProjectPathConstraintRecord, int, error) {
	if start < 0 || start >= end || end > len(payload) {
		return ProjectPathConstraintRecord{}, start, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "path timeline owner boundary is invalid",
		}
	}
	for _, constraint := range constraints.Records {
		if constraint.Offset == start {
			return constraint, constraint.EndOffset, nil
		}
	}
	if !constraints.ReferencesComplete {
		return ProjectPathConstraintRecord{}, start, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "path constraint references are incomplete",
		}
	}
	reference, cursor, ok := readPositiveVarint(payload, start)
	if !ok {
		return ProjectPathConstraintRecord{}, start, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "path timeline constraint reference is invalid",
		}
	}
	for _, constraint := range constraints.Records {
		if constraint.WireReference != reference {
			continue
		}
		if cursor+len(projectTimelinePrefix) <= end &&
			bytes.HasPrefix(payload[cursor:end], projectTimelinePrefix) {
			return constraint, cursor, nil
		}
		if cursor < end && payload[cursor] == 0x01 {
			_, next, ownerOK := readPositiveVarint(payload, cursor+1)
			if ownerOK && next+2 <= end &&
				projectCompactV2OwnerSuffix(
					payload[next],
					payload[next+1],
				) {
				return constraint, next + 2, nil
			}
		}
		break
	}
	if constraintReference, found :=
		constraints.groupOwnerReferences[reference]; found {
		ownerEnd := cursor
		ownerBoundaryOK := cursor+len(projectTimelinePrefix) <= end &&
			bytes.HasPrefix(payload[cursor:end], projectTimelinePrefix)
		if !ownerBoundaryOK {
			ownerEnd, ownerBoundaryOK = readProjectPathGroupTerminalEnd(
				payload,
				cursor,
				end,
			)
		}
		if ownerBoundaryOK {
			for _, constraint := range constraints.Records {
				if constraint.WireReference == constraintReference {
					return constraint, ownerEnd, nil
				}
			}
		}
	}
	if ownerEnd, terminalOK := readProjectPathGroupTerminalEnd(
		payload,
		cursor,
		end,
	); terminalOK {
		sortedReferences := make([]int, 0, len(constraints.Records))
		for _, constraint := range constraints.Records {
			sortedReferences = append(
				sortedReferences,
				constraint.WireReference,
			)
		}
		sort.Ints(sortedReferences)
		contiguous := len(sortedReferences) != 0
		for index := 1; index < len(sortedReferences); index++ {
			if sortedReferences[index] != sortedReferences[index-1]+1 {
				contiguous = false
				break
			}
		}
		if contiguous && reference+1 == sortedReferences[0] {
			for _, constraint := range constraints.Records {
				if constraint.WireReference == sortedReferences[0] {
					return constraint, ownerEnd, nil
				}
			}
		}
	}
	if constraint, ownerEnd, ok := resolveProjectPathTimelineGroupOwner(
		payload,
		timelineOffset,
		start,
		end,
		constraints,
	); ok {
		return constraint, ownerEnd, nil
	}
	return ProjectPathConstraintRecord{}, start, &ParseError{
		Code: ErrInvalidProject,
		Msg:  fmt.Sprintf("path timeline owner reference %d is unresolved", reference),
	}
}

var projectPathTimelineGroupPrefixV2 = []byte{0x13, 0x01, 0x04, 0x07}

func discoverProjectPathGroupOwnerReferences(
	payload []byte,
	constraints *ProjectPathConstraintDirectory,
) map[int]int {
	animations, err := DiscoverProjectAnimations(payload)
	if err != nil {
		return nil
	}
	result := make(map[int]int)
	conflicts := make(map[int]struct{})
	for _, animation := range animations.Records {
		for offset := animation.Offset; offset+len(projectTimelinePrefix)+2 <
			animation.EndOffset; offset++ {
			if !bytes.HasPrefix(
				payload[offset:animation.EndOffset],
				projectTimelinePrefix,
			) {
				continue
			}
			typeOffset := offset + len(projectTimelinePrefix)
			if (payload[typeOffset] != projectTimelinePathPosition &&
				payload[typeOffset] != projectTimelinePathSpacing) ||
				payload[typeOffset+1] != 0x01 {
				continue
			}
			keyCount, keyCursor, ok := readPositiveVarint(
				payload,
				typeOffset+2,
			)
			if !ok || keyCount < 1 || keyCount > 100_000 {
				continue
			}
			_, ownerOffset, ok := readProjectTransformKeysV2(
				payload,
				keyCursor,
				animation.EndOffset,
				keyCount,
				1,
			)
			if !ok {
				continue
			}
			ownerReference, _, ok := readPositiveVarint(
				payload,
				ownerOffset,
			)
			if !ok || ownerReference < projectFirstWireReference {
				continue
			}
			constraint, _, ok := resolveProjectPathTimelineGroupOwner(
				payload,
				offset,
				ownerOffset,
				animation.EndOffset,
				constraints,
			)
			if !ok {
				continue
			}
			if existing, found := result[ownerReference]; found &&
				existing != constraint.WireReference {
				conflicts[ownerReference] = struct{}{}
				delete(result, ownerReference)
				continue
			}
			if _, conflict := conflicts[ownerReference]; !conflict {
				result[ownerReference] = constraint.WireReference
			}
		}
	}
	return result
}

func readProjectPathGroupTerminalEnd(
	payload []byte,
	offset int,
	end int,
) (int, bool) {
	if offset >= end || payload[offset] != 0x01 {
		return offset, false
	}
	_, terminalEnd, ok := readPositiveVarint(payload, offset+1)
	if !ok || terminalEnd+2 > end ||
		!projectCompactV2OwnerSuffix(
			payload[terminalEnd],
			payload[terminalEnd+1],
		) {
		return offset, false
	}
	return terminalEnd + 2, true
}

func resolveProjectPathTimelineGroupOwner(
	payload []byte,
	timelineOffset int,
	ownerOffset int,
	end int,
	constraints *ProjectPathConstraintDirectory,
) (ProjectPathConstraintRecord, int, bool) {
	if timelineOffset < 0 || timelineOffset >= ownerOffset ||
		ownerOffset >= end || end > len(payload) {
		return ProjectPathConstraintRecord{}, ownerOffset, false
	}
	_, cursor, ok := readPositiveVarint(payload, ownerOffset)
	if !ok {
		return ProjectPathConstraintRecord{}, ownerOffset, false
	}
	terminalEnd, ok := readProjectPathGroupTerminalEnd(
		payload,
		cursor,
		end,
	)
	if !ok {
		return ProjectPathConstraintRecord{}, ownerOffset, false
	}
	groupPrefixOffset := bytes.LastIndex(
		payload[:timelineOffset],
		projectPathTimelineGroupPrefixV2,
	)
	if groupPrefixOffset < 0 {
		return ProjectPathConstraintRecord{}, ownerOffset, false
	}
	groupStart := groupPrefixOffset + len(projectPathTimelineGroupPrefixV2)
	matches := make(map[int]ProjectPathConstraintRecord, 1)
	for offset := groupStart; offset < timelineOffset; offset++ {
		if offset+len(projectTimelinePrefix)+2 >= timelineOffset ||
			!bytes.HasPrefix(
				payload[offset:timelineOffset],
				projectTimelinePrefix,
			) {
			continue
		}
		typeOffset := offset + len(projectTimelinePrefix)
		if (payload[typeOffset] != projectTimelinePathPosition &&
			payload[typeOffset] != projectTimelinePathSpacing) ||
			payload[typeOffset+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(
			payload,
			typeOffset+2,
		)
		if !countOK || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		_, next, keysOK := readProjectTransformKeysV2(
			payload,
			keyCursor,
			timelineOffset,
			keyCount,
			1,
		)
		if !keysOK {
			continue
		}
		recordIndex := recordIndexByProjectPathOffset(
			constraints.Records,
			next,
		)
		if recordIndex >= 0 {
			record := constraints.Records[recordIndex]
			matches[record.WireReference] = record
		}
	}
	if len(matches) != 1 {
		return ProjectPathConstraintRecord{}, ownerOffset, false
	}
	for _, record := range matches {
		return record, terminalEnd, true
	}
	return ProjectPathConstraintRecord{}, ownerOffset, false
}
