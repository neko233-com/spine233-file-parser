import { inflateRawLimited, toBytes } from "./binary";
import { SpineParseError } from "./errors";
import type {
  BinaryInput,
  InspectProjectOptions,
  SpineProjectInspection
} from "./types";

const DEFAULT_MAX_UNCOMPRESSED_BYTES = 256 * 1024 * 1024;
const DEFAULT_MAX_STRINGS = 10_000;
const VERSION_PATTERN = /^\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?$/;

/**
 * Finds Kryo's ASCII-optimized strings in a decompressed Spine project stream.
 * The project schema itself is private and version-dependent, so these strings
 * are diagnostic metadata rather than a semantic project tree.
 */
export function scanSpineProjectStrings(
  bytes: Uint8Array,
  maxStrings = DEFAULT_MAX_STRINGS
): string[] {
  if (!Number.isSafeInteger(maxStrings) || maxStrings <= 0) {
    throw new SpineParseError(
      "INVALID_INPUT",
      "maxStrings must be a positive safe integer."
    );
  }

  const values: string[] = [];
  const seen = new Set<string>();

  for (let index = 0; index < bytes.length && values.length < maxStrings;) {
    const start = index;
    let cursor = index;
    let text = "";

    while (cursor < bytes.length && cursor - start < 1024) {
      const byte = bytes[cursor]!;
      const character = byte & 0x7f;
      const isLast = (byte & 0x80) !== 0;

      if (character < 0x20 || character > 0x7e) break;
      text += String.fromCharCode(character);
      cursor += 1;

      if (isLast) {
        const value = text.trim();
        const letterCount = [...value].filter((char) =>
          /[0-9A-Za-z]/.test(char)
        ).length;
        const likelyText =
          value.length >= 3 &&
          letterCount >= Math.max(2, Math.ceil(value.length * 0.4));

        if (likelyText && !seen.has(value)) {
          seen.add(value);
          values.push(value);
        }
        index = cursor;
        break;
      }
    }

    if (cursor === start || (bytes[cursor - 1]! & 0x80) === 0) {
      index = start + 1;
    }
  }

  return values;
}

export function inspectSpineProject(
  input: BinaryInput,
  options: InspectProjectOptions = {}
): SpineProjectInspection {
  const source = toBytes(input);
  if (source.length === 0) {
    throw new SpineParseError("INVALID_PROJECT", "Spine project is empty.");
  }

  const inflated = inflateRawLimited(
    source,
    options.maxUncompressedBytes ?? DEFAULT_MAX_UNCOMPRESSED_BYTES
  );
  const strings = scanSpineProjectStrings(
    inflated,
    options.maxStrings ?? DEFAULT_MAX_STRINGS
  );
  const version = strings.find((value) => VERSION_PATTERN.test(value));

  if (inflated.length < 8 || strings.length === 0) {
    throw new SpineParseError(
      "INVALID_PROJECT",
      "Raw-DEFLATE stream does not look like a Spine project."
    );
  }

  return {
    kind: "project",
    compression: "deflate-raw",
    compressedBytes: source.length,
    uncompressedBytes: inflated.length,
    ...(version === undefined ? {} : { spineVersion: version }),
    strings
  };
}

export function decodeSpineProject(
  input: BinaryInput,
  options: Pick<InspectProjectOptions, "maxUncompressedBytes"> = {}
): Uint8Array {
  return inflateRawLimited(
    toBytes(input),
    options.maxUncompressedBytes ?? DEFAULT_MAX_UNCOMPRESSED_BYTES
  );
}
