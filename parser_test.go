package spineparser

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestInspectOfficialProProject(t *testing.T) {
	source := fixture(t, "coin-pro.spine")
	result, err := InspectProject(source, InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.SpineVersion != "4.0.07" {
		t.Fatalf("version = %q", result.SpineVersion)
	}
	if result.UncompressedBytes != 11399 {
		t.Fatalf("uncompressed bytes = %d", result.UncompressedBytes)
	}
	if Detect(source) != FileProject {
		t.Fatalf("kind = %q", Detect(source))
	}
	if !contains(result.Strings, "coin-front-shine-logo") {
		t.Fatal("expected project string not found")
	}
}

func TestProjectBinaryRoundTrip(t *testing.T) {
	source := fixture(t, "coin-pro.spine")
	document, err := DeserializeProject(source, InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := SerializeProject(document, ProjectSerializeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := DeserializeProject(serialized, InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(document.Payload, roundTrip.Payload) {
		t.Fatal("project payload changed during round trip")
	}
	if roundTrip.Inspection.SpineVersion != "4.0.07" {
		t.Fatalf("version = %q", roundTrip.Inspection.SpineVersion)
	}

	binaryValue, err := document.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var unmarshaled ProjectDocument
	if err := unmarshaled.UnmarshalBinary(binaryValue); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(document.Payload, unmarshaled.Payload) {
		t.Fatal("BinaryMarshaler round trip changed payload")
	}
}

func TestInspectOfficialSkeletonBinary(t *testing.T) {
	source := fixture(t, "coin-pro.skel")
	result, err := InspectSkeletonBinary(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.SpineVersion != "4.2.22" {
		t.Fatalf("version = %q", result.SpineVersion)
	}
	if result.Hash != "7caafe7dee2b2849" {
		t.Fatalf("hash = %q", result.Hash)
	}
	if result.ReferenceScale == nil || *result.ReferenceScale != 100 {
		t.Fatalf("reference scale = %v", result.ReferenceScale)
	}
}

func TestSkeletonBinaryRoundTrip(t *testing.T) {
	source := fixture(t, "coin-pro.skel")
	document, err := DeserializeSkeletonBinary(source)
	if err != nil {
		t.Fatal(err)
	}
	serialized, err := SerializeSkeletonBinary(document)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, serialized) {
		t.Fatal("skeleton binary changed during lossless round trip")
	}

	document.Header.Width = 321
	changed, err := document.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var reparsed SkeletonBinaryDocument
	if err := reparsed.UnmarshalBinary(changed); err != nil {
		t.Fatal(err)
	}
	if reparsed.Header.Width != 321 {
		t.Fatalf("width = %v", reparsed.Header.Width)
	}
	if !bytes.Equal(document.Payload, reparsed.Payload) {
		t.Fatal("skeleton payload changed while rewriting header")
	}
}

func TestInspectLimit(t *testing.T) {
	_, err := InspectProject(fixture(t, "coin-pro.spine"), InspectOptions{
		MaxUncompressedBytes: 100,
	})
	var parseErr *ParseError
	if !errors.As(err, &parseErr) || parseErr.Code != ErrLimitExceeded {
		t.Fatalf("error = %#v", err)
	}
}

func TestInspectFileKeepsDiagnostics(t *testing.T) {
	result, err := InspectFile(
		filepath.Join("testdata", "coin-pro.spine"),
		InspectFileOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(result.OutputDirectory)

	content, err := os.ReadFile(result.Artifacts.StringsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "coin-front-shine-logo") {
		t.Fatal("diagnostic strings missing expected value")
	}
	info, err := os.Stat(result.Artifacts.DecodedBinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 11399 {
		t.Fatalf("decoded size = %d", info.Size())
	}
}

func TestExportFailureKeepsDiagnostics(t *testing.T) {
	output := t.TempDir()
	_, err := ExportProject(
		context.Background(),
		filepath.Join("testdata", "coin-pro.spine"),
		ExportOptions{
			InspectFileOptions: InspectFileOptions{OutputDirectory: output},
			Executable:         "__missing_spine_executable__",
		},
	)
	if err == nil || !strings.Contains(err.Error(), "diagnostics kept at") {
		t.Fatalf("error = %v", err)
	}
	content, readErr := os.ReadFile(filepath.Join(
		output,
		"diagnostics",
		"coin-pro.spine-cli.log",
	))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(content), "# error") {
		t.Fatal("CLI failure log missing error")
	}
}

func TestIntegrationExportOfficialProProject(t *testing.T) {
	if os.Getenv("SPINE_INTEGRATION") != "1" {
		t.Skip("set SPINE_INTEGRATION=1 to use the locally licensed Spine CLI")
	}
	result, err := ExportProject(
		context.Background(),
		filepath.Join("testdata", "coin-pro.spine"),
		ExportOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Documents) != 1 || len(result.Documents[0].Data.Bones) != 7 {
		t.Fatalf("documents = %#v", result.Documents)
	}
	imported, err := ImportProject(
		context.Background(),
		result.Documents[0].Data,
		filepath.Join(t.TempDir(), "coin-roundtrip.spine"),
		ImportOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	reexported, err := ExportProject(
		context.Background(),
		imported.ProjectPath,
		ExportOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(reexported.Documents) != 1 ||
		len(reexported.Documents[0].Data.Bones) != 7 {
		t.Fatalf("reexported documents = %#v", reexported.Documents)
	}
	t.Logf(
		"export diagnostics: %s; import diagnostics: %s; re-export diagnostics: %s",
		result.OutputDirectory,
		imported.OutputDirectory,
		reexported.OutputDirectory,
	)
}

func TestParseJSON(t *testing.T) {
	result, err := DeserializeJSON([]byte(`{
		"skeleton":{"spine":"4.2.0","customSkeleton":true},
		"bones":[{"name":"root","rotation":12,"customBone":"kept"}],
		"animations":{"idle":{}},
		"customRoot":{"value":7}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Skeleton == nil || result.Skeleton.Spine != "4.2.0" {
		t.Fatalf("skeleton = %#v", result.Skeleton)
	}
	if len(result.Bones) != 1 || result.Bones[0].Name != "root" {
		t.Fatalf("bones = %#v", result.Bones)
	}
	result.Bones[0].Name = "renamed-root"
	serialized, err := SerializeJSON(result, JSONSerializeOptions{Indent: "  "})
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := DeserializeJSON(serialized)
	if err != nil {
		t.Fatal(err)
	}
	if roundTrip.Bones[0].Name != "renamed-root" ||
		roundTrip.Bones[0].Data["customBone"] != "kept" {
		t.Fatalf("round-trip bone = %#v", roundTrip.Bones[0])
	}
	if _, exists := roundTrip.Raw["customRoot"]; !exists {
		t.Fatal("unknown root field was lost")
	}
	if _, exists := roundTrip.Skeleton.Raw["customSkeleton"]; !exists {
		t.Fatal("unknown skeleton field was lost")
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
