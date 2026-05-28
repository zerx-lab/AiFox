// Tokens tab — usage table + proportional bar + best-effort cost estimate.
//
// Pricing only covers the public list-prices of the Anthropic models we know
// about as of late 2026. For unknown models we still render the token counts
// and show "—" for cost; this avoids printing misleading numbers.
//
// Numbers go through decimal.js-light so list-price reconciliation matches the
// official invoice to the cent; native JS floats drift on long sums.

import Decimal from "decimal.js-light";
import type { components } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";

type AnthropicUsage = components["schemas"]["AnthropicUsage"];

interface Pricing {
  /** USD per million input tokens (no cache). */
  input: string;
  /** USD per million output tokens. */
  output: string;
  /** USD per million cache-read input tokens. */
  cacheRead: string;
  /** USD per million cache-creation input tokens. */
  cacheCreate: string;
}

// Public list prices in USD per MTok, kept as strings so Decimal sees the
// exact number the pricing sheet publishes. Source: anthropic.com/pricing
// circa late 2026. Update when Anthropic publishes a new sheet.
const PRICING: Record<string, Pricing> = {
  "claude-opus-4-7": {
    input: "15",
    output: "75",
    cacheRead: "1.5",
    cacheCreate: "18.75",
  },
  "claude-opus-4-5": {
    input: "15",
    output: "75",
    cacheRead: "1.5",
    cacheCreate: "18.75",
  },
  "claude-sonnet-4-6": {
    input: "3",
    output: "15",
    cacheRead: "0.3",
    cacheCreate: "3.75",
  },
  "claude-sonnet-4-5": {
    input: "3",
    output: "15",
    cacheRead: "0.3",
    cacheCreate: "3.75",
  },
  "claude-haiku-4-5": {
    input: "1",
    output: "5",
    cacheRead: "0.1",
    cacheCreate: "1.25",
  },
};

const MTOK = new Decimal(1_000_000);

// Single source of truth for every dollar figure on this tab; the top stat
// card and the breakdown calculator both go through this path so they can't
// disagree on rounding.
function lineCost(tokens: number, ratePerMTok: string): Decimal {
  return new Decimal(tokens).mul(ratePerMTok).div(MTOK);
}

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
  const cost = pricing ? totalCost({ cacheRead, cacheCreate, uncachedInput, output }, pricing) : null;

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
    pricing
      ? renderCostCalculator(pricing, { cacheRead, cacheCreate, uncachedInput, output })
      : null,
  );
}

interface UsageCounts {
  cacheRead: number;
  cacheCreate: number;
  uncachedInput: number;
  output: number;
}

function totalCost(counts: UsageCounts, pricing: Pricing): Decimal {
  return lineCost(counts.uncachedInput, pricing.input)
    .plus(lineCost(counts.output, pricing.output))
    .plus(lineCost(counts.cacheRead, pricing.cacheRead))
    .plus(lineCost(counts.cacheCreate, pricing.cacheCreate));
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

interface CalcRow {
  key: keyof UsageCounts;
  label: string;
  rate: string;
}

function renderCostCalculator(pricing: Pricing, actual: UsageCounts): HTMLElement {
  const rows: CalcRow[] = [
    { key: "cacheRead", label: t("detail.tokensRowCacheRead"), rate: pricing.cacheRead },
    { key: "cacheCreate", label: t("detail.tokensRowCacheCreate"), rate: pricing.cacheCreate },
    { key: "uncachedInput", label: t("detail.tokensRowInput"), rate: pricing.input },
    { key: "output", label: t("detail.tokensRowOutput"), rate: pricing.output },
  ];

  // Inputs are the source of truth while editing; we keep refs to update their
  // corresponding subtotal cells and the running total in place.
  const inputs = new Map<keyof UsageCounts, HTMLInputElement>();
  const subtotals = new Map<keyof UsageCounts, HTMLElement>();
  const totalCell = h("div.tk-calc-cell.tk-calc-total-val");
  const totalTokensCell = h("div.tk-calc-cell.tk-calc-num");

  const recompute = () => {
    const counts: UsageCounts = {
      cacheRead: readInput(inputs.get("cacheRead")),
      cacheCreate: readInput(inputs.get("cacheCreate")),
      uncachedInput: readInput(inputs.get("uncachedInput")),
      output: readInput(inputs.get("output")),
    };
    for (const r of rows) {
      const cell = subtotals.get(r.key);
      if (cell) cell.textContent = formatCost(lineCost(counts[r.key], r.rate));
    }
    totalCell.textContent = formatCost(totalCost(counts, pricing));
    totalTokensCell.textContent = (
      counts.cacheRead + counts.cacheCreate + counts.uncachedInput + counts.output
    ).toLocaleString();
  };

  const header = h(
    "div.tk-calc-row.tk-calc-head-row",
    null,
    h("div.tk-calc-cell.tk-calc-col-h", null, t("detail.tokensCalcColCategory")),
    h("div.tk-calc-cell.tk-calc-col-h", null, t("detail.tokensCalcColTokens")),
    h("div.tk-calc-cell.tk-calc-col-h", null, t("detail.tokensCalcColRate")),
    h("div.tk-calc-cell.tk-calc-col-h", null, t("detail.tokensCalcColCost")),
  );

  const grid = h("div.tk-calc-grid", null, header);
  for (const r of rows) {
    const input = h("input.tk-calc-input", {
      type: "number",
      min: "0",
      step: "1",
      value: String(actual[r.key]),
      disabled: true,
      oninput: recompute,
    }) as HTMLInputElement;
    inputs.set(r.key, input);

    const subtotal = h("div.tk-calc-cell.tk-calc-num.tk-calc-cost");
    subtotals.set(r.key, subtotal);

    grid.appendChild(
      h(
        "div.tk-calc-row",
        null,
        h("div.tk-calc-cell.tk-calc-cat", null, r.label),
        h("div.tk-calc-cell", null, input),
        h("div.tk-calc-cell.tk-calc-rate", null, priceLabel(r.rate)),
        subtotal,
      ),
    );
  }

  grid.appendChild(
    h(
      "div.tk-calc-row.tk-calc-total-row",
      null,
      h("div.tk-calc-cell.tk-calc-total-lbl", null, t("detail.tokensCalcTotal")),
      totalTokensCell,
      h("div.tk-calc-cell"),
      totalCell,
    ),
  );

  let editing = false;
  const toggleBtn = h("button.tk-calc-toggle", {
    type: "button",
    onclick: () => {
      editing = !editing;
      for (const input of inputs.values()) input.disabled = !editing;
      toggleBtn.textContent = editing
        ? t("detail.tokensCalcCancel")
        : t("detail.tokensCalcEdit");
      resetBtn.style.display = editing ? "" : "none";
      if (!editing) {
        for (const r of rows) {
          const input = inputs.get(r.key);
          if (input) input.value = String(actual[r.key]);
        }
        recompute();
      }
    },
  }, t("detail.tokensCalcEdit")) as HTMLButtonElement;

  const resetBtn = h("button.tk-calc-reset", {
    type: "button",
    style: { display: "none" },
    onclick: () => {
      for (const r of rows) {
        const input = inputs.get(r.key);
        if (input) input.value = String(actual[r.key]);
      }
      recompute();
    },
  }, t("detail.tokensCalcReset")) as HTMLButtonElement;

  recompute();

  return h(
    "div.tk-calc",
    null,
    h(
      "div.tk-calc-head",
      null,
      h("span.tk-calc-title", null, t("detail.tokensCalcTitle")),
      h("div.tk-calc-actions", null, resetBtn, toggleBtn),
    ),
    grid,
  );
}

function readInput(el: HTMLInputElement | undefined): number {
  if (!el) return 0;
  const n = Number(el.value);
  return Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0;
}

// 4 decimal places matches the top "Cost" stat card so the two displays line up.
function formatCost(value: Decimal): string {
  return `$${value.toFixed(4)}`;
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

function priceLabel(ratePerMTok: string): string {
  return `$${new Decimal(ratePerMTok).toFixed(2)}/MTok`;
}
