package spineparser

import (
	"reflect"
	"testing"
)

func TestProjectSkinAttachmentOrderWindowChoosesNarrowestCoverage(t *testing.T) {
	candidates := []projectSkinAttachmentOrderCandidate{
		{item: 0, offset: 10},
		{item: 1, offset: 20},
		{item: 0, offset: 100},
		{item: 1, offset: 101},
		{item: 2, offset: 102},
	}
	start, end, ok := projectSkinAttachmentOrderWindow(candidates, 3)
	if !ok || start != 2 || end != 4 {
		t.Fatalf(
			"narrowest coverage = %d..%d, ok=%v, want 2..4",
			start,
			end,
			ok,
		)
	}
}

func TestCompactProjectSkinAttachmentOrderCandidates(t *testing.T) {
	candidates := []projectSkinAttachmentOrderCandidate{
		{item: 0, offset: 10},
		{item: 0, offset: 10},
		{item: 0, offset: 11},
		{item: 1, offset: 12},
		{item: 1, offset: 12},
	}
	got := compactProjectSkinAttachmentOrderCandidates(candidates)
	want := []projectSkinAttachmentOrderCandidate{
		{item: 0, offset: 10},
		{item: 0, offset: 11},
		{item: 1, offset: 12},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compacted candidates = %#v, want %#v", got, want)
	}
}

func TestProjectSkinAttachmentOrderWindowRequiresCompleteCoverage(t *testing.T) {
	candidates := []projectSkinAttachmentOrderCandidate{
		{item: 0, offset: 10},
		{item: 1, offset: 20},
		{item: 0, offset: 30},
	}
	if _, _, ok := projectSkinAttachmentOrderWindow(candidates, 3); ok {
		t.Fatal("incomplete three-item coverage unexpectedly accepted")
	}
}
