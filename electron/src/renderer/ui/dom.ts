// Tiny createElement helper so component code stays declarative without React.
// `h('div.foo#bar', { onclick: fn }, child1, child2)`

type Attrs = Record<string, unknown> & {
  class?: string;
  className?: string;
  style?: Partial<CSSStyleDeclaration> | string;
  dataset?: Record<string, string>;
};
type Child = Node | string | number | false | null | undefined;

export function h<K extends keyof HTMLElementTagNameMap>(
  spec: K | string,
  attrs?: Attrs | null,
  ...children: Child[]
): HTMLElement {
  const { tag, id, classes } = parseSpec(spec);
  const el = document.createElement(tag);
  if (id) el.id = id;
  for (const c of classes) el.classList.add(c);
  if (attrs) applyAttrs(el, attrs);
  for (const c of children) appendChild(el, c);
  return el;
}

function parseSpec(spec: string): { tag: string; id: string | null; classes: string[] } {
  const idx = spec.search(/[#.\s]/);
  const tag = idx === -1 ? spec : spec.slice(0, idx);
  const rest = idx === -1 ? "" : spec.slice(idx);
  let id: string | null = null;
  const classes: string[] = [];
  // Also split on whitespace so "div.foo bar" yields ["foo", "bar"] instead
  // of one class token containing a space — classList.add would throw
  // InvalidCharacterError on the latter and kill the entire render path.
  for (const part of rest.split(/(?=[.#])|\s+/g)) {
    if (!part) continue;
    if (part[0] === "#") id = part.slice(1);
    else if (part[0] === ".") classes.push(part.slice(1));
    else classes.push(part);
  }
  return { tag: tag || "div", id, classes };
}

function applyAttrs(el: HTMLElement, attrs: Attrs) {
  for (const [key, value] of Object.entries(attrs)) {
    if (value === undefined || value === null || value === false) continue;
    if (key === "class" || key === "className") {
      for (const c of String(value).split(/\s+/)) if (c) el.classList.add(c);
    } else if (key === "style") {
      if (typeof value === "string") el.setAttribute("style", value);
      else Object.assign(el.style, value as Partial<CSSStyleDeclaration>);
    } else if (key === "dataset") {
      for (const [k, v] of Object.entries(value as Record<string, string>)) {
        el.dataset[k] = v;
      }
    } else if (key.startsWith("on") && typeof value === "function") {
      el.addEventListener(key.slice(2).toLowerCase(), value as EventListener);
    } else if (typeof value === "boolean") {
      if (value) el.setAttribute(key, "");
    } else {
      el.setAttribute(key, String(value));
    }
  }
}

function appendChild(parent: HTMLElement, child: Child) {
  if (child === null || child === undefined || child === false) return;
  if (child instanceof Node) parent.appendChild(child);
  else parent.appendChild(document.createTextNode(String(child)));
}

export function clear(el: HTMLElement) {
  while (el.firstChild) el.removeChild(el.firstChild);
}

export function mount(parent: HTMLElement, node: HTMLElement) {
  clear(parent);
  parent.appendChild(node);
}
