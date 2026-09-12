package spineparser

import (
	"fmt"
	"sort"
)

// ProjectSkinAttachmentOrderRecord identifies one default-skin attachment.
type ProjectSkinAttachmentOrderRecord struct {
	ClassID       int `json:"classId"`
	WireReference int `json:"wireReference"`
}

// ProjectSkinAttachmentOrderDirectory preserves project default-skin order.
type ProjectSkinAttachmentOrderDirectory struct {
	Format      string                             `json:"format"`
	RegionStart int                                `json:"regionStart"`
	RegionEnd   int                                `json:"regionEnd"`
	Count       int                                `json:"count"`
	Records     []ProjectSkinAttachmentOrderRecord `json:"records"`
}

type projectSkinAttachmentOrderItem struct {
	record ProjectSkinAttachmentOrderRecord
	offset int
}

type projectSkinAttachmentOrderCandidate struct {
	item   int
	offset int
}

// DiscoverProjectDefaultSkinAttachmentOrder recovers the attachment map order
// before the animation graph begins. Kryo may recursively create most of the
// object graph while writing the first map entry, so the dense tail can omit
// that first inline attachment.
func DiscoverProjectDefaultSkinAttachmentOrder(
	payload []byte,
) (*ProjectSkinAttachmentOrderDirectory, error) {
	if len(payload) == 0 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	animations, err := DiscoverProjectAnimations(payload)
	if err != nil {
		return nil, err
	}
	items, err := discoverProjectSkinAttachmentOrderItems(payload)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "default skin contains no supported attachments",
		}
	}
	if len(items) == 1 {
		return &ProjectSkinAttachmentOrderDirectory{
			Format:      "kryo-default-skin-attachment-order-v2",
			RegionStart: items[0].offset,
			RegionEnd:   items[0].offset,
			Count:       1,
			Records:     []ProjectSkinAttachmentOrderRecord{items[0].record},
		}, nil
	}

	itemByIdentity := make(
		map[ProjectSkinAttachmentOrderRecord]int,
		len(items),
	)
	candidates := make([]projectSkinAttachmentOrderCandidate, 0, len(items)*2)
	for index, item := range items {
		if item.record.WireReference == 0 {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  "default skin attachment reference is incomplete",
			}
		}
		if _, duplicate := itemByIdentity[item.record]; duplicate {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg: fmt.Sprintf(
					"default skin attachment reference %d:%d is duplicated",
					item.record.ClassID,
					item.record.WireReference,
				),
			}
		}
		itemByIdentity[item.record] = index
		if item.offset >= 0 && item.offset < animations.HeaderOffset {
			candidates = append(candidates, projectSkinAttachmentOrderCandidate{
				item:   index,
				offset: item.offset,
			})
		}
	}
	for offset := 0; offset+2 < animations.HeaderOffset; offset++ {
		classID := int(payload[offset])
		if !supportedProjectAttachmentClassID(classID) {
			continue
		}
		reference, _, ok := readPositiveVarint(payload, offset+1)
		if !ok {
			continue
		}
		index, exists := itemByIdentity[ProjectSkinAttachmentOrderRecord{
			ClassID:       classID,
			WireReference: reference,
		}]
		if !exists {
			continue
		}
		candidates = append(candidates, projectSkinAttachmentOrderCandidate{
			item:   index,
			offset: offset,
		})
	}
	sort.Slice(candidates, func(left int, right int) bool {
		if candidates[left].offset != candidates[right].offset {
			return candidates[left].offset < candidates[right].offset
		}
		return candidates[left].item < candidates[right].item
	})
	candidates = compactProjectSkinAttachmentOrderCandidates(candidates)

	fullStart, fullEnd, fullOK := projectSkinAttachmentOrderWindow(
		candidates,
		len(items),
	)
	if !fullOK {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "default skin attachment order is incomplete",
		}
	}
	start := fullStart
	end := fullEnd
	prepend := -1
	partialStart, partialEnd, partialOK := projectSkinAttachmentOrderWindow(
		candidates,
		len(items)-1,
	)
	if partialOK {
		fullWidth := candidates[fullEnd].offset - candidates[fullStart].offset
		partialWidth := candidates[partialEnd].offset -
			candidates[partialStart].offset
		if fullWidth > 64*1024 && partialWidth*4 < fullWidth {
			seen := make(map[int]struct{}, len(items)-1)
			for index := partialStart; index <= partialEnd; index++ {
				seen[candidates[index].item] = struct{}{}
			}
			for index := range items {
				if _, exists := seen[index]; exists {
					continue
				}
				if prepend >= 0 || items[index].offset >= candidates[partialStart].offset {
					return nil, &ParseError{
						Code: ErrInvalidProject,
						Msg:  "default skin recursive root is ambiguous",
					}
				}
				prepend = index
			}
			if prepend < 0 {
				return nil, &ParseError{
					Code: ErrInvalidProject,
					Msg:  "default skin recursive root is absent",
				}
			}
			start = partialStart
			end = partialEnd
		}
	}

	ordered := make([]ProjectSkinAttachmentOrderRecord, 0, len(items))
	used := make(map[int]struct{}, len(items))
	if prepend >= 0 {
		ordered = append(ordered, items[prepend].record)
		used[prepend] = struct{}{}
	}
	for index := start; index <= end; index++ {
		item := candidates[index].item
		if _, exists := used[item]; exists {
			continue
		}
		ordered = append(ordered, items[item].record)
		used[item] = struct{}{}
	}
	if len(ordered) != len(items) {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg: fmt.Sprintf(
				"default skin attachment order contains %d/%d records",
				len(ordered),
				len(items),
			),
		}
	}
	return &ProjectSkinAttachmentOrderDirectory{
		Format:      "kryo-default-skin-attachment-order-v2",
		RegionStart: candidates[start].offset,
		RegionEnd:   candidates[end].offset,
		Count:       len(ordered),
		Records:     ordered,
	}, nil
}

func discoverProjectSkinAttachmentOrderItems(
	payload []byte,
) ([]projectSkinAttachmentOrderItem, error) {
	items := make([]projectSkinAttachmentOrderItem, 0)
	if regions, err := DiscoverProjectRegionAttachments(payload); err == nil {
		for _, record := range regions.Records {
			items = append(items, projectSkinAttachmentOrderItem{
				record: ProjectSkinAttachmentOrderRecord{
					ClassID:       ProjectAttachmentClassRegion,
					WireReference: record.WireReference,
				},
				offset: record.Offset,
			})
		}
	}
	if meshes, err := DiscoverProjectMeshAttachments(payload); err == nil {
		for _, record := range meshes.Records {
			items = append(items, projectSkinAttachmentOrderItem{
				record: ProjectSkinAttachmentOrderRecord{
					ClassID:       ProjectAttachmentClassMesh,
					WireReference: record.WireReference,
				},
				offset: record.Offset,
			})
		}
	}
	if vertices, err := DiscoverProjectVertexAttachments(payload); err == nil {
		for _, record := range vertices.Records {
			items = append(items, projectSkinAttachmentOrderItem{
				record: ProjectSkinAttachmentOrderRecord{
					ClassID:       record.ClassID,
					WireReference: record.WireReference,
				},
				offset: record.Offset,
			})
		}
	}
	return items, nil
}

func compactProjectSkinAttachmentOrderCandidates(
	candidates []projectSkinAttachmentOrderCandidate,
) []projectSkinAttachmentOrderCandidate {
	if len(candidates) < 2 {
		return candidates
	}
	result := candidates[:1]
	for _, candidate := range candidates[1:] {
		previous := result[len(result)-1]
		if candidate.item == previous.item && candidate.offset == previous.offset {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func projectSkinAttachmentOrderWindow(
	candidates []projectSkinAttachmentOrderCandidate,
	target int,
) (int, int, bool) {
	if target < 1 || len(candidates) < target {
		return 0, 0, false
	}
	counts := make(map[int]int, target)
	unique := 0
	left := 0
	bestStart := 0
	bestEnd := -1
	for right, candidate := range candidates {
		if counts[candidate.item] == 0 {
			unique++
		}
		counts[candidate.item]++
		for unique >= target && left <= right {
			if bestEnd < bestStart ||
				candidates[right].offset-candidates[left].offset <
					candidates[bestEnd].offset-candidates[bestStart].offset {
				bestStart = left
				bestEnd = right
			}
			item := candidates[left].item
			counts[item]--
			if counts[item] == 0 {
				delete(counts, item)
				unique--
			}
			left++
		}
	}
	return bestStart, bestEnd, bestEnd >= bestStart
}
