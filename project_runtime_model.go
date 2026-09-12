package spineparser

import (
	"fmt"
)

type projectRuntimeAdapter func([]byte, string) (*ProjectRuntimeModel, error)

var projectRuntimeAdapters = map[string]projectRuntimeAdapter{
	"4.2.43": discoverProjectRuntimeModelV4243,
	"4.3.6":  discoverProjectRuntimeModelV4306,
	"4.3.8":  discoverProjectRuntimeModelV4308,
	"4.3.11": discoverProjectRuntimeModelV4311,
	"4.3.14": discoverProjectRuntimeModelV4314,
	"4.3.15": discoverProjectRuntimeModelV4315,
	"4.3.17": discoverProjectRuntimeModelV4317,
	"4.3.19": discoverProjectRuntimeModelV4319,
	"4.3.23": discoverProjectRuntimeModelV4323,
	"4.3.26": discoverProjectRuntimeModelV4326,
}

// ProjectRuntimeModel 是各版本 .spine 解析器输出的同构中间结构。
// 导出层只消费该结构，不直接依赖某个版本的私有 Kryo 字段布局。
type ProjectRuntimeModel struct {
	Layout              string
	SourceVersion       string
	Payload             []byte
	Bones               *ProjectBoneDirectory
	Metadata            *ProjectSkeletonMetadata
	Bounds              *ProjectSkeletonBounds
	Slots               *ProjectSlotDirectory
	Regions             *ProjectRegionAttachmentDirectory
	Meshes              *ProjectMeshAttachmentDirectory
	VertexAttachments   *ProjectVertexAttachmentDirectory
	Physics             *ProjectPhysicsConstraintDirectory
	PathConstraints     *ProjectPathConstraintDirectory
	Animations          *ProjectAnimationDirectory
	ConstraintOrder     []ProjectConstraintOrderRecord
	EventDefinitions    []string
	SkinAttachmentOrder *ProjectSkinAttachmentOrderDirectory
}

// DiscoverProjectRuntimeModel 根据源项目小版本选择专用解析器。
// 4.2 与 4.3 即使最终 Runtime 目标相同，也不能共用私有项目字段解析。
func DiscoverProjectRuntimeModel(payload []byte, sourceVersion string) (*ProjectRuntimeModel, error) {
	profile, err := resolveProjectRuntimeVersionProfile(sourceVersion)
	if err != nil {
		return nil, err
	}
	adapter, ok := projectRuntimeAdapters[profile.ExactKey]
	if !ok {
		return nil, fmt.Errorf(
			"unsupported Spine source version %s: no dedicated project adapter",
			sourceVersion,
		)
	}
	return adapter(payload, sourceVersion)
}

func projectRuntimeVersionFamily(version string) (int, int, error) {
	profile, err := resolveProjectRuntimeVersionProfile(version)
	if err != nil {
		return 0, 0, err
	}
	return profile.Major, profile.Minor, nil
}
