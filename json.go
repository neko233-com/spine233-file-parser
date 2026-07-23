package spineparser

import (
	"encoding/json"
)

// SkeletonInfo is the exported Spine JSON metadata block.
type SkeletonInfo struct {
	Hash   string                     `json:"hash,omitempty"`
	Spine  string                     `json:"spine,omitempty"`
	X      float64                    `json:"x,omitempty"`
	Y      float64                    `json:"y,omitempty"`
	Width  float64                    `json:"width,omitempty"`
	Height float64                    `json:"height,omitempty"`
	FPS    float64                    `json:"fps,omitempty"`
	Images string                     `json:"images,omitempty"`
	Audio  string                     `json:"audio,omitempty"`
	Raw    map[string]json.RawMessage `json:"-"`
}

// Bone is an exported skeleton bone. Data retains all version-specific fields.
type Bone struct {
	Name   string         `json:"name"`
	Parent string         `json:"parent,omitempty"`
	Data   map[string]any `json:"-"`
}

// Slot is an exported skeleton slot. Data retains all version-specific fields.
type Slot struct {
	Name string         `json:"name"`
	Bone string         `json:"bone"`
	Data map[string]any `json:"-"`
}

// SpineJSON is typed where stable and keeps full raw JSON for version-specific data.
type SpineJSON struct {
	Skeleton   *SkeletonInfo              `json:"skeleton,omitempty"`
	Bones      []Bone                     `json:"bones,omitempty"`
	Slots      []Slot                     `json:"slots,omitempty"`
	Skins      json.RawMessage            `json:"skins,omitempty"`
	Events     map[string]json.RawMessage `json:"events,omitempty"`
	Animations map[string]json.RawMessage `json:"animations,omitempty"`
	Raw        map[string]json.RawMessage `json:"-"`
}

func unmarshalObjectData(data []byte) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneAnyMap(source map[string]any) map[string]any {
	result := make(map[string]any, len(source)+2)
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (b *Bone) UnmarshalJSON(data []byte) error {
	type stable Bone
	var parsed stable
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	all, err := unmarshalObjectData(data)
	if err != nil {
		return err
	}
	*b = Bone(parsed)
	b.Data = all
	return nil
}

func (b Bone) MarshalJSON() ([]byte, error) {
	data := cloneAnyMap(b.Data)
	data["name"] = b.Name
	if b.Parent == "" {
		delete(data, "parent")
	} else {
		data["parent"] = b.Parent
	}
	return json.Marshal(data)
}

func (s *Slot) UnmarshalJSON(data []byte) error {
	type stable Slot
	var parsed stable
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	all, err := unmarshalObjectData(data)
	if err != nil {
		return err
	}
	*s = Slot(parsed)
	s.Data = all
	return nil
}

func (s Slot) MarshalJSON() ([]byte, error) {
	data := cloneAnyMap(s.Data)
	data["name"] = s.Name
	data["bone"] = s.Bone
	return json.Marshal(data)
}

func cloneRawMap(source map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(source)+8)
	for key, value := range source {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func setRaw(data map[string]json.RawMessage, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data[key] = encoded
	return nil
}

func (s *SkeletonInfo) UnmarshalJSON(data []byte) error {
	type stable SkeletonInfo
	var parsed stable
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*s = SkeletonInfo(parsed)
	s.Raw = raw
	return nil
}

func (s SkeletonInfo) MarshalJSON() ([]byte, error) {
	data := cloneRawMap(s.Raw)
	fields := []struct {
		key   string
		value any
		zero  bool
	}{
		{"hash", s.Hash, s.Hash == ""},
		{"spine", s.Spine, s.Spine == ""},
		{"x", s.X, s.X == 0},
		{"y", s.Y, s.Y == 0},
		{"width", s.Width, s.Width == 0},
		{"height", s.Height, s.Height == 0},
		{"fps", s.FPS, s.FPS == 0},
		{"images", s.Images, s.Images == ""},
		{"audio", s.Audio, s.Audio == ""},
	}
	for _, field := range fields {
		if field.zero {
			delete(data, field.key)
			continue
		}
		if err := setRaw(data, field.key, field.value); err != nil {
			return nil, err
		}
	}
	return json.Marshal(data)
}

func (s SpineJSON) MarshalJSON() ([]byte, error) {
	data := cloneRawMap(s.Raw)
	fields := []struct {
		key     string
		value   any
		present bool
	}{
		{"skeleton", s.Skeleton, s.Skeleton != nil},
		{"bones", s.Bones, s.Bones != nil},
		{"slots", s.Slots, s.Slots != nil},
		{"skins", s.Skins, len(s.Skins) > 0},
		{"events", s.Events, s.Events != nil},
		{"animations", s.Animations, s.Animations != nil},
	}
	for _, field := range fields {
		if !field.present {
			continue
		}
		if err := setRaw(data, field.key, field.value); err != nil {
			return nil, err
		}
	}
	return json.Marshal(data)
}

// ParseJSON parses standard Spine skeleton JSON.
func ParseJSON(source []byte) (*SpineJSON, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(source, &raw); err != nil {
		return nil, &ParseError{Code: ErrInvalidJSON, Msg: "invalid Spine JSON", Cause: err}
	}
	if raw == nil {
		return nil, &ParseError{Code: ErrInvalidJSON, Msg: "Spine JSON root must be an object"}
	}

	var parsed SpineJSON
	if err := json.Unmarshal(source, &parsed); err != nil {
		return nil, &ParseError{Code: ErrInvalidJSON, Msg: "invalid Spine JSON structure", Cause: err}
	}
	for _, bone := range parsed.Bones {
		if bone.Name == "" {
			return nil, &ParseError{Code: ErrInvalidJSON, Msg: "Spine JSON contains a bone without a name"}
		}
	}
	parsed.Raw = raw
	return &parsed, nil
}

// JSONSerializeOptions controls Spine JSON output.
type JSONSerializeOptions struct {
	// Indent enables pretty printing, for example "  ". Empty means compact.
	Indent string
}

// DeserializeJSON parses Spine JSON and preserves unknown fields.
func DeserializeJSON(source []byte) (*SpineJSON, error) {
	return ParseJSON(source)
}

// SerializeJSON writes Spine JSON while preserving unknown fields.
func SerializeJSON(document *SpineJSON, options JSONSerializeOptions) ([]byte, error) {
	if document == nil {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "Spine JSON document is nil"}
	}
	if options.Indent != "" {
		return json.MarshalIndent(document, "", options.Indent)
	}
	return json.Marshal(document)
}
