import { spawn } from "node:child_process";
import { readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, extname, join, resolve } from "node:path";
import { mkdtemp } from "node:fs/promises";

import { inspectSpineProject, parseSpineJson } from "./index";
import type {
  ParsedSpineJson,
  SpineProjectInspection
} from "./index";

export * from "./index";

export interface ExportSpineProjectOptions {
  /**
   * Spine CLI executable. Defaults to SPINE_EXECUTABLE, then Spine.com on
   * Windows or Spine elsewhere.
   */
  executable?: string;
  /**
   * Existing Spine export settings JSON. Defaults to Spine's `json` preset.
   */
  exportSettings?: string;
  /**
   * Existing output directory. A temporary directory is used by default.
   */
  outputDirectory?: string;
  /**
   * Editor version passed to `--update`, for example `4.2.xx`.
   */
  editorVersion?: string;
  /**
   * Process timeout.
   * @default 120000
   */
  timeoutMs?: number;
}

export interface ExportedSpineDocument {
  fileName: string;
  path: string;
  parsed: ParsedSpineJson;
}

export interface ExportSpineProjectResult {
  inspection: SpineProjectInspection;
  documents: ExportedSpineDocument[];
  outputDirectory: string;
  stdout: string;
}

interface ProcessResult {
  stdout: string;
  stderr: string;
}

function run(
  executable: string,
  args: string[],
  timeoutMs: number
): Promise<ProcessResult> {
  return new Promise((resolvePromise, reject) => {
    const child = spawn(executable, args, {
      windowsHide: true,
      stdio: ["ignore", "pipe", "pipe"]
    });
    let stdout = "";
    let stderr = "";
    let settled = false;

    const timer = setTimeout(() => {
      child.kill();
      if (!settled) {
        settled = true;
        reject(new Error(`Spine CLI timed out after ${timeoutMs} ms.`));
      }
    }, timeoutMs);

    child.stdout.setEncoding("utf8");
    child.stderr.setEncoding("utf8");
    child.stdout.on("data", (chunk: string) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk: string) => {
      stderr += chunk;
    });
    child.on("error", (error) => {
      clearTimeout(timer);
      if (!settled) {
        settled = true;
        reject(error);
      }
    });
    child.on("close", (code) => {
      clearTimeout(timer);
      if (settled) return;
      settled = true;
      if (code === 0) {
        resolvePromise({ stdout, stderr });
      } else {
        const output = [stdout, stderr].filter(Boolean).join("\n").trim();
        reject(
          new Error(
            `Spine CLI exited with code ${code ?? "unknown"}` +
              (output ? `:\n${output}` : ".")
          )
        );
      }
    });
  });
}

export async function exportSpineProject(
  projectPath: string,
  options: ExportSpineProjectOptions = {}
): Promise<ExportSpineProjectResult> {
  const absoluteProjectPath = resolve(projectPath);
  const inspection = inspectSpineProject(await readFile(absoluteProjectPath));
  const ownsOutputDirectory = options.outputDirectory === undefined;
  const outputDirectory = ownsOutputDirectory
    ? await mkdtemp(join(tmpdir(), "spine-file-parser-"))
    : resolve(options.outputDirectory!);
  const executable =
    options.executable ??
    process.env.SPINE_EXECUTABLE ??
    (process.platform === "win32" ? "Spine.com" : "Spine");
  const timeoutMs = options.timeoutMs ?? 120_000;

  if (!Number.isSafeInteger(timeoutMs) || timeoutMs <= 0) {
    throw new TypeError("timeoutMs must be a positive safe integer.");
  }

  const args = ["--hide-license"];
  if (options.editorVersion) {
    args.push("--update", options.editorVersion);
  }
  args.push(
    "--input",
    absoluteProjectPath,
    "--output",
    outputDirectory,
    "--export",
    options.exportSettings ? resolve(options.exportSettings) : "json"
  );

  try {
    const result = await run(executable, args, timeoutMs);
    const entries = await readdir(outputDirectory, { withFileTypes: true });
    const jsonFiles = entries
      .filter(
        (entry) =>
          entry.isFile() && extname(entry.name).toLowerCase() === ".json"
      )
      .map((entry) => entry.name)
      .sort();

    if (jsonFiles.length === 0) {
      throw new Error(
        `Spine CLI produced no JSON files for ${basename(projectPath)}.`
      );
    }

    const documents = await Promise.all(
      jsonFiles.map(async (fileName) => {
        const path = join(outputDirectory, fileName);
        return {
          fileName,
          path,
          parsed: parseSpineJson(await readFile(path))
        };
      })
    );

    return {
      inspection,
      documents,
      outputDirectory,
      stdout: result.stdout
    };
  } catch (error) {
    if (ownsOutputDirectory) {
      await rm(outputDirectory, { recursive: true, force: true });
    }
    throw error;
  }
}
