# spine-file-parser

TypeScript library for Spine files:

- inspect compressed Spine Editor `.spine` project files in browsers or Node.js;
- read embedded editor version and diagnostic strings;
- inspect official exported `.skel` headers;
- parse exported Spine JSON with TypeScript types;
- export complete Professional project data through the official Spine CLI.

> `.spine` is Spine Editor's private, version-dependent project format. Its
> semantic schema is not public. This package does not pretend to reverse
> engineer that schema: pure TypeScript APIs safely inspect its raw-DEFLATE
> envelope, while `spine-file-parser/node` uses Spine's supported CLI export
> path for complete bones, slots, constraints, skins, meshes, events, and
> animations.

## Install

```bash
npm install spine-file-parser
```

## Inspect a `.spine` project

```ts
import { inspectSpineProject } from "spine-file-parser";

const bytes = new Uint8Array(await file.arrayBuffer());
const project = inspectSpineProject(bytes);

console.log(project.spineVersion);
console.log(project.compressedBytes, project.uncompressedBytes);
console.log(project.strings);
```

## Export and parse a Spine Professional project

Requires a locally installed and licensed Spine Editor. The library never
bundles Spine, Spine Runtimes, or a license.

```ts
import { exportSpineProject } from "spine-file-parser/node";

const result = await exportSpineProject("./character.spine", {
  // Optional. Defaults to Spine.com on Windows, Spine elsewhere.
  executable: "D:/IDE/Spine/Spine.com",
  // Optional: pin the editor line used by your runtime.
  editorVersion: "4.3.xx"
});

for (const document of result.documents) {
  console.log(document.fileName);
  console.log(document.parsed.data.bones);
  console.log(document.parsed.data.animations);
}
```

Use `SPINE_EXECUTABLE` instead of passing `executable` in every call:

```bash
SPINE_EXECUTABLE=/Applications/Spine.app/Contents/MacOS/Spine node app.js
```

For nonessential editor data, save a JSON export settings file in Spine and
pass it as `exportSettings`.

## Other formats

```ts
import {
  detectSpineFile,
  inspectSkeletonBinary,
  parseSpineJson
} from "spine-file-parser";

detectSpineFile(bytes);
inspectSkeletonBinary(skelBytes);
parseSpineJson(jsonText);
```

## Safety

`inspectSpineProject` limits decompressed output to 256 MiB by default. Change
it only for trusted, known-large projects:

```ts
inspectSpineProject(bytes, { maxUncompressedBytes: 512 * 1024 * 1024 });
```

## Compatibility

- ESM
- Node.js 18+
- modern browsers for pure inspection APIs
- Spine CLI availability and license required only by `spine-file-parser/node`

## License

MIT. Spine is a trademark of Esoteric Software LLC. Spine Editor and Spine
Runtimes have their own licenses.
