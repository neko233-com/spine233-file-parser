package spineparser

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func deformBinding43Fixture(t *testing.T) map[string]any {
	t.Helper()
	var document map[string]any
	decoder := json.NewDecoder(strings.NewReader(`{"bones":[{"name":"root"},{"name":"z","parent":"root"},{"name":"a","parent":"root"}],"skins":[{"name":"default","attachments":{"slot":{"mesh":{"type":"mesh","uvs":[0,0,1,0,0,1],"vertices":[2,1,11,12,0.4,2,21,22,0.6,1,2,31,32,1,1,1,41,42,1]},"linked":{"type":"mesh","source":"mesh"}}}}]}`))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	return document
}

func TestResolveDeform43NamedOrderingAndLinkedMesh(t *testing.T) {
	document := deformBinding43Fixture(t)
	before, _ := json.Marshal(document)
	for _, name := range []string{"mesh", "linked"} {
		binding, err := ResolveAnimation43DeformBinding(document, "default", "slot", name)
		if err != nil {
			t.Fatal(err)
		}
		if binding.Count != 8 || !reflect.DeepEqual(binding.OffsetOrder, []int{2, 3, 0, 1, 4, 5, 6, 7}) {
			t.Fatalf("bad binding: %+v", binding)
		}
		first := binding.Influences[0].([]any)[0].(map[string]any)
		if first["bone"] != "a" || first["x"] != json.Number("21") || first["weight"] != json.Number("0.6") {
			t.Fatalf("lost named or exact numeric identity: %+v", first)
		}
	}
	after, _ := json.Marshal(document)
	if string(before) != string(after) {
		t.Fatal("resolver mutated source")
	}
}

func TestResolveDeform43RejectsUnsafeLayouts(t *testing.T) {
	for _, mutation := range []string{"bone", "duplicate", "count", "cycle", "missing"} {
		t.Run(mutation, func(t *testing.T) {
			d := deformBinding43Fixture(t)
			slot := d["skins"].([]any)[0].(map[string]any)["attachments"].(map[string]any)["slot"].(map[string]any)
			mesh := slot["mesh"].(map[string]any)
			v := mesh["vertices"].([]any)
			switch mutation {
			case "bone":
				v[1] = json.Number("99")
			case "duplicate":
				v[5] = json.Number("1")
			case "count":
				mesh["uvs"] = []any{0.0, 0.0}
			case "cycle":
				mesh["source"] = "linked"
			case "missing":
				delete(slot, "mesh")
			}
			if _, err := ResolveAnimation43DeformBinding(d, "default", "slot", "mesh"); err == nil {
				t.Fatal("unsafe layout accepted")
			}
		})
	}
}
