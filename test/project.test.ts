import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

import {
  decodeSpineProject,
  detectSpineFile,
  inspectSpineProject,
  SpineParseError
} from "../src";

const fixture = fileURLToPath(
  new URL("./fixtures/coin-pro.spine", import.meta.url)
);

describe("Spine project", () => {
  it("inspects an official Professional project", async () => {
    const bytes = await readFile(fixture);
    const result = inspectSpineProject(bytes);

    expect(result.kind).toBe("project");
    expect(result.compression).toBe("deflate-raw");
    expect(result.compressedBytes).toBe(2666);
    expect(result.uncompressedBytes).toBe(11399);
    expect(result.spineVersion).toBe("4.0.07");
    expect(result.strings).toContain("./images/");
    expect(result.strings).toContain("coin-front-shine-logo");
    expect(detectSpineFile(bytes)).toBe("project");
    expect(decodeSpineProject(bytes)).toHaveLength(11399);
  });

  it("rejects invalid input", () => {
    expect(() => inspectSpineProject(new Uint8Array([1, 2, 3]))).toThrow(
      SpineParseError
    );
  });

  it("enforces the output limit", async () => {
    const bytes = await readFile(fixture);
    expect(() =>
      inspectSpineProject(bytes, { maxUncompressedBytes: 100 })
    ).toThrow(/exceeds 100 bytes/);
  });
});
