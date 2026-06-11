// Settings page: upstream + auth + appearance + runtime. Saving PUTs the
// whole SettingsBody back to /v1/settings, applies theme/language locally,
// and refreshes the proxy info chip.

import { setLanguage, supportedLanguages, t } from "../i18n";
import { getProxyInfo, putSettings } from "./api-service";
import { h } from "./dom";
import { customSelect } from "./select";
import { getState, type HeaderKV, type Settings, setState } from "./state";
import { setTheme, type ThemeChoice } from "./theme";

interface Draft {
  upstreamBaseUrl: string;
  upstreamApiKey: string;
  authPreset: string;
  customHeaders: HeaderKV[];
  proxyEnabled: boolean;
  proxyPort: number;
  language: string;
  theme: string;
}

const DEFAULT_PROXY_PORT = 8787;

function toDraft(s: Settings): Draft {
  return {
    upstreamBaseUrl: s.upstreamBaseUrl ?? "",
    upstreamApiKey: s.upstreamApiKey ?? "",
    authPreset: s.authPreset || "anthropic",
    customHeaders: (s.customHeaders ?? []).map((h) => ({ ...h })),
    proxyEnabled: s.proxyEnabled ?? false,
    proxyPort: s.proxyPort && s.proxyPort > 0 ? s.proxyPort : DEFAULT_PROXY_PORT,
    language: s.language ?? "",
    theme: s.theme ?? "",
  };
}

function emptySettings(): Settings {
  return {
    upstreamBaseUrl: "",
    upstreamApiKey: "",
    authPreset: "anthropic",
    customHeaders: [],
    proxyEnabled: false,
    proxyPort: DEFAULT_PROXY_PORT,
    language: "",
    theme: "",
    layout: { colLeft: 0, colRight: 0, bottomHeight: 0 },
  };
}

const PRESET_OPTIONS: ReadonlyArray<[string, string]> = [
  ["anthropic", "settings.presetAnthropic"],
  ["openai", "settings.presetOpenAI"],
  ["openai-responses", "settings.presetOpenAIResponses"],
  ["custom", "settings.presetCustom"],
];

// Module-level draft so user input is not discarded on unrelated re-renders
// (e.g. an SSE entry event bumping the `ui` version). The draft is rebuilt
// only when the server-side settings actually change (after a successful PUT
// or on first render when no draft exists yet).
let _draft: Draft | null = null;
// The Settings revision that _draft was seeded from. Using object identity
// rather than a counter — the state.settings reference is replaced on every
// successful PUT or initial load, so identity change = server-authoritative
// update and we rebuild the draft.
let _draftSource: Settings | null = null;

export function renderSettings(): HTMLElement {
  const serverSettings = getState().settings ?? emptySettings();

  // Rebuild draft only when server settings changed (new object reference)
  // or no draft has been created yet. This preserves user edits across the
  // region re-renders triggered by unrelated state slices.
  if (_draft === null || _draftSource !== getState().settings) {
    _draft = toDraft(serverSettings);
    _draftSource = getState().settings;
  }
  const draft = _draft;

  const root = h("div.view-settings");
  const inner = h("div.settings");
  root.appendChild(inner);

  inner.appendChild(h("h1", null, t("settings.title")));
  inner.appendChild(h("p.lead", null, t("settings.description")));

  // ----- upstream section -----
  const section1 = section(t("settings.sectionUpstream"));
  inner.appendChild(section1);

  const baseUrlInput = textField({
    label: t("settings.upstreamBaseUrl"),
    hint: t("settings.upstreamBaseUrlHint"),
    placeholder: "https://api.anthropic.com",
    value: draft.upstreamBaseUrl,
    onchange: (v) => (draft.upstreamBaseUrl = v),
  });
  section1.appendChild(baseUrlInput);

  // ----- auth section -----
  const section2 = section(t("settings.sectionAuth"));
  inner.appendChild(section2);

  const apiKeyInput = textField({
    label: t("settings.upstreamApiKey"),
    hint: t("settings.upstreamApiKeyHint"),
    type: "password",
    value: draft.upstreamApiKey,
    onchange: (v) => (draft.upstreamApiKey = v),
  });
  section2.appendChild(apiKeyInput);

  const headersHost = h("div");
  const renderHeaders = () => {
    headersHost.replaceChildren(headerEditor(draft, () => renderHeaders()));
  };

  const presetField = selectField({
    label: t("settings.authPreset"),
    hint: t("settings.authPresetHint"),
    value: draft.authPreset,
    options: PRESET_OPTIONS.map(([v, k]) => [v, t(k)] as [string, string]),
    onchange: (v) => {
      draft.authPreset = v;
      renderHeaders();
    },
  });
  section2.appendChild(presetField);
  renderHeaders();
  section2.appendChild(headersHost);

  // ----- runtime section (proxy enabled) -----
  const section3 = section(t("settings.sectionRuntime"));
  inner.appendChild(section3);

  const proxyField = selectField({
    label: t("settings.proxyEnabled"),
    hint: t("settings.proxyEnabledHint"),
    value: draft.proxyEnabled ? "on" : "off",
    options: [
      ["on", t("settings.proxyOn")],
      ["off", t("settings.proxyOff")],
    ],
    onchange: (v) => (draft.proxyEnabled = v === "on"),
  });
  section3.appendChild(proxyField);

  const portField = numberField({
    label: t("settings.proxyPort"),
    hint: t("settings.proxyPortHint"),
    value: draft.proxyPort,
    min: 1,
    max: 65535,
    onchange: (v) => {
      draft.proxyPort = v;
    },
  });
  section3.appendChild(portField);

  // ----- appearance section -----
  const section4 = section(t("settings.sectionAppearance"));
  inner.appendChild(section4);

  section4.appendChild(
    h(
      "div.row2",
      null,
      selectField({
        label: t("settings.language"),
        value: draft.language,
        options: [
          ["", t("settings.languageFollowOs")],
          ...supportedLanguages().map(
            (c) => [c, c === "en" ? "English" : "中文"] as [string, string],
          ),
        ],
        onchange: (v) => (draft.language = v),
      }),
      selectField({
        label: t("settings.theme"),
        value: draft.theme,
        options: [
          ["", t("settings.themeFollowOs")],
          ["dark", t("settings.themeDark")],
          ["light", t("settings.themeLight")],
        ],
        onchange: (v) => (draft.theme = v),
      }),
    ),
  );

  // ----- save row -----
  const toast = h("span", {
    style: { marginLeft: "12px", fontSize: "12px" },
  }) as HTMLElement;

  let saving = false;
  const saveBtn = h(
    "button.btn",
    {
      onclick: async (e: Event) => {
        if (saving) return;
        saving = true;
        const btn = e.currentTarget as HTMLButtonElement;
        btn.disabled = true;
        btn.textContent = t("settings.saving");
        toast.textContent = "";

        const next: Settings = {
          upstreamBaseUrl: draft.upstreamBaseUrl.trim(),
          upstreamApiKey: draft.upstreamApiKey,
          authPreset: (draft.authPreset || "anthropic") as Settings["authPreset"],
          customHeaders: draft.customHeaders
            .map((h) => ({ name: h.name.trim(), value: h.value }))
            .filter((h) => h.name.length > 0),
          proxyEnabled: draft.proxyEnabled,
          proxyPort: clampPort(draft.proxyPort),
          language: draft.language as Settings["language"],
          theme: draft.theme as Settings["theme"],
          // Layout is owned by the resize-drag persistence (separate endpoint);
          // pass the server's current value through untouched so a settings
          // Save never resets the user's panel geometry.
          layout: getState().settings?.layout ?? {
            colLeft: 0,
            colRight: 0,
            bottomHeight: 0,
          },
        };
        const res = await putSettings(next);
        if (!res.ok) {
          // The service already surfaced the error via the global toast; reflect
          // it inline next to the Save button too.
          toast.textContent = t("settings.saveFailedGeneric");
          toast.style.color = "var(--err)";
          btn.disabled = false;
          btn.textContent = t("settings.save");
          saving = false;
          return;
        }
        const data = res.data;
        setState({ settings: data });
        setLanguage((data.language ?? "") as "" | "en" | "zh-CN");
        setTheme((data.theme ?? "") as ThemeChoice);
        // Refresh proxy info so the chip + statusbar reflect the new enabled
        // state and the configured/notConfigured warning.
        const info = await getProxyInfo();
        if (info.ok) setState({ proxy: info.data });
        toast.textContent = t("settings.saved");
        toast.style.color = "var(--ok)";
        btn.disabled = false;
        btn.textContent = t("settings.save");
        saving = false;
      },
    },
    t("settings.save"),
  );

  inner.appendChild(
    h("div", { style: { display: "flex", alignItems: "center" } }, saveBtn, toast),
  );

  return root;
}

function section(title: string): HTMLElement {
  return h("div.settings-section", null, h("h2", null, title));
}

function textField(opts: {
  label: string;
  hint?: string;
  type?: string;
  value: string;
  placeholder?: string;
  onchange: (v: string) => void;
}): HTMLElement {
  const input = document.createElement("input");
  input.type = opts.type ?? "text";
  input.value = opts.value;
  if (opts.placeholder) input.placeholder = opts.placeholder;
  input.addEventListener("input", () => opts.onchange(input.value));
  return h(
    "div.field",
    null,
    h("label", null, opts.label),
    input,
    opts.hint ? h("div.hint", null, opts.hint) : null,
  );
}

function numberField(opts: {
  label: string;
  hint?: string;
  value: number;
  min?: number;
  max?: number;
  onchange: (v: number) => void;
}): HTMLElement {
  const input = document.createElement("input");
  input.type = "number";
  input.value = String(opts.value);
  if (opts.min !== undefined) input.min = String(opts.min);
  if (opts.max !== undefined) input.max = String(opts.max);
  input.addEventListener("input", () => {
    const parsed = Number.parseInt(input.value, 10);
    if (Number.isFinite(parsed)) opts.onchange(parsed);
  });
  return h(
    "div.field",
    null,
    h("label", null, opts.label),
    input,
    opts.hint ? h("div.hint", null, opts.hint) : null,
  );
}

function clampPort(n: number): number {
  if (!Number.isFinite(n)) return DEFAULT_PROXY_PORT;
  if (n < 1 || n > 65535) return DEFAULT_PROXY_PORT;
  return Math.floor(n);
}

function selectField(opts: {
  label: string;
  hint?: string;
  value: string;
  options: ReadonlyArray<[string, string]>;
  onchange: (v: string) => void;
}): HTMLElement {
  const sel = customSelect({
    value: opts.value,
    options: opts.options.map(([value, label]) => ({ value, label })),
    onChange: (v) => opts.onchange(v),
    className: "cselect-field",
  });
  return h(
    "div.field",
    null,
    h("label", null, opts.label),
    sel,
    opts.hint ? h("div.hint", null, opts.hint) : null,
  );
}

function headerEditor(draft: Draft, rerender: () => void): HTMLElement {
  if (draft.authPreset !== "custom") {
    // Show a read-only preview of what the preset will inject so users know
    // what the proxy is actually sending.
    const preset = previewPreset(draft);
    return h(
      "div.field",
      null,
      h("label", null, t("settings.customHeaders")),
      h("div.hint", null, t("settings.authPresetHint")),
      h(
        "div.preset-preview",
        null,
        ...preset.map((row) =>
          h(
            "div.preset-row",
            null,
            h("span.preset-key", null, row.name),
            h("span.preset-val", null, row.value),
          ),
        ),
      ),
    );
  }

  const rows = h("div.header-rows");
  draft.customHeaders.forEach((kv, idx) => {
    const nameInput = document.createElement("input");
    nameInput.type = "text";
    nameInput.placeholder = t("settings.headerName");
    nameInput.value = kv.name;
    nameInput.addEventListener("input", () => {
      const current = draft.customHeaders[idx];
      if (current) current.name = nameInput.value;
    });
    const valInput = document.createElement("input");
    valInput.type = "text";
    valInput.placeholder = t("settings.headerValue");
    valInput.value = kv.value;
    valInput.addEventListener("input", () => {
      const current = draft.customHeaders[idx];
      if (current) current.value = valInput.value;
    });
    rows.appendChild(
      h(
        "div.header-row",
        null,
        nameInput,
        valInput,
        h(
          "button.icon-btn",
          {
            type: "button",
            title: t("settings.headerRemove"),
            "aria-label": t("settings.headerRemove"),
            onclick: () => {
              draft.customHeaders.splice(idx, 1);
              rerender();
            },
          },
          "×",
        ),
      ),
    );
  });

  return h(
    "div.field",
    null,
    h("label", null, t("settings.customHeaders")),
    h("div.hint", null, t("settings.customHeadersHint")),
    rows,
    h(
      "button.btn.secondary",
      {
        type: "button",
        onclick: () => {
          draft.customHeaders.push({ name: "", value: "" });
          rerender();
        },
      },
      `+ ${t("settings.headerAdd")}`,
    ),
  );
}

function previewPreset(draft: Draft): { name: string; value: string }[] {
  const key = draft.upstreamApiKey || "<api-key>";
  switch (draft.authPreset) {
    case "openai":
    case "openai-responses":
      return [{ name: "Authorization", value: `Bearer ${key}` }];
    default:
      return [
        { name: "x-api-key", value: key },
        { name: "anthropic-version", value: "2023-06-01" },
      ];
  }
}
