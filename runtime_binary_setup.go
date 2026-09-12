package spineparser

import (
	"encoding/binary"
	"fmt"
	"math"
)

// RuntimeBinaryBone is one setup-pose bone decoded from Spine 4.3 binary data.
type RuntimeBinaryBone struct {
	Name         string  `json:"name"`
	Parent       string  `json:"parent,omitempty"`
	ParentIndex  int     `json:"parentIndex"`
	Rotation     float32 `json:"rotation"`
	X            float32 `json:"x"`
	Y            float32 `json:"y"`
	ScaleX       float32 `json:"scaleX"`
	ScaleY       float32 `json:"scaleY"`
	ShearX       float32 `json:"shearX"`
	ShearY       float32 `json:"shearY"`
	Inherit      byte    `json:"inherit"`
	Length       float32 `json:"length"`
	SkinRequired bool    `json:"skinRequired"`
}

// RuntimeBinarySlot is one setup-pose slot decoded from Spine 4.3 binary data.
type RuntimeBinarySlot struct {
	Name       string `json:"name"`
	Bone       string `json:"bone"`
	BoneIndex  int    `json:"boneIndex"`
	Color      uint32 `json:"color"`
	DarkColor  int32  `json:"darkColor"`
	Attachment string `json:"attachment,omitempty"`
	Blend      int    `json:"blend"`
}

// RuntimeBinarySetup contains the format prefix needed to compare a private
// project object graph with its official 4.3 runtime export.
type RuntimeBinarySetup struct {
	Header          SkeletonBinaryInspection `json:"header"`
	Strings         []string                 `json:"strings"`
	Bones           []RuntimeBinaryBone      `json:"bones"`
	Slots           []RuntimeBinarySlot      `json:"slots"`
	RemainingOffset int                      `json:"remainingOffset"`
}

type runtimeBinaryReader struct {
	data    []byte
	offset  int
	strings []string
}

func (reader *runtimeBinaryReader) require(count int) error {
	if count < 0 || reader.offset+count > len(reader.data) {
		return &ParseError{
			Code: ErrInvalidSkel,
			Msg:  "unexpected end of Spine runtime binary",
		}
	}
	return nil
}

func (reader *runtimeBinaryReader) readByte() (byte, error) {
	if err := reader.require(1); err != nil {
		return 0, err
	}
	value := reader.data[reader.offset]
	reader.offset++
	return value, nil
}

func (reader *runtimeBinaryReader) readBoolean() (bool, error) {
	value, err := reader.readByte()
	return value != 0, err
}

func (reader *runtimeBinaryReader) readVarint() (int, error) {
	result := 0
	for shift := uint(0); shift < 35; shift += 7 {
		value, err := reader.readByte()
		if err != nil {
			return 0, err
		}
		result |= int(value&0x7f) << shift
		if value&0x80 == 0 {
			return result, nil
		}
	}
	return 0, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime varint"}
}

func (reader *runtimeBinaryReader) readInt32() (int32, error) {
	if err := reader.require(4); err != nil {
		return 0, err
	}
	value := int32(binary.BigEndian.Uint32(reader.data[reader.offset:]))
	reader.offset += 4
	return value, nil
}

func (reader *runtimeBinaryReader) readUint64() (uint64, error) {
	if err := reader.require(8); err != nil {
		return 0, err
	}
	value := binary.BigEndian.Uint64(reader.data[reader.offset:])
	reader.offset += 8
	return value, nil
}

func (reader *runtimeBinaryReader) readFloat32() (float32, error) {
	value, err := reader.readInt32()
	if err != nil {
		return 0, err
	}
	return math.Float32frombits(uint32(value)), nil
}

func (reader *runtimeBinaryReader) readString() (string, error) {
	encodedLength, err := reader.readVarint()
	if err != nil {
		return "", err
	}
	if encodedLength == 0 || encodedLength == 1 {
		return "", nil
	}
	length := encodedLength - 1
	if err := reader.require(length); err != nil {
		return "", err
	}
	value := string(reader.data[reader.offset : reader.offset+length])
	reader.offset += length
	return value, nil
}

func (reader *runtimeBinaryReader) readStringRef() (string, error) {
	index, err := reader.readVarint()
	if err != nil || index == 0 {
		return "", err
	}
	index--
	if index < 0 || index >= len(reader.strings) {
		return "", &ParseError{
			Code: ErrInvalidSkel,
			Msg:  fmt.Sprintf("runtime string reference %d is out of range", index+1),
		}
	}
	return reader.strings[index], nil
}

// ParseSkeletonBinarySetup decodes header, string table, bones, and slots from
// Spine 4.3 runtime binary. It is intentionally bounded and fails closed.
func ParseSkeletonBinarySetup(source []byte) (*RuntimeBinarySetup, error) {
	reader := runtimeBinaryReader{data: source}
	hash, err := reader.readUint64()
	if err != nil {
		return nil, err
	}
	version, err := reader.readString()
	if err != nil || !versionPattern.MatchString(version) {
		return nil, &ParseError{
			Code:  ErrInvalidSkel,
			Msg:   "invalid Spine runtime binary version",
			Cause: err,
		}
	}
	values := make([]float32, 5)
	for index := range values {
		values[index], err = reader.readFloat32()
		if err != nil {
			return nil, err
		}
	}
	nonessential, err := reader.readBoolean()
	if err != nil {
		return nil, err
	}
	if nonessential {
		if _, err = reader.readFloat32(); err != nil {
			return nil, err
		}
		if _, err = reader.readString(); err != nil {
			return nil, err
		}
		if _, err = reader.readString(); err != nil {
			return nil, err
		}
	}

	stringCount, err := reader.readVarint()
	if err != nil || stringCount < 0 || stringCount > defaultMaxStrings {
		return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime string count", Cause: err}
	}
	reader.strings = make([]string, stringCount)
	for index := range reader.strings {
		reader.strings[index], err = reader.readString()
		if err != nil {
			return nil, err
		}
	}

	boneCount, err := reader.readVarint()
	if err != nil || boneCount < 1 || boneCount > 100_000 {
		return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime bone count", Cause: err}
	}
	bones := make([]RuntimeBinaryBone, boneCount)
	for index := range bones {
		name, readErr := reader.readString()
		if readErr != nil || name == "" {
			return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime bone name", Cause: readErr}
		}
		parentIndex := -1
		parent := ""
		if index > 0 {
			parentIndex, readErr = reader.readVarint()
			if readErr != nil || parentIndex < 0 || parentIndex >= index {
				return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime bone parent", Cause: readErr}
			}
			parent = bones[parentIndex].Name
		}
		bone := RuntimeBinaryBone{Name: name, Parent: parent, ParentIndex: parentIndex}
		targets := []*float32{
			&bone.Rotation, &bone.X, &bone.Y, &bone.ScaleX,
			&bone.ScaleY, &bone.ShearX, &bone.ShearY,
		}
		for _, target := range targets {
			*target, readErr = reader.readFloat32()
			if readErr != nil {
				return nil, readErr
			}
		}
		bone.Inherit, readErr = reader.readByte()
		if readErr != nil {
			return nil, readErr
		}
		bone.Length, readErr = reader.readFloat32()
		if readErr != nil {
			return nil, readErr
		}
		bone.SkinRequired, readErr = reader.readBoolean()
		if readErr != nil {
			return nil, readErr
		}
		if nonessential {
			if _, readErr = reader.readInt32(); readErr != nil {
				return nil, readErr
			}
			if _, readErr = reader.readString(); readErr != nil {
				return nil, readErr
			}
			for field := 0; field < 2; field++ {
				if _, readErr = reader.readFloat32(); readErr != nil {
					return nil, readErr
				}
			}
			if _, readErr = reader.readBoolean(); readErr != nil {
				return nil, readErr
			}
		}
		bones[index] = bone
	}

	slotCount, err := reader.readVarint()
	if err != nil || slotCount < 0 || slotCount > 100_000 {
		return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime slot count", Cause: err}
	}
	slots := make([]RuntimeBinarySlot, slotCount)
	for index := range slots {
		name, readErr := reader.readString()
		if readErr != nil || name == "" {
			return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime slot name", Cause: readErr}
		}
		boneIndex, readErr := reader.readVarint()
		if readErr != nil || boneIndex < 0 || boneIndex >= len(bones) {
			return nil, &ParseError{Code: ErrInvalidSkel, Msg: "invalid runtime slot bone", Cause: readErr}
		}
		color, readErr := reader.readInt32()
		if readErr != nil {
			return nil, readErr
		}
		darkColor, readErr := reader.readInt32()
		if readErr != nil {
			return nil, readErr
		}
		attachment, readErr := reader.readStringRef()
		if readErr != nil {
			return nil, readErr
		}
		blend, readErr := reader.readVarint()
		if readErr != nil {
			return nil, readErr
		}
		if nonessential {
			if _, readErr = reader.readBoolean(); readErr != nil {
				return nil, readErr
			}
		}
		slots[index] = RuntimeBinarySlot{
			Name:       name,
			Bone:       bones[boneIndex].Name,
			BoneIndex:  boneIndex,
			Color:      uint32(color),
			DarkColor:  darkColor,
			Attachment: attachment,
			Blend:      blend,
		}
	}

	referenceScale := values[4]
	return &RuntimeBinarySetup{
		Header: SkeletonBinaryInspection{
			Kind:           FileSkeletonBinary,
			Hash:           fmt.Sprintf("%016x", hash),
			SpineVersion:   version,
			X:              values[0],
			Y:              values[1],
			Width:          values[2],
			Height:         values[3],
			ReferenceScale: &referenceScale,
			Nonessential:   nonessential,
		},
		Strings:         append([]string(nil), reader.strings...),
		Bones:           bones,
		Slots:           slots,
		RemainingOffset: reader.offset,
	}, nil
}
