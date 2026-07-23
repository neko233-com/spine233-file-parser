# spine233-file-parser

Pure Go Spine 文件库。零第三方依赖，零 Spine Editor 进程依赖。

- 检测 `.spine`、`.skel`、Spine JSON。
- 无损解包/封包 `.spine` raw-DEFLATE payload。
- fail-closed 修改指定动画记录内大端 float32 关键帧。
- 自动解析现代 `.spine` 动画表、数量、名称和记录边界。
- 自动解析现代 `.spine` 骨骼名、对象偏移和原始父对象 token。
- 语义解析及修改现代 `.spine` rotate/translate/scale/shear 时间线。
- 语义解析及重定时现代 `.spine` slot attachment 时间线。
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

## License

MIT。Spine 是 Esoteric Software LLC 商标。
