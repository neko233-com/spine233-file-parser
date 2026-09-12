package spineparser

import (
	"bytes"
	"fmt"
)

// ProjectAnimationDuration is the last keyed frame across every timeline in
// one animation. Duration is expressed in seconds at the project frame rate.
type ProjectAnimationDuration struct {
	Animation     string  `json:"animation"`
	FrameRate     int     `json:"frameRate"`
	TimelineCount int     `json:"timelineCount"`
	KeyCount      int     `json:"keyCount"`
	LastFrame     float32 `json:"lastFrame"`
	Duration      float32 `json:"duration"`
}

// DiscoverProjectAnimationDuration reads timeline key frames without
// resolving timeline owners or values. This keeps duration discovery complete
// even when a semantic decoder intentionally rejects an unsupported payload.
func DiscoverProjectAnimationDuration(
	payload []byte,
	animation string,
) (*ProjectAnimationDuration, error) {
	record, err := uniqueProjectAnimationRecord(payload, animation)
	if err != nil {
		return nil, err
	}
	return discoverProjectAnimationDurationForRecord(payload, record)
}

// DiscoverProjectAnimationDurationInRange reads a duration from an animation
// record already selected by a version-specific project adapter.
func DiscoverProjectAnimationDurationInRange(
	payload []byte,
	animation string,
	start int,
	end int,
) (*ProjectAnimationDuration, error) {
	if start < 0 || end <= start || end > len(payload) {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "invalid animation range"}
	}
	return discoverProjectAnimationDurationForRecord(payload, ProjectAnimationRecord{
		Name: animation, Offset: start, EndOffset: end,
	})
}

func discoverProjectAnimationDurationForRecord(
	payload []byte,
	record ProjectAnimationRecord,
) (*ProjectAnimationDuration, error) {
	offsets := make([]int, 0)
	for offset := record.Offset; offset+len(projectTimelinePrefix) < record.EndOffset; offset++ {
		if !bytes.HasPrefix(payload[offset:record.EndOffset], projectTimelinePrefix) {
			continue
		}
		if _, _, _, ok := readProjectTimelineHeaderWithKeyOffset(
			payload,
			offset,
			record.EndOffset,
		); ok {
			offsets = append(offsets, offset)
		}
	}
	if len(offsets) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("animation %q contains no readable timelines", record.Name),
		}
	}
	result := &ProjectAnimationDuration{
		Animation:     record.Name,
		FrameRate:     projectAnimationFrameRate,
		TimelineCount: len(offsets),
	}
	for index, offset := range offsets {
		_, keyCount, keyOffset, ok := readProjectTimelineHeaderWithKeyOffset(
			payload,
			offset,
			record.EndOffset,
		)
		if !ok {
			return nil, &ParseError{
				Code: ErrInvalidProject,
				Msg:  fmt.Sprintf("animation %q timeline header is invalid at offset %d", record.Name, offset),
			}
		}
		timelineEnd := record.EndOffset
		if index+1 < len(offsets) {
			timelineEnd = offsets[index+1]
		}
		lastFrame, err := readProjectTimelineLastFrame(
			payload,
			keyOffset,
			timelineEnd,
			keyCount,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"animation %q timeline at offset %d: %w",
				record.Name,
				offset,
				err,
			)
		}
		result.KeyCount += keyCount
		if lastFrame > result.LastFrame {
			result.LastFrame = lastFrame
		}
	}
	result.Duration = result.LastFrame / float32(result.FrameRate)
	return result, nil
}

func readProjectTimelineLastFrame(
	payload []byte,
	offset int,
	end int,
	keyCount int,
) (float32, error) {
	cursor := offset
	lastFrame := float32(0)
	for index := 0; index < keyCount; index++ {
		relative := bytes.Index(payload[cursor:end], projectTimelineKeyPrefix)
		if relative < 0 {
			return 0, &ParseError{Code: ErrInvalidProject, Msg: "timeline key count does not match payload"}
		}
		keyOffset := cursor + relative
		frameOffset := keyOffset + len(projectTimelineKeyPrefix)
		if frameOffset+4 > end {
			return 0, &ParseError{Code: ErrInvalidProject, Msg: "timeline key frame exceeds animation range"}
		}
		frame := readProjectFloat32(payload, frameOffset)
		if !finiteProjectFloat(frame) || frame < 0 || (index > 0 && frame < lastFrame) {
			return 0, &ParseError{Code: ErrInvalidProject, Msg: "timeline key frame is invalid or out of order"}
		}
		lastFrame = frame
		cursor = frameOffset + 4
	}
	return lastFrame, nil
}
