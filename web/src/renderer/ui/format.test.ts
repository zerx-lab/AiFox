import { describe, expect, it } from "vitest";
import { fmtBytes, fmtDuration, isPending, statusKind } from "./format";

describe("fmtBytes", () => {
  it("formats each magnitude", () => {
    expect(fmtBytes(0)).toBe("0 B");
    expect(fmtBytes(512)).toBe("512 B");
    expect(fmtBytes(2048)).toBe("2.0 KB");
    expect(fmtBytes(3 * 1024 * 1024)).toBe("3.00 MB");
  });

  it("rejects negative and non-finite input", () => {
    expect(fmtBytes(-1)).toBe("—");
    expect(fmtBytes(Number.NaN)).toBe("—");
  });
});

describe("fmtDuration", () => {
  it("switches units at one second", () => {
    expect(fmtDuration(999)).toBe("999 ms");
    expect(fmtDuration(1500)).toBe("1.50 s");
  });

  it("treats zero and negatives as not-yet-measured", () => {
    expect(fmtDuration(0)).toBe("—");
    expect(fmtDuration(-5)).toBe("—");
  });
});

describe("isPending", () => {
  it("treats Go's zero time as still pending", () => {
    // Go's zero time.Time marshals to year 1 — a negative epoch, not an end.
    expect(isPending({ endedAt: "0001-01-01T00:00:00Z" })).toBe(true);
  });

  it("a real end timestamp or status code means not pending", () => {
    expect(isPending({ endedAt: "2026-06-11T10:00:00Z" })).toBe(false);
    expect(isPending({ statusCode: 200 })).toBe(false);
  });
});

describe("statusKind", () => {
  it("error beats everything", () => {
    expect(statusKind({ error: "x", statusCode: 200 })).toBe("err");
  });

  it("pending before upstream headers arrive", () => {
    expect(statusKind({})).toBe("pending");
  });

  it("streaming when flagged", () => {
    expect(statusKind({ streaming: true, statusCode: 200 })).toBe("streaming");
  });

  it("4xx/5xx and missing status code are errors once ended", () => {
    expect(statusKind({ endedAt: "2026-06-11T10:00:00Z", statusCode: 404 })).toBe("err");
    expect(statusKind({ endedAt: "2026-06-11T10:00:00Z", statusCode: 0 })).toBe("err");
    expect(statusKind({ endedAt: "2026-06-11T10:00:00Z", statusCode: 200 })).toBe("ok");
  });
});
