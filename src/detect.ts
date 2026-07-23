import { inspectSkeletonBinary } from "./skel";
import { inspectSpineProject } from "./project";
import { toBytes } from "./binary";
import type { BinaryInput, SpineFileKind } from "./types";

export function detectSpineFile(input: BinaryInput): SpineFileKind {
  const bytes = toBytes(input);
  const firstNonWhitespace = bytes.find(
    (byte) => byte !== 0x20 && byte !== 0x09 && byte !== 0x0a && byte !== 0x0d
  );
  if (firstNonWhitespace === 0x7b) return "skeleton-json";

  try {
    inspectSpineProject(bytes, {
      maxUncompressedBytes: 16 * 1024 * 1024,
      maxStrings: 1
    });
    return "project";
  } catch {
    // Continue with exported skeleton binary detection.
  }

  try {
    const result = inspectSkeletonBinary(bytes);
    return result.spineVersion === null ? "unknown" : "skeleton-binary";
  } catch {
    return "unknown";
  }
}
