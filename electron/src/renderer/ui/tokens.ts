// Tokens tab — usage table + proportional bar + best-effort cost estimate.
//
// Pricing only covers the public list-prices of the Anthropic models we know
// about as of late 2026. For unknown models we still render the token counts
// and show "—" for cost; this avoids printing misleading numbers.

import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";

type AnthropicUsage = components["schemas"]["AnthropicUsage"];

interface Pricing {
  /** USD per input token (no cache). */
  input: number;
  /** USD per output token. */
  output: number;
  /** USD per cache-read input token. */
  cacheRead: number;
  /** USD per cache-creation input token. */
  cacheCreate: number;
}

// Public list prices, expressed in $/token. Sources: anthropic.com/pricing
// circa late 2026. Update when Anthropic publishes a new sheet.
const PRICING: Record<string, Pricing> = {
  "claude-opus-4-7": {
    input: 15 / 1_000_000,
    output: 75 / 1_000_000,
    cacheRead: 1.5 / 1_000_000,
    cacheCreate: 18.75 / 1_000_000,
  },
  "claude-opus-4-5": {
    input: 15 / 1_000_000,
    output: 75 / 1_000_000,
    cacheRead: 1.5 / 1_000_000,
    cacheCreate: 18.75 / 1_000_000,
  },
  "claude-sonnet-4-6": {
    input: 3 / 1_000_000,
    output: 15 / 1_000_000,
    cacheRead: 0.3 / 1_000_000,
    cacheCreate: 3.75 / 1_000_000,
  },
  "claude-sonnet-4-5": {
    input: 3 / 1_000_000,
    output: 15 / 1_000_000,
    cacheRead: 0.3 / 1_000_000,
    cacheCreate: 3.75 / 1_000_000,
  },
  "claude-haiku-4-5": {
    input: 1 / 1_000_000,
    output: 5 / 1_000_000,
    cacheRead: 0.1 / 1_000_000,
    cacheCreate: 1.25 / 1_000_000,
  },
};

export function renderTokens(usage: AnthropicUsage | undefined, model?: string): HTMLElement {
  if (!usage) {
    return h("div.detail-empty", null, t("detail.tokensEmpty"));
  }

  const cacheRead = usage.cacheReadInputTokens ?? 0;
  const cacheCreate = usage.cacheCreationInputTokens ?? 0;
  const uncachedInput = usage.inputTokens ?? 0;
  const totalIn = uncachedInput + cacheRead + cacheCreate;
  const output = usage.outputTokens ?? 0;
  const total = totalIn + output || 1;

  const pricing = model ? matchPricing(model) : undefined;
  const cost = pricing
    ? uncachedInput * pricing.input +
      output * pricing.output +
      cacheRead * pricing.cacheRead +
      cacheCreate * pricing.cacheCreate
    : null;

  const stats = h(
    "div.det-usage-stats",
    null,
    statCell(t("detail.tokensTotalInput"), totalIn.toLocaleString()),
    statCell(t("detail.tokensOutput"), output.toLocaleString(), "tool"),
    cost !== null
      ? statCell(t("detail.tokensCost"), `$${cost.toFixed(4)}`)
      : statCell(t("detail.tokensCost"), "—"),
  );

  const bar = h("div.tk-bar");
  appendBarSlice(bar, "b-cache", cacheRead, total, "cacheRead");
  appendBarSlice(bar, "b-cache-create", cacheCreate, total, "cacheCreate");
  appendBarSlice(bar, "b-input", uncachedInput, total, "input");
  appendBarSlice(bar, "b-output", output, total, "output");

  const table = h(
    "table.tk-table",
    null,
    h(
      "tbody",
      null,
      tkRow("cache_read_input_tokens", t("detail.tokensRowCacheRead"), cacheRead),
      tkRow("cache_creation_input_tokens", t("detail.tokensRowCacheCreate"), cacheCreate),
      tkRow("input_tokens", t("detail.tokensRowInput"), uncachedInput),
      tkRow("output_tokens", t("detail.tokensRowOutput"), output),
      h(
        "tr.total",
        null,
        h("td", null, t("detail.tokensTotal")),
        h("td", null, total.toLocaleString()),
      ),
    ),
  );

  return h(
    "div",
    null,
    stats,
    bar,
    table,
    renderPricing(model, pricing),
  );
}

function renderPricing(
  model: string | undefined,
  pricing: Pricing | undefined,
): HTMLElement {
  if (!pricing) {
    return h(
      "div.tk-pricing-note",
      null,
      t("detail.tokensNoPricing", { model: model ?? "—" }),
    );
  }
  return h(
    "div.tk-pricing",
    null,
    h(
      "div.tk-pricing-head",
      null,
      h("span.tk-pricing-label", null, t("detail.tokensPricingModel")),
      h("span.tk-pricing-model", { title: model ?? "" }, model ?? "—"),
    ),
    h(
      "div.tk-pricing-grid",
      null,
      pricingCell(t("detail.tokensPricingCacheRead"), priceLabel(pricing.cacheRead), "ok"),
      pricingCell(t("detail.tokensPricingCacheCreate"), priceLabel(pricing.cacheCreate), "warn"),
      pricingCell(t("detail.tokensPricingInput"), priceLabel(pricing.input)),
      pricingCell(t("detail.tokensPricingOutput"), priceLabel(pricing.output), "tool"),
    ),
  );
}

function pricingCell(label: string, value: string, variant?: string): HTMLElement {
  return h(
    `div.tk-price${variant ? `.v-${variant}` : ""}`,
    null,
    h("div.tk-price-l", null, label),
    h("div.tk-price-v", null, value),
  );
}

function statCell(label: string, value: string, variant?: string): HTMLElement {
  return h(
    `div.cstat${variant ? `.v-${variant}` : ""}`,
    null,
    h("div.l", null, label),
    h("div.v", null, value),
  );
}

function tkRow(rawKey: string, label: string, value: number): HTMLElement {
  // Hover shows the raw Anthropic field name; the visible label is the
  // localized description chosen by the active locale.
  return h(
    "tr",
    null,
    h("td", { title: rawKey }, label),
    h("td", null, value.toLocaleString()),
  );
}

function appendBarSlice(
  bar: HTMLElement,
  klass: string,
  value: number,
  total: number,
  _label: string,
) {
  if (value <= 0) return;
  const pct = (value / total) * 100;
  const slice = h(`div.${klass}`, null, `${Math.round(pct)}%`);
  (slice.style as CSSStyleDeclaration).width = `${pct}%`;
  bar.appendChild(slice);
}

function matchPricing(model: string): Pricing | undefined {
  // Exact match first; fall back to a normalized "family" key so
  // "claude-opus-4-5-20250101" still maps to "claude-opus-4-5".
  if (PRICING[model]) return PRICING[model];
  const trimmed = model.replace(/-\d{8}.*$/, "");
  return PRICING[trimmed];
}

function priceLabel(perToken: number): string {
  return `$${(perToken * 1_000_000).toFixed(2)}/MTok`;
}
