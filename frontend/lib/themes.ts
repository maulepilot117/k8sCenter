// CLIENT-ONLY MODULE — Do NOT import in server-rendered components.
// This module uses browser APIs (document, localStorage) and Preact signals.

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
}

export interface Theme {
  id: string;
  name: string;
  colors: ThemeColors;
}

const nexus: Theme = {
  id: "nexus",
  name: "Nexus",
  colors: {
    bgBase: "#0B0E14",
    bgSurface: "#131720",
    bgElevated: "#1A1F2B",
    bgHover: "#212735",
    borderPrimary: "#252B37",
    borderSubtle: "#1E2330",
    textPrimary: "#E0E4EB",
    textSecondary: "#8B95A5",
    textMuted: "#5A6478",
    accent: "#00C2FF",
    accentGlow: "rgba(0,194,255,0.15)",
    accentDim: "rgba(0,194,255,0.08)",
    accentSecondary: "#7C5CFC",
    success: "#00E676",
    successDim: "rgba(0,230,118,0.12)",
    warning: "#FFB300",
    warningDim: "rgba(255,179,0,0.12)",
    error: "#FF5252",
    errorDim: "rgba(255,82,82,0.12)",
    info: "#40C4FF",
  },
};

const dracula: Theme = {
  id: "dracula",
  name: "Dracula",
  colors: {
    bgBase: "#282A36",
    bgSurface: "#2D2F3D",
    bgElevated: "#343746",
    bgHover: "#3E4155",
    borderPrimary: "#44475A",
    borderSubtle: "#383B4A",
    textPrimary: "#F8F8F2",
    textSecondary: "#C0C0D0",
    textMuted: "#6272A4",
    accent: "#BD93F9",
    accentGlow: "rgba(189,147,249,0.15)",
    accentDim: "rgba(189,147,249,0.08)",
    accentSecondary: "#FF79C6",
    success: "#50FA7B",
    successDim: "rgba(80,250,123,0.12)",
    warning: "#F1FA8C",
    warningDim: "rgba(241,250,140,0.12)",
    error: "#FF5555",
    errorDim: "rgba(255,85,85,0.12)",
    info: "#8BE9FD",
  },
};

const tokyoNight: Theme = {
  id: "tokyo-night",
  name: "Tokyo Night",
  colors: {
    bgBase: "#1A1B26",
    bgSurface: "#1E2030",
    bgElevated: "#24283B",
    bgHover: "#2A2E42",
    borderPrimary: "#29293D",
    borderSubtle: "#232338",
    textPrimary: "#A9B1D6",
    textSecondary: "#8890AB",
    textMuted: "#565F89",
    accent: "#7AA2F7",
    accentGlow: "rgba(122,162,247,0.15)",
    accentDim: "rgba(122,162,247,0.08)",
    accentSecondary: "#BB9AF7",
    success: "#9ECE6A",
    successDim: "rgba(158,206,106,0.12)",
    warning: "#E0AF68",
    warningDim: "rgba(224,175,104,0.12)",
    error: "#F7768E",
    errorDim: "rgba(247,118,142,0.12)",
    info: "#7DCFFF",
  },
};

const catppuccinMocha: Theme = {
  id: "catppuccin-mocha",
  name: "Catppuccin Mocha",
  colors: {
    bgBase: "#1E1E2E",
    bgSurface: "#24243A",
    bgElevated: "#313244",
    bgHover: "#3B3C50",
    borderPrimary: "#45475A",
    borderSubtle: "#383850",
    textPrimary: "#CDD6F4",
    textSecondary: "#BAC2DE",
    textMuted: "#6C7086",
    accent: "#89B4FA",
    accentGlow: "rgba(137,180,250,0.15)",
    accentDim: "rgba(137,180,250,0.08)",
    accentSecondary: "#CBA6F7",
    success: "#A6E3A1",
    successDim: "rgba(166,227,161,0.12)",
    warning: "#FAB387",
    warningDim: "rgba(250,179,135,0.12)",
    error: "#F38BA8",
    errorDim: "rgba(243,139,168,0.12)",
    info: "#89DCEB",
  },
};

const nord: Theme = {
  id: "nord",
  name: "Nord",
  colors: {
    bgBase: "#2E3440",
    bgSurface: "#333A47",
    bgElevated: "#3B4252",
    bgHover: "#434C5E",
    borderPrimary: "#4C566A",
    borderSubtle: "#434C5E",
    textPrimary: "#ECEFF4",
    textSecondary: "#D8DEE9",
    textMuted: "#7B88A1",
    accent: "#88C0D0",
    accentGlow: "rgba(136,192,208,0.15)",
    accentDim: "rgba(136,192,208,0.08)",
    accentSecondary: "#B48EAD",
    success: "#A3BE8C",
    successDim: "rgba(163,190,140,0.12)",
    warning: "#EBCB8B",
    warningDim: "rgba(235,203,139,0.12)",
    error: "#BF616A",
    errorDim: "rgba(191,97,106,0.12)",
    info: "#81A1C1",
  },
};

const oneDark: Theme = {
  id: "one-dark",
  name: "One Dark",
  colors: {
    bgBase: "#282C34",
    bgSurface: "#2C313A",
    bgElevated: "#333842",
    bgHover: "#3E4451",
    borderPrimary: "#3E4452",
    borderSubtle: "#353B45",
    textPrimary: "#ABB2BF",
    textSecondary: "#9DA5B4",
    textMuted: "#5C6370",
    accent: "#61AFEF",
    accentGlow: "rgba(97,175,239,0.15)",
    accentDim: "rgba(97,175,239,0.08)",
    accentSecondary: "#C678DD",
    success: "#98C379",
    successDim: "rgba(152,195,121,0.12)",
    warning: "#E5C07B",
    warningDim: "rgba(229,192,123,0.12)",
    error: "#E06C75",
    errorDim: "rgba(224,108,117,0.12)",
    info: "#56B6C2",
  },
};

const gruvbox: Theme = {
  id: "gruvbox",
  name: "Gruvbox",
  colors: {
    bgBase: "#1D2021",
    bgSurface: "#282828",
    bgElevated: "#3C3836",
    bgHover: "#504945",
    borderPrimary: "#504945",
    borderSubtle: "#3C3836",
    textPrimary: "#EBDBB2",
    textSecondary: "#D5C4A1",
    textMuted: "#928374",
    accent: "#83A598",
    accentGlow: "rgba(131,165,152,0.15)",
    accentDim: "rgba(131,165,152,0.08)",
    accentSecondary: "#D3869B",
    success: "#B8BB26",
    successDim: "rgba(184,187,38,0.12)",
    warning: "#FABD2F",
    warningDim: "rgba(250,189,47,0.12)",
    error: "#FB4934",
    errorDim: "rgba(251,73,52,0.12)",
    info: "#8EC07C",
  },
};

export const THEMES: Theme[] = [
  nexus,
  dracula,
  tokyoNight,
  catppuccinMocha,
  nord,
  oneDark,
  gruvbox,
];

const STORAGE_KEY = "k8scenter-theme";
const DEFAULT_THEME = "nexus";

export const currentTheme = signal<string>(DEFAULT_THEME);

export function getTheme(id?: string): Theme {
  const themeId = id ?? currentTheme.value;
  return THEMES.find((t) => t.id === themeId) ?? nexus;
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
