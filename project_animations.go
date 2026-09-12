package spineparser

import (
	"bytes"
	"fmt"
	"strings"
)

var (
	modernAnimationHeaderPrefix   = []byte{0x07, 0x0f, 0x01}
	modernAnimationHeaderSuffix   = []byte{0x12, 0x01}
	modernAnimationHeaderTail     = []byte{0x00, 0x03, 0x01, 0x01}
	modernAnimationValuePrefix    = []byte{0x02, 0x0f, 0x01}
	legacyV42AnimationValuePrefix = []byte{0x06, 0x01, 0x02, 0x0f, 0x01}
	projectAnimationV2Value       = []byte{0x0a, 0x1e, 0x01}
)

// ProjectAnimationRecord identifies one top-level animation value in the
// decompressed project stream. EndOffset is the next animation key, or payload
// length for the final record.
type ProjectAnimationRecord struct {
	Name      string `json:"name"`
	Offset    int    `json:"offset"`
	EndOffset int    `json:"endOffset"`
}

// ProjectAnimationDirectory is the directly decoded top-level animation map.
type ProjectAnimationDirectory struct {
	Format       string                   `json:"format"`
	HeaderOffset int                      `json:"headerOffset"`
	Count        int                      `json:"count"`
	Records      []ProjectAnimationRecord `json:"records"`
}

// DiscoverProjectAnimations locates and decodes the modern Spine project
// animation map without launching Spine Editor.
func DiscoverProjectAnimations(payload []byte) (*ProjectAnimationDirectory, error) {
	if len(payload) == 0 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	candidates := make([]ProjectAnimationDirectory, 0, 1)
	for offset := 0; offset+len(modernAnimationHeaderPrefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], modernAnimationHeaderPrefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(modernAnimationHeaderPrefix))
		if !ok || count < 1 || count > 10_000 {
			continue
		}
		if cursor+len(modernAnimationHeaderSuffix)+1+len(modernAnimationHeaderTail) > len(payload) ||
			!bytes.Equal(
				payload[cursor:cursor+len(modernAnimationHeaderSuffix)],
				modernAnimationHeaderSuffix,
			) {
			continue
		}
		cursor += len(modernAnimationHeaderSuffix)
		stringMode := payload[cursor]
		if stringMode != 0x09 && stringMode != 0x0a {
			continue
		}
		cursor++
		if !bytes.HasPrefix(payload[cursor:], modernAnimationHeaderTail) {
			continue
		}
		firstRecord := cursor + len(modernAnimationHeaderTail)
		records := scanProjectAnimationRecords(payload, firstRecord, count)
		if len(records) != count {
			continue
		}
		candidates = append(candidates, ProjectAnimationDirectory{
			Format:       "kryo-animation-map-v1",
			HeaderOffset: offset,
			Count:        count,
			Records:      records,
		})
	}
	if len(candidates) == 0 {
		if directory, err := discoverProjectAnimationsV42(payload); err == nil {
			return directory, nil
		}
		// 旧 4.3 项目没有现代 Animation Map 头。统一返回旧对象图
		// 的动画区间，供所有时间线解析器复用，避免导出器静默跳过动画。
		if strings.HasPrefix(legacyProject43Family(payload), "spine-4.3-legacy-project") {
			if directory := discoverLegacyProjectAnimations(payload, nil, ""); len(directory.Records) > 0 {
				return directory, nil
			}
		}
		return discoverProjectAnimationsV2(payload)
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d animation map candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

// discoverProjectAnimationsV42 解析 4.2.x 项目保存布局中的动画 Map。
// 4.2 在动画 Map 头部多了一层字段包装，Map value 也多出 06 01；
// 其后仍复用同一套动画对象与关键帧数据边界。
func discoverProjectAnimationsV42(payload []byte) (*ProjectAnimationDirectory, error) {
	candidates := make([]ProjectAnimationDirectory, 0, 1)
	for offset := 0; offset+len(modernAnimationHeaderPrefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], modernAnimationHeaderPrefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(modernAnimationHeaderPrefix))
		if !ok || count < 1 || count > 10_000 ||
			cursor+len(modernAnimationHeaderSuffix)+1 > len(payload) ||
			!bytes.Equal(payload[cursor:cursor+len(modernAnimationHeaderSuffix)], modernAnimationHeaderSuffix) {
			continue
		}
		cursor += len(modernAnimationHeaderSuffix)
		if payload[cursor] != 0x09 {
			continue
		}
		cursor++
		if cursor+2 <= len(payload) && bytes.Equal(payload[cursor:cursor+2], []byte{0x05, 0x01}) {
			cursor += 2
		}
		if cursor+len(modernAnimationHeaderTail) > len(payload) ||
			!bytes.HasPrefix(payload[cursor:], modernAnimationHeaderTail) {
			continue
		}
		firstRecord := cursor + len(modernAnimationHeaderTail)
		records := scanProjectAnimationRecordsWithValuePrefix(
			payload,
			firstRecord,
			count,
			legacyV42AnimationValuePrefix,
			false,
		)
		if len(records) != count {
			continue
		}
		candidates = append(candidates, ProjectAnimationDirectory{
			Format:       "kryo-animation-map-v42",
			HeaderOffset: offset,
			Count:        count,
			Records:      records,
		})
	}
	if len(candidates) == 0 {
		return nil, &ParseError{Code: ErrInvalidProject, Msg: "4.2 project animation map was not found"}
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d 4.2 animation map candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

// discoverProjectAnimationsV2 supports the 4.3.23 tagged-field layout. The
// map header still carries the exact entry count, while each inline key is
// guarded by both the map-entry prefix and the Animation value class prefix.
func discoverProjectAnimationsV2(payload []byte) (*ProjectAnimationDirectory, error) {
	candidates := make([]ProjectAnimationDirectory, 0, 1)
	for offset := 0; offset+len(modernAnimationHeaderPrefix) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], modernAnimationHeaderPrefix) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+len(modernAnimationHeaderPrefix))
		if !ok || count < 1 || count > 10_000 ||
			cursor+len(modernAnimationHeaderSuffix)+1 > len(payload) ||
			!bytes.Equal(payload[cursor:cursor+len(modernAnimationHeaderSuffix)], modernAnimationHeaderSuffix) {
			continue
		}
		cursor += len(modernAnimationHeaderSuffix)
		if payload[cursor] != 0x0a {
			continue
		}
		records := scanProjectAnimationRecordsV2(payload, cursor+1, count)
		if len(records) != count {
			continue
		}
		candidates = append(candidates, ProjectAnimationDirectory{
			Format:       "kryo-animation-map-v2",
			HeaderOffset: offset,
			Count:        count,
			Records:      records,
		})
	}
	if len(candidates) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported project animation map was not found",
		}
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d V2 animation map candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

func scanProjectAnimationRecordsV2(
	payload []byte,
	firstOffset int,
	count int,
) []ProjectAnimationRecord {
	records := make([]ProjectAnimationRecord, 0, count)
	for keyOffset := firstOffset; keyOffset < len(payload) && len(records) < count; keyOffset++ {
		if keyOffset > firstOffset && isUnterminatedASCII(payload[keyOffset-1]) {
			continue
		}
		name, end, ok := decodeProjectASCII(payload, keyOffset)
		if !ok || end+len(projectAnimationV2Value) > len(payload) ||
			!bytes.Equal(payload[end:end+len(projectAnimationV2Value)], projectAnimationV2Value) {
			continue
		}
		records = append(records, ProjectAnimationRecord{Name: name, Offset: keyOffset})
		keyOffset = end + len(projectAnimationV2Value) - 1
	}
	if len(records) != count {
		return nil
	}
	for index := range records {
		if index+1 < len(records) {
			records[index].EndOffset = records[index+1].Offset
		} else {
			records[index].EndOffset = len(payload)
		}
	}
	return records
}

func scanProjectAnimationRecords(
	payload []byte,
	firstOffset int,
	count int,
) []ProjectAnimationRecord {
	return scanProjectAnimationRecordsWithValuePrefix(
		payload,
		firstOffset,
		count,
		modernAnimationValuePrefix,
		true,
	)
}

func scanProjectAnimationRecordsWithValuePrefix(
	payload []byte,
	firstOffset int,
	count int,
	valuePrefix []byte,
	requireFirstOffset bool,
) []ProjectAnimationRecord {
	records := make([]ProjectAnimationRecord, 0, count)
	for offset := firstOffset; offset < len(payload) && len(records) < count; offset++ {
		if offset != firstOffset && isUnterminatedASCII(payload[offset-1]) {
			continue
		}
		name, end, ok := decodeProjectASCII(payload, offset)
		if !ok || end+len(valuePrefix) > len(payload) ||
			!bytes.Equal(payload[end:end+len(valuePrefix)], valuePrefix) {
			continue
		}
		records = append(records, ProjectAnimationRecord{Name: name, Offset: offset})
		offset = end + len(valuePrefix) - 1
	}
	if len(records) != count || len(records) == 0 || (requireFirstOffset && records[0].Offset != firstOffset) {
		return nil
	}
	for index := range records {
		if index+1 < len(records) {
			records[index].EndOffset = records[index+1].Offset
		} else {
			records[index].EndOffset = len(payload)
		}
	}
	return records
}

func decodeProjectASCII(payload []byte, offset int) (string, int, bool) {
	if offset < 0 || offset >= len(payload) {
		return "", offset, false
	}
	const maxASCIIBytes = 63
	decoded := make([]byte, 0, 16)
	for cursor := offset; cursor < len(payload) && cursor-offset < maxASCIIBytes; cursor++ {
		value := payload[cursor]
		character := value & 0x7f
		if character < 0x20 || character > 0x7e {
			return "", offset, false
		}
		decoded = append(decoded, character)
		if value&0x80 != 0 {
			return string(decoded), cursor + 1, true
		}
	}
	return "", offset, false
}

func isUnterminatedASCII(value byte) bool {
	return value&0x80 == 0 && value >= 0x20 && value <= 0x7e
}

func readPositiveVarint(payload []byte, offset int) (int, int, bool) {
	value := 0
	for shift := 0; shift <= 28 && offset < len(payload); shift += 7 {
		current := payload[offset]
		offset++
		value |= int(current&0x7f) << shift
		if current&0x80 == 0 {
			return value, offset, true
		}
	}
	return 0, offset, false
}
