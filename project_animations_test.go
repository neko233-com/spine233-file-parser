package spineparser

import "testing"

func TestDiscoverProjectAnimations(t *testing.T) {
	payload := []byte{0x55, 0x66}
	payload = append(payload, modernAnimationHeaderPrefix...)
	payload = append(payload, 0x03)
	payload = append(payload, modernAnimationHeaderSuffix...)
	payload = append(payload, 0x0a)
	payload = append(payload, modernAnimationHeaderTail...)
	payload = append(payload, kryoASCIIForTest("attack")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, 0x00, 0x01, 0x02)
	payload = append(payload, kryoASCIIForTest("idle-from fall")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, 0x04, 0x05)
	payload = append(payload, kryoASCIIForTest("walk")...)
	payload = append(payload, modernAnimationValuePrefix...)
	payload = append(payload, 0x07)

	directory, err := DiscoverProjectAnimations(payload)
	if err != nil {
		t.Fatal(err)
	}
	if directory.Count != 3 || len(directory.Records) != 3 {
		t.Fatalf("directory = %#v", directory)
	}
	got := []string{
		directory.Records[0].Name,
		directory.Records[1].Name,
		directory.Records[2].Name,
	}
	want := []string{"attack", "idle-from fall", "walk"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("record %d = %q, want %q", index, got[index], want[index])
		}
	}
	if directory.Records[0].EndOffset != directory.Records[1].Offset {
		t.Fatal("first record boundary does not end at second key")
	}
}

func TestDiscoverProjectAnimationsRejectsOldLayout(t *testing.T) {
	if _, err := DiscoverProjectAnimations([]byte("old project payload")); err == nil {
		t.Fatal("expected unsupported layout error")
	}
}
