package spineparser

import (
	"bytes"
	"compress/flate"
	"encoding"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

var (
	_ encoding.BinaryMarshaler   = (*ProjectDocument)(nil)
	_ encoding.BinaryUnmarshaler = (*ProjectDocument)(nil)
	_ encoding.BinaryMarshaler   = (*SkeletonBinaryDocument)(nil)
	_ encoding.BinaryUnmarshaler = (*SkeletonBinaryDocument)(nil)
)

// ProjectDocument is the lossless decompressed payload of a private .spine file.
//
// The payload is retained losslessly. Direct, bounded animation edits are
// available through PatchProjectAnimationFloat32.
type ProjectDocument struct {
	Inspection ProjectInspection `json:"inspection"`
	Payload    []byte            `json:"payload"`
}

// ProjectSerializeOptions controls the raw-DEFLATE encoder.
type ProjectSerializeOptions struct {
	// CompressionLevel accepts compress/flate levels. Nil uses DefaultCompression.
	CompressionLevel *int
}

// DeserializeProject decodes a .spine envelope without losing private payload bytes.
func DeserializeProject(source []byte, options InspectOptions) (*ProjectDocument, error) {
	inspection, err := InspectProject(source, options)
	if err != nil {
		return nil, err
	}
	payload, err := DecodeProject(source, options)
	if err != nil {
		return nil, err
	}
	return &ProjectDocument{
		Inspection: inspection,
		Payload:    payload,
	}, nil
}

// SerializeProject encodes an opaque project payload as a .spine raw-DEFLATE stream.
func SerializeProject(document *ProjectDocument, options ProjectSerializeOptions) ([]byte, error) {
	if document == nil || len(document.Payload) == 0 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	level := flate.DefaultCompression
	if options.CompressionLevel != nil {
		level = *options.CompressionLevel
	}
	var output bytes.Buffer
	writer, err := flate.NewWriter(&output, level)
	if err != nil {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "invalid DEFLATE compression level", Cause: err}
	}
	if _, err := writer.Write(document.Payload); err != nil {
		writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (d *ProjectDocument) MarshalBinary() ([]byte, error) {
	return SerializeProject(d, ProjectSerializeOptions{})
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (d *ProjectDocument) UnmarshalBinary(source []byte) error {
	document, err := DeserializeProject(source, InspectOptions{})
	if err != nil {
		return err
	}
	*d = *document
	return nil
}

// SkeletonBinaryFormat identifies an exported .skel header generation.
type SkeletonBinaryFormat string

const (
	SkeletonBinaryModern SkeletonBinaryFormat = "modern"
	SkeletonBinaryLegacy SkeletonBinaryFormat = "legacy"
)

// SkeletonBinaryDocument retains the parsed header and untouched binary payload.
type SkeletonBinaryDocument struct {
	Format  SkeletonBinaryFormat     `json:"format"`
	Header  SkeletonBinaryInspection `json:"header"`
	Payload []byte                   `json:"payload"`
}

func deserializeModernSkeleton(source []byte) (*SkeletonBinaryDocument, error) {
	if len(source) < 8 {
		return nil, &ParseError{Code: ErrInvalidSkel, Msg: "skeleton binary is too short"}
	}
	reader := skeletonReader{data: source, offset: 8}
	version, err := reader.string()
	if err != nil || !versionPattern.MatchString(version) {
		return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid skeleton version", Cause: err}
	}
	values := make([]float32, 5)
	for index := range values {
		values[index], err = reader.float32()
		if err != nil {
			return nil, err
		}
	}
	nonessential, err := reader.byte()
	if err != nil {
		return nil, err
	}
	hash := ""
	if !bytes.Equal(source[:8], make([]byte, 8)) {
		hash = hex.EncodeToString(source[:8])
	}
	referenceScale := values[4]
	return &SkeletonBinaryDocument{
		Format: SkeletonBinaryModern,
		Header: SkeletonBinaryInspection{
			Kind:           FileSkeletonBinary,
			Hash:           hash,
			SpineVersion:   version,
			X:              values[0],
			Y:              values[1],
			Width:          values[2],
			Height:         values[3],
			ReferenceScale: &referenceScale,
			Nonessential:   nonessential != 0,
		},
		Payload: append([]byte(nil), source[reader.offset:]...),
	}, nil
}

func deserializeLegacySkeleton(source []byte) (*SkeletonBinaryDocument, error) {
	reader := skeletonReader{data: source}
	hash, err := reader.string()
	if err != nil {
		return nil, err
	}
	version, err := reader.string()
	if err != nil || !versionPattern.MatchString(version) {
		return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid skeleton version", Cause: err}
	}
	values := make([]float32, 4)
	for index := range values {
		values[index], err = reader.float32()
		if err != nil {
			return nil, err
		}
	}
	nonessential, err := reader.byte()
	if err != nil {
		return nil, err
	}
	return &SkeletonBinaryDocument{
		Format: SkeletonBinaryLegacy,
		Header: SkeletonBinaryInspection{
			Kind:         FileSkeletonBinary,
			Hash:         hash,
			SpineVersion: version,
			X:            values[0],
			Y:            values[1],
			Width:        values[2],
			Height:       values[3],
			Nonessential: nonessential != 0,
		},
		Payload: append([]byte(nil), source[reader.offset:]...),
	}, nil
}

// DeserializeSkeletonBinary parses a .skel header and retains the remaining payload.
func DeserializeSkeletonBinary(source []byte) (*SkeletonBinaryDocument, error) {
	if document, err := deserializeModernSkeleton(source); err == nil {
		return document, nil
	}
	if document, err := deserializeLegacySkeleton(source); err == nil {
		return document, nil
	}
	return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid Spine skeleton binary"}
}

func writeVarint(output *bytes.Buffer, value uint32) {
	for {
		current := byte(value & 0x7f)
		value >>= 7
		if value == 0 {
			output.WriteByte(current)
			return
		}
		output.WriteByte(current | 0x80)
	}
}

func writeSkeletonString(output *bytes.Buffer, value string) {
	if value == "" {
		writeVarint(output, 1)
		return
	}
	writeVarint(output, uint32(len([]byte(value))+1))
	output.WriteString(value)
}

func writeFloat32(output *bytes.Buffer, value float32) {
	var buffer [4]byte
	binary.BigEndian.PutUint32(buffer[:], math.Float32bits(value))
	output.Write(buffer[:])
}

// SerializeSkeletonBinary rewrites a .skel header and appends the untouched payload.
func SerializeSkeletonBinary(document *SkeletonBinaryDocument) ([]byte, error) {
	if document == nil {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "skeleton document is nil"}
	}
	if !versionPattern.MatchString(document.Header.SpineVersion) {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "invalid skeleton Spine version"}
	}

	var output bytes.Buffer
	switch document.Format {
	case SkeletonBinaryModern:
		hash := make([]byte, 8)
		if document.Header.Hash != "" {
			decoded, err := hex.DecodeString(document.Header.Hash)
			if err != nil || len(decoded) != 8 {
				return nil, &ParseError{
					Code:  ErrInvalidInput,
					Msg:   "modern skeleton hash must be 16 hexadecimal characters",
					Cause: err,
				}
			}
			copy(hash, decoded)
		}
		if document.Header.ReferenceScale == nil {
			return nil, &ParseError{Code: ErrInvalidInput, Msg: "modern skeleton reference scale is required"}
		}
		output.Write(hash)
		writeSkeletonString(&output, document.Header.SpineVersion)
		writeFloat32(&output, document.Header.X)
		writeFloat32(&output, document.Header.Y)
		writeFloat32(&output, document.Header.Width)
		writeFloat32(&output, document.Header.Height)
		writeFloat32(&output, *document.Header.ReferenceScale)
	case SkeletonBinaryLegacy:
		writeSkeletonString(&output, document.Header.Hash)
		writeSkeletonString(&output, document.Header.SpineVersion)
		writeFloat32(&output, document.Header.X)
		writeFloat32(&output, document.Header.Y)
		writeFloat32(&output, document.Header.Width)
		writeFloat32(&output, document.Header.Height)
	default:
		return nil, fmt.Errorf("unsupported skeleton binary format %q", document.Format)
	}
	if document.Header.Nonessential {
		output.WriteByte(1)
	} else {
		output.WriteByte(0)
	}
	output.Write(document.Payload)
	return output.Bytes(), nil
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (d *SkeletonBinaryDocument) MarshalBinary() ([]byte, error) {
	return SerializeSkeletonBinary(d)
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (d *SkeletonBinaryDocument) UnmarshalBinary(source []byte) error {
	document, err := DeserializeSkeletonBinary(source)
	if err != nil {
		return err
	}
	*d = *document
	return nil
}
