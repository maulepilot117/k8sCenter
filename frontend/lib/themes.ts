// CLIENT-ONLY MODULE — Do NOT import in server-rendered components.
//
// Liquid Glass is the single design language. All color tokens live in
// shared/themes/liquid-glass.json and reach the browser as static :root
// CSS variables via frontend/assets/themes.generated.css (regenerate with
// `deno task theme-gen`; CI enforces parity via `make check-themes`).
//
// Light/dark mode is owned by lib/theme.ts (applyTheme / toggleTheme).
// This module only clears the persisted theme id left behind by the retired
// multi-theme system and then delegates to applyTheme() so the persisted
// light/dark choice is applied at mount. Do NOT add a second theme-class
// writer here — lib/theme.ts is the single runtime owner.
import { applyTheme } from "@/lib/theme.ts";

const LEGACY_STORAGE_KEY = "k8scenter-theme";

export function initTheme(): void {
  try {
    localStorage.removeItem(LEGACY_STORAGE_KEY);
  } catch {
    // localStorage may be unavailable
  }
  // Apply the persisted light/dark class. lib/theme.ts is the single owner.
  applyTheme();
}
