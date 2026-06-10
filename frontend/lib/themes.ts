// CLIENT-ONLY MODULE — Do NOT import in server-rendered components.
// This module uses browser APIs (document, localStorage) and Preact signals.
//
// COLOR TOKENS: kept in lockstep with shared/themes/*.json (the canonical
// source consumed by tools/theme-gen/main.ts). When you edit a theme,
// change the JSON file under shared/themes/ and run `make check-themes` —
// the same tokens are re-emitted into frontend/assets/themes.generated.css
// and mobile/lib/theme/themes.g.dart, and CI fails on drift.

import { signal } from "@preact/signals";

export interface ThemeColors {
  bgBase: string;
  bgSurface: string;
  bgElevated: string;
  bgHover: string;
  borderPrimary: string;
  borderSubtle: string;
  textPrimary: string;
  textSecondary: string;
  textMuted: string;
  accent: string;
  accentGlow: string;
  accentDim: string;
  accentSecondary: string;
  success: string;
  successDim: string;
  warning: string;
  warningDim: string;
  error: string;
  errorDim: string;
  info: string;
  glassSurface: string;
  glassElevated: string;
  glassBorder: string;
  glassHighlight: string;
  glassScrim: string;
}

export interface Theme {
  id: string;
  name: string;
  colors: ThemeColors;
}

// SYNC: bgBase values are duplicated in routes/_app.tsx (inline script) and assets/styles.css
const liquidGlass: Theme = {
  id: "liquid-glass",
  name: "Liquid Glass",
  colors: {
    bgBase: "#05080F",
    bgSurface: "#0D1422",
    bgElevated: "#141C2E",
    bgHover: "#1C2640",
    borderPrimary: "#263350",
    borderSubtle: "#1A2338",
    textPrimary: "#EDF2FB",
    textSecondary: "#97A4C0",
    textMuted: "#66738F",
    accent: "#3DAEFF",
    accentGlow: "rgba(61,174,255,0.16)",
    accentDim: "rgba(61,174,255,0.09)",
    accentSecondary: "#8E7BFF",
    success: "#34D399",
    successDim: "rgba(52,211,153,0.12)",
    warning: "#FBBF24",
    warningDim: "rgba(251,191,36,0.12)",
    error: "#FB7185",
    errorDim: "rgba(251,113,133,0.12)",
    info: "#7DD3FC",
    glassSurface: "rgba(15,21,36,0.55)",
    glassElevated: "rgba(22,30,49,0.66)",
    glassBorder: "rgba(151,180,228,0.16)",
    glassHighlight: "rgba(255,255,255,0.07)",
    glassScrim: "rgba(3,6,12,0.50)",
  },
};

export const THEMES: Theme[] = [
  liquidGlass,
];

const STORAGE_KEY = "k8scenter-theme";
const DEFAULT_THEME = "liquid-glass";

export const currentTheme = signal<string>(DEFAULT_THEME);

export function getTheme(id?: string): Theme {
  const themeId = id ?? currentTheme.value;
  return THEMES.find((t) => t.id === themeId) ?? liquidGlass;
}

const CSS_VAR_MAP: Record<keyof ThemeColors, string> = {
  bgBase: "--bg-base",
  bgSurface: "--bg-surface",
  bgElevated: "--bg-elevated",
  bgHover: "--bg-hover",
  borderPrimary: "--border-primary",
  borderSubtle: "--border-subtle",
  textPrimary: "--text-primary",
  textSecondary: "--text-secondary",
  textMuted: "--text-muted",
  accent: "--accent",
  accentGlow: "--accent-glow",
  accentDim: "--accent-dim",
  accentSecondary: "--accent-secondary",
  success: "--success",
  successDim: "--success-dim",
  warning: "--warning",
  warningDim: "--warning-dim",
  error: "--error",
  errorDim: "--error-dim",
  info: "--info",
  glassSurface: "--glass-surface",
  glassElevated: "--glass-elevated",
  glassBorder: "--glass-border",
  glassHighlight: "--glass-highlight",
  glassScrim: "--glass-scrim",
};

export function applyTheme(themeId: string): void {
  const theme = getTheme(themeId);
  const style = document.documentElement.style;

  for (const [key, cssVar] of Object.entries(CSS_VAR_MAP)) {
    style.setProperty(cssVar, theme.colors[key as keyof ThemeColors]);
  }

  document.documentElement.dataset.theme = theme.id;
  currentTheme.value = theme.id;

  try {
    localStorage.setItem(STORAGE_KEY, theme.id);
  } catch {
    // localStorage may be unavailable
  }
}

export function initTheme(): void {
  let themeId = DEFAULT_THEME;
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored && THEMES.some((t) => t.id === stored)) {
      themeId = stored;
    }
  } catch {
    // localStorage may be unavailable
  }
  applyTheme(themeId);
}
