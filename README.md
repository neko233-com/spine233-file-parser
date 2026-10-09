# spine233-file-parser

Pure Go Spine 文件库。零第三方依赖，零 Spine Editor 进程依赖。

- 检测 `.spine`、`.skel`、Spine JSON。
- 无损解包/封包 `.spine` raw-DEFLATE payload。
- fail-closed 修改指定动画记录内大端 float32 关键帧。
- 自动解析现代 `.spine` 动画表、数量、名称和记录边界。
- 自动解析现代 `.spine` 骨骼名、对象偏移、原始父对象 token 和 Kryo wire reference。
- 语义解析及修改现代 `.spine` rotate/translate/scale/shear 时间线。
- 语义解析及重定时现代 `.spine` slot attachment 时间线。
- 解析并重定时现代 `.spine` event 时间线，保留事件值原始字节。
- 解析/序列化 `.skel` header，保留未知 payload。
- 解析/序列化 Spine JSON，保留未知字段。
- Spine JSON 深度分析、引用验证、动画生成。

## 安装

```bash
go get github.com/neko233-com/spine233-file-parser
```

```go
import spineparser "github.com/neko233-com/spine233-file-parser"
```

## `.spine` round trip

```go
source, err := os.ReadFile("character.spine")
document, err := spineparser.DeserializeProject(
	source,
	spineparser.InspectOptions{},
)
encoded, err := spineparser.SerializeProject(
	document,
	spineparser.ProjectSerializeOptions{},
)
err = os.WriteFile("character-copy.spine", encoded, 0o644)
```

重压缩后压缩字节可能不同；解压 payload 保持逐字节一致。

## 直接动画修改

```go
patched, report, err := spineparser.PatchProjectAnimationFloat32(
	document,
	spineparser.ProjectAnimationFloatPatch{
		Animation:       "attack",
		TargetAnimation: "attack-agent",
		EndBefore:       "crouch",
		Edits: []spineparser.ProjectFloat32Edit{
			{From: 13.22, To: 24, ExpectedMatches: 1},
		},
	},
)
```

关键帧编辑不修改对象引用；重命名只替换 Kryo ASCII 字符串。
动画边界或匹配数量不符时失败。

动画目录：

```go
directory, err := spineparser.DiscoverProjectAnimations(document.Payload)
for _, animation := range directory.Records {
	fmt.Println(animation.Name, animation.Offset, animation.EndOffset)
}
```

动作时长：

```go
duration, err := spineparser.DiscoverProjectAnimationDurationInRange(
	document.Payload,
	animation.Name,
	animation.Offset,
	animation.EndOffset,
)
fmt.Println(duration.LastFrame, duration.Duration)
```

动作时长只读取 timeline 关键帧的最大帧号，不要求解析 timeline owner
或具体数值，因此可覆盖语义导出器尚未支持的 timeline 类型。`Duration`
按项目固定帧率换算为秒；范围内 timeline 或关键帧结构不完整时失败。

骨骼引用：

```go
bones, err := spineparser.DiscoverProjectBones(document.Payload)
reference, ok := bones.WireReferenceByName("front-arm")
name, ok := bones.BoneNameByWireReference(reference)
fmt.Println(bones.ReferencesComplete, reference, name)
```

`ReferencesComplete=false` 表示复杂对象图未能全部证明；调用方必须 fail closed，
不能按骨骼数组索引猜测动画引用。

Rotate 时间线：

```go
timelines, err := spineparser.DiscoverProjectRotateTimelines(
	document.Payload,
	"attack",
)
patched, report, err := spineparser.PatchProjectRotateValues(
	document,
	spineparser.ProjectRotatePatch{
		Animation:       "attack",
		TargetAnimation: "attack-agent",
		Edits: []spineparser.ProjectRotateValueEdit{
			{BoneReference: 6, KeyIndex: 1, From: 13.22, To: 24},
		},
	},
)
```

语义修改按骨骼引用、关键帧索引和旧值三重校验；任何结构漂移都会失败。
`channel: "frame"` 可重定时，且必须保持关键帧严格递增。
`channel: "curve.x.0"` 等可修改某通道的 4 个原始曲线控制值。
输入 `document` 永不被修改。

通用骨骼变换：

```go
timelines, err := spineparser.DiscoverProjectTransformTimelines(
	document.Payload,
	"attack",
)
patched, report, err := spineparser.PatchProjectTransformValues(
	document,
	spineparser.ProjectTransformPatch{
		Animation: "attack",
		Edits: []spineparser.ProjectTransformValueEdit{
			{
				BoneReference: 6,
				BoneName:      "body",
				Timeline:      "translate",
				KeyIndex:      1,
				Channel:       "x",
				From:          4.86,
				To:            8,
			},
		},
	},
)
```

固定拓扑整条重写：

```go
patched, report, err := spineparser.RewriteProjectTransformTimelines(
	document,
	spineparser.ProjectTransformRewrite{
		Animation:       "attack",
		TargetAnimation: "attack-agent",
		Timelines: []spineparser.ProjectTransformTimelineRewrite{
			{
				BoneReference: 6,
				BoneName:      "body",
				Timeline:      "translate",
				Keys: []spineparser.ProjectTransformKeySpec{
					{Frame: 0, Values: []float32{-0.77, -1.89}},
					{Frame: 5, Values: []float32{8, -0.24}},
				},
			},
		},
	},
)
```

非空 `BoneName` 与 `BoneReference` 会被双重校验；无法证明映射或名称不一致时
立即失败。留空仅用于兼容底层 ref-only 调用。

Slot attachment 关键帧：

```go
timelines, err := spineparser.DiscoverProjectSlotAttachmentTimelines(
	document.Payload,
	"blink",
)
patched, report, err := spineparser.PatchProjectSlotAttachmentFrames(
	document,
	spineparser.ProjectSlotAttachmentPatch{
		Animation:       "blink",
		TargetAnimation: "blink-agent",
		Edits: []spineparser.ProjectSlotAttachmentFrameEdit{
			{
				SlotReference:     14,
				TimelineReference: 300,
				TimelineOffset:    timelines.Timelines[0].Offset,
				KeyIndex:          1,
				From:              16,
				To:                18,
			},
		},
	},
)
```

Attachment 对象名称引用尚未猜测；当前只安全修改已有 key 的帧，保持对象数量
和引用不变。

Event 关键帧：

```go
timelines, err := spineparser.DiscoverProjectEventTimelines(
	document.Payload,
	"run",
)
timeline := timelines.Timelines[0]
patched, report, err := spineparser.PatchProjectEventFrames(
	document,
	spineparser.ProjectEventPatch{
		Animation:       "run",
		TargetAnimation: "run-agent",
		Edits: []spineparser.ProjectEventFrameEdit{{
			TimelineReference: timeline.TimelineReference,
			KeyReference:      timeline.KeyReference,
			TimelineOffset:    timeline.Offset,
			KeyIndex:          0,
			From:              8,
			To:                9,
		}},
	},
)
```

当前只修改已有 event key 的帧；事件定义、名称、参数和未知字段逐字节保留。

末尾动画删除：

```go
patched, report, err := spineparser.DeleteLastProjectAnimation(
	document,
	"walk",
)
```

仅支持已验证的现代 `kryo-animation-map-v1`，且只能删除动画表最后一项；
至少保留一个动画。
函数同步更新 map count，并逐字节验证所有保留动画记录；输入 `document` 不变。
非末项动画、关键帧或时间线的增删仍需完整对象图重编号，当前明确拒绝。

## `.skel`

```go
document, err := spineparser.DeserializeSkeletonBinary(source)
document.Header.Width = 1920
encoded, err := spineparser.SerializeSkeletonBinary(document)
```

## Spine JSON

```go
document, err := spineparser.DeserializeJSON(source)
document.Bones[0].Name = "renamed-root"
encoded, err := spineparser.SerializeJSON(
	document,
	spineparser.JSONSerializeOptions{Indent: "  "},
)
```

## 诊断

```go
result, err := spineparser.InspectFile(
	"character.spine",
	spineparser.InspectFileOptions{},
)
fmt.Println(result.OutputDirectory)
```

默认解压限制 256 MiB；可用 `InspectOptions.MaxUncompressedBytes` 调整。

## Spine 4.3 校验与可编辑工程归档

`ValidateSpine43JSON` 验证版本、容器类型、父骨骼顺序、4.3 `constraints` 引用、
slot/skin/附件/事件引用、已知时间线结构、关键帧顺序、颜色、每通道 Bezier
控制值以及 mesh/加权顶点索引。同一时刻允许多个事件。它保留未知字段，
不是完整运行时加载器。`AnalyzeJSON` 同时统计 4.3 的统一约束数组。

`VisitAnimation43Timelines` 是公共的确定性 visitor，覆盖 bones、slots、IK、
transform、path、physics、slider、deform/sequence、events、drawOrder 和
drawOrderFolder 的已知 key 数组，返回路径、所有者、种类和通道数。
未知扩展对象中的 time/curve 不作为时间线处理，便于调用方安全重定时和比较。

插值时间线要求严格递增的 key time。附件、draw order、inherit 等离散时间线
允许同一时刻有多个 key，顺序保留；时间倒退仍报错。此规则已用真实 3.8 → 4.3
角色迁移和重复附件 key 验证，不会静默修改输入动画或删除事件。

```go
err := spineparser.VisitAnimation43Timelines(animation, func(t spineparser.Animation43Timeline) error {
    fmt.Println(t.Path, t.Category, t.Owner, t.Kind, len(t.Keys), t.Channels)
    return nil
})
```

```go
report, err := spineparser.ValidateSpine43JSON(runtimeJSON)
archive, err := spineparser.ArchiveProject(document)
// archive 可序列化为 JSON；Payload 是 base64，PayloadSHA256 检查原始数据。
// 修改 archive.Animations 中已有 key 的 Frame、Values，然后恢复工程。
restored, changes, err := spineparser.RestoreProjectArchive(archive)
encoded, err := spineparser.SerializeProject(restored, spineparser.ProjectSerializeOptions{})
```

未修改归档时，恢复的解压 payload 逐字节一致。修改操作保留未知工程对象，
固定时间线和关键帧数量；拒绝校验和不符、引用不符、重复选择和非法帧顺序。
非空 SourceVersion 必须与 payload 中的版本一致，不能只靠修改元数据冒充版本。
归档 JSON 使用 `spine233-project/v1`，与 Spine runtime JSON 不同。
无法解析的时间线保留在原始 payload 中，并通过 `Warnings` 显式报告。
新增动画/骨骼/附件等改变对象图的操作，应通过官方 Spine JSON 导入完成。
公共动画发现入口支持 4.3.26 布局。

## License

MIT。Spine 是 Esoteric Software LLC 商标。
