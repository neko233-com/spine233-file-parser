package spineparser

import "testing"

func TestAssignProjectPathConstraintReferenceSetWithInlineFirstRecord(
	t *testing.T,
) {
	records := []ProjectPathConstraintRecord{
		{Name: "p0", Offset: 100},
		{Name: "p1", Offset: 200},
		{Name: "p2", Offset: 300},
	}
	references := map[int]struct{}{2478: {}, 2561: {}, 2562: {}}
	inline := map[int]struct{}{0: {}}
	if !assignProjectPathConstraintReferenceSet(records, references, inline) {
		t.Fatal("reference set was rejected")
	}
	for index, want := range []int{2560, 2561, 2562} {
		if records[index].WireReference != want {
			t.Fatalf(
				"record[%d].wireReference = %d, want %d",
				index,
				records[index].WireReference,
				want,
			)
		}
	}
}

func TestAssignProjectPathConstraintReferenceSetRejectsGap(t *testing.T) {
	records := []ProjectPathConstraintRecord{
		{Name: "p0", Offset: 100},
		{Name: "p1", Offset: 200},
		{Name: "p2", Offset: 300},
	}
	references := map[int]struct{}{2561: {}, 2563: {}}
	inline := map[int]struct{}{0: {}}
	if assignProjectPathConstraintReferenceSet(records, references, inline) {
		t.Fatal("gapped reference set was accepted")
	}
}

func TestResolveProjectPathTimelineGroupOwnerUsesUniqueInlineConstraint(
	t *testing.T,
) {
	payload := append([]byte(nil), projectPathTimelineGroupPrefixV2...)
	inlineTimelineOffset := len(payload)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, projectTimelinePathPosition, 0x01, 0x01)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 0)
	payload = appendFloat32ForTest(payload, 1)
	payload = append(payload, 0)
	constraintOffset := len(payload)
	payload = append(payload, 0xaa, 0xbb, 0xcc)
	timelineOffset := len(payload)
	payload = append(payload, projectTimelinePrefix...)
	payload = append(payload, projectTimelinePathSpacing, 0x01, 0x01)
	payload = append(payload, projectTimelineKeyPrefix...)
	payload = appendFloat32ForTest(payload, 0)
	payload = appendFloat32ForTest(payload, 200)
	payload = append(payload, 0)
	ownerOffset := len(payload)
	payload = appendPositiveVarintForTest(payload, 2478)
	payload = append(payload, 0x01)
	payload = appendPositiveVarintForTest(payload, 6)
	payload = append(payload, 0x04, 0x01)
	directory := &ProjectPathConstraintDirectory{
		ReferencesComplete: true,
		Records: []ProjectPathConstraintRecord{{
			Name:          "path",
			WireReference: 2560,
			Offset:        constraintOffset,
		}},
	}

	record, ownerEnd, ok := resolveProjectPathTimelineGroupOwner(
		payload,
		timelineOffset,
		ownerOffset,
		len(payload),
		directory,
	)
	if !ok || record.Name != "path" || ownerEnd != len(payload) {
		t.Fatalf(
			"record = %#v, ownerEnd = %d, ok = %t, inline = %d",
			record,
			ownerEnd,
			ok,
			inlineTimelineOffset,
		)
	}
}
