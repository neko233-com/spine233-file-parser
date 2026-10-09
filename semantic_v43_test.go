package spineparser

import (
	"strings"
	"testing"
)

func TestValidateSpine43JSON(t *testing.T) {
	valid := `{"skeleton":{"spine":"4.3.26"},"bones":[{"name":"root"},{"name":"body","parent":"root"}],"slots":[{"name":"body","bone":"body"}],"constraints":[{"name":"ik","type":"ik","bones":["body"],"target":"root"}],"events":{"step":{}},"animations":{"idle":{"bones":{"body":{"scale":[{"x":1,"y":1,"curve":[0.1,1,0.2,1,0.1,1,0.2,1]},{"time":1,"x":1,"y":1}]}},"events":[{"time":0.5,"name":"step"}]}}}`
	report, err := ValidateSpine43JSON([]byte(valid))
	if err != nil || !report.Valid {
		t.Fatalf("valid input: %+v %v", report, err)
	}
	analysis, err := AnalyzeJSON([]byte(valid))
	if err != nil || analysis.Counts["ikConstraints"] != 1 {
		t.Fatalf("4.3 unified constraint inventory: %+v, %v", analysis, err)
	}
	for _, test := range []struct{ old, replacement string }{
		{`"4.3.26"`, `"4.2.43"`},
		{`"parent":"root"`, `"parent":"body"`},
		{`"target":"root"`, `"target":"missing"`},
		{`"time":1`, `"time":-1`},
		{`"time":1`, `"time":"1"`},
		{`0.1,1,0.2,1,0.1,1,0.2,1`, `0.1,1,0.2,1`},
		{`"name":"step"`, `"name":"missing"`},
	} {
		report, err := ValidateSpine43JSON([]byte(strings.Replace(valid, test.old, test.replacement, 1)))
		if err != nil || report.Valid {
			t.Fatalf("expected validation failure for %s: %+v %v", test.replacement, report, err)
		}
	}
}

func TestValidateSpine43OrderedKeysAndSlotOwners(t *testing.T) {
	report, err := ValidateSpine43JSON([]byte(`{"skeleton":{"spine":"4.3"},"bones":[{"name":"root"}],"animations":{"idle":{"slots":{"missing":{"attachment":[{"time":1},{"time":0}]}}}}}`))
	if err != nil || report.Valid || len(report.Issues) != 2 {
		t.Fatalf("report = %+v, %v", report, err)
	}
}
