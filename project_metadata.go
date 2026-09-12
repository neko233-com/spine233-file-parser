package spineparser

import (
	"bytes"
	"fmt"
	"unicode/utf8"
)

var projectSkeletonImagesFieldV2 = []byte{
	0x24, 0x0f, 0x01, 0x00, 0x03, 0x01,
}

// ProjectSkeletonMetadata contains project-level export paths decoded from
// the modern skeleton object.
type ProjectSkeletonMetadata struct {
	Offset int    `json:"offset"`
	Images string `json:"images"`
	Audio  string `json:"audio"`
}

// DiscoverProjectSkeletonMetadata decodes the 4.3 project skeleton export
// paths without relying on diagnostic string scans.
func DiscoverProjectSkeletonMetadata(
	payload []byte,
) (*ProjectSkeletonMetadata, error) {
	candidates := make([]ProjectSkeletonMetadata, 0, 1)
	for imagesFieldOffset := 0; ; {
		relative := bytes.Index(
			payload[imagesFieldOffset:],
			projectSkeletonImagesFieldV2,
		)
		if relative < 0 {
			break
		}
		imagesFieldOffset += relative
		images, afterImages, ok := decodeProjectASCII(
			payload,
			imagesFieldOffset+len(projectSkeletonImagesFieldV2),
		)
		if !ok || afterImages > len(payload) || !projectExportPath(images) {
			imagesFieldOffset++
			continue
		}
		searchStart := imagesFieldOffset - 512
		if searchStart < 0 {
			searchStart = 0
		}
		audioFieldOffset := bytes.LastIndexByte(
			payload[searchStart:imagesFieldOffset],
			0x22,
		)
		if audioFieldOffset < 0 {
			imagesFieldOffset++
			continue
		}
		audioFieldOffset += searchStart
		audio := ""
		if audioFieldOffset+1 < imagesFieldOffset &&
			payload[audioFieldOffset+1] == 0x01 {
			var afterAudio int
			var audioOK bool
			audio, afterAudio, audioOK = decodeProjectMetadataString(
				payload,
				audioFieldOffset+2,
			)
			if !audioOK || afterAudio > imagesFieldOffset ||
				!projectExportPath(audio) {
				imagesFieldOffset++
				continue
			}
		}
		candidates = append(candidates, ProjectSkeletonMetadata{
			Offset: audioFieldOffset,
			Images: images,
			Audio:  audio,
		})
		imagesFieldOffset++
	}
	if len(candidates) != 1 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg: fmt.Sprintf(
				"project contains %d modern skeleton metadata candidates",
				len(candidates),
			),
		}
	}
	return &candidates[0], nil
}

func projectExportPath(value string) bool {
	if len(value) >= 2 && (value[0:2] == "./" || value[0:2] == ".\\") {
		return true
	}
	if len(value) >= 3 &&
		((value[0] >= 'A' && value[0] <= 'Z') ||
			(value[0] >= 'a' && value[0] <= 'z')) &&
		value[1] == ':' && (value[2] == '/' || value[2] == '\\') {
		return true
	}
	return len(value) >= 1 && (value[0] == '/' || value[0] == '\\')
}

func decodeProjectMetadataString(
	payload []byte,
	offset int,
) (string, int, bool) {
	if offset < 0 || offset >= len(payload) {
		return "", offset, false
	}
	if payload[offset]&0xc0 == 0x80 {
		return decodeProjectShortASCIIWithEnd(payload, offset)
	}
	if payload[offset]&0xc0 != 0xc0 {
		return decodeProjectASCII(payload, offset)
	}
	length := int(payload[offset] & 0x3f)
	cursor := offset + 1
	if payload[offset]&0x40 != 0 {
		for shift := 6; shift <= 27; shift += 7 {
			if cursor >= len(payload) {
				return "", offset, false
			}
			current := payload[cursor]
			cursor++
			length |= int(current&0x7f) << shift
			if current&0x80 == 0 {
				break
			}
			if shift == 27 {
				return "", offset, false
			}
		}
	}
	length--
	if length < 1 || cursor+length > len(payload) ||
		!utf8.Valid(payload[cursor:cursor+length]) {
		return "", offset, false
	}
	return string(payload[cursor : cursor+length]), cursor + length, true
}
