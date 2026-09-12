package spineparser

import (
	"fmt"
	"strconv"
	"strings"
)

// ProjectRuntimeVersionProfile 是 .spine 源版本的统一路由键。
// ExactKey 选择专用小版本适配器；FamilyKey 只保留给适配器内部共享
// 低层解码器，不作为生产路由的回退键。
type ProjectRuntimeVersionProfile struct {
	Source    string
	ExactKey  string
	FamilyKey string
	Major     int
	Minor     int
	Patch     int
}

func resolveProjectRuntimeVersionProfile(
	version string,
) (ProjectRuntimeVersionProfile, error) {
	trimmed := strings.TrimSpace(version)
	parts := strings.Split(trimmed, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return ProjectRuntimeVersionProfile{}, fmt.Errorf(
			"invalid Spine source version %q",
			version,
		)
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil {
		return ProjectRuntimeVersionProfile{}, fmt.Errorf(
			"invalid Spine source version %q",
			version,
		)
	}
	patch := 0
	if len(parts) == 3 {
		var patchErr error
		patch, patchErr = strconv.Atoi(parts[2])
		if patchErr != nil {
			return ProjectRuntimeVersionProfile{}, fmt.Errorf(
				"invalid Spine source version %q",
				version,
			)
		}
	}
	profile := ProjectRuntimeVersionProfile{
		Source:    trimmed,
		FamilyKey: fmt.Sprintf("%d.%d.x", major, minor),
		Major:     major,
		Minor:     minor,
		Patch:     patch,
	}
	if len(parts) == 3 {
		profile.ExactKey = fmt.Sprintf("%d.%d.%d", major, minor, patch)
	}
	return profile, nil
}
