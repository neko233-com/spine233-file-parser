import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

import { detectSpineFile, inspectSkeletonBinary } from "../src";

const fixture = fileURLToPath(
  new URL("./fixtures/coin-pro.skel", import.meta.url)
);

describe("Spine skeleton binary", () => {
  it("inspects an official 4.2 export", async () => {
    const bytes = await readFile(fixture);
    const result = inspectSkeletonBinary(bytes);

    expect(result.spineVersion).toBe("4.2.22");
    expect(result.hash).toBe("7caafe7dee2b2849");
    expect(result.referenceScale).toBe(100);
    expect(detectSpineFile(bytes)).toBe("skeleton-binary");
  });
});
