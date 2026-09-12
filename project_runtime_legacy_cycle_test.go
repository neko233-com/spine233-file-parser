package spineparser

import (
	"testing"
	"time"
)

// runBounded 在超时保护下执行被测函数；解析器父链遍历一旦死循环，
// 测试必须失败而不是挂死整个 go test 进程。
func runBounded(t *testing.T, run func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		run()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("parent chain walk did not terminate within 10s (cyclic ParentToken hang)")
	}
}

// TestAssignLegacyV42MeshBoneReferencesCyclicParents 复现此前让
// exporter.test.exe 内存膨胀到 42.6GB 的死循环：环状 ParentToken
// 上溯时必须在有限步内停止，并回退到安全分支，不产出候选引用。
func TestAssignLegacyV42MeshBoneReferencesCyclicParents(t *testing.T) {
	bones := []ProjectBoneRecord{
		{Name: "a", WireReference: 1, ParentToken: 2},
		{Name: "b", WireReference: 2, ParentToken: 1},
	}
	meshes := &ProjectMeshAttachmentDirectory{
		Records: []ProjectMeshAttachmentRecord{
			{Name: "m", Weighted: true, CandidateBones: 2, legacyOwnerBoneName: "a"},
		},
	}
	runBounded(t, func() {
		assignLegacyV42MeshBoneReferences(meshes, bones)
	})
	if len(meshes.Records[0].BoneReferences) != 0 {
		t.Fatalf("cyclic parents must not produce candidate references: %v", meshes.Records[0].BoneReferences)
	}
}

// TestAssignLegacyV42MeshBoneReferencesSelfLoop 覆盖自环骨骼：
// ParentToken 指向自身同样必须有限步停止。
func TestAssignLegacyV42MeshBoneReferencesSelfLoop(t *testing.T) {
	bones := []ProjectBoneRecord{
		{Name: "a", WireReference: 1, ParentToken: 1},
		{Name: "b", WireReference: 2, ParentToken: 1},
	}
	meshes := &ProjectMeshAttachmentDirectory{
		Records: []ProjectMeshAttachmentRecord{
			{Name: "m", Weighted: true, CandidateBones: 2, legacyOwnerBoneName: "a"},
		},
	}
	runBounded(t, func() {
		assignLegacyV42MeshBoneReferences(meshes, bones)
	})
	if len(meshes.Records[0].BoneReferences) != 0 {
		t.Fatalf("self-loop parent must not produce candidate references: %v", meshes.Records[0].BoneReferences)
	}
}

// TestLegacyV43MeshCandidateBoneIndicesCyclicParents 覆盖 v43 变体：
// 环状父链时 chain 不能无限增长，函数必须在有限步内返回，
// 且返回长度不能超过骨骼总数。
func TestLegacyV43MeshCandidateBoneIndicesCyclicParents(t *testing.T) {
	bones := []ProjectBoneRecord{
		{Name: "a", WireReference: 1, ParentToken: 2},
		{Name: "b", WireReference: 2, ParentToken: 1},
	}
	var indices []int
	runBounded(t, func() {
		indices = legacyV43MeshCandidateBoneIndices("m", "a", 1, bones)
	})
	if len(indices) > len(bones) {
		t.Fatalf("cyclic parents produced overlong chain: %v", indices)
	}
}

// TestLegacyV43MeshCandidateBoneIndicesSelfLoop 覆盖 v43 自环骨骼，
// 且请求数量超过骨骼总数时不得返回超出范围的下标。
func TestLegacyV43MeshCandidateBoneIndicesSelfLoop(t *testing.T) {
	bones := []ProjectBoneRecord{
		{Name: "a", WireReference: 1, ParentToken: 1},
	}
	var indices []int
	runBounded(t, func() {
		indices = legacyV43MeshCandidateBoneIndices("m", "a", 5, bones)
	})
	if len(indices) != 0 {
		t.Fatalf("self-loop parent must return nil: %v", indices)
	}
}
