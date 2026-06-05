// Replay popover — re-issues the currently selected entry with the user's
// overrides applied. The new entry will arrive via the SSE stream and the
// sidebar will tag it with "↩ replay of <id>".

import { getClient } from "../../api/client";
import { t } from "../i18n";
import { h } from "./dom";
import { getState, selectedFull, setReplayOpen, setState, type TrafficEntry } from "./state";

interface Draft {
  model: string;
  temperature: string;
  topP: string;
  maxTokens: string;
}

export function renderReplayPopover(): HTMLElement | null {
  const state = getState();
  if (!state.replayOpen) return null;
  // selectedFull() returns the loaded full TrafficEntry; null while loading.
  const entry = selectedFull();
  if (!entry) return null;

  const draft: Draft = {
    model: pickModel(entry),
    temperature: pickTemperature(entry),
    topP: pickTopP(entry),
    maxTokens: pickMaxTokens(entry),
  };
  const status = h("div.replay-status");

  const pop = h(
    "div.replay-pop",
    {
      onclick: (e: Event) => e.stopPropagation(),
    },
    h(
      "h4",
      null,
      t("replay.title"),
      " ",
      h("span.replay-id", null, entry.id),
    ),
    h(
      "div.replay-hint",
      null,
      t("replay.hint"),
    ),
    row(t("replay.model"), draftInput(draft, "model", "text")),
    row(t("replay.temperature"), draftInput(draft, "temperature", "number", "0.1")),
    row(t("replay.topP"), draftInput(draft, "topP", "number", "0.05")),
    row(t("replay.maxTokens"), draftInput(draft, "maxTokens", "number", "64")),
    status,
    h(
      "div.actions",
      null,
      h(
        "button.btn.secondary",
        { onclick: () => setReplayOpen(false) },
        t("replay.cancel"),
      ),
      h(
        "button.btn",
        {
          onclick: async () => {
            status.textContent = t("replay.running");
            status.className = "replay-status info";
            try {
              const client = await getClient();
              const overrides = buildOverrides(draft, entry);
              const { data, error } = await client.POST("/v1/traffic/{id}/replay", {
                params: { path: { id: entry.id } },
                body: { overrides },
              });
              if (error || !data) {
                status.textContent = t("replay.failed", {
                  error: String((error as { detail?: string })?.detail ?? "unknown"),
                });
                status.className = "replay-status err";
                return;
              }
              setReplayOpen(false);
              // Pre-select the freshly issued entry so the user sees the
              // result immediately instead of having to find it in the list.
              setState({ selectedId: data.entryId });
            } catch (e) {
              status.textContent = t("replay.failed", { error: String(e) });
              status.className = "replay-status err";
            }
          },
        },
        t("replay.run"),
      ),
    ),
  );
  return pop;
}

function draftInput(
  draft: Draft,
  key: keyof Draft,
  type: string,
  step?: string,
): HTMLInputElement {
  const inp = document.createElement("input");
  inp.type = type;
  inp.value = draft[key];
  if (step) inp.step = step;
  inp.addEventListener("input", () => {
    draft[key] = inp.value;
  });
  return inp;
}

function row(label: string, input: HTMLElement): HTMLElement {
  return h("div.replay-row", null, h("label", null, label), input);
}

function pickModel(entry: TrafficEntry): string {
  const a = entry.analysis as { anthropic?: { request?: { model?: string } } } | undefined;
  return a?.anthropic?.request?.model ?? "";
}

function pickTemperature(entry: TrafficEntry): string {
  const a = entry.analysis as { anthropic?: { request?: { temperature?: number } } } | undefined;
  const v = a?.anthropic?.request?.temperature;
  return v === undefined ? "" : String(v);
}

function pickTopP(entry: TrafficEntry): string {
  const a = entry.analysis as { anthropic?: { request?: { topP?: number } } } | undefined;
  const v = a?.anthropic?.request?.topP;
  return v === undefined ? "" : String(v);
}

function pickMaxTokens(entry: TrafficEntry): string {
  const a = entry.analysis as { anthropic?: { request?: { maxTokens?: number } } } | undefined;
  const v = a?.anthropic?.request?.maxTokens;
  return v === undefined ? "" : String(v);
}

function buildOverrides(
  draft: Draft,
  original: TrafficEntry,
): {
  model?: string;
  temperature?: number;
  topP?: number;
  maxTokens?: number;
} {
  const out: { model?: string; temperature?: number; topP?: number; maxTokens?: number } = {};
  if (draft.model && draft.model !== pickModel(original)) out.model = draft.model;
  const t1 = num(draft.temperature);
  if (t1 !== null && draft.temperature !== pickTemperature(original)) out.temperature = t1;
  const t2 = num(draft.topP);
  if (t2 !== null && draft.topP !== pickTopP(original)) out.topP = t2;
  const t3 = num(draft.maxTokens);
  if (t3 !== null && draft.maxTokens !== pickMaxTokens(original)) out.maxTokens = Math.floor(t3);
  return out;
}

function num(s: string): number | null {
  if (!s.trim()) return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}
