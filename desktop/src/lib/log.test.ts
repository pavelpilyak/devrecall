import { describe, it, expect } from "vitest";
import { parseCSV, buildLogRequest } from "./log";

describe("parseCSV", () => {
  it("returns empty array for empty input", () => {
    expect(parseCSV("")).toEqual([]);
  });

  it("splits a single value", () => {
    expect(parseCSV("decision")).toEqual(["decision"]);
  });

  it("splits multiple values", () => {
    expect(parseCSV("a,b,c")).toEqual(["a", "b", "c"]);
  });

  it("trims whitespace around values", () => {
    expect(parseCSV(" a , b , c ")).toEqual(["a", "b", "c"]);
  });

  it("drops empty entries", () => {
    expect(parseCSV(",,a,,b,")).toEqual(["a", "b"]);
  });

  it("dedupes repeated values", () => {
    expect(parseCSV("a,b,a,c,b")).toEqual(["a", "b", "c"]);
  });
});

describe("buildLogRequest", () => {
  it("trims text and returns minimal payload", () => {
    expect(buildLogRequest({ text: "  hello  " })).toEqual({ text: "hello" });
  });

  it("throws on empty text", () => {
    expect(() => buildLogRequest({ text: "" })).toThrow("Text is required");
  });

  it("throws on whitespace-only text", () => {
    expect(() => buildLogRequest({ text: "   \n  " })).toThrow("Text is required");
  });

  it("includes optional fields when set", () => {
    expect(
      buildLogRequest({
        text: "Decision call",
        at: "2026-04-01 09:30",
        tags: "decision, deploy",
        people: "anna@example.com, bob",
      })
    ).toEqual({
      text: "Decision call",
      at: "2026-04-01 09:30",
      tags: ["decision", "deploy"],
      people: ["anna@example.com", "bob"],
    });
  });

  it("omits empty optional fields rather than sending []", () => {
    const req = buildLogRequest({
      text: "ping",
      at: "  ",
      tags: "  ",
      people: "",
    });
    expect(req).toEqual({ text: "ping" });
    expect("tags" in req).toBe(false);
    expect("people" in req).toBe(false);
    expect("at" in req).toBe(false);
  });
});
