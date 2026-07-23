export type BinaryInput = ArrayBuffer | ArrayBufferView;

export type SpineFileKind =
  | "project"
  | "skeleton-json"
  | "skeleton-binary"
  | "unknown";

export interface InspectProjectOptions {
  /**
   * Reject an inflated project larger than this value.
   * @default 268435456 (256 MiB)
   */
  maxUncompressedBytes?: number;
  /**
   * Maximum number of unique embedded strings to return.
   * @default 10000
   */
  maxStrings?: number;
}

export interface SpineProjectInspection {
  kind: "project";
  compression: "deflate-raw";
  compressedBytes: number;
  uncompressedBytes: number;
  spineVersion?: string;
  strings: string[];
}

export interface SpineSkeletonBinaryInspection {
  kind: "skeleton-binary";
  hash: string | null;
  spineVersion: string | null;
  x: number;
  y: number;
  width: number;
  height: number;
  referenceScale?: number;
  nonessential: boolean;
}

export interface SpineJson {
  skeleton?: {
    hash?: string;
    spine?: string;
    x?: number;
    y?: number;
    width?: number;
    height?: number;
    fps?: number;
    images?: string;
    audio?: string;
    [key: string]: unknown;
  };
  bones?: Array<{
    name: string;
    parent?: string;
    [key: string]: unknown;
  }>;
  slots?: Array<{
    name: string;
    bone: string;
    [key: string]: unknown;
  }>;
  skins?: Array<Record<string, unknown>> | Record<string, unknown>;
  events?: Record<string, unknown>;
  animations?: Record<string, unknown>;
  [key: string]: unknown;
}

export interface ParsedSpineJson {
  kind: "skeleton-json";
  data: SpineJson;
}
