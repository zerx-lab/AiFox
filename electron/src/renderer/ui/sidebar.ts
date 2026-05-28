// Sidebar: filterable list of captured traffic entries (newest first).

import { getClient } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { fmtDuration, fmtTime, statusKind } from "./format";
import { clearEntries, getState, setState, type TrafficEntry } from "./state";

export function renderSidebar(): HTMLElement {
  const state = getState();
  const filtered = applyFilter(state.entries, state.filter);

  const filterInput = h("input", {
    type: "text",
    placeholder: t("sidebar.filterPlaceholder"),
    value: state.filter,
    oninput: (e: Event) => setState({ filter: (e.target as HTMLInputElement).value }),
  }) as HTMLInputElement;

  const head = h(
    "div.side-head",
    null,
    h("span.label", null, t("nav.traffic")),
    h("span.count", null, String(state.entries.length)),
  );

  const list = h("div.side-list");
  if (filtered.length === 0) {
    list.appendChild(
      h(
        "div.side-empty",
        null,
        h("div.h", null, t("sidebar.empty")),
        h("div", null, t("sidebar.emptyHint")),
      ),
    );
  } else {
    for (const entry of filtered) list.appendChild(entryRow(entry));
  }

  const actions = h(
    "div.side-actions",
    null,
    h(
      "button",
      {
        onclick: async () => {
          if (!confirm(t("sidebar.confirmClear"))) return;
          const client = await getClient();
          await client.DELETE("/v1/traffic", {});
          clearEntries();
        },
      },
      t("sidebar.clear"),
    ),
  );

  return h("div.sidebar", null, head, h("div.side-filter", null, filterInput), actions, list);
}

function entryRow(entry: TrafficEntry): HTMLElement {
  const state = getState();
  const active = state.selectedId === entry.id;
  const kind = statusKind(entry);
  const badgeLabel =
    kind === "pending"
      ? "…"
      : kind === "err"
        ? entry.statusCode > 0
          ? String(entry.statusCode)
          : "ERR"
        : entry.streaming
          ? "SSE"
          : String(entry.statusCode);

  return h(
    "div",
    {
      class: `entry${active ? " active" : ""}`,
      onclick: () => setState({ selectedId: entry.id }),
    },
    h("span", { class: `badge ${kind}` }, badgeLabel),
    h("span.path", { title: entry.url }, entry.method, " ", entry.url),
    h("span.meta", null, fmtDuration(entry.durationMillis)),
    h(
      "span.sub",
      null,
      h("span", null, fmtTime(entry.startedAt)),
      entry.streaming ? h("span", null, "stream") : null,
      entry.truncated ? h("span", null, "truncated") : null,
    ),
  );
}

function applyFilter(entries: TrafficEntry[], filter: string): TrafficEntry[] {
  if (!filter.trim()) return entries;
  const needle = filter.trim().toLowerCase();
  return entries.filter((e) => {
    return (
      e.url.toLowerCase().includes(needle) ||
      e.method.toLowerCase().includes(needle) ||
      String(e.statusCode).includes(needle)
    );
  });
}
