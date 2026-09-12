package spineparser

import (
	"fmt"
	"sort"
)

// ProjectConstraintOrderRecord identifies one setup constraint in runtime
// execution order.
type ProjectConstraintOrderRecord struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type projectConstraintSetupRecord struct {
	kind      string
	name      string
	classID   byte
	reference int
	offset    int
	endOffset int
	inline    bool
	successor int
}

// ResolveProjectConstraintOrder reconstructs Spine's unified setup constraint
// order from object creation order and explicit inline-predecessor references.
func ResolveProjectConstraintOrder(
	payload []byte,
	physics *ProjectPhysicsConstraintDirectory,
	paths *ProjectPathConstraintDirectory,
) ([]ProjectConstraintOrderRecord, error) {
	records := make([]projectConstraintSetupRecord, 0)
	physicsRecords := make([]ProjectPhysicsConstraintRecord, 0)
	if physics != nil {
		if !physics.ReferencesComplete {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  "physics constraint references are incomplete",
			}
		}
		physicsRecords = append(physicsRecords, physics.Records...)
		sort.Slice(physicsRecords, func(left int, right int) bool {
			return physicsRecords[left].Offset < physicsRecords[right].Offset
		})
		for _, record := range physicsRecords {
			records = append(records, projectConstraintSetupRecord{
				kind:      "physics",
				name:      record.Name,
				classID:   0x79,
				reference: record.WireReference,
				offset:    record.Offset,
				endOffset: record.EndOffset,
			})
		}
	}
	pathRecords := make([]ProjectPathConstraintRecord, 0)
	if paths != nil {
		if !paths.ReferencesComplete {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  "path constraint references are incomplete",
			}
		}
		pathRecords = append(pathRecords, paths.Records...)
		sort.Slice(pathRecords, func(left int, right int) bool {
			return pathRecords[left].Offset < pathRecords[right].Offset
		})
		for _, record := range pathRecords {
			records = append(records, projectConstraintSetupRecord{
				kind:      "path",
				name:      record.Name,
				classID:   0x48,
				reference: record.WireReference,
				offset:    record.Offset,
				endOffset: record.EndOffset,
			})
		}
	}
	if len(records) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "setup constraints were not found",
		}
	}
	if len(records) == 1 {
		return []ProjectConstraintOrderRecord{
			{Type: records[0].kind, Name: records[0].name},
		}, nil
	}

	if len(physicsRecords) > 1 &&
		physicsRecords[1].WireReference ==
			physicsRecords[0].WireReference+2 {
		if !markProjectInlineConstraint(
			records,
			"physics",
			physicsRecords[0].Offset,
			physicsRecords[0].WireReference,
		) {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  "inline physics constraint could not be identified",
			}
		}
	}
	if len(pathRecords) != 0 {
		ownerReference := 0
		for owner, dataReference := range paths.groupOwnerReferences {
			if dataReference != pathRecords[0].WireReference {
				continue
			}
			if ownerReference != 0 {
				return nil, &ParseError{
					Code: ErrInvalidProject,
					Msg:  "inline path constraint has multiple owner references",
				}
			}
			ownerReference = owner
		}
		if ownerReference != 0 &&
			!markProjectInlineConstraint(
				records,
				"path",
				pathRecords[0].Offset,
				ownerReference,
			) {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  "inline path constraint could not be identified",
			}
		}
	}

	sort.Slice(records, func(left int, right int) bool {
		return records[left].offset < records[right].offset
	})
	for inlineIndex := range records {
		if !records[inlineIndex].inline {
			continue
		}
		successor := -1
		for index := range records {
			if records[index].inline {
				continue
			}
			end := records[index].endOffset + 256
			if index+1 < len(records) {
				end = records[index+1].offset
			}
			if end > len(payload) {
				end = len(payload)
			}
			if !hasProjectConstraintInlinePredecessorReference(
				payload,
				records[index].endOffset,
				end,
				records[inlineIndex].classID,
				records[inlineIndex].reference,
			) {
				continue
			}
			if successor >= 0 {
				return nil, &ParseError{
					Code: ErrInvalidProject,
					Msg: fmt.Sprintf(
						"inline %s constraint %q has multiple successors",
						records[inlineIndex].kind,
						records[inlineIndex].name,
					),
				}
			}
			successor = index
		}
		if successor < 0 {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg: fmt.Sprintf(
					"inline %s constraint %q successor is missing",
					records[inlineIndex].kind,
					records[inlineIndex].name,
				),
			}
		}
		records[inlineIndex].successor = records[successor].offset
	}

	ordered := make([]projectConstraintSetupRecord, 0, len(records))
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].inline {
			continue
		}
		for inlineIndex := range records {
			if records[inlineIndex].inline &&
				records[inlineIndex].successor == records[index].offset {
				ordered = append(ordered, records[inlineIndex])
			}
		}
		ordered = append(ordered, records[index])
	}
	if len(ordered) != len(records) {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "unified setup constraint order is incomplete",
		}
	}
	result := make([]ProjectConstraintOrderRecord, len(ordered))
	for index, record := range ordered {
		result[index] = ProjectConstraintOrderRecord{
			Type: record.kind,
			Name: record.name,
		}
	}
	return result, nil
}

func markProjectInlineConstraint(
	records []projectConstraintSetupRecord,
	kind string,
	offset int,
	reference int,
) bool {
	for index := range records {
		if records[index].kind != kind || records[index].offset != offset {
			continue
		}
		records[index].inline = true
		records[index].reference = reference
		return true
	}
	return false
}

func hasProjectConstraintInlinePredecessorReference(
	payload []byte,
	start int,
	end int,
	classID byte,
	reference int,
) bool {
	if start < 0 || start >= end || end > len(payload) {
		return false
	}
	for offset := start; offset+3 < end; offset++ {
		if payload[offset] != 0x03 &&
			payload[offset] != 0x07 &&
			payload[offset] != 0x08 &&
			payload[offset] != 0x09 &&
			payload[offset] != 0x0b {
			continue
		}
		if payload[offset+1] != classID {
			continue
		}
		value, _, ok := readPositiveVarint(payload, offset+2)
		if ok && value == reference {
			return true
		}
	}
	return false
}
