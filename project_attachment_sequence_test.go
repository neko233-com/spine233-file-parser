package spineparser

import "testing"

func TestReadProjectAttachmentSequence(t *testing.T) {
	payload := []byte{
		0xaa,
		0x01, 0x04, 0x04, 0x00, 0x02,
		0x2a,
		0x03, 0x0a,
		0x01, 0x00,
		0x0a,
		0xbb,
	}
	sequence := readProjectAttachmentSequence(payload, 0, len(payload), 0x0a)
	if sequence == nil ||
		sequence.Count != 21 ||
		sequence.Digits != 5 ||
		sequence.Start != 0 {
		t.Fatalf("sequence = %#v", sequence)
	}
}

func TestReadProjectAttachmentSequenceRejectsWrongTerminal(t *testing.T) {
	payload := []byte{
		0x01, 0x04, 0x04, 0x00, 0x02,
		0x0a,
		0x03, 0x02,
		0x01, 0x02,
		0x0a,
	}
	if sequence := readProjectAttachmentSequence(
		payload,
		0,
		len(payload),
		0x1c,
	); sequence != nil {
		t.Fatalf("sequence = %#v", sequence)
	}
}
