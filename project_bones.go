package spineparser

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
)

var projectBoneTablePrefix = []byte{0x0f, 0x01}

var projectBoneRecordV2Tail = []byte{0x1f, 0x0f, 0x01, 0x00, 0x09}

// ProjectBoneRecord identifies one bone object in project serialization order.
// ParentToken is the raw Kryo parent object token: null, new object, or ref.
type ProjectBoneRecord struct {
	Name              string  `json:"name"`
	Offset            int     `json:"offset"`
	ParentToken       int     `json:"parentToken"`
	WireReference     int     `json:"wireReference,omitempty"`
	NameEncoding      string  `json:"nameEncoding"`
	Color             [4]byte `json:"color"`
	Inherit           string  `json:"inherit,omitempty"`
	Icon              string  `json:"icon,omitempty"`
	iconObject        int
	iconToken         int
	iconInline        bool
	Length            float32 `json:"length"`
	X                 float32 `json:"x"`
	Y                 float32 `json:"y"`
	Rotation          float32 `json:"rotation"`
	ScaleX            float32 `json:"scaleX"`
	ScaleY            float32 `json:"scaleY"`
	ShearX            float32 `json:"shearX"`
	ShearY            float32 `json:"shearY"`
	Visible           bool    `json:"visible"`
	legacySetupIndex  int
	legacySetupDirect bool
	legacyOwnerToken  int
	// legacyRawParentToken 保存 setup 块或槽记录中按字节读取的父骨骼
	// owner token；最终 ParentToken 以它优先，解析失败时保持 0 走名称推断。
	legacyRawParentToken int
}

// ProjectBoneDirectory contains directly decoded modern project bone names
// and proven Kryo wire references. ReferencesComplete is false when only the
// leading directly serialized references could be established safely.
type ProjectBoneDirectory struct {
	Format             string              `json:"format"`
	HeaderOffset       int                 `json:"headerOffset"`
	ClassID            int                 `json:"classId"`
	Count              int                 `json:"count"`
	ReferencesComplete bool                `json:"referencesComplete"`
	Records            []ProjectBoneRecord `json:"records"`
}

// DiscoverProjectBones decodes bone names, object offsets, and parent
// references from a modern Spine Pro project without launching Spine Editor.
func DiscoverProjectBones(payload []byte) (*ProjectBoneDirectory, error) {
	if len(payload) == 0 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	candidates := make([]ProjectBoneDirectory, 0, 1)
	for headerOffset := 0; headerOffset+2 < len(payload); headerOffset++ {
		if !bytes.HasPrefix(payload[headerOffset:], projectBoneTablePrefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, headerOffset+2)
		if !ok || count < 1 || count > 100_000 ||
			cursor+3 >= len(payload) ||
			payload[cursor] != 0x0c ||
			payload[cursor+1] != 0x01 {
			continue
		}
		classID := int(payload[cursor+2])
		firstRecord := cursor + 1
		prefix := []byte{0x01, byte(classID), 0x00, 0x03, 0x01}
		if !bytes.HasPrefix(payload[firstRecord:], prefix) {
			continue
		}
		records := scanProjectBoneRecords(
			payload,
			firstRecord,
			classID,
			count,
		)
		if len(records) != count || records[0].ParentToken != 0 ||
			!uniqueProjectBoneNames(records) {
			continue
		}
		directory := ProjectBoneDirectory{
			Format:       "kryo-bone-table-v1",
			HeaderOffset: headerOffset,
			ClassID:      classID,
			Count:        count,
			Records:      records,
		}
		references, complete := resolveProjectBoneReferences(
			payload,
			cursor,
			records,
		)
		for index := range directory.Records {
			directory.Records[index].WireReference = references[index]
		}
		directory.ReferencesComplete = complete
		candidates = append(candidates, directory)
	}
	if len(candidates) == 0 {
		return discoverProjectBonesV2(payload)
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d bone table candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

// discoverProjectBonesV2 decodes the 4.3.23 object-table layout observed in
// current Spine projects. Unlike v1, the record carries an extra object token
// before its field sequence, so parent references cannot yet be proven here.
func discoverProjectBonesV2(payload []byte) (*ProjectBoneDirectory, error) {
	candidates := make([]ProjectBoneDirectory, 0, 1)
	for headerOffset := 0; headerOffset+2 < len(payload); headerOffset++ {
		if !bytes.HasPrefix(payload[headerOffset:], projectBoneTablePrefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, headerOffset+2)
		if !ok || count < 1 || count > 100_000 ||
			cursor+3 >= len(payload) ||
			payload[cursor] != 0x0c || payload[cursor+1] != 0x01 {
			continue
		}
		classID := int(payload[cursor+2])
		firstRecord := cursor + 1
		prefix := []byte{0x01, byte(classID), 0x02, 0x01, 0x03}
		if !bytes.HasPrefix(payload[firstRecord:], prefix) {
			continue
		}
		records := scanProjectBoneRecordsV2(payload, firstRecord, prefix, count)
		if len(records) != count {
			continue
		}
		directory := ProjectBoneDirectory{
			Format:       "kryo-bone-table-v2",
			HeaderOffset: headerOffset,
			ClassID:      classID,
			Count:        count,
			Records:      records,
		}
		directory.ReferencesComplete = resolveProjectBoneReferencesV2(directory.Records)
		candidates = append(candidates, directory)
	}
	if len(candidates) == 0 {
		return discoverProjectBonesV2Fallback(payload)
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d bone table candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

// discoverProjectBonesV2Fallback supports projects whose 4.3 bone object
// records are unchanged but whose enclosing collection layout differs from
// the table header emitted by newer editor saves. It deliberately requires a
// root-first, unique record sequence and each record's verified V2 tail so a
// coincidental byte pattern cannot be treated as a skeleton.
func discoverProjectBonesV2Fallback(payload []byte) (*ProjectBoneDirectory, error) {
	candidates := make([]ProjectBoneDirectory, 0, 1)
	for classID := 1; classID <= 255; classID++ {
		prefix := []byte{0x01, byte(classID), 0x02, 0x01, 0x03}
		firstOffset := bytes.Index(payload, prefix)
		if firstOffset < 0 {
			continue
		}
		maxRecordCount := countProjectBoneRecordV2Prefixes(
			payload,
			firstOffset,
			byte(classID),
		)
		var records []ProjectBoneRecord
		for recordCount := maxRecordCount; recordCount > 0; recordCount-- {
			records = scanProjectBoneRecordsV2(
				payload,
				firstOffset,
				prefix,
				recordCount,
			)
			if len(records) == recordCount {
				break
			}
		}
		if len(records) == 0 || records[0].Name != "root" ||
			records[0].ParentToken != 0 || !uniqueProjectBoneNames(records) {
			continue
		}
		directory := ProjectBoneDirectory{
			Format:       "kryo-bone-table-v2-fallback",
			HeaderOffset: firstOffset,
			ClassID:      classID,
			Count:        len(records),
			Records:      records,
		}
		directory.ReferencesComplete = resolveProjectBoneReferencesV2(directory.Records)
		candidates = append(candidates, directory)
	}
	if len(candidates) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported project bone table was not found",
		}
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d V2 fallback bone candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

func scanProjectBoneRecordsV2(
	payload []byte,
	firstOffset int,
	prefix []byte,
	count int,
) []ProjectBoneRecord {
	offsets := make([]int, 0, count)
	for offset := firstOffset; offset+len(prefix) <= len(payload); offset++ {
		if isProjectBoneRecordV2Prefix(payload, offset, prefix[1]) {
			offsets = append(offsets, offset)
			if len(offsets) == count {
				break
			}
		}
	}
	if len(offsets) != count {
		return nil
	}
	records := make([]ProjectBoneRecord, 0, count)
	for index := range offsets {
		parentToken, _, ok := readPositiveVarint(
			payload,
			offsets[index]+len(prefix),
		)
		if !ok {
			return nil
		}
		end := len(payload)
		if index+1 < len(offsets) {
			end = offsets[index+1]
		}
		tailOffset := bytes.Index(payload[offsets[index]+len(prefix):end], projectBoneRecordV2Tail)
		if tailOffset < 0 {
			return nil
		}
		nameEnd := offsets[index] + len(prefix) + tailOffset
		name, ok := lastProjectString(payload, offsets[index]+len(prefix), nameEnd)
		nameEncoding := "v2-inline-last-string"
		if !ok {
			name, ok = fallbackProjectBoneName(payload, offsets[index], index, records)
			if !ok {
				return nil
			}
			nameEncoding = "v2-referenced-bone"
		}
		inherit, icon, iconObject, iconToken, iconInline, ok := readProjectBoneFields(
			payload,
			offsets[index]+len(prefix),
			nameEnd,
		)
		if !ok {
			return nil
		}
		setup, ok := parseProjectBoneSetupV2(
			payload,
			nameEnd+len(projectBoneRecordV2Tail),
			end,
		)
		if !ok {
			return nil
		}
		records = append(records, ProjectBoneRecord{
			Name:         name,
			Offset:       offsets[index],
			ParentToken:  parentToken,
			NameEncoding: nameEncoding,
			Color:        setup.Color,
			Inherit:      inherit,
			Icon:         icon,
			iconObject:   iconObject,
			iconToken:    iconToken,
			iconInline:   iconInline,
			Length:       setup.Length,
			X:            setup.X,
			Y:            setup.Y,
			Rotation:     setup.Rotation,
			ScaleX:       setup.ScaleX,
			ScaleY:       setup.ScaleY,
			ShearX:       setup.ShearX,
			ShearY:       setup.ShearY,
			Visible:      payload[offsets[index]+3] != 0,
		})
	}
	bindProjectBoneIcons(records)
	return records
}

func fallbackProjectBoneName(
	payload []byte,
	offset int,
	index int,
	records []ProjectBoneRecord,
) (string, bool) {
	// 极简 4.3 工程会把第二根骨骼的名称复用为首根骨骼的 icon 字符串，
	// 记录本身只有对象引用，没有内联字符串。仅在 root-first、第二条记录
	// 且 payload 确实含有独立 bone 字符串时启用，避免扩大猜测范围。
	if index != 1 || len(records) != 1 || records[0].Name != "root" ||
		offset < 0 || offset >= len(payload) {
		return "", false
	}
	for cursor := 0; cursor < len(payload); cursor++ {
		name, _, ok := decodeProjectASCII(payload, cursor)
		if ok && name == "bone" {
			return name, true
		}
	}
	return "", false
}

func readProjectBoneFields(
	payload []byte,
	start int,
	end int,
) (string, string, int, int, bool, bool) {
	cursor := -1
	objectToken := 0
	inherit := "normal"
	for offset := start; offset < end; offset++ {
		if payload[offset] != 0x1b {
			continue
		}
		currentObjectToken, afterObjectToken, ok := readPositiveVarint(
			payload,
			offset+1,
		)
		if !ok || afterObjectToken >= end {
			continue
		}
		inheritToken := 1
		afterInheritToken := afterObjectToken
		if payload[afterObjectToken] != 0x1c {
			inheritToken, afterInheritToken, ok = readPositiveVarint(
				payload,
				afterObjectToken,
			)
			if !ok || afterInheritToken >= end ||
				payload[afterInheritToken] != 0x1c {
				continue
			}
		}
		inherit, ok = projectBoneInheritName(inheritToken)
		if !ok {
			continue
		}
		cursor = afterInheritToken + 1
		objectToken = currentObjectToken
		break
	}
	if cursor < 0 {
		return "normal", "", 0, 0, false, true
	}
	token, afterToken, ok := readPositiveVarint(payload, cursor)
	if !ok || afterToken > end {
		return "", "", 0, 0, false, false
	}
	if token != 1 {
		return inherit, "", objectToken, token, false, true
	}
	icon, afterIcon, ok := decodeProjectASCII(payload, afterToken)
	if !ok || afterIcon > end {
		return "", "", 0, 0, false, false
	}
	return inherit, icon, objectToken, 0, true, true
}

func projectBoneInheritName(token int) (string, bool) {
	switch token {
	case 1:
		return "normal", true
	case 2:
		return "onlyTranslation", true
	case 3:
		return "noRotationOrReflection", true
	case 4:
		return "noScale", true
	case 5:
		return "noScaleOrReflection", true
	default:
		return "", false
	}
}

func bindProjectBoneIcons(records []ProjectBoneRecord) {
	iconsByToken := make(map[int]string)
	registeredObjectTokens := make(map[int]struct{})
	for index := range records {
		record := &records[index]
		if !record.iconInline {
			continue
		}
		if _, registered := registeredObjectTokens[record.iconObject]; registered {
			continue
		}
		registeredObjectTokens[record.iconObject] = struct{}{}
		iconsByToken[record.iconObject+5] = record.Icon
	}
	for index := range records {
		record := &records[index]
		if record.iconInline {
			continue
		}
		if record.iconToken == 0 || record.iconToken == record.iconObject+1 {
			continue
		}
		if icon, ok := iconsByToken[record.iconToken]; ok {
			record.Icon = icon
		}
	}
	for index := range records {
		if records[index].Icon == "bone" {
			records[index].Icon = ""
		}
	}
}

func isProjectBoneRecordV2Prefix(payload []byte, offset int, classID byte) bool {
	return offset+5 <= len(payload) &&
		payload[offset] == 0x01 &&
		payload[offset+1] == classID &&
		payload[offset+2] == 0x02 &&
		(payload[offset+3] == 0x00 || payload[offset+3] == 0x01) &&
		payload[offset+4] == 0x03
}

func countProjectBoneRecordV2Prefixes(
	payload []byte,
	firstOffset int,
	classID byte,
) int {
	count := 0
	for offset := firstOffset; offset+5 <= len(payload); offset++ {
		if isProjectBoneRecordV2Prefix(payload, offset, classID) {
			count++
			offset += 4
		}
	}
	return count
}

type projectBoneSetupV2 struct {
	Color    [4]byte
	Length   float32
	X        float32
	Y        float32
	Rotation float32
	ScaleX   float32
	ScaleY   float32
	ShearX   float32
	ShearY   float32
}

func parseProjectBoneSetupV2(payload []byte, start int, end int) (projectBoneSetupV2, bool) {
	setup := projectBoneSetupV2{}
	if start+4 > end {
		return setup, false
	}
	setup.Length = projectFloat32(payload[start : start+4])
	cursor := start + 4
	var ok bool
	setup.ShearX, cursor, ok = readProjectTaggedFloat(payload, cursor, end, 0x17, 0)
	if !ok {
		return setup, false
	}
	_, cursor, ok = readProjectTaggedFloat(payload, cursor, end, 0x22, 0)
	if !ok {
		return setup, false
	}
	_, cursor, ok = readProjectTaggedFloat(payload, cursor, end, 0x21, 0)
	if !ok {
		return setup, false
	}
	colorMarker := []byte{0x11, 0x1e, 0x01}
	colorOffset := bytes.Index(payload[cursor:end], colorMarker)
	if colorOffset < 0 {
		return setup, false
	}
	colorOffset += cursor
	if colorOffset+len(colorMarker)+len(setup.Color) > end {
		return setup, false
	}
	copy(
		setup.Color[:],
		payload[colorOffset+len(colorMarker):colorOffset+len(colorMarker)+len(setup.Color)],
	)
	cursor = colorOffset + len(colorMarker) + len(setup.Color)
	cursor = findProjectTag(payload, cursor, end, 0x19, 16)
	if cursor < 0 {
		return setup, false
	}
	setup.ShearY, cursor, ok = readProjectTaggedFloat(
		payload,
		cursor,
		end,
		0x19,
		0,
	)
	if !ok {
		return setup, false
	}
	setup.ScaleX, cursor, ok = readProjectTaggedFloat(payload, cursor, end, 0x0d, 0)
	if !ok {
		return setup, false
	}
	setup.X, cursor, ok = readProjectTaggedFloat(payload, cursor, end, 0x0a, 0)
	if !ok {
		return setup, false
	}
	rotationTag := findProjectTag(payload, cursor, end, 0x0c, 8)
	if rotationTag < 0 {
		return setup, false
	}
	setup.Rotation, cursor, ok = readProjectTaggedFloat(payload, rotationTag, end, 0x0c, 0)
	if !ok {
		return setup, false
	}
	setup.Y, cursor, ok = readProjectTaggedFloat(payload, cursor, end, 0x0b, 0)
	if !ok {
		return setup, false
	}
	setup.ScaleY, _, ok = readProjectTaggedFloat(payload, cursor, end, 0x0e, 0)
	return setup, ok
}

func readProjectTaggedFloat(
	payload []byte,
	cursor int,
	end int,
	tag byte,
	searchBytes int,
) (float32, int, bool) {
	if searchBytes > 0 {
		cursor = findProjectTag(payload, cursor, end, tag, searchBytes)
	}
	if cursor < 0 || cursor+5 > end || payload[cursor] != tag {
		return 0, cursor, false
	}
	return projectFloat32(payload[cursor+1 : cursor+5]), cursor + 5, true
}

func findProjectTag(payload []byte, cursor int, end int, tag byte, maxDistance int) int {
	limit := cursor + maxDistance + 1
	if limit > end {
		limit = end
	}
	for offset := cursor; offset < limit; offset++ {
		if payload[offset] == tag {
			return offset
		}
	}
	return -1
}

func projectFloat32(payload []byte) float32 {
	return math.Float32frombits(binary.BigEndian.Uint32(payload))
}

func resolveProjectBoneReferencesV2(records []ProjectBoneRecord) bool {
	if len(records) < 2 || records[0].ParentToken != 0 || records[1].ParentToken < 1 {
		return false
	}
	firstReference := records[1].ParentToken
	for index := range records {
		records[index].WireReference = firstReference + index
		if index == 0 {
			continue
		}
		parentIndex := records[index].ParentToken - firstReference
		if parentIndex < 0 || parentIndex >= index {
			for clearIndex := range records {
				records[clearIndex].WireReference = 0
			}
			return false
		}
	}
	return true
}

func lastProjectString(payload []byte, start int, end int) (string, bool) {
	name := ""
	for offset := start; offset < end; offset++ {
		if offset == 0 || payload[offset-1] != 0x01 {
			continue
		}
		value, _, ok := decodeProjectASCII(payload, offset)
		if ok {
			name = value
			continue
		}
		value, ok = decodeProjectShortASCII(payload, offset)
		if ok {
			name = value
		}
	}
	if name == "" {
		return "", false
	}
	return name, true
}

// decodeProjectShortASCII reads Kryo's short string form. This form is used
// by single-character attachment and bone names, for example 0x82 followed by
// the ASCII byte '1'. Only printable ASCII is accepted here; UTF-8 names stay
// fail-closed until their complete length encoding is independently covered.
func decodeProjectShortASCII(payload []byte, offset int) (string, bool) {
	value, _, ok := decodeProjectShortASCIIWithEnd(payload, offset)
	return value, ok
}

func decodeProjectShortASCIIWithEnd(payload []byte, offset int) (string, int, bool) {
	if offset >= len(payload) || payload[offset]&0xc0 != 0x80 {
		return "", offset, false
	}
	length := int(payload[offset]&0x3f) - 1
	if length < 1 || offset+1+length > len(payload) {
		return "", offset, false
	}
	for index := 0; index < length; index++ {
		value := payload[offset+1+index]
		if value < 0x20 || value > 0x7e {
			return "", offset, false
		}
	}
	return string(payload[offset+1 : offset+1+length]), offset + 1 + length, true
}

// WireReferenceByName resolves a bone name to the stable Kryo object
// reference used by animation timelines. The boolean is false when the bone
// does not exist or its reference could not be proved from the project.
func (directory *ProjectBoneDirectory) WireReferenceByName(
	name string,
) (int, bool) {
	if directory == nil {
		return 0, false
	}
	for _, record := range directory.Records {
		if record.Name == name && record.WireReference > 0 {
			return record.WireReference, true
		}
	}
	return 0, false
}

// BoneNameByWireReference resolves an animation timeline's Kryo object
// reference back to the project bone name.
func (directory *ProjectBoneDirectory) BoneNameByWireReference(
	reference int,
) (string, bool) {
	if directory == nil || reference < 1 {
		return "", false
	}
	for _, record := range directory.Records {
		if record.WireReference == reference {
			return record.Name, true
		}
	}
	return "", false
}

func scanProjectBoneRecords(
	payload []byte,
	firstOffset int,
	classID int,
	count int,
) []ProjectBoneRecord {
	prefix := []byte{0x01, byte(classID), 0x00, 0x03, 0x01}
	records := make([]ProjectBoneRecord, 0, count)
	for offset := firstOffset; offset < len(payload) && len(records) < count; {
		relative := bytes.Index(payload[offset:], prefix)
		if relative < 0 {
			break
		}
		recordOffset := offset + relative
		tokenOffset := recordOffset + len(prefix)
		name, afterName, encoding, ok := decodeProjectBoneName(
			payload,
			firstOffset,
			recordOffset,
			tokenOffset,
		)
		if !ok || afterName+3 > len(payload) ||
			payload[afterName] != 0x02 ||
			(payload[afterName+1] != 0x00 && payload[afterName+1] != 0x01) ||
			payload[afterName+2] != 0x03 {
			offset = recordOffset + 1
			continue
		}
		parentToken, _, ok := readPositiveVarint(payload, afterName+3)
		if !ok || (len(records) > 0 && parentToken < 1) {
			offset = recordOffset + 1
			continue
		}
		records = append(records, ProjectBoneRecord{
			Name:         name,
			Offset:       recordOffset,
			ParentToken:  parentToken,
			NameEncoding: encoding,
			Visible:      true,
		})
		offset = afterName + 1
	}
	return records
}

func decodeProjectBoneName(
	payload []byte,
	firstOffset int,
	recordOffset int,
	tokenOffset int,
) (string, int, string, bool) {
	if tokenOffset >= len(payload) {
		return "", tokenOffset, "", false
	}
	if payload[tokenOffset] == 0x01 {
		name, afterName, ok := decodeProjectASCII(payload, tokenOffset+1)
		return name, afterName, "inline", ok
	}
	_, afterReference, ok := readPositiveVarint(payload, tokenOffset)
	if !ok || recordOffset < 1 || payload[recordOffset-1] != 0x02 {
		return "", tokenOffset, "", false
	}
	searchStart := recordOffset - 512
	if searchStart < firstOffset {
		searchStart = firstOffset
	}
	for classID := 0; classID < 64; classID++ {
		wrapperPrefix := []byte{
			0x01, byte(classID), 0x00, 0x03, 0x01, 0x01,
		}
		wrapperOffset := bytes.LastIndex(
			payload[searchStart:recordOffset],
			wrapperPrefix,
		)
		if wrapperOffset < 0 {
			continue
		}
		wrapperOffset += searchStart
		name, end, decoded := decodeProjectASCII(
			payload,
			wrapperOffset+len(wrapperPrefix),
		)
		if decoded && end == recordOffset-1 {
			return name, afterReference, "wrapper-reference", true
		}
	}
	return "", tokenOffset, "", false
}

func uniqueProjectBoneNames(records []ProjectBoneRecord) bool {
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.Name == "" {
			return false
		}
		if _, exists := seen[record.Name]; exists {
			return false
		}
		seen[record.Name] = struct{}{}
	}
	return true
}
