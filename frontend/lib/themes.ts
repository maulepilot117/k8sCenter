// CLIENT-ONLY MODULE — Do NOT import in server-rendered components.
//
// Liquid Glass is the single design language. All color tokens live in
// shared/themes/liquid-glass.json and reach the browser as static :root
// CSS variables via frontend/assets/themes.generated.css (regenerate with
// `deno task theme-gen`; CI enforces parity via `make check-themes`).
//
// There is no runtime theme switching. This module only clears the
// persisted theme id left behind by the retired multi-theme system so a
// future theme feature starts from a clean slate. If multi-theme support
// returns, restore applyTheme/currentTheme from git history (pre
// liquid-glass redesign) alongside per-theme [data-theme] CSS blocks.

const LEGACY_STORAGE_KEY = "k8scenter-theme";

export function initTheme(): void {
  try {
    localStorage.removeItem(LEGACY_STORAGE_KEY);
  } catch {
    // localStorage may be unavailable
  }
}
