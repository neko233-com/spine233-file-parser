import { readFloat32BE, toBytes } from "./binary";
import { SpineParseError } from "./errors";
import type {
  BinaryInput,
  SpineSkeletonBinaryInspection
} from "./types";

class SkeletonHeaderReader {
  offset = 0;

  constructor(readonly bytes: Uint8Array) {}

  byte(): number {
    const value = this.bytes[this.offset];
    if (value === undefined) {
      throw new SpineParseError(
        "INVALID_SKEL",
        "Unexpected end of skeleton binary header."
      );
    }
    this.offset += 1;
    return value;
  }

  varint(): number {
    let value = 0;
    for (let shift = 0; shift < 35; shift += 7) {
      const byte = this.byte();
      value |= (byte & 0x7f) << shift;
      if ((byte & 0x80) === 0) return value >>> 0;
    }
    throw new SpineParseError("INVALID_SKEL", "Invalid skeleton varint.");
  }

  string(): string | null {
    const encodedLength = this.varint();
    if (encodedLength === 0) return null;
    if (encodedLength === 1) return "";
    const length = encodedLength - 1;
    const end = this.offset + length;
    if (end > this.bytes.length) {
      throw new SpineParseError(
        "INVALID_SKEL",
        "Unexpected end of skeleton string."
      );
    }
    const value = new TextDecoder("utf-8", { fatal: true }).decode(
      this.bytes.subarray(this.offset, end)
    );
    this.offset = end;
    return value;
  }

  float(): number {
    const value = readFloat32BE(this.bytes, this.offset);
    this.offset += 4;
    return value;
  }
}

function isSpineVersion(value: string | null): boolean {
  return value !== null && /^\d+\.\d+(?:\.\d+)?/.test(value);
}

function inspectModernHeader(
  bytes: Uint8Array
): SpineSkeletonBinaryInspection {
  if (bytes.length < 8) {
    throw new SpineParseError("INVALID_SKEL", "Skeleton binary is too short.");
  }

  const reader = new SkeletonHeaderReader(bytes);
  const hashBytes = bytes.subarray(0, 8);
  reader.offset = 8;
  const spineVersion = reader.string();
  if (!isSpineVersion(spineVersion)) {
    throw new SpineParseError(
      "INVALID_SKEL",
      "Skeleton binary has an invalid Spine version."
    );
  }

  const x = reader.float();
  const y = reader.float();
  const width = reader.float();
  const height = reader.float();
  const referenceScale = reader.float();
  const nonessential = reader.byte() !== 0;
  const hash =
    hashBytes.every((value) => value === 0)
      ? null
      : [...hashBytes]
          .map((value) => value.toString(16).padStart(2, "0"))
          .join("");

  return {
    kind: "skeleton-binary",
    hash,
    spineVersion,
    x,
    y,
    width,
    height,
    referenceScale,
    nonessential
  };
}

function inspectLegacyHeader(
  bytes: Uint8Array
): SpineSkeletonBinaryInspection {
  const reader = new SkeletonHeaderReader(bytes);
  const hash = reader.string();
  const spineVersion = reader.string();
  if (!isSpineVersion(spineVersion)) {
    throw new SpineParseError(
      "INVALID_SKEL",
      "Skeleton binary has an invalid Spine version."
    );
  }
  const x = reader.float();
  const y = reader.float();
  const width = reader.float();
  const height = reader.float();
  const nonessential = reader.byte() !== 0;

  return {
    kind: "skeleton-binary",
    hash,
    spineVersion,
    x,
    y,
    width,
    height,
    nonessential
  };
}

export function inspectSkeletonBinary(
  input: BinaryInput
): SpineSkeletonBinaryInspection {
  const bytes = toBytes(input);
  try {
    return inspectModernHeader(bytes);
  } catch (modernError) {
    try {
      return inspectLegacyHeader(bytes);
    } catch (legacyError) {
      throw new SpineParseError(
        "INVALID_SKEL",
        "Invalid Spine skeleton binary.",
        { cause: new AggregateError([modernError, legacyError]) }
      );
    }
  }
}
