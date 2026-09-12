package spineparser

import (
	"strings"
	"testing"
)

func TestResolveProjectRuntimeVersionProfile(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		exactKey  string
		familyKey string
	}{
		{name: "4.2 exact", source: "4.2.43", exactKey: "4.2.43", familyKey: "4.2.x"},
		{name: "4.3 padded patch", source: "4.3.06", exactKey: "4.3.6", familyKey: "4.3.x"},
		{name: "4.3 exact", source: "4.3.19", exactKey: "4.3.19", familyKey: "4.3.x"},
		{name: "minor only", source: "4.2", exactKey: "", familyKey: "4.2.x"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile, err := resolveProjectRuntimeVersionProfile(test.source)
			if err != nil {
				t.Fatal(err)
			}
			if profile.ExactKey != test.exactKey || profile.FamilyKey != test.familyKey {
				t.Fatalf("profile=%+v, want exact=%q family=%q", profile, test.exactKey, test.familyKey)
			}
		})
	}
}

func TestDiscoverProjectRuntimeModelVersionRouting(t *testing.T) {
	versions := []string{
		"4.2.43",
		"4.3.06",
		"4.3.08",
		"4.3.11",
		"4.3.14",
		"4.3.15",
		"4.3.17",
		"4.3.19",
		"4.3.23",
		"4.3.26",
	}
	for _, version := range versions {
		profile, err := resolveProjectRuntimeVersionProfile(version)
		if err != nil {
			t.Fatal(err)
		}
		if projectRuntimeAdapters[profile.ExactKey] == nil {
			t.Fatalf("version %s has no dedicated adapter: %+v", version, profile)
		}
	}
}

func TestDiscoverProjectRuntimeModelRejectsUnknownPatchVersion(t *testing.T) {
	_, err := DiscoverProjectRuntimeModel([]byte{0x01}, "4.3.27")
	if err == nil || !strings.Contains(err.Error(), "no dedicated project adapter") {
		t.Fatalf("error = %v, want missing dedicated adapter", err)
	}
}
