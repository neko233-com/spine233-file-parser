package spineparser

import "testing"

func TestDiscoverProjectSkeletonMetadata(t *testing.T) {
	payload := []byte{0xaa, 0x22, 0x01}
	payload = append(payload, kryoASCIIForTest("./audio")...)
	payload = append(payload, 0x26, 0x6c, 0x01)
	payload = append(payload, projectSkeletonImagesFieldV2...)
	payload = append(payload, kryoASCIIForTest("./img/")...)
	payload = append(payload, 0xbb)

	metadata, err := DiscoverProjectSkeletonMetadata(payload)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Audio != "./audio" || metadata.Images != "./img/" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestDiscoverProjectSkeletonMetadataRejectsLooseStrings(t *testing.T) {
	payload := append(kryoASCIIForTest("./audio"), kryoASCIIForTest("./img/")...)
	if _, err := DiscoverProjectSkeletonMetadata(payload); err == nil {
		t.Fatal("expected metadata structure error")
	}
}

func TestDiscoverProjectSkeletonMetadataAcceptsNullAudio(t *testing.T) {
	payload := []byte{0xaa, 0x22, 0x18, 0x26}
	payload = append(payload, projectSkeletonImagesFieldV2...)
	payload = append(payload, kryoASCIIForTest("./images/")...)

	metadata, err := DiscoverProjectSkeletonMetadata(payload)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Audio != "" || metadata.Images != "./images/" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestDiscoverProjectSkeletonMetadataAcceptsLongKryoAudioPath(t *testing.T) {
	audio := "C:/Users/Administrator/Desktop/Git-Projects/Team-Art-Resources/audio"
	payload := []byte{0xaa, 0x22, 0x01}
	payload = append(payload, kryoUTF8ForMetadataTest(audio)...)
	payload = append(payload, 0x26, 0x6c, 0x01)
	payload = append(payload, projectSkeletonImagesFieldV2...)
	payload = append(payload, kryoASCIIForTest("./images/")...)

	metadata, err := DiscoverProjectSkeletonMetadata(payload)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Audio != audio || metadata.Images != "./images/" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func kryoUTF8ForMetadataTest(value string) []byte {
	length := len(value) + 1
	first := byte(length & 0x3f)
	length >>= 6
	first |= 0x80
	if length != 0 {
		first |= 0x40
	}
	result := []byte{first}
	for length != 0 {
		current := byte(length & 0x7f)
		length >>= 7
		if length != 0 {
			current |= 0x80
		}
		result = append(result, current)
	}
	return append(result, value...)
}
