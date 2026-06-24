// Filter pill — sits above the timeline. Each pill is a toggle/selector
// against the shared TrafficFilter state. Sidebar and statusbar both
// re-render off the same state.

import { t } from "../i18n";
import { h } from "./dom";
import { applyFilter, distinctModels } from "./grouping";
import { customSelect } from "./select";
import { getState, setFilters } from "./state";

export function renderFilterPills(): HTMLElement {
  const state = getState();
  const models = distinctModels(state.entries);
  const filtered = applyFilter(state.entries, state.filters);

  const modelOptions = [
    { value: "", label: t("filter.allModels") },
    ...models.map((m) => ({ value: m, label: m })),
  ];

  const modelSelect = customSelect({
    value: state.filters.model,
    options: modelOptions,
    onChange: (v) => setFilters({ model: v }),
    className: "filter-pill filter-model",
    menuClassName: "filter-model-menu",
  });

  // The free-text query lives in the sidebar's search box (the primary input).
  // Here we surface it as a read-only chip with its own × so the user can see
  // and clear an active text filter from the center bar too — without a second
  // mirrored input box that confusingly echoes the sidebar one (§4.1.4).
  const textChip = state.filters.text
    ? h(
        "button.filter-pill.filter-text-chip",
        {
          title: t("filter.textChipTitle", { text: state.filters.text }),
          onclick: () => setFilters({ text: "" }),
        },
        h("span.filter-text-chip-q", null, `"${state.filters.text}"`),
        h("span.filter-text-chip-x", null, "×"),
      )
    : null;

  return h(
    "div.filter-bar",
    null,
    togglePill("streaming", t("filter.streaming"), state.filters.streaming),
    togglePill("errors", t("filter.errors"), state.filters.errors),
    modelSelect,
    textChip,
    state.filters.streaming || state.filters.errors || state.filters.model || state.filters.text
      ? h(
          "button.filter-pill.filter-clear",
          {
            onclick: () =>
              setFilters({ streaming: false, errors: false, model: "", text: "" }),
          },
          t("filter.clear"),
        )
      : null,
    h(
      "span.filter-count",
      null,
      t("filter.visible", {
        visible: String(filtered.length),
        total: String(state.entries.length),
      }),
    ),
  );
}

function togglePill(
  key: "streaming" | "errors",
  label: string,
  active: boolean,
): HTMLElement {
  return h(
    "button",
    {
      class: `filter-pill${active ? " active" : ""}`,
      onclick: () => setFilters({ [key]: !active } as { streaming?: boolean; errors?: boolean }),
    },
    label,
  );
}
