// Global toast / banner channel (§4.1.7). A lightweight bottom-right stack of
// transient notices: errors red, successes green. Each toast auto-dismisses
// after a timeout and can be closed manually. The container is a single
// document-level host mounted lazily on first use, OUTSIDE the region-render
// tree, so a notice never gets torn down by an unrelated re-render and shows up
// regardless of which view is mounted.

import { t } from "../i18n";
import { h } from "./dom";

export type ToastKind = "error" | "success" | "info";

const AUTO_DISMISS_MS = 5_000;
const SUCCESS_DISMISS_MS = 3_000;

let host: HTMLElement | null = null;

function ensureHost(): HTMLElement {
  if (host?.isConnected) return host;
  host = h("div.toast-host", { role: "status", "aria-live": "polite" });
  document.body.appendChild(host);
  return host;
}

export function showToast(message: string, kind: ToastKind = "info"): void {
  const root = ensureHost();
  let timer: number | null = null;

  const dismiss = () => {
    if (timer !== null) {
      window.clearTimeout(timer);
      timer = null;
    }
    el.classList.add("toast-leaving");
    // Remove after the CSS leave transition; fall back to immediate removal if
    // the transition never fires (e.g. reduced-motion).
    window.setTimeout(() => el.remove(), 200);
  };

  const closeBtn = h(
    "button.toast-close",
    {
      type: "button",
      title: t("toast.dismiss"),
      "aria-label": t("toast.dismiss"),
      onclick: dismiss,
    },
    "×",
  );

  const el = h(
    "div",
    { class: `toast toast-${kind}` },
    h("span.toast-msg", null, message),
    closeBtn,
  );

  root.appendChild(el);

  const ttl = kind === "success" ? SUCCESS_DISMISS_MS : AUTO_DISMISS_MS;
  timer = window.setTimeout(dismiss, ttl);
}

export const toastError = (message: string) => showToast(message, "error");
export const toastSuccess = (message: string) => showToast(message, "success");
