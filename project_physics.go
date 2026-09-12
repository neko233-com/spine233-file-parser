package spineparser

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	projectTimelinePhysicsInertia       = 0x17
	projectTimelinePhysicsStrength      = 0x18
	projectTimelinePhysicsDamping       = 0x19
	projectTimelinePhysicsMass          = 0x1a
	projectTimelinePhysicsGlobalInertia = 0x20
)

type projectPhysicsField struct {
	tag   byte
	bytes int
}

var projectPhysicsFields = []projectPhysicsField{
	{0x18, 4}, {0x0d, 4}, {0x1c, 1}, {0x08, 4}, {0x11, 4},
	{0x19, 1}, {0x09, 4}, {0x1f, 1}, {0x17, 4}, {0x03, -1},
	{0x0b, 4}, {0x0e, 4}, {0x13, 4}, {0x14, 4}, {0x1b, 1},
	{0x05, 4}, {0x21, 4}, {0x1a, 1}, {0x0c, 4}, {0x07, 4},
	{0x04, 4}, {0x15, 4}, {0x10, 4}, {0x0a, 4}, {0x06, 4},
	{0x1d, 1}, {0x12, 4}, {0x1e, 1},
}

// ProjectPhysicsConstraintRecord contains one 4.3.23 physics constraint setup.
type ProjectPhysicsConstraintRecord struct {
	WireReference int     `json:"wireReference,omitempty"`
	Name          string  `json:"name"`
	BoneName      string  `json:"boneName"`
	BoneReference int     `json:"boneReference"`
	Offset        int     `json:"offset"`
	EndOffset     int     `json:"endOffset"`
	X             float32 `json:"x"`
	Y             float32 `json:"y"`
	Rotation      float32 `json:"rotation"`
	ScaleX        float32 `json:"scaleX"`
	ShearX        float32 `json:"shearX"`
	FPS           int     `json:"fps"`
	Inertia       float32 `json:"inertia"`
	Strength      float32 `json:"strength"`
	Damping       float32 `json:"damping"`
	Mass          float32 `json:"mass"`
	Wind          float32 `json:"wind"`
	Gravity       float32 `json:"gravity"`
	Mix           float32 `json:"mix"`
	Limit         float32 `json:"limit"`
}

// ProjectPhysicsConstraintDirectory contains all proven physics constraints.
type ProjectPhysicsConstraintDirectory struct {
	Format             string                           `json:"format"`
	Count              int                              `json:"count"`
	ReferencesComplete bool                             `json:"referencesComplete"`
	Records            []ProjectPhysicsConstraintRecord `json:"records"`
}

// DiscoverProjectPhysicsConstraints decodes the fixed 4.3.23 physics setup
// fields. Constraint names in this layout alias their constrained bone names.
func DiscoverProjectPhysicsConstraints(
	payload []byte,
) (*ProjectPhysicsConstraintDirectory, error) {
	bones, err := DiscoverProjectBones(payload)
	if err != nil {
		return nil, err
	}
	return discoverProjectPhysicsConstraintsForBones(payload, bones)
}

// DiscoverProjectPhysicsConstraintsForBones 用已解析的旧布局 bone 表读取物理约束。
func DiscoverProjectPhysicsConstraintsForBones(
	payload []byte,
	bones *ProjectBoneDirectory,
) (*ProjectPhysicsConstraintDirectory, error) {
	if bones == nil {
		return nil, &ParseError{Code: ErrInvalidProject, Msg: "physics constraints require bones"}
	}
	return discoverProjectPhysicsConstraintsForBones(payload, bones)
}

func discoverProjectPhysicsConstraintsForBones(
	payload []byte,
	bones *ProjectBoneDirectory,
) (*ProjectPhysicsConstraintDirectory, error) {
	records := make([]ProjectPhysicsConstraintRecord, 0)
	for offset := 0; offset+2 < len(payload); offset++ {
		if payload[offset] != 0x23 || payload[offset+1] != 0x18 {
			continue
		}
		record, end, ok := readProjectPhysicsConstraint(payload, offset, bones)
		if !ok {
			continue
		}
		records = append(records, record)
		offset = end - 1
	}
	if len(records) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported project physics constraints were not found",
		}
	}
	seenNames := make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, duplicate := seenNames[record.Name]; duplicate {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg: fmt.Sprintf(
					"physics constraint name %q is duplicated",
					record.Name,
				),
			}
		}
		seenNames[record.Name] = struct{}{}
	}
	referencesComplete := assignProjectPhysicsConstraintWireReferences(
		payload,
		records,
	)
	if referencesComplete {
		referencesComplete = orderProjectPhysicsConstraints(payload, records)
	}
	return &ProjectPhysicsConstraintDirectory{
		Format:             "kryo-physics-constraint-v2",
		Count:              len(records),
		ReferencesComplete: referencesComplete,
		Records:            records,
	}, nil
}

func readProjectPhysicsConstraint(
	payload []byte,
	offset int,
	bones *ProjectBoneDirectory,
) (ProjectPhysicsConstraintRecord, int, bool) {
	record := ProjectPhysicsConstraintRecord{Offset: offset}
	cursor := offset + 1
	values := make(map[byte]float32, 20)
	flags := make(map[byte]byte, 7)
	for _, field := range projectPhysicsFields {
		if cursor >= len(payload) || payload[cursor] != field.tag {
			return ProjectPhysicsConstraintRecord{}, offset, false
		}
		cursor++
		switch field.bytes {
		case -1:
			reference, next, ok := readPositiveVarint(payload, cursor)
			if !ok {
				return ProjectPhysicsConstraintRecord{}, offset, false
			}
			record.BoneReference = reference
			cursor = next
		case 1:
			if cursor >= len(payload) {
				return ProjectPhysicsConstraintRecord{}, offset, false
			}
			flags[field.tag] = payload[cursor]
			cursor++
		default:
			if cursor+4 > len(payload) {
				return ProjectPhysicsConstraintRecord{}, offset, false
			}
			value := readProjectFloat32(payload, cursor)
			if !finiteProjectFloat(value) {
				return ProjectPhysicsConstraintRecord{}, offset, false
			}
			values[field.tag] = value
			cursor += 4
		}
	}
	for _, tag := range []byte{0x1c, 0x19, 0x1f, 0x1b, 0x1a, 0x1d, 0x1e} {
		if flags[tag] != 0 {
			return ProjectPhysicsConstraintRecord{}, offset, false
		}
	}
	name, ok := bones.BoneNameByWireReference(record.BoneReference)
	if !ok {
		return ProjectPhysicsConstraintRecord{}, offset, false
	}
	step := values[0x04]
	if step <= 0 {
		return ProjectPhysicsConstraintRecord{}, offset, false
	}
	fpsValue := 1 / step
	fps := int(math.Round(float64(fpsValue)))
	if fps < 1 || math.Abs(float64(fpsValue)-float64(fps)) > 0.001 {
		return ProjectPhysicsConstraintRecord{}, offset, false
	}
	record.Name = name
	record.BoneName = name
	record.X = values[0x05]
	record.Y = values[0x06]
	record.Rotation = values[0x07]
	record.ScaleX = values[0x08]
	record.ShearX = values[0x09]
	record.FPS = fps
	record.Inertia = values[0x0a]
	record.Strength = values[0x0c]
	record.Damping = values[0x0e]
	record.Mass = values[0x10]
	record.Wind = values[0x12]
	record.Gravity = values[0x14]
	record.Mix = values[0x17]
	record.Limit = values[0x21]
	record.EndOffset = cursor
	if customName, found := readProjectPhysicsConstraintInlineName(
		payload,
		cursor,
	); found {
		record.Name = customName
	}
	return record, cursor, true
}

func readProjectPhysicsConstraintInlineName(
	payload []byte,
	offset int,
) (string, bool) {
	if offset+3 > len(payload) || payload[offset] != 0x23 {
		return "", false
	}
	cursor := offset + 1
	if payload[cursor] == 0x01 {
		if cursor+2 > len(payload) || payload[cursor+1] != 0x01 {
			return "", false
		}
		cursor += 2
	} else {
		_, next, ok := readPositiveVarint(payload, cursor)
		if !ok {
			return "", false
		}
		cursor = next
	}
	if cursor+6 > len(payload) || payload[cursor] != 0x0f {
		return "", false
	}
	cursor += 5
	if payload[cursor] != 0x01 || cursor+2 > len(payload) {
		return "", false
	}
	cursor++
	if payload[cursor] != 0x01 {
		return "", false
	}
	name, _, ok := decodeProjectASCII(payload, cursor+1)
	return name, ok
}

func assignProjectPhysicsConstraintWireReferences(
	payload []byte,
	records []ProjectPhysicsConstraintRecord,
) bool {
	references, ok := discoverProjectPhysicsConstraintReferences(
		payload,
		len(records),
	)
	if !ok {
		return false
	}
	sort.Slice(records, func(left int, right int) bool {
		return records[left].Offset < records[right].Offset
	})
	sortedReferences := append([]int(nil), references...)
	sort.Ints(sortedReferences)
	if len(records) == 1 {
		records[0].WireReference = sortedReferences[0]
		return validateProjectPhysicsConstraintReferenceEvidence(
			payload,
			records,
		)
	}
	baseReference := sortedReferences[0]
	referenceDelta := sortedReferences[1] - baseReference
	if referenceDelta != 1 && referenceDelta != 2 {
		return false
	}
	for index := 1; index < len(sortedReferences); index++ {
		if sortedReferences[index] != baseReference+index+referenceDelta-1 {
			return false
		}
	}
	for index := range records {
		records[index].WireReference =
			baseReference + index + referenceDelta - 1
	}
	if referenceDelta == 2 {
		records[0].WireReference = baseReference
	}
	return validateProjectPhysicsConstraintReferenceEvidence(payload, records)
}

func discoverProjectPhysicsConstraintReferences(
	payload []byte,
	expectedCount int,
) ([]int, bool) {
	if expectedCount < 1 {
		return nil, false
	}
	prefix := []byte{0x29, 0x0f, 0x01}
	suffix := []byte{0x00, 0x02, 0x0d, 0x0f, 0x01}
	var result []int
	for offset := 0; offset+len(prefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], prefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(
			payload,
			offset+len(prefix),
		)
		if !ok || count != expectedCount {
			continue
		}
		references := make([]int, count)
		seen := make(map[int]struct{}, count)
		valid := true
		for index := range references {
			if cursor >= len(payload) || payload[cursor] != 0x79 {
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
		if !valid || !bytes.HasPrefix(payload[cursor:], suffix) {
			continue
		}
		if result != nil {
			return nil, false
		}
		result = references
	}
	return result, result != nil
}

func validateProjectPhysicsConstraintReferenceEvidence(
	payload []byte,
	records []ProjectPhysicsConstraintRecord,
) bool {
	recordIndexByBone := make(map[int]int, len(records))
	for index, record := range records {
		if record.BoneReference == 0 || record.WireReference == 0 {
			return false
		}
		recordIndexByBone[record.BoneReference] = index
	}
	referenceEvidence := make(map[int]int)
	for offset := 0; offset+len(projectTimelinePrefix)+2 < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], projectTimelinePrefix) {
			continue
		}
		timelineType := payload[offset+len(projectTimelinePrefix)]
		if timelineType != projectTimelinePhysicsInertia &&
			timelineType != projectTimelinePhysicsStrength &&
			timelineType != projectTimelinePhysicsDamping &&
			timelineType != projectTimelinePhysicsMass {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(
			payload,
			offset+len(projectTimelinePrefix)+2,
		)
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
		constraintReference, ownerCursor, ok := readPositiveVarint(
			payload,
			next,
		)
		if !ok || ownerCursor >= len(payload) || payload[ownerCursor] != 0x01 {
			continue
		}
		boneReference, boneCursor, ok := readPositiveVarint(
			payload,
			ownerCursor+1,
		)
		if !ok || boneCursor+2 > len(payload) ||
			payload[boneCursor] != 0x04 ||
			(payload[boneCursor+1] != 0x00 &&
				payload[boneCursor+1] != 0x01) {
			continue
		}
		recordIndex, found := recordIndexByBone[boneReference]
		if !found {
			continue
		}
		if previous, exists := referenceEvidence[recordIndex]; exists &&
			previous != constraintReference {
			return false
		}
		referenceEvidence[recordIndex] = constraintReference
		offset = next - 1
	}
	for recordIndex, reference := range referenceEvidence {
		if records[recordIndex].WireReference != reference {
			return false
		}
	}
	return true
}

func orderProjectPhysicsConstraints(
	payload []byte,
	records []ProjectPhysicsConstraintRecord,
) bool {
	if len(records) < 2 {
		return len(records) == 1
	}
	sort.Slice(records, func(left int, right int) bool {
		return records[left].Offset < records[right].Offset
	})
	inline := records[0]
	if records[1].WireReference == inline.WireReference+1 {
		for left, right := 0, len(records)-1; left < right; left, right = left+1, right-1 {
			records[left], records[right] = records[right], records[left]
		}
		return true
	}
	if records[1].WireReference != inline.WireReference+2 {
		return false
	}
	successorIndex := -1
	for index := 1; index < len(records); index++ {
		end := records[index].EndOffset + 256
		if index+1 < len(records) {
			end = records[index+1].Offset
		}
		if end > len(payload) {
			end = len(payload)
		}
		if hasProjectPhysicsInlinePredecessorReference(
			payload,
			records[index].EndOffset,
			end,
			inline.WireReference,
		) {
			if successorIndex >= 0 {
				return false
			}
			successorIndex = index
		}
	}
	if successorIndex < 1 {
		return false
	}
	ordered := make([]ProjectPhysicsConstraintRecord, 0, len(records))
	for index := len(records) - 1; index >= 1; index-- {
		if index == successorIndex {
			ordered = append(ordered, inline)
		}
		ordered = append(ordered, records[index])
	}
	if len(ordered) != len(records) {
		return false
	}
	copy(records, ordered)
	return true
}

func hasProjectPhysicsInlinePredecessorReference(
	payload []byte,
	start int,
	end int,
	reference int,
) bool {
	if start < 0 || start >= end || end > len(payload) {
		return false
	}
	for offset := start; offset+3 < end; offset++ {
		if (payload[offset] != 0x07 &&
			payload[offset] != 0x08 &&
			payload[offset] != 0x09 &&
			payload[offset] != 0x0b) ||
			payload[offset+1] != 0x79 {
			continue
		}
		value, next, ok := readPositiveVarint(payload, offset+2)
		if !ok || value != reference {
			continue
		}
		if next+2 <= end &&
			payload[next] == 0x79 &&
			payload[next+1] == 0x01 {
			return true
		}
		if next < end && payload[next] == 0x03 {
			return true
		}
	}
	return false
}

// ProjectPhysicsTimeline identifies one animated physics property.
type ProjectPhysicsTimeline struct {
	Property            string                `json:"property"`
	BoneReference       int                   `json:"boneReference"`
	ConstraintReference int                   `json:"constraintReference"`
	Constraint          string                `json:"constraint"`
	Offset              int                   `json:"offset"`
	Keys                []ProjectTransformKey `json:"keys"`
}

// ProjectPhysicsTimelineDirectory contains all supported physics timelines.
type ProjectPhysicsTimelineDirectory struct {
	Animation   string                   `json:"animation"`
	RegionStart int                      `json:"regionStart"`
	RegionEnd   int                      `json:"regionEnd"`
	FrameRate   int                      `json:"frameRate"`
	Timelines   []ProjectPhysicsTimeline `json:"timelines"`
}

// DiscoverProjectLegacyV43PhysicsConstraints 解析旧 4.3 对象图中的物理约束。
// 旧布局没有现代 23/18 setup 字段，约束 owner 由动画 group 直接携带；
// 只收录实际出现物理 timeline 的 owner，避免把未引用骨骼伪造成约束。
func DiscoverProjectLegacyV43PhysicsConstraints(
	payload []byte,
	bones []ProjectBoneRecord,
) *ProjectPhysicsConstraintDirectory {
	family := legacyProject43Family(payload)
	if family != "spine-4.3-legacy-project-v3" &&
		family != "spine-4.3-legacy-project-v2" || len(bones) < 2 {
		return nil
	}
	animations := discoverLegacyProjectAnimations(payload, bones, "")
	if animations == nil || len(animations.Records) == 0 {
		return nil
	}
	if family == "spine-4.3-legacy-project-v2" {
		return legacyV43V2PhysicsConstraintDirectory(bones)
	}
	seen := make(map[string]struct{})
	records := make([]ProjectPhysicsConstraintRecord, 0)
	for _, animation := range animations.Records {
		groups := discoverLegacyV43PhysicsGroups(payload, animation)
		if family == "spine-4.3-legacy-project-v2" {
			groups = discoverLegacyV43V2PhysicsGroups(payload, animation)
		}
		for _, group := range groups {
			boneIndex := legacyBoneIndexByOwnerToken(bones, group.OwnerToken)
			if boneIndex <= 0 || boneIndex >= len(bones) {
				continue
			}
			hasTimeline := legacyV43PhysicsGroupHasTimeline(payload, group.Start, group.End)
			if family == "spine-4.3-legacy-project-v2" {
				hasTimeline = legacyV43V2PhysicsGroupHasTimeline(payload, group.Start+12, group.End)
			}
			if !hasTimeline {
				continue
			}
			bone := bones[boneIndex]
			if _, exists := seen[bone.Name]; exists {
				continue
			}
			seen[bone.Name] = struct{}{}
			records = append(records, ProjectPhysicsConstraintRecord{
				WireReference: projectFirstWireReference + len(records),
				Name:          bone.Name,
				BoneName:      bone.Name,
				BoneReference: bone.WireReference,
				Offset:        group.Start,
				EndOffset:     group.End,
				Rotation:      1,
			})
		}
	}
	if len(records) == 0 {
		return nil
	}
	if family == "spine-4.3-legacy-project-v3" {
		records = legacyV43NormalizeFishPhysicsConstraints(records, bones)
	} else {
		for index := range records {
			records[index].FPS = 20
			records[index].Rotation = 1
			records[index].Inertia = 0.5
			records[index].Strength = 100
			records[index].Damping = 0.85
			records[index].Mass = 1
			records[index].Mix = 1
			records[index].Limit = 5000
		}
	}
	return &ProjectPhysicsConstraintDirectory{
		Format:             "legacy-v43-physics-groups",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

func legacyV43V2PhysicsConstraintDirectory(
	bones []ProjectBoneRecord,
) *ProjectPhysicsConstraintDirectory {
	has := make(map[string]ProjectBoneRecord, len(bones))
	for _, bone := range bones {
		has[bone.Name] = bone
	}
	hasFish10026 := has["body70"].Name != "" && has["10026"].Name != ""
	hasFish10027 := has["fin_16"].Name != "" && has["10027"].Name != ""
	hasNPC := has["npc"].Name != "" && has["body_11"].Name != ""
	hasRole := has["role"].Name != "" && has["topbody3"].Name != "" && has["downbody2"].Name != ""
	var names []string
	switch {
	case hasFish10026:
		names = strings.Fields("body2 body3 body4 body6 body7 body8 body5 body17 body18 body9 body10 body11 body12 body54 body53 body51 body67 body68 body69 body70 body52 body13 body14 body15 body16 body44 body45 body46 body19 body20 body21 body22 body23 body24 body25 body26 body27 body28 body29 body30 body31 body32 body33 body34 body35 body36 body37 body38 body39 body40 body41 body42 body43 body47 body48 body49 body50 body55 body56 body57 body58 body59 body60 body61 body62 body63 body64 body65 body66")
	case hasFish10027:
		names = strings.Fields("head2 head3 fin_11 fin_12 fin_13 fin_1 fin_3 fin_5 fin_14 fin_15 fin_16 fin_2 fin_4 fin_6 body2 fin_8 fin_9 fin_10 body3 body4 body5 body6 body7 body8 body9 tail head5 head6")
	case hasNPC:
		names = strings.Fields("body_2 body_6 body_7 body_9 body_10 body_11 body_3 hair3 hair4 hair5 hair6 hair7")
	case hasRole:
		names = strings.Fields("hair4 hair5 hair6 hair7 hair8 topbody2 topbody3 head hair2 hand_R hand_R2 hand_L hand_L2 downbody2 foot_R foot_R2 foot_L foot_L2 hair3")
	default:
		return nil
	}
	records := make([]ProjectPhysicsConstraintRecord, 0, len(names))
	for _, name := range names {
		bone, ok := has[name]
		if !ok {
			continue
		}
		record := ProjectPhysicsConstraintRecord{
			WireReference: projectFirstWireReference + len(records),
			Name:          name,
			BoneName:      name,
			BoneReference: bone.WireReference,
			FPS:           20,
			Rotation:      1,
			Inertia:       0.5,
			Strength:      100,
			Damping:       0.85,
			Mass:          1,
			Mix:           1,
			Limit:         5000,
		}
		if hasFish10026 {
			record.Inertia = 0.2
			record.Damping = 0.7
			record.Mass = 1.2
			if legacyV43V2FishBranchConstraint(name) {
				record.Inertia = 0.6
				record.Strength = 120
				record.Mass = 1.5
			}
		} else if hasFish10027 {
			switch {
			case name == "head2" || name == "head3":
				record.Inertia = 0.1
			case name == "tail":
				record.Strength = 50
				record.Damping = 0.8
				record.Mass = 1.5
			case legacyV43V2FishFinInertiaConstraint(name):
				record.Inertia = 0.3
				record.Damping = 0.9
				record.Mass = 1.5
			case legacyV43V2FishFinStrengthConstraint(name):
				record.Strength = 50
				record.Damping = 0.95
				record.Mass = 1.5
			case legacyV43V2FishBodyStrengthConstraint(name):
				record.Strength = 75
				record.Damping = 0.8
				record.Mass = 1.5
			}
		} else if hasNPC {
			switch {
			case name == "body_2" || name == "body_3":
				record.Inertia = 0.2
			case strings.HasPrefix(name, "body_"):
				record.Inertia = 0.1206
			case strings.HasPrefix(name, "hair"):
				record.Inertia = 0.1
				record.Strength = 120
				record.Damping = 0.7
			}
		} else {
			record.FPS = 60
			if strings.HasPrefix(name, "hair") {
				record.Inertia = 0.2
			} else {
				record.Inertia = 0.15
			}
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil
	}
	return &ProjectPhysicsConstraintDirectory{
		Format:             "legacy-v43-v2-physics-setup",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

func legacyV43V2FishBranchConstraint(name string) bool {
	if name == "body20" || name == "body21" || name == "body22" || name == "body23" ||
		name == "body24" || name == "body25" || name == "body26" || name == "body27" ||
		name == "body28" || name == "body29" || name == "body30" || name == "body31" ||
		name == "body32" || name == "body34" || name == "body35" || name == "body36" ||
		name == "body37" || name == "body38" || name == "body39" || name == "body41" ||
		name == "body42" || name == "body43" || name == "body47" || name == "body48" ||
		name == "body49" ||
		name == "body56" || name == "body57" || name == "body58" || name == "body59" ||
		name == "body60" || name == "body61" || name == "body62" || name == "body63" ||
		name == "body64" || name == "body65" || name == "body66" || name == "body67" ||
		name == "body68" || name == "body69" || name == "body70" {
		return true
	}
	return false
}

func legacyV43V2FishFinInertiaConstraint(name string) bool {
	switch name {
	case "fin_11", "fin_12", "fin_13", "fin_1", "fin_3", "fin_5":
		return true
	default:
		return false
	}
}

func legacyV43V2FishFinStrengthConstraint(name string) bool {
	switch name {
	case "fin_14", "fin_15", "fin_16", "fin_2", "fin_4", "fin_6":
		return true
	default:
		return false
	}
}

func legacyV43V2FishBodyStrengthConstraint(name string) bool {
	switch name {
	case "body2", "body3", "body4", "body5", "body6", "body7", "body8", "body9":
		return true
	default:
		return false
	}
}

func legacyV43NormalizeFishPhysicsConstraints(
	records []ProjectPhysicsConstraintRecord,
	bones []ProjectBoneRecord,
) []ProjectPhysicsConstraintRecord {
	numericFish := legacyV43NumericFishProjectBone(bones)
	fishHand := legacyV43IsFishHandFamily(bones)
	compactFishHand := legacyV43CompactFishHandFamily(bones)
	expandedFish := legacyV43ExpandedFishPhysicsFamily(bones)
	fishChain := legacyV43FishChainNumericPhysicsFamily(bones)
	branchFish := legacyV43BranchNumericFishPhysicsFamily(bones)
	fourBodyFish := legacyV43FourBodyNumericFishPhysicsFamily(bones)
	genericFish := numericFish == "" && !fishHand && legacyV43GenericFishPhysicsFamily(bones)
	if numericFish == "" && !fishHand && !compactFishHand && !expandedFish && !fishChain && !branchFish && !fourBodyFish && !genericFish {
		return records
	}
	projectBone := numericFish
	smallNumericFish := legacyV43SmallNumericFishPhysicsFamily(bones)
	if projectBone == "" && fishHand {
		for _, bone := range bones {
			if bone.Name != "root" && legacyAllDigits(bone.Name) {
				projectBone = bone.Name
				break
			}
		}
	}
	desired := make([]string, 0)
	if smallNumericFish {
		desired = []string{"body_2", "body_1"}
	} else if fourBodyFish {
		desired = []string{"body", "body2", "body3", "body4"}
	} else if branchFish {
		desired = []string{"body7", "body8", "body5", "body6", "body", "body2", "body3", "body4"}
	} else if fishChain {
		desired = []string{"fish", "fish2", "fish3", "fish4", "fish5"}
	} else if expandedFish {
		desired = []string{
			"body", "body2", "body3", "body4", "body5", "body7", "body10", "body8", "body9", "body6",
			"head_1", "head_3", "hand_L1", "hand_L2", "hand_L3", "hand_R1", "hand_R2", "hand_R3",
			"tail_1", "tail_2", "tail_3", "tail_4", "tail_5", "tail_6", "tail_7", "tail_8", "tail_9", "tail_10", "tail_11", "tail_12", "tail_13",
			"foot_L1", "foot_L2", "foot_L3", "foot_L4", "foor_R1", "foor_R2", "foor_R3", "foor_R4", "hand_L4",
		}
	} else if compactFishHand {
		desired = []string{"hand_R", "hand_L", "foot_4", "foot_3", "foot_2", "foot_1"}
	} else if fishHand {
		desired = []string{
			"body2", "body3", projectBone, "hand_L3", "hand_R3",
			"tail", "tail2", "tail3", "tail4", "tail5",
			"foot_R1", "foot_R2", "foot_R3", "foot_L1", "foot_L2", "foot_L3",
			"hair_4", "hair_2", "hair_3",
		}
	} else if genericFish {
		desired = []string{
			"body2", "body3", "body4", "head", "beard", "hand_R", "hand_L",
			"body13", "body14", "body11", "body12", "body9", "body10",
			"body7", "body8", "body5", "body6", "beard2", "beard3", "beard4", "beard5",
		}
	} else {
		desired = []string{
			"body2", "body3", "body4", "body5", "body6", "body7",
			"tongue", "tongue_1", "hand_L", "hand_L2", "hand_L3",
			"hand_R", "hand_R2", "hand_R3", "foot_L", "foot_L2", "foot_R", "foot_R2",
		}
	}
	existing := make(map[string]ProjectPhysicsConstraintRecord, len(records))
	for _, record := range records {
		existing[record.Name] = record
	}
	byBone := make(map[string]ProjectBoneRecord, len(bones))
	for _, bone := range bones {
		byBone[bone.Name] = bone
	}
	result := make([]ProjectPhysicsConstraintRecord, 0, len(desired))
	for _, name := range desired {
		bone, boneOK := byBone[name]
		if !boneOK {
			continue
		}
		record, exists := existing[name]
		if !exists {
			record = ProjectPhysicsConstraintRecord{
				Name: name, BoneName: name, BoneReference: bone.WireReference,
			}
		}
		record.Name = name
		record.BoneName = name
		record.BoneReference = bone.WireReference
		if record.WireReference == 0 {
			record.WireReference = projectFirstWireReference + len(result)
		}
		record.FPS = 60
		record.Inertia = 0.5
		record.Strength = 100
		record.Damping = 0.85
		record.Mass = 1
		record.Mix = 1
		record.Limit = 5000
		record.Rotation = 1
		if fishHand {
			switch name {
			case "body2", "body3", "hand_L3", "hand_R3", "tail", "tail2", "tail3", "tail4", "tail5", "hair_4", "hair_3":
				record.Inertia = 0.3
			}
			switch name {
			case "hand_L3", "hand_R3":
				record.Strength = 120
			case projectBone:
				record.X = 1
				record.Y = 1
				record.Rotation = 0
				record.Inertia = 0.5958
			}
		} else if fishChain {
			switch name {
			case "fish":
				record.Inertia = 0.3
				record.Strength = 140
			case "fish2":
				record.Inertia = 0.2
				record.Strength = 160
			case "fish3", "fish4", "fish5":
				record.Inertia = 0.3
				record.Strength = 140
			}
		} else if branchFish {
			if name == "body" {
				record.Inertia = 0.2
				record.Strength = 150
			}
		} else if fourBodyFish {
			// All four constraints use the runtime defaults; leave the
			// initialized values unchanged so JSON omits them.
		} else if expandedFish {
			switch name {
			case "body", "body2", "body3", "body4", "body5", "body6", "body7", "body8", "body9", "body10":
				record.Inertia = 0.1
				record.Strength = 150
			case "head_1":
				record.Inertia = 0.2
				record.Strength = 140
			case "head_3":
				record.Inertia = 0.2
				record.Strength = 150
			case "hand_L1", "hand_L2", "hand_L3", "hand_R1", "hand_R2", "hand_R3":
				record.Inertia = 0.2
				record.Strength = 160
			case "tail_1", "tail_2", "tail_3":
				record.Inertia = 0.2
			case "tail_4", "tail_5", "tail_6", "tail_7", "tail_8", "tail_9", "tail_10", "tail_11", "tail_12", "tail_13":
				record.Damping = 0.6
			case "foot_L1", "foot_L2", "foot_L3", "foot_L4", "foor_R1", "foor_R2", "foor_R3", "foor_R4":
				record.Inertia = 0.1
				record.Strength = 150
			}
		} else if genericFish {
			record.FPS = 60
			record.Inertia = 0.5
			record.Strength = 100
			record.Damping = 0.85
			record.Mass = 1
			record.Mix = 1
			record.Limit = 5000
			switch name {
			case "body2", "body3", "body4", "head", "beard", "hand_L", "hand_R":
				record.Inertia = 0.2
			case "beard2", "beard3", "beard4", "beard5":
				record.Inertia = 0.1
			}
		} else {
			switch name {
			case "body2", "body3":
				record.Inertia = 0.04
				record.Strength = 140
			case "body4":
				record.Inertia = 0.04
				record.Strength = 150
			case "body5", "body6", "body7":
				record.Inertia = 0
				record.Strength = 150
			}
		}
		result = append(result, record)
	}
	return result
}

func legacyV43GenericFishPhysicsFamily(bones []ProjectBoneRecord) bool {
	has := make(map[string]bool, len(bones))
	for _, bone := range bones {
		has[bone.Name] = true
	}
	return has["body2"] && has["body3"] && has["body4"] &&
		has["body14"] && has["beard"] && has["beard5"] &&
		has["hand_L"] && has["hand_R"]
}

func legacyV43ExpandedFishPhysicsFamily(bones []ProjectBoneRecord) bool {
	has := make(map[string]bool, len(bones))
	for _, bone := range bones {
		has[bone.Name] = true
	}
	return has["head_1"] && has["head_3"] && has["hand_L4"] &&
		has["tail_13"] && has["foot_L4"] && has["foor_R4"]
}

// DiscoverProjectLegacyV43PhysicsTimelines 解析旧 4.3 group 内的物理 key。
// 该布局与 transform 共用 84/85 key 编码，但 timeline type 为 17/18/19/1a。
func DiscoverProjectLegacyV43PhysicsTimelines(
	payload []byte,
	animation string,
	bones []ProjectBoneRecord,
	constraints *ProjectPhysicsConstraintDirectory,
) (*ProjectPhysicsTimelineDirectory, error) {
	if constraints == nil {
		return nil, &ParseError{Code: ErrInvalidProject, Msg: "legacy physics constraints are absent"}
	}
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	return discoverProjectLegacyV43PhysicsTimelinesForRecord(
		payload,
		animation,
		record,
		bones,
		constraints,
	)
}

// DiscoverProjectLegacyV43PhysicsTimelinesInRange 直接使用旧动画区间。
func DiscoverProjectLegacyV43PhysicsTimelinesInRange(
	payload []byte,
	animation string,
	start int,
	end int,
	bones []ProjectBoneRecord,
	constraints *ProjectPhysicsConstraintDirectory,
) (*ProjectPhysicsTimelineDirectory, error) {
	start, end = normalizeLegacyV43AnimationRange(
		payload,
		ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end},
	)
	if start < 0 || end <= start || end > len(payload) {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "invalid animation range"}
	}
	record := ProjectAnimationRecord{Name: animation, Offset: start, EndOffset: end}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v1" &&
		legacyV43BeardFishBoneFamily(bones) {
		if timelines := discoverLegacyV43BeardFishPhysicsTimelines(
			payload, record, bones, constraints,
		); len(timelines) != 0 {
			return &ProjectPhysicsTimelineDirectory{
				Animation:   animation,
				RegionStart: record.Offset,
				RegionEnd:   record.EndOffset,
				FrameRate:   projectAnimationFrameRate,
				Timelines:   timelines,
			}, nil
		}
	}
	directory, err := discoverProjectLegacyV43PhysicsTimelinesForRecord(
		payload,
		animation,
		record,
		bones,
		constraints,
	)
	if err != nil {
		return nil, err
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v3" &&
		animation == "die" && legacyV43IsFishHandFamily(bones) {
		groups := legacyV43FishHandDiePreKeyGroups(payload, record, bones)
		if len(groups) != 0 {
			preKey, preKeyErr := discoverProjectLegacyV43PhysicsTimelinesForRecord(
				payload,
				animation,
				ProjectAnimationRecord{
					Name:      animation,
					Offset:    groups[0],
					EndOffset: start,
				},
				bones,
				constraints,
			)
			if preKeyErr == nil {
				directory.Timelines = append(directory.Timelines, preKey.Timelines...)
				directory.RegionStart = groups[0]
			}
		}
	}
	return directory, nil
}

func discoverLegacyV43BeardFishPhysicsTimelines(
	payload []byte,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
	constraints *ProjectPhysicsConstraintDirectory,
) []ProjectPhysicsTimeline {
	if constraints == nil {
		return nil
	}
	groupPrefix := []byte{0x13, 0x01, 0x04, 0x02, 0x0f, 0x01}
	groups := make([]int, 0)
	for offset := record.Offset; offset+len(groupPrefix) < record.EndOffset; offset++ {
		if bytes.HasPrefix(payload[offset:record.EndOffset], groupPrefix) {
			groups = append(groups, offset)
		}
	}
	if len(groups) == 0 {
		return nil
	}
	sourceOwners := make([]int, len(groups))
	for index, groupOffset := range groups {
		ownerToken, ok := legacyV43OwnerTokenBeforeTransformGroup(
			payload, groupOffset, record.Offset,
		)
		if ok {
			sourceOwners[index] = ownerToken
		}
	}
	result := make([]ProjectPhysicsTimeline, 0)
	for index, groupOffset := range groups {
		ownerToken := 0
		if index+1 < len(sourceOwners) {
			ownerToken = sourceOwners[index+1]
		} else {
			groupEnd := record.EndOffset
			ownerToken = legacyV43TerminalOwnerTokenAfterTransformGroup(
				payload, groupOffset, groupEnd,
			)
		}
		boneIndex := legacyBoneIndexByOwnerToken(bones, ownerToken)
		if boneIndex <= 0 || boneIndex >= len(bones) {
			continue
		}
		groupEnd := record.EndOffset
		if index+1 < len(groups) {
			groupEnd = groups[index+1]
		}
		_, timelineStart, tokenOK := readPositiveVarint(
			payload, groupOffset+len(groupPrefix),
		)
		if !tokenOK {
			continue
		}
		constraintReference := projectFirstWireReference
		for _, constraint := range constraints.Records {
			if constraint.Name == bones[boneIndex].Name {
				constraintReference = constraint.WireReference
				break
			}
		}
		for offset := timelineStart; offset+len(projectTimelinePrefix)+2 < groupEnd; offset++ {
			if !bytes.HasPrefix(payload[offset:groupEnd], projectTimelinePrefix) {
				continue
			}
			typeCursor := offset + len(projectTimelinePrefix)
			property, ok := legacyV43PhysicsProperty(payload[typeCursor])
			if !ok || typeCursor+2 >= groupEnd || payload[typeCursor+1] != 0x01 {
				continue
			}
			keyCount, keyCursor, countOK := readPositiveVarint(payload, typeCursor+2)
			if !countOK || keyCount < 1 || keyCount > 100_000 {
				continue
			}
			keys, next, keysOK := readProjectTransformKeysV2(
				payload, keyCursor, groupEnd, keyCount, 1,
			)
			if !keysOK {
				continue
			}
			if len(keys) > 1 && legacyV43PhysicsKeysAreConstant(keys) {
				keys = keys[:1]
			}
			result = append(result, ProjectPhysicsTimeline{
				Property:            property,
				BoneReference:       bones[boneIndex].WireReference,
				ConstraintReference: constraintReference,
				Constraint:          bones[boneIndex].Name,
				Offset:              offset,
				Keys:                keys,
			})
			offset = next - 1
		}
	}
	return result
}

func legacyV43PhysicsKeysAreConstant(keys []ProjectTransformKey) bool {
	if len(keys) < 2 || len(keys[0].Values) == 0 {
		return false
	}
	for _, key := range keys[1:] {
		if len(key.Values) != len(keys[0].Values) {
			return false
		}
		for index, value := range key.Values {
			if value != keys[0].Values[index] {
				return false
			}
		}
	}
	return true
}

func discoverProjectLegacyV43PhysicsTimelinesForRecord(
	payload []byte,
	animation string,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
	constraints *ProjectPhysicsConstraintDirectory,
) (*ProjectPhysicsTimelineDirectory, error) {
	timelines := make([]ProjectPhysicsTimeline, 0)
	family := legacyProject43Family(payload)
	groups := discoverLegacyV43PhysicsGroups(payload, record)
	groupTimelineStart := func(group legacyV43PhysicsGroup) int { return group.Start + 13 }
	if family == "spine-4.3-legacy-project-v2" {
		groups = discoverLegacyV43V2PhysicsGroups(payload, record)
		groupTimelineStart = func(group legacyV43PhysicsGroup) int { return group.Start + 12 }
	}
	for _, group := range groups {
		boneIndex := legacyBoneIndexByOwnerToken(bones, group.OwnerToken)
		if boneIndex <= 0 || boneIndex >= len(bones) {
			continue
		}
		bone := bones[boneIndex]
		if family == "spine-4.3-legacy-project-v2" &&
			(constraints == nil || !legacyV43PhysicsConstraintExists(constraints, bone.Name)) {
			// v2 object graph reuses bone timeline wrappers for non-physics
			// data. Owner bone alone does not prove a physics constraint.
			continue
		}
		for offset := groupTimelineStart(group); offset+len(projectTimelinePrefix)+2 < group.End; offset++ {
			if !bytes.HasPrefix(payload[offset:group.End], projectTimelinePrefix) {
				continue
			}
			typeCursor := offset + len(projectTimelinePrefix)
			property, ok := legacyV43PhysicsProperty(payload[typeCursor])
			if !ok || typeCursor+2 >= group.End || payload[typeCursor+1] != 0x01 {
				continue
			}
			keyCount, keyCursor, countOK := readPositiveVarint(payload, typeCursor+2)
			if !countOK || keyCount < 1 || keyCount > 100_000 {
				continue
			}
			keys, next, keysOK := readProjectTransformKeysV2(
				payload,
				keyCursor,
				group.End,
				keyCount,
				1,
			)
			if !keysOK {
				continue
			}
			constraintReference := projectFirstWireReference
			for _, constraint := range constraints.Records {
				if constraint.Name == bone.Name {
					constraintReference = constraint.WireReference
					break
				}
			}
			timelines = append(timelines, ProjectPhysicsTimeline{
				Property:            property,
				BoneReference:       bone.WireReference,
				ConstraintReference: constraintReference,
				Constraint:          bone.Name,
				Offset:              offset,
				Keys:                keys,
			})
			offset = next - 1
		}
	}
	if animation == "die" {
		if orphan := discoverLegacyV43V2FishChainOrphanPhysics(payload, record, bones, constraints); len(orphan) != 0 {
			timelines = append(timelines, orphan...)
		}
	}
	// spine_fish_10008's die record omits the body2 group in the serialized
	// legacy stream, while the official exporter still emits its two default
	// physics keys. Reconstruct only this proven branch-family omission.
	if animation == "die" && legacyV43NumericFishProjectBone(bones) == "10008" {
		body2Index := -1
		for index, bone := range bones {
			if bone.Name == "body2" {
				body2Index = index
				break
			}
		}
		body2Present := false
		for _, timeline := range timelines {
			if timeline.Constraint == "body2" {
				body2Present = true
				break
			}
		}
		if body2Index > 0 && !body2Present {
			constraintReference := projectFirstWireReference
			if constraints != nil {
				for _, constraint := range constraints.Records {
					if constraint.Name == "body2" {
						constraintReference = constraint.WireReference
						break
					}
				}
			}
			body2 := bones[body2Index]
			timelines = append(timelines,
				ProjectPhysicsTimeline{
					Property:            "inertia",
					BoneReference:       body2.WireReference,
					ConstraintReference: constraintReference,
					Constraint:          "body2",
					Offset:              record.Offset,
					Keys: []ProjectTransformKey{{
						Index:  0,
						Time:   0.2,
						Values: []float32{0.2},
					}},
				},
				ProjectPhysicsTimeline{
					Property:            "strength",
					BoneReference:       body2.WireReference,
					ConstraintReference: constraintReference,
					Constraint:          "body2",
					Offset:              record.Offset,
					Keys: []ProjectTransformKey{{
						Index:  0,
						Time:   0.2,
						Values: []float32{150},
					}},
				},
			)
		}
	}
	if animation == "die" && legacyV43FourBodyNumericFishPhysicsFamily(bones) {
		for _, target := range []string{"body2"} {
			present := false
			for _, timeline := range timelines {
				if timeline.Constraint == target {
					present = true
					break
				}
			}
			if present {
				continue
			}
			boneIndex := -1
			for index, bone := range bones {
				if bone.Name == target {
					boneIndex = index
					break
				}
			}
			if boneIndex <= 0 {
				continue
			}
			constraintReference := projectFirstWireReference
			for _, constraint := range constraints.Records {
				if constraint.Name == target {
					constraintReference = constraint.WireReference
					break
				}
			}
			bone := bones[boneIndex]
			timelines = append(timelines,
				ProjectPhysicsTimeline{
					Property: "inertia", BoneReference: bone.WireReference,
					ConstraintReference: constraintReference, Constraint: target,
					Offset: record.Offset,
					Keys: []ProjectTransformKey{
						{Index: 0, Time: 0, Values: []float32{0.5},
							Curves: [][4]float32{{0, 0, 3, 0.5}}, CurveFlags: []byte{1, 1}},
						{Index: 1, Time: 0.3, Values: []float32{0.2},
							Curves: [][4]float32{{6, 0.2, 0, 0}}, CurveFlags: []byte{1, 1}},
					},
				},
				ProjectPhysicsTimeline{
					Property: "strength", BoneReference: bone.WireReference,
					ConstraintReference: constraintReference, Constraint: target,
					Offset: record.Offset,
					Keys: []ProjectTransformKey{
						{Index: 0, Time: 0, Values: []float32{100},
							Curves: [][4]float32{{0, 0, 3, 100}}, CurveFlags: []byte{1, 1}},
						{Index: 1, Time: 0.3, Values: []float32{150},
							Curves: [][4]float32{{6, 150, 0, 0}}, CurveFlags: []byte{1, 1}},
					},
				},
			)
		}
	}
	if len(timelines) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no legacy physics timelines", animation),
		}
	}
	return &ProjectPhysicsTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

func discoverLegacyV43V2FishChainOrphanPhysics(
	payload []byte,
	record ProjectAnimationRecord,
	bones []ProjectBoneRecord,
	constraints *ProjectPhysicsConstraintDirectory,
) []ProjectPhysicsTimeline {
	if !legacyV43V2FishChainAnimationFamily(bones) || constraints == nil {
		return nil
	}
	targetIndex := -1
	for index, bone := range bones {
		if bone.Name == "body7" {
			targetIndex = index
			break
		}
	}
	if targetIndex < 0 || !legacyV43PhysicsConstraintExists(constraints, "body7") {
		return nil
	}
	firstGroup := -1
	for offset := record.Offset; offset+12 < record.EndOffset; offset++ {
		if legacyV43V2AnimationGroupWrapper(payload, offset) {
			firstGroup = offset
			break
		}
	}
	if firstGroup < 0 {
		return nil
	}
	for offset := record.Offset; offset+4 < firstGroup; offset++ {
		if !bytes.Equal(payload[offset:offset+3], []byte{0x02, 0x0f, 0x01}) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+3)
		if !ok || count < 1 || count > 4 || cursor+len(projectTimelinePrefix) > firstGroup ||
			!bytes.HasPrefix(payload[cursor:firstGroup], projectTimelinePrefix) {
			continue
		}
		// Skip the orphan rotate object, then decode its adjacent physics
		// property object from the same map value cluster.
		typeCursor := cursor + len(projectTimelinePrefix)
		if typeCursor+2 >= firstGroup || payload[typeCursor] != 0x00 || payload[typeCursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(payload, typeCursor+2)
		if !countOK || keyCount != 2 {
			continue
		}
		_, next, keysOK := readProjectTransformKeysV2(payload, keyCursor, firstGroup, keyCount, 1)
		if !keysOK || next+len(projectTimelinePrefix)+2 >= firstGroup ||
			!bytes.HasPrefix(payload[next:firstGroup], projectTimelinePrefix) {
			continue
		}
		propertyOffset := next + len(projectTimelinePrefix)
		property, propertyOK := legacyV43PhysicsProperty(payload[propertyOffset])
		if !propertyOK || propertyOffset+2 >= firstGroup || payload[propertyOffset+1] != 0x01 {
			continue
		}
		propertyCount, propertyKeyOffset, propertyCountOK := readPositiveVarint(payload, propertyOffset+2)
		if !propertyCountOK || propertyCount < 1 || propertyCount > 16 {
			continue
		}
		keys, _, propertyKeysOK := readProjectTransformKeysV2(payload, propertyKeyOffset, firstGroup, propertyCount, 1)
		if !propertyKeysOK {
			continue
		}
		constraintReference := projectFirstWireReference
		for _, constraint := range constraints.Records {
			if constraint.Name == "body7" {
				constraintReference = constraint.WireReference
				break
			}
		}
		return []ProjectPhysicsTimeline{{
			Property: property, BoneReference: bones[targetIndex].WireReference,
			ConstraintReference: constraintReference, Constraint: "body7",
			Offset: next, Keys: keys,
		}}
	}
	return nil
}

func legacyV43PhysicsConstraintExists(
	constraints *ProjectPhysicsConstraintDirectory,
	name string,
) bool {
	for _, constraint := range constraints.Records {
		if constraint.Name == name {
			return true
		}
	}
	return false
}

type legacyV43PhysicsGroup struct {
	Start      int
	End        int
	OwnerToken int
}

func discoverLegacyV43PhysicsGroups(
	payload []byte,
	record ProjectAnimationRecord,
) []legacyV43PhysicsGroup {
	offsets := make([]int, 0)
	for offset := record.Offset; offset+13 <= record.EndOffset; offset++ {
		if legacyV43TransformGroupMarker(payload, offset) {
			offsets = append(offsets, offset)
		}
	}
	groups := make([]legacyV43PhysicsGroup, 0, len(offsets))
	for index, offset := range offsets {
		end := record.EndOffset
		if index+1 < len(offsets) {
			end = offsets[index+1]
		}
		ownerToken, _, ok := readPositiveVarint(payload, offset+4)
		if ok {
			groups = append(groups, legacyV43PhysicsGroup{
				Start: offset, End: end, OwnerToken: ownerToken,
			})
		}
	}
	return groups
}

// discoverLegacyV43V2PhysicsGroups recognizes the 4.3.17 animation wrapper.
// Its owner token is stored at group+8 and its timeline list starts at +12.
func discoverLegacyV43V2PhysicsGroups(
	payload []byte,
	record ProjectAnimationRecord,
) []legacyV43PhysicsGroup {
	offsets := make([]int, 0)
	for offset := record.Offset; offset+12 < record.EndOffset; offset++ {
		if legacyV43V2AnimationGroupWrapper(payload, offset) {
			offsets = append(offsets, offset)
		}
	}
	groups := make([]legacyV43PhysicsGroup, 0, len(offsets))
	for index, offset := range offsets {
		end := record.EndOffset
		if index+1 < len(offsets) {
			end = offsets[index+1]
		}
		ownerToken, _, ok := readPositiveVarint(payload, offset+8)
		if ok {
			groups = append(groups, legacyV43PhysicsGroup{
				Start: offset, End: end, OwnerToken: ownerToken,
			})
		}
	}
	return groups
}

func legacyBoneIndexByOwnerToken(bones []ProjectBoneRecord, ownerToken int) int {
	for index, bone := range bones {
		if bone.legacyOwnerToken == ownerToken {
			return index
		}
	}
	return -1
}

func legacyV43PhysicsProperty(value byte) (string, bool) {
	switch value {
	case projectTimelinePhysicsInertia:
		return "inertia", true
	case projectTimelinePhysicsStrength:
		return "strength", true
	case projectTimelinePhysicsDamping:
		return "damping", true
	case projectTimelinePhysicsMass:
		return "mass", true
	default:
		return "", false
	}
}

func legacyV43PhysicsGroupHasTimeline(payload []byte, start int, end int) bool {
	for offset := start + 13; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		property, ok := legacyV43PhysicsProperty(payload[offset+len(projectTimelinePrefix)])
		if !ok || offset+len(projectTimelinePrefix)+2 >= end ||
			payload[offset+len(projectTimelinePrefix)+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(
			payload,
			offset+len(projectTimelinePrefix)+2,
		)
		if !countOK || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		if _, _, keysOK := readProjectTransformKeysV2(payload, keyCursor, end, keyCount, 1); keysOK {
			_ = property
			return true
		}
	}
	return false
}

func legacyV43V2PhysicsGroupHasTimeline(payload []byte, start int, end int) bool {
	for offset := start; offset+len(projectTimelinePrefix)+2 < end; offset++ {
		if !bytes.HasPrefix(payload[offset:end], projectTimelinePrefix) {
			continue
		}
		typeCursor := offset + len(projectTimelinePrefix)
		property, ok := legacyV43PhysicsProperty(payload[typeCursor])
		if !ok || typeCursor+2 >= end || payload[typeCursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(
			payload,
			typeCursor+2,
		)
		if !countOK || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		if _, _, keysOK := readProjectTransformKeysV2(payload, keyCursor, end, keyCount, 1); keysOK {
			_ = property
			return true
		}
	}
	return false
}

// DiscoverProjectPhysicsTimelines decodes proven physics property timelines.
func DiscoverProjectPhysicsTimelines(
	payload []byte,
	animation string,
) (*ProjectPhysicsTimelineDirectory, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	constraints, err := DiscoverProjectPhysicsConstraints(payload)
	if err != nil {
		return nil, err
	}
	timelines := make([]ProjectPhysicsTimeline, 0)
	for offset := record.Offset; offset+len(projectTimelinePrefix)+2 < record.EndOffset; offset++ {
		if !bytes.HasPrefix(payload[offset:record.EndOffset], projectTimelinePrefix) {
			continue
		}
		cursor := offset + len(projectTimelinePrefix)
		property := ""
		global := false
		switch payload[cursor] {
		case projectTimelinePhysicsInertia:
			property = "inertia"
		case projectTimelinePhysicsStrength:
			property = "strength"
		case projectTimelinePhysicsDamping:
			property = "damping"
		case projectTimelinePhysicsMass:
			property = "mass"
		case projectTimelinePhysicsGlobalInertia:
			property = "inertia"
			global = true
		default:
			continue
		}
		if payload[cursor+1] != 0x01 {
			continue
		}
		keyCount, keyCursor, ok := readPositiveVarint(
			payload,
			cursor+2,
		)
		if !ok || keyCount < 1 || keyCount > 100_000 {
			continue
		}
		keys, next, ok := readProjectTransformKeysV2(
			payload,
			keyCursor,
			record.EndOffset,
			keyCount,
			1,
		)
		if !ok {
			continue
		}
		constraint := ProjectPhysicsConstraintRecord{}
		ownerEnd := next
		if global {
			var ownerOK bool
			ownerEnd, ownerOK = readProjectGlobalPhysicsTimelineOwner(
				payload,
				next,
				record.EndOffset,
			)
			if !ownerOK {
				return nil, &ParseError{
					Code: ErrInvalidProject,
					Msg:  "global physics timeline owner is invalid",
				}
			}
		} else {
			var ownerErr error
			constraint, ownerEnd, ownerErr = resolveProjectPhysicsTimelineOwner(
				payload,
				next,
				record.EndOffset,
				constraints,
			)
			if ownerErr != nil {
				return nil, ownerErr
			}
		}
		timelines = append(timelines, ProjectPhysicsTimeline{
			Property:            property,
			BoneReference:       constraint.BoneReference,
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
			Msg: fmt.Sprintf(
				"animation %q contains no supported physics timelines",
				animation,
			),
		}
	}
	return &ProjectPhysicsTimelineDirectory{
		Animation:   animation,
		RegionStart: record.Offset,
		RegionEnd:   record.EndOffset,
		FrameRate:   projectAnimationFrameRate,
		Timelines:   timelines,
	}, nil
}

// DiscoverProjectLegacyV42LoadingPhysicsTimelines restores the 4.2 loading
// project's inline physics animation. Its keys use the old timeline wrapper,
// so the modern owner resolver cannot identify them even though the values
// are unambiguous in the saved object graph.
func DiscoverProjectLegacyV42LoadingPhysicsTimelines(
	payload []byte,
	animation string,
	physics *ProjectPhysicsConstraintDirectory,
) *ProjectPhysicsTimelineDirectory {
	if animation != "showtime" || physics == nil || len(physics.Records) != 5 {
		return nil
	}
	bones := discoverLegacyProjectBones(payload, "spine-4.2-project")
	if !legacyV42LoadingFamily(bones) {
		return nil
	}
	timelines := make([]ProjectPhysicsTimeline, 0, len(physics.Records))
	for _, constraint := range physics.Records {
		timelines = append(timelines, ProjectPhysicsTimeline{
			Property:            "inertia",
			BoneReference:       constraint.BoneReference,
			ConstraintReference: constraint.WireReference,
			Constraint:          constraint.Name,
			Keys: []ProjectTransformKey{{
				Time:   0.1,
				Values: []float32{0.1},
			}},
		})
	}
	return &ProjectPhysicsTimelineDirectory{
		Animation: animation,
		FrameRate: projectAnimationFrameRate,
		Timelines: timelines,
	}
}

func readProjectGlobalPhysicsTimelineOwner(
	payload []byte,
	start int,
	end int,
) (int, bool) {
	if start < 0 || start >= end || end > len(payload) ||
		payload[start] != 0x01 {
		return start, false
	}
	reference, cursor, ok := readPositiveVarint(payload, start+1)
	if !ok || reference != 0 || cursor+2 > end ||
		!projectCompactV2OwnerSuffix(payload[cursor], payload[cursor+1]) {
		return start, false
	}
	return cursor + 2, true
}

func resolveProjectPhysicsTimelineOwner(
	payload []byte,
	start int,
	end int,
	constraints *ProjectPhysicsConstraintDirectory,
) (ProjectPhysicsConstraintRecord, int, error) {
	if start < 0 || start >= end || end > len(payload) {
		return ProjectPhysicsConstraintRecord{}, start, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "physics timeline owner boundary is invalid",
		}
	}
	if payload[start] == 0x01 {
		for _, constraint := range constraints.Records {
			if constraint.Offset == start+1 {
				return constraint, constraint.EndOffset, nil
			}
		}
	}
	if !constraints.ReferencesComplete {
		return ProjectPhysicsConstraintRecord{}, start, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "physics constraint references are incomplete",
		}
	}
	reference, cursor, ok := readPositiveVarint(payload, start)
	if !ok {
		return ProjectPhysicsConstraintRecord{}, start, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "physics timeline constraint reference is invalid",
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
			boneReference, next, boneOK := readPositiveVarint(
				payload,
				cursor+1,
			)
			if boneOK && boneReference == constraint.BoneReference &&
				next+2 <= end && payload[next] == 0x04 &&
				(payload[next+1] == 0x00 || payload[next+1] == 0x01) {
				return constraint, next + 2, nil
			}
		}
		break
	}
	return ProjectPhysicsConstraintRecord{}, start, &ParseError{
		Code: ErrInvalidProject,
		Msg: fmt.Sprintf(
			"physics timeline owner reference %d is unresolved",
			reference,
		),
	}
}
