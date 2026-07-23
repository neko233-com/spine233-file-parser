package spineparser

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode"
)

const (
	defaultMaxUncompressedBytes int64 = 256 * 1024 * 1024
	defaultMaxStrings                 = 10_000
)

var versionPattern = regexp.MustCompile(`^\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?$`)

// FileKind describes a recognized Spine file representation.
type FileKind string

const (
	FileProject        FileKind = "project"
	FileSkeletonJSON   FileKind = "skeleton-json"
	FileSkeletonBinary FileKind = "skeleton-binary"
	FileUnknown        FileKind = "unknown"
)

// InspectOptions controls project resource limits.
type InspectOptions struct {
	MaxUncompressedBytes int64
	MaxStrings           int
}

func (o InspectOptions) normalized() (InspectOptions, error) {
	if o.MaxUncompressedBytes == 0 {
		o.MaxUncompressedBytes = defaultMaxUncompressedBytes
	}
	if o.MaxStrings == 0 {
		o.MaxStrings = defaultMaxStrings
	}
	if o.MaxUncompressedBytes < 1 {
		return o, &ParseError{Code: ErrInvalidInput, Msg: "MaxUncompressedBytes must be positive"}
	}
	if o.MaxStrings < 1 {
		return o, &ParseError{Code: ErrInvalidInput, Msg: "MaxStrings must be positive"}
	}
	return o, nil
}

// ProjectInspection contains schema-independent .spine metadata.
type ProjectInspection struct {
	Kind              FileKind `json:"kind"`
	Compression       string   `json:"compression"`
	CompressedBytes   int      `json:"compressedBytes"`
	UncompressedBytes int      `json:"uncompressedBytes"`
	SpineVersion      string   `json:"spineVersion,omitempty"`
	Strings           []string `json:"strings"`
}

// SkeletonBinaryInspection contains an exported .skel header.
type SkeletonBinaryInspection struct {
	Kind           FileKind `json:"kind"`
	Hash           string   `json:"hash,omitempty"`
	SpineVersion   string   `json:"spineVersion"`
	X              float32  `json:"x"`
	Y              float32  `json:"y"`
	Width          float32  `json:"width"`
	Height         float32  `json:"height"`
	ReferenceScale *float32 `json:"referenceScale,omitempty"`
	Nonessential   bool     `json:"nonessential"`
}

func inflateRawLimited(source []byte, maxBytes int64) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(source))
	defer reader.Close()

	decoded, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, &ParseError{
			Code:  ErrInvalidProject,
			Msg:   "input is not a valid raw-DEFLATE Spine project",
			Cause: err,
		}
	}
	if int64(len(decoded)) > maxBytes {
		return nil, &ParseError{
			Code: ErrLimitExceeded,
			Msg:  fmt.Sprintf("inflated project exceeds %d bytes", maxBytes),
		}
	}
	return decoded, nil
}

// DecodeProject returns the decompressed private Spine project stream.
func DecodeProject(source []byte, options InspectOptions) ([]byte, error) {
	normalized, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if len(source) == 0 {
		return nil, &ParseError{Code: ErrInvalidProject, Msg: "Spine project is empty"}
	}
	return inflateRawLimited(source, normalized.MaxUncompressedBytes)
}

// ScanProjectStrings finds Kryo ASCII-optimized diagnostic strings.
func ScanProjectStrings(decoded []byte, maxStrings int) ([]string, error) {
	if maxStrings == 0 {
		maxStrings = defaultMaxStrings
	}
	if maxStrings < 1 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "maxStrings must be positive"}
	}

	values := make([]string, 0)
	seen := make(map[string]struct{})
	for index := 0; index < len(decoded) && len(values) < maxStrings; {
		start := index
		cursor := index
		var builder strings.Builder
		ended := false

		for cursor < len(decoded) && cursor-start < 1024 {
			value := decoded[cursor]
			character := value & 0x7f
			last := value&0x80 != 0
			if character < 0x20 || character > 0x7e {
				break
			}
			builder.WriteByte(character)
			cursor++

			if last {
				text := strings.TrimSpace(builder.String())
				letters := 0
				for _, char := range text {
					if unicode.IsLetter(char) || unicode.IsDigit(char) {
						letters++
					}
				}
				likely := len(text) >= 3 && letters >= max(2, (len(text)*4+9)/10)
				if likely {
					if _, exists := seen[text]; !exists {
						seen[text] = struct{}{}
						values = append(values, text)
					}
				}
				index = cursor
				ended = true
				break
			}
		}
		if !ended {
			index = start + 1
		}
	}
	return values, nil
}

// InspectProject parses the raw-DEFLATE envelope and diagnostic metadata.
func InspectProject(source []byte, options InspectOptions) (ProjectInspection, error) {
	normalized, err := options.normalized()
	if err != nil {
		return ProjectInspection{}, err
	}
	decoded, err := DecodeProject(source, normalized)
	if err != nil {
		return ProjectInspection{}, err
	}
	values, err := ScanProjectStrings(decoded, normalized.MaxStrings)
	if err != nil {
		return ProjectInspection{}, err
	}
	if len(decoded) < 8 || len(values) == 0 {
		return ProjectInspection{}, &ParseError{
			Code: ErrInvalidProject,
			Msg:  "raw-DEFLATE stream does not look like a Spine project",
		}
	}

	version := ""
	for _, value := range values {
		if versionPattern.MatchString(value) {
			version = value
			break
		}
	}
	return ProjectInspection{
		Kind:              FileProject,
		Compression:       "deflate-raw",
		CompressedBytes:   len(source),
		UncompressedBytes: len(decoded),
		SpineVersion:      version,
		Strings:           values,
	}, nil
}

type skeletonReader struct {
	data   []byte
	offset int
}

func (r *skeletonReader) byte() (byte, error) {
	if r.offset >= len(r.data) {
		return 0, &ParseError{Code: ErrInvalidSkel, Msg: "unexpected end of skeleton header"}
	}
	value := r.data[r.offset]
	r.offset++
	return value, nil
}

func (r *skeletonReader) varint() (uint32, error) {
	var result uint32
	for shift := uint(0); shift < 35; shift += 7 {
		value, err := r.byte()
		if err != nil {
			return 0, err
		}
		result |= uint32(value&0x7f) << shift
		if value&0x80 == 0 {
			return result, nil
		}
	}
	return 0, &ParseError{Code: ErrInvalidSkel, Msg: "invalid skeleton varint"}
}

func (r *skeletonReader) string() (string, error) {
	encodedLength, err := r.varint()
	if err != nil {
		return "", err
	}
	if encodedLength == 0 || encodedLength == 1 {
		return "", nil
	}
	length := int(encodedLength - 1)
	end := r.offset + length
	if end > len(r.data) {
		return "", &ParseError{Code: ErrInvalidSkel, Msg: "unexpected end of skeleton string"}
	}
	value := string(r.data[r.offset:end])
	r.offset = end
	return value, nil
}

func (r *skeletonReader) float32() (float32, error) {
	end := r.offset + 4
	if end > len(r.data) {
		return 0, &ParseError{Code: ErrInvalidSkel, Msg: "unexpected end of skeleton float"}
	}
	value := math.Float32frombits(binary.BigEndian.Uint32(r.data[r.offset:end]))
	r.offset = end
	return value, nil
}

// InspectSkeletonBinary parses current and legacy exported .skel headers.
func InspectSkeletonBinary(source []byte) (SkeletonBinaryInspection, error) {
	document, err := DeserializeSkeletonBinary(source)
	if err != nil {
		return SkeletonBinaryInspection{}, err
	}
	return document.Header, nil
}

// Detect identifies project, exported JSON, and exported binary files.
func Detect(source []byte) FileKind {
	trimmed := bytes.TrimSpace(source)
	if len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed) {
		return FileSkeletonJSON
	}
	if _, err := InspectProject(source, InspectOptions{
		MaxStrings: 1,
	}); err == nil {
		return FileProject
	}
	if result, err := InspectSkeletonBinary(source); err == nil && result.SpineVersion != "" {
		return FileSkeletonBinary
	}
	return FileUnknown
}
