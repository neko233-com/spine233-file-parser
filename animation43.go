package spineparser

import (
	"fmt"
	"sort"
)

// Animation43Timeline is a runtime key array identified by a JSON pointer.
// Unknown animation fields are intentionally not traversed as timelines.
type Animation43Timeline struct {
	Path        string
	Category    string
	Owner       string
	Skin        string
	Slot        string
	Attachment  string
	Kind        string
	Keys        []any
	Channels    int
	FolderSlots []string
}

// VisitAnimation43Timelines visits only the key arrays consumed by Spine 4.3.
// The callback can edit key maps in place. Ordering is deterministic.
func VisitAnimation43Timelines(animation map[string]any, visit func(Animation43Timeline) error) error {
	for _, category := range []string{"bones", "slots", "ik", "transform", "path", "physics", "slider"} {
		raw, exists := animation[category]
		if !exists {
			continue
		}
		owners, ok := object(raw)
		if !ok {
			return fmt.Errorf("/%s: timeline owners must be an object", category)
		}
		for _, owner := range sorted43Keys(owners) {
			path := "/" + category + "/" + escapePointer(owner)
			if category == "ik" || category == "transform" {
				if err := visit43Array(owners[owner], Animation43Timeline{Path: path, Category: category, Owner: owner, Kind: category, Channels: map[string]int{"ik": 2, "transform": 6}[category]}, visit); err != nil {
					return err
				}
				continue
			}
			timelines, ok := object(owners[owner])
			if !ok {
				return fmt.Errorf("%s: timelines must be an object", path)
			}
			for _, kind := range sorted43Keys(timelines) {
				channels, known := timeline43Channels(category, kind)
				if !known {
					return fmt.Errorf("%s/%s: unsupported Spine 4.3 timeline", path, escapePointer(kind))
				}
				if err := visit43Array(timelines[kind], Animation43Timeline{Path: path + "/" + escapePointer(kind), Category: category, Owner: owner, Kind: kind, Channels: channels}, visit); err != nil {
					return err
				}
			}
		}
	}
	if raw, exists := animation["attachments"]; exists {
		skins, ok := object(raw)
		if !ok {
			return fmt.Errorf("/attachments: skins must be an object")
		}
		for _, skin := range sorted43Keys(skins) {
			slots, ok := object(skins[skin])
			if !ok {
				return fmt.Errorf("/attachments/%s: slots must be an object", escapePointer(skin))
			}
			for _, slot := range sorted43Keys(slots) {
				attachments, ok := object(slots[slot])
				if !ok {
					return fmt.Errorf("/attachments/%s/%s: attachments must be an object", escapePointer(skin), escapePointer(slot))
				}
				for _, attachment := range sorted43Keys(attachments) {
					timelines, ok := object(attachments[attachment])
					path := "/attachments/" + escapePointer(skin) + "/" + escapePointer(slot) + "/" + escapePointer(attachment)
					if !ok {
						return fmt.Errorf("%s: timelines must be an object", path)
					}
					for _, kind := range sorted43Keys(timelines) {
						channels := 0
						if kind == "deform" {
							channels = 1
						} else if kind != "sequence" {
							return fmt.Errorf("%s/%s: unsupported attachment timeline", path, escapePointer(kind))
						}
						if err := visit43Array(timelines[kind], Animation43Timeline{Path: path + "/" + escapePointer(kind), Category: "attachments", Skin: skin, Slot: slot, Attachment: attachment, Kind: kind, Channels: channels}, visit); err != nil {
							return err
						}
					}
				}
			}
		}
	}
	for _, category := range []string{"events", "drawOrder"} {
		if raw, exists := animation[category]; exists {
			if err := visit43Array(raw, Animation43Timeline{Path: "/" + category, Category: category, Kind: category}, visit); err != nil {
				return err
			}
		}
	}
	if raw, exists := animation["drawOrderFolder"]; exists {
		folders, ok := array(raw)
		if !ok {
			return fmt.Errorf("/drawOrderFolder: folders must be an array")
		}
		for i, raw := range folders {
			folder, ok := object(raw)
			if !ok {
				return fmt.Errorf("/drawOrderFolder/%d: folder must be an object", i)
			}
			names := []string{}
			if raw, exists := folder["slots"]; exists {
				values, ok := array(raw)
				if !ok {
					return fmt.Errorf("/drawOrderFolder/%d/slots: slots must be an array", i)
				}
				for _, raw := range values {
					name, ok := raw.(string)
					if !ok {
						return fmt.Errorf("/drawOrderFolder/%d/slots: slot must be a string", i)
					}
					names = append(names, name)
				}
			}
			keys, exists := folder["keys"]
			if !exists {
				keys = []any{}
			}
			if err := visit43Array(keys, Animation43Timeline{Path: fmt.Sprintf("/drawOrderFolder/%d/keys", i), Category: "drawOrderFolder", Kind: "drawOrder", FolderSlots: names}, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func visit43Array(raw any, timeline Animation43Timeline, visit func(Animation43Timeline) error) error {
	keys, ok := array(raw)
	if !ok {
		return fmt.Errorf("%s: timeline must be a key array", timeline.Path)
	}
	for i, raw := range keys {
		if _, ok := object(raw); !ok {
			return fmt.Errorf("%s/%d: key must be an object", timeline.Path, i)
		}
	}
	timeline.Keys = keys
	return visit(timeline)
}

func timeline43Channels(category, kind string) (int, bool) {
	kinds := map[string]map[string]int{
		"bones":   {"rotate": 1, "translate": 2, "translatex": 1, "translatey": 1, "scale": 2, "scalex": 1, "scaley": 1, "shear": 2, "shearx": 1, "sheary": 1, "inherit": 0},
		"slots":   {"attachment": 0, "rgba": 4, "rgb": 3, "alpha": 1, "rgba2": 7, "rgb2": 6},
		"path":    {"position": 1, "spacing": 1, "mix": 3},
		"physics": {"inertia": 1, "strength": 1, "damping": 1, "mass": 1, "wind": 1, "gravity": 1, "mix": 1, "reset": 0},
		"slider":  {"time": 1, "mix": 1},
	}
	channels, ok := kinds[category][kind]
	return channels, ok
}

func sorted43Keys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
