package spineparser

// discoverProjectRuntimeModelV43 解析 4.3 私有项目对象图。
// 当前已覆盖的 4.3.23 布局沿用已验证的字段解析器；旧保存布局由同一版本适配器继续分流。
func discoverProjectRuntimeModelV43(payload []byte, sourceVersion string) (*ProjectRuntimeModel, error) {
	runtimePayload, err := discoverProjectRuntimePayload(payload)
	if err != nil {
		return nil, err
	}
	bones, err := DiscoverProjectBones(runtimePayload)
	if err != nil {
		return discoverLegacyProjectRuntimeModel(payload, sourceVersion, legacyProject43Family(payload))
	}
	metadata, metadataErr := DiscoverProjectSkeletonMetadata(runtimePayload)
	if metadataErr != nil {
		metadata = &ProjectSkeletonMetadata{}
	}
	bounds, err := DiscoverProjectSkeletonBounds(runtimePayload)
	if err != nil {
		return nil, err
	}
	slots, err := discoverProjectSlotRecords(runtimePayload, payload)
	if err != nil {
		return nil, err
	}
	animations, err := DiscoverProjectAnimations(runtimePayload)
	if err != nil {
		return nil, err
	}
	model := &ProjectRuntimeModel{
		Layout:        "spine-4.3-project",
		SourceVersion: sourceVersion,
		Payload:       runtimePayload,
		Bones:         bones,
		Metadata:      metadata,
		Bounds:        bounds,
		Slots:         slots,
		Animations:    animations,
	}
	model.Regions, _ = DiscoverProjectRegionAttachments(runtimePayload)
	model.Meshes, _ = DiscoverProjectMeshAttachments(runtimePayload)
	model.VertexAttachments, _ = DiscoverProjectVertexAttachments(runtimePayload)
	model.Physics, _ = DiscoverProjectPhysicsConstraints(runtimePayload)
	model.PathConstraints, _ = DiscoverProjectPathConstraints(runtimePayload)
	model.ConstraintOrder, _ = ResolveProjectConstraintOrder(runtimePayload, model.Physics, model.PathConstraints)
	model.EventDefinitions, _ = DiscoverProjectEventDefinitions(runtimePayload)
	model.SkinAttachmentOrder, _ = DiscoverProjectDefaultSkinAttachmentOrder(runtimePayload)
	return model, nil
}

func legacyProject43Family(payload []byte) string {
	for offset := 0; offset+2 < len(payload); offset++ {
		if payload[offset] != 0x01 || payload[offset+1] != 0x01 {
			continue
		}
		_, end, ok := decodeProjectASCII(payload, offset+2)
		if !ok || end >= len(payload) {
			continue
		}
		if payload[end] == 0x09 {
			return "spine-4.3-legacy-project-v1"
		}
		if end+2 < len(payload) && payload[end] == 0x02 && payload[end+1] == 0x01 && payload[end+2] == 0x0a {
			return "spine-4.3-legacy-project-v2"
		}
		if end+6 < len(payload) &&
			payload[end] == 0x1d &&
			payload[end+1] == 0x00 &&
			payload[end+2] == 0x1f &&
			payload[end+3] == 0x0f &&
			payload[end+4] == 0x01 &&
			payload[end+5] == 0x00 &&
			payload[end+6] == 0x1c {
			return "spine-4.3-legacy-project-v3"
		}
		if end+2 < len(payload) &&
			payload[end] == 0x1d &&
			payload[end+1] == 0x00 &&
			payload[end+2] == 0x7e {
			return "spine-4.3-legacy-project-v4"
		}
	}
	return "spine-4.3-legacy-project"
}
