package spineparser

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
)

const ProjectArchiveFormat = "spine233-project/v1"

// ProjectArchive retains the complete private payload alongside editable
// transform timelines. This is an authoring archive, not runtime Spine JSON.
// Fixed key counts and object references are required by the native writer.
type ProjectArchive struct {
	Format        string                    `json:"format"`
	SourceVersion string                    `json:"sourceVersion,omitempty"`
	PayloadSHA256 string                    `json:"payloadSHA256"`
	Payload       []byte                    `json:"payload"`
	Animations    []ProjectTransformRewrite `json:"animations"`
	Warnings      []string                  `json:"warnings,omitempty"`
}

func ArchiveProject(document *ProjectDocument) (*ProjectArchive, error) {
	if document == nil || len(document.Payload) == 0 {
		return nil, fmt.Errorf("project payload is empty")
	}
	digest := sha256.Sum256(document.Payload)
	archive := &ProjectArchive{Format: ProjectArchiveFormat, SourceVersion: document.Inspection.SpineVersion, PayloadSHA256: hex.EncodeToString(digest[:]), Payload: append([]byte(nil), document.Payload...), Animations: []ProjectTransformRewrite{}}
	directory, err := DiscoverProjectAnimations(document.Payload)
	if err != nil {
		archive.Warnings = append(archive.Warnings, "animation inventory unavailable: "+err.Error())
		return archive, nil
	}
	for _, record := range directory.Records {
		directory, err := DiscoverProjectTransformTimelines(document.Payload, record.Name)
		if err != nil {
			archive.Warnings = append(archive.Warnings, record.Name+": "+err.Error())
			continue
		}
		animation := ProjectTransformRewrite{Animation: record.Name, Timelines: []ProjectTransformTimelineRewrite{}}
		counts := map[string]int{}
		for _, timeline := range directory.Timelines {
			counts[fmt.Sprintf("%d/%s", timeline.BoneReference, timeline.Type)]++
		}
		for _, timeline := range directory.Timelines {
			if counts[fmt.Sprintf("%d/%s", timeline.BoneReference, timeline.Type)] != 1 {
				archive.Warnings = append(archive.Warnings, fmt.Sprintf("%s: ambiguous %s timeline for bone reference %d retained in payload", record.Name, timeline.Type, timeline.BoneReference))
				continue
			}
			keys := make([]ProjectTransformKeySpec, len(timeline.Keys))
			for i, key := range timeline.Keys {
				keys[i] = ProjectTransformKeySpec{Frame: key.Frame, Values: append([]float32(nil), key.Values...)}
			}
			animation.Timelines = append(animation.Timelines, ProjectTransformTimelineRewrite{BoneReference: timeline.BoneReference, BoneName: timeline.BoneName, Timeline: timeline.Type, Keys: keys})
		}
		archive.Animations = append(archive.Animations, animation)
	}
	return archive, nil
}

// RestoreProjectArchive validates the original payload checksum and applies
// all edits transactionally. It never mutates the archive or its payload.
func RestoreProjectArchive(archive *ProjectArchive) (*ProjectDocument, []ProjectTransformPatchReport, error) {
	if archive == nil || archive.Format != ProjectArchiveFormat {
		return nil, nil, fmt.Errorf("unsupported project archive format")
	}
	if len(archive.Payload) == 0 || len(archive.Payload) > int(defaultMaxUncompressedBytes) {
		return nil, nil, fmt.Errorf("invalid project archive payload size")
	}
	digest := sha256.Sum256(archive.Payload)
	if hex.EncodeToString(digest[:]) != archive.PayloadSHA256 {
		return nil, nil, fmt.Errorf("project archive payload checksum mismatch")
	}
	document := &ProjectDocument{Payload: append([]byte(nil), archive.Payload...)}
	reports := []ProjectTransformPatchReport{}
	seen := map[string]bool{}
	for _, rewrite := range archive.Animations {
		if seen[rewrite.Animation] {
			return nil, nil, fmt.Errorf("duplicate animation %q", rewrite.Animation)
		}
		seen[rewrite.Animation] = true
		if rewrite.TargetAnimation != "" && rewrite.TargetAnimation != rewrite.Animation {
			return nil, nil, fmt.Errorf("archive renaming is unsupported; use a project patch")
		}
		directory, err := DiscoverProjectTransformTimelines(document.Payload, rewrite.Animation)
		if err != nil {
			return nil, nil, err
		}
		changed := false
		selected := map[string]bool{}
		for _, requested := range rewrite.Timelines {
			selection := fmt.Sprintf("%d/%s", requested.BoneReference, requested.Timeline)
			if selected[selection] {
				return nil, nil, fmt.Errorf("duplicate timeline %s", selection)
			}
			selected[selection] = true
			matches := 0
			for _, current := range directory.Timelines {
				if current.BoneReference != requested.BoneReference || current.Type != requested.Timeline {
					continue
				}
				matches++
				if current.BoneName != requested.BoneName {
					return nil, nil, fmt.Errorf("archive bone name does not match payload")
				}
				if len(current.Keys) != len(requested.Keys) {
					return nil, nil, fmt.Errorf("archive key count must remain unchanged")
				}
				for i, key := range requested.Keys {
					if !finiteProjectFloat(key.Frame) || key.Frame < 0 {
						return nil, nil, fmt.Errorf("invalid archive key frame")
					}
					if i > 0 && key.Frame <= requested.Keys[i-1].Frame {
						return nil, nil, fmt.Errorf("archive frames must be strictly increasing")
					}
					for _, value := range key.Values {
						if !finiteProjectFloat(value) {
							return nil, nil, fmt.Errorf("invalid archive key value")
						}
					}
					if len(key.Values) != len(current.Channels) {
						return nil, nil, fmt.Errorf("archive channel count must remain unchanged")
					}
					changed = changed || key.Frame != current.Keys[i].Frame || !reflect.DeepEqual(key.Values, current.Keys[i].Values)
					if len(key.Curves) != 0 {
						if len(key.Curves) != len(current.Channels) {
							return nil, nil, fmt.Errorf("archive curve count must remain unchanged")
						}
						for channel, curve := range key.Curves {
							if channel >= len(current.Keys[i].Curves) || channel >= len(current.Keys[i].CurveOffsets) {
								return nil, nil, fmt.Errorf("project curve layout is unavailable")
							}
							for j, value := range curve {
								if !finiteProjectFloat(value) {
									return nil, nil, fmt.Errorf("invalid archive curve")
								}
								if value != current.Keys[i].Curves[channel][j] && current.Keys[i].CurveOffsets[channel][j] <= 0 {
									return nil, nil, fmt.Errorf("cannot add a curve to a compact project key")
								}
							}
						}
						changed = changed || !reflect.DeepEqual(key.Curves, current.Keys[i].Curves)
					}
				}
			}
			if matches != 1 {
				return nil, nil, fmt.Errorf("archive timeline %s has %d matches", selection, matches)
			}
		}
		if !changed {
			continue
		}
		patched, report, err := RewriteProjectTransformTimelines(document, rewrite)
		if err != nil {
			return nil, nil, err
		}
		document = patched
		reports = append(reports, report)
	}
	return document, reports, nil
}
