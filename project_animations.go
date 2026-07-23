package spineparser

import (
	"bytes"
	"fmt"
)

var (
	modernAnimationHeaderPrefix = []byte{0x07, 0x0f, 0x01}
	modernAnimationHeaderSuffix = []byte{0x12, 0x01}
	modernAnimationHeaderTail   = []byte{0x00, 0x03, 0x01, 0x01}
	modernAnimationValuePrefix  = []byte{0x02, 0x0f, 0x01}
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
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "supported project animation map was not found",
		}
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("project contains %d animation map candidates", len(candidates)),
		}
	}
	return &candidates[0], nil
}

func scanProjectAnimationRecords(
	payload []byte,
	firstOffset int,
	count int,
) []ProjectAnimationRecord {
	records := make([]ProjectAnimationRecord, 0, count)
	seen := make(map[string]struct{}, count)
	for offset := firstOffset; offset < len(payload) && len(records) < count; offset++ {
		if offset != firstOffset && isUnterminatedASCII(payload[offset-1]) {
			continue
		}
		name, end, ok := decodeProjectASCII(payload, offset)
		if !ok || end+len(modernAnimationValuePrefix) > len(payload) ||
			!bytes.Equal(payload[end:end+len(modernAnimationValuePrefix)], modernAnimationValuePrefix) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		records = append(records, ProjectAnimationRecord{Name: name, Offset: offset})
		offset = end + len(modernAnimationValuePrefix) - 1
	}
	if len(records) != count || len(records) == 0 || records[0].Offset != firstOffset {
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
