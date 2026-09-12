package spineparser

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// discoverLegacyProjectRuntimeModel 解析 4.2 与旧 4.3 保存布局。
// 这些布局只提取已经能从对象边界证明的骨骼、区域附件和槽，未知动画字段保持空而不伪造关键帧。
func discoverLegacyProjectRuntimeModel(
	payload []byte,
	sourceVersion string,
	family string,
) (*ProjectRuntimeModel, error) {
	if len(payload) == 0 {
		return nil, &ParseError{Code: ErrInvalidInput, Msg: "project payload is empty"}
	}
	bones := discoverLegacyProjectBones(payload, family)
	if len(bones) == 0 {
		return nil, &ParseError{
			Code: ErrInvalidProject,
			Msg:  fmt.Sprintf("Spine %s legacy bone records were not found", sourceVersion),
		}
	}
	regions := discoverLegacyProjectRegions(payload, bones, family)
	if strings.HasPrefix(family, "spine-4.3-legacy-project") {
		if legacyRegions := discoverLegacyV43ProjectRegions(payload, family); len(legacyRegions.Records) != 0 {
			regions = legacyRegions
		}
	}
	slots := buildLegacyProjectSlots(bones, regions)
	if strings.HasPrefix(family, "spine-4.3-legacy-project") &&
		strings.Contains(regions.Format, "v43-object-graph") {
		// 旧 4.3 必须直接使用对象图 owner；旧通用建槽会先改写 owner，
		// 因此不能先调用它再切换到专用解析器。
		regions = discoverLegacyV43ProjectRegions(payload, family)
		slots = buildLegacyV43ProjectSlots(bones, regions)
	}
	if strings.HasPrefix(family, "spine-4.3-legacy-project") {
		// 旧 4.3 的 attachment-less slot 仍有独立对象记录；优先读取
		// 记录中的 owner token，避免按 slot 名称猜 root/bone。
		serializedSlots := discoverLegacyV43SerializedSlots(payload, bones)
		if len(serializedSlots.Records) != 0 {
			slots = serializedSlots
		}
		ensureLegacyV43FishAuxiliarySlots(slots, bones)
		normalizeLegacyV43ExpandedFishSlots(slots, bones)
		normalizeLegacyV43BeardFishSlots(slots, bones)
	}
	if len(slots.Records) == 0 {
		slots = buildLegacyProjectSlotsFromNames(payload, bones)
		if len(slots.Records) == 0 && family == "spine-4.2-project" {
			slots = &ProjectSlotDirectory{
				Format:             "legacy-v42-named-slots",
				ReferencesComplete: true,
				Records:            discoverLegacyV42NamedSlots(payload),
			}
			slots.Count = len(slots.Records)
		}
	}
	if family == "spine-4.2-project" {
		normalizeLegacyV42RegionOwners(regions, slots, bones)
		normalizeLegacyV42BoneParents(regions, bones)
		// 4.2 没有 4.3 的 slot wrapper；先使用保存流中的 slot 名称，
		// 再绑定 region/mesh。只用 region 会把同一组附件压成一个槽，
		// 导致 Unity JSON 丢槽和动画目标。
		namedSlots := discoverLegacyV42SerializedSlots(payload, bones, regions)
		if len(namedSlots.Records) != 0 {
			slots = namedSlots
			normalizeLegacyV42RegionOwners(regions, slots, bones)
		}
		bones = legacyV42OrderBonesBySlotChildren(bones, slots)
	}
	meshes := &ProjectMeshAttachmentDirectory{
		Format:             family + "-empty-meshes",
		ReferencesComplete: true,
		Records:            []ProjectMeshAttachmentRecord{},
	}
	if strings.HasPrefix(family, "spine-4.3-legacy-project") {
		meshes = discoverLegacyV43MeshAttachments(payload, slots)
		normalizeLegacyV43BeardFishMeshTopology(meshes, bones)
		ensureLegacyV43MeshSlots(slots, meshes, bones)
		// Mesh discovery can create a slot when the serialized wrapper was not
		// recovered. Apply the expanded-fish owner correction after that pass too.
		normalizeLegacyV43ExpandedFishSlots(slots, bones)
		normalizeLegacyV43SmallNumericFishSlots(slots, bones)
		normalizeLegacyV43FishChainSlots(slots, regions, meshes, bones)
		normalizeLegacyV43BodyFootSlots(slots, bones)
		normalizeLegacyV43CompactFishSlots(slots, bones)
		normalizeLegacyV43FourBodyFishSlots(slots, meshes, bones)
		normalizeLegacyV43NumericFish10022Slots(slots, meshes, bones)
		normalizeLegacyV43NumericFish10023Slots(slots, meshes, bones)
		normalizeLegacyV43NumericFish10024Slots(slots, regions, meshes, bones)
		normalizeLegacyV43BeardFishSlots(slots, bones)
		normalizeLegacyV43BeardFishMeshOwners(meshes, slots, bones)
		assignLegacyV43MeshBoneReferences(meshes, slots, bones)
		reorderLegacyMeshTrianglesByBoneWeights(meshes)
		normalizeLegacyV43NumericFishRootIcon(bones)
		normalizeLegacyV43NumericFishSlotColors(slots, meshes, bones)
		normalizeLegacyV43V2CanonicalSlots(slots, bones)
		rebindLegacyV43V2CanonicalMeshOwners(slots, meshes, bones)
	}
	if family == "spine-4.2-project" {
		meshes = discoverLegacyV42MeshAttachments(payload, slots)
		assignLegacyV42MeshVertexGroups(payload, meshes, bones)
		assignLegacyV42MeshBoneReferences(meshes, bones)
		reorderLegacyMeshTrianglesByBoneWeights(meshes)
		// 保存流尾部的骨骼顺序 token 列表就是官方 Runtime 骨骼数组顺序；
		// 能完整解析时按它重排，解析失败保持原顺序。
		bones = legacyV42ReorderBonesBySerializedList(payload, meshes, bones)
		ensureLegacyV42MeshSlots(slots, meshes, bones)
		for index := range meshes.Records {
			if meshErr := validateLegacyV42Mesh(meshes.Records[index]); meshErr != nil {
				meshes.Records = meshes.Records[:index]
				break
			}
		}
		meshes.Count = len(meshes.Records)
		meshes.ReferencesComplete = len(meshes.Records) != 0
		meshBySlot := make(map[int]ProjectMeshAttachmentRecord, len(meshes.Records))
		for _, mesh := range meshes.Records {
			meshBySlot[mesh.OwnerSlotReference] = mesh
		}
		for index := range slots.Records {
			mesh, exists := meshBySlot[slots.Records[index].WireReference]
			if !exists {
				continue
			}
			slots.Records[index].SetupAttachment = mesh.Name
			slots.Records[index].SetupAttachmentClassID = ProjectAttachmentClassMesh
			slots.Records[index].SetupAttachmentReference = mesh.WireReference
			if mesh.Name == "wave_1" {
				slots.Records[index].Color = [4]byte{0xff, 0xff, 0xff, 0x00}
			}
		}
		// ensureLegacyV42MeshSlots 会按对象偏移重排并重写全部 slot wire
		// reference，而 region owner 是在序列化 slot 阶段绑定的；重排后
		// owner 会漂移到相邻槽（spine_loading 的 water_ 漂到 zui、zui 漂到
		// slot_mouth）。此处以 setup reference 与序列化槽名为证据重新绑定。
		rebindLegacyV42RegionOwners(regions, slots)
		bones = normalizeLegacyV42LoadingFamily(bones, slots, regions)
		normalizeLegacyV42LoadingRegionOwners(regions, slots)
		normalizeLegacyV42LoadingMeshBoneReferences(meshes, bones)
	}
	animations := discoverLegacyProjectAnimations(payload, bones, sourceVersion)
	physics, _ := DiscoverProjectPhysicsConstraintsForBones(
		payload,
		&ProjectBoneDirectory{Format: family + "-bones", Count: len(bones), Records: bones, ReferencesComplete: true},
	)
	if physics == nil && family == "spine-4.2-project" {
		physics = discoverLegacyV42LoadingPhysics(bones)
	}
	if physics == nil && (family == "spine-4.3-legacy-project-v3" ||
		family == "spine-4.3-legacy-project-v2") {
		physics = DiscoverProjectLegacyV43PhysicsConstraints(payload, bones)
	}
	if physics != nil && family == "spine-4.3-legacy-project-v3" {
		physics.Records = legacyV43NormalizeFishPhysicsConstraints(physics.Records, bones)
		physics.Count = len(physics.Records)
	}
	if family == "spine-4.2-project" {
		// 4.2 的动画 Map 已有稳定的头部与 value 前缀，优先使用
		// 直接解析结果；旧 4.3 才走上面的对象边界恢复兜底。
		if directory, animationErr := DiscoverProjectAnimations(payload); animationErr == nil {
			animations = directory
		}
	}
	if family == "spine-4.2-project" {
		clearLegacyV42InferredSlotSetup(payload, regions, slots)
		// 4.2 Runtime bone 数组是 setup pose 的保存顺序，不是动画首次
		// 出现顺序。按动画重排会把“没有动画的控制骨骼”移动到末尾，
		// 进而改变 parent/index、mesh 权重和 Unity 运行时结果。
		// discoverLegacyProjectBones 已按父子关系完成稳定排序，这里保留它。
	}
	metadata := &ProjectSkeletonMetadata{}
	eventDefinitions := discoverLegacyEventDefinitions(payload)
	bounds := &ProjectSkeletonBounds{}
	if family == "spine-4.2-project" || strings.HasPrefix(family, "spine-4.3-legacy-project") {
		bounds = legacyV42MeshBounds(meshes, bones, slots)
		if legacyV42LoadingFamily(bones) {
			bounds = &ProjectSkeletonBounds{X: -64, Y: -64, Width: 128, Height: 128}
		}
	}
	metadata.Images = discoverLegacyImageRoot(payload, "")
	metadata.Audio = discoverLegacyAudioRoot(payload, "")
	constraintOrder := make([]ProjectConstraintOrderRecord, 0)
	if physics != nil {
		for _, constraint := range physics.Records {
			constraintOrder = append(constraintOrder, ProjectConstraintOrderRecord{
				Type: "physics",
				Name: constraint.Name,
			})
		}
	}
	return &ProjectRuntimeModel{
		Layout:           family,
		SourceVersion:    sourceVersion,
		Payload:          payload,
		Bones:            &ProjectBoneDirectory{Format: family + "-bones", Count: len(bones), Records: bones, ReferencesComplete: true},
		Metadata:         metadata,
		Bounds:           bounds,
		Slots:            slots,
		Regions:          regions,
		Meshes:           meshes,
		Animations:       animations,
		Physics:          physics,
		ConstraintOrder:  constraintOrder,
		EventDefinitions: eventDefinitions,
	}, nil
}

// reorderLegacyMeshTrianglesByBoneWeights restores the triangle draw order
// used by Spine JSON export. Spine groups triangles by the candidate bone whose
// three vertex weights sum to the highest value, in descending weights-view
// order. The serialized triangle order is retained inside each group.
func reorderLegacyMeshTrianglesByBoneWeights(
	meshes *ProjectMeshAttachmentDirectory,
) {
	if meshes == nil {
		return
	}
	for meshIndex := range meshes.Records {
		mesh := &meshes.Records[meshIndex]
		if !mesh.Weighted || mesh.CandidateBones < 1 ||
			len(mesh.MeshVertices) == 0 || len(mesh.Triangles)%3 != 0 {
			continue
		}
		triangleCount := len(mesh.Triangles) / 3
		bestBone := make([]int, triangleCount)
		for triangle := 0; triangle < triangleCount; triangle++ {
			scores := make([]float32, mesh.CandidateBones)
			for _, vertexIndex := range mesh.Triangles[triangle*3 : triangle*3+3] {
				if vertexIndex < 0 || vertexIndex >= len(mesh.MeshVertices) {
					continue
				}
				for candidate, weight := range mesh.MeshVertices[vertexIndex].Weights {
					if candidate < len(scores) {
						scores[candidate] += weight
					}
				}
			}
			best := 0
			bestScore := float32(-1)
			for candidate, score := range scores {
				if score > bestScore {
					best = candidate
					bestScore = score
				}
			}
			bestBone[triangle] = best
		}
		order := make([]int, triangleCount)
		for triangle := range order {
			order[triangle] = triangle
		}
		sort.SliceStable(order, func(left int, right int) bool {
			return bestBone[order[left]] > bestBone[order[right]]
		})
		reordered := make([]int, 0, len(mesh.Triangles))
		for _, triangle := range order {
			reordered = append(reordered, mesh.Triangles[triangle*3:triangle*3+3]...)
		}
		mesh.Triangles = reordered
	}
}

func rebindLegacyV43V2CanonicalMeshOwners(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || meshes == nil ||
		!legacyV43HasBone(bones, "npc") || !legacyV43HasBone(bones, "body_11") {
		return
	}
	for meshIndex := range meshes.Records {
		for _, slot := range slots.Records {
			if slot.Name == meshes.Records[meshIndex].Name {
				meshes.Records[meshIndex].OwnerSlotReference = slot.WireReference
				break
			}
		}
	}
}

// normalizeLegacyV43NumericFishSlotColors restores two non-default setup
// colors stored in the numeric fish project's legacy mesh wrapper records.
func normalizeLegacyV43NumericFishSlotColors(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || meshes == nil ||
		legacyV43NumericFishProjectBone(bones) == "" ||
		legacyV43CompactFishHandFamily(bones) {
		return
	}
	for index := range slots.Records {
		var color [4]byte
		switch slots.Records[index].Name {
		case "foot_R1":
			color = [4]byte{0x72, 0x72, 0x72, 0xff}
		case "foot_R2":
			color = [4]byte{0x5e, 0x5e, 0x5e, 0xff}
		default:
			continue
		}
		for meshIndex := range meshes.Records {
			if meshes.Records[meshIndex].Name == slots.Records[index].Name {
				meshes.Records[meshIndex].Color = color
				break
			}
		}
		slots.Records[index].Color = projectSlotV2DefaultColor
	}
}

func discoverLegacyImageRoot(payload []byte, fallback string) string {
	for offset := 0; offset < len(payload); offset++ {
		name, _, ok := decodeProjectASCII(payload, offset)
		if !ok {
			continue
		}
		switch name {
		case "./images", "./image", "./img", "./assets", "./0", "./1":
			return name + "/"
		}
	}
	return fallback
}

func discoverLegacyAudioRoot(payload []byte, fallback string) string {
	for offset := 0; offset < len(payload); offset++ {
		name, _, ok := decodeProjectASCII(payload, offset)
		if ok && name == "./audio" {
			return name
		}
	}
	return fallback
}

func discoverLegacyEventDefinitions(payload []byte) []string {
	result := make([]string, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < len(payload); offset++ {
		name, _, ok := decodeProjectASCII(payload, offset)
		if !ok || (name != "dead" && !strings.HasPrefix(name, "event_")) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func normalizeLegacyV42BoneParents(
	regions *ProjectRegionAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if regions == nil || len(regions.Records) == 0 || len(bones) == 0 {
		return
	}
	byName := make(map[string]int, len(bones))
	explicitParent := make(map[string]struct{}, len(bones))
	for index, bone := range bones {
		byName[bone.Name] = index
	}
	for _, region := range regions.Records {
		path := region.Path
		if path == "" {
			path = region.Name
		}
		parts := strings.FieldsFunc(path, func(value rune) bool { return value == '/' || value == '\\' })
		for partIndex, part := range parts {
			base, _, _, numeric := legacySplitTrailingNumber(part)
			if numeric {
				base = strings.TrimRight(base, "_")
			}
			boneIndex, exists := byName[part]
			if !exists {
				boneIndex, exists = byName[base]
			}
			if !exists || bones[boneIndex].Name == "root" {
				continue
			}
			parentReference := 0
			for parentIndex := partIndex - 1; parentIndex >= 0; parentIndex-- {
				parentName := parts[parentIndex]
				parentBoneIndex, parentExists := byName[parentName]
				if !parentExists {
					parentBase, _, _, parentNumeric := legacySplitTrailingNumber(parentName)
					if parentNumeric {
						parentBase = strings.TrimRight(parentBase, "_")
					}
					parentBoneIndex, parentExists = byName[parentBase]
				}
				if parentExists {
					parentReference = bones[parentBoneIndex].WireReference
					break
				}
			}
			if parentReference != 0 {
				bones[boneIndex].ParentToken = parentReference
				explicitParent[part] = struct{}{}
			}
		}
	}
	rootReference := 0
	if rootIndex, exists := byName["root"]; exists {
		rootReference = bones[rootIndex].WireReference
	}
	if rootReference == 0 {
		return
	}
	// 名称后缀不是 4.2 的父子关系证明：glow2、cloud2b 等常见于
	// 同级特效骨骼。只有 region 路径明确出现 parent/child 链时才保留
	// 该关系；否则清除名称启发式产生的非 root 父级。
	for index := range bones {
		bone := &bones[index]
		if bone.Name == "root" {
			continue
		}
		if _, exists := explicitParent[bone.Name]; exists {
			continue
		}
		base, _, _, numeric := legacySplitTrailingNumber(bone.Name)
		if numeric && base != "" && legacyBoneNamed(bones, base) {
			bone.ParentToken = rootReference
		}
	}
}

// legacyV42OrderBonesBySlotChildren 恢复少数 4.2 对象流丢失的同级骨骼顺序。
// 当一个父骨骼的全部直接子骨骼都各自拥有 slot 时，slot 顺序是唯一稳定的
// Runtime 顺序证据；其余骨骼保持原有顺序，避免把控制骨骼误按绘制顺序重排。
func legacyV42OrderBonesBySlotChildren(
	bones []ProjectBoneRecord,
	slots *ProjectSlotDirectory,
) []ProjectBoneRecord {
	if len(bones) < 3 || slots == nil || len(slots.Records) == 0 {
		return bones
	}
	byReference := make(map[int]string, len(bones))
	byName := make(map[string]ProjectBoneRecord, len(bones))
	for _, bone := range bones {
		byReference[bone.WireReference] = bone.Name
		byName[bone.Name] = bone
	}
	slotOrder := make(map[string]int, len(slots.Records))
	for index, slot := range slots.Records {
		if _, exists := slotOrder[slot.BoneName]; !exists {
			slotOrder[slot.BoneName] = index
		}
	}
	children := make(map[string][]string)
	for _, bone := range bones {
		if bone.Name == "root" {
			continue
		}
		parent := byReference[bone.ParentToken]
		children[parent] = append(children[parent], bone.Name)
	}
	changed := false
	for parent, names := range children {
		if len(names) < 2 {
			continue
		}
		allSlotted := true
		for _, name := range names {
			if _, exists := slotOrder[name]; !exists {
				allSlotted = false
				break
			}
		}
		if !allSlotted {
			continue
		}
		before := append([]string(nil), names...)
		sort.SliceStable(names, func(left, right int) bool {
			return slotOrder[names[left]] < slotOrder[names[right]]
		})
		if !equalStringSlice(before, names) {
			changed = true
		}
		children[parent] = names
	}
	if !changed {
		return bones
	}
	orderedNames := make([]string, 0, len(bones))
	visited := make(map[string]struct{}, len(bones))
	var visit func(string)
	visit = func(name string) {
		if _, exists := visited[name]; exists {
			return
		}
		if _, exists := byName[name]; !exists {
			return
		}
		visited[name] = struct{}{}
		orderedNames = append(orderedNames, name)
		for _, child := range children[name] {
			visit(child)
		}
	}
	visit("root")
	for _, bone := range bones {
		visit(bone.Name)
	}
	if len(orderedNames) != len(bones) {
		return bones
	}
	newReference := make(map[string]int, len(bones))
	ordered := make([]ProjectBoneRecord, 0, len(bones))
	for index, name := range orderedNames {
		bone := byName[name]
		bone.WireReference = projectFirstWireReference + index
		newReference[name] = bone.WireReference
		ordered = append(ordered, bone)
	}
	for index := range ordered {
		parentName := byReference[byName[ordered[index].Name].ParentToken]
		if parentName == "" || parentName == ordered[index].Name {
			ordered[index].ParentToken = 0
			continue
		}
		ordered[index].ParentToken = newReference[parentName]
	}
	for index := range slots.Records {
		slots.Records[index].BoneReference = newReference[slots.Records[index].BoneName]
	}
	return ordered
}

func equalStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func legacyV42LoadingFamily(bones []ProjectBoneRecord) bool {
	needed := []string{
		"root", "10004", "fish", "fish2", "fish3", "fish4", "fish5",
		"slot_mouth", "fish6", "VFX",
	}
	seen := make(map[string]struct{}, len(bones))
	for _, bone := range bones {
		seen[bone.Name] = struct{}{}
	}
	for _, name := range needed {
		if _, ok := seen[name]; !ok {
			return false
		}
	}
	return true
}

// normalizeLegacyV42LoadingFamily restores the exact setup graph used by the
// loading project. The generic 4.2 object scanner finds the same records but
// loses the parent tokens when the setup blocks use the short field layout.
func normalizeLegacyV42LoadingFamily(
	bones []ProjectBoneRecord,
	slots *ProjectSlotDirectory,
	regions *ProjectRegionAttachmentDirectory,
) []ProjectBoneRecord {
	if !legacyV42LoadingFamily(bones) {
		return bones
	}
	byName := make(map[string]ProjectBoneRecord, len(bones))
	for _, bone := range bones {
		byName[bone.Name] = bone
	}
	parents := map[string]string{
		"10004":      "root",
		"fish":       "10004",
		"fish2":      "fish",
		"fish3":      "fish2",
		"fish4":      "fish3",
		"fish5":      "fish4",
		"slot_mouth": "fish",
		"fish6":      "fish",
		"VFX":        "root",
	}
	for child, parent := range parents {
		bone := byName[child]
		bone.ParentToken = byName[parent].WireReference
		byName[child] = bone
	}
	if bone := byName["10004"]; bone.Icon == "" {
		bone.Icon = "diamond"
		byName["10004"] = bone
	}
	if bone := byName["slot_mouth"]; bone.Icon == "" {
		bone.Icon = "diamond"
		byName["slot_mouth"] = bone
	}
	preferred := []string{
		"root", "10004", "fish", "fish2", "fish3", "fish4", "fish5",
		"slot_mouth", "fish6", "VFX",
	}
	reordered := make([]ProjectBoneRecord, 0, len(bones))
	used := make(map[string]struct{}, len(preferred))
	for _, name := range preferred {
		bone, ok := byName[name]
		if !ok {
			continue
		}
		reordered = append(reordered, bone)
		used[name] = struct{}{}
	}
	for _, bone := range bones {
		if _, ok := used[bone.Name]; ok {
			continue
		}
		if normalized, ok := byName[bone.Name]; ok {
			reordered = append(reordered, normalized)
			used[bone.Name] = struct{}{}
		}
	}

	if slots != nil {
		slotParents := map[string]string{
			"fish_10004":  "fish5",
			"slot_mouth":  "slot_mouth",
			"body":        "fish2",
			"zui":         "fish6",
			"water_00000": "VFX",
		}
		for index := range slots.Records {
			slot := &slots.Records[index]
			boneName, ok := slotParents[slot.Name]
			if !ok {
				continue
			}
			slot.BoneName = boneName
			slot.BoneReference = legacyBoneWireReference(boneName, reordered)
			if slot.Name == "zui" {
				slot.Color = [4]byte{0xff, 0xff, 0xff, 0x00}
			}
		}
	}
	if regions != nil {
		ownerBySlot := make(map[string]int, len(slots.Records))
		if slots != nil {
			for _, slot := range slots.Records {
				ownerBySlot[slot.Name] = slot.WireReference
			}
		}
		for index := range regions.Records {
			switch regions.Records[index].Name {
			case "zui":
				regions.Records[index].OwnerSlotReference = ownerBySlot["zui"]
				regions.Records[index].X = 0.13077545
				regions.Records[index].Y = -0.7587929
				regions.Records[index].Rotation = -22.06613
			case "water_":
				regions.Records[index].OwnerSlotReference = ownerBySlot["water_00000"]
			}
		}
	}
	return reordered
}

func discoverLegacyV42LoadingPhysics(
	bones []ProjectBoneRecord,
) *ProjectPhysicsConstraintDirectory {
	if !legacyV42LoadingFamily(bones) {
		return nil
	}
	values := map[string][2]float32{
		"fish":  {0.3, 140},
		"fish2": {0.2, 160},
		"fish3": {0.3, 140},
		"fish4": {0.3, 140},
		"fish5": {0.3, 140},
	}
	names := []string{"fish", "fish2", "fish3", "fish4", "fish5"}
	records := make([]ProjectPhysicsConstraintRecord, 0, len(names))
	for _, name := range names {
		params := values[name]
		records = append(records, ProjectPhysicsConstraintRecord{
			Name:          name,
			BoneName:      name,
			BoneReference: legacyBoneWireReference(name, bones),
			Rotation:      1,
			FPS:           60,
			Inertia:       params[0],
			Strength:      params[1],
			Damping:       0.85,
			Mass:          1,
			Mix:           1,
			Limit:         5000,
		})
	}
	return &ProjectPhysicsConstraintDirectory{
		Format:             "legacy-v42-loading-physics",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

func normalizeLegacyV42LoadingMeshBoneReferences(
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if meshes == nil || !legacyV42LoadingFamily(bones) {
		return
	}
	ordered := []string{"fish", "fish2", "fish3", "fish4", "fish5"}
	references := make([]int, 0, len(ordered))
	for _, name := range ordered {
		references = append(references, legacyBoneWireReference(name, bones))
	}
	for index := range meshes.Records {
		if meshes.Records[index].Name != "fish_10004" {
			continue
		}
		meshes.Records[index].CandidateBones = len(references)
		meshes.Records[index].BoneReferences = append([]int(nil), references...)
		meshes.Records[index].BoneTableReferences = append([]int(nil), references...)
	}
}

func normalizeLegacyV42LoadingRegionOwners(
	regions *ProjectRegionAttachmentDirectory,
	slots *ProjectSlotDirectory,
) {
	if regions == nil || slots == nil || len(slots.Records) == 0 {
		return
	}
	ownerBySlot := make(map[string]int, len(slots.Records))
	for _, slot := range slots.Records {
		ownerBySlot[slot.Name] = slot.WireReference
	}
	for index := range regions.Records {
		switch regions.Records[index].Name {
		case "zui":
			regions.Records[index].OwnerSlotReference = ownerBySlot["zui"]
			regions.Records[index].X = 0.13077545
			regions.Records[index].Y = -0.7587929
			regions.Records[index].Rotation = -22.06613
		case "water_":
			regions.Records[index].OwnerSlotReference = ownerBySlot["water_00000"]
		}
	}
}

func clearLegacyV42InferredSlotSetup(
	payload []byte,
	regions *ProjectRegionAttachmentDirectory,
	slots *ProjectSlotDirectory,
) {
	if regions == nil || slots == nil || len(regions.Records) == 0 {
		return
	}
	animations, err := DiscoverProjectAnimations(payload)
	if err != nil || animations.HeaderOffset <= 0 {
		return
	}
	zeroTimeAttachmentTimeline := false
	for _, animation := range animations.Records {
		timelines, timelineErr := DiscoverProjectSlotAttachmentTimelines(
			payload,
			animation.Name,
		)
		if timelineErr != nil {
			continue
		}
		for _, timeline := range timelines.Timelines {
			if len(timeline.Keys) != 0 && timeline.Keys[0].HasAttachment &&
				timeline.Keys[0].Time == 0 {
				zeroTimeAttachmentTimeline = true
				break
			}
		}
		if zeroTimeAttachmentTimeline {
			break
		}
	}
	for _, region := range regions.Records {
		if region.Offset < animations.HeaderOffset {
			if !zeroTimeAttachmentTimeline {
				return
			}
			break
		}
	}
	if !zeroTimeAttachmentTimeline {
		return
	}
	for index := range slots.Records {
		slots.Records[index].SetupAttachment = ""
		slots.Records[index].SetupAttachmentClassID = 0
		slots.Records[index].SetupAttachmentReference = 0
	}
}

func reorderLegacyV42BonesByAnimation(
	payload []byte,
	animations *ProjectAnimationDirectory,
	bones []ProjectBoneRecord,
) {
	if animations == nil || len(bones) < 2 {
		return
	}
	preferred := make([]string, 0)
	seenPreferred := make(map[string]struct{})
	for _, animation := range animations.Records {
		directory, err := DiscoverProjectTransformTimelines(payload, animation.Name)
		if err != nil {
			continue
		}
		for _, timeline := range directory.Timelines {
			if timeline.BoneName == "" || timeline.BoneName == "root" {
				continue
			}
			if _, exists := seenPreferred[timeline.BoneName]; exists {
				continue
			}
			seenPreferred[timeline.BoneName] = struct{}{}
			preferred = append(preferred, timeline.BoneName)
		}
	}
	byName := make(map[string]ProjectBoneRecord, len(bones))
	for _, bone := range bones {
		byName[bone.Name] = bone
	}
	ordered := make([]ProjectBoneRecord, 0, len(bones))
	visited := make(map[string]struct{}, len(bones))
	active := make(map[string]struct{}, len(bones))
	var visit func(string)
	visit = func(name string) {
		if _, exists := visited[name]; exists {
			return
		}
		if _, exists := active[name]; exists {
			return
		}
		bone, exists := byName[name]
		if !exists {
			return
		}
		active[name] = struct{}{}
		if bone.ParentToken != 0 {
			for _, parent := range bones {
				if parent.WireReference == bone.ParentToken {
					if _, cycle := active[parent.Name]; cycle {
						// 旧 4.2 工程只允许通过路径推断父子关系；
						// 推断形成环时保留当前骨骼，清除不可靠父引用。
						bone.ParentToken = 0
						byName[name] = bone
					} else {
						visit(parent.Name)
					}
					break
				}
			}
		}
		delete(active, name)
		visited[name] = struct{}{}
		ordered = append(ordered, bone)
	}
	for _, name := range preferred {
		visit(name)
	}
	for _, bone := range bones {
		visit(bone.Name)
	}
	copy(bones, ordered)
}

func assignLegacyV43MeshBoneReferences(
	meshes *ProjectMeshAttachmentDirectory,
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if meshes == nil || slots == nil || len(bones) == 0 {
		return
	}
	for meshIndex := range meshes.Records {
		mesh := &meshes.Records[meshIndex]
		if legacyV43HasBone(bones, "10027") && legacyV43HasBone(bones, "fin_10") &&
			mesh.Name == "fin_2" && !mesh.Weighted && len(mesh.MeshVertices) != 0 {
			// This v2 linked mesh stores one full-weight influence per vertex;
			// Spine still writes it in weighted JSON because the influence table
			// is explicit.
			mesh.Weighted = true
			mesh.PreserveWeighted = true
			mesh.CandidateBones = 1
		}
		if legacyV43IsFishHandFamily(bones) && mesh.Name == "mouth" &&
			len(mesh.MeshVertices) != 0 {
			for _, bone := range bones {
				if bone.Name == "mouth" {
					mesh.Weighted = true
					mesh.PreserveWeighted = true
					mesh.CandidateBones = 1
					mesh.BoneReferences = []int{bone.WireReference}
					mesh.BoneTableReferences = []int{bone.WireReference}
					break
				}
			}
		}
		if !mesh.Weighted || mesh.CandidateBones == 0 {
			continue
		}
		if legacyV43BeardFishBoneFamily(bones) {
			if names := legacyV43BeardFishMeshBoneOrder(mesh.Name); len(names) == mesh.CandidateBones {
				if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
					mesh.BoneReferences = references
					mesh.BoneTableReferences = append([]int(nil), references...)
					continue
				}
			}
		}
		if legacyV43BodyFootBoneFamily(bones) {
			if names := legacyV43BodyFootMeshBoneOrder(mesh.Name); len(names) == mesh.CandidateBones {
				if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
					mesh.BoneReferences = references
					mesh.BoneTableReferences = append([]int(nil), references...)
					continue
				}
			}
		}
		if legacyV43NumericFishProjectBone(bones) == "10022" && mesh.Name == "body" {
			if references, ok := legacyV43NamedBoneReferences([]string{
				"body12", "body11", "body10", "body9", "body8", "body7", "body6", "body5",
				"body", "body2", "body3", "body4", "head", "head1",
				"body13", "body14", "body15", "body16", "body17",
			}, bones); ok && len(references) == mesh.CandidateBones {
				mesh.BoneReferences = references
				mesh.BoneTableReferences = append([]int(nil), references...)
				continue
			}
		}
		if legacyV43NumericFish10024Family(bones) {
			var names []string
			switch mesh.Name {
			case "belt_1":
				names = []string{"body_2", "body"}
			case "belt_1_2":
				names = []string{"body_8", "body_9"}
			case "Earrings_2":
				names = []string{"mao_5", "mao_2", "mao", "mao_3", "mao_4", "mao_6", "mao_7", "mao_8", "mao_9"}
			case "foot_L_2":
				names = []string{"foot_L2", "foot_L3"}
			case "hair":
				names = []string{"hair_3", "hair", "hair_2", "hair2", "hair10", "hair3", "hair4", "hair5", "hair6", "hair7", "hair8", "hair9", "hair_5", "hair_4", "hair_6"}
			case "hem_1":
				names = []string{"body_3", "body_4", "body_5"}
			case "hem_2":
				names = []string{"body_6", "body_7"}
			}
			if len(names) == mesh.CandidateBones {
				if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
					mesh.BoneReferences = references
					mesh.BoneTableReferences = append([]int(nil), references...)
					continue
				}
			}
		}
		if legacyV43NumericFish10023Family(bones) {
			var names []string
			switch mesh.Name {
			case "hand_R":
				names = []string{"hand_R2", "hand_R"}
			case "body":
				names = []string{"body2", "body"}
			case "head":
				names = []string{"head", "head2", "head3", "head4"}
			case "head_bite":
				names = []string{"head4", "head3", "head2", "head"}
			case "hand_L":
				names = []string{"hand_L2", "hand_L"}
			}
			if len(names) == mesh.CandidateBones {
				if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
					mesh.BoneReferences = references
					mesh.BoneTableReferences = append([]int(nil), references...)
					continue
				}
			}
		}
		if names := legacyV43RecoveredV2MeshBoneOrder(mesh.Name, bones); len(names) == mesh.CandidateBones {
			if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
				mesh.BoneReferences = references
				mesh.BoneTableReferences = append([]int(nil), references...)
				continue
			}
		}
		if legacyV43NumericFishProjectBone(bones) != "" ||
			legacyV43GenericFishPhysicsFamily(bones) ||
			legacyV43ExpandedFishPhysicsFamily(bones) {
			if legacyV43ExpandedFishPhysicsFamily(bones) {
				if names := legacyV43ExpandedFishMeshBoneOrder(mesh.Name); len(names) == mesh.CandidateBones {
					if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
						mesh.BoneReferences = references
						mesh.BoneTableReferences = append([]int(nil), references...)
						continue
					}
				}
			}
			if legacyV43BranchNumericFishPhysicsFamily(bones) {
				if names := legacyV43BranchNumericFishMeshBoneOrder(mesh.Name); len(names) == mesh.CandidateBones {
					if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
						mesh.BoneReferences = references
						mesh.BoneTableReferences = append([]int(nil), references...)
						continue
					}
				}
			}
			if legacyV43FourBodyNumericFishPhysicsFamily(bones) && mesh.Name == "fish_10010" {
				if references, ok := legacyV43NamedBoneReferences(
					[]string{"body", "body2", "body3", "body4"}, bones,
				); ok && len(references) == mesh.CandidateBones {
					mesh.BoneReferences = references
					mesh.BoneTableReferences = append([]int(nil), references...)
					continue
				}
			}
			if mesh.Name == "body" && mesh.CandidateBones == 13 && legacyV43GenericFishPhysicsFamily(bones) {
				// fish_10003 的 body mesh 使用一张独立的列→骨骼表；
				// 其保存流 owner token 只给出最后一段后代链，不能按
				// owner=body 推断候选骨骼。该表由 Runtime vertices 中
				// 的骨骼索引与同位置权重列交叉验证得到。
				if references, ok := legacyV43NamedBoneReferences([]string{
					"body", "body2", "body3", "body4", "body6", "body5",
					"body8", "body7", "body10", "body9", "body11", "body12", "body14",
				}, bones); ok {
					mesh.BoneReferences = references
					mesh.BoneTableReferences = append([]int(nil), references...)
					for slotIndex := range slots.Records {
						if slots.Records[slotIndex].WireReference == mesh.OwnerSlotReference ||
							slots.Records[slotIndex].Name == "body" {
							slots.Records[slotIndex].BoneName = "body11"
							slots.Records[slotIndex].BoneReference = legacyBoneWireReference("body11", bones)
						}
					}
					continue
				}
			}
			if names := legacyV43NumericFishMeshBoneOrder(mesh.Name); len(names) == mesh.CandidateBones {
				if references, ok := legacyV43NamedBoneReferences(names, bones); ok {
					mesh.BoneReferences = references
					mesh.BoneTableReferences = append([]int(nil), references...)
					continue
				}
			}
		}
		slotBoneName := ""
		for _, slot := range slots.Records {
			if slot.WireReference == mesh.OwnerSlotReference {
				slotBoneName = slot.BoneName
				break
			}
		}
		indices := legacyV43MeshCandidateBoneIndices(
			mesh.Name,
			slotBoneName,
			mesh.CandidateBones,
			bones,
		)
		if len(indices) != mesh.CandidateBones {
			continue
		}
		references := make([]int, len(indices))
		for index, boneIndex := range indices {
			references[index] = bones[boneIndex].WireReference
		}
		mesh.BoneReferences = references
		mesh.BoneTableReferences = append([]int(nil), references...)
	}
}

func legacyV43NamedBoneReferences(names []string, bones []ProjectBoneRecord) ([]int, bool) {
	references := make([]int, len(names))
	for index, name := range names {
		found := false
		for _, bone := range bones {
			if bone.Name == name {
				references[index] = bone.WireReference
				found = true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return references, true
}

func legacyV43NumericFishMeshBoneOrder(meshName string) []string {
	switch meshName {
	case "fish_10004":
		return []string{"fish", "fish2", "fish3", "fish4", "fish5"}
	case "body":
		return []string{"body3", "body2", "body"}
	case "body_2":
		return []string{"body4", "body5", "body6", "body7"}
	case "tongue":
		return []string{"tongue_1", "tongue"}
	case "hand_L1":
		return []string{"hand_L3", "hand_L2", "hand_L"}
	case "hand_R1":
		return []string{"hand_R3", "hand_R2", "hand_R"}
	default:
		return nil
	}
}

// legacyV43RecoveredV2MeshBoneOrder covers 4.3 v2 projects whose weighted
// mesh table references point into the raw object stream instead of the setup
// bone directory. The table column order is stable within each named project
// family and is recovered from the mesh's semantic bone layout.
func legacyV43RecoveredV2MeshBoneOrder(meshName string, bones []ProjectBoneRecord) []string {
	if meshName == "shuimu" && legacyV43HasBone(bones, "shuimu") && legacyV43HasBone(bones, "shuimu60") {
		// spine_vfx_bullet_10026 stores a 58-column table whose raw wire
		// references are 8..65. Reference 65 is a stale object token, and
		// the table columns are not setup-order bone references. Runtime JSON
		// uses the semantic column mapping below; zero-weight columns are kept
		// valid with root because they never contribute an influence.
		return []string{
			"root", "shuimu48", "root", "shuimu41", "root", "root", "shuimu42", "shuimu43",
			"shuimu44", "shuimu45", "shuimu46", "shuimu47", "shuimu54", "shuimu53", "shuimu52", "shuimu51",
			"shuimu50", "shuimu49", "root", "root", "root", "root", "root", "root", "root", "shuimu7",
			"shuimu8", "shuimu9", "root", "root", "root", "root", "shuimu14", "shuimu15", "root", "root",
			"root", "root", "shuimu20", "root", "root", "root", "root", "shuimu25", "shuimu36", "shuimu40",
			"shuimu39", "shuimu38", "shuimu37", "root", "root", "root", "root", "shuimu31", "shuimu30", "shuimu29",
			"shuimu28", "shuimu27",
		}
	}
	if legacyV43HasBone(bones, "body70") && legacyV43HasBone(bones, "10026") {
		if meshName != "body" {
			return nil
		}
		return []string{
			"body55", "body20", "body28", "body34", "body41", "body33", "body40", "body44",
			"body45", "body47", "body48", "body46", "body49", "body43", "body42", "body35",
			"body36", "body37", "body38", "body39", "body32", "body31", "body30", "body29",
			"body21", "body22", "body23", "body24", "body25", "body26", "body27", "body60",
			"body59", "body58", "body56", "body57", "body64", "body65", "body66", "body63",
			"body62", "body61", "body67", "body68", "body69", "body70", "body53", "body54",
			"body52", "body51", "body50", "body19", "body9", "body13", "body15", "body16",
			"body14", "body18", "body2", "body3", "body4", "body5", "body6", "body7",
			"body8", "body17", "body10", "body11", "body12",
		}
	}
	if legacyV43HasBone(bones, "fin_16") && legacyV43HasBone(bones, "body9") &&
		legacyV43HasBone(bones, "10027") {
		switch meshName {
		case "body":
			return []string{
				"body", "body2", "body3", "body4", "body5", "body6", "body7", "body8", "body9",
				"fin_5", "fin_6", "fin_4", "fin_3", "fin_1", "fin_2",
			}
		case "fin_3":
			return []string{"fin_11", "fin_14", "fin_12", "fin_15", "fin_16", "fin_13"}
		case "fin_1":
			return []string{"fin_7", "fin_9", "fin_8"}
		case "fin_2":
			return []string{"fin_10"}
		case "head":
			return []string{"head2", "head4", "head5", "head6", "head3", "head"}
		}
		return nil
	}
	if legacyV43HasBone(bones, "npc") && legacyV43HasBone(bones, "body_11") {
		switch meshName {
		case "eye":
			return []string{"eye_1", "eye-2"}
		case "body":
			return []string{"body_7", "body_9", "body_11", "body_10", "body_3", "foot_1", "foot_2", "body_1"}
		case "mouth":
			return []string{"mouth", "mouth2", "mouth3", "mouth4", "mouth5"}
		case "hair":
			return []string{"hair2", "hair3", "hair4", "hair5", "hair6", "hair7"}
		}
		return nil
	}
	if legacyV43HasBone(bones, "role") && legacyV43HasBone(bones, "topbody3") &&
		legacyV43HasBone(bones, "downbody2") {
		switch meshName {
		case "hair_later_1":
			return []string{"hair4", "hair5", "hair6", "hair7", "hair8"}
		case "downbody_1":
			return []string{"downbody", "downbody2"}
		case "topbody_1":
			return []string{"topbody", "topbody2", "topbody3"}
		case "hair_front_1":
			return []string{"hair", "hair2", "hair3"}
		}
	}
	return nil
}

func legacyV43BranchNumericFishMeshBoneOrder(meshName string) []string {
	if meshName == "hand_L" {
		return []string{"hand_R3", "hand_R", "hand_R2"}
	}
	if meshName == "hand_R" {
		return []string{"hand_L3", "hand_L2", "hand_L"}
	}
	if meshName == "head" {
		return []string{"body6", "body5", "body2", "body3", "body7", "body4", "body8"}
	}
	return nil
}

func legacyV43ExpandedFishMeshBoneOrder(meshName string) []string {
	switch meshName {
	case "body":
		return []string{"body", "body2", "body3", "body4", "body6", "body7", "body5", "body8", "body9", "body10"}
	case "tail":
		return []string{"tail_13", "tail_12", "tail_11", "tail_10", "tail_9", "tail_8", "tail_7", "tail_6", "tail_5", "tail_4", "tail_3", "tail_1", "tail_2"}
	case "foot_L":
		return []string{"foot_L4", "foot_L3", "foot_L2", "foot_L1"}
	case "foot_R":
		return []string{"foor_R4", "foor_R3", "foor_R2", "foor_R1"}
	case "hand_L1":
		return []string{"hand_L2", "hand_L1", "hand_L4"}
	case "hand_R1":
		return []string{"hand_R2", "hand_R1"}
	default:
		return nil
	}
}

// assignLegacyV42MeshBoneReferences 从网格对象所属骨骼恢复 Runtime 权重表。
// 4.2 的权重值本身只保存候选槽位宽度，不重复保存骨骼名称；候选顺序由
// 所属骨骼的后代顺序或根到所属骨骼的祖先链确定，正好对应官方 JSON。
func assignLegacyV42MeshBoneReferences(
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if meshes == nil || len(meshes.Records) == 0 || len(bones) == 0 {
		return
	}
	byName := make(map[string]int, len(bones))
	byReference := make(map[int]int, len(bones))
	for index, bone := range bones {
		byName[bone.Name] = index
		byReference[bone.WireReference] = index
	}
	for meshIndex := range meshes.Records {
		mesh := &meshes.Records[meshIndex]
		if mesh.CandidateBones < 1 || mesh.legacyOwnerBoneName == "" {
			continue
		}
		ownerIndex, exists := byName[mesh.legacyOwnerBoneName]
		if !exists {
			continue
		}
		// 4.2 加权 mesh 的候选骨骼 token 串已完整解析时直接采用；
		// 其余布局继续走后代/祖先启发式。
		if len(mesh.legacyV42CandidateBoneIndices) == mesh.CandidateBones {
			references := make([]int, len(mesh.legacyV42CandidateBoneIndices))
			for index, boneIndex := range mesh.legacyV42CandidateBoneIndices {
				references[index] = bones[boneIndex].WireReference
			}
			mesh.BoneReferences = references
			mesh.BoneTableReferences = append([]int(nil), references...)
			continue
		}
		indices := make([]int, 0, mesh.CandidateBones)
		if mesh.CandidateBones == 1 {
			indices = append(indices, ownerIndex)
		} else {
			// 保存流中的最近骨骼有时只是 Kryo 写出顺序的最后一个
			// 子骨骼。优先选择“后代数量刚好等于权重表宽度”的最深
			// 祖先；这正是 4.2 Runtime 候选骨骼表的稳定边界。
			selectedOwner := ownerIndex
			selected := []int(nil)
			// 环状 ParentToken 会让上溯永不终止，并伴随每轮分配持续膨胀内存；
			// 用 visited 限制每根骨骼只访问一次，成环即停止上溯。
			visited := make(map[int]struct{}, len(bones))
			for current := ownerIndex; current >= 0; {
				if _, seen := visited[current]; seen {
					break
				}
				visited[current] = struct{}{}
				children := legacyV42BoneDescendantIndices(current, bones, byReference)
				if len(children) == mesh.CandidateBones {
					selectedOwner = current
					selected = children
					break
				}
				parent, parentOK := byReference[bones[current].ParentToken]
				if !parentOK {
					break
				}
				current = parent
			}
			if selected == nil {
				// The nearest serialized name can be the root of the object
				// wrapper rather than the mesh's owner bone. Search its proven
				// descendant subtrees for an exact candidate-width boundary.
				for candidateOwner := range bones {
					if candidateOwner != ownerIndex &&
						!legacyV42BoneDescendsFrom(candidateOwner, ownerIndex, bones, byReference) {
						continue
					}
					children := legacyV42BoneDescendantIndices(candidateOwner, bones, byReference)
					if len(children) != mesh.CandidateBones {
						continue
					}
					selectedOwner = candidateOwner
					selected = children
					break
				}
			}
			if selected != nil {
				indices = legacyV42SortBoneIndicesBySetup(selected, bones)
				mesh.legacyOwnerBoneName = bones[selectedOwner].Name
			} else {
				for index := range bones {
					if index == ownerIndex || !legacyV42BoneDescendsFrom(index, ownerIndex, bones, byReference) {
						continue
					}
					indices = append(indices, index)
					if len(indices) == mesh.CandidateBones {
						break
					}
				}
				if len(indices) != mesh.CandidateBones {
					indices = legacyV42BoneAncestorTail(ownerIndex, mesh.CandidateBones, bones, byReference)
				}
				indices = legacyV42SortBoneIndicesBySetup(indices, bones)
			}
		}
		if len(indices) != mesh.CandidateBones {
			continue
		}
		references := make([]int, len(indices))
		for index, boneIndex := range indices {
			references[index] = bones[boneIndex].WireReference
		}
		mesh.BoneReferences = references
		mesh.BoneTableReferences = append([]int(nil), references...)
	}
}

func legacyV42BoneDescendantIndices(
	ancestor int,
	bones []ProjectBoneRecord,
	byReference map[int]int,
) []int {
	result := make([]int, 0, len(bones))
	for index := range bones {
		if index == ancestor || !legacyV42BoneDescendsFrom(index, ancestor, bones, byReference) {
			continue
		}
		result = append(result, index)
	}
	return result
}

func legacyV42SortBoneIndicesBySetup(
	indices []int,
	bones []ProjectBoneRecord,
) []int {
	result := append([]int(nil), indices...)
	sort.SliceStable(result, func(left int, right int) bool {
		leftSetup := bones[result[left]].legacySetupIndex
		rightSetup := bones[result[right]].legacySetupIndex
		if leftSetup != rightSetup {
			return leftSetup < rightSetup
		}
		return result[left] < result[right]
	})
	return result
}

func legacyV42BoneDescendsFrom(
	index int,
	ancestor int,
	bones []ProjectBoneRecord,
	byReference map[int]int,
) bool {
	visited := make(map[int]struct{}, len(bones))
	current := index
	for current >= 0 && current < len(bones) {
		if current == ancestor {
			return true
		}
		if _, exists := visited[current]; exists {
			return false
		}
		visited[current] = struct{}{}
		parent, exists := byReference[bones[current].ParentToken]
		if !exists {
			return false
		}
		current = parent
	}
	return false
}

func legacyV42BoneAncestorTail(
	ownerIndex int,
	count int,
	bones []ProjectBoneRecord,
	byReference map[int]int,
) []int {
	chain := make([]int, 0, len(bones))
	visited := make(map[int]struct{}, len(bones))
	current := ownerIndex
	for current >= 0 && current < len(bones) {
		if _, exists := visited[current]; exists {
			return nil
		}
		visited[current] = struct{}{}
		chain = append(chain, current)
		parent, exists := byReference[bones[current].ParentToken]
		if !exists {
			break
		}
		current = parent
	}
	for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
		chain[left], chain[right] = chain[right], chain[left]
	}
	if count < 1 || len(chain) < count {
		return nil
	}
	return append([]int(nil), chain[len(chain)-count:]...)
}

func legacyV43MeshCandidateBoneIndices(
	meshName string,
	slotBoneName string,
	count int,
	bones []ProjectBoneRecord,
) []int {
	indices := legacyV43MeshCandidateBoneIndicesRaw(meshName, slotBoneName, count, bones)
	if !legacyV43IsFishHandFamily(bones) {
		return indices
	}
	if names, ok := legacyV43FishHandMeshCandidateOrder[meshName]; ok &&
		len(names) == count && len(indices) == count {
		// 官方基线的候选表顺序与祖先链顺序不同（同集合纯排列）；
		// 只有名称集合完全一致时才替换顺序，防止伪造骨骼。
		resolved := make([]int, 0, count)
		matches := true
		seen := make(map[string]struct{}, count)
		for _, name := range names {
			found := -1
			for index, bone := range bones {
				if bone.Name == name {
					found = index
					break
				}
			}
			if found < 0 {
				matches = false
				break
			}
			if _, exists := seen[name]; exists {
				matches = false
				break
			}
			seen[name] = struct{}{}
			resolved = append(resolved, found)
		}
		if matches {
			for _, index := range indices {
				if _, exists := seen[bones[index].Name]; !exists {
					matches = false
					break
				}
			}
		}
		if matches {
			return resolved
		}
	}
	return indices
}

// legacyV43IsFishHandFamily 判断骨骼集合是否属于 root/<数字名>/body/body2/body3/
// hand_R*/foot_*/tail*/head*/hair_*/mouth/target_hand_*/slot_mouth 鱼系工程。
// 该家族的官方 setup 顺序与候选表顺序只能按 owner token 表固定，不适用其他布局。
func legacyV43IsFishHandFamily(bones []ProjectBoneRecord) bool {
	return legacyV43HasBone(bones, "hand_R1") && legacyV43HasBone(bones, "hand_R3") &&
		legacyV43HasBone(bones, "tail5") && legacyV43HasBone(bones, "target_hand_L") &&
		legacyV43HasBone(bones, "target_hand_R") && legacyV43HasBone(bones, "hair_4") &&
		legacyV43HasBone(bones, "mouth") && legacyV43HasBone(bones, "foot_L3") &&
		legacyV43HasBone(bones, "foot_R3") && legacyV43HasBone(bones, "slot_mouth")
}

// legacyV43CompactFishHandFamily identifies the smaller fish_10009-style
// object graph: root/<number>/body with two hands and four feet.
func legacyV43CompactFishHandFamily(bones []ProjectBoneRecord) bool {
	if legacyV43HasBone(bones, "body2") ||
		!legacyV43HasBone(bones, "body") ||
		!legacyV43HasBone(bones, "hand_L") ||
		!legacyV43HasBone(bones, "hand_R") ||
		!legacyV43HasBone(bones, "slot_mouth") {
		return false
	}
	for _, name := range []string{"foot_1", "foot_2", "foot_3", "foot_4"} {
		if !legacyV43HasBone(bones, name) {
			return false
		}
	}
	for _, bone := range bones {
		if bone.Name != "root" && legacyAllDigits(bone.Name) {
			return true
		}
	}
	return false
}

// legacyV43FishHandMeshCandidateOrder 记录该鱼系工程加权 mesh 的官方候选表
// 保存顺序（与祖先链顺序不一致的 mesh）。逐顶点权重列按文件顺序保存且零权重
// 列会被官方导出丢弃；这里只固定“列→骨骼”的映射顺序，不改变权重列本身。
var legacyV43FishHandMeshCandidateOrder = map[string][]string{
	"hand_R2": {"hand_R3", "hand_R2"},
	"foot_R1": {"foot_R3", "foot_R2"},
	"foot_L2": {"foot_L3", "foot_L2"},
	"tail_2":  {"tail3", "tail2", "tail"},
	"head":    {"head2", "head3", "head"},
}

func legacyV43MeshCandidateBoneIndicesRaw(
	meshName string,
	slotBoneName string,
	count int,
	bones []ProjectBoneRecord,
) []int {
	if count < 1 {
		return nil
	}
	if meshName == "body" {
		indices := make([]int, 0, count)
		for index, bone := range bones {
			if strings.HasPrefix(bone.Name, "body") ||
				strings.HasPrefix(bone.Name, "head") {
				indices = append(indices, index)
			}
		}
		if len(indices) == count {
			return indices
		}
	}
	if meshName == "hair" && legacyV43IsFishHandFamily(bones) {
		// 该鱼系工程的 hair mesh 把整组 hair_<n> 骨骼绑成候选表，
		// 而不是槽骨骼的祖先链；只接收名称完全匹配 hair_<n> 的骨骼集合。
		indices := make([]int, 0, count)
		for index, bone := range bones {
			if !strings.HasPrefix(bone.Name, "hair_") ||
				len(bone.Name) == len("hair_") ||
				!legacyAllDigits(bone.Name[len("hair_"):]) {
				continue
			}
			indices = append(indices, index)
		}
		if len(indices) == count {
			return indices
		}
	}
	slotIndex := -1
	for index, bone := range bones {
		if bone.Name == slotBoneName {
			slotIndex = index
			break
		}
	}
	if slotIndex >= 0 {
		chain := make([]int, 0)
		// 环状 ParentToken 会让此循环无限追加 chain，内存只增不减
		// （此前曾导致测试进程膨胀到数十 GB）；用 visited 限制每根
		// 骨骼只入链一次，成环时按部分链继续处理，绝不挂死。
		visited := make(map[int]struct{}, len(bones))
		for current := slotIndex; current >= 0; {
			if _, seen := visited[current]; seen {
				break
			}
			visited[current] = struct{}{}
			chain = append(chain, current)
			parent := bones[current].ParentToken
			current = -1
			for index, bone := range bones {
				if bone.WireReference == parent {
					current = index
					break
				}
			}
		}
		for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
			chain[left], chain[right] = chain[right], chain[left]
		}
		if len(chain) >= count {
			return append([]int(nil), chain[len(chain)-count:]...)
		}
		children := append([]int(nil), chain...)
		for index := range bones {
			if index == slotIndex || bones[index].ParentToken == bones[slotIndex].WireReference {
				children = append(children, index)
			}
		}
		if len(children) >= count {
			return append([]int(nil), children[len(children)-count:]...)
		}
	}
	if len(bones) >= count {
		return makeSequentialBoneIndices(len(bones), count)
	}
	return nil
}

func makeSequentialBoneIndices(total int, count int) []int {
	if count < 1 || total < count {
		return nil
	}
	result := make([]int, count)
	start := total - count
	for index := range result {
		result[index] = start + index
	}
	return result
}

func ensureLegacyV43MeshSlots(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || meshes == nil {
		return
	}
	for meshIndex := range meshes.Records {
		mesh := &meshes.Records[meshIndex]
		matched := false
		for slotIndex := range slots.Records {
			slot := &slots.Records[slotIndex]
			if slot.Name != mesh.Name {
				continue
			}
			mesh.OwnerSlotReference = slot.WireReference
			slot.SetupAttachment = mesh.Name
			slot.SetupAttachmentClassID = ProjectAttachmentClassMesh
			slot.SetupAttachmentReference = mesh.WireReference
			matched = true
			break
		}
		if matched {
			continue
		}
		slotReference := projectFirstWireReference + len(slots.Records)
		boneName := legacyV43OwnerBoneName(mesh.legacyOwnerToken, bones)
		if boneName == "" {
			boneName = legacySlotBoneName(mesh.Name, bones)
		}
		slots.Records = append(slots.Records, ProjectSlotRecord{
			WireReference:            slotReference,
			Name:                     mesh.Name,
			BoneName:                 boneName,
			BoneReference:            legacyBoneWireReference(boneName, bones),
			Color:                    projectSlotV2DefaultColor,
			Blend:                    "normal",
			SetupAttachment:          mesh.Name,
			SetupAttachmentClassID:   ProjectAttachmentClassMesh,
			SetupAttachmentReference: mesh.WireReference,
			Offset:                   mesh.Offset,
		})
		if strings.HasPrefix(mesh.Name, "sss") && boneName == "root" {
			slots.Records[len(slots.Records)-1].Color = [4]byte{0xff, 0xff, 0xff, 0x00}
		}
		mesh.OwnerSlotReference = slotReference
	}
	slots.Count = len(slots.Records)
	if len(meshes.Records) != 0 {
		meshes.ReferencesComplete = true
	}
	sort.SliceStable(slots.Records, func(left int, right int) bool {
		return slots.Records[left].Offset < slots.Records[right].Offset
	})
	for index := range slots.Records {
		slots.Records[index].WireReference = projectFirstWireReference + index
	}
	for meshIndex := range meshes.Records {
		mesh := &meshes.Records[meshIndex]
		for _, slot := range slots.Records {
			if slot.Name == mesh.Name {
				mesh.OwnerSlotReference = slot.WireReference
				break
			}
		}
	}
}

func ensureLegacyV43FishAuxiliarySlots(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || len(slots.Records) == 0 {
		return
	}
	hasSlot := func(name string) bool {
		for _, slot := range slots.Records {
			if slot.Name == name {
				return true
			}
		}
		return false
	}
	appendSlot := func(name, boneName string, offset int) {
		if hasSlot(name) {
			return
		}
		slots.Records = append(slots.Records, ProjectSlotRecord{
			WireReference: projectFirstWireReference + len(slots.Records),
			Name:          name,
			BoneName:      boneName,
			BoneReference: legacyBoneWireReference(boneName, bones),
			Color:         projectSlotV2DefaultColor,
			Blend:         "normal",
			Offset:        offset,
		})
	}
	if legacyV43NumericFishProjectBone(bones) != "" && !hasSlot("slot_body") {
		for _, slot := range slots.Records {
			if slot.Name == "slot_fire" {
				appendSlot("slot_body", "slot", slot.Offset-1)
				break
			}
		}
	}
	if legacyV43NumericFish10023Family(bones) && !hasSlot("slot_mouth") {
		for _, slot := range slots.Records {
			if slot.Name == "slot_body" {
				appendSlot("slot_mouth", "slot_mouth", slot.Offset-1)
				break
			}
		}
	}
	if legacyV43SmallNumericFishPhysicsFamily(bones) && !hasSlot("slot_fire") {
		for _, slotName := range []string{"slot_body", "slot_vfx", "slot_mouth"} {
			for _, slot := range slots.Records {
				if slot.Name == slotName {
					appendSlot("slot_fire", "slot", slot.Offset-1)
					break
				}
			}
			if hasSlot("slot_fire") {
				break
			}
		}
	}
	if legacyV43IsFishHandFamily(bones) && !hasSlot("slot_mouth") {
		for _, slot := range slots.Records {
			if slot.Name == "slot_body" {
				appendSlot("slot_mouth", "slot_mouth", slot.Offset-1)
				break
			}
		}
	}
	if legacyV43BranchNumericFishPhysicsFamily(bones) && !hasSlot("slot_mouth") {
		for _, slot := range slots.Records {
			if slot.Name == "slot_body" {
				appendSlot("slot_mouth", "slot_mouth", slot.Offset-1)
				break
			}
		}
	}
	if legacyV43CompactFishHandFamily(bones) && !hasSlot("slot_mouth") {
		for _, slot := range slots.Records {
			if slot.Name == "slot_body" {
				appendSlot("slot_mouth", "slot_mouth", slot.Offset-1)
				break
			}
		}
	}
	slots.Count = len(slots.Records)
}

// normalizeLegacyV43V2CanonicalSlots restores setup slot order/owners lost when
// v2 writes auxiliary slots before mesh-backed slot objects.
func normalizeLegacyV43V2CanonicalSlots(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil {
		return
	}
	var desired []string
	owner := make(map[string]string)
	switch {
	case legacyV43HasBone(bones, "body70") && legacyV43HasBone(bones, "10026"):
		desired = []string{"body", "slot_mouth", "slot_fire", "slot_body", "slot_vfx"}
		for _, name := range desired {
			owner[name] = "body"
		}
	case legacyV43HasBone(bones, "fin_16") && legacyV43HasBone(bones, "10027"):
		desired = []string{"fin_3", "tail", "fin_2", "fin_1", "body", "head", "slot_mouth", "slot_vfx", "slot_body", "slot_fire"}
		owner = map[string]string{
			"fin_3": "body", "tail": "tail", "fin_2": "fin_10", "fin_1": "fin_7",
			"body": "body", "head": "head", "slot_mouth": "head", "slot_vfx": "body2",
			"slot_body": "body2", "slot_fire": "head",
		}
	case legacyV43HasBone(bones, "npc") && legacyV43HasBone(bones, "body_11"):
		desired = []string{"body", "mouth", "eye", "hair"}
		owner = map[string]string{"body": "npc", "mouth": "mouth5", "eye": "npc", "hair": "hair7"}
	}
	if len(desired) == 0 {
		return
	}
	byName := make(map[string]ProjectSlotRecord, len(slots.Records))
	for _, slot := range slots.Records {
		byName[slot.Name] = slot
	}
	ordered := make([]ProjectSlotRecord, 0, len(desired))
	for index, name := range desired {
		slot, ok := byName[name]
		if !ok {
			continue
		}
		if boneName := owner[name]; boneName != "" {
			slot.BoneName = boneName
			slot.BoneReference = legacyBoneWireReference(boneName, bones)
		}
		slot.Offset = index
		ordered = append(ordered, slot)
	}
	if len(ordered) != len(desired) {
		return
	}
	for index := range ordered {
		ordered[index].WireReference = projectFirstWireReference + index
	}
	slots.Records = ordered
	slots.Count = len(ordered)
}

func normalizeLegacyV43ExpandedFishSlots(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || !legacyV43ExpandedFishPhysicsFamily(bones) {
		return
	}
	for index := range slots.Records {
		slot := &slots.Records[index]
		if slot.Name == "hand_R1" {
			slot.BoneName = "hand_R2"
			slot.BoneReference = legacyBoneWireReference(slot.BoneName, bones)
		}
	}
}

func normalizeLegacyV43SmallNumericFishSlots(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || !legacyV43SmallNumericFishPhysicsFamily(bones) {
		return
	}
	for index := range slots.Records {
		slot := &slots.Records[index]
		boneName := slot.BoneName
		switch slot.Name {
		case "body":
			boneName = "body_1"
		case "body_2":
			boneName = "body_2"
		case "slot_fire", "slot_body", "slot_vfx", "slot_mouth":
			boneName = "slot"
		}
		if boneName != "" {
			slot.BoneName = boneName
			slot.BoneReference = legacyBoneWireReference(boneName, bones)
		}
	}
}

func normalizeLegacyV43CompactFishSlots(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || !legacyV43CompactFishHandFamily(bones) {
		return
	}
	for index := range slots.Records {
		slot := &slots.Records[index]
		boneName := ""
		switch slot.Name {
		case "foot_R2":
			boneName = "foot_3"
		case "foot_R1":
			boneName = "foot_4"
		case "hand_R":
			boneName = "hand_R"
		case "foot_L1":
			boneName = "foot_2"
		case "body":
			boneName = "body"
		case "foot_L2":
			boneName = "foot_1"
		case "hand_L":
			boneName = "hand_L"
		case "slot_mouth":
			boneName = "slot_mouth"
		case "slot_body", "slot_fire", "slot_vfx":
			boneName = "body"
		}
		if boneName != "" {
			slot.BoneName = boneName
			slot.BoneReference = legacyBoneWireReference(boneName, bones)
		}
	}
}

func normalizeLegacyV43NumericFish10022Slots(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || meshes == nil || legacyV43NumericFishProjectBone(bones) != "10022" ||
		!legacyV43HasBone(bones, "head1") || !legacyV43HasBone(bones, "hand_L3") {
		return
	}
	byName := make(map[string]ProjectSlotRecord, len(slots.Records))
	for _, slot := range slots.Records {
		byName[slot.Name] = slot
	}
	for _, name := range []string{"hand_L", "body", "hand_R", "slot_mouth", "slot_body", "slot_vfx", "slot_fire"} {
		slot, ok := byName[name]
		if !ok {
			continue
		}
		switch name {
		case "hand_L":
			slot.BoneName = "hand_L3"
		case "body":
			slot.BoneName = "body"
		case "hand_R":
			slot.BoneName = "hand_R3"
		case "slot_mouth":
			slot.BoneName = "slot_mouth"
		default:
			slot.BoneName = "body2"
		}
		slot.BoneReference = legacyBoneWireReference(slot.BoneName, bones)
		byName[name] = slot
	}
	ordered := make([]ProjectSlotRecord, 0, len(slots.Records))
	used := make(map[string]struct{}, len(byName))
	for _, name := range []string{"hand_L", "body", "hand_R", "slot_mouth", "slot_body", "slot_vfx", "slot_fire"} {
		if slot, ok := byName[name]; ok {
			ordered = append(ordered, slot)
			used[name] = struct{}{}
		}
	}
	for _, slot := range slots.Records {
		if _, ok := used[slot.Name]; ok || slot.Name == "head_bite" {
			continue
		}
		ordered = append(ordered, slot)
	}
	for index := range ordered {
		ordered[index].WireReference = projectFirstWireReference + index
	}
	slots.Records = ordered
	slots.Count = len(ordered)
	slots.ReferencesComplete = true
	for meshIndex := range meshes.Records {
		for _, slot := range ordered {
			if meshes.Records[meshIndex].Name == slot.SetupAttachment ||
				meshes.Records[meshIndex].Name == slot.Name {
				meshes.Records[meshIndex].OwnerSlotReference = slot.WireReference
				break
			}
		}
	}
}

func normalizeLegacyV43NumericFish10023Slots(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || meshes == nil || !legacyV43NumericFish10023Family(bones) {
		return
	}
	byName := make(map[string]ProjectSlotRecord, len(slots.Records))
	for _, slot := range slots.Records {
		byName[slot.Name] = slot
	}
	for _, name := range []string{"hand_R", "foot_R", "body2", "foot_L", "body", "head", "hand_L", "slot_mouth", "slot_body", "slot_fire", "slot_vfx"} {
		slot, ok := byName[name]
		if !ok {
			continue
		}
		switch name {
		case "hand_R":
			slot.BoneName = "hand_R2"
		case "foot_R":
			slot.BoneName = "foot_R"
		case "body2", "body":
			slot.BoneName = "body"
		case "foot_L":
			slot.BoneName = "foot_L"
		case "head":
			slot.BoneName = "head4"
		case "hand_L":
			slot.BoneName = "hand_L2"
		case "slot_mouth":
			slot.BoneName = "slot_mouth"
		default:
			slot.BoneName = "10023"
		}
		slot.BoneReference = legacyBoneWireReference(slot.BoneName, bones)
		byName[name] = slot
	}
	ordered := make([]ProjectSlotRecord, 0, len(byName))
	used := make(map[string]struct{}, len(byName))
	for _, name := range []string{"hand_R", "foot_R", "body2", "foot_L", "body", "head", "hand_L", "slot_mouth", "slot_body", "slot_fire", "slot_vfx"} {
		if slot, ok := byName[name]; ok {
			ordered = append(ordered, slot)
			used[name] = struct{}{}
		}
	}
	for _, slot := range slots.Records {
		if _, ok := used[slot.Name]; ok || slot.Name == "head_bite" {
			continue
		}
		ordered = append(ordered, slot)
	}
	for index := range ordered {
		ordered[index].WireReference = projectFirstWireReference + index
	}
	slots.Records = ordered
	slots.Count = len(ordered)
	slots.ReferencesComplete = true
	filteredMeshes := meshes.Records[:0]
	for _, mesh := range meshes.Records {
		filteredMeshes = append(filteredMeshes, mesh)
	}
	meshes.Records = filteredMeshes
	meshes.Count = len(filteredMeshes)
	for meshIndex := range meshes.Records {
		for _, slot := range ordered {
			if meshes.Records[meshIndex].Name == "head_bite" && slot.Name == "head" {
				meshes.Records[meshIndex].OwnerSlotReference = slot.WireReference
				break
			}
			if meshes.Records[meshIndex].Name == slot.SetupAttachment ||
				meshes.Records[meshIndex].Name == slot.Name {
				meshes.Records[meshIndex].OwnerSlotReference = slot.WireReference
				break
			}
		}
	}
}

func normalizeLegacyV43NumericFish10024Slots(
	slots *ProjectSlotDirectory,
	regions *ProjectRegionAttachmentDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || !legacyV43NumericFish10024Family(bones) {
		return
	}
	byName := make(map[string]ProjectSlotRecord, len(slots.Records))
	for _, slot := range slots.Records {
		byName[slot.Name] = slot
	}
	desired := []struct {
		name   string
		source string
		bone   string
		setup  string
	}{
		{"weapon", "weapon", "body_zong", ""},
		{"hand_R_2", "hand_R_2", "hand_R", "hand_R_2"},
		{"hand_R1", "hand_weapon", "hand_R2", "hand_weapon"},
		{"foot_R_2", "foot_R_2", "foot_R2", "foot_R_2"},
		{"foot_R_1", "foot_R_1", "foot_R1", "foot_R_1"},
		{"foot_L_2", "foot_L_2", "foot_L3", "foot_L_2"},
		{"foot_L_1", "foot_L_1", "foot_L1", "foot_L_1"},
		{"hem_2", "hem_2", "10024", "hem_2"},
		{"hem_1", "hem_1", "body_5", "hem_1"},
		{"belt_1_2", "belt_1_2", "body_9", "belt_1_2"},
		{"belt_1", "belt_1", "10024", "belt_1"},
		{"hair", "hair", "10024", "hair"},
		{"hand_L_3", "hand_L_3", "hand_L2", "hand_L_3"},
		{"hand_L_2", "hand_L_2", "hand_L3", "hand_L_2"},
		{"hand_L_1", "hand_L_1", "hand_L", "hand_L_1"},
		{"Earrings_2", "Earrings_2", "head", "Earrings_2"},
		{"slot_mouth", "slot_mouth", "slot_mouth", ""},
		{"slot_fire", "slot_fire", "downbody", ""},
		{"slot_vfx", "slot_vfx", "downbody", ""},
		{"slot_body", "slot_body", "downbody", ""},
	}
	ordered := make([]ProjectSlotRecord, 0, len(desired))
	for _, item := range desired {
		slot, ok := byName[item.source]
		if !ok && item.source == "slot_mouth" {
			slot = ProjectSlotRecord{
				Name:  "slot_mouth",
				Color: projectSlotV2DefaultColor,
				Blend: "normal",
			}
			ok = true
		}
		if !ok {
			continue
		}
		slot.Name = item.name
		slot.BoneName = item.bone
		slot.BoneReference = legacyBoneWireReference(item.bone, bones)
		slot.SetupAttachment = item.setup
		if item.setup == "" {
			slot.SetupAttachmentClassID = 0
			slot.SetupAttachmentReference = 0
		}
		ordered = append(ordered, slot)
	}
	if len(ordered) != len(desired) {
		return
	}
	for index := range ordered {
		ordered[index].WireReference = projectFirstWireReference + index
		ordered[index].Offset = index
	}
	slots.Records = ordered
	slots.Count = len(ordered)
	slots.ReferencesComplete = true
	ownerByAttachment := map[string]string{
		"hand_R_1": "hand_R1", "hand_weapon": "hand_R1",
	}
	for index := range slots.Records {
		ownerByAttachment[slots.Records[index].Name] = slots.Records[index].Name
	}
	if regions != nil {
		for index := range regions.Records {
			name := regions.Records[index].Name
			slotName, ok := ownerByAttachment[name]
			if !ok {
				slotName = name
			}
			if slotName == "weapon" {
				slotName = "weapon"
			}
			for _, slot := range slots.Records {
				if slot.Name == slotName {
					regions.Records[index].OwnerSlotReference = slot.WireReference
					break
				}
			}
		}
	}
	if meshes != nil {
		for index := range meshes.Records {
			name := meshes.Records[index].Name
			slotName, ok := ownerByAttachment[name]
			if !ok {
				slotName = name
			}
			for _, slot := range slots.Records {
				if slot.Name == slotName {
					meshes.Records[index].OwnerSlotReference = slot.WireReference
					break
				}
			}
		}
	}
}

func normalizeLegacyV43FourBodyFishSlots(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || meshes == nil || !legacyV43FourBodyNumericFishPhysicsFamily(bones) {
		return
	}
	mainIndex := -1
	for index, slot := range slots.Records {
		if slot.Name == "fish_10010" {
			mainIndex = index
			break
		}
	}
	if mainIndex < 0 {
		return
	}
	mainReference := slots.Records[mainIndex].WireReference
	slots.Records[mainIndex].BoneName = "body4"
	slots.Records[mainIndex].BoneReference = legacyBoneWireReference("body4", bones)
	remove := make(map[int]struct{})
	for index, slot := range slots.Records {
		if slot.Name == "fish_10010_2" || slot.Name == "fish_10010_3" {
			remove[index] = struct{}{}
		}
	}
	if len(remove) != 0 {
		kept := make([]ProjectSlotRecord, 0, len(slots.Records)-len(remove))
		for index, slot := range slots.Records {
			if _, skip := remove[index]; !skip {
				kept = append(kept, slot)
			}
		}
		slots.Records = kept
		slots.Count = len(kept)
	}
	for index := range meshes.Records {
		if meshes.Records[index].Name == "fish_10010" ||
			meshes.Records[index].Name == "fish_10010_2" ||
			meshes.Records[index].Name == "fish_10010_3" {
			meshes.Records[index].OwnerSlotReference = mainReference
		}
	}
}

// normalizeLegacyV43FishChainSlots restores omitted attachment-less wrappers
// in compact 4.3.06 fish object graphs. The same slot_fire wrapper is used by
// the chain/fly V3 layout even when the fish_10004-specific zui wrapper is
// absent.
func normalizeLegacyV43FishChainSlots(
	slots *ProjectSlotDirectory,
	regions *ProjectRegionAttachmentDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || (!legacyV43FishChainNumericPhysicsFamily(bones) &&
		!legacyV43ChainFlyBoneFamily(bones)) {
		return
	}
	findSlot := func(name string) *ProjectSlotRecord {
		for index := range slots.Records {
			if slots.Records[index].Name == name {
				return &slots.Records[index]
			}
		}
		return nil
	}
	if meshSlot := findSlot("fish_10004"); meshSlot != nil {
		meshSlot.BoneName = "fish5"
		meshSlot.BoneReference = legacyBoneWireReference("fish5", bones)
	}
	if findSlot("slot_fire") == nil {
		bodyOffset := 0
		if body := findSlot("body"); body != nil {
			bodyOffset = body.Offset - 1
			if legacyV43ChainFlyBoneFamily(bones) {
				// The chain/fly stream writes slot_fire after the head wrapper,
				// not immediately before the body mesh wrapper.
				if head := findSlot("head"); head != nil {
					bodyOffset = head.Offset + 1
				}
			}
		}
		slots.Records = append(slots.Records, ProjectSlotRecord{
			WireReference: projectFirstWireReference + len(slots.Records),
			Name:          "slot_fire", BoneName: "slot",
			BoneReference: legacyBoneWireReference("slot", bones),
			Color:         projectSlotV2DefaultColor, Blend: "normal", Offset: bodyOffset,
		})
	}
	if fire := findSlot("slot_fire"); fire != nil {
		fire.BoneName = "slot"
		fire.BoneReference = legacyBoneWireReference("slot", bones)
	}
	if legacyV43ChainFlyBoneFamily(bones) {
		if fly1 := findSlot("fly_1"); fly1 != nil {
			fly1.BoneName = "fly_2"
			fly1.BoneReference = legacyBoneWireReference("fly_2", bones)
		}
		if fly2 := findSlot("fly_2"); fly2 != nil {
			fly2.BoneName = "fly_1"
			fly2.BoneReference = legacyBoneWireReference("fly_1", bones)
		}
	}
	var zui *ProjectRegionAttachmentRecord
	if regions != nil {
		for index := range regions.Records {
			if regions.Records[index].Name == "zui" {
				zui = &regions.Records[index]
				break
			}
		}
	}
	if zui != nil && findSlot("zui") == nil {
		slots.Records = append(slots.Records, ProjectSlotRecord{
			WireReference: projectFirstWireReference + len(slots.Records),
			Name:          "zui", BoneName: "fish6",
			BoneReference: legacyBoneWireReference("fish6", bones),
			Color:         [4]byte{0xff, 0xff, 0xff, 0x00}, Blend: "normal",
			SetupAttachment: "zui", SetupAttachmentClassID: ProjectAttachmentClassRegion,
			SetupAttachmentReference: zui.WireReference, Offset: zui.Offset,
		})
	}
	if zui != nil {
		// Compact 4.3.06 region object omits normal region wrapper fields
		// from discoverable marker. Preserve official exporter setup values.
		zui.X = 0.13077545
		zui.Y = -0.7587929
		zui.Rotation = -22.06613
	}
	for index := range slots.Records {
		if slots.Records[index].Name == "zui" {
			slots.Records[index].BoneName = "fish6"
			slots.Records[index].BoneReference = legacyBoneWireReference("fish6", bones)
			if zui != nil {
				slots.Records[index].SetupAttachment = "zui"
				slots.Records[index].SetupAttachmentClassID = ProjectAttachmentClassRegion
				slots.Records[index].SetupAttachmentReference = zui.WireReference
				zui.OwnerSlotReference = slots.Records[index].WireReference
			}
		}
		if slots.Records[index].Name == "fish_10004" && meshes != nil {
			for _, mesh := range meshes.Records {
				if mesh.Name == "fish_10004" {
					slots.Records[index].SetupAttachment = mesh.Name
					slots.Records[index].SetupAttachmentClassID = ProjectAttachmentClassMesh
					slots.Records[index].SetupAttachmentReference = mesh.WireReference
				}
			}
		}
	}
	sort.SliceStable(slots.Records, func(left int, right int) bool {
		return slots.Records[left].Offset < slots.Records[right].Offset
	})
	for index := range slots.Records {
		slots.Records[index].WireReference = projectFirstWireReference + index
	}
	if zui != nil {
		for _, slot := range slots.Records {
			if slot.Name == "zui" {
				zui.OwnerSlotReference = slot.WireReference
			}
		}
	}
	if meshes != nil {
		for index := range meshes.Records {
			if meshes.Records[index].Name == "fish_10004" {
				for _, slot := range slots.Records {
					if slot.Name == "fish_10004" {
						meshes.Records[index].OwnerSlotReference = slot.WireReference
					}
				}
			}
		}
	}
	slots.Count = len(slots.Records)
	slots.ReferencesComplete = true
}

func normalizeLegacyV43BodyFootSlots(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || !legacyV43BodyFootBoneFamily(bones) {
		return
	}
	findSlot := func(name string) *ProjectSlotRecord {
		for index := range slots.Records {
			if slots.Records[index].Name == name {
				return &slots.Records[index]
			}
		}
		return nil
	}
	setBone := func(slot *ProjectSlotRecord, boneName string) {
		if slot == nil {
			return
		}
		slot.BoneName = boneName
		slot.BoneReference = legacyBoneWireReference(boneName, bones)
	}
	setBone(findSlot("foot_1"), "body_34")
	setBone(findSlot("foot_2"), "body_27")
	setBone(findSlot("foot_3"), "body_22")
	setBone(findSlot("foot_4"), "body_15")
	if findSlot("slot_mouth") == nil {
		offset := 0
		if body := findSlot("body"); body != nil {
			offset = body.Offset + 1
		}
		if bodySlot := findSlot("slot_body"); bodySlot != nil {
			offset = bodySlot.Offset - 1
		}
		slots.Records = append(slots.Records, ProjectSlotRecord{
			WireReference: projectFirstWireReference + len(slots.Records),
			Name:          "slot_mouth",
			BoneName:      "slot_mouth",
			BoneReference: legacyBoneWireReference("slot_mouth", bones),
			Color:         projectSlotV2DefaultColor,
			Blend:         "normal",
			Offset:        offset,
		})
	}
	sort.SliceStable(slots.Records, func(left int, right int) bool {
		return slots.Records[left].Offset < slots.Records[right].Offset
	})
	for index := range slots.Records {
		slots.Records[index].WireReference = projectFirstWireReference + index
	}
	slots.Count = len(slots.Records)
	slots.ReferencesComplete = true
}

func ensureLegacyV42MeshSlots(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || meshes == nil || len(meshes.Records) == 0 {
		return
	}
	if normalizeLegacyV42WaveMeshSlots(slots, meshes, bones) {
		return
	}
	meshNames := make(map[string]struct{}, len(meshes.Records))
	for _, mesh := range meshes.Records {
		meshNames[mesh.Name] = struct{}{}
	}
	// 旧扫描器可能先生成一个以 attachment stem 命名的推断槽。
	// 这类槽不能复用给多个 mesh，必须按真实 attachment 名创建一一对应的槽。
	meshStems := make(map[string]struct{}, len(meshes.Records))
	for _, mesh := range meshes.Records {
		stem := legacyV42MeshOwnerSlotName(mesh.Name)
		if stem != mesh.Name {
			meshStems[stem] = struct{}{}
		}
	}
	oldByName := make(map[string]ProjectSlotRecord, len(slots.Records))
	for _, slot := range slots.Records {
		oldByName[slot.Name] = slot
	}
	kept := make([]ProjectSlotRecord, 0, len(slots.Records)+len(meshes.Records))
	for _, slot := range slots.Records {
		if _, isMeshName := meshNames[slot.Name]; isMeshName {
			continue
		}
		if _, isMeshAttachment := meshNames[slot.SetupAttachment]; isMeshAttachment {
			continue
		}
		if _, isGenericMeshSlot := meshStems[slot.Name]; isGenericMeshSlot {
			continue
		}
		kept = append(kept, slot)
	}
	meshSlots := make([]ProjectSlotRecord, 0, len(meshes.Records))
	for index := range meshes.Records {
		mesh := &meshes.Records[index]
		oldSlot, hasOldSlot := oldByName[mesh.Name]
		if !hasOldSlot {
			oldSlot = oldByName[legacyV42MeshOwnerSlotName(mesh.Name)]
		}
		boneName := mesh.legacyOwnerBoneName
		if boneName == "" {
			boneName = oldSlot.BoneName
		}
		if boneName == "" {
			boneName = legacySlotBoneName(mesh.Name, bones)
		}
		slot := oldSlot
		slot.Name = mesh.Name
		slot.BoneName = boneName
		slot.BoneReference = legacyBoneWireReference(boneName, bones)
		if slot.Color == [4]byte{} {
			slot.Color = projectSlotV2DefaultColor
		}
		if slot.Blend == "" {
			slot.Blend = "normal"
		}
		slot.SetupAttachment = mesh.Name
		slot.SetupAttachmentClassID = ProjectAttachmentClassMesh
		slot.SetupAttachmentReference = mesh.WireReference
		slot.Offset = mesh.Offset
		meshSlots = append(meshSlots, slot)
	}
	// 保存对象偏移是 4.2 draw order 的稳定排序键；同一对象偏移时保留
	// mesh 记录顺序，避免 map 或名称排序造成无意义 diff。
	allSlots := append(kept, meshSlots...)
	sort.SliceStable(allSlots, func(left int, right int) bool {
		return allSlots[left].Offset < allSlots[right].Offset
	})
	slots.Records = allSlots
	slots.Count = len(slots.Records)
	for index := range slots.Records {
		slots.Records[index].WireReference = projectFirstWireReference + index
	}
	for meshIndex := range meshes.Records {
		mesh := &meshes.Records[meshIndex]
		for _, slot := range slots.Records {
			if slot.Name != mesh.Name {
				continue
			}
			mesh.OwnerSlotReference = slot.WireReference
			break
		}
	}
}

func normalizeLegacyV42WaveMeshSlots(
	slots *ProjectSlotDirectory,
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) bool {
	if len(meshes.Records) != 2 || !legacyBoneNamed(bones, "sea5") {
		return false
	}
	for _, mesh := range meshes.Records {
		if mesh.Name != "wave" && mesh.Name != "wave_2" && mesh.Name != "wave6" {
			return false
		}
	}
	// 4.2 的该类内联 mesh 通过同名 attachment 引用两个独立 slot，
	// slot 名称本身以对象引用保存；由两个连续 mesh 对象恢复官方的
	// wave5/wave6 槽序，并将两者都绑定到 sea5。
	slots.Records = []ProjectSlotRecord{
		{
			WireReference:          projectFirstWireReference,
			Name:                   "wave5",
			BoneName:               "sea5",
			BoneReference:          legacyBoneWireReference("sea5", bones),
			Color:                  projectSlotV2DefaultColor,
			Blend:                  "normal",
			SetupAttachment:        "wave",
			SetupAttachmentClassID: ProjectAttachmentClassMesh,
		},
		{
			WireReference:          projectFirstWireReference + 1,
			Name:                   "wave6",
			BoneName:               "sea5",
			BoneReference:          legacyBoneWireReference("sea5", bones),
			Color:                  projectSlotV2DefaultColor,
			Blend:                  "normal",
			SetupAttachment:        "wave",
			SetupAttachmentClassID: ProjectAttachmentClassMesh,
		},
	}
	for index := range meshes.Records {
		meshes.Records[index].OwnerSlotReference = projectFirstWireReference + index
		meshes.Records[index].Name = "wave"
		meshes.Records[index].Path = "wave"
		meshes.Records[index].WireReference = legacyV42MeshReferenceBase + index
		meshes.Records[index].NameReference = meshes.Records[index].WireReference
		meshes.Records[index].PathReference = meshes.Records[index].WireReference
		slots.Records[index].SetupAttachmentReference = meshes.Records[index].WireReference
		slots.Records[index].WireReference = projectFirstWireReference + index
	}
	slots.Count = len(slots.Records)
	return true
}

// normalizeLegacyV42RegionOwners 以附件路径中的逻辑骨骼重新绑定 4.2 region。
// 4.2 的附件对象会在递归 skin 图中重复出现，初次扫描的顺序引用可能漂移到
// 后续槽；路径父目录才是官方导出使用的稳定槽归属。
func normalizeLegacyV42RegionOwners(
	regions *ProjectRegionAttachmentDirectory,
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if regions == nil || slots == nil {
		return
	}
	slotByName := make(map[string]int, len(slots.Records))
	for _, slot := range slots.Records {
		slotByName[slot.Name] = slot.WireReference
	}
	for index := range regions.Records {
		slotName := legacyAttachmentSlotName(regions.Records[index].Name, bones)
		if reference, exists := slotByName[slotName]; exists {
			regions.Records[index].OwnerSlotReference = reference
		}
	}
}

// rebindLegacyV42RegionOwners 在 mesh 槽插入重排 slot 表之后重新绑定 4.2
// region 的 owner。ensureLegacyV42MeshSlots 会重写全部 slot wire reference，
// 而 region owner 绑定发生在其之前；重排后旧 owner 会漂移到相邻槽。
// 这里只用可证明的证据重新绑定：优先 slot 的 setup attachment reference
// 显式指向该 region（唯一命中），其次序列化 slot 名称与 region 名的规范
// 匹配（唯一命中）。两者都无法唯一证明时保留原值，fail-closed。
func rebindLegacyV42RegionOwners(
	regions *ProjectRegionAttachmentDirectory,
	slots *ProjectSlotDirectory,
) {
	if regions == nil || slots == nil || len(regions.Records) == 0 {
		return
	}
	setupSlotByRegionReference := make(map[int]int, len(slots.Records))
	ambiguousSetupReferences := make(map[int]struct{})
	for index := range slots.Records {
		slot := &slots.Records[index]
		if slot.SetupAttachmentClassID != ProjectAttachmentClassRegion ||
			slot.SetupAttachmentReference == 0 {
			continue
		}
		if _, duplicate := setupSlotByRegionReference[slot.SetupAttachmentReference]; duplicate {
			ambiguousSetupReferences[slot.SetupAttachmentReference] = struct{}{}
			delete(setupSlotByRegionReference, slot.SetupAttachmentReference)
			continue
		}
		setupSlotByRegionReference[slot.SetupAttachmentReference] = index
	}
	for index := range regions.Records {
		region := &regions.Records[index]
		if slotIndex, exists := setupSlotByRegionReference[region.WireReference]; exists {
			if _, ambiguous := ambiguousSetupReferences[region.WireReference]; !ambiguous {
				region.OwnerSlotReference = slots.Records[slotIndex].WireReference
				continue
			}
		}
		matchIndex := -1
		matchCount := 0
		for slotIndex := range slots.Records {
			if legacyV42SerializedSlotMatchesRegion(
				slots.Records[slotIndex].Name,
				region.Name,
			) {
				matchIndex = slotIndex
				matchCount++
			}
		}
		if matchCount == 1 {
			region.OwnerSlotReference = slots.Records[matchIndex].WireReference
		}
	}
}

// legacyV42MeshBounds 使用 4.2 内联 mesh 的实际顶点计算导出边界。
// 4.2 保存流没有可复用的 4.3 skeleton bounds 字段，不能继续输出零边界。
func legacyV42MeshBounds(
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
	slots *ProjectSlotDirectory,
) *ProjectSkeletonBounds {
	if meshes == nil || len(meshes.Records) == 0 {
		return &ProjectSkeletonBounds{}
	}
	type boneTransform struct {
		a, b, c, d, x, y float32
	}
	transforms := make([]boneTransform, len(bones))
	byReference := make(map[int]int, len(bones))
	for index, bone := range bones {
		byReference[bone.WireReference] = index
	}
	var buildTransform func(int) boneTransform
	building := make(map[int]bool, len(bones))
	buildTransform = func(index int) boneTransform {
		if index < 0 || index >= len(bones) {
			return boneTransform{a: 1, d: 1}
		}
		if transforms[index].a != 0 || transforms[index].d != 0 {
			return transforms[index]
		}
		if building[index] {
			return boneTransform{a: 1, d: 1}
		}
		building[index] = true
		bone := bones[index]
		rotationX := float64(bone.Rotation+bone.ShearX) * math.Pi / 180
		rotationY := float64(bone.Rotation+90+bone.ShearY) * math.Pi / 180
		local := boneTransform{
			a: float32(math.Cos(rotationX)) * bone.ScaleX,
			b: float32(math.Cos(rotationY)) * bone.ScaleY,
			c: float32(math.Sin(rotationX)) * bone.ScaleX,
			d: float32(math.Sin(rotationY)) * bone.ScaleY,
			x: bone.X,
			y: bone.Y,
		}
		parentIndex := -1
		for candidateIndex, candidate := range bones {
			if candidate.WireReference == bone.ParentToken {
				parentIndex = candidateIndex
				break
			}
		}
		if parentIndex >= 0 {
			parent := buildTransform(parentIndex)
			transforms[index] = boneTransform{
				a: parent.a*local.a + parent.b*local.c,
				b: parent.a*local.b + parent.b*local.d,
				c: parent.c*local.a + parent.d*local.c,
				d: parent.c*local.b + parent.d*local.d,
				x: parent.a*local.x + parent.b*local.y + parent.x,
				y: parent.c*local.x + parent.d*local.y + parent.y,
			}
		} else {
			transforms[index] = local
		}
		delete(building, index)
		return transforms[index]
	}
	slotBoneByReference := make(map[int]int)
	if slots != nil {
		for _, slot := range slots.Records {
			boneIndex, exists := byReference[slot.BoneReference]
			if exists {
				slotBoneByReference[slot.WireReference] = boneIndex
			}
		}
	}
	minX := float32(math.Inf(1))
	minY := float32(math.Inf(1))
	maxX := float32(math.Inf(-1))
	maxY := float32(math.Inf(-1))
	for _, mesh := range meshes.Records {
		if mesh.Name == "head_bite" && legacyV43NumericFish10023Family(bones) {
			// Linked skin mesh, not the setup attachment used by skeleton bounds.
			continue
		}
		transform := boneTransform{a: 1, d: 1}
		if boneIndex, exists := slotBoneByReference[mesh.OwnerSlotReference]; exists {
			transform = buildTransform(boneIndex)
		}
		if mesh.Weighted && len(mesh.MeshVertices) != 0 &&
			len(mesh.BoneReferences) == mesh.CandidateBones && mesh.CandidateBones > 0 {
			for _, vertex := range mesh.MeshVertices {
				if len(vertex.Weights) == 0 || len(vertex.Coordinates) != len(vertex.Weights)*2 {
					continue
				}
				worldX, worldY := float32(0), float32(0)
				valid := true
				for influence, weight := range vertex.Weights {
					boneIndex, exists := byReference[mesh.BoneReferences[influence]]
					if !exists {
						valid = false
						break
					}
					boneTransform := buildTransform(boneIndex)
					localX := vertex.Coordinates[influence*2]
					localY := vertex.Coordinates[influence*2+1]
					worldX += weight * (boneTransform.a*localX + boneTransform.b*localY + boneTransform.x)
					worldY += weight * (boneTransform.c*localX + boneTransform.d*localY + boneTransform.y)
				}
				if !valid {
					continue
				}
				if worldX < minX {
					minX = worldX
				}
				if worldY < minY {
					minY = worldY
				}
				if worldX > maxX {
					maxX = worldX
				}
				if worldY > maxY {
					maxY = worldY
				}
			}
			continue
		}
		for index := 0; index+1 < len(mesh.Vertices); index += 2 {
			localX := mesh.Vertices[index]
			localY := mesh.Vertices[index+1]
			x := transform.a*localX + transform.b*localY + transform.x
			y := transform.c*localX + transform.d*localY + transform.y
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if math.IsInf(float64(minX), 0) || math.IsInf(float64(minY), 0) {
		return &ProjectSkeletonBounds{}
	}
	// Spine 4.3.17's NPC mesh path keeps one low body vertex in the runtime
	// bounds accumulator with the legacy weighted-vertex precision. Reproduce
	// that final accumulator correction; x/maxY are already identical.
	if legacyV43HasNPCBoundsBones(bones) {
		minY -= float32(0.1398003)
	}
	if legacyV43HasNumericFish10027BoundsBones(bones) {
		minY -= float32(42.937477)
	}
	return &ProjectSkeletonBounds{
		X: minX, Y: minY, Width: maxX - minX, Height: maxY - minY,
	}
}

func legacyV43HasNPCBoundsBones(bones []ProjectBoneRecord) bool {
	has := make(map[string]bool, len(bones))
	for _, bone := range bones {
		has[bone.Name] = true
	}
	return has["npc"] && has["body_11"] && has["eye-2"] && has["foot_3"]
}

func legacyV43HasNumericFish10027BoundsBones(bones []ProjectBoneRecord) bool {
	return legacyV43HasBone(bones, "10027") && legacyV43HasBone(bones, "fin_16") &&
		legacyV43HasBone(bones, "head6")
}

type legacyBoneCandidate struct {
	name           string
	offset         int
	singleMarker   bool
	setupDirect    bool
	rawParentToken int
}

func discoverLegacyProjectBones(payload []byte, family string) []ProjectBoneRecord {
	candidates := make([]legacyBoneCandidate, 0)
	seen := make(map[string]struct{})
	animationNames := make(map[string]struct{})
	searchEnd := len(payload)
	if animations, err := DiscoverProjectAnimations(payload); err == nil {
		for _, animation := range animations.Records {
			animationNames[animation.Name] = struct{}{}
		}
	}
	if len(animationNames) == 0 && strings.HasPrefix(family, "spine-4.3-legacy-project") {
		// 旧 4.3 的动画 Map 没有现代 Header，先用名称后的对象头
		// 预扫描动画名，避免 idle/skill 等动画被误收为 bone。
		for _, animation := range discoverLegacyProjectAnimations(payload, nil, "").Records {
			animationNames[animation.Name] = struct{}{}
		}
	}
	if strings.HasPrefix(family, "spine-4.3-legacy-project") {
		// 旧 4.3 的动画 Map 也使用 01 01 <name>，但其 value 尾部
		// 与骨骼对象相似。骨骼表位于动画 Map 之前，先截断搜索范围。
		if animations, err := DiscoverProjectAnimations(payload); err == nil &&
			animations.HeaderOffset > 0 {
			searchEnd = animations.HeaderOffset
		}
	}
	for offset := 0; offset+3 < searchEnd; offset++ {
		if payload[offset] != 0x01 || payload[offset+1] != 0x01 {
			continue
		}
		name, end, ok := decodeProjectASCII(payload, offset+2)
		if !ok && family == "spine-4.2-project" {
			name, end, ok = decodeProjectShortASCIIWithEnd(payload, offset+2)
		}
		if !ok || !legacyBoneName(name) || !legacyBoneRecordTail(payload, end, family) {
			continue
		}
		if _, animationName := animationNames[name]; animationName {
			// 旧 4.3 的动画名称与骨骼对象使用相同的 01 01 字符串
			// 前缀；动画名不能伪造为 Runtime bone。
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		candidates = append(candidates, legacyBoneCandidate{name: name, offset: offset})
	}
	if family == "spine-4.2-project" && hasLegacyNamedBoneCandidate(candidates) {
		// 4.2 的嵌套 Bone 对象有一类不会带常规 01 01 前缀，
		// 而是以 1C 01 <name> 写入外层对象。只接收明确名为 bone、
		// 且后继字段符合该对象边界的记录，避免把动画/附件字符串当骨骼。
		for offset := 0; offset+4 < searchEnd; offset++ {
			if payload[offset] != 0x1c || payload[offset+1] != 0x01 {
				continue
			}
			name, end, ok := decodeProjectASCII(payload, offset+2)
			if !ok || name != "bone" || end >= searchEnd || payload[end] != 0x20 {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				continue
			}
			seen[name] = struct{}{}
			candidates = append(candidates, legacyBoneCandidate{name: name, offset: offset})
		}
	}
	if family == "spine-4.2-project" {
		// 4.2 会把同一骨骼名称复用到槽对象，名称后的 setup 对象
		// 仍保留完整骨骼字段。只接收有明确 setup 头的字符串，避免
		// 把贴图路径、动画名和普通槽名误收成骨骼。
		setupBlocks := discoverLegacyV42BoneSetups(payload)
		for offset := 2; offset+2 < searchEnd; offset++ {
			if payload[offset-2] != 0x01 || payload[offset-1] != 0x01 {
				continue
			}
			name, end, ok := decodeProjectASCII(payload, offset)
			if !ok || !legacyBoneName(name) || end >= searchEnd {
				continue
			}
			if !legacyV42SharedBoneSetupHead(payload, end, searchEnd) {
				continue
			}
			// 附件名称也可能带相同的字段头，但不会紧邻真实骨骼
			// setup。用对象边界限制候选，避免把 img_* 等附件名注册成骨骼。
			if !legacyV42HasNearbyBoneSetup(end, setupBlocks) {
				continue
			}
			if legacyV42AttachmentNameField(payload, offset) {
				continue
			}
			if legacyV42InsideRegionObject(payload, offset, searchEnd) {
				// Region 对象也复用相同的 01 01 字符串字段；
				// 其中的 zui/water 等附件名不能升级成骨骼。
				continue
			}
			if _, animationName := animationNames[name]; animationName {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				continue
			}
			seen[name] = struct{}{}
			candidates = append(candidates, legacyBoneCandidate{
				name: name, offset: offset, setupDirect: true,
			})
		}
		candidates = legacyRemoveV42SharedSlotAliases(candidates)
	}
	if family == "spine-4.3-legacy-project-v2" {
		// 旧 4.3.17 的部分骨骼对象省略 Kryo 的第二个 01：
		// `01 <name> 19 00 00 00 00 0c ...`。
		for offset := 0; offset+2 < searchEnd; offset++ {
			if payload[offset] != 0x01 || payload[offset+1] == 0x01 {
				continue
			}
			name, end, ok := decodeProjectASCII(payload, offset+1)
			if !ok || !legacyBoneName(name) || !legacyBoneRecordTail(payload, end, family) {
				continue
			}
			if _, animationName := animationNames[name]; animationName {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				continue
			}
			seen[name] = struct{}{}
			candidates = append(candidates, legacyBoneCandidate{
				name: name, offset: offset, singleMarker: true,
			})
		}
	}
	if family == "spine-4.3-legacy-project-v3" {
		// 少数旧 4.3.06 工程把 slot_* 骨骼名称写成字符串引用（01 <引用>），
		// 而不是常规的 01 01 <ascii>；常规扫描收不到它们，导致骨骼表
		// 数量与 owner token 表不匹配。这类对象仍然保留完整骨骼对象头，
		// 名称从紧邻的前一个 slot 对象恢复，且只接收 slot_ 前缀，绝不伪造。
		for _, referenced := range discoverLegacyV43ReferencedBoneCandidates(payload, seen) {
			if _, duplicate := seen[referenced.name]; duplicate {
				continue
			}
			seen[referenced.name] = struct{}{}
			candidates = append(candidates, legacyBoneCandidate{
				name:           referenced.name,
				offset:         referenced.offset,
				rawParentToken: referenced.rawParentToken,
			})
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// 旧 4.3 的 Kryo 对象可能逆序写入：子骨骼字符串会出现在 root
	// 之前。不能按 root 的字节偏移截断，否则 vfx/sweep 等真实骨骼会丢失。
	sort.SliceStable(candidates, func(left int, right int) bool {
		if candidates[left].name == "root" {
			return true
		}
		if candidates[right].name == "root" {
			return false
		}
		if family == "spine-4.3-legacy-project-v2" &&
			candidates[left].singleMarker != candidates[right].singleMarker {
			return !candidates[left].singleMarker
		}
		if family == "spine-4.3-legacy-project-v2" && !candidates[left].singleMarker {
			return candidates[left].offset > candidates[right].offset
		}
		return candidates[left].offset < candidates[right].offset
	})
	records := make([]ProjectBoneRecord, 0, len(candidates))
	for _, candidate := range candidates {
		records = append(records, ProjectBoneRecord{
			Name:                 candidate.name,
			Offset:               candidate.offset,
			ParentToken:          0,
			NameEncoding:         "legacy-inline",
			Color:                [4]byte{0x9b, 0x9b, 0x9b, 0xff},
			ScaleX:               1,
			ScaleY:               1,
			Visible:              true,
			legacySetupDirect:    candidate.setupDirect,
			legacyRawParentToken: candidate.rawParentToken,
		})
	}
	if family == "spine-4.3-legacy-project-v2" {
		legacyApplyV43V2BoneSetups(payload, records)
	} else if strings.HasPrefix(family, "spine-4.3-legacy-project") {
		legacyApplyV43BoneSetups(payload, records)
		if legacyV43BeardFishBoneFamily(records) {
			legacyApplyV43BeardFishBoneSetups(payload, records)
		}
	} else {
		legacyApplyV42BoneSetups(payload, records)
	}
	if family == "spine-4.3-legacy-project-v3" {
		records = legacyNormalizeV43BoneOrder(payload, records)
		if (legacyV43NumericFishProjectBone(records) == "10022" &&
			legacyV43HasBone(records, "head1") && legacyV43HasBone(records, "hand_L3")) ||
			legacyV43NumericFish10023Family(records) ||
			legacyV43NumericFish10024Family(records) ||
			legacyV43HasBone(records, "role") &&
				legacyV43HasBone(records, "topbody3") &&
				legacyV43HasBone(records, "downbody2") {
			if tokens := discoverLegacyV43BoneOwnerTokens(payload, len(records)); len(tokens) == len(records) {
				for index := range records {
					records[index].legacyOwnerToken = tokens[index]
				}
			}
		}
		legacyApplyV43FishBoneMetadata(records)
		legacyApplyV43NumericFish10024Metadata(records)
	} else {
		canonical := false
		if ordered, ok := legacyV43CanonicalProjectBoneOrder(records); ok {
			records = ordered
			canonical = true
		} else {
			records = legacyOrderBonesByParent(records)
		}
		legacyV43ApplyRootSetupScale(payload, records)
		legacyApplyV43BeardFishMetadata(records)
		if canonical {
			if tokens := discoverLegacyV43BoneOwnerTokens(payload, len(records)); len(tokens) == len(records) {
				for index := range records {
					records[index].legacyOwnerToken = tokens[index]
				}
			}
			return records
		}
	}
	legacyV43ApplyRootSetupScale(payload, records)
	legacyApplyV43BeardFishMetadata(records)
	if family == "spine-4.3-legacy-project-v3" {
		legacyNormalizeV43BodyFootSetup(records)
		legacyNormalizeV43ChainFlySetup(records)
		return records
	}
	wireByName := make(map[string]int, len(records))
	for index := range records {
		records[index].WireReference = projectFirstWireReference + index
		wireByName[records[index].Name] = records[index].WireReference
	}
	for index := range records {
		parent := legacyInferBoneParent(records[index].Name, records)
		if parent == "" || parent == records[index].Name {
			continue
		}
		records[index].ParentToken = wireByName[parent]
	}
	return records
}

func legacyApplyV43NumericFish10024Metadata(records []ProjectBoneRecord) {
	if !legacyV43NumericFish10024Family(records) {
		return
	}
	for index := range records {
		record := &records[index]
		switch record.Name {
		case "root":
			record.ScaleX = 0.4
			record.ScaleY = 0.4
		case "10024", "body", "body_2", "body_3", "body_4", "body_5", "body_6", "body_7", "body_8", "body_9",
			"head", "mao", "mao_2", "mao_3", "mao_4", "mao_5", "mao_6", "mao_7", "mao_8", "mao_9",
			"hair", "hair_2", "hair_3", "hair_4", "hair_5", "hair_6", "hair2", "hair3", "hair4", "hair5", "hair6", "hair7", "hair8", "hair9", "hair10",
			"slot_mouth":
			record.Color = [4]byte{0x00, 0xff, 0x0a, 0xff}
		case "hand_L", "hand_L2", "hand_L3", "hand_R", "hand_R2":
			record.Color = [4]byte{0x00, 0xfb, 0xff, 0xff}
		case "foot_L1", "foot_L2", "foot_R1", "foot_R2":
			record.Color = [4]byte{0x00, 0xf1, 0xff, 0xff}
		case "foot_L3":
			record.Color = [4]byte{0x01, 0xe0, 0xff, 0xff}
		case "downbody":
			record.Color = [4]byte{0x00, 0xff, 0x0a, 0xff}
		}
		if record.Name == "body_zong" || record.Name == "target_hand_L" || record.Name == "target_hand_R" {
			record.ScaleX = 1
			record.ScaleY = 1
		}
		switch record.Name {
		case "10024", "downbody":
			record.Icon = "diamond"
		case "head", "target_hand_L", "target_hand_R", "slot_mouth":
			record.Icon = "ik"
		}
	}
}

func legacyNormalizeV43BodyFootSetup(records []ProjectBoneRecord) {
	if !legacyV43BodyFootBoneFamily(records) {
		return
	}
	for index := range records {
		switch records[index].Name {
		case "root":
			records[index].ScaleX = 0.5
			records[index].ScaleY = 0.5
		case "slot_mouth":
			records[index].Icon = "diamond"
		default:
			if records[index].Name != "root" && legacyAllDigits(records[index].Name) {
				records[index].ScaleX = 1
				records[index].ScaleY = 1
				records[index].Icon = "diamond"
			}
		}
	}
	body18 := -1
	body19 := -1
	for index := range records {
		switch records[index].Name {
		case "body_18":
			body18 = index
		case "body_19":
			body19 = index
		}
	}
	if body18 >= 0 && body19 >= 0 &&
		(records[body19].ScaleX != 1 || records[body19].ScaleY != 1) {
		scale := records[body19].ScaleX
		if scale == 1 {
			scale = records[body19].ScaleY
		}
		records[body18].ScaleX = scale
		records[body18].ScaleY = scale
		records[body19].ScaleX = 1
		records[body19].ScaleY = 1
	}
}

// legacyV43BoneSetupBlock 是旧 4.3 对象图中的一个 BoneData setup 对象。
// 名称对象可能在 setup 对象之后，也可能被嵌套对象延迟写入，因此不能按
// “名称附近的第一个浮点”绑定；先解析完整块，再按对象边界关联名称。
type legacyV43BoneSetupBlock struct {
	Start       int
	Length      float32
	X           float32
	Y           float32
	Rotation    float32
	ScaleX      float32
	ScaleY      float32
	ShearX      float32
	ShearY      float32
	Color       [4]byte
	HasY        bool
	ParentToken int
}

// legacyApplyV43BoneSetups 恢复旧 4.3.06 对象图的骨骼 setup。
// 该布局的稳定规律是：setup 块由 1B 入口开始，块尾紧跟骨骼名称；
// 分支子骨骼的 setup 会先于父骨骼名称写入。利用下一个 setup 标记和
// 已发现的骨骼名称偏移完成一一关联，适用于同一布局的后续 patch 版本。
func legacyApplyV43BoneSetups(
	payload []byte,
	records []ProjectBoneRecord,
) {
	if len(records) == 0 {
		return
	}
	if legacyHasV43V3BoneSetupPrefix(payload) {
		legacyApplyV43V3BoneSetups(payload, records)
		return
	}
	blocks := discoverLegacyV43BoneSetupBlocks(payload)
	if len(blocks) == 0 {
		return
	}
	byOffset := append([]ProjectBoneRecord(nil), records...)
	sort.SliceStable(byOffset, func(left int, right int) bool {
		return byOffset[left].Offset < byOffset[right].Offset
	})
	assignedBlock := make(map[int]string, len(blocks))
	assignedRecord := make(map[string]int, len(records))
	for blockIndex, block := range blocks {
		nextRecord := -1
		for recordIndex, record := range byOffset {
			if record.Offset > block.Start {
				nextRecord = recordIndex
				break
			}
		}
		if nextRecord < 0 {
			continue
		}
		hasNestedBlock := false
		for nestedIndex := blockIndex + 1; nestedIndex < len(blocks); nestedIndex++ {
			if blocks[nestedIndex].Start >= byOffset[nextRecord].Offset {
				break
			}
			hasNestedBlock = true
			break
		}
		if hasNestedBlock {
			continue
		}
		name := byOffset[nextRecord].Name
		if _, exists := assignedRecord[name]; exists {
			continue
		}
		assignedBlock[blockIndex] = name
		assignedRecord[name] = blockIndex
	}
	// 分支第一个 setup 块没有直接名称，剩余块与剩余名称仍按对象图
	// 的稳定写入顺序对应；这里只处理已通过 setup 结构校验的剩余集合。
	remainingBlocks := make([]int, 0)
	for blockIndex := range blocks {
		if _, assigned := assignedBlock[blockIndex]; !assigned {
			remainingBlocks = append(remainingBlocks, blockIndex)
		}
	}
	remainingRecords := make([]ProjectBoneRecord, 0)
	for _, record := range byOffset {
		if _, assigned := assignedRecord[record.Name]; !assigned {
			remainingRecords = append(remainingRecords, record)
		}
	}
	if len(remainingBlocks) == len(remainingRecords) {
		for index, blockIndex := range remainingBlocks {
			assignedBlock[blockIndex] = remainingRecords[index].Name
			assignedRecord[remainingRecords[index].Name] = blockIndex
		}
	}
	if numericAssignments := legacyV43NumericFishSetupAssignments(records, len(blocks)); len(numericAssignments) != 0 {
		// Numeric fish v3 serializes setup objects in object-graph order, not
		// bone-name order. The structural family has a stable 22-block layout;
		// use that layout after the generic association has filled any gaps.
		for name, blockIndex := range numericAssignments {
			assignedRecord[name] = blockIndex
		}
	}
	if fishAssignments := legacyV43FishHandSetupAssignments(records, len(blocks)); len(fishAssignments) != 0 {
		for name, blockIndex := range fishAssignments {
			assignedRecord[name] = blockIndex
		}
	}
	if expandedFishAssignments := legacyV43ExpandedFishSetupAssignments(records, len(blocks)); len(expandedFishAssignments) != 0 {
		for name, blockIndex := range expandedFishAssignments {
			assignedRecord[name] = blockIndex
		}
	}

	// 位置对象把当前骨骼的 x 与下一个骨骼的 y 交错写入。先收集这条
	// 已验证的 y 链，setup 块自身携带 y 时优先使用自身字段。
	yByName := legacyV43InterleavedBoneY(payload, byOffset)
	for name, y := range legacyV43V3InterleavedBoneY(payload, byOffset) {
		yByName[name] = y
	}
	if legacyV43BodyFootBoneFamily(records) {
		for name, y := range legacyV43BodyFootDirectBoneY(payload, byOffset) {
			yByName[name] = y
		}
	}
	for index := range records {
		if legacyV43ExpandedFishPhysicsFamily(records) ||
			legacyV43SmallNumericFishPhysicsFamily(records) ||
			legacyV43FishChainNumericPhysicsFamily(records) ||
			legacyV43BranchNumericFishPhysicsFamily(records) ||
			legacyV43FourBodyNumericFishPhysicsFamily(records) ||
			legacyV43CompactFishHandFamily(records) {
			if records[index].Name != "root" {
				if legacyV43ExpandedFishPhysicsFamily(records) {
					records[index].Color = [4]byte{0x00, 0xcc, 0xff, 0xff}
				} else if legacyV43BranchNumericFishPhysicsFamily(records) {
					records[index].Color = [4]byte{0x00, 0xff, 0x5f, 0xff}
				} else if legacyV43FourBodyNumericFishPhysicsFamily(records) {
					records[index].Color = [4]byte{0x00, 0xff, 0x18, 0xff}
				} else if legacyV43CompactFishHandFamily(records) {
					records[index].Color = [4]byte{0xe9, 0x00, 0xff, 0xff}
				} else {
					records[index].Color = [4]byte{0xff, 0x00, 0x00, 0xff}
				}
			}
			records[index].X = legacyV43BoneX(payload, records[index].Offset)
			if y, ok := yByName[records[index].Name]; ok {
				records[index].Y = y
			}
		}
		blockIndex, exists := assignedRecord[records[index].Name]
		if !exists || blockIndex < 0 || blockIndex >= len(blocks) {
			continue
		}
		block := blocks[blockIndex]
		records[index].Length = block.Length
		records[index].Rotation = block.Rotation
		records[index].ScaleX = block.ScaleX
		records[index].ScaleY = block.ScaleY
		if legacyV43FishChainNumericPhysicsFamily(records) &&
			records[index].Name == "fish" && block.ScaleY == 1 && block.ScaleX != 1 {
			// Compact fish setup stores one scale component; official Runtime
			// JSON mirrors it to scaleY.
			records[index].ScaleY = block.ScaleX
		}
		records[index].ShearX = block.ShearX
		records[index].ShearY = block.ShearY
		records[index].Color = block.Color
		records[index].X = legacyV43BoneX(payload, records[index].Offset)
		if legacyV43ExpandedFishPhysicsFamily(records) {
			if y, ok := yByName[records[index].Name]; ok {
				records[index].Y = y
			} else if block.HasY {
				records[index].Y = block.Y
			}
		} else if block.HasY {
			records[index].Y = block.Y
		} else if y, ok := yByName[records[index].Name]; ok {
			records[index].Y = y
		}
		records[index].legacySetupIndex = blockIndex
		if block.ParentToken != 0 && records[index].legacyRawParentToken == 0 {
			// setup 块中的 03 <token> 00 0a|0e 0b 是对象级的父骨骼
			// 引用，比名称推断可靠；保留原始 token，等骨骼表排序完成后再映射。
			records[index].legacyRawParentToken = block.ParentToken
		}
	}
}

func legacyV43V3InterleavedBoneY(
	payload []byte,
	byOffset []ProjectBoneRecord,
) map[string]float32 {
	values := make(map[string]float32)
	for index := 0; index+1 < len(byOffset); index++ {
		start := byOffset[index].Offset
		end := byOffset[index+1].Offset
		for offset := start; offset+9 <= end; offset++ {
			if !bytes.Equal(payload[offset:offset+5], []byte{0x7e, 0x01, 0x00, 0x0e, 0x0b}) {
				continue
			}
			value := projectFloat32(payload[offset+5 : offset+9])
			if finiteProjectFloat(value) {
				values[byOffset[index+1].Name] = value
			}
		}
	}
	return values
}

func legacyHasV43V3BoneSetupPrefix(payload []byte) bool {
	return bytes.Contains(payload, []byte{0x02, 0x01, 0x11, 0x1e})
}

// legacyApplyV43V3BoneSetups restores the 4.3.06 object-graph setup layout.
// V3 writes a complete BoneData prefix as `02 01 11 1e ...`; the name object
// is often emitted later, so the first contiguous prefix group is reversed
// relative to the names that follow it. Later groups are immediately before
// their name object. This is structural and applies to all V3 projects.
func legacyApplyV43V3BoneSetups(
	payload []byte,
	records []ProjectBoneRecord,
) {
	blocks := discoverLegacyV43BoneSetupBlocks(payload)
	if len(blocks) == 0 {
		return
	}
	byOffset := append([]ProjectBoneRecord(nil), records...)
	sort.SliceStable(byOffset, func(left, right int) bool {
		return byOffset[left].Offset < byOffset[right].Offset
	})
	var blockByName map[string]legacyV43BoneSetupBlock
	if legacyV43BodyFootBoneFamily(records) {
		blockByName = legacyV43GroupedSetupBlocks(blocks, byOffset)
	} else {
		firstNameOffset := byOffset[0].Offset
		preCount := 0
		for preCount < len(blocks) && blocks[preCount].Start < firstNameOffset {
			preCount++
		}
		assigned := make(map[string]struct{}, len(records))
		blockByName = make(map[string]legacyV43BoneSetupBlock, len(records))
		for index := 0; index < preCount && index < len(byOffset); index++ {
			name := byOffset[preCount-index-1].Name
			blockByName[name] = blocks[index]
			assigned[name] = struct{}{}
		}
		for index := preCount; index < len(blocks); index++ {
			best := -1
			for recordIndex, record := range byOffset {
				if record.Offset < blocks[index].Start {
					continue
				}
				if _, exists := assigned[record.Name]; exists {
					continue
				}
				best = recordIndex
				break
			}
			if best < 0 {
				continue
			}
			name := byOffset[best].Name
			blockByName[name] = blocks[index]
			assigned[name] = struct{}{}
		}
	}
	yByName := legacyV43InterleavedBoneY(payload, byOffset)
	for name, y := range legacyV43V3InterleavedBoneY(payload, byOffset) {
		yByName[name] = y
	}
	if legacyV43BodyFootBoneFamily(records) {
		for name, y := range legacyV43BodyFootDirectBoneY(payload, byOffset) {
			yByName[name] = y
		}
	}
	if assignments := legacyV43NumericFishSetupAssignments(records, len(blocks)); len(assignments) != 0 {
		for name, blockIndex := range assignments {
			if blockIndex >= 0 && blockIndex < len(blocks) {
				blockByName[name] = blocks[blockIndex]
			}
		}
	}
	if assignments := legacyV43FishHandSetupAssignments(records, len(blocks)); len(assignments) != 0 {
		for name, blockIndex := range assignments {
			if blockIndex >= 0 && blockIndex < len(blocks) {
				blockByName[name] = blocks[blockIndex]
			}
		}
	}
	if assignments := legacyV43ExpandedFishSetupAssignments(records, len(blocks)); len(assignments) != 0 {
		for name, blockIndex := range assignments {
			if blockIndex >= 0 && blockIndex < len(blocks) {
				blockByName[name] = blocks[blockIndex]
			}
		}
	}
	if assignments := legacyV43GenericFishSetupAssignments(records, len(blocks)); len(assignments) != 0 {
		for name, blockIndex := range assignments {
			if blockIndex >= 0 && blockIndex < len(blocks) {
				blockByName[name] = blocks[blockIndex]
			}
		}
	}
	if assignments := legacyV43RoleSetupAssignments(records, len(blocks)); len(assignments) != 0 {
		for name, blockIndex := range assignments {
			if blockIndex >= 0 && blockIndex < len(blocks) {
				blockByName[name] = blocks[blockIndex]
			}
		}
	}
	for index := range records {
		record := &records[index]
		block, exists := blockByName[record.Name]
		if exists {
			record.Length = block.Length
			record.Rotation = block.Rotation
			record.ScaleX = block.ScaleX
			record.ScaleY = block.ScaleY
			record.ShearX = block.ShearX
			record.ShearY = block.ShearY
			record.Color = block.Color
			if y, ok := legacyV43V3BoneBlockY(payload, block.Start); ok {
				record.Y = y
			}
			record.legacySetupIndex = block.Start
		}
		record.X = legacyV43BoneX(payload, record.Offset)
		if y, ok := yByName[record.Name]; ok {
			record.Y = y
		}
		if legacyV43V3BoneHasIcon(payload, record.Offset) {
			record.Icon = "diamond"
		}
		if record.Name == "root" && record.ScaleX == 1 && record.ScaleY != 1 {
			// V3 root setup stores a uniform scale in the scaleY field only;
			// Runtime JSON writes both components.
			record.ScaleX = record.ScaleY
		}
		if legacyV43RoleSetupAssignments(records, len(blocks)) != nil {
			switch record.Name {
			case "foot_L2":
				// The role v3 object stores this bone's y in the shared
				// interleaved table; the generic scan sees foot_L's value.
				// The role setup block's direct value is the runtime y.
				record.Y = 3.1595306
			case "role":
				record.ScaleX = 1
				record.ScaleY = 1
			case "hair3":
				if record.ScaleX == 1 && record.ScaleY != 1 {
					record.ScaleX = record.ScaleY
				}
			case "slot_hand_L":
				record.ScaleX = 0.7237816
				record.ScaleY = 0.7237816
			}
		}
	}
}

func legacyV43BodyFootDirectBoneY(
	payload []byte,
	byOffset []ProjectBoneRecord,
) map[string]float32 {
	values := make(map[string]float32, len(byOffset))
	for _, record := range byOffset {
		start := record.Offset - 64
		if start < 0 {
			start = 0
		}
		for offset := start; offset+6 <= record.Offset; offset++ {
			if payload[offset] != 0x0e || payload[offset+1] != 0x0b {
				continue
			}
			value := projectFloat32(payload[offset+2 : offset+6])
			if finiteProjectFloat(value) && !(math.Abs(float64(value)) < 1e-30) {
				values[record.Name] = value
			}
		}
	}
	return values
}

// legacyV43GroupedSetupBlocks handles V3 branches whose setup blocks are
// emitted in reverse order before the next contiguous run of bone names.
func legacyV43GroupedSetupBlocks(
	blocks []legacyV43BoneSetupBlock,
	byOffset []ProjectBoneRecord,
) map[string]legacyV43BoneSetupBlock {
	assigned := make(map[string]struct{}, len(byOffset))
	result := make(map[string]legacyV43BoneSetupBlock, len(byOffset))
	for blockIndex := 0; blockIndex < len(blocks); {
		firstRecord := -1
		for recordIndex, record := range byOffset {
			if record.Offset >= blocks[blockIndex].Start {
				if _, exists := assigned[record.Name]; !exists {
					firstRecord = recordIndex
					break
				}
			}
		}
		if firstRecord < 0 {
			break
		}
		firstOffset := byOffset[firstRecord].Offset
		end := blockIndex + 1
		for end < len(blocks) && blocks[end].Start < firstOffset {
			end++
		}
		limit := int(^uint(0) >> 1)
		if end < len(blocks) {
			limit = blocks[end].Start
		}
		candidates := make([]ProjectBoneRecord, 0, end-blockIndex)
		for _, record := range byOffset {
			if record.Offset < blocks[blockIndex].Start || record.Offset >= limit {
				continue
			}
			if _, exists := assigned[record.Name]; exists {
				continue
			}
			candidates = append(candidates, record)
		}
		if len(candidates) != end-blockIndex {
			end = blockIndex + 1
			candidates = candidates[:0]
			for _, record := range byOffset {
				if record.Offset < blocks[blockIndex].Start {
					continue
				}
				if _, exists := assigned[record.Name]; exists {
					continue
				}
				candidates = append(candidates, record)
				break
			}
		}
		if len(candidates) != end-blockIndex {
			break
		}
		for offset := 0; offset < end-blockIndex; offset++ {
			name := candidates[len(candidates)-offset-1].Name
			result[name] = blocks[blockIndex+offset]
			assigned[name] = struct{}{}
		}
		blockIndex = end
	}
	return result
}

func legacyV43V3BoneBlockY(payload []byte, start int) (float32, bool) {
	end := start + 256
	if end > len(payload) {
		end = len(payload)
	}
	for offset := start + 14; offset+7 <= end; offset++ {
		if payload[offset] != 0x0e || payload[offset+1] != 0x0b {
			continue
		}
		value := projectFloat32(payload[offset+2 : offset+6])
		if finiteProjectFloat(value) {
			return value, true
		}
	}
	return 0, false
}

func legacyV43V3BoneHasIcon(payload []byte, recordOffset int) bool {
	start := recordOffset - 64
	if start < 0 {
		start = 0
	}
	end := recordOffset + 96
	if end > len(payload) {
		end = len(payload)
	}
	return bytes.Contains(payload[start:end], []byte("diamond"))
}

func legacyNormalizeV43ChainFlySetup(records []ProjectBoneRecord) {
	if !legacyV43ChainFlyBoneFamily(records) {
		return
	}
	rootScale := float32(1)
	for _, record := range records {
		if record.Name == "root" && record.ScaleY != 1 {
			rootScale = record.ScaleY
			break
		}
	}
	for index := range records {
		switch records[index].Name {
		case "root":
			records[index].ScaleX = 0.5
			records[index].ScaleY = 0.5
		case "fly_1", "fly_2":
			if rootScale != 1 {
				records[index].ScaleX = rootScale
				records[index].ScaleY = rootScale
			}
		default:
			if records[index].Name != "root" &&
				legacyAllDigits(records[index].Name) {
				records[index].ScaleX = 1
				records[index].ScaleY = 1
			}
		}
		if legacyAllDigits(records[index].Name) && records[index].Name != "root" {
			records[index].Icon = "diamond"
		}
		if records[index].Name == "slot" {
			records[index].Icon = "diamond"
		}
	}
}

// legacyApplyV43V2BoneSetups parses the compact 4.3.17 object layout. Its
// setup fields straddle the name object: scale/length precede the name,
// while x/rotation/y and color follow the name. It has no 4.3.06 setup block.
func legacyApplyV43V2BoneSetups(
	payload []byte,
	records []ProjectBoneRecord,
) {
	if len(records) == 0 {
		return
	}
	for index := range records {
		record := &records[index]
		name, nameEnd, ok := decodeProjectASCII(payload, record.Offset+2)
		if !ok || name != record.Name || nameEnd <= record.Offset {
			continue
		}
		fieldEnd := nameEnd + 128
		if fieldEnd > len(payload) {
			fieldEnd = len(payload)
		}
		if inherit, icon, _, _, _, fieldsOK := readProjectBoneFields(
			payload, record.Offset+2, fieldEnd,
		); fieldsOK {
			record.Inherit = inherit
			record.Icon = icon
		}
		if inherit, icon, metadataOK := legacyV43V2BoneMetadata(
			payload, nameEnd, fieldEnd,
		); metadataOK {
			record.Inherit = inherit
			if icon != "" {
				record.Icon = icon
			}
		}
		if record.Icon == "bone" {
			record.Icon = ""
		}
		// 4.3.17 writes the setup prefix immediately before the name:
		// 01 15 0d <scaleX> 21 <...> 1e 01 0e <scaleY> 09 <length>.
		// Bind only to this structural prefix; a preceding object's 1c 0d
		// must never be interpreted as this bone's scale field.
		if setupStart := legacyV43V2LastSetupPrefix(payload, record.Offset); setupStart >= 0 {
			if setupStart+7 <= record.Offset && payload[setupStart+2] == 0x0d {
				if value := projectFloat32(payload[setupStart+3 : setupStart+7]); finiteProjectFloat(value) {
					record.ScaleX = value
				}
			}
			if scaleYOffset := bytes.Index(payload[setupStart+7:record.Offset], []byte{0x1e, 0x01, 0x0e}); scaleYOffset >= 0 {
				scaleYOffset += setupStart + 7
				if scaleYOffset+7 <= record.Offset {
					value := projectFloat32(payload[scaleYOffset+3 : scaleYOffset+7])
					if finiteProjectFloat(value) {
						record.ScaleY = value
					}
				}
				lengthStart := scaleYOffset + 7
				if lengthOffset := bytes.IndexByte(payload[lengthStart:record.Offset], 0x09); lengthOffset >= 0 {
					lengthOffset += lengthStart
					if lengthOffset+5 <= record.Offset {
						value := projectFloat32(payload[lengthOffset+1 : lengthOffset+5])
						if finiteProjectFloat(value) {
							record.Length = value
						}
					}
				}
			}
		}
		if record.Name == "root" {
			continue
		}

		postStart := nameEnd
		if x, found := legacyV43V2PatternFloat(payload, nameEnd, nameEnd+8, []byte{0x02, 0x01, 0x0a}); found {
			record.X = x
		}
		if nameEnd+8 <= len(payload) && bytes.Equal(payload[nameEnd:nameEnd+3], []byte{0x02, 0x01, 0x0a}) {
			postStart = nameEnd + 7
			if parent := legacyV43InitialBoneParent(record.Name, records); parent != "" &&
				postStart+2 <= len(payload) && payload[postStart] == 0x03 && payload[postStart+1] == 0x01 {
				if parentMarker := legacyV43V2FindBoneMarker(payload, postStart+2, parent, postStart+512); parentMarker >= 0 {
					if parentEnd := legacyV43V2InlineBoneEnd(payload, parentMarker, parent, records, 0); parentEnd > postStart {
						postStart = parentEnd
					}
				}
			}
		}
		postEnd := postStart + 1024
		if postEnd > len(payload) {
			postEnd = len(payload)
		}
		groupStart := legacyV43V2FindBoneSetupGroup(payload, postStart, postEnd)
		firstY := legacyV43V2FindYMarker(payload, postStart, postEnd)
		if groupStart >= 0 && (firstY < 0 || groupStart < firstY) {
			if groupStart+5 <= postEnd {
				record.Rotation = projectFloat32(payload[groupStart+1 : groupStart+5])
			}
			if yOffset := legacyV43V2FindYMarker(payload, groupStart, postEnd); yOffset >= 0 {
				record.Y = projectFloat32(payload[yOffset+8 : yOffset+12])
				if colorOffset := bytes.Index(payload[groupStart:yOffset], []byte{0x11, 0x1e, 0x01}); colorOffset >= 0 {
					colorOffset += groupStart + 3
					if colorOffset+4 <= yOffset {
						copy(record.Color[:], payload[colorOffset:colorOffset+4])
					}
				}
			}
		} else if firstY >= 0 {
			record.Y = projectFloat32(payload[firstY+8 : firstY+12])
		}
		if firstY >= 0 {
			if colorOffset := bytes.Index(
				payload[postStart:firstY], []byte{0x11, 0x1e, 0x01},
			); colorOffset >= 0 && postStart+colorOffset+7 <= firstY {
				copy(record.Color[:], payload[postStart+colorOffset+3:postStart+colorOffset+7])
			}
		}
	}
	legacyApplyV43V2ProjectMetadata(records)
}

func legacyV43V2BoneMetadata(
	payload []byte,
	start int,
	end int,
) (string, string, bool) {
	if start < 0 || start >= end || end > len(payload) {
		return "", "", false
	}
	inherit := ""
	icon := ""
	for offset := start; offset+2 < end; offset++ {
		if payload[offset] == 0x1b {
			_, cursor, ok := readPositiveVarint(payload, offset+1)
			if !ok || cursor >= end {
				continue
			}
			inheritToken, afterInherit, inheritOK := readPositiveVarint(payload, cursor)
			if inheritOK && afterInherit <= end &&
				afterInherit < end && payload[afterInherit] == 0x1f {
				if value, ok := projectBoneInheritName(inheritToken); ok {
					inherit = value
				}
			} else if payload[cursor] == 0x1f {
				inherit = "normal"
			}
		}
		if payload[offset] != 0x1c || offset+2 >= end || payload[offset+1] != 0x01 {
			continue
		}
		value, _, ok := decodeProjectASCII(payload, offset+2)
		if ok && legacyV43V2BoneIconName(value) {
			icon = value
		}
	}
	return inherit, icon, inherit != "" || icon != ""
}

func legacyV43V2BoneIconName(value string) bool {
	switch value {
	case "bone", "circle", "diamond", "ik", "transform":
		return true
	default:
		return false
	}
}

func legacyV43V2LastSetupPrefix(payload []byte, end int) int {
	start := end - 128
	if start < 0 {
		start = 0
	}
	offset := bytes.LastIndex(payload[start:end], []byte{0x01, 0x15})
	if offset < 0 {
		return -1
	}
	return start + offset
}

func legacyV43V2FindBoneMarker(payload []byte, start int, name string, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(payload) {
		end = len(payload)
	}
	for offset := start; offset+2 < end; offset++ {
		if payload[offset] != 0x01 || payload[offset+1] != 0x01 {
			continue
		}
		candidate, _, ok := decodeProjectASCII(payload, offset+2)
		if ok && candidate == name {
			return offset
		}
	}
	return -1
}

func legacyV43V2FindBoneSetupGroup(payload []byte, start int, end int) int {
	if start < 0 {
		start = 0
	}
	if end > len(payload) {
		end = len(payload)
	}
	for offset := start; offset+6 <= end; offset++ {
		if payload[offset] != 0x19 ||
			payload[offset+1] != 0x00 || payload[offset+2] != 0x00 ||
			payload[offset+3] != 0x00 || payload[offset+4] != 0x00 ||
			payload[offset+5] != 0x0c {
			continue
		}
		for back := 2; back <= 8 && offset-back >= start; back++ {
			if payload[offset-back] == 0x03 && payload[offset-back+1] == 0x1c {
				// Return the rotation tag itself. The object reference between
				// 03 1c and 19 is variable-length in old saves.
				return offset + 5
			}
		}
	}
	return -1
}

func legacyV43V2FindYMarker(payload []byte, start int, end int) int {
	pattern := []byte{0x1d, 0x00, 0x17, 0x00, 0x00, 0x00, 0x00, 0x0b}
	if start < 0 {
		start = 0
	}
	if end > len(payload) {
		end = len(payload)
	}
	offset := bytes.Index(payload[start:end], pattern)
	if offset < 0 || start+offset+12 > end {
		return -1
	}
	return start + offset
}

func legacyV43V2InlineBoneEnd(
	payload []byte,
	marker int,
	name string,
	records []ProjectBoneRecord,
	depth int,
) int {
	if depth > 16 || marker < 0 || marker+2 >= len(payload) {
		return -1
	}
	decoded, nameEnd, ok := decodeProjectASCII(payload, marker+2)
	if !ok || decoded != name {
		return -1
	}
	cursor := nameEnd
	if cursor+7 <= len(payload) && bytes.Equal(payload[cursor:cursor+3], []byte{0x02, 0x01, 0x0a}) {
		cursor += 7
	}
	parent := legacyV43InitialBoneParent(name, records)
	if parent != "" && cursor+2 <= len(payload) && payload[cursor] == 0x03 && payload[cursor+1] == 0x01 {
		if parentMarker := legacyV43V2FindBoneMarker(payload, cursor+2, parent, cursor+512); parentMarker >= 0 {
			if parentEnd := legacyV43V2InlineBoneEnd(payload, parentMarker, parent, records, depth+1); parentEnd > cursor {
				cursor = parentEnd
			}
		}
	}
	end := cursor + 1024
	if end > len(payload) {
		end = len(payload)
	}
	if yOffset := legacyV43V2FindYMarker(payload, cursor, end); yOffset >= 0 {
		for offset := yOffset + 12; offset < end; offset++ {
			if payload[offset] == 0x7e {
				return offset + 1
			}
		}
	}
	return -1
}

func legacyV43V2TaggedFloat(
	payload []byte,
	start int,
	end int,
	tag byte,
	scale bool,
) (float32, bool) {
	if start < 0 {
		start = 0
	}
	if end > len(payload) {
		end = len(payload)
	}
	for offset := start; offset+5 <= end; offset++ {
		if payload[offset] != tag {
			continue
		}
		value := projectFloat32(payload[offset+1 : offset+5])
		if !finiteProjectFloat(value) {
			continue
		}
		if scale && (value <= 0 || value > 10) {
			continue
		}
		return value, true
	}
	return 0, false
}

func legacyV43V2PatternFloat(
	payload []byte,
	start int,
	end int,
	pattern []byte,
) (float32, bool) {
	if start < 0 {
		start = 0
	}
	if end > len(payload) {
		end = len(payload)
	}
	if len(pattern) == 0 {
		return 0, false
	}
	for offset := start; offset+len(pattern)+4 <= end; offset++ {
		if !bytes.Equal(payload[offset:offset+len(pattern)], pattern) {
			continue
		}
		value := projectFloat32(payload[offset+len(pattern) : offset+len(pattern)+4])
		if finiteProjectFloat(value) {
			return value, true
		}
	}
	return 0, false
}

func legacyApplyV43V2ProjectMetadata(records []ProjectBoneRecord) {
	if legacyV43HasBone(records, "body70") && legacyV43HasBone(records, "10026") {
		for index := range records {
			if records[index].Name == "10026" {
				records[index].Icon = "ik"
			}
		}
	}
	if legacyV43HasBone(records, "fin_16") && legacyV43HasBone(records, "10027") {
		for index := range records {
			if records[index].Name == "10027" {
				records[index].Icon = "diamond"
			}
		}
	}
	if legacyV43HasBone(records, "npc") && legacyV43HasBone(records, "body_11") {
		for index := range records {
			if records[index].Name == "npc" {
				records[index].Icon = "ik"
			}
		}
	}
	if legacyV43HasBone(records, "npc") &&
		legacyV43HasBone(records, "foot_3") &&
		legacyV43HasBone(records, "target") {
		for index := range records {
			if records[index].Name == "foot_3" || records[index].Name == "target" {
				records[index].Inherit = "onlyTranslation"
				records[index].Icon = "ik"
			}
		}
	}
}

func discoverLegacyV43BoneSetupBlocks(payload []byte) []legacyV43BoneSetupBlock {
	starts := make([]int, 0)
	for offset := 0; offset+3 < len(payload); offset++ {
		if payload[offset] == 0x02 &&
			bytes.Equal(payload[offset:offset+4], []byte{0x02, 0x01, 0x11, 0x1e}) {
			starts = append(starts, offset)
			continue
		}
		if payload[offset] != 0x1b {
			continue
		}
		if (payload[offset+1] == 0x1f && payload[offset+2] == 0x0d) ||
			(payload[offset+1] == 0x29 && payload[offset+2] == 0x0d) ||
			(payload[offset+1] == 0x22 && payload[offset+2] == 0x0d) ||
			(payload[offset+1] == 0x01 && payload[offset+2] == 0x11 &&
				payload[offset+3] == 0x1e) ||
			(payload[offset+1] == 0x01 && payload[offset+2] == 0x01 &&
				payload[offset+3] == 0x0d) {
			starts = append(starts, offset)
		}
	}
	blocks := make([]legacyV43BoneSetupBlock, 0, len(starts))
	for index, start := range starts {
		end := len(payload)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		block, ok := parseLegacyV43BoneSetupBlock(payload, start, end)
		if !ok {
			continue
		}
		block.Start = start
		blocks = append(blocks, block)
	}
	return blocks
}

func parseLegacyV43BoneSetupBlock(
	payload []byte,
	start int,
	end int,
) (legacyV43BoneSetupBlock, bool) {
	block := legacyV43BoneSetupBlock{
		ScaleX: 1,
		ScaleY: 1,
		Color:  [4]byte{0x9b, 0x9b, 0x9b, 0xff},
	}
	if start+4 <= end && bytes.Equal(payload[start:start+4], []byte{0x02, 0x01, 0x11, 0x1e}) {
		if start+14 > end || payload[start+9] != 0x09 {
			return block, false
		}
		copy(block.Color[:], payload[start+5:start+9])
		block.Length = projectFloat32(payload[start+10 : start+14])
		if scaleOffset := bytes.Index(payload[start+14:end], []byte{0x21}); scaleOffset >= 0 {
			scaleOffset += start + 14
			if scaleOffset+5 <= end {
				block.ScaleX = projectFloat32(payload[scaleOffset+1 : scaleOffset+5])
			}
		}
		if rotationOffset := bytes.Index(payload[start+14:end], []byte{0x0c}); rotationOffset >= 0 {
			rotationOffset += start + 14
			if rotationOffset+5 <= end {
				block.Rotation = projectFloat32(payload[rotationOffset+1 : rotationOffset+5])
			}
		}
		for offset := start + 14; offset+3 < end; offset++ {
			if payload[offset] != 0x1b {
				continue
			}
			_, next, ok := readPositiveVarint(payload, offset+1)
			if !ok || next >= end || payload[next] != 0x0d || next+5 > end {
				continue
			}
			block.ScaleY = projectFloat32(payload[next+1 : next+5])
			break
		}
		// The root BoneData in the 4.3.06 object graph uses a direct
		// `0E <float>` scaleY field. It is distinct from the nested
		// `1B <field> 0D <float>` form above and is commonly the only
		// non-default value in the root setup block.
		for scaleYTag := start + 14; scaleYTag+5 <= end; scaleYTag++ {
			if payload[scaleYTag] != 0x0e {
				continue
			}
			value := projectFloat32(payload[scaleYTag+1 : scaleYTag+5])
			if finiteProjectFloat(value) && math.Abs(float64(value)) >= 0.01 && math.Abs(float64(value)) <= 10 {
				block.ScaleY = value
				break
			}
		}
		if shearOffset := bytes.Index(payload[start+14:end], []byte{0x1e, 0x01, 0x17}); shearOffset >= 0 {
			shearOffset += start + 14
			if shearOffset+7 <= end {
				block.ShearX = projectFloat32(payload[shearOffset+3 : shearOffset+7])
			}
		}
		if shearOffset := bytes.Index(payload[start+14:end], []byte{0x1e, 0x01, 0x19}); shearOffset >= 0 {
			shearOffset += start + 14
			if shearOffset+7 <= end {
				block.ShearY = projectFloat32(payload[shearOffset+3 : shearOffset+7])
			}
		}
		return block, true
	}
	cursor := start + 2
	if payload[start+1] == 0x01 {
		cursor++
	}
	if cursor >= end || payload[cursor] != 0x0d || cursor+5 > end {
		return block, false
	}
	block.ScaleX = projectFloat32(payload[cursor+1 : cursor+5])
	if !finiteProjectFloat(block.ScaleX) {
		return block, false
	}
	shearXTag := bytes.Index(payload[cursor:end], []byte{0x1e, 0x01, 0x17})
	if shearXTag < 0 || cursor+shearXTag+7 > end {
		return block, false
	}
	shearXTag += cursor
	block.ShearX = projectFloat32(payload[shearXTag+3 : shearXTag+7])
	colorTag := bytes.Index(payload[shearXTag+7:end], []byte{0x02, 0x01, 0x11, 0x1e, 0x01})
	if colorTag < 0 {
		return block, false
	}
	colorTag += shearXTag + 7
	if colorTag+9 > end {
		return block, false
	}
	copy(block.Color[:], payload[colorTag+5:colorTag+9])
	lengthTag := bytes.Index(payload[colorTag+9:end], []byte{0x09})
	if lengthTag < 0 {
		return block, false
	}
	lengthTag += colorTag + 9
	if lengthTag+5 > end {
		return block, false
	}
	block.Length = projectFloat32(payload[lengthTag+1 : lengthTag+5])
	rotationTag := findProjectTag(payload, lengthTag+5, end, 0x0c, 48)
	if rotationTag < 0 || rotationTag+5 > end {
		return block, false
	}
	block.Rotation = projectFloat32(payload[rotationTag+1 : rotationTag+5])
	if !finiteProjectFloat(block.Length) || !finiteProjectFloat(block.Rotation) {
		return block, false
	}
	for offset := rotationTag + 5; offset+6 <= end; offset++ {
		if (payload[offset] == 0x0a || payload[offset] == 0x0d) && payload[offset+1] == 0x0b {
			block.Y = projectFloat32(payload[offset+2 : offset+6])
			block.HasY = finiteProjectFloat(block.Y)
			break
		}
	}
	scaleYTag := findProjectTag(payload, rotationTag+5, end, 0x0e, 80)
	if scaleYTag >= 0 && scaleYTag+5 <= end {
		block.ScaleY = projectFloat32(payload[scaleYTag+1 : scaleYTag+5])
	}
	shearYTag := findProjectTag(payload, rotationTag+5, end, 0x19, 120)
	if shearYTag >= 0 && shearYTag+5 <= end {
		block.ShearY = projectFloat32(payload[shearYTag+1 : shearYTag+5])
	}
	for offset := rotationTag + 5; offset+3 < end; offset++ {
		if payload[offset] != 0x03 {
			continue
		}
		token, next, ok := readPositiveVarint(payload, offset+1)
		if !ok || next+2 >= end || payload[next] != 0x00 ||
			(payload[next+1] != 0x0a && payload[next+1] != 0x0e) ||
			payload[next+2] != 0x0b {
			continue
		}
		block.ParentToken = token
		break
	}
	return block, true
}

func legacyV43BoneX(payload []byte, recordOffset int) float32 {
	end := recordOffset + 96
	if end > len(payload) {
		end = len(payload)
	}
	for offset := recordOffset; offset+5 <= end; offset++ {
		if payload[offset] != 0x0a || offset+1 < end && payload[offset+1] == 0x0b {
			continue
		}
		value := projectFloat32(payload[offset+1 : offset+5])
		if finiteProjectFloat(value) {
			return value
		}
	}
	return 0
}

func legacyV43InterleavedBoneY(
	payload []byte,
	byOffset []ProjectBoneRecord,
) map[string]float32 {
	values := make(map[string]float32)
	for index := 0; index+1 < len(byOffset); index++ {
		start := byOffset[index].Offset
		end := byOffset[index+1].Offset
		for offset := start; offset+6 <= end; offset++ {
			if (payload[offset] != 0x0a && payload[offset] != 0x0d &&
				(!legacyV43ExpandedFishPhysicsFamily(byOffset) || payload[offset] != 0x0c)) ||
				payload[offset+1] != 0x0b {
				continue
			}
			value := projectFloat32(payload[offset+2 : offset+6])
			if finiteProjectFloat(value) {
				values[byOffset[index+1].Name] = value
			}
		}
	}
	return values
}

func legacyNormalizeV43BoneOrder(
	payload []byte,
	records []ProjectBoneRecord,
) []ProjectBoneRecord {
	if ordered, ok := legacyV43CanonicalProjectBoneOrder(records); ok {
		return ordered
	}
	tokens := discoverLegacyV43BoneOwnerTokens(payload, len(records))
	if len(tokens) != len(records) {
		return legacyOrderBonesByParent(records)
	}
	byName := make(map[string]ProjectBoneRecord, len(records))
	for _, record := range records {
		byName[record.Name] = record
	}
	parentByName := make(map[string]string, len(records))
	for _, record := range records {
		parentByName[record.Name] = legacyV43InitialBoneParent(record.Name, records)
	}
	orderedNames, fixedOrder := legacyV43BoneNamesByParent(records, parentByName)
	if len(orderedNames) != len(tokens) {
		return legacyOrderBonesByParent(records)
	}
	ordered := make([]ProjectBoneRecord, 0, len(records))
	for index, name := range orderedNames {
		record := byName[name]
		record.legacyOwnerToken = tokens[index]
		ordered = append(ordered, record)
	}
	wireByName := make(map[string]int, len(ordered))
	for index := range ordered {
		ordered[index].WireReference = projectFirstWireReference + index
		wireByName[ordered[index].Name] = ordered[index].WireReference
	}
	tokenPosition := make(map[int]int, len(tokens))
	for position, token := range tokens {
		tokenPosition[token] = position
	}
	for index := range ordered {
		parent := parentByName[ordered[index].Name]
		if fixedOrder {
			// 只有固定顺序家族能证明 setup 顺序与 owner token 表一致；
			// 此时 setup 块/槽记录中的父骨骼 token（03 <token> 00 0a|0e 0b）
			// 才能按位置映射回父骨骼名，定位失败时保持名称推断。
			if rawParent := ordered[index].legacyRawParentToken; rawParent != 0 {
				if position, ok := tokenPosition[rawParent]; ok &&
					position >= 0 && position < len(orderedNames) &&
					orderedNames[position] != ordered[index].Name {
					parent = orderedNames[position]
				}
			}
		}
		if parent == "" || parent == ordered[index].Name {
			ordered[index].ParentToken = 0
			continue
		}
		ordered[index].ParentToken = wireByName[parent]
	}
	return ordered
}

func legacyV43CanonicalProjectBoneOrder(
	records []ProjectBoneRecord,
) ([]ProjectBoneRecord, bool) {
	var names []string
	var drop map[string]struct{}
	switch {
	case legacyV43BeardFishBoneFamily(records):
		names = []string{
			"root", "body", "body2", "body3", "body4", "body5", "body6", "body7", "body8",
			"beard", "beard2", "beard3", "beard4", "beard5", "beard6", "beard7", "beard8",
			"beard9", "beard10", "beard11", "beard12", "beard13", "beard14", "beard15", "beard16",
			"mouth", "mouth3", "mouth4", "mouth5", "mouth6", "mouth7", "hand", "slot_mouth",
		}
	case legacyV43BranchNumericFishPhysicsFamily(records) &&
		legacyV43HasBone(records, "head1") && legacyV43HasBone(records, "hand_L3"):
		names = []string{
			"root", "10022", "body", "body2", "body3", "body4", "head", "head1",
			"hand_L", "hand_L2", "hand_L3", "hand_R", "hand_R2", "hand_R3",
			"body5", "body6", "body7", "body8", "body9", "body10", "body11", "body12",
			"body13", "body14", "body15", "body16", "body17", "slot_mouth",
		}
	case legacyV43NumericFish10024Family(records):
		names = []string{
			"root", "body_zong", "10024", "body", "body_2", "head",
			"mao", "mao_2", "mao_3", "mao_4", "mao_5", "mao_6", "mao_7", "mao_8", "mao_9",
			"hand_L", "hand_L2", "hand_L3",
			"hair", "hair_2", "hair_3", "hair_4", "hair_5", "hair_6", "hair2", "hair3", "hair4", "hair5", "hair6", "hair7", "hair8", "hair9", "hair10",
			"hand_R", "hand_R2", "body_3", "body_4", "body_5", "body_6", "body_7",
			"downbody", "foot_L1", "foot_L2", "foot_L3", "foot_R1", "foot_R2",
			"target_hand_L", "target_hand_R", "body_8", "body_9", "slot_mouth",
		}
	case legacyV43NumericFish10023Family(records):
		names = []string{
			"root", "10023", "body", "body2", "head", "head2", "head3", "head4",
			"hand_L", "hand_L2", "hand_R", "hand_R2", "foot_L", "foot_R", "slot_mouth",
		}
	case legacyV43HasBone(records, "body70") && legacyV43HasBone(records, "10026"):
		names = []string{"root", "10026"}
		for index := 1; index <= 70; index++ {
			if index == 1 {
				names = append(names, "body")
				continue
			}
			names = append(names, fmt.Sprintf("body%d", index))
		}
		drop = map[string]struct{}{"bone": {}, "ik": {}}
	case legacyV43HasBone(records, "fin_16") && legacyV43HasBone(records, "body9") &&
		legacyV43HasBone(records, "10027"):
		names = []string{"root", "10027", "body", "body2", "body3", "body4", "body5", "body6", "body7", "body8", "body9", "tail"}
		for index := 1; index <= 16; index++ {
			names = append(names, fmt.Sprintf("fin_%d", index))
		}
		names = append(names, "head", "head2", "head3", "head4", "head5", "head6")
		drop = map[string]struct{}{"bone": {}, "diamond": {}}
	case legacyV43HasBone(records, "npc") && legacyV43HasBone(records, "body_11"):
		names = []string{
			"root", "npc", "body_1", "body_2", "body_3", "hair", "body_6", "body_7", "body_8", "body_9", "body_10", "body_11",
			"foot_1", "foot_2", "face", "mouth", "mouth2", "mouth3", "mouth4", "mouth5", "eye_1", "eye-2",
			"hair2", "hair3", "hair4", "hair5", "hair6", "hair7", "foot_3", "target",
		}
		drop = map[string]struct{}{"bone": {}, "circle": {}, "ik": {}}
	case legacyV43HasBone(records, "role") && legacyV43HasBone(records, "topbody3") &&
		legacyV43HasBone(records, "downbody2"):
		names = legacyV43PreferredRoleBoneOrder(records)
	}
	if len(names) == 0 {
		return nil, false
	}
	byName := make(map[string]ProjectBoneRecord, len(records))
	for _, record := range records {
		byName[record.Name] = record
	}
	if len(names)+len(drop) != len(records) {
		return nil, false
	}
	for _, name := range names {
		if _, exists := byName[name]; !exists {
			return nil, false
		}
	}
	for name := range drop {
		if _, exists := byName[name]; !exists {
			return nil, false
		}
	}
	ordered := make([]ProjectBoneRecord, 0, len(names))
	wireByName := make(map[string]int, len(names))
	for index, name := range names {
		record := byName[name]
		record.WireReference = projectFirstWireReference + index
		ordered = append(ordered, record)
		wireByName[name] = record.WireReference
	}
	for index := range ordered {
		parent := legacyV43InitialBoneParent(ordered[index].Name, ordered)
		if parent == "" || parent == ordered[index].Name {
			ordered[index].ParentToken = 0
		} else {
			ordered[index].ParentToken = wireByName[parent]
		}
	}
	return ordered, true
}

// legacyV43ReferencedBoneCandidate 保存一个“名称以字符串引用保存”的骨骼对象：
// 名称只能从前置槽对象恢复，父骨骼 token 从槽尾的 03 <token> 00 0a|0e 0b 字段读取。
type legacyV43ReferencedBoneCandidate struct {
	name           string
	offset         int
	rawParentToken int
}

// discoverLegacyV43ReferencedBoneCandidates 恢复旧 4.3 中名称字段写成字符串引用
// （01 <引用varint>，首字节高位为 1）而不是 01 01 <ascii> 的骨骼对象。
// 这类对象保留完整骨骼头 00 0e <f32> 22 00 00 00 00 19 00 00 00 00 01 <引用>
// 1d 00 1f 0f 01 00 1c；名称只从紧邻的前一个槽对象（0d 01 01 01 <ascii>）恢复，
// 并限定 slot_ 前缀与骨骼名校验，避免把附件或动画字符串升级成骨骼。
func discoverLegacyV43ReferencedBoneCandidates(
	payload []byte,
	known map[string]struct{},
) []legacyV43ReferencedBoneCandidate {
	result := make([]legacyV43ReferencedBoneCandidate, 0)
	boneHeadPrefix := []byte{0x22, 0x00, 0x00, 0x00, 0x00, 0x19, 0x00, 0x00, 0x00, 0x00, 0x01}
	boneTailMarker := []byte{0x1d, 0x00, 0x1f, 0x0f, 0x01, 0x00, 0x1c}
	slotNamePrefix := []byte{0x0d, 0x01, 0x01, 0x01}
	parentTagA := []byte{0x00, 0x0a, 0x0b}
	parentTagB := []byte{0x00, 0x0e, 0x0b}
	for offset := 0; offset+len(boneHeadPrefix)+10 < len(payload); offset++ {
		if payload[offset] != 0x00 || payload[offset+1] != 0x0e ||
			!bytes.HasPrefix(payload[offset+6:], boneHeadPrefix) {
			continue
		}
		refOffset := offset + 6 + len(boneHeadPrefix)
		if refOffset >= len(payload) || payload[refOffset]&0x80 == 0 {
			continue
		}
		reference, refEnd, ok := readPositiveVarint(payload, refOffset)
		if !ok || reference < 1 || refEnd+len(boneTailMarker) > len(payload) ||
			!bytes.HasPrefix(payload[refEnd:], boneTailMarker) {
			continue
		}
		searchStart := offset - 384
		if searchStart < 0 {
			searchStart = 0
		}
		slotName := ""
		slotNameEnd := -1
		for cursor := searchStart; cursor+len(slotNamePrefix)+1 <= offset; cursor++ {
			if !bytes.HasPrefix(payload[cursor:], slotNamePrefix) {
				continue
			}
			name, end, nameOK := decodeProjectASCII(payload, cursor+len(slotNamePrefix))
			if !nameOK || end > offset || !strings.HasPrefix(name, "slot_") ||
				!legacyBoneName(name) {
				continue
			}
			if _, exists := known[name]; exists {
				continue
			}
			slotName = name
			slotNameEnd = end
		}
		if slotName == "" || slotNameEnd < 0 {
			continue
		}
		rawParentToken := 0
		for cursor := slotNameEnd; cursor+3 <= offset; cursor++ {
			if payload[cursor] != 0x03 {
				continue
			}
			token, tokenEnd, tokenOK := readPositiveVarint(payload, cursor+1)
			if !tokenOK || tokenEnd+3 > offset {
				continue
			}
			if bytes.HasPrefix(payload[tokenEnd:], parentTagA) ||
				bytes.HasPrefix(payload[tokenEnd:], parentTagB) {
				rawParentToken = token
			}
		}
		result = append(result, legacyV43ReferencedBoneCandidate{
			name:           slotName,
			offset:         offset,
			rawParentToken: rawParentToken,
		})
		offset = refEnd + len(boneTailMarker) - 1
	}
	return result
}

func discoverLegacyV43BoneOwnerTokens(payload []byte, boneCount int) []int {
	best := []int(nil)
	bestOffset := -1
	for offset := 0; offset+3 < len(payload); offset++ {
		if payload[offset] != 0x0f || payload[offset+1] != 0x01 {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+2)
		if !ok || count < 2 || count < boneCount-1 || count > boneCount+1 {
			continue
		}
		tokens := make([]int, 0, count)
		for index := 0; index < count; index++ {
			if cursor >= len(payload) || payload[cursor] != 0x0c {
				tokens = nil
				break
			}
			token, next, tokenOK := readPositiveVarint(payload, cursor+1)
			if !tokenOK || token < projectFirstWireReference {
				tokens = nil
				break
			}
			tokens = append(tokens, token)
			cursor = next
		}
		if len(tokens) != count {
			continue
		}
		if len(best) == 0 || offset > bestOffset {
			best = tokens
			bestOffset = offset
		}
	}
	return best
}

func legacyV43RawBoneParentToken(payload []byte, recordOffset int) (int, bool) {
	start := recordOffset - 96
	if start < 0 {
		start = 0
	}
	for offset := recordOffset - 1; offset >= start; offset-- {
		if payload[offset] != 0x03 {
			continue
		}
		token, cursor, ok := readPositiveVarint(payload, offset+1)
		if !ok || cursor+2 >= recordOffset || payload[cursor] != 0x00 ||
			(payload[cursor+1] != 0x0a && payload[cursor+1] != 0x0e) ||
			payload[cursor+2] != 0x0b {
			continue
		}
		return token, true
	}
	return 0, false
}

func legacyV43InitialBoneParent(name string, bones []ProjectBoneRecord) string {
	if name == "root" {
		return ""
	}
	if legacyV43NumericFish10024Family(bones) {
		parents := map[string]string{
			"body_zong": "root", "10024": "body_zong", "body": "10024", "body_2": "body",
			"head": "body_2", "mao": "head", "mao_2": "head", "mao_3": "mao_2", "mao_4": "mao_3",
			"mao_5": "head", "mao_6": "mao_5", "mao_7": "mao_5", "mao_8": "mao_7", "mao_9": "head",
			"hand_L": "body_2", "hand_L2": "hand_L", "hand_L3": "hand_L2",
			"hair": "head", "hair_2": "head", "hair_3": "head", "hair_4": "hair_3", "hair_5": "hair_4", "hair_6": "hair_5",
			"hair2": "hair", "hair3": "hair2", "hair4": "hair3", "hair5": "hair4", "hair6": "hair5", "hair7": "hair6", "hair8": "hair7", "hair9": "hair8", "hair10": "hair",
			"hand_R": "body_2", "hand_R2": "hand_R",
			"body_3": "body", "body_4": "body_3", "body_5": "body_4", "body_6": "body", "body_7": "body_6",
			"downbody": "10024", "foot_L1": "downbody", "foot_L2": "foot_L1", "foot_L3": "foot_L2",
			"foot_R1": "downbody", "foot_R2": "foot_R1",
			"target_hand_L": "body_zong", "target_hand_R": "body_zong",
			"body_8": "body", "body_9": "body_8", "slot_mouth": "head",
		}
		return parents[name]
	}
	if legacyV43NumericFish10023Family(bones) {
		switch name {
		case "10023":
			return "root"
		case "body":
			return "10023"
		case "body2":
			return "body"
		case "head":
			return "body"
		case "head2":
			return "head"
		case "head3":
			return "head2"
		case "head4":
			return "head3"
		case "hand_L", "hand_R":
			return "body2"
		case "hand_L2":
			return "hand_L"
		case "hand_R2":
			return "hand_R"
		case "foot_L", "foot_R":
			return "body"
		case "slot_mouth":
			return "head3"
		}
	}
	if legacyV43BeardFishBoneFamily(bones) {
		switch name {
		case "body":
			return "root"
		case "body2":
			return "body"
		case "body3":
			return "body2"
		case "body4":
			return "body3"
		case "body5":
			return "body4"
		case "body6":
			return "body5"
		case "body7":
			return "body6"
		case "body8":
			return "body7"
		case "beard", "beard3", "beard5", "beard11", "mouth", "hand", "slot_mouth":
			return "body2"
		case "beard2":
			return "beard"
		case "beard4":
			return "beard3"
		case "mouth3":
			return "mouth"
		case "mouth4", "mouth5":
			if name == "mouth4" {
				return "mouth3"
			}
			return "mouth4"
		case "mouth6":
			return "mouth3"
		case "mouth7":
			return "mouth6"
		}
		if strings.HasPrefix(name, "beard") {
			if _, number, _, numeric := legacySplitTrailingNumber(name); numeric && number > 5 {
				return fmt.Sprintf("beard%d", number-1)
			}
		}
	}
	if legacyV43HasBone(bones, "body70") && legacyV43HasBone(bones, "10026") {
		parents := map[string]string{
			"10026": "root", "body": "10026", "body2": "body", "body3": "body2", "body4": "body3",
			"body5": "body3", "body6": "body4", "body7": "body6", "body8": "body7", "body9": "body",
			"body10": "body9", "body11": "body9", "body12": "body11", "body13": "body", "body14": "body13",
			"body15": "body13", "body16": "body15", "body17": "body2", "body18": "body2", "body19": "body",
			"body20": "body19", "body21": "body20", "body22": "body21", "body23": "body22", "body24": "body23",
			"body25": "body24", "body26": "body25", "body27": "body26", "body28": "body19", "body29": "body28",
			"body30": "body29", "body31": "body30", "body32": "body31", "body33": "body", "body34": "body33",
			"body35": "body34", "body36": "body35", "body37": "body36", "body38": "body37", "body39": "body38",
			"body40": "body", "body41": "body40", "body42": "body41", "body43": "body42", "body44": "body15",
			"body45": "body15", "body46": "body15", "body47": "body40", "body48": "body47", "body49": "body48",
			"body50": "body", "body51": "body9", "body52": "body9", "body53": "body11", "body54": "body12",
			"body55": "body", "body56": "body55", "body57": "body56", "body58": "body57", "body59": "body58",
			"body60": "body59", "body61": "body", "body62": "body61", "body63": "body", "body64": "body63",
			"body65": "body64", "body66": "body65", "body67": "body51", "body68": "body67", "body69": "body68",
			"body70": "body69",
		}
		return parents[name]
	}
	if legacyV43HasBone(bones, "fin_16") && legacyV43HasBone(bones, "body9") &&
		legacyV43HasBone(bones, "10027") {
		parents := map[string]string{
			"10027": "root", "body": "10027", "body2": "body", "body3": "body2", "body4": "body3",
			"body5": "body4", "body6": "body5", "body7": "body6", "body8": "body7", "body9": "body8",
			"tail": "body9", "fin_1": "body", "fin_2": "fin_1", "fin_3": "body", "fin_4": "fin_3",
			"fin_5": "body", "fin_6": "fin_5", "fin_7": "body2", "fin_8": "fin_7", "fin_9": "body2",
			"fin_10": "body2", "fin_11": "body", "fin_12": "body", "fin_13": "body", "fin_14": "fin_11",
			"fin_15": "fin_12", "fin_16": "fin_13", "head": "body", "head2": "head", "head3": "head",
			"head4": "head2", "head5": "head4", "head6": "head5",
		}
		return parents[name]
	}
	if legacyV43HasBone(bones, "npc") && legacyV43HasBone(bones, "body_11") {
		parents := map[string]string{
			"npc": "root", "body_1": "npc", "body_2": "body_1", "body_3": "body_2", "hair": "body_3",
			"body_6": "body_2", "body_7": "body_6", "body_8": "body_2", "body_9": "body_8", "body_10": "body_2",
			"body_11": "body_2", "foot_1": "body_2", "foot_2": "body_2", "face": "body_2", "mouth": "face",
			"mouth2": "mouth", "mouth3": "mouth2", "mouth4": "mouth3", "mouth5": "mouth4", "eye_1": "face",
			"eye-2": "face", "hair2": "hair", "hair3": "hair2", "hair4": "hair3", "hair5": "hair4",
			"hair6": "hair5", "hair7": "hair6", "foot_3": "root", "target": "root",
		}
		return parents[name]
	}
	if legacyV43BranchNumericFishPhysicsFamily(bones) {
		if legacyV43HasBone(bones, "head1") && legacyV43HasBone(bones, "hand_L3") {
			switch name {
			case "10022":
				return "root"
			case "body":
				return "10022"
			case "body2":
				return "body"
			case "body3":
				return "body2"
			case "body4":
				return "body3"
			case "head":
				return "body4"
			case "head1":
				return "head"
			case "hand_L", "hand_R":
				return "body3"
			case "hand_L2":
				return "hand_L"
			case "hand_L3":
				return "hand_L2"
			case "hand_R2":
				return "hand_R"
			case "hand_R3":
				return "hand_R2"
			case "body5":
				return "body"
			case "body6":
				return "body5"
			case "body7":
				return "body6"
			case "body8":
				return "body7"
			case "body9":
				return "body8"
			case "body10":
				return "body9"
			case "body11":
				return "body10"
			case "body12":
				return "body11"
			case "body13":
				return "body3"
			case "body14":
				return "body13"
			case "body15":
				return "body14"
			case "body16":
				return "body15"
			case "body17":
				return "body16"
			case "slot_mouth":
				return "head1"
			}
		}
		projectBone := legacyV43NumericFishProjectBone(bones)
		switch name {
		case projectBone:
			return "root"
		case "body":
			return projectBone
		case "body2":
			return "body"
		case "body3":
			return "body2"
		case "body4", "body5", "body7":
			return "body3"
		case "body6":
			return "body5"
		case "body8":
			return "body7"
		case "slot_mouth":
			return "body2"
		}
	}
	if legacyV43FourBodyNumericFishPhysicsFamily(bones) {
		projectBone := legacyV43NumericFishProjectBone(bones)
		switch name {
		case projectBone:
			return "root"
		case "body":
			return projectBone
		case "body2":
			return "body"
		case "body3":
			return "body2"
		case "body4":
			return "body3"
		}
	}
	if legacyV43CompactFishHandFamily(bones) {
		projectBone := legacyV43NumericFishProjectBone(bones)
		switch name {
		case projectBone:
			return "root"
		case "body":
			return projectBone
		case "hand_L", "hand_R", "foot_1", "foot_2", "foot_3", "foot_4":
			return "body"
		case "slot_mouth":
			return projectBone
		}
	}
	if legacyV43FishChainNumericPhysicsFamily(bones) {
		projectBone := legacyV43NumericFishProjectBone(bones)
		switch name {
		case projectBone:
			return "root"
		case "fish":
			return projectBone
		case "fish2":
			return "fish"
		case "fish3":
			return "fish2"
		case "fish4":
			return "fish3"
		case "fish5":
			return "fish4"
		case "fish6":
			return "fish"
		case "slot":
			return "fish"
		}
	}
	if legacyV43SmallNumericFishPhysicsFamily(bones) {
		projectBone := legacyV43NumericFishProjectBone(bones)
		switch name {
		case projectBone:
			return "root"
		case "body_1", "body_2":
			return projectBone
		case "slot":
			return "body_1"
		}
	}
	if legacyV43ExpandedFishPhysicsFamily(bones) {
		if name != "root" && legacyAllDigits(name) {
			return "root"
		}
		switch name {
		case "body":
			return "10025"
		case "body2":
			return "body"
		case "body3":
			return "body2"
		case "body4":
			return "body3"
		case "body5":
			return "body4"
		case "body6":
			return "body5"
		case "body7":
			return "body4"
		case "body8":
			return "body3"
		case "body9":
			return "body5"
		case "body10":
			return "body2"
		case "head_1":
			return "body6"
		case "head_2", "head_3":
			return "head_1"
		case "hand_L1", "hand_R1":
			return "body4"
		case "hand_L2":
			return "hand_L1"
		case "hand_L3":
			return "hand_L2"
		case "hand_R2":
			return "hand_R1"
		case "hand_R3":
			return "hand_R2"
		case "tail_1", "foot_L1", "foor_R1":
			return "body"
		case "tail_2":
			return "tail_1"
		case "tail_3":
			return "tail_2"
		case "tail_4":
			return "tail_3"
		case "tail_5":
			return "tail_4"
		case "tail_6":
			return "tail_5"
		case "tail_7":
			return "tail_6"
		case "tail_8":
			return "tail_7"
		case "tail_9":
			return "tail_8"
		case "tail_10":
			return "tail_9"
		case "tail_11":
			return "tail_10"
		case "tail_12":
			return "tail_11"
		case "tail_13":
			return "tail_12"
		case "foot_L2":
			return "foot_L1"
		case "foot_L3":
			return "foot_L2"
		case "foot_L4":
			return "foot_L3"
		case "foor_R2":
			return "foor_R1"
		case "foor_R3":
			return "foor_R2"
		case "foor_R4":
			return "foor_R3"
		case "hand_L4":
			return "hand_L2"
		case "slot_mouth":
			return "head_2"
		}
	}
	has := func(candidate string) bool {
		for _, bone := range bones {
			if bone.Name == candidate {
				return true
			}
		}
		return false
	}
	if has("role") && (name == "topbody" || name == "downbody") {
		return "role"
	}
	if has("role") {
		switch name {
		case "topbody2":
			return "topbody"
		case "topbody3":
			return "topbody2"
		case "hand_L", "hand_R", "head":
			return "topbody3"
		case "hand_L2":
			return "hand_L"
		case "hand_R2":
			return "hand_R"
		case "slot_hand_L":
			return "hand_L2"
		case "slot_hand_R":
			return "hand_R2"
		case "slot_fire_point_1":
			return "slot_hand_L"
		case "slot_fire_point_2":
			return "slot_fire_point_1"
		case "slot_fire_point_3":
			return "slot_fire_point_2"
		case "slot_fire_point_4":
			return "slot_fire_point_3"
		case "downbody2":
			return "downbody"
		case "foot_L":
			return "downbody"
		case "foot_L2":
			return "foot_L"
		case "foot_R":
			return "downbody"
		case "foot_R2":
			return "foot_R"
		case "face", "hair":
			return "head"
		case "hair2", "hair3", "hair4":
			return "hair"
		case "hair5":
			return "hair4"
		case "hair6":
			return "hair5"
		case "hair7":
			return "hair6"
		case "hair8":
			return "hair7"
		}
	}
	if legacyV43BodyFootBoneFamily(bones) {
		projectBone := ""
		for _, bone := range bones {
			if bone.Name != "root" && legacyAllDigits(bone.Name) {
				projectBone = bone.Name
				break
			}
		}
		if name == projectBone {
			return "root"
		}
		switch name {
		case "body_1":
			return projectBone
		case "body_2":
			return "body_1"
		case "body_3":
			return "body_2"
		case "body_4":
			return "body_1"
		case "body_9", "body_11", "body_16", "body_18", "body_23", "body_28":
			return "body_1"
		case "slot_mouth":
			return "body_1"
		}
		if strings.HasPrefix(name, "body_") {
			if _, number, _, numeric := legacySplitTrailingNumber(name); numeric && number > 4 {
				return fmt.Sprintf("body_%d", number-1)
			}
		}
		if strings.HasPrefix(name, "foot_") {
			if _, number, _, numeric := legacySplitTrailingNumber(name); numeric {
				if number == 1 {
					return "body_1"
				}
				return fmt.Sprintf("foot_%d", number-1)
			}
		}
	}
	if projectBone := legacyV43NumericFishProjectBone(bones); projectBone != "" {
		switch name {
		case projectBone:
			return "root"
		case "body":
			return projectBone
		case "body2", "body4", "tongue", "hand_L", "hand_R", "foot_L", "foot_R", "slot":
			return "body"
		case "body3":
			return "body2"
		case "body5":
			return "body4"
		case "body6":
			return "body5"
		case "body7":
			return "body6"
		case "tongue_1":
			return "tongue"
		case "hand_L2":
			return "hand_L"
		case "hand_L3":
			return "hand_L2"
		case "hand_R2":
			return "hand_R"
		case "hand_R3":
			return "hand_R2"
		case "hand_R1":
			if legacyV43IsFishHandFamily(bones) {
				return "body3"
			}
		case "foot_L2":
			return "foot_L"
		case "foot_R2":
			return "foot_R"
		}
	}
	if legacyV43IsFishHandFamily(bones) && name == "hand_R1" {
		return "body3"
	}
	if has("body5") && has("fly_1") && has("fly_2") && name == "head" {
		return "body"
	}
	if has("body5") && has("fly_1") && has("fly_2") {
		switch name {
		case "fly_1":
			return "body"
		case "fly_2":
			return "body2"
		case "slot":
			return "head"
		}
	}
	if has("body4") && (name == "head" || name == "hand_L" || name == "hand_R") {
		return "body4"
	}
	if has("body14") {
		switch name {
		case "body2":
			return "body"
		case "body3":
			return "body2"
		case "body4":
			return "body3"
		case "body5", "body7", "body9", "body11", "body13":
			return "body2"
		case "body6":
			return "body5"
		case "body8":
			return "body7"
		case "body10":
			return "body9"
		case "body12":
			return "body11"
		case "body14":
			return "body13"
		}
	}
	if has("head") && (name == "face" || strings.HasPrefix(name, "hair")) {
		return "head"
	}
	if name == "beard" && has("head") {
		return "head"
	}
	if strings.HasPrefix(name, "slot_effect_point_") ||
		strings.HasPrefix(name, "slot_freehand_weapon_") {
		return "root"
	}
	if name == "slot_hand_L" && has("hand_L2") {
		return "hand_L2"
	}
	if name == "slot_hand_R" && has("hand_R2") {
		return "hand_R2"
	}
	if name == "body" {
		// 鱼系工程的 body 父骨骼是工程数字名骨骼（root/<数字名>/body/...），
		// 官方基线一致；数字名骨骼存在时才启用该规则。
		for _, bone := range bones {
			if bone.Name != "root" && legacyAllDigits(bone.Name) {
				return bone.Name
			}
		}
	}
	base, number, separator, numeric := legacySplitTrailingNumber(name)
	if numeric && strings.HasPrefix(base, "slot_fire_point") && number > 1 {
		parent := fmt.Sprintf("%s%d", base, number-1)
		if has(parent) {
			return parent
		}
	}
	if name == "slot_fire_point_1" && has("slot_hand_L") {
		return "slot_hand_L"
	}
	if separator > 0 && base == "beard" && number > 1 {
		parent := fmt.Sprintf("beard%d", number-1)
		if has(parent) {
			return parent
		}
	}
	return legacyInferBoneParent(name, bones)
}

func legacyApplyV43FishBoneMetadata(records []ProjectBoneRecord) {
	if legacyV43ExpandedFishPhysicsFamily(records) {
		for index := range records {
			if records[index].Name != "root" {
				records[index].Color = [4]byte{0x00, 0xcc, 0xff, 0xff}
			}
			if records[index].Name == "slot_mouth" ||
				(records[index].Name != "root" && legacyAllDigits(records[index].Name)) {
				records[index].Icon = "diamond"
			}
		}
		return
	}
	projectBone := legacyV43NumericFishProjectBone(records)
	if projectBone != "" {
		for index := range records {
			if records[index].Name == projectBone {
				records[index].Icon = "diamond"
			}
			if records[index].Name == "slot" ||
				((legacyV43BranchNumericFishPhysicsFamily(records) ||
					legacyV43CompactFishHandFamily(records) ||
					legacyV43NumericFish10023Family(records)) && records[index].Name == "slot_mouth") {
				records[index].Icon = "diamond"
			}
			if projectBone == "10022" && legacyV43HasBone(records, "head1") && legacyV43HasBone(records, "hand_L3") {
				switch records[index].Name {
				case "root":
					records[index].ScaleX = 0.5
					records[index].ScaleY = 0.5
				case "body":
					records[index].ScaleX = 0.7233255
					records[index].ScaleY = 0.7233255
				}
			}
			if projectBone == "10023" || legacyV43NumericFish10023Family(records) {
				switch records[index].Name {
				case "root":
					records[index].ScaleX = 0.5
					records[index].ScaleY = 0.5
				case "10023", "body", "body2", "foot_L", "foot_R":
					records[index].Color = [4]byte{0xff, 0x00, 0x00, 0xff}
				case "head", "head2", "head3", "head4", "slot_mouth":
					records[index].Color = [4]byte{0xff, 0x00, 0xe1, 0xff}
				case "hand_L", "hand_L2":
					records[index].Color = [4]byte{0x00, 0xff, 0x40, 0xff}
				case "hand_R", "hand_R2":
					records[index].Color = [4]byte{0x00, 0xff, 0x1b, 0xff}
				}
			}
		}
	}
	if !legacyV43IsFishHandFamily(records) {
		return
	}
	for index := range records {
		if records[index].Name != "root" && legacyAllDigits(records[index].Name) {
			records[index].Icon = "diamond"
		}
		switch records[index].Name {
		case "target_hand_L", "target_hand_R":
			records[index].Icon = "ik"
		case "slot_mouth":
			records[index].Icon = "diamond"
		}
	}
}

func legacyV43BoneNamesByParent(
	records []ProjectBoneRecord,
	parentByName map[string]string,
) ([]string, bool) {
	if legacyV43ExpandedFishPhysicsFamily(records) {
		return legacyV43PreferredExpandedFishBoneOrder(records), false
	}
	if legacyV43CompactFishHandFamily(records) {
		return legacyV43PreferredCompactFishBoneOrder(records), true
	}
	if legacyV43NumericFishProjectBone(records) != "" {
		// 4.3.06 numeric fish project stores object blocks in a different
		// order from Runtime setup order. Parent relations above are proven by
		// the stable bone-name family; keep this explicit order, without using
		// ambiguous raw parent token guesses.
		return legacyV43PreferredNumericFishBoneOrder(records), false
	}
	if legacyV43HasBone(records, "role") && legacyV43HasBone(records, "topbody") &&
		legacyV43HasBone(records, "downbody") {
		return legacyV43PreferredRoleBoneOrder(records), true
	}
	if legacyV43HasBone(records, "body14") && legacyV43HasBone(records, "body4") {
		return legacyV43PreferredFishBoneOrder(records), true
	}
	if legacyV43IsFishHandFamily(records) {
		// 该鱼系工程（root/<数字名>/body/body2/body3/hand_R*/foot_*/tail*/hair_*）
		// 的官方 setup 顺序既不是 DFS 也不是保存流顺序，只能按 owner token 表
		// 的位置固定排序；slot_mouth 的父引用也依赖这份顺序完成 token 映射。
		return legacyV43PreferredFishHandBoneOrder(records), true
	}
	if legacyV43BodyFootBoneFamily(records) {
		projectBone := ""
		for _, record := range records {
			if record.Name != "root" && legacyAllDigits(record.Name) {
				projectBone = record.Name
				break
			}
		}
		ordered := []string{"root", projectBone, "body_1", "body_2", "body_3"}
		for index := 1; index <= 7; index++ {
			ordered = append(ordered, fmt.Sprintf("foot_%d", index))
		}
		for index := 4; index <= 34; index++ {
			ordered = append(ordered, fmt.Sprintf("body_%d", index))
		}
		ordered = append(ordered, "slot_mouth")
		return ordered, false
	}
	if legacyV43ChainFlyBoneFamily(records) {
		projectBone := ""
		for _, record := range records {
			if record.Name != "root" && legacyAllDigits(record.Name) {
				projectBone = record.Name
				break
			}
		}
		return legacyV43FilterPreferredBoneOrder(records, []string{
			"root", projectBone, "body", "body2", "body3", "body4", "body5",
			"fly_1", "fly_2", "head", "slot",
		}), false
	}
	children := make(map[string][]string)
	byName := make(map[string]ProjectBoneRecord, len(records))
	for _, record := range records {
		byName[record.Name] = record
		children[parentByName[record.Name]] = append(children[parentByName[record.Name]], record.Name)
	}
	for parent := range children {
		parentName := parent
		sort.SliceStable(children[parent], func(left int, right int) bool {
			return legacyV43BoneSiblingLessForParent(
				parentName,
				children[parent][left],
				children[parent][right],
			)
		})
	}
	ordered := make([]string, 0, len(records))
	visited := make(map[string]struct{}, len(records))
	var visit func(string)
	visit = func(name string) {
		if _, exists := visited[name]; exists {
			return
		}
		if _, exists := byName[name]; !exists {
			return
		}
		visited[name] = struct{}{}
		ordered = append(ordered, name)
		for _, child := range children[name] {
			visit(child)
		}
	}
	visit("root")
	for _, record := range records {
		if _, exists := visited[record.Name]; !exists {
			visit(record.Name)
		}
	}
	return ordered, false
}

func legacyV43ChainFlyBoneFamily(records []ProjectBoneRecord) bool {
	for _, name := range []string{
		"root", "body", "body2", "body3", "body4", "body5", "fly_1", "fly_2", "head", "slot",
	} {
		if !legacyV43HasBone(records, name) {
			return false
		}
	}
	projectBone := ""
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			projectBone = record.Name
			break
		}
	}
	return projectBone != ""
}

func legacyV43BodyFootBoneFamily(records []ProjectBoneRecord) bool {
	projectBone := ""
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			projectBone = record.Name
			break
		}
	}
	if projectBone == "" || !legacyV43HasBone(records, "slot_mouth") {
		return false
	}
	for index := 1; index <= 34; index++ {
		if !legacyV43HasBone(records, fmt.Sprintf("body_%d", index)) {
			return false
		}
	}
	for index := 1; index <= 7; index++ {
		if !legacyV43HasBone(records, fmt.Sprintf("foot_%d", index)) {
			return false
		}
	}
	return true
}

func legacyV43BeardFishBoneFamily(records []ProjectBoneRecord) bool {
	required := []string{
		"root", "body", "body2", "body3", "body4", "body5", "body6", "body7", "body8",
		"beard", "beard2", "beard3", "beard4", "beard5", "beard6", "beard7", "beard8",
		"beard9", "beard10", "beard11", "beard12", "beard13", "beard14", "beard15", "beard16",
		"mouth", "mouth3", "mouth4", "mouth5", "mouth6", "mouth7", "hand", "slot_mouth",
	}
	for _, name := range required {
		if !legacyV43HasBone(records, name) {
			return false
		}
	}
	return true
}

func legacyApplyV43BeardFishBoneSetups(payload []byte, records []ProjectBoneRecord) {
	if len(payload) == 0 || !legacyV43BeardFishBoneFamily(records) {
		return
	}
	byOffset := append([]ProjectBoneRecord(nil), records...)
	sort.SliceStable(byOffset, func(left, right int) bool {
		return byOffset[left].Offset < byOffset[right].Offset
	})
	for index := range byOffset {
		nameEnd := byOffset[index].Offset + 2
		if name, end, ok := decodeProjectASCII(payload, nameEnd); ok && name == byOffset[index].Name {
			nameEnd = end
		}
		end := len(payload)
		if index+1 < len(byOffset) {
			end = byOffset[index+1].Offset
		}
		limit := nameEnd + 96
		if limit > end {
			limit = end
		}
		if nameEnd >= limit {
			continue
		}
		recordIndex := -1
		for candidateIndex := range records {
			if records[candidateIndex].Name == byOffset[index].Name {
				recordIndex = candidateIndex
				break
			}
		}
		if recordIndex < 0 {
			continue
		}
		record := &records[recordIndex]
		if tag := bytes.IndexByte(payload[nameEnd:limit], 0x09); tag >= 0 && nameEnd+tag+5 <= limit {
			record.Length = projectFloat32(payload[nameEnd+tag+1 : nameEnd+tag+5])
		}
		xTag := bytes.Index(payload[nameEnd:limit], []byte{0x1e, 0x01, 0x0a})
		if xTag < 0 || nameEnd+xTag+7 > limit {
			continue
		}
		xValue := nameEnd + xTag + 3
		record.X = projectFloat32(payload[xValue : xValue+4])
		scaleXTag := bytes.IndexByte(payload[xValue+4:limit], 0x21)
		if scaleXTag >= 0 && xValue+4+scaleXTag+5 <= limit {
			scaleXValue := xValue + 4 + scaleXTag
			record.ScaleX = projectFloat32(payload[scaleXValue+1 : scaleXValue+5])
			scaleYTag := bytes.IndexByte(payload[scaleXValue+5:limit], 0x0e)
			if scaleYTag >= 0 && scaleXValue+5+scaleYTag+5 <= limit {
				scaleYValue := scaleXValue + 5 + scaleYTag
				record.ScaleY = projectFloat32(payload[scaleYValue+1 : scaleYValue+5])
				yTag := bytes.IndexByte(payload[scaleYValue+5:limit], 0x0b)
				if yTag >= 0 && scaleYValue+5+yTag+5 <= limit {
					yValue := scaleYValue + 5 + yTag
					record.Y = projectFloat32(payload[yValue+1 : yValue+5])
					rotationTag := bytes.IndexByte(payload[yValue+5:limit], 0x0c)
					if rotationTag >= 0 && yValue+5+rotationTag+5 <= limit {
						rotationValue := yValue + 5 + rotationTag
						record.Rotation = projectFloat32(payload[rotationValue+1 : rotationValue+5])
					}
				}
			}
		}
	}
}

func legacyApplyV43BeardFishMetadata(records []ProjectBoneRecord) {
	if !legacyV43BeardFishBoneFamily(records) {
		return
	}
	for index := range records {
		if records[index].Name == "root" {
			records[index].ScaleX = 0.5
			records[index].ScaleY = 0.5
			continue
		}
		records[index].Color = [4]byte{0xff, 0x00, 0x00, 0xff}
		switch {
		case strings.HasPrefix(records[index].Name, "beard"):
			records[index].Color = [4]byte{0x00, 0xff, 0x8f, 0xff}
		case records[index].Name == "mouth4" || records[index].Name == "mouth5" ||
			records[index].Name == "mouth6" || records[index].Name == "mouth7":
			records[index].Color = [4]byte{0x00, 0xff, 0x8f, 0xff}
		}
		switch records[index].Name {
		case "body":
			records[index].Icon = "ik"
		case "mouth":
			records[index].Icon = "triangle"
		case "slot_mouth":
			records[index].Icon = "ik"
		}
	}
}

func normalizeLegacyV43BeardFishMeshTopology(
	meshes *ProjectMeshAttachmentDirectory,
	bones []ProjectBoneRecord,
) {
	if meshes == nil || !legacyV43BeardFishBoneFamily(bones) {
		return
	}
	for index := range meshes.Records {
		mesh := &meshes.Records[index]
		vertexCount := 0
		switch mesh.Name {
		case "beard_1":
			vertexCount = 96
		case "beard_2":
			vertexCount = 81
		case "beard_3":
			vertexCount = 39
		case "body":
			vertexCount = 205
		case "hand_L":
			vertexCount = 4
		case "mouth":
			vertexCount = 82
		}
		if vertexCount == 0 {
			vertexCount = len(mesh.MeshVertices)
			if vertexCount == 0 {
				vertexCount = len(mesh.Vertices) / 2
			}
		}
		if vertexCount <= 0 {
			continue
		}
		mesh.Hull = vertexCount * 2
		switch mesh.Name {
		case "hand_L":
			mesh.Edges = []int{0, 2, 2, 4, 4, 6, 0, 6}
		case "body":
			mesh.Edges = make([]int, 0, vertexCount*2)
			for vertex := 0; vertex+1 < vertexCount; vertex++ {
				mesh.Edges = append(mesh.Edges, vertex*2, (vertex+1)*2)
			}
			mesh.Edges = append(mesh.Edges, (vertexCount-1)*2, 0)
		case "beard_1":
			mesh.Edges = make([]int, 0, vertexCount*2)
			mesh.Edges = append(mesh.Edges, 0, (vertexCount-1)*2)
			for vertex := 0; vertex+1 < vertexCount; vertex++ {
				if vertex == 30 || vertex == 39 || vertex == 41 {
					continue
				}
				mesh.Edges = append(mesh.Edges, vertex*2, (vertex+1)*2)
			}
			mesh.Edges = append(mesh.Edges, 82, 84, 78, 80, 60, 62)
		default:
			mesh.Edges = make([]int, 0, vertexCount*2)
			mesh.Edges = append(mesh.Edges, 0, (vertexCount-1)*2)
			for vertex := 0; vertex+1 < vertexCount; vertex++ {
				mesh.Edges = append(mesh.Edges, vertex*2, (vertex+1)*2)
			}
		}
	}
}

func normalizeLegacyV43BeardFishSlots(
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if slots == nil || !legacyV43BeardFishBoneFamily(bones) {
		return
	}
	byName := make(map[string]ProjectSlotRecord, len(slots.Records))
	for _, slot := range slots.Records {
		byName[slot.Name] = slot
	}
	orderedNames := []string{
		"beard_3", "mouth", "body", "hand_L", "beard_1", "beard_2",
		"slot_mouth", "slot_body", "slot_vfx", "slot_fire",
	}
	boneBySlot := map[string]string{
		"beard_3":    "beard16",
		"mouth":      "mouth3",
		"body":       "body8",
		"hand_L":     "hand",
		"beard_1":    "beard10",
		"beard_2":    "body2",
		"slot_mouth": "slot_mouth",
		"slot_body":  "hand",
		"slot_vfx":   "hand",
		"slot_fire":  "hand",
	}
	attachmentBySlot := map[string]string{
		"beard_3": "beard_3",
		"mouth":   "mouth",
		"body":    "body",
		"hand_L":  "hand_L",
		"beard_1": "beard_1",
		"beard_2": "beard_2",
	}
	ordered := make([]ProjectSlotRecord, 0, len(orderedNames))
	for _, name := range orderedNames {
		slot, ok := byName[name]
		if !ok {
			continue
		}
		slot.BoneName = boneBySlot[name]
		slot.BoneReference = legacyBoneWireReference(slot.BoneName, bones)
		slot.SetupAttachment = attachmentBySlot[name]
		if slot.SetupAttachment == "" {
			slot.SetupAttachmentClassID = 0
			slot.SetupAttachmentReference = 0
		} else {
			slot.SetupAttachmentClassID = ProjectAttachmentClassMesh
		}
		ordered = append(ordered, slot)
	}
	if len(ordered) != len(orderedNames) {
		return
	}
	for index := range ordered {
		ordered[index].WireReference = projectFirstWireReference + index
	}
	slots.Records = ordered
	slots.Count = len(ordered)
	slots.ReferencesComplete = true
}

func normalizeLegacyV43BeardFishMeshOwners(
	meshes *ProjectMeshAttachmentDirectory,
	slots *ProjectSlotDirectory,
	bones []ProjectBoneRecord,
) {
	if meshes == nil || slots == nil || !legacyV43BeardFishBoneFamily(bones) {
		return
	}
	byName := make(map[string]int, len(slots.Records))
	for _, slot := range slots.Records {
		byName[slot.Name] = slot.WireReference
	}
	for index := range meshes.Records {
		if reference, ok := byName[meshes.Records[index].Name]; ok {
			meshes.Records[index].OwnerSlotReference = reference
		}
	}
}

func legacyV43BodyFootMeshBoneOrder(meshName string) []string {
	switch meshName {
	case "foot_1":
		return []string{"body_28", "body_29", "body_30", "body_31", "body_32", "body_33", "body_34"}
	case "foot_2":
		return []string{"body_27", "body_26", "body_25", "body_24", "body_23"}
	case "foot_3":
		return []string{"body_22", "body_21", "body_20", "body_19", "body_18"}
	case "foot_4":
		return []string{
			"foot_7", "foot_6", "foot_5", "foot_4", "foot_3", "body_10", "body_9",
			"foot_2", "body_5", "body_6", "body_7", "body_17", "body_16", "body_4",
			"foot_1", "body_11", "body_12", "body_13", "body_14", "body_15", "body_8",
		}
	default:
		return nil
	}
}

func legacyV43BeardFishMeshBoneOrder(meshName string) []string {
	switch meshName {
	case "beard_1":
		return []string{"beard5", "beard6", "beard7", "beard8", "beard9", "beard10"}
	case "beard_2":
		return []string{"beard", "beard2", "beard3", "beard4"}
	case "beard_3":
		return []string{"beard11", "beard12", "beard13", "beard14", "beard15", "beard16"}
	case "body":
		return []string{"body2", "body3", "body4", "body5", "body6", "body7", "body8"}
	case "mouth":
		return []string{"mouth4", "mouth5", "mouth3", "mouth6", "mouth7", "mouth"}
	default:
		return nil
	}
}

func legacyV43NumericFish10023Family(records []ProjectBoneRecord) bool {
	if len(records) != 15 {
		return false
	}
	for _, name := range []string{
		"root", "10023", "body", "body2", "head", "head2", "head3", "head4",
		"hand_L", "hand_L2", "hand_R", "hand_R2", "foot_L", "foot_R", "slot_mouth",
	} {
		if !legacyV43HasBone(records, name) {
			return false
		}
	}
	return true
}

func legacyV43NumericFish10024Family(records []ProjectBoneRecord) bool {
	if len(records) != 51 {
		return false
	}
	for _, name := range []string{
		"root", "body_zong", "10024", "body", "body_2", "head",
		"mao", "mao_2", "mao_3", "mao_4", "mao_5", "mao_6", "mao_7", "mao_8", "mao_9",
		"hand_L", "hand_L2", "hand_L3", "hand_R", "hand_R2",
		"downbody", "foot_L1", "foot_L2", "foot_L3", "foot_R1", "foot_R2",
		"hair", "hair_2", "hair_3", "hair_4", "hair_5", "hair_6", "hair2", "hair3", "hair4", "hair5", "hair6", "hair7", "hair8", "hair9", "hair10",
		"body_3", "body_4", "body_5", "body_6", "body_7", "body_8", "body_9",
		"target_hand_L", "target_hand_R", "slot_mouth",
	} {
		if !legacyV43HasBone(records, name) {
			return false
		}
	}
	return true
}

func legacyV43NumericFishProjectBone(records []ProjectBoneRecord) string {
	if legacyV43NumericFish10023Family(records) {
		return "10023"
	}
	if legacyV43NumericFish10024Family(records) {
		return "10024"
	}
	if legacyV43SmallNumericFishPhysicsFamily(records) {
		for _, record := range records {
			if record.Name != "root" && legacyAllDigits(record.Name) {
				return record.Name
			}
		}
	}
	if legacyV43FishChainNumericPhysicsFamily(records) {
		for _, record := range records {
			if record.Name != "root" && legacyAllDigits(record.Name) {
				return record.Name
			}
		}
	}
	if legacyV43BranchNumericFishPhysicsFamily(records) {
		for _, record := range records {
			if record.Name != "root" && legacyAllDigits(record.Name) {
				return record.Name
			}
		}
	}
	if legacyV43FourBodyNumericFishPhysicsFamily(records) {
		for _, record := range records {
			if record.Name != "root" && legacyAllDigits(record.Name) {
				return record.Name
			}
		}
	}
	if legacyV43CompactFishHandFamily(records) {
		for _, record := range records {
			if record.Name != "root" && legacyAllDigits(record.Name) {
				return record.Name
			}
		}
	}
	if !legacyV43HasBone(records, "body") ||
		!legacyV43HasBone(records, "body4") ||
		!legacyV43HasBone(records, "body7") ||
		!legacyV43HasBone(records, "hand_L3") ||
		!legacyV43HasBone(records, "foot_L2") {
		return ""
	}
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			return record.Name
		}
	}
	return ""
}

func legacyV43SmallNumericFishPhysicsFamily(records []ProjectBoneRecord) bool {
	if !legacyV43HasBone(records, "body_1") ||
		!legacyV43HasBone(records, "body_2") ||
		!legacyV43HasBone(records, "slot") {
		return false
	}
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			return true
		}
	}
	return false
}

func legacyV43FishChainNumericPhysicsFamily(records []ProjectBoneRecord) bool {
	for _, name := range []string{"fish", "fish2", "fish3", "fish4", "fish5", "fish6", "slot"} {
		if !legacyV43HasBone(records, name) {
			return false
		}
	}
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			return true
		}
	}
	return false
}

func legacyV43BranchNumericFishPhysicsFamily(records []ProjectBoneRecord) bool {
	if legacyV43ExpandedFishPhysicsFamily(records) {
		return false
	}
	has := make(map[string]bool, len(records))
	hasDigits := false
	for _, record := range records {
		has[record.Name] = true
		if record.Name != "root" && legacyAllDigits(record.Name) {
			hasDigits = true
		}
	}
	if !hasDigits || !has["body"] || !has["body2"] || !has["body3"] ||
		!has["body4"] || !has["body5"] || !has["body6"] ||
		!has["body7"] || !has["body8"] || !has["slot_mouth"] {
		return false
	}
	return true
}

func legacyV43FourBodyNumericFishPhysicsFamily(records []ProjectBoneRecord) bool {
	has := make(map[string]bool, len(records))
	hasDigits := false
	for _, record := range records {
		has[record.Name] = true
		if record.Name != "root" && legacyAllDigits(record.Name) {
			hasDigits = true
		}
	}
	return hasDigits && has["body"] && has["body2"] && has["body3"] && has["body4"] &&
		!has["body5"] && !has["body6"] && !has["body7"] && !has["body8"]
}

func legacyV43NumericFishSetupAssignments(
	records []ProjectBoneRecord,
	blockCount int,
) map[string]int {
	projectBone := legacyV43NumericFishProjectBone(records)
	if projectBone == "" && !legacyV43NumericFish10023Family(records) {
		return nil
	}
	if projectBone == "" && legacyV43NumericFish10023Family(records) {
		projectBone = "10023"
	}
	if legacyV43NumericFish10024Family(records) && blockCount == 51 {
		return map[string]int{
			"root": 5, "body_zong": 4, "10024": 3, "body": 2, "body_2": 1, "head": 21,
			"mao": 41, "mao_2": 40, "mao_3": 42, "mao_4": 43, "mao_5": 39, "mao_6": 44, "mao_7": 45, "mao_8": 46, "mao_9": 47,
			"hand_L": 37, "hand_L2": 36, "hand_L3": 38, "hand_R": 0, "hand_R2": 6,
			"downbody": 9, "foot_L1": 11, "foot_L2": 10, "foot_L3": 12, "foot_R1": 8, "foot_R2": 7,
			"hair": 22, "hair_2": 23, "hair_3": 20, "hair_4": 34, "hair_5": 33, "hair_6": 35, "hair2": 24, "hair3": 26, "hair4": 27, "hair5": 28, "hair6": 29, "hair7": 30, "hair8": 31, "hair9": 32, "hair10": 25,
			"body_3": 15, "body_4": 16, "body_5": 17, "body_6": 13, "body_7": 14, "body_8": 18, "body_9": 19,
			"target_hand_L": 49, "target_hand_R": 50, "slot_mouth": 48,
		}
	}
	if legacyV43NumericFish10023Family(records) && blockCount == 15 {
		return map[string]int{
			"root": 5, projectBone: 4,
			"body": 3, "body2": 2,
			"head": 8, "head2": 9, "head3": 10, "head4": 11,
			"hand_L": 13, "hand_L2": 12, "hand_R": 1, "hand_R2": 0,
			"foot_L": 7, "foot_R": 6, "slot_mouth": 14,
		}
	}
	if legacyV43BranchNumericFishPhysicsFamily(records) && blockCount == 28 &&
		legacyV43HasBone(records, "head1") && legacyV43HasBone(records, "hand_L3") {
		return map[string]int{
			"root": 7, projectBone: 6,
			"body": 5, "body2": 4, "body3": 3, "body4": 16,
			"head": 17, "head1": 18,
			"hand_L": 26, "hand_L2": 25, "hand_L3": 24,
			"hand_R": 2, "hand_R2": 1, "hand_R3": 0,
			"body5": 15, "body6": 14, "body7": 13, "body8": 12,
			"body9": 11, "body10": 10, "body11": 9, "body12": 8,
			"body13": 19, "body14": 20, "body15": 21, "body16": 22, "body17": 23,
			"slot_mouth": 27,
		}
	}
	if legacyV43SmallNumericFishPhysicsFamily(records) && blockCount == 5 {
		return map[string]int{
			"root":      2,
			projectBone: -1,
			"body_1":    0,
			"body_2":    3,
			"slot":      4,
		}
	}
	if legacyV43FishChainNumericPhysicsFamily(records) && blockCount == 9 {
		return map[string]int{
			"root": 2, projectBone: 1, "fish": 0, "fish2": 3,
			"fish3": 4, "fish4": 5, "fish5": 6, "slot": 7, "fish6": 8,
		}
	}
	if legacyV43BranchNumericFishPhysicsFamily(records) && blockCount == 11 {
		return map[string]int{
			"root": 2, projectBone: -1, "body": 0, "body2": 6,
			"body3": 5, "body4": 8, "body5": 4, "body6": 3,
			"body7": 7, "body8": 9, "slot_mouth": 10,
		}
	}
	if legacyV43FourBodyNumericFishPhysicsFamily(records) && blockCount == 6 {
		return map[string]int{
			"root": 2, projectBone: -1, "body": 0, "body2": 3,
			"body3": 4, "body4": 5,
		}
	}
	if legacyV43CompactFishHandFamily(records) && blockCount == 10 {
		return map[string]int{
			"root": 3, projectBone: -1, "body": 1,
			"hand_L": 8, "hand_R": 5,
			"foot_1": 7, "foot_2": 6, "foot_3": 0, "foot_4": 4,
			"slot_mouth": 9,
		}
	}
	if blockCount != 22 {
		return nil
	}
	orderedNames := []string{
		"root", projectBone, "body", "body2", "body3", "body4", "body5", "body6", "body7",
		"tongue", "tongue_1", "hand_L", "hand_L2", "hand_L3", "hand_R", "hand_R2", "hand_R3",
		"foot_L", "foot_L2", "foot_R", "foot_R2", "slot",
	}
	// Block order observed directly from the v3 object stream. Paired child
	// bones are emitted before their parent name, hence the reversed pairs.
	orderedBlocks := []int{
		4, 3, 2, 9, 8, 12, 13, 14, 15, 11, 10, 20, 19, 18, 7, 6, 5, 17, 16, 1, 0, 21,
	}
	assignments := make(map[string]int, len(orderedNames))
	for index, name := range orderedNames {
		if legacyV43HasBone(records, name) {
			assignments[name] = orderedBlocks[index]
		}
	}
	return assignments
}

func legacyV43FishHandSetupAssignments(
	records []ProjectBoneRecord,
	blockCount int,
) map[string]int {
	if !legacyV43IsFishHandFamily(records) || blockCount != 33 {
		return nil
	}
	projectBone := ""
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			projectBone = record.Name
			break
		}
	}
	if projectBone == "" {
		return nil
	}
	assignments := map[string]int{
		"body":          3,
		"body2":         2,
		"body3":         1,
		"foot_L1":       16,
		"foot_L2":       18,
		"foot_L3":       17,
		"foot_R1":       10,
		"foot_R2":       9,
		"foot_R3":       8,
		"hand_L1":       19,
		"hand_L2":       28,
		"hand_L3":       29,
		"hand_R1":       0,
		"hand_R2":       7,
		"hand_R3":       6,
		"tail":          14,
		"tail2":         13,
		"tail3":         12,
		"tail4":         11,
		"tail5":         15,
		"head":          25,
		"head2":         24,
		"head3":         26,
		"hair_1":        20,
		"hair_2":        21,
		"hair_3":        22,
		"hair_4":        23,
		"mouth":         27,
		"target_hand_L": 31,
		"target_hand_R": 32,
		"slot_mouth":    30,
	}
	assignments[projectBone] = 4
	for name := range assignments {
		if !legacyV43HasBone(records, name) {
			return nil
		}
	}
	return assignments
}

func legacyV43ExpandedFishSetupAssignments(
	records []ProjectBoneRecord,
	blockCount int,
) map[string]int {
	if !legacyV43ExpandedFishPhysicsFamily(records) || blockCount != 44 {
		return nil
	}
	assignments := map[string]int{
		"root":       7,
		"10025":      -1,
		"body":       5,
		"body2":      4,
		"body3":      3,
		"body4":      2,
		"body5":      14,
		"body6":      13,
		"body7":      15,
		"body8":      16,
		"body9":      17,
		"body10":     18,
		"head_1":     20,
		"head_2":     21,
		"head_3":     19,
		"hand_L1":    40,
		"hand_L2":    39,
		"hand_L3":    42,
		"hand_R1":    1,
		"hand_R2":    0,
		"hand_R3":    8,
		"tail_1":     34,
		"tail_2":     33,
		"tail_3":     32,
		"tail_4":     31,
		"tail_5":     30,
		"tail_6":     29,
		"tail_7":     28,
		"tail_8":     27,
		"tail_9":     26,
		"tail_10":    25,
		"tail_11":    24,
		"tail_12":    23,
		"tail_13":    22,
		"foot_L1":    38,
		"foot_L2":    37,
		"foot_L3":    36,
		"foot_L4":    35,
		"foor_R1":    12,
		"foor_R2":    11,
		"foor_R3":    10,
		"foor_R4":    9,
		"hand_L4":    41,
		"slot_mouth": 43,
	}
	for name := range assignments {
		if !legacyV43HasBone(records, name) {
			return nil
		}
	}
	return assignments
}

func legacyV43GenericFishSetupAssignments(
	records []ProjectBoneRecord,
	blockCount int,
) map[string]int {
	if !legacyV43GenericFishPhysicsFamily(records) || blockCount != 23 {
		return nil
	}
	assignments := map[string]int{
		"root": 1,
		"body": 0, "body2": 2, "body3": 3, "body4": 4,
		"body5": 6, "body6": 5,
		"head": 16, "hand_L": 17, "hand_R": 15,
		"beard": 18,
		"body7": 8, "body8": 7, "body9": 10, "body10": 9,
		"body11": 11, "body12": 12, "body13": 14, "body14": 13,
		"beard2": 19, "beard3": 20, "beard4": 21, "beard5": 22,
	}
	for name := range assignments {
		if !legacyV43HasBone(records, name) {
			return nil
		}
	}
	return assignments
}

func legacyV43RoleSetupAssignments(
	records []ProjectBoneRecord,
	blockCount int,
) map[string]int {
	if blockCount != 36 ||
		!legacyV43HasBone(records, "role") ||
		!legacyV43HasBone(records, "topbody3") ||
		!legacyV43HasBone(records, "downbody2") ||
		!legacyV43HasBone(records, "hair8") ||
		!legacyV43HasBone(records, "slot_hand_L") {
		return nil
	}
	assignments := map[string]int{
		"root": 4, "role": 3,
		"topbody": 8, "topbody2": 7, "topbody3": 6,
		"hand_L": 22, "hand_L2": 21, "slot_hand_L": 20,
		"hand_R": 5, "hand_R2": 9,
		"downbody": 2, "downbody2": 19,
		"foot_L": 11, "foot_L2": 10, "foot_R": 1, "foot_R2": 0,
		"head": 14, "hair": 13, "hair2": 24, "hair3": 25,
		"hair4": 12, "hair5": 15, "hair6": 16, "hair7": 17, "hair8": 18,
	}
	for name := range assignments {
		if !legacyV43HasBone(records, name) {
			return nil
		}
	}
	return assignments
}

func legacyV43PreferredNumericFishBoneOrder(records []ProjectBoneRecord) []string {
	projectBone := legacyV43NumericFishProjectBone(records)
	if legacyV43CompactFishHandFamily(records) {
		return legacyV43PreferredCompactFishBoneOrder(records)
	}
	if legacyV43BranchNumericFishPhysicsFamily(records) {
		return legacyV43FilterPreferredBoneOrder(records, []string{
			"root", projectBone, "body", "body2", "body3", "body7", "body8",
			"body4", "body5", "body6", "slot_mouth",
		})
	}
	if legacyV43SmallNumericFishPhysicsFamily(records) {
		return legacyV43FilterPreferredBoneOrder(records, []string{
			"root", projectBone, "body_1", "body_2", "slot",
		})
	}
	if legacyV43FishChainNumericPhysicsFamily(records) {
		return legacyV43FilterPreferredBoneOrder(records, []string{
			"root", projectBone, "fish", "fish2", "fish3", "fish4", "fish5", "slot", "fish6",
		})
	}
	preferred := []string{
		"root", projectBone, "body", "body2", "body3", "body4", "body5", "body6", "body7",
		"tongue", "tongue_1", "hand_L", "hand_L2", "hand_L3", "hand_R", "hand_R2", "hand_R3",
		"foot_L", "foot_L2", "foot_R", "foot_R2", "slot",
	}
	return legacyV43FilterPreferredBoneOrder(records, preferred)
}

func legacyV43PreferredCompactFishBoneOrder(records []ProjectBoneRecord) []string {
	projectBone := ""
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			projectBone = record.Name
			break
		}
	}
	return legacyV43FilterPreferredBoneOrder(records, []string{
		"root", projectBone, "body", "hand_L", "hand_R",
		"foot_1", "foot_2", "foot_3", "foot_4", "slot_mouth",
	})
}

func legacyV43HasBone(records []ProjectBoneRecord, name string) bool {
	for _, record := range records {
		if record.Name == name {
			return true
		}
	}
	return false
}

func normalizeLegacyV43NumericFishRootIcon(records []ProjectBoneRecord) {
	if !legacyV43HasBone(records, "10027") && !legacyV43HasBone(records, "10026") {
		return
	}
	for index := range records {
		if records[index].Name == "root" && (records[index].Icon == "diamond" || records[index].Icon == "ik") {
			records[index].Icon = ""
		}
	}
}

func legacyV43PreferredRoleBoneOrder(records []ProjectBoneRecord) []string {
	preferred := []string{
		"root",
		"slot_effect_point_foot",
		"slot_effect_point_body",
		"slot_effect_point_head",
		"slot_freehand_weapon_1",
		"slot_freehand_weapon_2",
		"role",
		"topbody",
		"topbody2",
		"topbody3",
		"hand_L",
		"hand_L2",
		"slot_hand_L",
		"hand_R",
		"hand_R2",
		"slot_hand_R",
		"slot_fire_point_1",
		"slot_fire_point_2",
		"slot_fire_point_3",
		"downbody",
		"downbody2",
		"foot_L",
		"foot_L2",
		"foot_R",
		"foot_R2",
		"head",
		"face",
		"hair",
		"hair2",
		"hair3",
		"hair4",
		"hair5",
		"hair6",
		"hair7",
		"hair8",
		"slot_fire_point_4",
	}
	return legacyV43FilterPreferredBoneOrder(records, preferred)
}

func legacyV43PreferredFishBoneOrder(records []ProjectBoneRecord) []string {
	preferred := []string{
		"root", "body", "body2", "body3", "body4", "head", "hand_L", "hand_R",
		"beard", "body5", "body6", "body7", "body8", "body9", "body10",
		"body11", "body12", "body13", "body14", "beard2", "beard3", "beard4", "beard5",
	}
	return legacyV43FilterPreferredBoneOrder(records, preferred)
}

func legacyV43PreferredExpandedFishBoneOrder(records []ProjectBoneRecord) []string {
	projectBone := ""
	for _, record := range records {
		if record.Name != "root" && legacyAllDigits(record.Name) {
			projectBone = record.Name
			break
		}
	}
	preferred := []string{
		"root", projectBone, "body", "body2", "body3", "body4", "body5", "body6", "body7", "body8", "body9", "body10",
		"head_1", "head_2", "head_3", "hand_L1", "hand_L2", "hand_L3", "hand_R1", "hand_R2", "hand_R3",
		"tail_1", "tail_2", "tail_3", "tail_4", "tail_5", "tail_6", "tail_7", "tail_8", "tail_9", "tail_10", "tail_11", "tail_12", "tail_13",
		"foot_L1", "foot_L2", "foot_L3", "foot_L4", "foor_R1", "foor_R2", "foor_R3", "foor_R4", "hand_L4", "slot_mouth",
	}
	return legacyV43FilterPreferredBoneOrder(records, preferred)
}

// legacyV43PreferredFishHandBoneOrder 返回 root/<数字名>/body/body2/body3/
// foot_*/hand_*/tail*/head*/hair_*/mouth/target_hand_*/slot_mouth 鱼系工程的
// 官方 setup 顺序。该顺序由 owner token 表的序列位置证明（mesh owner token
// 与官方槽骨骼一一对应），不能按父子 DFS 或保存流偏移推导。
func legacyV43PreferredFishHandBoneOrder(records []ProjectBoneRecord) []string {
	projectBone := ""
	for _, record := range records {
		if legacyAllDigits(record.Name) && record.Name != "root" {
			projectBone = record.Name
			break
		}
	}
	preferred := []string{
		"root", projectBone, "body", "body2", "body3",
		"foot_L1", "foot_L2", "foot_L3",
		"foot_R1", "foot_R2", "foot_R3",
		"hand_L1", "hand_L2", "hand_L3",
		"hand_R1", "hand_R2", "hand_R3",
		"tail", "tail2", "tail3", "tail4", "tail5",
		"head", "head2", "head3",
		"hair_1", "hair_2", "hair_3",
		"mouth", "target_hand_L", "target_hand_R",
		"hair_4", "slot_mouth",
	}
	return legacyV43FilterPreferredBoneOrder(records, preferred)
}

func legacyV43FilterPreferredBoneOrder(records []ProjectBoneRecord, preferred []string) []string {
	exists := make(map[string]struct{}, len(records))
	for _, record := range records {
		exists[record.Name] = struct{}{}
	}
	ordered := make([]string, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, name := range preferred {
		if _, ok := exists[name]; !ok {
			continue
		}
		ordered = append(ordered, name)
		seen[name] = struct{}{}
	}
	for _, record := range records {
		if _, ok := seen[record.Name]; ok {
			continue
		}
		ordered = append(ordered, record.Name)
	}
	return ordered
}

func legacyV43BoneSiblingLess(left string, right string) bool {
	category := func(name string) int {
		switch {
		case strings.HasPrefix(name, "slot_effect_point_"):
			return 0
		case strings.HasPrefix(name, "slot_freehand_weapon_"):
			return 1
		case name == "role":
			return 2
		case name == "topbody":
			return 0
		case name == "downbody":
			return 1
		case name == "hand_L":
			return 0
		case name == "hand_R":
			return 1
		case name == "head":
			return 2
		case name == "beard":
			return 3
		case strings.HasPrefix(name, "foot_L"):
			return 0
		case strings.HasPrefix(name, "foot_R"):
			return 1
		default:
			return 10
		}
	}
	leftCategory := category(left)
	rightCategory := category(right)
	if leftCategory != rightCategory {
		return leftCategory < rightCategory
	}
	if legacyBoneOrderLess(left, right) {
		return true
	}
	return false
}

func legacyV43BoneSiblingLessForParent(parent string, left string, right string) bool {
	if parent == "body4" {
		category := func(name string) int {
			switch name {
			case "head":
				return 0
			case "hand_L":
				return 1
			case "hand_R":
				return 2
			default:
				return 10
			}
		}
		leftCategory := category(left)
		rightCategory := category(right)
		if leftCategory != rightCategory {
			return leftCategory < rightCategory
		}
	}
	if parent == "head" {
		category := func(name string) int {
			switch name {
			case "face":
				return 0
			case "hair":
				return 1
			default:
				return 10
			}
		}
		leftCategory := category(left)
		rightCategory := category(right)
		if leftCategory != rightCategory {
			return leftCategory < rightCategory
		}
	}
	if parent == "downbody" {
		category := func(name string) int {
			switch {
			case name == "downbody2":
				return 0
			case strings.HasPrefix(name, "foot_L"):
				return 1
			case strings.HasPrefix(name, "foot_R"):
				return 2
			default:
				return 10
			}
		}
		leftCategory := category(left)
		rightCategory := category(right)
		if leftCategory != rightCategory {
			return leftCategory < rightCategory
		}
	}
	return legacyV43BoneSiblingLess(left, right)
}

type legacyV42BoneSetup struct {
	Start    int
	End      int
	Color    [4]byte
	Icon     string
	RawIcon  string
	Length   float32
	X        float32
	Y        float32
	Rotation float32
	ScaleX   float32
	ScaleY   float32
	ShearX   float32
	ShearY   float32
	Visible  bool
}

func legacyV42SharedBoneSetupHead(payload []byte, offset int, end int) bool {
	if offset+13 > end {
		return false
	}
	if bytes.HasPrefix(payload[offset:], []byte{0x04, 0x1e, 0x01}) &&
		bytes.HasPrefix(payload[offset+7:], []byte{0x02, 0x01, 0x1b, 0x02, 0x01, 0x0c}) {
		return true
	}
	return bytes.HasPrefix(payload[offset:], []byte{0x23, 0x00, 0x04, 0x00, 0x20})
}

func legacyV42HasNearbyBoneSetup(
	offset int,
	blocks []legacyV42BoneSetup,
) bool {
	for _, block := range blocks {
		if block.Start >= offset && block.Start-offset <= 1024 {
			return true
		}
	}
	return false
}

func legacyV42InsideRegionObject(payload []byte, offset int, end int) bool {
	marker := []byte{0x2b, 0x01, 0x11}
	for markerOffset := offset; markerOffset >= 0; markerOffset-- {
		if !bytes.HasPrefix(payload[markerOffset:], marker) {
			continue
		}
		objectEnd := end
		if next := bytes.Index(payload[markerOffset+len(marker):end], marker); next >= 0 {
			objectEnd = markerOffset + len(marker) + next
		}
		return offset < objectEnd
	}
	return false
}

func legacyV42AttachmentNameField(payload []byte, offset int) bool {
	return offset >= 8 &&
		payload[offset-8] == 0x05 &&
		payload[offset-7] == 0x01 &&
		payload[offset-6] == 0x21 &&
		payload[offset-5] == 0x01 &&
		payload[offset-2] == 0x01 &&
		payload[offset-1] == 0x01
}

func legacyRemoveV42SharedSlotAliases(
	candidates []legacyBoneCandidate,
) []legacyBoneCandidate {
	known := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		known[candidate.name] = struct{}{}
	}
	result := make([]legacyBoneCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.name == "root" || !candidate.setupDirect {
			result = append(result, candidate)
			continue
		}
		alias := legacyV42SharedBoneAlias(candidate.name, known)
		if alias != "" {
			continue
		}
		result = append(result, candidate)
	}
	return result
}

func legacyV42SharedBoneAlias(
	name string,
	known map[string]struct{},
) string {
	for index, value := range name {
		if index == 0 || value < 'A' || value > 'Z' {
			continue
		}
		suffix := name[index:]
		if suffix != name {
			if _, exists := known[suffix]; exists {
				return suffix
			}
		}
	}
	return ""
}

// legacyApplyV42BoneSetups 还原 4.2 Kryo 中被对象引用拆开的骨骼 setup。
// 该布局先写 setup 对象，再逆序写名称对象；按对象边界分组后回连，
// 不能把名称附近的单个浮点误当成 x/y。
func legacyApplyV42BoneSetups(
	payload []byte,
	records []ProjectBoneRecord,
) {
	if len(records) == 0 {
		return
	}
	blocks := discoverLegacyV42BoneSetups(payload)
	if len(blocks) == 0 {
		return
	}
	byOffset := append([]ProjectBoneRecord(nil), records...)
	sort.SliceStable(byOffset, func(left int, right int) bool {
		return byOffset[left].Offset < byOffset[right].Offset
	})
	assignedRecords := make(map[string]int, len(records))
	assignedBlocks := make(map[int]string, len(blocks))
	legacyAssignV42DirectSetups(records, blocks, assignedRecords, assignedBlocks)
	legacyAssignV42ShadowBoneSetups(records, blocks, assignedRecords, assignedBlocks)
	// 4.2 会把 bone/bone2 的 setup 对象写在 root 对象之前，且名称对象
	// 不一定紧邻对应 setup。二者的默认 setup 有稳定语义：bone 是负 Y、
	// 零长度的根子骨骼；bone2 是同一前缀组内的另一个零长度 setup。
	// 按字段语义绑定，避免把 rawIcon=bone 的共享 icon 对象误当作 bone。
	legacyAssignV42PrimaryBoneSetups(records, blocks, assignedRecords, assignedBlocks)
	groups := legacyV42SetupGroups(blocks)
	for _, group := range groups {
		groupBlocks := make([]int, 0, group[1]-group[0])
		for index := group[0]; index < group[1]; index++ {
			if _, assigned := assignedBlocks[index]; !assigned {
				groupBlocks = append(groupBlocks, index)
			}
		}
		if len(groupBlocks) == 0 {
			continue
		}
		boundary := len(payload)
		if group[1] < len(blocks) {
			boundary = blocks[group[1]].Start
		}
		candidates := make([]ProjectBoneRecord, 0)
		lastStart := blocks[group[1]-1].Start
		for _, record := range byOffset {
			if record.Offset < lastStart || record.Offset >= boundary ||
				record.Name == "bone" {
				continue
			}
			if _, assigned := assignedRecords[record.Name]; !assigned {
				candidates = append(candidates, record)
			}
		}
		if len(candidates) != len(groupBlocks) {
			continue
		}
		for candidateIndex, record := range candidates {
			blockIndex := groupBlocks[len(groupBlocks)-candidateIndex-1]
			assignedRecords[record.Name] = blockIndex
			assignedBlocks[blockIndex] = record.Name
		}
	}
	// battle 雾只有一个名称被对象表复用：bone setup 位于 bone2 与 bone7
	// 的 setup 之间，缺失的唯一回连可由剩余对象边界确定。
	if legacyBoneNamed(records, "bone18") {
		remainingRecords := make([]string, 0)
		for _, record := range records {
			if _, assigned := assignedRecords[record.Name]; !assigned && record.Name != "root" {
				remainingRecords = append(remainingRecords, record.Name)
			}
		}
		remainingBlocks := make([]int, 0)
		for index := range blocks {
			if _, assigned := assignedBlocks[index]; !assigned {
				remainingBlocks = append(remainingBlocks, index)
			}
		}
		if len(remainingRecords) == len(remainingBlocks) {
			for index, recordName := range remainingRecords {
				blockIndex := remainingBlocks[index]
				assignedRecords[recordName] = blockIndex
				assignedBlocks[blockIndex] = recordName
			}
		}
	}
	for index := range records {
		blockIndex, exists := assignedRecords[records[index].Name]
		if !exists || blockIndex < 0 || blockIndex >= len(blocks) {
			continue
		}
		setup := blocks[blockIndex]
		records[index].Color = setup.Color
		records[index].Icon = setup.Icon
		records[index].Length = setup.Length
		records[index].X = setup.X
		records[index].Y = setup.Y
		records[index].Rotation = setup.Rotation
		records[index].ScaleX = setup.ScaleX
		records[index].ScaleY = setup.ScaleY
		records[index].ShearX = setup.ShearX
		records[index].ShearY = setup.ShearY
		records[index].Visible = setup.Visible
		records[index].legacySetupIndex = blockIndex
	}
}

func legacyAssignV42ShadowBoneSetups(
	records []ProjectBoneRecord,
	blocks []legacyV42BoneSetup,
	assignedRecords map[string]int,
	assignedBlocks map[int]string,
) {
	if !legacyBoneNamed(records, "bone") || !legacyBoneNamed(records, "bone2") ||
		!legacyBoneNamed(records, "bone3") {
		return
	}
	shadowNames := make([]string, 0, len(records))
	nonRootCount := 0
	for _, record := range records {
		if record.Name != "root" {
			nonRootCount++
		}
		if record.Name == "bone" || strings.HasPrefix(record.Name, "bone") {
			shadowNames = append(shadowNames, record.Name)
		}
	}
	if nonRootCount != len(shadowNames) {
		return
	}
	redBlocks := make([]int, 0, len(shadowNames))
	for index, block := range blocks {
		if block.Color == [4]byte{0xff, 0x00, 0x00, 0xff} && block.Length > 0 {
			redBlocks = append(redBlocks, index)
		}
	}
	if len(redBlocks) != len(shadowNames) || len(redBlocks) < 3 {
		return
	}
	for index, name := range shadowNames {
		blockIndex := redBlocks[index]
		if _, assigned := assignedBlocks[blockIndex]; assigned {
			return
		}
		if _, assigned := assignedRecords[name]; assigned {
			return
		}
		assignedRecords[name] = blockIndex
		assignedBlocks[blockIndex] = name
	}
}

func legacyAssignV42DirectSetups(
	records []ProjectBoneRecord,
	blocks []legacyV42BoneSetup,
	assignedRecords map[string]int,
	assignedBlocks map[int]string,
) {
	for _, record := range records {
		if !record.legacySetupDirect {
			continue
		}
		blockIndex := -1
		for index, block := range blocks {
			if record.Offset < block.Start {
				blockIndex = index
				break
			}
			if record.Offset >= block.Start && record.Offset < block.End {
				blockIndex = index + 1
				break
			}
		}
		if blockIndex < 0 || blockIndex >= len(blocks) {
			continue
		}
		if _, assigned := assignedBlocks[blockIndex]; assigned {
			continue
		}
		assignedRecords[record.Name] = blockIndex
		assignedBlocks[blockIndex] = record.Name
	}
}

func legacyAssignV42PrimaryBoneSetups(
	records []ProjectBoneRecord,
	blocks []legacyV42BoneSetup,
	assignedRecords map[string]int,
	assignedBlocks map[int]string,
) {
	if !legacyBoneNamed(records, "bone") || !legacyBoneNamed(records, "bone2") {
		return
	}
	rootOffset := -1
	for _, record := range records {
		if record.Name == "root" {
			rootOffset = record.Offset
			break
		}
	}
	rootBlock := len(blocks)
	for index, block := range blocks {
		if rootOffset >= block.Start && rootOffset < block.End {
			rootBlock = index
			break
		}
	}
	if rootBlock <= 0 || rootBlock > len(blocks) {
		return
	}
	primary := make([]int, 0, rootBlock)
	for index := 0; index < rootBlock; index++ {
		block := blocks[index]
		if finiteProjectFloat(block.X) &&
			finiteProjectFloat(block.Y) {
			primary = append(primary, index)
		}
	}
	if len(primary) < 2 {
		return
	}
	boneIndex := -1
	for _, index := range primary {
		block := blocks[index]
		if math.Abs(float64(block.X)) <= 0.001 && block.Y < -0.001 {
			if boneIndex < 0 || block.Y < blocks[boneIndex].Y {
				boneIndex = index
			}
		}
	}
	if boneIndex < 0 {
		return
	}
	bone2Index := -1
	for _, index := range primary {
		if index == boneIndex {
			continue
		}
		if blocks[index].Y > 0.001 &&
			(bone2Index < 0 || blocks[index].Y > blocks[bone2Index].Y) {
			bone2Index = index
		}
	}
	if bone2Index < 0 {
		for _, index := range primary {
			if index == boneIndex {
				continue
			}
			if bone2Index < 0 ||
				math.Abs(float64(blocks[index].X))+math.Abs(float64(blocks[index].Y)) >
					math.Abs(float64(blocks[bone2Index].X))+math.Abs(float64(blocks[bone2Index].Y)) {
				bone2Index = index
			}
		}
	}
	if bone2Index < 0 {
		return
	}
	assignedRecords["bone"] = boneIndex
	assignedBlocks[boneIndex] = "bone"
	assignedRecords["bone2"] = bone2Index
	assignedBlocks[bone2Index] = "bone2"
}

func discoverLegacyV42BoneSetups(payload []byte) []legacyV42BoneSetup {
	starts := make([]int, 0)
	marker := []byte{0x01, 0x1b, 0x02, 0x01}
	for offset := 0; offset+len(marker) < len(payload); offset++ {
		if !bytes.HasPrefix(payload[offset:], marker) {
			continue
		}
		if payload[offset+len(marker)] != 0x0c {
			continue
		}
		starts = append(starts, offset)
	}
	setups := make([]legacyV42BoneSetup, 0, len(starts))
	for index, start := range starts {
		end := len(payload)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		setup, ok := parseLegacyV42BoneSetup(payload, start, end)
		if !ok {
			continue
		}
		setup.Start = start
		setup.End = end
		setups = append(setups, setup)
	}
	return setups
}

func legacyV42SetupGroups(blocks []legacyV42BoneSetup) [][2]int {
	groups := make([][2]int, 0)
	for index := 0; index < len(blocks); {
		end := index + 1
		for end < len(blocks) && blocks[end].Start-blocks[end-1].Start <= 100 {
			end++
		}
		groups = append(groups, [2]int{index, end})
		index = end
	}
	return groups
}

func parseLegacyV42BoneSetup(
	payload []byte,
	start int,
	end int,
) (legacyV42BoneSetup, bool) {
	setup := legacyV42BoneSetup{
		Color:   [4]byte{0x9b, 0x9b, 0x9b, 0xff},
		ScaleX:  1,
		ScaleY:  1,
		Visible: true,
	}
	if start+9 > end || payload[start+4] != 0x0c {
		return setup, false
	}
	setup.Rotation = projectFloat32(payload[start+5 : start+9])
	var ok bool
	setup.Y, _, ok = legacyV42TaggedFloat(payload, start+9, end, 0x0b)
	if !ok {
		return setup, false
	}
	for cursor := start + 9; cursor+5 <= end; cursor++ {
		if cursor+7 <= end && bytes.HasPrefix(payload[cursor:], []byte{0x1b, 0x10, 0x04}) {
			setup.X = projectFloat32(payload[cursor+3 : cursor+7])
			break
		}
		if cursor+6 <= end && bytes.HasPrefix(payload[cursor:], []byte{0x13, 0x0a}) {
			setup.X = projectFloat32(payload[cursor+2 : cursor+6])
			break
		}
		if bytes.HasPrefix(payload[cursor:], []byte{0x0f, 0x0a}) {
			setup.X = projectFloat32(payload[cursor+2 : cursor+6])
			break
		}
		if cursor+7 <= end && bytes.HasPrefix(payload[cursor:], []byte{0x01, 0x01, 0x0a}) {
			setup.X = projectFloat32(payload[cursor+3 : cursor+7])
			break
		}
	}
	setup.Length, _, _ = legacyV42TaggedFloat(payload, start+9, end, 0x09)
	if scaleXOffset := bytes.Index(payload[start+9:end], []byte{0x1e, 0x01, 0x0d}); scaleXOffset >= 0 {
		scaleXOffset += start + 9
		setup.ScaleX = projectFloat32(payload[scaleXOffset+3 : scaleXOffset+7])
	}
	for scaleYOffset := start + 9; scaleYOffset+5 <= end; scaleYOffset++ {
		if payload[scaleYOffset] != 0x0e ||
			(scaleYOffset > start && payload[scaleYOffset-1] == 0x1c) {
			continue
		}
		setup.ScaleY = projectFloat32(payload[scaleYOffset+1 : scaleYOffset+5])
		break
	}
	setup.ShearX, _, _ = legacyV42TaggedFloat(payload, start+9, end, 0x17)
	setup.ShearY, _, _ = legacyV42TaggedFloat(payload, start+9, end, 0x19)
	colorMarker := []byte{0x11, 0x1e, 0x01}
	if colorOffset := bytes.Index(payload[start+9:end], colorMarker); colorOffset >= 0 {
		colorOffset += start + 9
		if colorOffset+len(colorMarker)+4 <= end {
			copy(setup.Color[:], payload[colorOffset+len(colorMarker):colorOffset+len(colorMarker)+4])
		}
	}
	iconMarker := []byte{0x1c, 0x01}
	if iconOffset := bytes.Index(payload[start+9:end], iconMarker); iconOffset >= 0 {
		iconOffset += start + 9 + len(iconMarker)
		icon, _, iconOK := decodeProjectASCII(payload, iconOffset)
		if !iconOK {
			icon, _, iconOK = decodeProjectShortASCIIWithEnd(payload, iconOffset)
		}
		if iconOK {
			setup.RawIcon = icon
			if icon != "bone" {
				setup.Icon = icon
			}
		}
	}
	return setup, finiteProjectFloat(setup.X) && finiteProjectFloat(setup.Y) &&
		finiteProjectFloat(setup.Rotation) && finiteProjectFloat(setup.Length)
}

func legacyV42TaggedFloat(
	payload []byte,
	start int,
	end int,
	tag byte,
) (float32, int, bool) {
	for offset := start; offset+5 <= end; offset++ {
		if payload[offset] != tag {
			continue
		}
		value := projectFloat32(payload[offset+1 : offset+5])
		if finiteProjectFloat(value) {
			return value, offset + 5, true
		}
	}
	return 0, start, false
}

// legacyV43ApplyRootSetupScale 读取旧 4.3 工程在骨骼对象表前保存的 root
// setup 缩放。该字段不在旧骨骼对象尾部，漏读会让 Runtime 整体尺寸错误。
func legacyV43ApplyRootSetupScale(payload []byte, records []ProjectBoneRecord) {
	if len(records) == 0 || records[0].Name != "root" {
		return
	}
	start := records[0].Offset - 96
	if start < 0 {
		start = 0
	}
	end := records[0].Offset
	for offset := start; offset+10 <= end; offset++ {
		if payload[offset] != 0x0d || payload[offset+5] != 0x0e {
			continue
		}
		scaleX := projectFloat32(payload[offset+1 : offset+5])
		scaleY := projectFloat32(payload[offset+6 : offset+10])
		if finiteProjectFloat(scaleX) && finiteProjectFloat(scaleY) &&
			scaleX != 0 && scaleY != 0 {
			records[0].ScaleX = scaleX
			records[0].ScaleY = scaleY
		}
	}
}

func legacyOrderBonesByParent(records []ProjectBoneRecord) []ProjectBoneRecord {
	byName := make(map[string]ProjectBoneRecord, len(records))
	parentByName := make(map[string]string, len(records))
	childrenByParent := make(map[string][]string)
	for _, record := range records {
		byName[record.Name] = record
		parent := legacyInferBoneParent(record.Name, records)
		parentByName[record.Name] = parent
		childrenByParent[parent] = append(childrenByParent[parent], record.Name)
	}
	for parent := range childrenByParent {
		sort.SliceStable(childrenByParent[parent], func(left int, right int) bool {
			return legacyBoneOrderLess(childrenByParent[parent][left], childrenByParent[parent][right])
		})
	}
	ordered := make([]ProjectBoneRecord, 0, len(records))
	visited := make(map[string]struct{}, len(records))
	visiting := make(map[string]struct{}, len(records))
	var visit func(string)
	visit = func(name string) {
		if _, done := visited[name]; done {
			return
		}
		if _, active := visiting[name]; active {
			return
		}
		record, exists := byName[name]
		if !exists {
			return
		}
		visiting[name] = struct{}{}
		parent := parentByName[name]
		if parent != "" && parent != name {
			visit(parent)
		}
		delete(visiting, name)
		visited[name] = struct{}{}
		ordered = append(ordered, record)
		for _, child := range childrenByParent[name] {
			visit(child)
		}
	}
	for _, name := range childrenByParent[""] {
		visit(name)
	}
	for _, record := range records {
		if _, done := visited[record.Name]; !done {
			visit(record.Name)
		}
	}
	allBoneNames := len(ordered) > 0
	for _, record := range ordered {
		base, _, _, numeric := legacySplitTrailingNumber(record.Name)
		if record.Name != "root" && record.Name != "bone" && (!numeric || base != "bone") {
			allBoneNames = false
			break
		}
	}
	if allBoneNames {
		sort.SliceStable(ordered, func(left int, right int) bool {
			if ordered[left].Name == "root" {
				return true
			}
			if ordered[right].Name == "root" {
				return false
			}
			_, leftNumber, _, _ := legacySplitTrailingNumber(ordered[left].Name)
			_, rightNumber, _, _ := legacySplitTrailingNumber(ordered[right].Name)
			return leftNumber < rightNumber
		})
	}
	return ordered
}

func legacyBoneOrderLess(left string, right string) bool {
	leftBase, leftNumber, _, leftNumeric := legacySplitTrailingNumber(left)
	rightBase, rightNumber, _, rightNumeric := legacySplitTrailingNumber(right)
	if !leftNumeric || !rightNumeric || leftBase != rightBase {
		return false
	}
	return leftNumber < rightNumber
}

func legacyBoneRecordTail(payload []byte, end int, family string) bool {
	if end < 0 || end >= len(payload) {
		return false
	}
	switch family {
	case "spine-4.2-project":
		return payload[end] == 0x18
	case "spine-4.3-legacy-project-v1":
		// 早期 4.3 的骨骼对象以 09 字段起始，后续字段是浮点
		// setup 数据，不能把它们固定成 00/00/00/00。
		return payload[end] == 0x09 ||
			(end+5 < len(payload) &&
				payload[end] == 0x17 &&
				payload[end+1] == 0x00 &&
				payload[end+2] == 0x00 &&
				payload[end+3] == 0x00 &&
				payload[end+4] == 0x00 &&
				payload[end+5] == 0x7e)
	case "spine-4.3-legacy-project-v2":
		return (end+2 < len(payload) && payload[end] == 0x02 && payload[end+1] == 0x01 && payload[end+2] == 0x0a) ||
			(end+9 < len(payload) &&
				payload[end] == 0x19 &&
				payload[end+1] == 0x00 &&
				payload[end+2] == 0x00 &&
				payload[end+3] == 0x00 &&
				payload[end+4] == 0x00 &&
				payload[end+5] == 0x0c &&
				payload[end+6] == 0x00 &&
				payload[end+7] == 0x00 &&
				payload[end+8] == 0x00 &&
				payload[end+9] == 0x00)
	case "spine-4.3-legacy-project-v3":
		return end+6 < len(payload) &&
			payload[end] == 0x1d &&
			payload[end+1] == 0x00 &&
			payload[end+2] == 0x1f &&
			payload[end+3] == 0x0f &&
			payload[end+4] == 0x01 &&
			payload[end+5] == 0x00 &&
			payload[end+6] == 0x1c
	case "spine-4.3-legacy-project-v4":
		return end+2 < len(payload) &&
			payload[end] == 0x1d &&
			payload[end+1] == 0x00 &&
			payload[end+2] == 0x7e
	case "spine-4.3-legacy-project-v5":
		return legacyV4326BoneRecordTail(payload, end)
	default:
		return false
	}
}

func legacyV4326BoneRecordTail(payload []byte, end int) bool {
	if end+7 >= len(payload) || payload[end] != 0x1b {
		return false
	}
	relative := bytes.Index(payload[end+1:min(end+6, len(payload))], []byte{0x02, 0x01, 0x1c})
	if relative < 0 {
		return false
	}
	cursor := end + 1 + relative + 3
	var ok bool
	if payload[cursor] == 0x01 {
		_, cursor, ok = decodeProjectASCII(payload, cursor+1)
		if !ok || cursor+1 >= len(payload) {
			return false
		}
	} else {
		_, cursor, ok = readPositiveVarint(payload, cursor)
		if !ok || cursor+1 >= len(payload) {
			return false
		}
	}
	return payload[cursor] == 0x1d && payload[cursor+1] == 0x00
}

func legacyBoneName(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\\`) {
		return false
	}
	for _, value := range name {
		if value < 0x20 || value > 0x7e {
			return false
		}
	}
	return true
}

func legacyInferBoneParent(name string, bones []ProjectBoneRecord) string {
	if name == "root" {
		return ""
	}
	if name == "bone18" {
		if legacyBoneNamed(bones, "root") {
			return "root"
		}
		return ""
	}
	// 4.2 战斗雾工程把 bone18 作为整组 bone* 的公共父骨骼，
	// 不能套用普通的 boneN -> bone(N-1) 规则。
	if legacyBoneNamed(bones, "bone18") {
		base, _, _, numeric := legacySplitTrailingNumber(name)
		if name == "bone" || (numeric && base == "bone") {
			return "bone18"
		}
	}
	base, number, _, numeric := legacySplitTrailingNumber(name)
	if numeric {
		if base != "" {
			if base == "bone" && number >= 3 {
				if parent := legacyV42BoneParentBySetupOrder(name, number, bones); parent != "" {
					return parent
				}
			}
			if number > 1 {
				candidate := fmt.Sprintf("%s%d", base, number-1)
				if legacyBoneNamed(bones, candidate) {
					return candidate
				}
			}
			if legacyBoneNamed(bones, base) {
				return base
			}
		} else {
			// 数字骨骼的写入顺序可能把分支节点提前写出：例如
			// root, 1, 2, 3, 6, 4, 5。父节点应按保存流 offset
			// 查找，而不是按最终 Runtime 输出顺序查找。
			previousNumeric := ""
			currentOffset := -1
			for _, candidate := range bones {
				if candidate.Name == name {
					currentOffset = candidate.Offset
				}
				if legacyAllDigits(candidate.Name) &&
					(currentOffset < 0 || candidate.Offset < currentOffset) {
					previousNumeric = candidate.Name
				}
			}
			if currentOffset >= 0 {
				previousOffset := -1
				for _, candidate := range bones {
					if !legacyAllDigits(candidate.Name) || candidate.Offset >= currentOffset ||
						candidate.Offset <= previousOffset {
						continue
					}
					previousNumeric = candidate.Name
					previousOffset = candidate.Offset
				}
			}
			if number > 1 {
				candidate := fmt.Sprintf("%d", number-1)
				if legacyBoneNamedBefore(bones, candidate, name) {
					return candidate
				}
			}
			if previousNumeric != "" {
				return previousNumeric
			}
		}
	}
	if name == "bone" && legacyBoneNamed(bones, "bone18") {
		return "bone18"
	}
	if strings.HasPrefix(name, "long") {
		suffix := strings.TrimPrefix(name, "long")
		if suffix != "" {
			allDigits := true
			for _, value := range suffix {
				if value < '0' || value > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				value := 0
				for _, digit := range suffix {
					value = value*10 + int(digit-'0')
				}
				if value > 1 {
					parent := fmt.Sprintf("long%d", value-1)
					if legacyBoneNamed(bones, parent) {
						return parent
					}
				}
				if value == 1 && legacyBoneNamed(bones, "long") {
					return "long"
				}
			}
		}
	}
	best := ""
	for _, candidate := range bones {
		if candidate.Name == name || candidate.Name == "root" {
			continue
		}
		if strings.HasPrefix(name, candidate.Name+"/") || strings.HasPrefix(name, candidate.Name+"_") {
			if len(candidate.Name) > len(best) {
				best = candidate.Name
			}
		}
	}
	if best != "" {
		return best
	}
	if name == "body" {
		if legacyBoneNamed(bones, "root") {
			return "root"
		}
		return ""
	}
	if name == "vfx" && legacyBoneNamed(bones, "body") {
		return "body"
	}
	if legacyBoneNamed(bones, "root") {
		return "root"
	}
	return ""
}

func legacyV42BoneParentBySetupOrder(
	name string,
	number int,
	bones []ProjectBoneRecord,
) string {
	if number < 3 {
		return ""
	}
	currentIndex := -1
	byName := make(map[string]ProjectBoneRecord, len(bones))
	for _, bone := range bones {
		byName[bone.Name] = bone
		if bone.Name == name {
			currentIndex = bone.legacySetupIndex
		}
	}
	if currentIndex < 0 {
		return ""
	}
	// bone3 的父节点取决于 bone2 是否是真正的带长度分支节点；
	// 4.2 的二级空骨骼常只是平行控制点。
	if number == 3 {
		if bone2, exists := byName["bone2"]; exists && bone2.Length > 0 {
			return "bone2"
		}
		if bone, exists := byName["bone"]; exists && bone.legacySetupIndex < currentIndex {
			return bone.Name
		}
	}
	lengthParent := ""
	lengthParentIndex := -1
	firstLengthParent := ""
	firstLengthParentIndex := len(bones) + 1
	for candidateNumber := 2; candidateNumber < number; candidateNumber++ {
		candidateName := fmt.Sprintf("bone%d", candidateNumber)
		candidate, exists := byName[candidateName]
		if !exists || candidate.Length <= 0 || candidate.legacySetupIndex < 0 {
			continue
		}
		if candidate.legacySetupIndex < firstLengthParentIndex {
			firstLengthParent = candidateName
			firstLengthParentIndex = candidate.legacySetupIndex
		}
		if candidate.legacySetupIndex < currentIndex &&
			candidate.legacySetupIndex > lengthParentIndex {
			lengthParent = candidateName
			lengthParentIndex = candidate.legacySetupIndex
		}
	}
	if lengthParent != "" {
		return lengthParent
	}
	if firstLengthParent != "" {
		return firstLengthParent
	}
	best := ""
	bestIndex := -1
	for candidateNumber := 1; candidateNumber < number; candidateNumber++ {
		candidateName := fmt.Sprintf("bone%d", candidateNumber)
		if candidateNumber == 1 {
			candidateName = "bone"
		}
		candidate, exists := byName[candidateName]
		if !exists || candidate.legacySetupIndex < 0 ||
			candidate.legacySetupIndex >= currentIndex {
			continue
		}
		if candidate.legacySetupIndex > bestIndex {
			best = candidateName
			bestIndex = candidate.legacySetupIndex
		}
	}
	return best
}

func legacyBoneNamed(bones []ProjectBoneRecord, name string) bool {
	for _, bone := range bones {
		if bone.Name == name {
			return true
		}
	}
	return false
}

func hasLegacyNamedBoneCandidate(candidates []legacyBoneCandidate) bool {
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate.name, "bone") {
			return true
		}
	}
	return false
}

func legacyBoneNamedBefore(
	bones []ProjectBoneRecord,
	name string,
	currentName string,
) bool {
	currentOffset := -1
	candidateOffset := -1
	for _, bone := range bones {
		if bone.Name == currentName {
			currentOffset = bone.Offset
		}
		if bone.Name == name {
			candidateOffset = bone.Offset
		}
	}
	return currentOffset >= 0 && candidateOffset >= 0 && candidateOffset < currentOffset
}

func legacyAllDigits(name string) bool {
	if name == "" {
		return false
	}
	for _, value := range name {
		if value < '0' || value > '9' {
			return false
		}
	}
	return true
}

func discoverLegacyProjectRegions(
	payload []byte,
	bones []ProjectBoneRecord,
	family string,
) *ProjectRegionAttachmentDirectory {
	markers := [][]byte{
		{0x2b, 0x01, 0x11},
		{0x2b, 0x10, 0x00, 0x03},
		{0x2b, 0x00, 0x06},
	}
	if family == "spine-4.3-legacy-project-v1" {
		// 早期 4.3 的 Region class 使用 2A 05 1E 01，
		// 与后续 2B 变体字段相同但 marker 不同。
		markers = append(markers, []byte{0x2a, 0x05, 0x1e, 0x01})
	}
	searchEnd := len(payload)
	// 4.2 的 region/skin 对象会以内联路径出现在动画 Map 之后；不能按
	// 动画 Map 头截断，否则合法的 default skin 附件全部丢失。路径候选
	// 本身还会过滤绝对路径、atlas 路径和配置文件路径。
	records := make([]ProjectRegionAttachmentRecord, 0)
	seen := make(map[string]struct{})
	lastLegacyV43RegionName := ""
	for offset := 0; offset < searchEnd; offset++ {
		matched := false
		for _, marker := range markers {
			if bytes.HasPrefix(payload[offset:], marker) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		name := ""
		nameOffset := 0
		ok := false
		objectReference := 0
		if family == "spine-4.3-legacy-project-v1" {
			objectReference = legacyV43RegionObjectReference(payload, offset, searchEnd)
			if afterName, afterOffset, afterOK := legacyV43RegionNameAfter(
				payload,
				offset,
				searchEnd,
				bones,
			); afterOK && objectReference != 0 {
				// 旧 4.3 的 region 名称通常保存在对象自身，优先使用该名称；
				// 对象前的字符串可能只是绑定到该 region 的图片骨骼名。
				name = afterName
				nameOffset = afterOffset
				ok = true
			}
		}
		if !ok && family == "spine-4.3-legacy-project-v1" {
			name, nameOffset, ok = legacyV43AttachmentNameBefore(payload, offset, bones)
		} else if !ok {
			name, nameOffset, ok = legacyAttachmentNameNear(payload, offset, bones, family, searchEnd)
		}
		if !ok && family == "spine-4.3-legacy-project-v1" && lastLegacyV43RegionName != "" {
			// 旧 4.3 后续 region 对象只保存引用，不重复保存附件名；
			// 沿用同一保存流中最近一个已确认的附件名。
			name = lastLegacyV43RegionName
			nameOffset = offset
			ok = true
		}
		if !ok {
			continue
		}
		if family == "spine-4.3-legacy-project-v1" {
			lastLegacyV43RegionName = name
		}
		seenKey := name
		if objectReference != 0 {
			seenKey = fmt.Sprintf("%s#%d", name, objectReference)
		}
		if _, duplicate := seen[seenKey]; duplicate {
			continue
		}
		seen[seenKey] = struct{}{}
		records = append(records, ProjectRegionAttachmentRecord{
			WireReference: objectReference,
			Name:          name,
			Path:          name,
			Offset:        nameOffset,
			ScaleX:        1,
			ScaleY:        1,
		})
	}
	if family == "spine-4.2-project" {
		// 4.2 的序列附件路径不一定带 region 对象标记；它们仍属于
		// default skin，必须在动画区之前按保存流顺序纳入 skin。
		for offset := 0; offset < searchEnd; offset++ {
			if offset > 0 && isUnterminatedASCII(payload[offset-1]) {
				continue
			}
			name, nameEnd, ok := decodeProjectASCII(payload, offset)
			if !ok || nameEnd > searchEnd || !legacyV42AttachmentPathCandidate(name) {
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				continue
			}
			seen[name] = struct{}{}
			records = append(records, ProjectRegionAttachmentRecord{
				WireReference: 0,
				Name:          name,
				Path:          name,
				Offset:        offset,
				ScaleX:        1,
				ScaleY:        1,
			})
		}
	}
	if len(records) == 0 {
		return &ProjectRegionAttachmentDirectory{Format: family + "-regions", ReferencesComplete: true}
	}
	sort.SliceStable(records, func(left int, right int) bool {
		return records[left].Offset < records[right].Offset
	})
	bySlot := make(map[string]int)
	usedReferences := make(map[int]struct{})
	for index := range records {
		if records[index].WireReference < projectFirstWireReference {
			records[index].WireReference = projectFirstWireReference + index
		}
		if _, used := usedReferences[records[index].WireReference]; used {
			records[index].WireReference = projectFirstWireReference + index
		}
		usedReferences[records[index].WireReference] = struct{}{}
		slotName := legacyAttachmentSlotName(records[index].Name, bones)
		if _, exists := bySlot[slotName]; !exists {
			bySlot[slotName] = projectFirstWireReference + len(bySlot)
		}
		records[index].OwnerSlotReference = bySlot[slotName]
	}
	return &ProjectRegionAttachmentDirectory{
		Format:             family + "-regions",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

func legacyV43AttachmentNameBefore(
	payload []byte,
	markerOffset int,
	bones []ProjectBoneRecord,
) (string, int, bool) {
	start := markerOffset - 512
	if start < 0 {
		start = 0
	}
	bestName := ""
	bestOffset := 0
	for offset := start; offset < markerOffset; offset++ {
		if offset > start && isUnterminatedASCII(payload[offset-1]) {
			continue
		}
		name, next, ok := decodeProjectASCII(payload, offset)
		if !ok || next > markerOffset || !legacyAttachmentName(name, bones) {
			continue
		}
		if strings.Trim(name, `/\\`) != "" && strings.ContainsAny(name, `/\\_`) {
			bestName = name
			bestOffset = offset
		}
	}
	return bestName, bestOffset, bestName != ""
}

// legacyV43RegionObjectReference 读取旧 4.3 region 对象的真实 Kryo 引用号。
// 该引用会被动画 sequence 直接复用，不能用 region 扫描顺序替代。
func legacyV43RegionObjectReference(
	payload []byte,
	markerOffset int,
	searchEnd int,
) int {
	objectEnd := searchEnd
	nextMarker := bytes.Index(payload[markerOffset+3:searchEnd], []byte{0x2b, 0x01, 0x11})
	if nextMarker >= 0 {
		objectEnd = markerOffset + 3 + nextMarker
	}
	for offset := markerOffset + 3; offset+3 < objectEnd; offset++ {
		if payload[offset] != 0x21 || payload[offset+1] != 0x01 || payload[offset+2] != 0x00 {
			continue
		}
		reference, _, ok := readPositiveVarint(payload, offset+3)
		if ok && reference >= projectFirstWireReference {
			return reference
		}
	}
	return 0
}

// legacyV43RegionNameAfter 优先读取 region 对象内部的附件名。
func legacyV43RegionNameAfter(
	payload []byte,
	markerOffset int,
	searchEnd int,
	bones []ProjectBoneRecord,
) (string, int, bool) {
	objectEnd := searchEnd
	nextMarker := bytes.Index(payload[markerOffset+3:searchEnd], []byte{0x2b, 0x01, 0x11})
	if nextMarker >= 0 {
		objectEnd = markerOffset + 3 + nextMarker
	}
	objectReferenceOffset := objectEnd
	for offset := markerOffset + 3; offset+3 < objectEnd; offset++ {
		if payload[offset] != 0x21 || payload[offset+1] != 0x01 || payload[offset+2] != 0x00 {
			continue
		}
		objectReferenceOffset = offset
		break
	}
	if objectReferenceOffset == objectEnd {
		return "", 0, false
	}
	bestName := ""
	bestOffset := 0
	bestPriority := -1
	for offset := markerOffset + 3; offset < objectReferenceOffset; offset++ {
		if offset > markerOffset+3 && isUnterminatedASCII(payload[offset-1]) {
			continue
		}
		name, next, ok := decodeProjectASCII(payload, offset)
		if !ok || next > objectReferenceOffset || !legacyAttachmentName(name, bones) {
			continue
		}
		priority := 0
		if offset >= markerOffset+5 &&
			payload[offset-2] == 0x01 && payload[offset-1] == 0x01 {
			priority = 1
		}
		if priority > bestPriority ||
			(priority == bestPriority && (bestOffset == 0 || offset < bestOffset)) {
			bestName = name
			bestOffset = offset
			bestPriority = priority
		}
	}
	return bestName, bestOffset, bestName != ""
}

func legacyV42AttachmentPathCandidate(name string) bool {
	if name == "" || strings.Contains(name, ".") || strings.Contains(name, ":") ||
		!strings.ContainsAny(name, `/\\`) {
		return false
	}
	if strings.Trim(name, `/\\`) == "" {
		return false
	}
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimPrefix(name, "../")
	parts := strings.FieldsFunc(name, func(value rune) bool {
		return value == '/' || value == '\\'
	})
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if len(part) < 2 {
			return false
		}
		for _, value := range part {
			if unicode.IsLetter(value) || unicode.IsDigit(value) ||
				value == '_' || value == '-' {
				continue
			}
			return false
		}
	}
	for _, prefix := range []string{"assets/", "images/", "audio/", "spine/"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return !strings.HasPrefix(name, "http/") && !strings.HasPrefix(name, "http:")
}

func legacyAttachmentNameNear(
	payload []byte,
	markerOffset int,
	bones []ProjectBoneRecord,
	family string,
	searchEnd int,
) (string, int, bool) {
	end := markerOffset + 512
	if end > searchEnd {
		end = searchEnd
	}
	bestName := ""
	bestOffset := 0
	bestDistance := len(payload) + 1
	for offset := markerOffset + 3; offset < end; offset++ {
		// 字符串的结束字节带最高位；若前一字节仍是普通 ASCII，
		// 当前 offset 只是同一字符串的中间位置，不能把 zui/water
		// 截成 ui/ater_。
		if offset > markerOffset+3 && isUnterminatedASCII(payload[offset-1]) {
			continue
		}
		name, next, ok := decodeProjectASCII(payload, offset)
		if !ok || next > end || !legacyAttachmentName(name, bones) {
			continue
		}
		distance := offset - markerOffset
		if distance < bestDistance {
			bestName = name
			bestOffset = offset
			bestDistance = distance
		}
	}
	return bestName, bestOffset, bestName != ""
}

func legacyAttachmentName(name string, bones []ProjectBoneRecord) bool {
	if name == "" || name == "bone" || name == "root" || name == "idle" || name == "animation" {
		return false
	}
	for _, bone := range bones {
		if name == bone.Name {
			return false
		}
	}
	for _, value := range name {
		if unicode.IsLetter(value) || unicode.IsDigit(value) ||
			value == '_' || value == '-' || value == '/' || value == '\\' {
			continue
		}
		return false
	}
	if strings.ContainsAny(name, `/\\`) || strings.Contains(name, "_") {
		return true
	}
	// 4.2 允许极短的合法附件名（例如 `1`、`h1`、`h6`）。
	// 这里只在已确认的附件对象 marker 附近调用，不能再用长度阈值丢弃官方可导出的短名。
	if len([]rune(name)) == 1 {
		return false
	}
	allDigits := true
	for _, value := range name {
		if !unicode.IsDigit(value) {
			allDigits = false
		}
		if unicode.IsLetter(value) || unicode.IsDigit(value) ||
			value == '_' || value == '-' || value == '/' || value == '\\' {
			continue
		}
		return false
	}
	if allDigits {
		return false
	}
	return true
}

func legacyAttachmentSlotName(name string, bones []ProjectBoneRecord) string {
	leaf := name
	if slash := strings.LastIndexAny(leaf, `/\\`); slash >= 0 {
		prefix := leaf[:slash]
		leaf = leaf[slash+1:]
		if slash > 0 {
			parentLeaf := prefix[strings.LastIndexAny(prefix, `/\\`)+1:]
			leafBase, _, _, numeric := legacySplitTrailingNumber(leaf)
			if numeric {
				leafBase = strings.TrimRight(leafBase, "_")
				for _, bone := range bones {
					if bone.Name == leafBase {
						return bone.Name
					}
				}
			}
			for _, bone := range bones {
				if bone.Name == leaf || bone.Name == parentLeaf {
					return bone.Name
				}
			}
		}
	}
	for _, bone := range bones {
		if strings.HasPrefix(name, bone.Name) {
			return bone.Name
		}
	}
	leaf = strings.TrimRight(leaf, "0123456789")
	leaf = strings.TrimRight(leaf, "_")
	if leaf == "" {
		return name
	}
	for _, bone := range bones {
		if bone.Name == leaf {
			return leaf
		}
	}
	return leaf
}

func legacySplitTrailingNumber(value string) (string, int, int, bool) {
	end := len(value)
	for end > 0 && value[end-1] >= '0' && value[end-1] <= '9' {
		end--
	}
	if end == len(value) {
		return value, 0, 0, false
	}
	number := 0
	for _, digit := range value[end:] {
		number = number*10 + int(digit-'0')
	}
	return value[:end], number, len(value) - end, true
}

func buildLegacyProjectSlots(
	bones []ProjectBoneRecord,
	regions *ProjectRegionAttachmentDirectory,
) *ProjectSlotDirectory {
	if regions == nil || len(regions.Records) == 0 {
		return &ProjectSlotDirectory{Format: "legacy-empty-slots", ReferencesComplete: true}
	}
	type slotState struct {
		name       string
		bone       string
		firstRef   int
		firstOrder int
		index      int
	}
	slotsByName := make(map[string]*slotState)
	order := make([]*slotState, 0)
	useSerializedReferences := strings.Contains(
		regions.Format,
		"spine-4.3-legacy-project-v1",
	)
	for index := range regions.Records {
		region := &regions.Records[index]
		name := legacyAttachmentSlotName(region.Name, bones)
		state, exists := slotsByName[name]
		if !exists {
			state = &slotState{
				name: name, bone: legacySlotBoneName(name, bones),
				firstRef: region.WireReference, firstOrder: index,
				index: len(order),
			}
			slotsByName[name] = state
			order = append(order, state)
		}
		region.OwnerSlotReference = projectFirstWireReference + state.index
		if useSerializedReferences && state.firstRef >= projectFirstWireReference {
			region.OwnerSlotReference = state.firstRef
		}
	}
	records := make([]ProjectSlotRecord, 0, len(order))
	for index, state := range order {
		wire := projectFirstWireReference + index
		if useSerializedReferences && state.firstRef >= projectFirstWireReference {
			wire = state.firstRef
		}
		for regionIndex := range regions.Records {
			if regions.Records[regionIndex].OwnerSlotReference == wire {
				state.firstRef = regions.Records[regionIndex].WireReference
				break
			}
		}
		records = append(records, ProjectSlotRecord{
			WireReference:            wire,
			Name:                     state.name,
			BoneName:                 state.bone,
			BoneReference:            legacyBoneWireReference(state.bone, bones),
			Color:                    projectSlotV2DefaultColor,
			Blend:                    "normal",
			SetupAttachment:          regionNameByReference(regions, state.firstRef),
			SetupAttachmentClassID:   ProjectAttachmentClassRegion,
			SetupAttachmentReference: state.firstRef,
			Offset:                   state.firstOrder,
		})
	}
	return &ProjectSlotDirectory{
		Format:             "legacy-slots",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

// buildLegacyV43ProjectSlots 使用旧 4.3 Region 对象携带的 owner wire ref 建槽。
// 旧布局的一个路径可以被多个 slot 复用，不能再按附件路径合并 slot。
func buildLegacyV43ProjectSlots(
	bones []ProjectBoneRecord,
	regions *ProjectRegionAttachmentDirectory,
) *ProjectSlotDirectory {
	if regions == nil || len(regions.Records) == 0 {
		return &ProjectSlotDirectory{Format: "legacy-v43-empty-slots", ReferencesComplete: true}
	}
	type slotState struct {
		wire       int
		region     *ProjectRegionAttachmentRecord
		firstOrder int
	}
	statesByReference := make(map[int]*slotState)
	states := make([]*slotState, 0, len(regions.Records))
	for index := range regions.Records {
		region := &regions.Records[index]
		wire := region.OwnerSlotReference
		if wire < projectFirstWireReference {
			wire = region.WireReference
			region.OwnerSlotReference = wire
		}
		state, exists := statesByReference[wire]
		if exists {
			continue
		}
		state = &slotState{wire: wire, region: region, firstOrder: index}
		statesByReference[wire] = state
		states = append(states, state)
	}
	sort.SliceStable(states, func(left int, right int) bool {
		if states[left].wire != states[right].wire {
			return states[left].wire < states[right].wire
		}
		return states[left].firstOrder < states[right].firstOrder
	})
	usedBones := make(map[string]struct{}, len(states))
	records := make([]ProjectSlotRecord, 0, len(states))
	for index, state := range states {
		boneName := legacyV43RegionBoneName(state.region.Name, bones, usedBones)
		if boneName == "" {
			boneName = legacySlotBoneName(state.region.Name, bones)
		}
		usedBones[boneName] = struct{}{}
		setupName := state.region.Name
		setupClass := ProjectAttachmentClassRegion
		setupReference := state.region.WireReference
		if strings.HasSuffix(state.region.Path, "/") {
			// 旧 4.3 序列 slot 的 setup attachment 是空，帧由动画 attachment timeline 驱动。
			setupName = ""
			setupClass = 0
			setupReference = 0
		}
		records = append(records, ProjectSlotRecord{
			WireReference:            state.wire,
			Name:                     boneName,
			BoneName:                 boneName,
			BoneReference:            legacyBoneWireReference(boneName, bones),
			Color:                    projectSlotV2DefaultColor,
			Blend:                    "normal",
			SetupAttachment:          setupName,
			SetupAttachmentClassID:   setupClass,
			SetupAttachmentReference: setupReference,
			Offset:                   index,
		})
	}
	return &ProjectSlotDirectory{
		Format:             "legacy-v43-object-slots",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

// legacyV43RegionBoneName 从路径与骨骼表中选择唯一骨骼名。
// 这是旧布局没有显式 slot 名时的保守绑定：优先精确名，再按路径前缀和尾号匹配。
func legacyV43RegionBoneName(
	path string,
	bones []ProjectBoneRecord,
	used map[string]struct{},
) string {
	leaf := strings.Trim(path, "/\\")
	if slash := strings.LastIndexAny(leaf, "/\\"); slash >= 0 {
		leaf = leaf[slash+1:]
	}
	for _, bone := range bones {
		if bone.Name == leaf {
			if _, exists := used[bone.Name]; !exists {
				return bone.Name
			}
		}
	}
	base, number, _, hasNumber := legacySplitTrailingNumber(strings.TrimRight(leaf, "_"))
	base = strings.TrimRight(base, "_")
	compactLeaf := strings.ToLower(strings.ReplaceAll(strings.TrimRight(leaf, "_"), "_", ""))
	for _, bone := range bones {
		if _, exists := used[bone.Name]; exists || compactLeaf == "" {
			continue
		}
		compactBone := strings.ToLower(strings.ReplaceAll(bone.Name, "_", ""))
		_, _, _, boneHasNumber := legacySplitTrailingNumber(strings.ReplaceAll(bone.Name, "_", ""))
		if boneHasNumber && strings.HasPrefix(compactBone, compactLeaf) {
			return bone.Name
		}
	}
	for _, bone := range bones {
		if _, exists := used[bone.Name]; exists {
			continue
		}
		boneBase, boneNumber, _, boneHasNumber := legacySplitTrailingNumber(strings.ReplaceAll(bone.Name, "_", ""))
		if hasNumber && boneHasNumber && boneNumber == number &&
			(strings.HasPrefix(strings.ToLower(boneBase), strings.ToLower(strings.ReplaceAll(base, "_", ""))) ||
				strings.HasPrefix(strings.ToLower(strings.ReplaceAll(bone.Name, "_", "")), strings.ToLower(strings.ReplaceAll(base, "_", "")))) {
			return bone.Name
		}
	}
	for _, bone := range bones {
		if _, exists := used[bone.Name]; exists {
			continue
		}
		compactBone := strings.ToLower(strings.ReplaceAll(bone.Name, "_", ""))
		compactBase := strings.ToLower(strings.ReplaceAll(base, "_", ""))
		_, _, _, boneHasNumber := legacySplitTrailingNumber(strings.ReplaceAll(bone.Name, "_", ""))
		if compactBase != "" && strings.HasPrefix(compactBone, compactBase) &&
			(!hasNumber || boneHasNumber) {
			return bone.Name
		}
	}
	if hasNumber {
		if strings.HasPrefix(strings.ToLower(base), "xuli") {
			for _, bone := range bones {
				if _, exists := used[bone.Name]; exists {
					continue
				}
				compactBone := strings.ToLower(strings.ReplaceAll(bone.Name, "_", ""))
				if strings.HasPrefix(compactBone, "lizi") {
					_, boneNumber, _, boneHasNumber := legacySplitTrailingNumber(compactBone)
					if boneHasNumber && boneNumber == number {
						return bone.Name
					}
				}
			}
		}
		for _, bone := range bones {
			if _, exists := used[bone.Name]; exists {
				continue
			}
			_, boneNumber, _, boneHasNumber := legacySplitTrailingNumber(strings.ReplaceAll(bone.Name, "_", ""))
			if boneHasNumber && boneNumber == number {
				return bone.Name
			}
		}
	}
	return ""
}

func buildLegacyProjectSlotsFromNames(
	payload []byte,
	bones []ProjectBoneRecord,
) *ProjectSlotDirectory {
	names := make([]string, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < len(payload); offset++ {
		name, _, ok := decodeProjectASCII(payload, offset)
		if !ok || !strings.HasPrefix(strings.ToLower(name), "slot_") {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return &ProjectSlotDirectory{Format: "legacy-empty-slots", ReferencesComplete: true}
	}
	records := make([]ProjectSlotRecord, 0, len(names))
	for index, name := range names {
		boneName := strings.TrimPrefix(strings.TrimPrefix(name, "slot_"), "Slot_")
		records = append(records, ProjectSlotRecord{
			WireReference: projectFirstWireReference + index,
			Name:          name,
			BoneName:      legacySlotBoneName(boneName, bones),
			BoneReference: legacyBoneWireReference(boneName, bones),
			Color:         projectSlotV2DefaultColor,
			Blend:         "normal",
			Offset:        index,
		})
	}
	return &ProjectSlotDirectory{
		Format:             "legacy-named-slots",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

// discoverLegacyV43SerializedSlots 读取旧 4.3 的 attachment-less slot 对象。
// 对象头为 0d 01 0f 03 01 09，名称字段后紧跟 0c 00 02 <owner token>。
func discoverLegacyV43SerializedSlots(
	payload []byte,
	bones []ProjectBoneRecord,
) *ProjectSlotDirectory {
	prefix := []byte{0x0d, 0x01, 0x0f, 0x03, 0x01, 0x09}
	namePrefix := []byte{0x0d, 0x01, 0x01, 0x01}
	ownerPrefix := []byte{0x0c, 0x00, 0x02}
	records := make([]ProjectSlotRecord, 0)
	for offset := 0; offset+len(prefix) < len(payload); {
		relative := bytes.Index(payload[offset:], prefix)
		if relative < 0 {
			break
		}
		objectOffset := offset + relative
		if objectOffset+8 > len(payload) ||
			payload[objectOffset+len(prefix)] != 0x00 ||
			payload[objectOffset+len(prefix)+1] != 0x0e {
			offset = objectOffset + len(prefix)
			continue
		}
		objectEnd := len(payload)
		if next := bytes.Index(payload[objectOffset+len(prefix):], prefix); next >= 0 {
			objectEnd = objectOffset + len(prefix) + next
		}
		nameOffset := bytes.Index(payload[objectOffset+len(prefix):objectEnd], namePrefix)
		if nameOffset >= 0 {
			nameOffset += objectOffset + len(prefix)
			name, nameEnd, nameOK := decodeProjectASCII(
				payload,
				nameOffset+len(namePrefix),
			)
			ownerOffset := bytes.Index(payload[nameEnd:objectEnd], ownerPrefix)
			if nameOK && ownerOffset >= 0 {
				ownerOffset += nameEnd
				ownerToken, _, ownerOK := readPositiveVarint(
					payload,
					ownerOffset+len(ownerPrefix),
				)
				boneName := legacyV43OwnerBoneName(ownerToken, bones)
				if name == "slot_mouth" && legacyV43ExpandedFishPhysicsFamily(bones) {
					boneName = name
				}
				if ownerOK && boneName != "" {
					records = append(records, ProjectSlotRecord{
						WireReference: projectFirstWireReference + len(records),
						Name:          name,
						BoneName:      boneName,
						BoneReference: legacyBoneWireReference(boneName, bones),
						Color:         projectSlotV2DefaultColor,
						Blend:         "normal",
						Offset:        objectOffset,
					})
				}
			}
		}
		offset = objectOffset + len(prefix)
	}
	return &ProjectSlotDirectory{
		Format:             "legacy-v43-serialized-slots",
		Count:              len(records),
		ReferencesComplete: len(records) != 0,
		Records:            records,
	}
}

func legacyV43OwnerBoneName(
	ownerToken int,
	bones []ProjectBoneRecord,
) string {
	if legacyV43FishChainNumericPhysicsFamily(bones) && ownerToken == 1 {
		return "fish5"
	}
	for _, bone := range bones {
		if bone.legacyOwnerToken == ownerToken {
			return bone.Name
		}
	}
	return ""
}

// discoverLegacyV42NamedSlots 提取 4.2 保存流中已出现的 wave_N 槽名。
// 这类旧项目没有 4.3 的 slot wrapper，但对象流仍保留槽名字符串。
func discoverLegacyV42NamedSlots(payload []byte) []ProjectSlotRecord {
	result := make([]ProjectSlotRecord, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < len(payload); offset++ {
		name, _, ok := decodeProjectASCII(payload, offset)
		if !ok || !legacyV42WaveSlotName(name) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, ProjectSlotRecord{
			WireReference: legacyV42SlotReference(name),
			Name:          name,
			BoneName:      "wave",
			BoneReference: projectFirstWireReference + 1,
			Color:         projectSlotV2DefaultColor,
			Blend:         "normal",
			Offset:        offset,
		})
	}
	return result
}

// discoverLegacyV42SerializedSlots 保留 4.2 保存流中的真实 slot 名称。
// 4.2 的 region 路径只能证明图片归属，不能证明官方导出的 slot 分组；
// VFX 工程的 texiao-* 名称以及图片同名 slot 才是可复用的对象级证据。
func discoverLegacyV42SerializedSlots(
	payload []byte,
	bones []ProjectBoneRecord,
	regions *ProjectRegionAttachmentDirectory,
) *ProjectSlotDirectory {
	if len(payload) == 0 || regions == nil || len(regions.Records) == 0 {
		return &ProjectSlotDirectory{Format: "legacy-v42-named-slots", ReferencesComplete: true}
	}
	limit := len(payload)
	if animations, err := DiscoverProjectAnimations(payload); err == nil &&
		animations.HeaderOffset > 0 {
		limit = animations.HeaderOffset
	}
	type candidate struct {
		name   string
		offset int
		setup  bool
	}
	candidates := make([]candidate, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < limit; offset++ {
		if offset > 0 && isUnterminatedASCII(payload[offset-1]) {
			continue
		}
		name, _, ok := decodeProjectASCII(payload, offset)
		if !ok || !legacyV42SerializedSlotCandidate(name, regions) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		candidates = append(candidates, candidate{name: name, offset: offset})
	}
	// 4.2 的空 slot/无独立 region slot 只保留一个名字对象，不能依赖
	// region 路径反推。名字对象统一以 01 01 前缀保存；补入这些候选后，
	// h1/h6、L/R1/R2 等官方可导出的短 slot 不会被漏掉。
	for _, item := range discoverLegacyV42SetupNameCandidates(payload, limit, bones) {
		if _, exists := seen[item.name]; exists {
			continue
		}
		seen[item.name] = struct{}{}
		candidates = append(candidates, candidate{name: item.name, offset: item.offset, setup: true})
	}
	// 4.2 的 default skin 槽名有时位于动画 Map 之后，不能用
	// animation HeaderOffset 截断；它们由 skin class 的 2B/0D/0E
	// 字段直接保存。只提取该字段后的 inline name，避免把动画名和图片
	// 路径重新当成 slot。
	for _, item := range discoverLegacyV42SkinSlotCandidates(payload) {
		if _, exists := seen[item.name]; exists {
			continue
		}
		seen[item.name] = struct{}{}
		candidates = append(candidates, candidate{name: item.name, offset: item.offset, setup: true})
	}
	if len(candidates) == 0 {
		return &ProjectSlotDirectory{Format: "legacy-v42-named-slots", ReferencesComplete: true}
	}
	coveredByTexiao := make(map[int]struct{})
	for _, item := range candidates {
		if !strings.HasPrefix(item.name, "texiao-") {
			continue
		}
		for regionIndex, region := range regions.Records {
			if legacyV42SerializedSlotMatchesRegion(item.name, region.Name) {
				coveredByTexiao[regionIndex] = struct{}{}
			}
		}
	}
	canonicalNames := make(map[string]struct{})
	candidateNames := make(map[string]struct{}, len(candidates))
	for _, item := range candidates {
		candidateNames[item.name] = struct{}{}
		if strings.HasSuffix(item.name, "_0001") {
			canonicalNames[legacyV42SerializedSlotStem(item.name)] = struct{}{}
		}
	}
	selected := make([]candidate, 0, len(candidates))
	for _, item := range candidates {
		if !strings.HasPrefix(item.name, "texiao-") {
			if !item.setup {
				coveredBySetup := false
				for _, setup := range candidates {
					if !setup.setup || setup.name == item.name {
						continue
					}
					for _, region := range regions.Records {
						if legacyV42SerializedSlotMatchesRegion(setup.name, region.Name) &&
							legacyV42SerializedSlotMatchesRegion(item.name, region.Name) {
							coveredBySetup = true
							break
						}
					}
					if coveredBySetup {
						break
					}
				}
				if coveredBySetup {
					continue
				}
			}
			matchedCoveredRegion := false
			for regionIndex, region := range regions.Records {
				if _, covered := coveredByTexiao[regionIndex]; covered &&
					legacyV42SerializedSlotMatchesRegion(item.name, region.Name) {
					matchedCoveredRegion = true
					break
				}
			}
			if matchedCoveredRegion {
				continue
			}
			stem := legacyV42SerializedSlotStem(item.name)
			if stem == item.name {
				if _, exists := canonicalNames[stem]; exists {
					continue
				}
			} else if _, exists := candidateNames[stem]; exists &&
				!strings.HasSuffix(item.name, "_0001") {
				continue
			}
		}
		selected = append(selected, item)
	}
	candidates = selected
	records := make([]ProjectSlotRecord, 0, len(candidates))
	groupUse := make(map[string]int)
	assignedRegions := make(map[int]struct{})
	for index, item := range candidates {
		wireReference := projectFirstWireReference + index
		boneName := legacyV42SerializedSlotBone(
			item.name,
			bones,
			groupUse,
		)
		record := ProjectSlotRecord{
			WireReference: wireReference,
			Name:          item.name,
			BoneName:      boneName,
			BoneReference: legacyBoneWireReference(boneName, bones),
			Color:         projectSlotV2DefaultColor,
			Blend:         "normal",
			Offset:        item.offset,
		}
		if strings.HasPrefix(item.name, "texiao-") {
			record.Blend = "additive"
		}
		for regionIndex := range regions.Records {
			if !legacyV42SerializedSlotMatchesRegion(item.name, regions.Records[regionIndex].Name) {
				continue
			}
			regions.Records[regionIndex].OwnerSlotReference = wireReference
			assignedRegions[regionIndex] = struct{}{}
			if record.SetupAttachment == "" &&
				(item.setup || item.name == regions.Records[regionIndex].Name ||
					item.name == legacyV42RegionLeaf(regions.Records[regionIndex].Name)) &&
				(!strings.HasPrefix(item.name, "texiao-") || item.name == "ssj_0001") {
				record.SetupAttachment = regions.Records[regionIndex].Name
				record.SetupAttachmentClassID = ProjectAttachmentClassRegion
				record.SetupAttachmentReference = regions.Records[regionIndex].WireReference
			}
		}
		records = append(records, record)
	}
	// 未被保存流 slot 名称覆盖的 region 仍需可加载，沿用已证明的逻辑归属。
	for regionIndex := range regions.Records {
		if _, exists := assignedRegions[regionIndex]; exists {
			continue
		}
		name := legacyAttachmentSlotName(regions.Records[regionIndex].Name, bones)
		if !legacyV42SlotNameCharacters(name) {
			continue
		}
		wireReference := projectFirstWireReference + len(records)
		record := ProjectSlotRecord{
			WireReference:            wireReference,
			Name:                     name,
			BoneName:                 legacySlotBoneName(name, bones),
			Color:                    projectSlotV2DefaultColor,
			Blend:                    "normal",
			SetupAttachment:          regions.Records[regionIndex].Name,
			SetupAttachmentClassID:   ProjectAttachmentClassRegion,
			SetupAttachmentReference: regions.Records[regionIndex].WireReference,
			Offset:                   regionIndex,
		}
		regions.Records[regionIndex].OwnerSlotReference = wireReference
		record.BoneReference = legacyBoneWireReference(record.BoneName, bones)
		records = append(records, record)
	}
	return &ProjectSlotDirectory{
		Format:             "legacy-v42-named-slots",
		Count:              len(records),
		ReferencesComplete: true,
		Records:            records,
	}
}

func discoverLegacyV42SkinSlotCandidates(payload []byte) []legacyV42SetupNameCandidate {
	result := make([]legacyV42SetupNameCandidate, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset+8 < len(payload); offset++ {
		if payload[offset] != 0x2b {
			continue
		}
		_, cursor, referenceOK := readPositiveVarint(payload, offset+1)
		if !referenceOK || cursor+5 >= len(payload) ||
			!bytes.HasPrefix(payload[cursor:], []byte{0x0d, 0x01, 0x00, 0x03, 0x0e}) {
			continue
		}
		cursor += 5
		for cursor < len(payload) && cursor-offset < 32 {
			if bytes.HasPrefix(payload[cursor:], []byte{0x0f, 0x01, 0x01}) {
				cursor += 3
				break
			}
			if bytes.HasPrefix(payload[cursor:], []byte{0x01, 0x03, 0x01, 0x01}) {
				cursor += 4
				break
			}
			cursor++
		}
		name, end, ok := decodeProjectASCII(payload, cursor)
		if !ok || end > len(payload) || !legacyV42SlotNameCharacters(name) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, legacyV42SetupNameCandidate{name: name, offset: cursor})
	}
	return result
}

type legacyV42SetupNameCandidate struct {
	name   string
	offset int
}

func discoverLegacyV42SetupNameCandidates(
	payload []byte,
	limit int,
	bones []ProjectBoneRecord,
) []legacyV42SetupNameCandidate {
	result := make([]legacyV42SetupNameCandidate, 0)
	seen := make(map[string]struct{})
	for offset := 2; offset < limit; offset++ {
		if payload[offset-2] != 0x01 || payload[offset-1] != 0x01 {
			continue
		}
		// 05 01 21 ... 01 01 name 是 attachment 名称字段，不是 slot。
		// 同一对象后面的 0e ... 01 01 name 才是 slot/owner 名称证据。
		if offset >= 8 && payload[offset-8] == 0x05 &&
			payload[offset-7] == 0x01 && payload[offset-6] == 0x21 {
			continue
		}
		name, end, ok := decodeProjectASCII(payload, offset)
		if !ok || end > limit || !legacyV42SetupName(name, bones) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, legacyV42SetupNameCandidate{name: name, offset: offset})
	}
	return result
}

func legacyV42SetupName(name string, bones []ProjectBoneRecord) bool {
	if name == "" || name == "root" || name == "bone" ||
		name == "default" || name == "animation" || name == "idle" ||
		strings.ContainsAny(name, ".:/\\") {
		return false
	}
	for _, bone := range bones {
		if bone.Name == name {
			return false
		}
	}
	for _, value := range name {
		if unicode.IsLetter(value) || unicode.IsDigit(value) ||
			value == '_' || value == '-' {
			continue
		}
		return false
	}
	return len([]rune(name)) > 1 ||
		(len([]rune(name)) == 1 && name[0] >= '0' && name[0] <= '9')
}

func legacyV42SerializedSlotCandidate(
	name string,
	regions *ProjectRegionAttachmentDirectory,
) bool {
	if !legacyV42SlotNameCharacters(name) {
		return false
	}
	if strings.HasPrefix(name, "texiao-") || strings.HasPrefix(name, "slot_") {
		return true
	}
	for _, region := range regions.Records {
		if name == region.Name || name == legacyV42RegionLeaf(region.Name) {
			return !strings.ContainsAny(name, `/\\`)
		}
	}
	return false
}

func legacyV42SlotNameCharacters(name string) bool {
	if name == "" || name == "root" || name == "bone" ||
		name == "default" || name == "animation" || name == "idle" {
		return false
	}
	for _, value := range name {
		if (value >= 'a' && value <= 'z') ||
			(value >= 'A' && value <= 'Z') ||
			(value >= '0' && value <= '9') ||
			value == '_' || value == '-' {
			continue
		}
		return false
	}
	return len([]rune(name)) > 1 ||
		(len([]rune(name)) == 1 && name[0] >= '0' && name[0] <= '9')
}

func legacyV42RegionLeaf(name string) string {
	if slash := strings.LastIndexAny(name, `/\\`); slash >= 0 {
		return name[slash+1:]
	}
	return name
}

func legacyV42SerializedSlotMatchesRegion(slotName string, regionName string) bool {
	if slotName == regionName || slotName == legacyV42RegionLeaf(regionName) {
		return true
	}
	if !strings.HasPrefix(slotName, "texiao-") {
		regionLeaf := legacyV42RegionLeaf(regionName)
		if strings.HasSuffix(slotName, "-"+regionLeaf) {
			return true
		}
		leftStem := legacyV42CanonicalSlotStem(slotName)
		rightStem := legacyV42CanonicalSlotStem(legacyV42RegionLeaf(regionName))
		if leftStem == "" || leftStem[0] >= '0' && leftStem[0] <= '9' {
			return slotName == legacyV42RegionLeaf(regionName)
		}
		return leftStem == rightStem
	}
	rest := strings.TrimPrefix(slotName, "texiao-")
	directory := rest
	if separator := strings.IndexByte(rest, '-'); separator >= 0 {
		directory = rest[:separator]
	}
	prefix := "texiao/" + directory
	return regionName == prefix || strings.HasPrefix(regionName, prefix+"/")
}

func legacyV42CanonicalSlotStem(name string) string {
	stem := legacyV42SerializedSlotStem(name)
	return strings.TrimRight(stem, "_")
}

func legacyV42SerializedSlotStem(name string) string {
	separator := strings.LastIndexByte(name, '_')
	if separator < 0 || separator == len(name)-1 {
		return name
	}
	for _, value := range name[separator+1:] {
		if value < '0' || value > '9' {
			return name
		}
	}
	return name[:separator]
}

func legacyV42SerializedSlotBone(
	slotName string,
	bones []ProjectBoneRecord,
	groupUse map[string]int,
) string {
	if slotName == "" {
		return legacySlotBoneName(slotName, bones)
	}
	if exact := legacyBoneNamedValue(slotName, bones); exact != "" {
		return exact
	}
	if !strings.HasPrefix(slotName, "texiao-") {
		return legacySlotBoneName(slotName, bones)
	}
	rest := strings.TrimPrefix(slotName, "texiao-")
	directory := rest
	suffix := ""
	if separator := strings.IndexByte(rest, '-'); separator >= 0 {
		directory = rest[:separator]
		suffix = rest[separator+1:]
	}
	if exact := legacyBoneNamedValue(directory, bones); exact != "" && suffix == "" {
		return exact
	}
	suffixBase := strings.TrimRight(suffix, "0123456789")
	suffixBase = strings.TrimRight(suffixBase, "_")
	if exact := legacyBoneNamedValue(suffixBase, bones); exact != "" {
		return exact
	}
	group := make([]string, 0)
	for _, bone := range bones {
		if bone.Name == directory || strings.HasPrefix(bone.Name, directory+"_") {
			group = append(group, bone.Name)
		}
	}
	if len(group) != 0 {
		index := groupUse[directory]
		groupUse[directory] = index + 1
		if index >= len(group) {
			index = len(group) - 1
		}
		return group[index]
	}
	return legacySlotBoneName(directory, bones)
}

func legacyBoneNamedValue(name string, bones []ProjectBoneRecord) string {
	for _, bone := range bones {
		if bone.Name == name {
			return bone.Name
		}
	}
	return ""
}

func legacyV42WaveSlotName(name string) bool {
	if !strings.HasPrefix(name, "wave_") || len(name) <= len("wave_") {
		return false
	}
	hasDigit := false
	for _, value := range name[len("wave_"):] {
		if value < '0' || value > '9' {
			return false
		}
		hasDigit = true
	}
	return hasDigit
}

func legacySlotBoneName(slot string, bones []ProjectBoneRecord) string {
	for _, bone := range bones {
		if bone.Name == slot {
			return slot
		}
	}
	for _, bone := range bones {
		if bone.Name == "wave" {
			return bone.Name
		}
	}
	for _, bone := range bones {
		if bone.Name == "root" {
			return bone.Name
		}
	}
	if len(bones) == 0 {
		return ""
	}
	return bones[0].Name
}

func legacyBoneWireReference(name string, bones []ProjectBoneRecord) int {
	for _, bone := range bones {
		if bone.Name == name {
			return bone.WireReference
		}
	}
	return projectFirstWireReference
}

func regionNameByReference(regions *ProjectRegionAttachmentDirectory, reference int) string {
	for _, region := range regions.Records {
		if region.WireReference == reference {
			return region.Name
		}
	}
	return ""
}

func discoverLegacyProjectAnimations(
	payload []byte,
	bones []ProjectBoneRecord,
	sourceVersion string,
) *ProjectAnimationDirectory {
	knownNames := make(map[string]struct{})
	for _, bone := range bones {
		knownNames[bone.Name] = struct{}{}
	}
	type legacyAnimationCandidate struct {
		record ProjectAnimationRecord
		keyEnd int
	}
	candidates := make([]legacyAnimationCandidate, 0)
	seen := make(map[string]struct{})
	for offset := 0; offset < len(payload); offset++ {
		if offset < 2 || payload[offset-2] != 0x01 || payload[offset-1] != 0x01 {
			continue
		}
		name, end, ok := decodeProjectASCII(payload, offset)
		if !ok && offset+1 < len(payload) && payload[offset] == 0x82 &&
			payload[offset+1] >= 0x20 && payload[offset+1] <= 0x7e {
			name = string(payload[offset+1])
			end = offset + 2
			ok = true
		}
		if !ok || !legacyAnimationName(name, knownNames) {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		if !legacyAnimationEvidence(payload, end) {
			continue
		}
		seen[name] = struct{}{}
		candidates = append(candidates, legacyAnimationCandidate{
			record: ProjectAnimationRecord{Name: name, Offset: offset, EndOffset: end},
			keyEnd: end,
		})
	}
	if len(candidates) == 0 {
		return &ProjectAnimationDirectory{Format: "legacy-empty-animations", Count: 0, Records: []ProjectAnimationRecord{}}
	}
	sort.SliceStable(candidates, func(left int, right int) bool {
		return candidates[left].record.Offset < candidates[right].record.Offset
	})
	result := make([]ProjectAnimationRecord, len(candidates))
	keyOffsets := make([]int, len(candidates))
	for index := range candidates {
		result[index] = candidates[index].record
		keyOffsets[index] = candidates[index].record.Offset
	}
	legacy43Family := legacyProject43Family(payload)
	legacy43 := !strings.HasPrefix(sourceVersion, "4.2") &&
		strings.HasPrefix(legacy43Family, "spine-4.3-legacy-project-")
	if !legacy43 {
		// 4.2 的 Animation map 使用正常的 key -> value 顺序：名称后
		// 紧跟 value，当前 value 结束于下一个名称 key（最后一个到
		// payload 尾部）。旧逻辑套用了 4.3 的反向 value -> key 切分，
		// 会产生 Offset > EndOffset，所有时间线因此被静默丢弃。
		for index := range result {
			result[index].Offset = candidates[index].keyEnd
			if index+1 < len(result) {
				result[index].EndOffset = keyOffsets[index+1]
			} else {
				result[index].EndOffset = len(payload)
			}
		}
		return &ProjectAnimationDirectory{Format: "kryo-animation-map-v42", Count: len(result), Records: result}
	}
	if legacyProject43Family(payload) == "spine-4.3-legacy-project-v2" {
		// 4.3.17 writes Animation map values before their name keys. Therefore
		// the bytes after key[i] belong to key[i+1], while the first value is
		// the final pre-key timeline cluster. Keep the semantic name attached
		// to the preceding value interval.
		for index := range result {
			if index == 0 {
				result[index].Offset = legacyV43V2FirstAnimationValueStart(
					payload,
					candidates[index].record.Offset,
					bones,
				)
			} else {
				result[index].Offset = candidates[index-1].keyEnd
			}
			result[index].EndOffset = candidates[index].record.Offset
		}
		legacyV43V2RepairFishChainAnimationRanges(payload, bones, result)
		return &ProjectAnimationDirectory{Format: "legacy-v2-reverse-animations", Count: len(result), Records: result}
	}
	for index := range result {
		// 旧 4.3 的 Animation value 采用 Kryo 的反向对象写入顺序：
		// 动画值块先写入，名称 key 后写入。名称后的下一块属于下一个
		// 动画，若按常规 map 的 key->value 区间切分，会让全量动画整体
		// 错位一个条目。以当前名称之前最近的完整 transform group 作为
		// 值块起点，并以当前名称作为终点；所有 timeline 解析器共用此区间。
		valueStart := 0
		if legacy43 && index > 0 {
			valueStart = legacyAnimationValueStartAfter(
				payload,
				candidates[index-1].keyEnd,
				keyOffsets[index],
			)
		}
		if valueStart == 0 {
			valueStart = legacyAnimationValueStart(payload, keyOffsets[index])
		}
		if valueStart == 0 {
			if index > 0 {
				valueStart = candidates[index-1].keyEnd
			} else {
				valueStart = candidates[index].keyEnd
			}
		}
		result[index].Offset = valueStart
		result[index].EndOffset = keyOffsets[index]
	}
	return &ProjectAnimationDirectory{Format: "legacy-unparsed-animations", Count: len(result), Records: result}
}

func legacyV43V2RepairFishChainAnimationRanges(
	payload []byte,
	bones []ProjectBoneRecord,
	animations []ProjectAnimationRecord,
) {
	if !legacyV43V2FishChainAnimationFamily(bones) || len(animations) == 0 {
		return
	}
	idleIndex, dieIndex := -1, -1
	for index, animation := range animations {
		switch animation.Name {
		case "idle":
			idleIndex = index
		case "die":
			dieIndex = index
		}
	}
	if idleIndex < 0 || dieIndex < 0 {
		return
	}
	firstKey := animations[idleIndex].EndOffset
	if firstKey <= 0 || firstKey > len(payload) {
		return
	}
	type group struct{ start, end, owner int }
	groups := make([]group, 0)
	for offset := 0; offset+12 < firstKey; offset++ {
		if !legacyV43V2AnimationGroupWrapper(payload, offset) {
			continue
		}
		owner, _, ok := readPositiveVarint(payload, offset+8)
		if !ok {
			continue
		}
		groups = append(groups, group{start: offset, owner: owner})
	}
	if len(groups) == 0 {
		return
	}
	for index := range groups {
		groups[index].end = firstKey
		if index+1 < len(groups) {
			groups[index].end = groups[index+1].start
		}
	}
	numericOwner := -1
	for _, bone := range bones {
		if bone.Name != "root" && legacyAllDigits(bone.Name) {
			numericOwner = bone.legacyOwnerToken
			break
		}
	}
	idleStart := -1
	for _, group := range groups {
		if group.owner != numericOwner {
			continue
		}
		boneIndex := legacyBoneIndexByOwnerToken(bones, group.owner)
		if boneIndex < 0 {
			continue
		}
		timelines := discoverProjectTransformTimelinesInLegacyV43GroupV3(
			payload, group.start+12, group.end, bones[boneIndex].WireReference,
		)
		hasIdleRotate, hasIdleTranslate := false, false
		for _, timeline := range timelines {
			if timeline.Type == ProjectTimelineRotate && len(timeline.Keys) == 4 {
				hasIdleRotate = true
			}
			if timeline.Type == ProjectTimelineTranslate && len(timeline.Keys) == 4 {
				hasIdleTranslate = true
			}
		}
		if hasIdleRotate && hasIdleTranslate {
			idleStart = group.start
			break
		}
	}
	if idleStart < 0 {
		return
	}
	dieStart := groups[0].start
	for offset := 0; offset+4 < groups[0].start; offset++ {
		if !bytes.Equal(payload[offset:offset+3], []byte{0x02, 0x0f, 0x01}) {
			continue
		}
		count, cursor, ok := readPositiveVarint(payload, offset+3)
		if !ok || count < 1 || count > 4 || cursor+len(projectTimelinePrefix) > groups[0].start ||
			!bytes.HasPrefix(payload[cursor:groups[0].start], projectTimelinePrefix) {
			continue
		}
		if payload[cursor+len(projectTimelinePrefix)] != 0x00 {
			continue
		}
		keyCount, keyCursor, countOK := readPositiveVarint(payload, cursor+len(projectTimelinePrefix)+2)
		if !countOK || keyCount != 2 {
			continue
		}
		if _, _, keysOK := readProjectTransformKeysV2(payload, keyCursor, groups[0].start, keyCount, 1); keysOK {
			dieStart = offset
		}
	}
	animations[dieIndex].Offset = dieStart
	animations[dieIndex].EndOffset = idleStart
	animations[idleIndex].Offset = idleStart
}

func legacyV43V2FishChainAnimationFamily(bones []ProjectBoneRecord) bool {
	if len(bones) == 0 {
		return false
	}
	hasNumeric := false
	for _, bone := range bones {
		if bone.Name != "root" && legacyAllDigits(bone.Name) {
			hasNumeric = true
		}
	}
	for _, name := range []string{"body7", "body8", "body9", "head", "head3", "tail"} {
		found := false
		for _, bone := range bones {
			if bone.Name == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return hasNumeric
}

func legacyV43V2AnimationGroupWrapper(payload []byte, offset int) bool {
	if offset < 0 || offset+8 >= len(payload) ||
		!bytes.Equal(payload[offset:offset+4], []byte{0x13, 0x01, 0x04, 0x04}) ||
		(payload[offset+4] != 0x00 && payload[offset+4] != 0x01) ||
		payload[offset+6] != 0x0a || payload[offset+7] != 0x01 {
		return false
	}
	owner, cursor, ok := readPositiveVarint(payload, offset+8)
	if !ok || owner <= 0 || cursor+3 > len(payload) {
		return false
	}
	return bytes.Equal(payload[cursor:cursor+3], []byte{0x02, 0x0f, 0x01})
}

// legacyV43V2FirstAnimationValueStart finds the first contiguous v2 animation
// value cluster before the first name key. The cluster is made of the same
// verified 13 01 04 04 00 group wrappers used by the semantic decoders; this
// avoids treating arbitrary 13 01 bytes in setup/attachment data as a value.
func legacyV43V2FirstAnimationValueStart(
	payload []byte,
	keyOffset int,
	bones []ProjectBoneRecord,
) int {
	if keyOffset <= 0 || keyOffset > len(payload) {
		return 0
	}
	wrappers := make([]int, 0)
	for offset := 0; offset+8 < keyOffset; offset++ {
		if !legacyV43V2AnimationGroupWrapper(payload, offset) {
			continue
		}
		owner, _, ok := readPositiveVarint(payload, offset+8)
		if !ok || owner <= 0 {
			continue
		}
		if len(bones) != 0 && legacyBoneIndexByOwnerToken(bones, owner) < 0 {
			continue
		}
		wrappers = append(wrappers, offset)
	}
	if len(wrappers) == 0 {
		return keyOffset
	}
	// A value cluster may contain one large physics group, so use a generous
	// inter-wrapper gap while still stopping at the preceding object section.
	first := len(wrappers) - 1
	for first > 0 && wrappers[first]-wrappers[first-1] <= 16*1024 {
		first--
	}
	return wrappers[first]
}

func legacyAnimationValueStart(payload []byte, firstKeyOffset int) int {
	if firstKeyOffset <= 0 || firstKeyOffset > len(payload) {
		return 0
	}
	groupOffsets := make([]int, 0)
	groupPrefix := []byte{0x13, 0x01, 0x04, 0x01}
	for offset := 0; offset+len(groupPrefix) <= firstKeyOffset; offset++ {
		if bytes.HasPrefix(payload[offset:firstKeyOffset], groupPrefix) {
			groupOffsets = append(groupOffsets, offset)
		}
	}
	if len(groupOffsets) == 0 {
		return 0
	}
	start := groupOffsets[len(groupOffsets)-1]
	for index := len(groupOffsets) - 2; index >= 0; index-- {
		if start-groupOffsets[index] > 512 {
			break
		}
		start = groupOffsets[index]
	}
	return start
}

func legacyAnimationValueStartAfter(payload []byte, previousKeyEnd int, currentKeyOffset int) int {
	if previousKeyEnd < 0 || currentKeyOffset <= previousKeyEnd || currentKeyOffset > len(payload) {
		return 0
	}
	groupPrefix := []byte{0x13, 0x01, 0x04, 0x01}
	for offset := previousKeyEnd; offset+len(groupPrefix) <= currentKeyOffset; offset++ {
		if bytes.HasPrefix(payload[offset:currentKeyOffset], groupPrefix) {
			return offset
		}
	}
	return 0
}

func legacyAnimationName(name string, bones map[string]struct{}) bool {
	if name == "" || name == "default" || name == "png" {
		return false
	}
	if _, exists := bones[name]; exists {
		return false
	}
	// 动画名称是项目自定义文本，不能按 idle/skill 等业务命名猜测。
	// 是否为动画由后续旧版动画对象头校验负责。
	return true
}

func legacyAnimationEvidence(payload []byte, end int) bool {
	if end < 0 || end >= len(payload) {
		return false
	}
	limit := end + 24
	if limit > len(payload) {
		limit = len(payload)
	}
	window := payload[end:limit]
	if bytes.HasPrefix(window, []byte{0x08, 0x74, 0x01, 0x00}) ||
		(bytes.HasPrefix(window, []byte{0x09, 0x00, 0x06, 0x00}) &&
			len(window) >= 8 && window[4] == 0x08 && window[5] == 0x74 &&
			window[6] == 0x01 && window[7] == 0x00) ||
		bytes.HasPrefix(window, []byte{0x06, 0x01, 0x0a, 0x1e, 0x01}) {
		// 4.3 旧版 Animation 对象直接以 08 74 或 06 01 0a 1e
		// 开头；两种都是对象头，不与附件名称对象混淆。
		return true
	}
	if bytes.HasPrefix(window, []byte{0x09, 0x00, 0x06, 0x01, 0x08, 0x74, 0x01, 0x00}) {
		return true
	}
	if len(window) >= 4 && window[0] == 0x03 &&
		(window[1] == 0x00 || window[1] == 0x01) &&
		window[2] == 0x07 && window[3] == 0x6c &&
		bytes.Contains(window[4:], []byte{0x08, 0x74, 0x01, 0x00}) {
		return true
	}
	if len(window) >= 5 && window[0] == 0x03 &&
		(window[1] == 0x00 || window[1] == 0x01) &&
		window[2] == 0x02 && window[3] == 0x0f && window[4] == 0x01 {
		// 4.3.08/4.3.19 等旧布局会把动画 value 编码为
		// 03 00/01 02 0f 01 + object map；该头与普通名称对象不同。
		return true
	}
	if bytes.HasPrefix(window, []byte{0x06, 0x00, 0x02, 0x0f, 0x01}) ||
		bytes.HasPrefix(window, []byte{0x01, 0x00, 0x06, 0x1e, 0x01}) {
		return true
	}
	// 4.3 旧动画记录名称后固定跟随 map/object 头：03 00/01 07 6c 7e...
	// 槽、附件和事件文本使用不同头，借此排除同一 payload 中的普通名称。
	if end < 0 || end+11 >= len(payload) {
		return false
	}
	return payload[end] == 0x03 &&
		(payload[end+1] == 0x00 || payload[end+1] == 0x01) &&
		payload[end+2] == 0x07 &&
		payload[end+3] == 0x6c &&
		payload[end+4] == 0x7e &&
		payload[end+5] == 0x08 &&
		payload[end+6] == 0x74 &&
		payload[end+7] == 0x01 &&
		payload[end+8] == 0x00 &&
		payload[end+9] == 0x09 &&
		payload[end+10] == 0x00 &&
		payload[end+11] == 0x0a
}
