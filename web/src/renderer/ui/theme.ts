// Applies the requested color scheme to <html data-theme="...">. The
// "system" choice resolves to the OS dark-mode preference reported by the
// Electron main process (nativeTheme.shouldUseDarkColors) and stays in sync
// when the OS toggles dark mode at runtime.

export type ThemeChoice = "" | "dark" | "light";

let userChoice: ThemeChoice = "";
let osIsDark = false;
let initialized = false;

export function setTheme(choice: ThemeChoice) {
  userChoice = normalize(choice);
  apply();
}

export function getThemeChoice(): ThemeChoice {
  return userChoice;
}

/** Concrete theme actually painted on the DOM right now. */
export function resolvedTheme(): "dark" | "light" {
  if (userChoice === "dark" || userChoice === "light") return userChoice;
  return osIsDark ? "dark" : "light";
}

/** Wire up the OS-theme listener exactly once. Safe to call before the
 *  bridge is ready — the initial probe is async and best-effort. */
export async function initTheme() {
  if (initialized) return;
  initialized = true;
  try {
    const probe = await window.aiFox.theme.native();
    osIsDark = probe.shouldUseDarkColors;
  } catch {
    osIsDark = window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? true;
  }
  window.aiFox.theme.onNativeChanged((dark) => {
    osIsDark = dark;
    if (userChoice === "") apply();
  });
  apply();
}

function apply() {
  document.documentElement.dataset.theme = resolvedTheme();
  // Mirror the explicit choice for public/theme-boot.js's first-paint pass.
  // "System" clears the mirror so the CSS prefers-color-scheme fallback
  // (always current) decides the first frame instead of a stale snapshot.
  try {
    if (userChoice === "") {
      localStorage.removeItem("aifox-theme");
    } else {
      localStorage.setItem("aifox-theme", userChoice);
    }
  } catch {
    // Best effort only; the CSS fallback still covers the first paint.
  }
}

function normalize(c: string): ThemeChoice {
  return c === "dark" || c === "light" ? c : "";
}
