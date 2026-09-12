package spineparser

import "bytes"

var (
	projectEventDefinitionPrefix = []byte{0x23, 0x01, 0x0a, 0x02, 0x01}
	projectEventDefinitionSuffix = []byte{0x03, 0x00, 0x0a, 0x6c}
)

// DiscoverProjectEventDefinitions decodes top-level 4.3.23 event names.
func DiscoverProjectEventDefinitions(payload []byte) ([]string, error) {
	if len(payload) == 0 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset+len(projectEventDefinitionPrefix) < len(payload); {
		relative := bytes.Index(payload[offset:], projectEventDefinitionPrefix)
		if relative < 0 {
			break
		}
		definitionOffset := offset + relative
		nameOffset := definitionOffset + len(projectEventDefinitionPrefix)
		name, nameEnd, ok := decodeProjectASCII(payload, nameOffset)
		if ok &&
			nameEnd+len(projectEventDefinitionSuffix) <= len(payload) &&
			bytes.Equal(
				payload[nameEnd:nameEnd+len(projectEventDefinitionSuffix)],
				projectEventDefinitionSuffix,
			) {
			if _, duplicate := seen[name]; !duplicate {
				names = append(names, name)
				seen[name] = struct{}{}
			}
			offset = nameEnd + len(projectEventDefinitionSuffix)
			continue
		}
		offset = definitionOffset + 1
	}
	return names, nil
}
