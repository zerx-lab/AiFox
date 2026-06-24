import { describe, expect, it } from "vitest";
import { aggregateUsage, applyFilter, distinctModels, groupKey } from "./grouping";
import type { EntryMeta, TrafficFilter } from "./state";

function meta(over: Partial<EntryMeta>): EntryMeta {
  return {
    id: "e1",
    method: "POST",
    url: "/v1/messages",
    statusCode: 200,
    ...over,
  } as EntryMeta;
}

function filter(over: Partial<TrafficFilter>): TrafficFilter {
  return { text: "", streaming: false, errors: false, model: "", ...over } as TrafficFilter;
}

describe("applyFilter", () => {
  const entries = [
    meta({ id: "a", url: "/v1/messages", model: "claude-sonnet-4-6", statusCode: 200 }),
    meta({ id: "b", url: "/v1/chat/completions", model: "gpt-4o", statusCode: 500 }),
    meta({ id: "c", url: "/v1/responses", streaming: true, statusCode: 0 }),
  ];

  it("matches text against the path", () => {
    expect(applyFilter(entries, filter({ text: "chat/comp" })).map((e) => e.id)).toEqual(["b"]);
  });

  it("matches text against the model (§4.1.4)", () => {
    expect(applyFilter(entries, filter({ text: "sonnet" })).map((e) => e.id)).toEqual(["a"]);
  });

  it("matches text against the status code", () => {
    expect(applyFilter(entries, filter({ text: "500" })).map((e) => e.id)).toEqual(["b"]);
  });

  it("is case-insensitive and trims", () => {
    expect(applyFilter(entries, filter({ text: "  GPT-4O  " })).map((e) => e.id)).toEqual(["b"]);
  });

  it("streaming flag keeps only streaming entries", () => {
    expect(applyFilter(entries, filter({ streaming: true })).map((e) => e.id)).toEqual(["c"]);
  });

  it("errors flag keeps >=400 or errored entries", () => {
    const withErr = [...entries, meta({ id: "d", error: "boom", statusCode: 0 })];
    expect(applyFilter(withErr, filter({ errors: true })).map((e) => e.id)).toEqual(["b", "d"]);
  });

  it("model pill filters by substring", () => {
    expect(applyFilter(entries, filter({ model: "claude" })).map((e) => e.id)).toEqual(["a"]);
  });
});

describe("aggregateUsage", () => {
  it("sums tokens treating missing fields as zero", () => {
    const totals = aggregateUsage([
      meta({ inputTokens: 10, outputTokens: 5, cacheRead: 100, cacheCreate: 7 }),
      meta({ inputTokens: 2 }),
      meta({}),
    ]);
    expect(totals).toEqual({ entries: 3, input: 12, output: 5, cacheRead: 100, cacheCreate: 7 });
  });
});

describe("distinctModels", () => {
  it("dedupes, drops empties, and sorts", () => {
    const models = distinctModels([
      meta({ model: "gpt-4o" }),
      meta({ model: "claude-sonnet-4-6" }),
      meta({ model: "gpt-4o" }),
      meta({}),
    ]);
    expect(models).toEqual(["claude-sonnet-4-6", "gpt-4o"]);
  });
});

describe("groupKey", () => {
  it("prefers the endpoint label and falls back to method+url", () => {
    expect(groupKey(meta({ endpoint: "anthropic.messages" }))).toBe("anthropic.messages");
    expect(groupKey(meta({ url: "/x" }))).toBe("POST /x");
  });
});
