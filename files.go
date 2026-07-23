package spineparser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
	CLILogPath        string `json:"cliLogPath,omitempty"`
}

// InspectFileResult is a project inspection plus kept diagnostic files.
type InspectFileResult struct {
	Inspection      ProjectInspection   `json:"inspection"`
	OutputDirectory string              `json:"outputDirectory"`
	Artifacts       DiagnosticArtifacts `json:"artifacts"`
}

func prepareOutputDirectory(requested string) (string, error) {
	if requested == "" {
		return os.MkdirTemp("", "spine-file-parser-")
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

// ExportOptions controls official Spine CLI conversion.
type ExportOptions struct {
	InspectFileOptions
	Executable     string
	ExportSettings string
	EditorVersion  string
	Timeout        time.Duration
}

// ExportedDocument is one skeleton JSON output.
type ExportedDocument struct {
	FileName string     `json:"fileName"`
	Path     string     `json:"path"`
	Data     *SpineJSON `json:"data"`
}

// ExportResult contains complete parsed data and kept diagnostic paths.
type ExportResult struct {
	Inspection      ProjectInspection   `json:"inspection"`
	Documents       []ExportedDocument  `json:"documents"`
	OutputDirectory string              `json:"outputDirectory"`
	Artifacts       DiagnosticArtifacts `json:"artifacts"`
	Stdout          string              `json:"stdout"`
	Stderr          string              `json:"stderr"`
}

func spineExecutable(requested string) string {
	if requested != "" {
		return requested
	}
	if configured := os.Getenv("SPINE_EXECUTABLE"); configured != "" {
		return configured
	}
	if runtime.GOOS == "windows" {
		return "Spine.com"
	}
	return "Spine"
}

// ExportProject uses the licensed official Spine CLI for complete Pro data.
func ExportProject(ctx context.Context, projectPath string, options ExportOptions) (*ExportResult, error) {
	if options.Timeout == 0 {
		options.Timeout = 2 * time.Minute
	}
	if options.Timeout < 0 {
		return nil, errors.New("Timeout must be positive")
	}
	runContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	inspected, err := InspectFile(projectPath, options.InspectFileOptions)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, err
	}
	stem := strings.TrimSuffix(filepath.Base(absolute), filepath.Ext(absolute))
	cliLogPath := filepath.Join(inspected.Artifacts.Directory, stem+".spine-cli.log")
	artifacts := inspected.Artifacts
	artifacts.CLILogPath = cliLogPath

	executable := spineExecutable(options.Executable)
	exportSettings := options.ExportSettings
	if exportSettings == "" {
		exportSettings = "json"
	} else {
		exportSettings, err = filepath.Abs(exportSettings)
		if err != nil {
			return nil, err
		}
	}

	args := []string{"--hide-license"}
	if options.EditorVersion != "" {
		args = append(args, "--update", options.EditorVersion)
	}
	args = append(args,
		"--input", absolute,
		"--output", inspected.OutputDirectory,
		"--export", exportSettings,
	)

	command := exec.CommandContext(runContext, executable, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	logText := "# stdout\n" + strings.TrimRight(stdout.String(), "\r\n") +
		"\n\n# stderr\n" + strings.TrimRight(stderr.String(), "\r\n") + "\n"
	if runErr != nil {
		logText += "\n# error\n" + runErr.Error() + "\n"
	}
	if writeErr := os.WriteFile(cliLogPath, []byte(logText), 0o644); writeErr != nil {
		return nil, writeErr
	}
	if runErr != nil {
		return nil, fmt.Errorf(
			"Spine export failed; diagnostics kept at %s: %w",
			inspected.OutputDirectory,
			runErr,
		)
	}

	entries, err := os.ReadDir(inspected.OutputDirectory)
	if err != nil {
		return nil, err
	}
	jsonNames := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
			jsonNames = append(jsonNames, entry.Name())
		}
	}
	sort.Strings(jsonNames)
	if len(jsonNames) == 0 {
		return nil, fmt.Errorf("Spine CLI produced no JSON files for %s", filepath.Base(absolute))
	}

	documents := make([]ExportedDocument, 0, len(jsonNames))
	for _, name := range jsonNames {
		path := filepath.Join(inspected.OutputDirectory, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		parsed, err := ParseJSON(data)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		documents = append(documents, ExportedDocument{
			FileName: name,
			Path:     path,
			Data:     parsed,
		})
	}

	return &ExportResult{
		Inspection:      inspected.Inspection,
		Documents:       documents,
		OutputDirectory: inspected.OutputDirectory,
		Artifacts:       artifacts,
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
	}, nil
}

// ImportOptions controls semantic Spine JSON to .spine serialization.
type ImportOptions struct {
	InspectFileOptions
	Executable    string
	EditorVersion string
	SkeletonName  string
	Timeout       time.Duration
	JSON          JSONSerializeOptions
}

// ImportArtifacts are kept inputs, metadata, decoded data, and CLI logs.
type ImportArtifacts struct {
	DiagnosticArtifacts
	InputJSONPath string `json:"inputJsonPath"`
}

// ImportResult contains the serialized .spine project and diagnostics.
type ImportResult struct {
	ProjectPath     string            `json:"projectPath"`
	OutputDirectory string            `json:"outputDirectory"`
	Inspection      ProjectInspection `json:"inspection"`
	Artifacts       ImportArtifacts   `json:"artifacts"`
	Stdout          string            `json:"stdout"`
	Stderr          string            `json:"stderr"`
}

// ImportProject serializes semantic Spine JSON to a .spine project through the
// official licensed Spine CLI.
func ImportProject(
	ctx context.Context,
	document *SpineJSON,
	projectPath string,
	options ImportOptions,
) (*ImportResult, error) {
	if document == nil {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "Spine JSON document is nil"}
	}
	if options.Timeout == 0 {
		options.Timeout = 2 * time.Minute
	}
	if options.Timeout < 0 {
		return nil, errors.New("Timeout must be positive")
	}
	runContext, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()

	absoluteProjectPath, err := filepath.Abs(projectPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(absoluteProjectPath), 0o755); err != nil {
		return nil, err
	}
	outputDirectory, err := prepareOutputDirectory(options.OutputDirectory)
	if err != nil {
		return nil, err
	}
	diagnosticDirectory := filepath.Join(outputDirectory, "diagnostics")
	if err := os.MkdirAll(diagnosticDirectory, 0o755); err != nil {
		return nil, err
	}
	stem := strings.TrimSuffix(
		filepath.Base(absoluteProjectPath),
		filepath.Ext(absoluteProjectPath),
	)
	inputJSONPath := filepath.Join(diagnosticDirectory, stem+".import.json")
	cliLogPath := filepath.Join(diagnosticDirectory, stem+".spine-cli.log")
	serialized, err := SerializeJSON(document, options.JSON)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(inputJSONPath, serialized, 0o644); err != nil {
		return nil, err
	}

	args := []string{"--hide-license"}
	if options.EditorVersion != "" {
		args = append(args, "--update", options.EditorVersion)
	}
	args = append(
		args,
		"--input", inputJSONPath,
		"--output", absoluteProjectPath,
		"--import",
	)
	if options.SkeletonName != "" {
		args = append(args, options.SkeletonName)
	}

	command := exec.CommandContext(
		runContext,
		spineExecutable(options.Executable),
		args...,
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	logText := "# stdout\n" + strings.TrimRight(stdout.String(), "\r\n") +
		"\n\n# stderr\n" + strings.TrimRight(stderr.String(), "\r\n") + "\n"
	if runErr != nil {
		logText += "\n# error\n" + runErr.Error() + "\n"
	}
	if writeErr := os.WriteFile(cliLogPath, []byte(logText), 0o644); writeErr != nil {
		return nil, writeErr
	}
	if runErr != nil {
		return nil, fmt.Errorf(
			"Spine import failed; diagnostics kept at %s: %w",
			outputDirectory,
			runErr,
		)
	}

	inspected, err := InspectFile(
		absoluteProjectPath,
		InspectFileOptions{
			InspectOptions:    options.InspectOptions,
			OutputDirectory:   outputDirectory,
			OmitDecodedBinary: options.OmitDecodedBinary,
		},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"Spine project created but inspection failed; diagnostics kept at %s: %w",
			outputDirectory,
			err,
		)
	}
	artifacts := inspected.Artifacts
	artifacts.CLILogPath = cliLogPath
	return &ImportResult{
		ProjectPath:     absoluteProjectPath,
		OutputDirectory: outputDirectory,
		Inspection:      inspected.Inspection,
		Artifacts: ImportArtifacts{
			DiagnosticArtifacts: artifacts,
			InputJSONPath:       inputJSONPath,
		},
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, nil
}

// DeserializeProjectFile performs semantic .spine to JSON conversion through
// the official Spine CLI.
func DeserializeProjectFile(
	ctx context.Context,
	projectPath string,
	options ExportOptions,
) (*ExportResult, error) {
	return ExportProject(ctx, projectPath, options)
}

// SerializeProjectFile performs semantic JSON to .spine conversion through the
// official Spine CLI.
func SerializeProjectFile(
	ctx context.Context,
	document *SpineJSON,
	projectPath string,
	options ImportOptions,
) (*ImportResult, error) {
	return ImportProject(ctx, document, projectPath, options)
}
