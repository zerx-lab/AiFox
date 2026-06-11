// Custom popover-style select. Replaces native <select> so the UI doesn't
// look like a generic web form. Used by titlebar (theme/language toggles),
// settings page, and the filter bar.

import { registerDispose } from "./app";
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
  // Item elements in option order, so arrow-key navigation can move a visual
  // "active" highlight without committing the selection until Enter (§4.1.6).
  const items: HTMLElement[] = [];
  opts.options.forEach((opt) => {
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
    items.push(item);
    menu.appendChild(item);
  });
  root.appendChild(menu);

  // active is the keyboard-highlighted index while the menu is open (-1 = none).
  let active = -1;
  const setActive = (i: number) => {
    if (items[active]) items[active]!.classList.remove("active");
    active = i;
    const el = items[active];
    if (el) {
      el.classList.add("active");
      el.scrollIntoView({ block: "nearest" });
    }
  };

  let open = false;
  let docCtrl: AbortController | null = null;

  // Region dispose protocol: when the enclosing region is rebuilt/discarded,
  // app.ts walks the outgoing subtree and runs this, aborting any open menu's
  // document-level listeners. This replaces the old reliance on the next
  // document event noticing root.isConnected === false (which never fired if no
  // further document event arrived — the F4 leak). The isConnected guards below
  // stay as a belt-and-suspenders fallback.
  registerDispose(root, () => {
    docCtrl?.abort();
    docCtrl = null;
    open = false;
  });

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
    if (!open) return;
    switch (e.key) {
      case "Escape":
        e.stopPropagation();
        close();
        trigger.focus();
        break;
      case "ArrowDown":
        e.preventDefault();
        e.stopPropagation();
        setActive(active < items.length - 1 ? active + 1 : 0);
        break;
      case "ArrowUp":
        e.preventDefault();
        e.stopPropagation();
        setActive(active > 0 ? active - 1 : items.length - 1);
        break;
      case "Home":
        e.preventDefault();
        e.stopPropagation();
        setActive(0);
        break;
      case "End":
        e.preventDefault();
        e.stopPropagation();
        setActive(items.length - 1);
        break;
      case "Enter":
      case " ": {
        if (active < 0) return;
        e.preventDefault();
        e.stopPropagation();
        const opt = opts.options[active];
        close();
        trigger.focus();
        if (opt) void opts.onChange(opt.value);
        break;
      }
    }
  };

  const setOpen = (v: boolean) => {
    if (v === open) return;
    open = v;
    root.classList.toggle("open", v);
    trigger.setAttribute("aria-expanded", v ? "true" : "false");
    menu.setAttribute("aria-hidden", v ? "false" : "true");
    if (v) {
      // Highlight the currently-selected option (or the first) on open so arrow
      // keys have a sensible starting point.
      const selIdx = opts.options.findIndex((o) => o.value === opts.value);
      setActive(selIdx >= 0 ? selIdx : 0);
      // Each open creates a fresh AbortController so multiple instances never
      // share signal state and cleanup is always paired with an open.
      docCtrl = new AbortController();
      const { signal } = docCtrl;
      document.addEventListener("mousedown", onDocMouseDown, { capture: true, signal });
      document.addEventListener("keydown", onKey, { capture: true, signal });
    } else {
      if (items[active]) items[active]!.classList.remove("active");
      active = -1;
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
