import { SpineParseError } from "./errors";
import type { ParsedSpineJson, SpineJson } from "./types";

export function parseSpineJson(
  input: string | Uint8Array
): ParsedSpineJson {
  let parsed: unknown;
  try {
    const text =
      typeof input === "string" ? input : new TextDecoder().decode(input);
    parsed = JSON.parse(text);
  } catch (error) {
    throw new SpineParseError("INVALID_JSON", "Invalid Spine JSON.", {
      cause: error
    });
  }

  if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
    throw new SpineParseError(
      "INVALID_JSON",
      "Spine JSON root must be an object."
    );
  }

  const data = parsed as SpineJson;
  if (
    data.bones !== undefined &&
    (!Array.isArray(data.bones) ||
      data.bones.some(
        (bone) =>
          bone === null ||
          typeof bone !== "object" ||
          typeof bone.name !== "string"
      ))
  ) {
    throw new SpineParseError(
      "INVALID_JSON",
      "Spine JSON contains an invalid bones array."
    );
  }

  return { kind: "skeleton-json", data };
}
