export class SpineParseError extends Error {
  readonly code:
    | "INVALID_INPUT"
    | "INVALID_PROJECT"
    | "INVALID_JSON"
    | "INVALID_SKEL"
    | "LIMIT_EXCEEDED";

  constructor(
    code: SpineParseError["code"],
    message: string,
    options?: ErrorOptions
  ) {
    super(message, options);
    this.name = "SpineParseError";
    this.code = code;
  }
}
