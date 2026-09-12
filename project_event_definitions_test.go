package spineparser

import (
	"reflect"
	"testing"
)

func TestDiscoverProjectEventDefinitions(t *testing.T) {
	payload := []byte{0xaa}
	for _, name := range []string{"event_weapon_off", "event_weapon_on"} {
		payload = append(payload, projectEventDefinitionPrefix...)
		payload = append(payload, kryoASCIIForTest(name)...)
		payload = append(payload, projectEventDefinitionSuffix...)
	}
	names, err := DiscoverProjectEventDefinitions(payload)
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"event_weapon_off", "event_weapon_on"}
	if !reflect.DeepEqual(names, expected) {
		t.Fatalf("names = %#v", names)
	}
}

func TestDiscoverProjectEventDefinitionsIgnoresIncompleteCandidate(t *testing.T) {
	payload := append([]byte{}, projectEventDefinitionPrefix...)
	payload = append(payload, kryoASCIIForTest("event")...)
	names, err := DiscoverProjectEventDefinitions(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("names = %#v", names)
	}
}
