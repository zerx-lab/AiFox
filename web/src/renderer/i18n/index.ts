// Minimal in-house i18n. Two locales (en, zh-CN) are enough for the MVP.
// String tables are keyed by dotted path so call sites read like `t("nav.traffic")`.
//
// Locale resolution order:
//   1. The value persisted via /v1/settings (user preference, follows-OS = "").
//   2. navigator.language prefix match.
//   3. "en" fallback.

import { en } from "./locales/en";
import { zhCN } from "./locales/zh-CN";

export type LanguageCode = "en" | "zh-CN";

// Dictionary keeps the shape of `en` but widens leaf strings so translated
// tables can supply different literals while still being structurally checked.
export type Dictionary = WidenStrings<typeof en>;
type WidenStrings<T> = {
  [K in keyof T]: T[K] extends string ? string : WidenStrings<T[K]>;
};

const TABLES: Record<LanguageCode, Dictionary> = {
  en,
  "zh-CN": zhCN,
};

const SUPPORTED: readonly LanguageCode[] = ["en", "zh-CN"];

let active: LanguageCode = "en";
const listeners = new Set<() => void>();

export function getLanguage(): LanguageCode {
  return active;
}

export function setLanguage(code: LanguageCode | "") {
  const next = code === "" ? detectFromNavigator() : normalize(code);
  if (next === active) return;
  active = next;
  document.documentElement.lang = next;
  for (const fn of listeners) fn();
}

export function onLanguageChange(fn: () => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

export function supportedLanguages(): readonly LanguageCode[] {
  return SUPPORTED;
}

/** Look up a dotted key. Missing keys fall back to the en table, then to the
 *  raw key itself so problems show up obviously in the UI. */
export function t(key: string, params?: Record<string, string | number>): string {
  const raw = lookup(TABLES[active], key) ?? lookup(TABLES.en, key) ?? key;
  return params ? interpolate(raw, params) : raw;
}

function lookup(table: Dictionary, key: string): string | undefined {
  let cur: unknown = table;
  for (const part of key.split(".")) {
    if (cur && typeof cur === "object" && part in (cur as Record<string, unknown>)) {
      cur = (cur as Record<string, unknown>)[part];
    } else {
      return undefined;
    }
  }
  return typeof cur === "string" ? cur : undefined;
}

function interpolate(raw: string, params: Record<string, string | number>): string {
  return raw.replace(/\{(\w+)\}/g, (_, name) => {
    const value = params[name];
    return value === undefined ? `{${name}}` : String(value);
  });
}

function detectFromNavigator(): LanguageCode {
  const nav = (typeof navigator !== "undefined" && navigator.language) || "";
  if (nav.toLowerCase().startsWith("zh")) return "zh-CN";
  return "en";
}

function normalize(code: string): LanguageCode {
  const lower = code.toLowerCase();
  if (lower === "zh-cn" || lower.startsWith("zh")) return "zh-CN";
  return "en";
}
