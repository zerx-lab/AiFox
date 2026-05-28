// Custom titlebar that adapts per platform.
//
//   macOS  — native traffic lights live in the top-left (Electron's
//            `hiddenInset` style). We only render a draggable spacer that
//            preserves room for them and shows the app title centered.
//   Win/Linux — no native chrome, so we draw min / max / close buttons on
//            the right. The whole bar is draggable via -webkit-app-region.
//
// Buttons sit outside the drag region (`no-drag`) so clicks reach them.

import { t } from "../i18n";
import { h } from "./dom";
import { getState } from "./state";

export function renderTitlebar(): HTMLElement {
  const isMac = getState().env?.platform === "darwin";
  const maximized = getState().windowMaximized;

  if (isMac) {
    return h(
      "div.titlebar.mac",
      null,
      h("div.titlebar-spacer-traffic"),
      h(
        "div.titlebar-drag",
        null,
        h("span.titlebar-title", null, t("app.title")),
      ),
    );
  }

  return h(
    "div.titlebar",
    null,
    h(
      "div.titlebar-drag",
      null,
      h("span.titlebar-brand-mark"),
      h("span.titlebar-title", null, t("app.title")),
    ),
    h(
      "div.titlebar-actions",
      null,
      button("min", t("titlebar.minimize"), minimizeIcon(), () => {
        void window.aiFox.window.minimize();
      }),
      button(
        "max",
        maximized ? t("titlebar.restore") : t("titlebar.maximize"),
        maximized ? restoreIcon() : maximizeIcon(),
        () => {
          void window.aiFox.window.maximizeToggle();
        },
      ),
      button("close", t("titlebar.close"), closeIcon(), () => {
        void window.aiFox.window.close();
      }),
    ),
  );
}

function button(
  variant: "min" | "max" | "close",
  label: string,
  svg: SVGElement,
  onclick: () => void,
): HTMLElement {
  const btn = h(
    "button",
    {
      class: `titlebar-btn ${variant}`,
      title: label,
      "aria-label": label,
      onclick,
    },
  );
  btn.appendChild(svg);
  return btn;
}

function minimizeIcon(): SVGElement {
  return iconSvg('<path d="M2 6 H10" stroke="currentColor" stroke-width="1.2" fill="none" />');
}
function maximizeIcon(): SVGElement {
  return iconSvg(
    '<rect x="2" y="2" width="8" height="8" stroke="currentColor" stroke-width="1.2" fill="none" />',
  );
}
function restoreIcon(): SVGElement {
  return iconSvg(
    '<rect x="2" y="4" width="6" height="6" stroke="currentColor" stroke-width="1.2" fill="none" />' +
      '<path d="M4 4 V2 H10 V8 H8" stroke="currentColor" stroke-width="1.2" fill="none" />',
  );
}
function closeIcon(): SVGElement {
  return iconSvg(
    '<path d="M2 2 L10 10 M10 2 L2 10" stroke="currentColor" stroke-width="1.2" fill="none" />',
  );
}

function iconSvg(inner: string): SVGElement {
  const wrap = document.createElement("div");
  wrap.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 12 12">${inner}</svg>`;
  return wrap.firstElementChild as SVGElement;
}
