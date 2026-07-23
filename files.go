package spineparser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// InspectFileOptions controls filesystem diagnostics.
type InspectFileOptions struct {
	InspectOptions
	OutputDirectory   string
	OmitDecodedBinary bool
}

// DiagnosticArtifacts are human-readable and binary troubleshooting files.
type DiagnosticArtifacts struct {
	Directory         string `json:"directory"`
	InspectionPath    string `json:"inspectionPath"`
	StringsPath       string `json:"stringsPath"`
	DecodedBinaryPath string `json:"decodedBinaryPath,omitempty"`
}

// InspectFileResult is a project inspection plus kept diagnostic files.
type InspectFileResult struct {
	Inspection      ProjectInspection   `json:"inspection"`
	OutputDirectory string              `json:"outputDirectory"`
	Artifacts       DiagnosticArtifacts `json:"artifacts"`
}

func prepareOutputDirectory(requested string) (string, error) {
	if requested == "" {
		return os.MkdirTemp("", "spine233-file-parser-")
	}
	absolute, err := filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return "", err
	}
	return absolute, nil
}

func writeInspectionArtifacts(
	projectPath string,
	outputDirectory string,
	inspection ProjectInspection,
	source []byte,
	options InspectFileOptions,
) (DiagnosticArtifacts, error) {
	directory := filepath.Join(outputDirectory, "diagnostics")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return DiagnosticArtifacts{}, err
	}
	stem := strings.TrimSuffix(filepath.Base(projectPath), filepath.Ext(projectPath))
	inspectionPath := filepath.Join(directory, stem+".inspection.json")
	stringsPath := filepath.Join(directory, stem+".strings.txt")
	decodedPath := ""
	if !options.OmitDecodedBinary {
		decodedPath = filepath.Join(directory, stem+".decoded.bin")
	}

	payload, err := json.MarshalIndent(struct {
		SourcePath  string            `json:"sourcePath"`
		GeneratedAt time.Time         `json:"generatedAt"`
		Inspection  ProjectInspection `json:"inspection"`
	}{
		SourcePath:  projectPath,
		GeneratedAt: time.Now().UTC(),
		Inspection:  inspection,
	}, "", "  ")
	if err != nil {
		return DiagnosticArtifacts{}, err
	}
	payload = append(payload, '\n')

	if err := os.WriteFile(inspectionPath, payload, 0o644); err != nil {
		return DiagnosticArtifacts{}, err
	}
	stringsPayload := ""
	if len(inspection.Strings) > 0 {
		stringsPayload = strings.Join(inspection.Strings, "\n") + "\n"
	}
	if err := os.WriteFile(stringsPath, []byte(stringsPayload), 0o644); err != nil {
		return DiagnosticArtifacts{}, err
	}
	if decodedPath != "" {
		decoded, err := DecodeProject(source, options.InspectOptions)
		if err != nil {
			return DiagnosticArtifacts{}, err
		}
		if err := os.WriteFile(decodedPath, decoded, 0o644); err != nil {
			return DiagnosticArtifacts{}, err
		}
	}

	return DiagnosticArtifacts{
		Directory:         directory,
		InspectionPath:    inspectionPath,
		StringsPath:       stringsPath,
		DecodedBinaryPath: decodedPath,
	}, nil
}

// InspectFile reads a .spine file and keeps diagnostics in a unique temp directory.
func InspectFile(projectPath string, options InspectFileOptions) (*InspectFileResult, error) {
	absolute, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, err
	}
	source, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	inspection, err := InspectProject(source, options.InspectOptions)
	if err != nil {
		return nil, err
	}
	outputDirectory, err := prepareOutputDirectory(options.OutputDirectory)
	if err != nil {
		return nil, err
	}
	artifacts, err := writeInspectionArtifacts(absolute, outputDirectory, inspection, source, options)
	if err != nil {
		return nil, err
	}
	return &InspectFileResult{
		Inspection:      inspection,
		OutputDirectory: outputDirectory,
		Artifacts:       artifacts,
	}, nil
}
