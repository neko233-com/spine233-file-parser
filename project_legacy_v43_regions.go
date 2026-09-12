package spineparser

import (
	"bytes"
	"sort"
	"strings"
)

// discoverLegacyV43ProjectRegions 解析旧 4.3 的 Region 对象图。
//
// 旧布局仍保留同一套 region class，但 path/name 可能是新建字符串，也可能
// 是前一对象的引用。对象尾部还保存真实的 slot 与 attachment 引用；不能把
// 0D 01 0F 当成 slot 表，也不能按图片目录反推全部附件。
func discoverLegacyV43ProjectRegions(
	payload []byte,
	family string,
) *ProjectRegionAttachmentDirectory {
	markers := make([]int, 0)
	prefix := []byte{0x2b, 0x01, 0x11}
	for offset := 0; offset+len(prefix) < len(payload); offset++ {
		if bytes.HasPrefix(payload[offset:], prefix) {
			markers = append(markers, offset)
		}
	}
	if len(markers) == 0 {
		return &ProjectRegionAttachmentDirectory{
			Format:             family + "-regions",
			ReferencesComplete: true,
		}
	}
	type pathReference struct {
		value string
		ref   int
	}
	paths := make(map[int]string)
	lastPath := ""
	records := make([]ProjectRegionAttachmentRecord, 0, len(markers))
	usedReferences := make(map[int]struct{})
	for index, marker := range markers {
		end := len(payload)
		if index+1 < len(markers) {
			end = markers[index+1]
		}
		path, pathEnd, pathRef, ok := readLegacyV43RegionPath(payload, marker+len(prefix), end)
		if !ok {
			continue
		}
		if path == "" && pathRef != 0 {
			path = paths[pathRef]
			if path == "" {
				path = lastPath
			}
		}
		if path == "" || !legacyV43RegionPath(path) {
			continue
		}
		if pathRef == 0 {
			lastPath = path
		}
		if pathRef != 0 {
			paths[pathRef] = path
		}
		ownerReference, ownerEnd, ownerOK := readPositiveVarintAfterZero(payload, pathEnd, end)
		if !ownerOK {
			continue
		}
		attachmentReference := readLegacyV43RegionAttachmentReference(
			payload,
			ownerEnd,
			end,
		)
		if attachmentReference == 0 {
			attachmentReference = projectFirstWireReference + len(records)
		}
		if _, duplicate := usedReferences[attachmentReference]; duplicate {
			attachmentReference = projectFirstWireReference + len(records)
			for {
				if _, exists := usedReferences[attachmentReference]; !exists {
					break
				}
				attachmentReference++
			}
		}
		usedReferences[attachmentReference] = struct{}{}
		width, widthOK := readLegacyV43RegionFloat(payload, marker, pathEnd, 0x0b)
		height, heightOK := readLegacyV43RegionFloat(payload, marker, pathEnd, 0x0c)
		if !widthOK {
			width = 0
		}
		if !heightOK {
			height = 0
		}
		records = append(records, ProjectRegionAttachmentRecord{
			WireReference:      attachmentReference,
			OwnerSlotReference: ownerReference,
			Name:               path,
			Path:               path,
			Offset:             marker,
			ScaleX:             1,
			ScaleY:             1,
			Width:              width,
			Height:             height,
		})
	}
	sort.SliceStable(records, func(left int, right int) bool {
		return records[left].Offset < records[right].Offset
	})
	return &ProjectRegionAttachmentDirectory{
		Format:             family + "-regions-v43-object-graph",
		Count:              len(records),
		ReferencesComplete: len(records) != 0,
		Records:            records,
	}
}

func readLegacyV43RegionPath(
	payload []byte,
	start int,
	end int,
) (string, int, int, bool) {
	bestDirectOffset := -1
	bestDirectEnd := start
	for offset := start; offset+3 < end; offset++ {
		if payload[offset] != 0x01 || payload[offset+1] != 0x01 {
			continue
		}
		name, next, ok := decodeProjectASCII(payload, offset+2)
		if !ok || next > end || !legacyV43RegionPath(name) {
			continue
		}
		// Region 对象的第一个合法路径字段才是对象自身的 path。
		// 后续 payload 还可能携带动画、引用对象或子对象路径；取最后一个会把
		// OwnerSlotReference/WireReference 错配到对象尾部，进而合并 Slot。
		if bestDirectOffset < 0 || offset < bestDirectOffset {
			bestDirectOffset = offset
			bestDirectEnd = next
		}
	}
	if bestDirectOffset >= 0 {
		name, _, ok := decodeProjectASCII(payload, bestDirectOffset+2)
		if !ok {
			return "", start, 0, false
		}
		return name, bestDirectEnd, 0, true
	}
	for offset := start; offset+3 < end; offset++ {
		if payload[offset] != 0x01 || payload[offset+1] == 0x01 {
			continue
		}
		reference, next, ok := readPositiveVarint(payload, offset+1)
		if !ok || reference < projectFirstWireReference || next >= end || payload[next] != 0x00 {
			continue
		}
		owner, _, ownerOK := readPositiveVarint(payload, next+1)
		if !ownerOK || owner < projectFirstWireReference {
			continue
		}
		return "", next, reference, true
	}
	return "", start, 0, false
}

func readPositiveVarintAfterZero(payload []byte, start int, end int) (int, int, bool) {
	if start < 0 || start >= end || payload[start] != 0x00 {
		return 0, start, false
	}
	return readPositiveVarint(payload, start+1)
}

func readLegacyV43RegionAttachmentReference(payload []byte, start int, end int) int {
	for offset := start; offset+1 < end; offset++ {
		if payload[offset] != byte(ProjectAttachmentClassRegion) ||
			(offset+2 < end && payload[offset+1] == 0x01 && payload[offset+2] == 0x11) {
			continue
		}
		reference, _, ok := readPositiveVarint(payload, offset+1)
		if ok && reference >= projectFirstWireReference {
			return reference
		}
	}
	return 0
}

func readLegacyV43RegionFloat(payload []byte, start int, end int, tag byte) (float32, bool) {
	for offset := end - 5; offset >= start; offset-- {
		if payload[offset] != tag {
			continue
		}
		value := projectFloat32(payload[offset+1 : offset+5])
		if finiteProjectFloat(value) {
			return value, true
		}
	}
	return 0, false
}

func legacyV43RegionPath(value string) bool {
	if value == "" || strings.Trim(value, "/\\") == "" || strings.ContainsAny(value, ":\\") {
		return false
	}
	if !strings.ContainsAny(value, "/_") {
		return false
	}
	for _, character := range value {
		if character == '/' || character == '_' || character == '-' ||
			(character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') {
			continue
		}
		return false
	}
	return true
}
