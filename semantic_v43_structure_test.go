package spineparser

import (
	"encoding/json"
	"strings"
	"testing"
)

const rich43Fixture = `{"skeleton":{"spine":"4.3.26"},"bones":[{"name":"root"},{"name":"arm","parent":"root"},{"name":"target","parent":"root"}],"slots":[{"name":"arm","bone":"arm","attachment":"arm"}],"skins":[{"name":"default","attachments":{"arm":{"arm":{"width":64,"height":64}}}}],"constraints":[{"name":"ik","type":"ik","bones":["arm"],"target":"target"}],"events":{"hit":{}},"animations":{"attack":{"bones":{"arm":{"rotate":[{"value":0},{"time":1,"value":30}]}},"slots":{"arm":{"attachment":[{"name":"arm"},{"time":1,"name":null}],"rgba":[{"color":"ffffffff"},{"time":1,"color":"ff0000ff"}]}},"ik":{"ik":[{"mix":1},{"time":1,"mix":0.5}]},"events":[{"name":"hit"},{"name":"hit"}]}}}`

func TestRich43ValidationRejectsBrokenAssetsAndTimelines(t *testing.T) {
	report, err := ValidateSpine43JSON([]byte(rich43Fixture))
	if err != nil || !report.Valid {
		t.Fatalf("valid fixture: %+v %v", report, err)
	}
	for _, test := range []struct{ from, to string }{
		{`"slots":[`, `"slots":{`},
		{`"attachment":"arm"`, `"attachment":"missing"`},
		{`"name":"arm"},{"time":1,"name":null`, `"name":"missing"},{"time":1,"name":null`},
		{`"width":64`, `"width":"64"`},
		{`"color":"ffffffff"`, `"color":"invalid"`},
		{`"rotate":[{"value":0},{"time":1,"value":30}]`, `"rotate":{"value":0}`},
		{`"rotate":[{"value":0},{"time":1,"value":30}]`, `"rotate":[3]`},
		{`"value":30`, `"angle":30`},
		{`"time":1,"value":30`, `"time":0,"value":30`},
		{`"ik":{"ik":[`, `"ik":{"missing":[`},
		{`"type":"ik"`, `"type":"unknown"`},
	} {
		data := []byte(strings.Replace(rich43Fixture, test.from, test.to, 1))
		report, err := ValidateSpine43JSON(data)
		if err == nil && report.Valid {
			t.Fatalf("invalid fixture accepted: %s", test.to)
		}
	}
}

func Test43TimelineVisitorDoesNotTreatExtensionsAsTimelines(t *testing.T) {
	var animation map[string]any
	if err := json.Unmarshal([]byte(`{"bones":{"root":{"rotate":[{"time":0,"value":1}]}},"extensions":{"time":900,"curve":[1,2,3,4]},"attachments":{"default":{"cloth":{"cloth":{"sequence":[{"time":0,"delay":0.1}]}}}}}`), &animation); err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	err := VisitAnimation43Timelines(animation, func(timeline Animation43Timeline) error { paths = append(paths, timeline.Path); return nil })
	if err != nil || len(paths) != 2 {
		t.Fatalf("paths=%v, %v", paths, err)
	}
}

func TestPathConstraint43UsesSlotReference(t *testing.T) {
	valid := `{"skeleton":{"spine":"4.3.26"},"bones":[{"name":"root"}],"slots":[{"name":"route","bone":"root"}],"constraints":[{"type":"path","name":"follow","bones":["root"],"slot":"route"}],"animations":{"follow":{"path":{"follow":{"mix":[{"mixX":0.2}]}}}}}`
	report, err := ValidateSpine43JSON([]byte(valid))
	if err != nil || !report.Valid {
		t.Fatalf("valid path constraint: %+v %v", report, err)
	}
	for _, data := range []string{strings.Replace(valid, `"slot":"route"`, `"target":"route"`, 1), strings.Replace(valid, `"slot":"route"`, `"slot":"missing"`, 1)} {
		report, err := ValidateSpine43JSON([]byte(data))
		if err == nil && report.Valid {
			t.Fatal("invalid path slot accepted")
		}
	}
}

func TestDiscreteAttachmentKeysMayShareATime(t *testing.T) {
	data := strings.Replace(rich43Fixture, `{"time":1,"name":null}`, `{"time":0,"name":null}`, 1)
	report, err := ValidateSpine43JSON([]byte(data))
	if err != nil || !report.Valid {
		t.Fatalf("official discrete key pattern rejected: %+v %v", report, err)
	}
}
