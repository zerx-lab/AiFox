// Tiny syntax highlighter for the body codebox. No dependencies — a single
// regex tokenizes JSON and SSE is walked line-by-line. Output is a
// DocumentFragment of `<span class="tok-...">` so the renderer can paint
// each token through CSS tokens that already follow the theme.

export type Kind = "json" | "sse" | "raw";

export function highlight(text: string, kind: Kind): DocumentFragment {
  if (kind === "json") return highlightJson(text);
  if (kind === "sse") return highlightSse(text);
  const frag = document.createDocumentFragment();
  frag.appendChild(document.createTextNode(text));
  return frag;
}

// --- JSON ---------------------------------------------------------------

// Order matters: longest-leftmost wins. String first so `:` inside a string
// doesn't become punctuation. The trailing `.` is a catch-all for stray
// bytes (e.g. body that lied about being JSON).
const JSON_TOKEN =
  /"(?:\\.|[^"\\])*"|(?:true|false|null)(?![A-Za-z0-9_])|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|[{}[\],:]|\s+|./g;

export function highlightJson(text: string): DocumentFragment {
  const frag = document.createDocumentFragment();
  JSON_TOKEN.lastIndex = 0;
  let m: RegExpExecArray | null = JSON_TOKEN.exec(text);
  while (m !== null) {
    const tok = m[0];
    if (tok[0] === '"') {
      // A string is a key iff its first non-whitespace successor is ':'.
      const after = text.slice(m.index + tok.length);
      const isKey = /^\s*:/.test(after);
      frag.appendChild(span(tok, isKey ? "tok-key" : "tok-string"));
    } else if (tok === "true" || tok === "false") {
      frag.appendChild(span(tok, "tok-bool"));
    } else if (tok === "null") {
      frag.appendChild(span(tok, "tok-null"));
    } else if (tok.length > 0 && /^-?\d/.test(tok)) {
      frag.appendChild(span(tok, "tok-num"));
    } else if (tok.length === 1 && "{}[],:".includes(tok)) {
      frag.appendChild(span(tok, "tok-punct"));
    } else {
      // whitespace + any unmatched character — passthrough as text so
      // newlines and indentation survive.
      frag.appendChild(document.createTextNode(tok));
    }
    m = JSON_TOKEN.exec(text);
  }
  return frag;
}

// --- SSE ----------------------------------------------------------------
//
// formatBody() already pretty-printed each `data:` line by promoting it to
// a bare `data:` followed by 2-space-indented JSON. We exploit that here:
// when we see a standalone `data:` line, we consume the indented block
// underneath and run highlightJson on it (leading indent passes through
// as whitespace, so the tokens still line up).

export function highlightSse(text: string): DocumentFragment {
  const frag = document.createDocumentFragment();
  const lines = text.split("\n");
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] ?? "";
    const lastLine = i === lines.length - 1;

    if (line === "") {
      if (!lastLine) frag.appendChild(document.createTextNode("\n"));
      continue;
    }

    // SSE comment line (starts with ':') — render dim.
    if (line.startsWith(":")) {
      frag.appendChild(span(line, "tok-comment"));
      if (!lastLine) frag.appendChild(document.createTextNode("\n"));
      continue;
    }

    const colon = line.indexOf(":");
    if (colon === -1) {
      frag.appendChild(document.createTextNode(line));
      if (!lastLine) frag.appendChild(document.createTextNode("\n"));
      continue;
    }
    const field = line.slice(0, colon);
    const rest = line.slice(colon + 1);

    // Standalone "data:" preceding an indented JSON block — highlight the
    // block as JSON in one go.
    if (field === "data" && rest === "") {
      frag.appendChild(span("data", "tok-sse-field"));
      frag.appendChild(span(":", "tok-punct"));
      frag.appendChild(document.createTextNode("\n"));
      const block: string[] = [];
      while (i + 1 < lines.length && (lines[i + 1] ?? "").startsWith("  ")) {
        i++;
        block.push(lines[i] ?? "");
      }
      if (block.length > 0) {
        frag.appendChild(highlightJson(block.join("\n")));
      }
      if (!(i === lines.length - 1)) frag.appendChild(document.createTextNode("\n"));
      continue;
    }

    // Regular "field: value" line.
    frag.appendChild(span(field, "tok-sse-field"));
    frag.appendChild(span(":", "tok-punct"));
    frag.appendChild(document.createTextNode(rest));
    if (!lastLine) frag.appendChild(document.createTextNode("\n"));
  }
  return frag;
}

function span(text: string, cls: string): HTMLSpanElement {
  const s = document.createElement("span");
  s.className = cls;
  s.textContent = text;
  return s;
}
