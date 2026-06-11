import { describe, expect, it } from "vitest";
import { parseEvent } from "./sse";

describe("parseEvent", () => {
  it("parses event name and data", () => {
    expect(parseEvent('event: entry\ndata: {"id":"x"}')).toEqual({
      event: "entry",
      data: '{"id":"x"}',
    });
  });

  it("defaults the event name to message", () => {
    expect(parseEvent("data: hi")).toEqual({ event: "message", data: "hi" });
  });

  it("joins multi-line data with newlines per the SSE spec", () => {
    expect(parseEvent("data: a\ndata: b")).toEqual({ event: "message", data: "a\nb" });
  });

  it("ignores comment lines and blank lines", () => {
    expect(parseEvent(": keepalive\n\ndata: x")).toEqual({ event: "message", data: "x" });
  });

  it("strips only a single leading space after the colon", () => {
    expect(parseEvent("data:  two")).toEqual({ event: "message", data: " two" });
    expect(parseEvent("data:none")).toEqual({ event: "message", data: "none" });
  });

  it("returns null when there is no data field", () => {
    expect(parseEvent("event: ping")).toBeNull();
    expect(parseEvent(": comment only")).toBeNull();
  });
});
