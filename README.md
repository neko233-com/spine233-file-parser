# spine233-file-parser

Pure Go Spine 文件库。零第三方依赖，零 Spine Editor 进程依赖。

- 检测 `.spine`、`.skel`、Spine JSON。
- 无损解包/封包 `.spine` raw-DEFLATE payload。
- fail-closed 修改指定动画记录内大端 float32 关键帧。
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
		Animation: "attack",
		EndBefore: "crouch",
		Edits: []spineparser.ProjectFloat32Edit{
			{From: 13.22, To: 24, ExpectedMatches: 1},
		},
	},
)
```

操作长度不变、不修改对象引用。动画边界或匹配数量不符时失败。
输入 `document` 永不被修改。

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
