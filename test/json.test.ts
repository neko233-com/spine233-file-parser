import { detectSpineFile, parseSpineJson, SpineParseError } from "../src";

describe("Spine JSON", () => {
  it("parses skeleton data", () => {
    const json = JSON.stringify({
      skeleton: { spine: "4.2.0" },
      bones: [{ name: "root" }],
      animations: { idle: {} }
    });
    const result = parseSpineJson(json);

    expect(result.data.skeleton?.spine).toBe("4.2.0");
    expect(result.data.bones?.[0]?.name).toBe("root");
    expect(detectSpineFile(new TextEncoder().encode(json))).toBe(
      "skeleton-json"
    );
  });

  it("rejects malformed data", () => {
    expect(() => parseSpineJson("{")).toThrow(SpineParseError);
    expect(() => parseSpineJson('{"bones":[{"name":1}]}')).toThrow(
      /invalid bones/
    );
  });
});
