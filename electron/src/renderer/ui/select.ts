// Custom popover-style select. Replaces native <select> so the UI doesn't
// look like a generic web form. Used by titlebar (theme/language toggles),
// settings page, and the filter bar.

import { h } from "./dom";

export interface SelectOption {
  value: string;
  label: string;
  icon?: Node | null;
}

export interface SelectOpts {
  value: string;
  options: SelectOption[];
  onChange: (v: string) => void | Promise<void>;
  /** Render only an icon in the trigger; the menu still shows full labels. */
  iconOnly?: boolean;
  /** Title / aria-label for icon-only triggers. */
  ariaLabel?: string;
  /** Extra class on the trigger button. */
  className?: string;
  /** Override the icon in the trigger when iconOnly = true. */
  triggerIcon?: Node | null;
  /** Open above the trigger instead of below (e.g. footer-anchored menus). */
  placement?: "below" | "above";
  /** Align the menu's right edge to the trigger's right edge. */
  align?: "start" | "end";
  /** Class on the popover menu. */
  menuClassName?: string;
}

export function customSelect(opts: SelectOpts): HTMLElement {
  const root = h("div.cselect");
  if (opts.placement === "above") root.classList.add("place-above");
  if (opts.align === "end") root.classList.add("align-end");

  const current = opts.options.find((o) => o.value === opts.value);
  const triggerLabel = current?.label ?? opts.value;

  const trigger = h("button", {
    type: "button",
    class: `cselect-trigger${opts.iconOnly ? " icon-only" : ""}${opts.className ? ` ${opts.className}` : ""}`,
    title: opts.ariaLabel ?? triggerLabel,
    "aria-label": opts.ariaLabel ?? triggerLabel,
    "aria-haspopup": "listbox",
    "aria-expanded": "false",
  });
  if (opts.iconOnly) {
    const icon = opts.triggerIcon ?? current?.icon ?? null;
    if (icon) trigger.appendChild(icon);
  } else {
    trigger.appendChild(h("span.cselect-text", null, triggerLabel));
    trigger.appendChild(chevron());
  }
  root.appendChild(trigger);

  const menu = h("div", {
    class: `cselect-menu${opts.menuClassName ? ` ${opts.menuClassName}` : ""}`,
    role: "listbox",
    "aria-hidden": "true",
  });
  for (const opt of opts.options) {
    const selected = opt.value === opts.value;
    const item = h(
      "button",
      {
        type: "button",
        class: `cselect-item${selected ? " selected" : ""}`,
        role: "option",
        "aria-selected": selected ? "true" : "false",
        onclick: (e: Event) => {
          e.stopPropagation();
          close();
          void opts.onChange(opt.value);
        },
      },
      opt.icon ?? null,
      h("span.cselect-label", null, opt.label),
      selected ? checkMark() : null,
    );
    menu.appendChild(item);
  }
  root.appendChild(menu);

  let open = false;
  let docCtrl: AbortController | null = null;

  const close = () => setOpen(false);

  const onDocMouseDown = (e: MouseEvent) => {
    // If our root was removed from the DOM, abort and clean up.
    if (!root.isConnected) {
      docCtrl?.abort();
      docCtrl = null;
      open = false;
      return;
    }
    if (!root.contains(e.target as Node)) close();
  };
  const onKey = (e: KeyboardEvent) => {
    // If our root was removed from the DOM, abort and clean up.
    if (!root.isConnected) {
      docCtrl?.abort();
      docCtrl = null;
      open = false;
      return;
    }
    if (e.key === "Escape") {
      e.stopPropagation();
      close();
    }
  };

  const setOpen = (v: boolean) => {
    if (v === open) return;
    open = v;
    root.classList.toggle("open", v);
    trigger.setAttribute("aria-expanded", v ? "true" : "false");
    menu.setAttribute("aria-hidden", v ? "false" : "true");
    if (v) {
      // Each open creates a fresh AbortController so multiple instances never
      // share signal state and cleanup is always paired with an open.
      docCtrl = new AbortController();
      const { signal } = docCtrl;
      document.addEventListener("mousedown", onDocMouseDown, { capture: true, signal });
      document.addEventListener("keydown", onKey, { capture: true, signal });
    } else {
      docCtrl?.abort();
      docCtrl = null;
    }
  };

  trigger.addEventListener("click", (e) => {
    e.stopPropagation();
    setOpen(!open);
  });

  return root;
}

function chevron(): SVGElement {
  return svg(
    '<path d="M2 4 L5 7 L8 4" stroke="currentColor" stroke-width="1.2" fill="none" stroke-linecap="round" stroke-linejoin="round"/>',
    10,
    "cselect-chevron",
  );
}

function checkMark(): SVGElement {
  return svg(
    '<path d="M2 5 L4.5 7.5 L8 3.5" stroke="currentColor" stroke-width="1.4" fill="none" stroke-linecap="round" stroke-linejoin="round"/>',
    10,
    "cselect-check",
  );
}

function svg(inner: string, size: number, cls: string): SVGElement {
  const wrap = document.createElement("div");
  wrap.innerHTML = `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 ${size} ${size}" class="${cls}">${inner}</svg>`;
  return wrap.firstElementChild as SVGElement;
}
