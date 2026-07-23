# spine233-file-parser

Pure Go library for Spine files. Zero third-party dependencies.

- detect `.spine`, `.skel`, and Spine JSON;
- deserialize and serialize private `.spine` raw-DEFLATE envelopes;
- deserialize and serialize `.skel` headers without changing unknown payload;
- deserialize and serialize Spine JSON while preserving unknown fields;
- convert complete `.spine ↔ JSON` data through the official Spine CLI;
- keep JSON, binary, strings, metadata, and CLI logs in temporary directories.

> `.spine` uses a private, version-dependent semantic schema. This library
> preserves its opaque binary payload losslessly. Semantic project conversion
> uses Spine's officially supported CLI import/export path.

## Install

```bash
go get github.com/neko233-com/spine233-file-parser
```

```go
import spineparser "github.com/neko233-com/spine233-file-parser"
```

## `.spine` binary round trip

```go
source, err := os.ReadFile("character.spine")
if err != nil {
	log.Fatal(err)
}

document, err := spineparser.DeserializeProject(
	source,
	spineparser.InspectOptions{},
)
if err != nil {
	log.Fatal(err)
}

fmt.Println(document.Inspection.SpineVersion)
fmt.Println(document.Inspection.Strings)

encoded, err := spineparser.SerializeProject(
	document,
	spineparser.ProjectSerializeOptions{},
)
if err != nil {
	log.Fatal(err)
}

if err := os.WriteFile("character-copy.spine", encoded, 0o644); err != nil {
	log.Fatal(err)
}
```

`ProjectDocument` also implements `encoding.BinaryMarshaler` and
`encoding.BinaryUnmarshaler`:

```go
encoded, err := document.MarshalBinary()

var decoded spineparser.ProjectDocument
err = decoded.UnmarshalBinary(encoded)
```

Compression bytes may differ after re-encoding, but the decompressed private
payload remains byte-for-byte identical.

## `.skel` binary round trip

```go
document, err := spineparser.DeserializeSkeletonBinary(source)
if err != nil {
	log.Fatal(err)
}

document.Header.Width = 1920
encoded, err := spineparser.SerializeSkeletonBinary(document)
```

`SkeletonBinaryDocument` preserves the unparsed skeleton payload and rewrites
only its header. It also implements `encoding.BinaryMarshaler` and
`encoding.BinaryUnmarshaler`.

## Spine JSON round trip

```go
document, err := spineparser.DeserializeJSON(source)
if err != nil {
	log.Fatal(err)
}

document.Bones[0].Name = "renamed-root"

encoded, err := spineparser.SerializeJSON(
	document,
	spineparser.JSONSerializeOptions{Indent: "  "},
)
```

Unknown root, skeleton, bone, and slot fields survive the round trip.

## Complete `.spine` → JSON deserialization

Requires a locally installed and licensed Spine Editor.

```go
result, err := spineparser.DeserializeProjectFile(
	context.Background(),
	"character.spine",
	spineparser.ExportOptions{
		Executable:    "D:/IDE/Spine/Spine.com",
		EditorVersion: "4.3.xx",
	},
)
if err != nil {
	log.Fatal(err)
}

for _, document := range result.Documents {
	fmt.Println(document.FileName)
	fmt.Println(len(document.Data.Bones))
	fmt.Println(document.Data.Animations)
}
```

`ExportProject` is the equivalent shorter name.

## Complete JSON → `.spine` serialization

```go
result, err := spineparser.SerializeProjectFile(
	context.Background(),
	document,
	"character-restored.spine",
	spineparser.ImportOptions{
		Executable:   "D:/IDE/Spine/Spine.com",
		SkeletonName: "character",
		JSON: spineparser.JSONSerializeOptions{
			Indent: "  ",
		},
	},
)
if err != nil {
	log.Fatal(err)
}

fmt.Println(result.ProjectPath)
```

`ImportProject` is the equivalent shorter name. Use JSON exported with
nonessential data when the restored project must retain the maximum available
editor metadata.

## Temporary diagnostics

Pure file inspection:

```go
result, err := spineparser.InspectFile(
	"character.spine",
	spineparser.InspectFileOptions{},
)
fmt.Println(result.OutputDirectory)
```

CLI export layout:

```text
spine233-file-parser-<random>/
├─ character.json
└─ diagnostics/
   ├─ character.inspection.json
   ├─ character.strings.txt
   ├─ character.decoded.bin
   └─ character.spine-cli.log
```

CLI import diagnostics additionally contain `character.import.json`. Output is
intentionally kept after success or failure. Set `OutputDirectory` for a known
location or `OmitDecodedBinary` to skip the decompressed payload.

Set `SPINE_EXECUTABLE` instead of passing `Executable` on every call.

## Resource limits

Project decompression is limited to 256 MiB by default:

```go
document, err := spineparser.DeserializeProject(
	source,
	spineparser.InspectOptions{
		MaxUncompressedBytes: 512 * 1024 * 1024,
		MaxStrings:           20_000,
	},
)
```

## License

MIT. Spine is a trademark of Esoteric Software LLC. Spine Editor and Spine
Runtimes have their own licenses.
