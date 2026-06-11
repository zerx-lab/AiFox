// First-paint theme bootstrap. Classic (non-module) script loaded
// synchronously from index.html <head> so it runs before the first frame,
// unlike the deferred renderer module. The localStorage key is a mirror of
// the Go-side theme setting, written by ui/theme.ts; the setting itself
// stays authoritative in the backend. When the user follows the system
// theme the key is absent and styles.css's prefers-color-scheme fallback
// applies instead.
try {
  const t = localStorage.getItem("aifox-theme");
  if (t === "dark" || t === "light") {
    document.documentElement.dataset.theme = t;
  }
} catch {
  // localStorage unavailable — fall through to the CSS fallback.
}
