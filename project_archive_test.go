package spineparser

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProjectArchiveRoundTripAndEdit(t *testing.T) {
	original := &ProjectDocument{Payload: namedProjectTransformPayloadForTest(false)}
	archive, err := ArchiveProject(original)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(archive)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ProjectArchive
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	restored, reports, err := RestoreProjectArchive(&decoded)
	if err != nil || len(reports) != 0 || !bytes.Equal(restored.Payload, original.Payload) {
		t.Fatalf("round trip: %v %+v", err, reports)
	}
	before := append([]byte(nil), archive.Payload...)
	archive.Animations[0].Timelines[1].Keys[1].Values[0] = 8
	patched, reports, err := RestoreProjectArchive(archive)
	if err != nil || len(reports) != 1 {
		t.Fatalf("edit: %v %+v", err, reports)
	}
	if !bytes.Equal(archive.Payload, before) || !bytes.Equal(original.Payload, before) {
		t.Fatal("input was mutated")
	}
	directory, err := DiscoverProjectTransformTimelines(patched.Payload, "attack")
	if err != nil {
		t.Fatal(err)
	}
	if directory.Timelines[1].Keys[1].Values[0] != 8 {
		t.Fatal("edit was lost")
	}
	encoded, err := SerializeProject(patched, ProjectSerializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	decodedProject, err := DeserializeProject(encoded, InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decodedProject.Payload, patched.Payload) {
		t.Fatal("serialization lost payload")
	}
}

func TestProjectArchiveRejectsInvalidEdits(t *testing.T) {
	for _, change := range []func(*ProjectArchive){
		func(a *ProjectArchive) { a.Payload[0] ^= 1 },
		func(a *ProjectArchive) { a.Animations[0].Timelines[0].Keys = nil },
		func(a *ProjectArchive) { a.Animations[0].Timelines[0].BoneName = "missing" },
		func(a *ProjectArchive) { a.Animations[0].Timelines[0].Keys[1].Frame = -1 },
		func(a *ProjectArchive) { a.Animations = append(a.Animations, a.Animations[0]) },
	} {
		archive, err := ArchiveProject(&ProjectDocument{Payload: namedProjectTransformPayloadForTest(false)})
		if err != nil {
			t.Fatal(err)
		}
		change(archive)
		if _, _, err := RestoreProjectArchive(archive); err == nil {
			t.Fatal("expected invalid archive error")
		}
	}
}
