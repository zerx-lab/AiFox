import { describe, expect, it } from "vitest";
import { diffLines } from "./diff";

describe("diffLines", () => {
  it("emits only context rows for identical input", () => {
    const rows = diffLines(["a", "b"], ["a", "b"]);
    expect(rows).toEqual([
      { kind: "ctx", text: "a" },
      { kind: "ctx", text: "b" },
    ]);
  });

  it("marks insertions and deletions around common lines", () => {
    const rows = diffLines(["a", "b", "c"], ["a", "x", "c"]);
    expect(rows).toEqual([
      { kind: "ctx", text: "a" },
      { kind: "del", text: "b" },
      { kind: "add", text: "x" },
      { kind: "ctx", text: "c" },
    ]);
  });

  it("handles pure insertion and pure deletion", () => {
    expect(diffLines([], ["a"])).toEqual([{ kind: "add", text: "a" }]);
    expect(diffLines(["a"], [])).toEqual([{ kind: "del", text: "a" }]);
  });

  it("preserves all lines: ctx+del covers a, ctx+add covers b", () => {
    const a = ["1", "2", "3", "4"];
    const b = ["2", "3", "5"];
    const rows = diffLines(a, b);
    expect(rows.filter((r) => r.kind !== "add").map((r) => r.text)).toEqual(a);
    expect(rows.filter((r) => r.kind !== "del").map((r) => r.text)).toEqual(b);
  });

  it("degrades to del-all/add-all beyond the line cap", () => {
    const big = Array.from({ length: 4001 }, (_, i) => String(i));
    const rows = diffLines(big, ["x"]);
    expect(rows).toHaveLength(big.length + 1);
    expect(rows.every((r) => r.kind !== "ctx")).toBe(true);
  });
});
