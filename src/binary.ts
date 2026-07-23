import { Inflate } from "fflate";

import { SpineParseError } from "./errors";
import type { BinaryInput } from "./types";

export function toBytes(input: BinaryInput): Uint8Array {
  if (input instanceof ArrayBuffer) return new Uint8Array(input);

  if (ArrayBuffer.isView(input)) {
    return new Uint8Array(input.buffer, input.byteOffset, input.byteLength);
  }

  throw new SpineParseError(
    "INVALID_INPUT",
    "Expected an ArrayBuffer or ArrayBufferView."
  );
}

export function inflateRawLimited(
  source: Uint8Array,
  maxOutputBytes: number
): Uint8Array {
  if (!Number.isSafeInteger(maxOutputBytes) || maxOutputBytes <= 0) {
    throw new SpineParseError(
      "INVALID_INPUT",
      "maxUncompressedBytes must be a positive safe integer."
    );
  }

  const chunks: Uint8Array[] = [];
  let total = 0;
  let inflateError: unknown;
  const inflater = new Inflate((chunk) => {
    total += chunk.length;
    if (total > maxOutputBytes) {
      inflateError = new SpineParseError(
        "LIMIT_EXCEEDED",
        `Inflated project exceeds ${maxOutputBytes} bytes.`
      );
      throw inflateError;
    }
    chunks.push(chunk);
  });

  try {
    inflater.push(source, true);
  } catch (error) {
    if (inflateError) throw inflateError;
    throw new SpineParseError(
      "INVALID_PROJECT",
      "Input is not a valid raw-DEFLATE Spine project.",
      { cause: error }
    );
  }

  if (inflateError) throw inflateError;

  const output = new Uint8Array(total);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.length;
  }
  return output;
}

export function readFloat32BE(
  bytes: Uint8Array,
  offset: number
): number {
  if (offset + 4 > bytes.length) {
    throw new SpineParseError(
      "INVALID_SKEL",
      "Unexpected end of skeleton binary header."
    );
  }
  return new DataView(
    bytes.buffer,
    bytes.byteOffset + offset,
    4
  ).getFloat32(0, false);
}
