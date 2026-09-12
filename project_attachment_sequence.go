package spineparser

import "bytes"

var projectAttachmentSequenceMarker = []byte{0x01, 0x04, 0x04, 0x00, 0x02}

// ProjectAttachmentSequence contains setup-pose image sequence metadata.
type ProjectAttachmentSequence struct {
	Count  int `json:"count"`
	Start  int `json:"start"`
	Digits int `json:"digits"`
	Setup  int `json:"setup"`
}

func readProjectAttachmentSequence(
	payload []byte,
	start int,
	end int,
	terminalTag byte,
) *ProjectAttachmentSequence {
	if start < 0 || start >= end || end > len(payload) {
		return nil
	}
	relative := bytes.Index(payload[start:end], projectAttachmentSequenceMarker)
	if relative < 0 {
		return nil
	}
	cursor := start + relative + len(projectAttachmentSequenceMarker)
	count, cursor, ok := readProjectSequenceInteger(payload, cursor, end)
	if !ok || count < 1 || cursor >= end || payload[cursor] != 0x03 {
		return nil
	}
	digits, cursor, ok := readProjectSequenceInteger(payload, cursor+1, end)
	if !ok || digits < 1 || cursor >= end || payload[cursor] != 0x01 {
		return nil
	}
	startValue, cursor, ok := readProjectSequenceInteger(payload, cursor+1, end)
	if !ok || cursor >= end || payload[cursor] != terminalTag {
		return nil
	}
	return &ProjectAttachmentSequence{
		Count:  count,
		Start:  startValue,
		Digits: digits,
	}
}

func readProjectSequenceInteger(
	payload []byte,
	cursor int,
	end int,
) (int, int, bool) {
	value, next, ok := readPositiveVarint(payload, cursor)
	if !ok || next > end || value&1 != 0 {
		return 0, cursor, false
	}
	return value >> 1, next, true
}
