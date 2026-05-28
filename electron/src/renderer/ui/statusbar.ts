import { t } from "../i18n";
import { h } from "./dom";
import { fmtBytes } from "./format";
import { getState } from "./state";

export function renderStatusbar(): HTMLElement {
  const s = getState();
  const total = s.entries.length;
  const lastError = s.entries.find((e) => e.error)?.error ?? "";

  const proxyText =
    s.proxy?.enabled && s.proxy.address
      ? t("status.listening", { address: s.proxy.address })
      : t("status.notListening");

  const bytesIn = s.entries.reduce((acc, e) => acc + (e.requestSize ?? 0), 0);
  const bytesOut = s.entries.reduce((acc, e) => acc + (e.responseSize ?? 0), 0);

  return h(
    "div.statusbar",
    null,
    h("span", { class: s.proxy?.configured ? "ok" : "warn" }, proxyText),
    h("span", null, "·"),
    h("span", null, t("status.entries", { count: total })),
    h("span", null, "·"),
    h("span", null, `↑ ${fmtBytes(bytesIn)}`),
    h("span", null, `↓ ${fmtBytes(bytesOut)}`),
    h("span.spacer"),
    lastError ? h("span.err", null, lastError) : null,
  );
}
