# k8sCenter Phase 6: Frontend Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Transform k8sCenter from a traditional sidebar + resource-list UI into a modern, dashboard-first interface with icon rail navigation, multi-theme support, command palette, cluster topology visualization, health scoring, split-pane views, and quick actions — all while preserving every existing feature.

**Architecture:** The redesign layers a new design system (CSS custom properties for 7 named themes) on top of the existing Fresh 2.x / Preact / Tailwind v4 stack. Navigation changes from a 240px sidebar with 40+ items to a 56px icon rail with 8 domain icons, each opening a dashboard landing page with sub-navigation tabs for drill-down. New islands (CommandPalette, ClusterTopology, HealthScore, QuickActions, SplitPane) are added alongside migrated existing islands. All existing routes, API integrations, WebSocket subscriptions, wizards, and RBAC checks are preserved.

**Tech Stack:** Deno 2.x, Fresh 2.x (Preact), Tailwind v4, Geist fonts (Google Fonts), @preact/signals, d3-force (topology graph), fuse.js (fuzzy search)

**Mockups:** `mockups/01-overview-dashboard.html`, `mockups/02-workloads-dashboard.html`, `mockups/03-split-pane-detail.html`

---

## File Structure

### New Files

```
frontend/
├── lib/
│   ├── themes.ts                    # Theme definitions (7 themes), CSS variable injection, persistence
│   ├── health-score.ts              # Health score calculation algorithm
│   ├── fuzzy-search.ts              # Command palette search index + fuzzy matching
│   ├── animation-prefs.ts           # Animation preference signal + persistence
│   └── hooks/
│       └── use-split-pane.ts        # Split pane resize hook
│
├── islands/
│   ├── IconRail.tsx                 # 56px icon rail navigation (replaces Sidebar)
│   ├── TopBarV2.tsx                 # Redesigned top bar with search trigger
│   ├── CommandPalette.tsx           # Cmd+K fuzzy search overlay
│   ├── ClusterTopology.tsx          # Force-directed cluster graph (d3-force)
│   ├── HealthScoreRing.tsx          # Circular health score with sub-scores
│   ├── QuickActionsFab.tsx          # Floating action button with menu
│   ├── SplitPane.tsx                # Resizable split-pane container
│   ├── DashboardV2.tsx              # New overview dashboard
│   ├── WorkloadsDashboard.tsx       # Workloads domain dashboard
│   ├── NetworkDashboard.tsx         # Network domain dashboard
│   ├── StorageDashboard.tsx         # Storage domain dashboard
│   ├── ConfigDashboard.tsx          # Config domain dashboard
│   ├── SecurityDashboard.tsx        # Security domain dashboard
│   ├── ObservabilityDashboard.tsx   # Observability domain dashboard
│   ├── SubNav.tsx                   # Sub-navigation tabs within domains
│   ├── MetricCard.tsx               # Reusable metric card with sparkline
│   ├── UtilizationGauge.tsx         # SVG gauge with trend chart
│   ├── ResourceTableV2.tsx          # Redesigned resource table
│   ├── ResourceDetailV2.tsx         # Redesigned detail view (split-pane ready)
│   ├── ThemeSelector.tsx            # Theme picker dropdown
│   └── AnimationToggle.tsx          # Animation preference toggle
│
├── components/
│   └── ui/
│       ├── StatusDot.tsx            # Animated status indicator dot
│       ├── SparklineChart.tsx       # Tiny inline SVG chart
│       ├── GaugeRing.tsx            # Reusable SVG ring gauge
│       ├── FilterChip.tsx           # Pill-shaped filter toggle
│       └── SummaryRing.tsx          # Small summary ring for strips
│
├── assets/
│   └── styles.css                   # REWRITTEN: Theme CSS variables + Tailwind v4
│
├── routes/
│   ├── _app.tsx                     # MODIFIED: Geist fonts, theme class injection
│   ├── _layout.tsx                  # MODIFIED: IconRail + TopBarV2 + new layout
│   ├── index.tsx                    # MODIFIED: DashboardV2 instead of Dashboard
│   ├── workloads/
│   │   └── index.tsx                # NEW: Workloads domain dashboard
│   ├── network/
│   │   └── index.tsx                # NEW: Network domain dashboard
│   ├── storage/
│   │   └── index.tsx                # MODIFIED: StorageDashboard
│   ├── config/
│   │   └── index.tsx                # NEW: Config domain dashboard
│   ├── security/
│   │   └── index.tsx                # NEW: Security domain dashboard
│   ├── observability/
│   │   └── index.tsx                # NEW: Observability domain dashboard
│   └── tools/
│       └── index.tsx                # NEW: Tools domain dashboard
│
└── static/
    └── fonts/                       # Geist font files (self-hosted for CSP)
```

### Modified Files

```
frontend/
├── lib/
│   ├── constants.ts                 # Add DOMAIN_SECTIONS (new nav), keep NAV_SECTIONS for backward compat
│   └── status-colors.ts             # Add theme-aware variants using CSS variables
│
├── islands/
│   ├── Sidebar.tsx                  # DEPRECATED — keep file, re-export IconRail for transition
│   ├── TopBar.tsx                   # DEPRECATED — keep file, re-export TopBarV2
│   ├── Dashboard.tsx                # DEPRECATED — keep file, re-export DashboardV2
│   ├── KeyboardShortcuts.tsx        # MODIFIED: Merge Cmd+K handling into CommandPalette
│   └── ToastProvider.tsx            # MODIFIED: Restyle with new theme variables
│
├── components/
│   └── ui/
│       ├── Button.tsx               # MODIFIED: Use CSS variables for theming
│       ├── Card.tsx                 # MODIFIED: Use CSS variables, add glow effect
│       ├── StatusBadge.tsx          # MODIFIED: Use CSS variables
│       ├── Tabs.tsx                 # MODIFIED: Use CSS variables, new active style
│       ├── SearchBar.tsx            # MODIFIED: Use CSS variables
│       ├── Skeleton.tsx             # MODIFIED: Use CSS variables
│       ├── Alert.tsx                # MODIFIED: Use CSS variables
│       └── Breadcrumb.tsx           # MODIFIED: Use CSS variables
│
├── main.ts                          # MODIFIED: Add Google Fonts to CSP connect-src (or self-host)
├── deno.json                        # MODIFIED: Add fuse.js, d3-force dependencies
└── assets/styles.css                # REWRITTEN: Full theme system
```

---

## Task 1: Theme System Foundation

**Files:**
- Create: `frontend/lib/themes.ts`
- Create: `frontend/lib/animation-prefs.ts`
- Rewrite: `frontend/assets/styles.css`
- Modify: `frontend/routes/_app.tsx`
- Modify: `frontend/deno.json`

This is the foundation everything else builds on. All 7 themes defined, CSS variables injected, persistence to localStorage.

- [ ] **Step 1: Define theme type system in `frontend/lib/themes.ts`**

```typescript
/**
 * Client-only module — theme definitions and CSS variable injection.
 * MUST NOT be imported in server-rendered components.
 */
import { signal } from "@preact/signals";

export interface ThemeColors {
  /** Page background */
  bgBase: string;
  /** Card/panel surfaces */
  bgSurface: string;
  /** Hover states, elevated cards */
  bgElevated: string;
  /** Active hover */
  bgHover: string;
  /** Primary border */
  borderPrimary: string;
  /** Subtle dividers */
  borderSubtle: string;
  /** Primary text */
  textPrimary: string;
  /** Secondary text */
  textSecondary: string;
  /** Muted/disabled text */
  textMuted: string;
  /** Primary accent */
  accent: string;
  /** Accent glow (rgba) */
  accentGlow: string;
  /** Accent dim background (rgba) */
  accentDim: string;
  /** Secondary accent */
  accentSecondary: string;
  /** Success/healthy */
  success: string;
  /** Success dim background */
  successDim: string;
  /** Warning/pending */
  warning: string;
  /** Warning dim background */
  warningDim: string;
  /** Error/failed */
  error: string;
  /** Error dim background */
  errorDim: string;
  /** Info */
  info: string;
}

export interface Theme {
  id: string;
  name: string;
  colors: ThemeColors;
}

export const THEMES: Theme[] = [
  {
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
      accentGlow: "rgba(0, 194, 255, 0.15)",
      accentDim: "rgba(0, 194, 255, 0.08)",
      accentSecondary: "#7C5CFC",
      success: "#00E676",
      successDim: "rgba(0, 230, 118, 0.12)",
      warning: "#FFB300",
      warningDim: "rgba(255, 179, 0, 0.12)",
      error: "#FF5252",
      errorDim: "rgba(255, 82, 82, 0.12)",
      info: "#40C4FF",
    },
  },
  {
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
      accentGlow: "rgba(189, 147, 249, 0.15)",
      accentDim: "rgba(189, 147, 249, 0.08)",
      accentSecondary: "#FF79C6",
      success: "#50FA7B",
      successDim: "rgba(80, 250, 123, 0.12)",
      warning: "#F1FA8C",
      warningDim: "rgba(241, 250, 140, 0.12)",
      error: "#FF5555",
      errorDim: "rgba(255, 85, 85, 0.12)",
      info: "#8BE9FD",
    },
  },
  {
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
      accentGlow: "rgba(122, 162, 247, 0.15)",
      accentDim: "rgba(122, 162, 247, 0.08)",
      accentSecondary: "#BB9AF7",
      success: "#9ECE6A",
      successDim: "rgba(158, 206, 106, 0.12)",
      warning: "#E0AF68",
      warningDim: "rgba(224, 175, 104, 0.12)",
      error: "#F7768E",
      errorDim: "rgba(247, 118, 142, 0.12)",
      info: "#7DCFFF",
    },
  },
  {
    id: "catppuccin",
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
      accentGlow: "rgba(137, 180, 250, 0.15)",
      accentDim: "rgba(137, 180, 250, 0.08)",
      accentSecondary: "#CBA6F7",
      success: "#A6E3A1",
      successDim: "rgba(166, 227, 161, 0.12)",
      warning: "#FAB387",
      warningDim: "rgba(250, 179, 135, 0.12)",
      error: "#F38BA8",
      errorDim: "rgba(243, 139, 168, 0.12)",
      info: "#89DCEB",
    },
  },
  {
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
      accentGlow: "rgba(136, 192, 208, 0.15)",
      accentDim: "rgba(136, 192, 208, 0.08)",
      accentSecondary: "#B48EAD",
      success: "#A3BE8C",
      successDim: "rgba(163, 190, 140, 0.12)",
      warning: "#EBCB8B",
      warningDim: "rgba(235, 203, 139, 0.12)",
      error: "#BF616A",
      errorDim: "rgba(191, 97, 106, 0.12)",
      info: "#81A1C1",
    },
  },
  {
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
      accentGlow: "rgba(97, 175, 239, 0.15)",
      accentDim: "rgba(97, 175, 239, 0.08)",
      accentSecondary: "#C678DD",
      success: "#98C379",
      successDim: "rgba(152, 195, 121, 0.12)",
      warning: "#E5C07B",
      warningDim: "rgba(229, 192, 123, 0.12)",
      error: "#E06C75",
      errorDim: "rgba(224, 108, 117, 0.12)",
      info: "#56B6C2",
    },
  },
  {
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
      accentGlow: "rgba(131, 165, 152, 0.15)",
      accentDim: "rgba(131, 165, 152, 0.08)",
      accentSecondary: "#D3869B",
      success: "#B8BB26",
      successDim: "rgba(184, 187, 38, 0.12)",
      warning: "#FABD2F",
      warningDim: "rgba(250, 189, 47, 0.12)",
      error: "#FB4934",
      errorDim: "rgba(251, 73, 52, 0.12)",
      info: "#8EC07C",
    },
  },
];

const STORAGE_KEY = "k8scenter-theme";
const DEFAULT_THEME = "nexus";

/** Reactive theme signal. */
export const currentTheme = signal<string>(DEFAULT_THEME);

/** Get the full Theme object for the current theme. */
export function getTheme(id?: string): Theme {
  const themeId = id ?? currentTheme.value;
  return THEMES.find((t) => t.id === themeId) ?? THEMES[0];
}

/** Apply a theme's CSS variables to the document root. */
export function applyTheme(themeId: string): void {
  const theme = getTheme(themeId);
  const root = document.documentElement;
  const c = theme.colors;

  root.style.setProperty("--bg-base", c.bgBase);
  root.style.setProperty("--bg-surface", c.bgSurface);
  root.style.setProperty("--bg-elevated", c.bgElevated);
  root.style.setProperty("--bg-hover", c.bgHover);
  root.style.setProperty("--border-primary", c.borderPrimary);
  root.style.setProperty("--border-subtle", c.borderSubtle);
  root.style.setProperty("--text-primary", c.textPrimary);
  root.style.setProperty("--text-secondary", c.textSecondary);
  root.style.setProperty("--text-muted", c.textMuted);
  root.style.setProperty("--accent", c.accent);
  root.style.setProperty("--accent-glow", c.accentGlow);
  root.style.setProperty("--accent-dim", c.accentDim);
  root.style.setProperty("--accent-secondary", c.accentSecondary);
  root.style.setProperty("--success", c.success);
  root.style.setProperty("--success-dim", c.successDim);
  root.style.setProperty("--warning", c.warning);
  root.style.setProperty("--warning-dim", c.warningDim);
  root.style.setProperty("--error", c.error);
  root.style.setProperty("--error-dim", c.errorDim);
  root.style.setProperty("--info", c.info);

  currentTheme.value = themeId;
  localStorage.setItem(STORAGE_KEY, themeId);
}

/** Initialize theme from localStorage or default. */
export function initTheme(): void {
  const saved = localStorage.getItem(STORAGE_KEY);
  const themeId = saved && THEMES.some((t) => t.id === saved) ? saved : DEFAULT_THEME;
  applyTheme(themeId);
}
```

- [ ] **Step 2: Create animation preferences module `frontend/lib/animation-prefs.ts`**

```typescript
/**
 * Client-only module — animation preference signal.
 */
import { signal } from "@preact/signals";

const STORAGE_KEY = "k8scenter-animations";

/** Whether animations are enabled. */
export const animationsEnabled = signal(true);

/** Toggle animation preference. */
export function setAnimations(enabled: boolean): void {
  animationsEnabled.value = enabled;
  localStorage.setItem(STORAGE_KEY, enabled ? "true" : "false");
  document.documentElement.classList.toggle("no-animations", !enabled);
}

/** Initialize from localStorage. */
export function initAnimationPrefs(): void {
  const saved = localStorage.getItem(STORAGE_KEY);
  if (saved === "false") {
    animationsEnabled.value = false;
    document.documentElement.classList.add("no-animations");
  }
}
```

- [ ] **Step 3: Rewrite `frontend/assets/styles.css` with theme CSS variables**

```css
@import "tailwindcss";
@import "@xterm/xterm/css/xterm.css";

/* ===== THEME SYSTEM ===== */
/* Default values (Nexus theme) — overridden at runtime by themes.ts */
:root {
  --bg-base: #0B0E14;
  --bg-surface: #131720;
  --bg-elevated: #1A1F2B;
  --bg-hover: #212735;
  --border-primary: #252B37;
  --border-subtle: #1E2330;
  --text-primary: #E0E4EB;
  --text-secondary: #8B95A5;
  --text-muted: #5A6478;
  --accent: #00C2FF;
  --accent-glow: rgba(0, 194, 255, 0.15);
  --accent-dim: rgba(0, 194, 255, 0.08);
  --accent-secondary: #7C5CFC;
  --success: #00E676;
  --success-dim: rgba(0, 230, 118, 0.12);
  --warning: #FFB300;
  --warning-dim: rgba(255, 179, 0, 0.12);
  --error: #FF5252;
  --error-dim: rgba(255, 82, 82, 0.12);
  --info: #40C4FF;

  --rail-width: 60px;
  --topbar-height: 52px;
  --radius: 10px;
  --radius-sm: 6px;

  --font-sans: 'Geist', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  --font-mono: 'Geist Mono', 'JetBrains Mono', 'Fira Code', monospace;
}

/* ===== TAILWIND THEME BRIDGE ===== */
/* Map CSS variables into Tailwind's @theme so utilities like bg-base, text-primary work */
@theme {
  --color-base: var(--bg-base);
  --color-surface: var(--bg-surface);
  --color-elevated: var(--bg-elevated);
  --color-hover: var(--bg-hover);
  --color-border: var(--border-primary);
  --color-border-subtle: var(--border-subtle);
  --color-txt-primary: var(--text-primary);
  --color-txt-secondary: var(--text-secondary);
  --color-txt-muted: var(--text-muted);
  --color-accent: var(--accent);
  --color-accent-glow: var(--accent-glow);
  --color-accent-dim: var(--accent-dim);
  --color-accent-secondary: var(--accent-secondary);
  --color-success: var(--success);
  --color-success-dim: var(--success-dim);
  --color-warning: var(--warning);
  --color-warning-dim: var(--warning-dim);
  --color-danger: var(--error);
  --color-danger-dim: var(--error-dim);
  --color-info: var(--info);

  /* Keep legacy color names for gradual migration */
  --color-brand: var(--accent);
  --color-brand-dark: var(--accent);
}

/* ===== BASE STYLES ===== */
body {
  font-family: var(--font-sans);
  background: var(--bg-base);
  color: var(--text-primary);
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}

/* ===== SCROLLBAR ===== */
* {
  scrollbar-width: thin;
  scrollbar-color: var(--bg-hover) transparent;
}

::-webkit-scrollbar { width: 6px; height: 6px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: var(--bg-hover); border-radius: 3px; }
::-webkit-scrollbar-thumb:hover { background: var(--border-primary); }

/* ===== ANIMATION CONTROLS ===== */
.no-animations *,
.no-animations *::before,
.no-animations *::after {
  animation-duration: 0.01ms !important;
  animation-iteration-count: 1 !important;
  transition-duration: 0.01ms !important;
}

/* ===== UTILITY ANIMATIONS ===== */
@keyframes fade-in-up {
  from { opacity: 0; transform: translateY(12px); }
  to { opacity: 1; transform: translateY(0); }
}

@keyframes pulse-glow {
  0%, 100% { opacity: 1; box-shadow: 0 0 0 0 var(--accent-glow); }
  50% { opacity: 0.7; box-shadow: 0 0 0 4px transparent; }
}

@keyframes status-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.5; }
}

.animate-fade-in-up { animation: fade-in-up 0.4s ease forwards; }
.animate-pulse-glow { animation: pulse-glow 2s ease-in-out infinite; }
.animate-status-pulse { animation: status-pulse 2s ease-in-out infinite; }
```

- [ ] **Step 4: Update `frontend/routes/_app.tsx` — add Geist fonts and theme init script**

Replace the existing `_app.tsx` with:

```tsx
// deno-lint-ignore-file react-no-danger
import { define } from "@/utils.ts";

export default define.page(function App({ Component }) {
  return (
    <html lang="en" class="h-full">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1.0" />
        <title>k8sCenter</title>
        <meta name="color-scheme" content="dark" />
        {/* Geist fonts */}
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link
          href="https://fonts.googleapis.com/css2?family=Geist:wght@300;400;500;600;700&family=Geist+Mono:wght@400;500;600&display=swap"
          rel="stylesheet"
        />
        {/* Apply saved theme before render to prevent flash */}
        <script
          dangerouslySetInnerHTML={{
            __html: `(function(){
              var t=localStorage.getItem("k8scenter-theme")||"nexus";
              document.documentElement.dataset.theme=t;
              if(localStorage.getItem("k8scenter-animations")==="false")
                document.documentElement.classList.add("no-animations");
            })()`,
          }}
        />
      </head>
      <body class="h-full">
        <Component />
      </body>
    </html>
  );
});
```

- [ ] **Step 5: Update `frontend/main.ts` — add Google Fonts to CSP**

In `main.ts`, update the CSP header to allow Google Fonts:

```typescript
res.headers.set(
  "Content-Security-Policy",
  [
    "default-src 'self'",
    "script-src 'self' 'unsafe-inline' https://esm.sh/",
    "style-src 'self' 'unsafe-inline' https://fonts.googleapis.com/ https://esm.sh/",
    "font-src 'self' https://fonts.gstatic.com/",
    "img-src 'self' data:",
    "connect-src 'self' https://esm.sh/",
    "worker-src 'self' blob: https://esm.sh/",
    "frame-src 'self'",
    "frame-ancestors 'none'",
    "base-uri 'self'",
    "object-src 'none'",
    "form-action 'self'",
  ].join("; "),
);
```

- [ ] **Step 6: Add dependencies to `frontend/deno.json`**

Add to the `"imports"` section:

```json
"fuse.js": "npm:fuse.js@^7.1.0",
"d3-force": "npm:d3-force@^3.0.0",
"d3-force/types": "npm:@types/d3-force@^3.0.0"
```

- [ ] **Step 7: Run `deno install` and verify build**

Run: `cd frontend && deno install && deno task build`
Expected: Clean build with no errors

- [ ] **Step 8: Commit**

```bash
git add frontend/lib/themes.ts frontend/lib/animation-prefs.ts frontend/assets/styles.css frontend/routes/_app.tsx frontend/main.ts frontend/deno.json
git commit -m "feat: add theme system foundation with 7 named themes and CSS variable architecture"
```

---

## Task 2: Icon Rail Navigation

**Files:**
- Create: `frontend/islands/IconRail.tsx`
- Modify: `frontend/lib/constants.ts`
- Modify: `frontend/routes/_layout.tsx`

- [ ] **Step 1: Add domain navigation definitions to `frontend/lib/constants.ts`**

Add after the existing `NAV_SECTIONS` (keep it for backward compat):

```typescript
/** Domain-oriented navigation for the redesigned icon rail. */
export interface DomainSection {
  id: string;
  label: string;
  icon: string;
  href: string;
  /** Sub-navigation tabs within this domain */
  tabs?: { label: string; href: string; kind?: string; count?: boolean }[];
}

export const DOMAIN_SECTIONS: DomainSection[] = [
  {
    id: "overview",
    label: "Overview",
    icon: "grid",
    href: "/",
  },
  {
    id: "workloads",
    label: "Workloads",
    icon: "box",
    href: "/workloads",
    tabs: [
      { label: "Deployments", href: "/workloads/deployments", kind: "deployments", count: true },
      { label: "StatefulSets", href: "/workloads/statefulsets", kind: "statefulsets", count: true },
      { label: "DaemonSets", href: "/workloads/daemonsets", kind: "daemonsets", count: true },
      { label: "Pods", href: "/workloads/pods", kind: "pods", count: true },
      { label: "Jobs", href: "/workloads/jobs", kind: "jobs", count: true },
      { label: "CronJobs", href: "/workloads/cronjobs", kind: "cronjobs", count: true },
      { label: "ReplicaSets", href: "/workloads/replicasets", kind: "replicasets", count: true },
    ],
  },
  {
    id: "network",
    label: "Network",
    icon: "globe",
    href: "/networking",
    tabs: [
      { label: "Services", href: "/networking/services", kind: "services", count: true },
      { label: "Ingresses", href: "/networking/ingresses", kind: "ingresses", count: true },
      { label: "Network Policies", href: "/networking/networkpolicies", kind: "networkpolicies", count: true },
      { label: "Cilium Policies", href: "/networking/cilium-policies", kind: "ciliumnetworkpolicies", count: true },
      { label: "Flows", href: "/networking/flows" },
      { label: "CNI", href: "/networking/cni" },
      { label: "Endpoints", href: "/networking/endpoints", kind: "endpoints", count: true },
    ],
  },
  {
    id: "storage",
    label: "Storage",
    icon: "harddrive",
    href: "/storage",
    tabs: [
      { label: "Overview", href: "/storage/overview" },
      { label: "PVCs", href: "/storage/pvcs", kind: "persistentvolumeclaims", count: true },
      { label: "PVs", href: "/cluster/pvs", kind: "persistentvolumes", count: true },
      { label: "Storage Classes", href: "/cluster/storageclasses", kind: "storageclasses", count: true },
      { label: "Snapshots", href: "/storage/snapshots" },
    ],
  },
  {
    id: "config",
    label: "Config",
    icon: "sliders",
    href: "/config",
    tabs: [
      { label: "ConfigMaps", href: "/config/configmaps", kind: "configmaps", count: true },
      { label: "Secrets", href: "/config/secrets", kind: "secrets", count: true },
      { label: "Service Accounts", href: "/config/serviceaccounts", kind: "serviceaccounts", count: true },
      { label: "Resource Quotas", href: "/config/resourcequotas", kind: "resourcequotas", count: true },
      { label: "Limit Ranges", href: "/config/limitranges", kind: "limitranges", count: true },
    ],
  },
  {
    id: "security",
    label: "Security",
    icon: "shield",
    href: "/rbac",
    tabs: [
      { label: "Overview", href: "/rbac/overview" },
      { label: "Roles", href: "/rbac/roles", kind: "roles", count: true },
      { label: "Cluster Roles", href: "/rbac/clusterroles", kind: "clusterroles", count: true },
      { label: "Role Bindings", href: "/rbac/rolebindings", kind: "rolebindings", count: true },
      { label: "Cluster Role Bindings", href: "/rbac/clusterrolebindings", kind: "clusterrolebindings", count: true },
      { label: "Webhooks", href: "/admin/validatingwebhooks" },
    ],
  },
  {
    id: "observability",
    label: "Observability",
    icon: "activity",
    href: "/monitoring",
    tabs: [
      { label: "Overview", href: "/monitoring" },
      { label: "Dashboards", href: "/monitoring/dashboards" },
      { label: "Prometheus", href: "/monitoring/prometheus" },
      { label: "Active Alerts", href: "/alerting" },
      { label: "Alert Rules", href: "/alerting/rules" },
      { label: "Alert Settings", href: "/alerting/settings" },
    ],
  },
  {
    id: "tools",
    label: "Tools",
    icon: "wrench",
    href: "/tools",
    tabs: [
      { label: "YAML Apply", href: "/tools/yaml-apply" },
      { label: "StorageClass Wizard", href: "/tools/storageclass-wizard" },
    ],
  },
];

/** Settings nav (always at bottom of rail). */
export const SETTINGS_SECTION: DomainSection = {
  id: "settings",
  label: "Settings",
  icon: "settings",
  href: "/settings/general",
  tabs: [
    { label: "General", href: "/settings/general" },
    { label: "Clusters", href: "/settings/clusters" },
    { label: "Users", href: "/settings/users" },
    { label: "Authentication", href: "/settings/auth" },
    { label: "Audit Log", href: "/settings/audit" },
  ],
};
```

- [ ] **Step 2: Create `frontend/islands/IconRail.tsx`**

```tsx
import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { DOMAIN_SECTIONS, SETTINGS_SECTION } from "@/lib/constants.ts";
import { useAuth } from "@/lib/auth.ts";

/** SVG icon paths keyed by domain icon name. */
const ICONS: Record<string, string> = {
  grid: '<rect x="3" y="3" width="6" height="6" rx="1"/><rect x="11" y="3" width="6" height="6" rx="1"/><rect x="3" y="11" width="6" height="6" rx="1"/><rect x="11" y="11" width="6" height="6" rx="1"/>',
  box: '<rect x="3" y="4" width="14" height="12" rx="2"/><path d="M7 4V2M13 4V2"/><circle cx="10" cy="10" r="2"/>',
  globe: '<circle cx="10" cy="10" r="7"/><path d="M2 10h4M14 10h4M10 2v4M10 14v4"/>',
  harddrive: '<rect x="3" y="5" width="14" height="4" rx="1"/><rect x="3" y="11" width="14" height="4" rx="1"/><circle cx="6" cy="7" r="1" fill="currentColor"/><circle cx="6" cy="13" r="1" fill="currentColor"/>',
  sliders: '<circle cx="10" cy="10" r="7"/><circle cx="10" cy="10" r="3"/><path d="M10 3v2M10 15v2M3 10h2M15 10h2"/>',
  shield: '<path d="M10 2l1.5 3.5L15 7l-3.5 1.5L10 12 8.5 8.5 5 7l3.5-1.5L10 2Z"/><path d="M4 14l2-1.5M16 14l-2-1.5M10 18v-3"/>',
  activity: '<polyline points="3,14 7,8 11,11 14,5 17,9"/><line x1="3" y1="17" x2="17" y2="17"/>',
  wrench: '<path d="M10 4v4l3 2"/><circle cx="10" cy="10" r="7"/>',
  settings: '<circle cx="10" cy="10" r="7"/><circle cx="10" cy="10" r="3"/><path d="M13 10h4M3 10h4M10 3v4M10 13v4"/>',
};

interface IconRailProps {
  currentPath: string;
}

export default function IconRail({ currentPath }: IconRailProps) {
  if (!IS_BROWSER) {
    // SSR: render minimal placeholder to avoid layout shift
    return <nav style={{ width: "var(--rail-width)" }} class="bg-surface border-r border-border" />;
  }

  const { user } = useAuth();
  const isAdmin = user.value?.role === "admin";

  /** Determine which domain is active based on current path. */
  function activeDomain(): string {
    if (currentPath === "/") return "overview";
    for (const section of DOMAIN_SECTIONS) {
      if (section.href !== "/" && currentPath.startsWith(section.href)) return section.id;
      if (section.tabs?.some((t) => currentPath.startsWith(t.href))) return section.id;
    }
    if (currentPath.startsWith("/settings") || currentPath.startsWith("/admin")) return "settings";
    // Check cluster routes
    if (currentPath.startsWith("/cluster")) return "overview";
    if (currentPath.startsWith("/scaling")) return "workloads";
    return "overview";
  }

  const active = activeDomain();

  return (
    <nav
      class="flex flex-col items-center border-r"
      style={{
        width: "var(--rail-width)",
        background: "var(--bg-surface)",
        borderColor: "var(--border-primary)",
        gridRow: "1 / -1",
      }}
    >
      {/* Logo */}
      <a
        href="/"
        class="flex w-full items-center justify-center border-b"
        style={{ height: "var(--topbar-height)", borderColor: "var(--border-primary)" }}
      >
        <svg viewBox="0 0 28 28" fill="none" width="28" height="28">
          <path d="M14 2L3 8.5V19.5L14 26L25 19.5V8.5L14 2Z" stroke="var(--accent)" stroke-width="1.5" />
          <circle cx="14" cy="14" r="4" fill="var(--accent)" opacity="0.8" />
          <circle cx="14" cy="6" r="1.5" fill="var(--accent)" />
          <circle cx="7" cy="18" r="1.5" fill="var(--accent)" />
          <circle cx="21" cy="18" r="1.5" fill="var(--accent)" />
        </svg>
      </a>

      {/* Domain icons */}
      <div class="flex flex-1 flex-col items-center gap-1 py-3 w-full">
        {DOMAIN_SECTIONS.map((section) => (
          <a
            key={section.id}
            href={section.href}
            class="group relative flex items-center justify-center rounded-md transition-all"
            style={{
              width: "42px",
              height: "42px",
              background: active === section.id ? "var(--accent-dim)" : "transparent",
              color: active === section.id ? "var(--accent)" : "var(--text-muted)",
            }}
          >
            {active === section.id && (
              <div
                class="absolute left-0 top-1/2 -translate-y-1/2 rounded-r"
                style={{ width: "3px", height: "20px", background: "var(--accent)", left: "-9px" }}
              />
            )}
            <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" width="20" height="20"
              dangerouslySetInnerHTML={{ __html: ICONS[section.icon] ?? "" }}
            />
            {/* Tooltip */}
            <span
              class="pointer-events-none absolute left-full ml-3 whitespace-nowrap rounded px-2 py-1 text-xs font-medium opacity-0 transition-opacity group-hover:opacity-100"
              style={{
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-primary)",
                color: "var(--text-primary)",
                zIndex: 100,
              }}
            >
              {section.label}
            </span>
          </a>
        ))}
      </div>

      {/* Settings (bottom) */}
      {isAdmin && (
        <div class="flex flex-col items-center gap-1 py-3 w-full border-t" style={{ borderColor: "var(--border-primary)" }}>
          <a
            href={SETTINGS_SECTION.href}
            class="group relative flex items-center justify-center rounded-md transition-all"
            style={{
              width: "42px",
              height: "42px",
              background: active === "settings" ? "var(--accent-dim)" : "transparent",
              color: active === "settings" ? "var(--accent)" : "var(--text-muted)",
            }}
          >
            {active === "settings" && (
              <div
                class="absolute left-0 top-1/2 -translate-y-1/2 rounded-r"
                style={{ width: "3px", height: "20px", background: "var(--accent)", left: "-9px" }}
              />
            )}
            <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" width="20" height="20"
              dangerouslySetInnerHTML={{ __html: ICONS.settings }}
            />
            <span
              class="pointer-events-none absolute left-full ml-3 whitespace-nowrap rounded px-2 py-1 text-xs font-medium opacity-0 transition-opacity group-hover:opacity-100"
              style={{
                background: "var(--bg-elevated)",
                border: "1px solid var(--border-primary)",
                color: "var(--text-primary)",
                zIndex: 100,
              }}
            >
              Settings
            </span>
          </a>
        </div>
      )}
    </nav>
  );
}
```

- [ ] **Step 3: Update `frontend/routes/_layout.tsx` to use new layout**

```tsx
import { define } from "@/utils.ts";
import IconRail from "@/islands/IconRail.tsx";
import TopBarV2 from "@/islands/TopBarV2.tsx";
import ToastProvider from "@/islands/ToastProvider.tsx";
import CommandPalette from "@/islands/CommandPalette.tsx";
import QuickActionsFab from "@/islands/QuickActionsFab.tsx";

export default define.page(function Layout({ Component, url }) {
  // Login, setup, and OIDC callback use their own full-screen layout
  if (url.pathname === "/login" || url.pathname === "/setup" || url.pathname.startsWith("/auth/")) {
    return <Component />;
  }

  return (
    <div
      class="h-full"
      style={{
        display: "grid",
        gridTemplateColumns: "var(--rail-width) 1fr",
        gridTemplateRows: "var(--topbar-height) 1fr",
      }}
    >
      <IconRail currentPath={url.pathname} />
      <TopBarV2 currentPath={url.pathname} />
      <main class="overflow-y-auto p-6">
        <Component />
      </main>
      <ToastProvider />
      <CommandPalette />
      <QuickActionsFab />
    </div>
  );
});
```

- [ ] **Step 4: Verify the app renders with the new layout**

Run: `cd frontend && deno task dev`
Expected: App loads at http://localhost:5173 with the icon rail visible on the left. The TopBarV2 island doesn't exist yet, so this will error. Create a stub:

- [ ] **Step 5: Create stub `frontend/islands/TopBarV2.tsx`**

```tsx
import { IS_BROWSER } from "fresh/runtime";
import { useAuth } from "@/lib/auth.ts";
import { selectedNamespace } from "@/lib/namespace.ts";
import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet, getAccessToken } from "@/lib/api.ts";
import type { K8sResource } from "@/lib/k8s-types.ts";
import { initTheme } from "@/lib/themes.ts";
import { initAnimationPrefs } from "@/lib/animation-prefs.ts";
import { selectedCluster } from "@/lib/cluster.ts";

interface TopBarV2Props {
  currentPath: string;
}

export default function TopBarV2({ currentPath }: TopBarV2Props) {
  const { user, logout: doLogout, fetchCurrentUser, refreshPermissions } = useAuth();
  const namespaces = useSignal<string[]>([]);
  const userMenuOpen = useSignal(false);

  useEffect(() => {
    if (!IS_BROWSER) return;
    initTheme();
    initAnimationPrefs();

    // Load user if we have a token
    if (getAccessToken() || document.cookie.includes("refresh")) {
      fetchCurrentUser(selectedNamespace.value !== "all" ? selectedNamespace.value : undefined);
    }

    // Load namespaces
    apiGet<{ items: K8sResource[] }>("/v1/resources/namespaces")
      .then((res) => {
        namespaces.value = (res.data.items ?? []).map((ns) => ns.metadata.name).sort();
      })
      .catch(() => {});
  }, []);

  function handleNamespaceChange(ns: string) {
    selectedNamespace.value = ns;
    if (ns !== "all") refreshPermissions(ns);
  }

  async function handleLogout() {
    await doLogout();
    globalThis.location.href = "/login";
  }

  return (
    <header
      class="flex items-center gap-3 border-b px-5"
      style={{
        background: "var(--bg-surface)",
        borderColor: "var(--border-primary)",
        height: "var(--topbar-height)",
      }}
    >
      {/* Left: selectors */}
      <div class="flex flex-1 items-center gap-4">
        {/* Cluster selector */}
        <div
          class="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium cursor-pointer"
          style={{ background: "var(--bg-elevated)", borderColor: "var(--border-primary)", color: "var(--text-primary)" }}
        >
          <span class="h-1.5 w-1.5 rounded-full" style={{ background: "var(--success)", boxShadow: "0 0 6px var(--success)" }} />
          <span class="text-[11px] uppercase tracking-wide mr-1" style={{ color: "var(--text-muted)" }}>Cluster</span>
          {selectedCluster.value || "local"}
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" width="14" height="14" style={{ color: "var(--text-muted)" }}>
            <path d="M4 6l4 4 4-4" />
          </svg>
        </div>

        {/* Namespace selector */}
        <select
          value={selectedNamespace.value}
          onChange={(e) => handleNamespaceChange((e.target as HTMLSelectElement).value)}
          class="rounded-md border px-3 py-1.5 text-sm font-medium cursor-pointer"
          style={{ background: "var(--bg-elevated)", borderColor: "var(--border-primary)", color: "var(--text-primary)" }}
        >
          <option value="all">All Namespaces</option>
          {namespaces.value.map((ns) => <option key={ns} value={ns}>{ns}</option>)}
        </select>

        {/* Search trigger */}
        <div
          class="flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm cursor-pointer min-w-[240px]"
          style={{ background: "var(--bg-elevated)", borderColor: "var(--border-primary)", color: "var(--text-muted)" }}
          onClick={() => {
            // Dispatch custom event that CommandPalette listens to
            globalThis.dispatchEvent(new CustomEvent("open-command-palette"));
          }}
        >
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" width="14" height="14">
            <circle cx="7" cy="7" r="4.5" /><path d="M10.5 10.5L14 14" />
          </svg>
          Search resources, actions...
          <span
            class="ml-auto rounded border px-1.5 py-0.5 font-mono text-[11px]"
            style={{ background: "var(--bg-base)", borderColor: "var(--border-primary)" }}
          >
            ⌘K
          </span>
        </div>
      </div>

      {/* Right: actions */}
      <div class="flex items-center gap-3">
        {/* Notification bell */}
        <button
          class="relative flex h-8 w-8 items-center justify-center rounded-md transition-colors"
          style={{ color: "var(--text-muted)" }}
        >
          <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.5" width="18" height="18">
            <path d="M10 3a5 5 0 015 5c0 3.5 1.5 4.5 1.5 4.5H3.5S5 11.5 5 8a5 5 0 015-5zM8.5 15.5a2 2 0 003 0" />
          </svg>
        </button>

        <div class="h-6 w-px" style={{ background: "var(--border-primary)" }} />

        {/* User avatar */}
        <div class="relative">
          <button
            onClick={() => { userMenuOpen.value = !userMenuOpen.value; }}
            class="flex h-8 w-8 items-center justify-center rounded-full text-xs font-semibold text-white"
            style={{ background: `linear-gradient(135deg, var(--accent), var(--accent-secondary))` }}
          >
            {user.value?.username?.[0]?.toUpperCase() ?? "?"}
          </button>

          {userMenuOpen.value && (
            <div
              class="absolute right-0 top-full mt-2 w-48 rounded-lg border p-2 shadow-xl"
              style={{ background: "var(--bg-surface)", borderColor: "var(--border-primary)", zIndex: 50 }}
            >
              <div class="px-3 py-2 text-sm font-medium" style={{ color: "var(--text-primary)" }}>
                {user.value?.username ?? "Unknown"}
              </div>
              <div class="px-3 pb-2 text-xs" style={{ color: "var(--text-muted)" }}>
                {user.value?.role ?? "user"}
              </div>
              <div class="border-t my-1" style={{ borderColor: "var(--border-primary)" }} />
              <button
                onClick={handleLogout}
                class="w-full rounded-md px-3 py-1.5 text-left text-sm transition-colors"
                style={{ color: "var(--error)" }}
              >
                Sign out
              </button>
            </div>
          )}
        </div>
      </div>
    </header>
  );
}
```

- [ ] **Step 6: Create stubs for CommandPalette and QuickActionsFab**

Create `frontend/islands/CommandPalette.tsx`:
```tsx
import { IS_BROWSER } from "fresh/runtime";
export default function CommandPalette() {
  if (!IS_BROWSER) return null;
  // Stub — implemented in Task 5
  return null;
}
```

Create `frontend/islands/QuickActionsFab.tsx`:
```tsx
import { IS_BROWSER } from "fresh/runtime";
export default function QuickActionsFab() {
  if (!IS_BROWSER) return null;
  // Stub — implemented in Task 8
  return null;
}
```

- [ ] **Step 7: Verify the app renders with new layout**

Run: `cd frontend && deno task dev`
Expected: App loads with icon rail on left, top bar across top, main content in remaining area. Navigation between domains works.

- [ ] **Step 8: Commit**

```bash
git add frontend/islands/IconRail.tsx frontend/islands/TopBarV2.tsx frontend/islands/CommandPalette.tsx frontend/islands/QuickActionsFab.tsx frontend/lib/constants.ts frontend/routes/_layout.tsx
git commit -m "feat: replace sidebar with icon rail navigation and redesigned top bar"
```

---

## Task 3: Migrate UI Components to Theme Variables

**Files:**
- Modify: `frontend/components/ui/Button.tsx`
- Modify: `frontend/components/ui/Card.tsx`
- Modify: `frontend/components/ui/StatusBadge.tsx`
- Modify: `frontend/components/ui/Tabs.tsx`
- Modify: `frontend/components/ui/SearchBar.tsx`
- Modify: `frontend/components/ui/Skeleton.tsx`
- Modify: `frontend/components/ui/Alert.tsx`
- Modify: `frontend/components/ui/Breadcrumb.tsx`
- Modify: `frontend/lib/status-colors.ts`
- Modify: `frontend/islands/ToastProvider.tsx`
- Create: `frontend/components/ui/StatusDot.tsx`
- Create: `frontend/components/ui/SparklineChart.tsx`
- Create: `frontend/components/ui/GaugeRing.tsx`
- Create: `frontend/components/ui/FilterChip.tsx`
- Create: `frontend/components/ui/SummaryRing.tsx`

This task migrates every shared UI component from hardcoded Tailwind dark: classes to CSS variable-based theming. Each component gets the same props API but renders with theme-aware styles.

- [ ] **Step 1: Update `frontend/lib/status-colors.ts` to use CSS variables**

Replace `VARIANT_CLASSES` with theme-aware versions:

```typescript
export const VARIANT_CLASSES: Record<StatusVariant, string> = {
  success: "ring-1 ring-inset",
  warning: "ring-1 ring-inset",
  danger: "ring-1 ring-inset",
  info: "ring-1 ring-inset",
  neutral: "ring-1 ring-inset",
};

/** Returns inline style object for status badges (theme-aware). */
export function statusStyle(status: string): Record<string, string> {
  const v = statusVariant(status);
  const map: Record<StatusVariant, Record<string, string>> = {
    success: { background: "var(--success-dim)", color: "var(--success)", "--tw-ring-color": "var(--success)" },
    warning: { background: "var(--warning-dim)", color: "var(--warning)", "--tw-ring-color": "var(--warning)" },
    danger: { background: "var(--error-dim)", color: "var(--error)", "--tw-ring-color": "var(--error)" },
    info: { background: "var(--accent-dim)", color: "var(--accent)", "--tw-ring-color": "var(--accent)" },
    neutral: { background: "var(--bg-elevated)", color: "var(--text-muted)", "--tw-ring-color": "var(--border-primary)" },
  };
  return map[v];
}
```

- [ ] **Step 2: Update `frontend/components/ui/Button.tsx`**

Replace variant classes to use CSS variables via inline styles:

```tsx
import type { JSX } from "preact";

type ButtonVariant = "primary" | "secondary" | "danger" | "ghost";
type ButtonSize = "sm" | "md" | "lg";

interface ButtonProps extends JSX.HTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  disabled?: boolean;
}

const sizeClasses: Record<ButtonSize, string> = {
  sm: "px-2.5 py-1.5 text-xs",
  md: "px-4 py-2 text-sm",
  lg: "px-6 py-3 text-base",
};

const variantStyles: Record<ButtonVariant, Record<string, string>> = {
  primary: { background: "var(--accent)", color: "var(--bg-base)" },
  secondary: { background: "transparent", color: "var(--text-secondary)", border: "1px solid var(--border-primary)" },
  danger: { background: "var(--error)", color: "white" },
  ghost: { background: "transparent", color: "var(--text-secondary)" },
};

export function Button({
  variant = "primary",
  size = "md",
  loading = false,
  disabled,
  class: className,
  style: styleProp,
  children,
  ...props
}: ButtonProps) {
  return (
    <button
      {...props}
      disabled={disabled || loading}
      class={`inline-flex items-center justify-center font-medium rounded-md transition-colors focus:outline-none disabled:cursor-not-allowed disabled:opacity-50 ${sizeClasses[size]} ${className ?? ""}`}
      style={{ ...variantStyles[variant], ...(typeof styleProp === "object" ? styleProp : {}) }}
    >
      {loading && (
        <svg class="animate-spin -ml-1 mr-2 h-4 w-4" viewBox="0 0 24 24" fill="none">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
        </svg>
      )}
      {children}
    </button>
  );
}
```

- [ ] **Step 3: Update `frontend/components/ui/Card.tsx`**

```tsx
import type { ComponentChildren } from "preact";

interface CardProps {
  title?: string;
  children: ComponentChildren;
  class?: string;
  glow?: boolean;
}

export function Card({ title, children, class: className, glow }: CardProps) {
  return (
    <div
      class={`rounded-[var(--radius)] border p-5 transition-colors ${className ?? ""}`}
      style={{
        background: "var(--bg-surface)",
        borderColor: "var(--border-primary)",
        ...(glow ? { boxShadow: `0 0 20px var(--accent-glow)` } : {}),
      }}
    >
      {title && (
        <h3
          class="mb-4 text-[11px] font-semibold uppercase tracking-[0.08em]"
          style={{ color: "var(--text-muted)" }}
        >
          {title}
        </h3>
      )}
      {children}
    </div>
  );
}
```

- [ ] **Step 4: Update StatusBadge, Tabs, SearchBar, Skeleton, Alert, Breadcrumb**

Each component follows the same pattern: replace hardcoded Tailwind `dark:` classes with inline `style` using CSS variables. The class structure stays the same for layout (padding, border-radius, flex), but colors come from variables.

For each component, replace color-related Tailwind classes with inline styles referencing `var(--text-primary)`, `var(--bg-surface)`, `var(--border-primary)`, etc.

*StatusBadge:*
```tsx
import { statusVariant } from "@/lib/status-colors.ts";
import { statusStyle } from "@/lib/status-colors.ts";

export function StatusBadge({ status, variant }: { status: string; variant?: string }) {
  const v = variant ?? statusVariant(status);
  const styles = statusStyle(status);
  return (
    <span
      class="inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset"
      style={styles}
    >
      {status}
    </span>
  );
}
```

*Skeleton:*
```tsx
export function Skeleton({ class: className = "" }: { class?: string }) {
  return (
    <div
      class={`animate-pulse rounded ${className}`}
      style={{ background: "var(--bg-elevated)" }}
    />
  );
}
```

- [ ] **Step 5: Create new shared components**

Create `frontend/components/ui/StatusDot.tsx`:
```tsx
interface StatusDotProps {
  status: "success" | "warning" | "error" | "info" | "neutral";
  pulse?: boolean;
  size?: number;
}

export function StatusDot({ status, pulse = false, size = 7 }: StatusDotProps) {
  const colorMap = {
    success: "var(--success)",
    warning: "var(--warning)",
    error: "var(--error)",
    info: "var(--accent)",
    neutral: "var(--text-muted)",
  };
  return (
    <span
      class={pulse ? "animate-pulse-glow" : ""}
      style={{
        width: `${size}px`,
        height: `${size}px`,
        borderRadius: "50%",
        background: colorMap[status],
        display: "inline-block",
        flexShrink: 0,
        ...(status === "success" ? { boxShadow: `0 0 6px ${colorMap[status]}` } : {}),
      }}
    />
  );
}
```

Create `frontend/components/ui/SparklineChart.tsx`:
```tsx
interface SparklineChartProps {
  data: number[];
  color: string;
  width?: number;
  height?: number;
}

export function SparklineChart({ data, color, width = 120, height = 32 }: SparklineChartProps) {
  if (data.length < 2) return null;
  const max = Math.max(...data);
  const min = Math.min(...data);
  const range = max - min || 1;
  const padding = 2;

  const points = data.map((v, i) => {
    const x = (i / (data.length - 1)) * width;
    const y = padding + ((max - v) / range) * (height - padding * 2);
    return `${x},${y}`;
  }).join(" ");

  const areaPath = `M0,${height} L${points.split(" ").map((p) => p).join(" L")} L${width},${height}Z`;

  return (
    <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none" width="100%" height={height}>
      <defs>
        <linearGradient id={`spark-${color.replace("#", "")}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color={color} stop-opacity="0.15" />
          <stop offset="100%" stop-color={color} stop-opacity="0" />
        </linearGradient>
      </defs>
      <path d={areaPath} fill={`url(#spark-${color.replace("#", "")})`} />
      <polyline
        points={points}
        fill="none"
        stroke={color}
        stroke-width="1.5"
        stroke-linecap="round"
        stroke-linejoin="round"
      />
    </svg>
  );
}
```

Create `frontend/components/ui/GaugeRing.tsx`:
```tsx
interface GaugeRingProps {
  value: number; // 0-100
  size?: number;
  strokeWidth?: number;
  color: string;
  secondaryColor?: string;
  label?: string;
}

export function GaugeRing({ value, size = 100, strokeWidth = 8, color, secondaryColor, label }: GaugeRingProps) {
  const radius = (size - strokeWidth) / 2;
  const circumference = 2 * Math.PI * radius;
  const offset = circumference * (1 - value / 100);

  const gradientId = `gauge-${Math.random().toString(36).slice(2, 8)}`;

  return (
    <div class="relative" style={{ width: `${size}px`, height: `${size}px` }}>
      <svg viewBox={`0 0 ${size} ${size}`} style={{ transform: "rotate(-90deg)" }} width={size} height={size}>
        {secondaryColor && (
          <defs>
            <linearGradient id={gradientId} x1="0%" y1="0%" x2="100%" y2="100%">
              <stop offset="0%" stop-color={color} />
              <stop offset="100%" stop-color={secondaryColor} />
            </linearGradient>
          </defs>
        )}
        <circle
          cx={size / 2} cy={size / 2} r={radius}
          fill="none" stroke="var(--border-primary)" stroke-width={strokeWidth}
        />
        <circle
          cx={size / 2} cy={size / 2} r={radius}
          fill="none"
          stroke={secondaryColor ? `url(#${gradientId})` : color}
          stroke-width={strokeWidth}
          stroke-linecap="round"
          stroke-dasharray={circumference}
          stroke-dashoffset={offset}
          style={{ transition: "stroke-dashoffset 1s ease", filter: `drop-shadow(0 0 6px ${color}40)` }}
        />
      </svg>
      <div
        class="absolute inset-0 flex flex-col items-center justify-center"
        style={{ color }}
      >
        <span class="text-2xl font-bold font-mono leading-none">{value}%</span>
        {label && <span class="text-[10px] mt-0.5" style={{ color: "var(--text-muted)" }}>{label}</span>}
      </div>
    </div>
  );
}
```

- [ ] **Step 6: Commit**

```bash
git add frontend/components/ frontend/lib/status-colors.ts frontend/islands/ToastProvider.tsx
git commit -m "feat: migrate all UI components to CSS variable theming"
```

---

## Task 4: Sub-Navigation Tabs Island

**Files:**
- Create: `frontend/islands/SubNav.tsx`

- [ ] **Step 1: Create `frontend/islands/SubNav.tsx`**

```tsx
import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { apiGet } from "@/lib/api.ts";
import { selectedNamespace } from "@/lib/namespace.ts";

interface SubNavTab {
  label: string;
  href: string;
  kind?: string;
  count?: boolean;
}

interface SubNavProps {
  tabs: SubNavTab[];
  currentPath: string;
}

export default function SubNav({ tabs, currentPath }: SubNavProps) {
  const counts = useSignal<Record<string, number>>({});

  useEffect(() => {
    // Fetch counts for tabs that have count: true
    const countTabs = tabs.filter((t) => t.count && t.kind);
    if (countTabs.length === 0) return;

    const ns = selectedNamespace.value;
    Promise.allSettled(
      countTabs.map(async (tab) => {
        const nsPath = ns && ns !== "all" ? `/${ns}` : "";
        const res = await apiGet<{ items: unknown[]; metadata?: { total?: number } }>(
          `/v1/resources/${tab.kind}${nsPath}?limit=1`,
        );
        return { kind: tab.kind!, count: res.data.metadata?.total ?? res.data.items?.length ?? 0 };
      }),
    ).then((results) => {
      const newCounts: Record<string, number> = {};
      for (const r of results) {
        if (r.status === "fulfilled") {
          newCounts[r.value.kind] = r.value.count;
        }
      }
      counts.value = newCounts;
    });
  }, [selectedNamespace.value]);

  function isActive(href: string): boolean {
    if (href === currentPath) return true;
    // Match sub-paths (e.g., /workloads/deployments/ns/name matches /workloads/deployments tab)
    return currentPath.startsWith(href + "/");
  }

  return (
    <div class="flex gap-0.5 border-b mb-5" style={{ borderColor: "var(--border-primary)" }}>
      {tabs.map((tab) => {
        const active = isActive(tab.href);
        return (
          <a
            key={tab.href}
            href={tab.href}
            class="flex items-center gap-1.5 px-4 py-2 text-[13px] font-medium transition-colors border-b-2"
            style={{
              color: active ? "var(--accent)" : "var(--text-muted)",
              borderBottomColor: active ? "var(--accent)" : "transparent",
              marginBottom: "-1px",
            }}
          >
            {tab.label}
            {tab.count && tab.kind && counts.value[tab.kind] !== undefined && (
              <span
                class="rounded-full px-1.5 py-0.5 text-[11px] font-mono"
                style={{
                  background: active ? "var(--accent-dim)" : "var(--bg-elevated)",
                  color: active ? "var(--accent)" : "var(--text-muted)",
                }}
              >
                {counts.value[tab.kind]}
              </span>
            )}
          </a>
        );
      })}
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/islands/SubNav.tsx
git commit -m "feat: add sub-navigation tabs island with resource counts"
```

---

## Task 5: Command Palette

**Files:**
- Create: `frontend/lib/fuzzy-search.ts`
- Replace stub: `frontend/islands/CommandPalette.tsx`

- [ ] **Step 1: Create `frontend/lib/fuzzy-search.ts`**

```typescript
/**
 * Lightweight fuzzy search for the command palette.
 * No external dependency — simple substring + prefix scoring.
 */

export interface SearchItem {
  id: string;
  type: "resource" | "action" | "navigation";
  label: string;
  detail?: string;
  href?: string;
  icon?: string;
  action?: () => void;
}

/** Score a query against a target string. Higher = better match. Returns 0 for no match. */
function score(query: string, target: string): number {
  const q = query.toLowerCase();
  const t = target.toLowerCase();

  // Exact match
  if (t === q) return 100;
  // Starts with
  if (t.startsWith(q)) return 80;
  // Contains
  if (t.includes(q)) return 60;
  // Fuzzy: all chars in order
  let qi = 0;
  for (let ti = 0; ti < t.length && qi < q.length; ti++) {
    if (t[ti] === q[qi]) qi++;
  }
  if (qi === q.length) return 40;

  return 0;
}

/** Search items by query, returning sorted results. */
export function fuzzySearch(items: SearchItem[], query: string): SearchItem[] {
  if (!query.trim()) return items.slice(0, 8);

  return items
    .map((item) => ({
      item,
      score: Math.max(score(query, item.label), score(query, item.detail ?? "")),
    }))
    .filter((r) => r.score > 0)
    .sort((a, b) => b.score - a.score)
    .slice(0, 12)
    .map((r) => r.item);
}
```

- [ ] **Step 2: Implement `frontend/islands/CommandPalette.tsx`**

```tsx
import { useSignal, useComputed } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";
import { useEffect, useRef } from "preact/hooks";
import { fuzzySearch, type SearchItem } from "@/lib/fuzzy-search.ts";
import { DOMAIN_SECTIONS, SETTINGS_SECTION } from "@/lib/constants.ts";

/** Build the static search index from navigation + common actions. */
function buildSearchIndex(): SearchItem[] {
  const items: SearchItem[] = [];

  // Navigation items
  for (const section of [...DOMAIN_SECTIONS, SETTINGS_SECTION]) {
    items.push({
      id: `nav-${section.id}`,
      type: "navigation",
      label: section.label,
      detail: `Go to ${section.label}`,
      href: section.href,
      icon: section.icon,
    });
    for (const tab of section.tabs ?? []) {
      items.push({
        id: `nav-${section.id}-${tab.label}`,
        type: "navigation",
        label: tab.label,
        detail: `${section.label} > ${tab.label}`,
        href: tab.href,
      });
    }
  }

  // Quick actions
  items.push(
    { id: "action-deploy", type: "action", label: "Create Deployment", detail: "wizard", href: "/workloads/deployments/new" },
    { id: "action-service", type: "action", label: "Create Service", detail: "wizard", href: "/networking/services/new" },
    { id: "action-yaml", type: "action", label: "Apply YAML", detail: "tools", href: "/tools/yaml-apply" },
    { id: "action-configmap", type: "action", label: "Create ConfigMap", detail: "wizard", href: "/config/configmaps/new" },
    { id: "action-secret", type: "action", label: "Create Secret", detail: "wizard", href: "/config/secrets/new" },
    { id: "action-ingress", type: "action", label: "Create Ingress", detail: "wizard", href: "/networking/ingresses/new" },
    { id: "action-hpa", type: "action", label: "Create HPA", detail: "wizard", href: "/scaling/hpas/new" },
    { id: "action-namespace", type: "action", label: "Create Namespace", detail: "wizard", href: "/cluster/namespaces/new" },
  );

  return items;
}

export default function CommandPalette() {
  if (!IS_BROWSER) return null;

  const open = useSignal(false);
  const query = useSignal("");
  const selectedIndex = useSignal(0);
  const inputRef = useRef<HTMLInputElement>(null);

  const searchIndex = useSignal<SearchItem[]>(buildSearchIndex());

  const results = useComputed(() => fuzzySearch(searchIndex.value, query.value));

  // Keyboard shortcuts
  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault();
        open.value = !open.value;
        if (!open.value) query.value = "";
      }
      if (e.key === "Escape" && open.value) {
        open.value = false;
        query.value = "";
      }
    }
    globalThis.addEventListener("keydown", onKeyDown);
    globalThis.addEventListener("open-command-palette", () => { open.value = true; });
    return () => {
      globalThis.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  // Focus input when opened
  useEffect(() => {
    if (open.value) {
      setTimeout(() => inputRef.current?.focus(), 50);
      selectedIndex.value = 0;
    }
  }, [open.value]);

  // Reset selection on query change
  useEffect(() => {
    selectedIndex.value = 0;
  }, [query.value]);

  function handleSelect(item: SearchItem) {
    if (item.href) {
      globalThis.location.href = item.href;
    } else if (item.action) {
      item.action();
    }
    open.value = false;
    query.value = "";
  }

  function handleKeyDown(e: KeyboardEvent) {
    const len = results.value.length;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      selectedIndex.value = (selectedIndex.value + 1) % len;
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      selectedIndex.value = (selectedIndex.value - 1 + len) % len;
    } else if (e.key === "Enter" && results.value[selectedIndex.value]) {
      e.preventDefault();
      handleSelect(results.value[selectedIndex.value]);
    }
  }

  if (!open.value) return null;

  // Group results by type
  const grouped: Record<string, SearchItem[]> = {};
  for (const item of results.value) {
    const key = item.type === "action" ? "Quick Actions" : item.type === "resource" ? "Resources" : "Navigation";
    (grouped[key] ??= []).push(item);
  }

  let flatIndex = 0;

  return (
    <div
      class="fixed inset-0 flex items-start justify-center pt-[20vh]"
      style={{ background: "rgba(0,0,0,0.6)", backdropFilter: "blur(4px)", zIndex: 1000 }}
      onClick={() => { open.value = false; query.value = ""; }}
    >
      <div
        class="w-[560px] rounded-xl border overflow-hidden"
        style={{
          background: "var(--bg-surface)",
          borderColor: "var(--border-primary)",
          boxShadow: "0 24px 64px rgba(0,0,0,0.5)",
        }}
        onClick={(e) => e.stopPropagation()}
      >
        {/* Search input */}
        <div class="flex items-center gap-2.5 px-4 py-3 border-b" style={{ borderColor: "var(--border-primary)" }}>
          <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" width="18" height="18" style={{ color: "var(--text-muted)" }}>
            <circle cx="7" cy="7" r="4.5" /><path d="M10.5 10.5L14 14" />
          </svg>
          <input
            ref={inputRef}
            type="text"
            value={query.value}
            onInput={(e) => { query.value = (e.target as HTMLInputElement).value; }}
            onKeyDown={handleKeyDown}
            placeholder="Search resources, actions, namespaces..."
            class="flex-1 bg-transparent border-none text-[15px] outline-none"
            style={{ color: "var(--text-primary)", fontFamily: "var(--font-sans)" }}
          />
        </div>

        {/* Results */}
        <div class="max-h-[320px] overflow-y-auto p-2">
          {Object.entries(grouped).map(([section, items]) => (
            <div key={section}>
              <div
                class="px-2.5 py-1.5 text-[10px] font-semibold uppercase tracking-[0.08em]"
                style={{ color: "var(--text-muted)" }}
              >
                {section}
              </div>
              {items.map((item) => {
                const idx = flatIndex++;
                return (
                  <div
                    key={item.id}
                    class="flex items-center gap-2.5 px-2.5 py-2 rounded-md cursor-pointer transition-colors"
                    style={{
                      background: idx === selectedIndex.value ? "var(--bg-elevated)" : "transparent",
                    }}
                    onClick={() => handleSelect(item)}
                    onMouseEnter={() => { selectedIndex.value = idx; }}
                  >
                    <div
                      class="flex h-7 w-7 items-center justify-center rounded-md"
                      style={{ background: "var(--accent-dim)", color: "var(--accent)" }}
                    >
                      <svg viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="1.5" width="14" height="14">
                        {item.type === "action"
                          ? <path d="M4 7h6M7 4v6" />
                          : <circle cx="7" cy="7" r="4.5" />}
                      </svg>
                    </div>
                    <span class="flex-1 text-[13px]" style={{ color: "var(--text-primary)" }}>{item.label}</span>
                    {item.detail && (
                      <span class="text-[11px] font-mono" style={{ color: "var(--text-muted)" }}>{item.detail}</span>
                    )}
                  </div>
                );
              })}
            </div>
          ))}
        </div>

        {/* Footer */}
        <div
          class="flex items-center gap-4 px-4 py-2.5 border-t text-[11px]"
          style={{ borderColor: "var(--border-primary)", color: "var(--text-muted)" }}
        >
          <span><kbd class="rounded border px-1 py-0.5 font-mono text-[10px]" style={{ background: "var(--bg-base)", borderColor: "var(--border-primary)" }}>↑↓</kbd> Navigate</span>
          <span><kbd class="rounded border px-1 py-0.5 font-mono text-[10px]" style={{ background: "var(--bg-base)", borderColor: "var(--border-primary)" }}>↵</kbd> Select</span>
          <span><kbd class="rounded border px-1 py-0.5 font-mono text-[10px]" style={{ background: "var(--bg-base)", borderColor: "var(--border-primary)" }}>esc</kbd> Close</span>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Verify command palette opens with Cmd+K**

Run: `cd frontend && deno task dev`
Expected: Press Cmd+K → palette opens. Type to search. Arrow keys to navigate. Enter to select. Esc to close.

- [ ] **Step 4: Commit**

```bash
git add frontend/lib/fuzzy-search.ts frontend/islands/CommandPalette.tsx
git commit -m "feat: add command palette with fuzzy search (Cmd+K)"
```

---

## Task 6: Health Score Engine

**Files:**
- Create: `frontend/lib/health-score.ts`
- Create: `frontend/islands/HealthScoreRing.tsx`

- [ ] **Step 1: Create `frontend/lib/health-score.ts`**

```typescript
/**
 * Cluster health score calculation.
 * Weighted aggregate of node health, pod readiness, service availability, and alert state.
 */

export interface HealthMetrics {
  nodesTotal: number;
  nodesReady: number;
  podsTotal: number;
  podsRunning: number;
  podsPending: number;
  podsFailed: number;
  servicesTotal: number;
  activeAlerts: number;
  criticalAlerts: number;
  cpuUtilization: number; // 0-100
  memoryUtilization: number; // 0-100
}

export interface HealthScore {
  overall: number; // 0-100
  nodes: number;
  pods: number;
  services: number;
  alerts: number;
}

const WEIGHTS = {
  nodes: 0.30,
  pods: 0.30,
  services: 0.15,
  alerts: 0.25,
};

export function calculateHealthScore(metrics: HealthMetrics): HealthScore {
  // Node score: % of nodes ready
  const nodes = metrics.nodesTotal > 0
    ? Math.round((metrics.nodesReady / metrics.nodesTotal) * 100)
    : 100;

  // Pod score: % of pods running, penalize failed more than pending
  const podsHealthy = metrics.podsTotal > 0
    ? (metrics.podsRunning / metrics.podsTotal) * 100
    : 100;
  const podsPenalty = metrics.podsTotal > 0
    ? ((metrics.podsFailed * 3 + metrics.podsPending) / metrics.podsTotal) * 15
    : 0;
  const pods = Math.max(0, Math.round(podsHealthy - podsPenalty));

  // Services: 100 if any exist, 0 if none expected (pass-through)
  const services = metrics.servicesTotal > 0 ? 100 : 100;

  // Alerts: 100 = no alerts, deduct for active alerts, more for critical
  const alertPenalty = (metrics.activeAlerts * 3) + (metrics.criticalAlerts * 10);
  const alerts = Math.max(0, Math.min(100, 100 - alertPenalty));

  const overall = Math.round(
    nodes * WEIGHTS.nodes +
    pods * WEIGHTS.pods +
    services * WEIGHTS.services +
    alerts * WEIGHTS.alerts,
  );

  return { overall, nodes, pods, services, alerts };
}
```

- [ ] **Step 2: Create `frontend/islands/HealthScoreRing.tsx`**

```tsx
import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import { calculateHealthScore, type HealthScore, type HealthMetrics } from "@/lib/health-score.ts";
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";

export default function HealthScoreRing() {
  const score = useSignal<HealthScore | null>(null);
  const loading = useSignal(true);

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function fetchMetrics() {
      try {
        // Fetch all needed data in parallel
        const [nodesRes, podsRes, servicesRes, alertsRes] = await Promise.allSettled([
          apiGet<{ items: { status?: { conditions?: { type: string; status: string }[] } }[] }>("/v1/resources/nodes"),
          apiGet<{ items: { status?: { phase?: string } }[]; metadata?: { total?: number } }>("/v1/resources/pods"),
          apiGet<{ items: unknown[]; metadata?: { total?: number } }>("/v1/resources/services"),
          apiGet<{ items: { status?: string; labels?: { severity?: string } }[] }>("/v1/alerting/active").catch(() => ({ data: { items: [] } })),
        ]);

        const nodes = nodesRes.status === "fulfilled" ? nodesRes.value.data.items ?? [] : [];
        const pods = podsRes.status === "fulfilled" ? podsRes.value.data.items ?? [] : [];
        const services = servicesRes.status === "fulfilled" ? servicesRes.value.data.items ?? [] : [];
        const alerts = alertsRes.status === "fulfilled" ? (alertsRes.value as any).data?.items ?? [] : [];

        const nodesReady = nodes.filter((n) =>
          n.status?.conditions?.some((c) => c.type === "Ready" && c.status === "True")
        ).length;

        const podsRunning = pods.filter((p) => p.status?.phase === "Running").length;
        const podsPending = pods.filter((p) => p.status?.phase === "Pending").length;
        const podsFailed = pods.filter((p) => p.status?.phase === "Failed").length;

        const criticalAlerts = alerts.filter((a: any) => a.labels?.severity === "critical").length;

        const metrics: HealthMetrics = {
          nodesTotal: nodes.length,
          nodesReady,
          podsTotal: pods.length,
          podsRunning,
          podsPending,
          podsFailed,
          servicesTotal: services.length,
          activeAlerts: alerts.length,
          criticalAlerts,
          cpuUtilization: 0,
          memoryUtilization: 0,
        };

        score.value = calculateHealthScore(metrics);
      } catch {
        // Fallback score
        score.value = { overall: 0, nodes: 0, pods: 0, services: 0, alerts: 0 };
      } finally {
        loading.value = false;
      }
    }

    fetchMetrics();
    // Refresh every 30 seconds
    const interval = setInterval(fetchMetrics, 30_000);
    return () => clearInterval(interval);
  }, []);

  if (loading.value) {
    return (
      <div class="flex flex-col items-center p-6">
        <div class="animate-pulse rounded-full" style={{ width: "160px", height: "160px", background: "var(--bg-elevated)" }} />
      </div>
    );
  }

  const s = score.value!;

  const subScores = [
    { label: "Nodes", value: s.nodes, color: s.nodes >= 90 ? "var(--success)" : s.nodes >= 70 ? "var(--warning)" : "var(--error)" },
    { label: "Pods", value: s.pods, color: s.pods >= 90 ? "var(--success)" : s.pods >= 70 ? "var(--warning)" : "var(--error)" },
    { label: "Services", value: s.services, color: s.services >= 90 ? "var(--success)" : "var(--warning)" },
    { label: "Alerts", value: s.alerts, color: s.alerts >= 90 ? "var(--accent)" : s.alerts >= 70 ? "var(--warning)" : "var(--error)" },
  ];

  return (
    <div class="flex flex-col items-center">
      <GaugeRing
        value={s.overall}
        size={160}
        strokeWidth={8}
        color="var(--accent)"
        secondaryColor="var(--success)"
        label="Health"
      />
      <div class="grid grid-cols-4 gap-2 w-full mt-4">
        {subScores.map((sub) => (
          <div
            key={sub.label}
            class="text-center rounded-md border p-2.5"
            style={{ background: "var(--bg-elevated)", borderColor: "var(--border-subtle)" }}
          >
            <div class="text-lg font-semibold font-mono" style={{ color: sub.color }}>{sub.value}%</div>
            <div class="text-[10px] uppercase tracking-wide mt-0.5" style={{ color: "var(--text-muted)" }}>{sub.label}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Commit**

```bash
git add frontend/lib/health-score.ts frontend/islands/HealthScoreRing.tsx
git commit -m "feat: add cluster health score calculation and ring visualization"
```

---

## Task 7: Overview Dashboard V2

**Files:**
- Create: `frontend/islands/DashboardV2.tsx`
- Create: `frontend/islands/MetricCard.tsx`
- Create: `frontend/islands/UtilizationGauge.tsx`
- Modify: `frontend/routes/index.tsx`

- [ ] **Step 1: Create `frontend/islands/MetricCard.tsx`**

A reusable metric card with value, label, status badge, and sparkline. Pattern taken from mockup `01-overview-dashboard.html`.

```tsx
import { SparklineChart } from "@/components/ui/SparklineChart.tsx";
import { StatusDot } from "@/components/ui/StatusDot.tsx";

interface MetricCardProps {
  value: number | string;
  label: string;
  status: "success" | "warning" | "error" | "info";
  statusText: string;
  sparklineData?: number[];
  sparklineColor?: string;
  href?: string;
}

export default function MetricCard({ value, label, status, statusText, sparklineData, sparklineColor, href }: MetricCardProps) {
  const content = (
    <div
      class="rounded-[var(--radius)] border p-4 transition-colors cursor-pointer"
      style={{ background: "var(--bg-surface)", borderColor: "var(--border-primary)" }}
    >
      <div class="flex items-center justify-between mb-2">
        <div
          class="flex h-8 w-8 items-center justify-center rounded-md"
          style={{
            background: status === "warning" ? "var(--warning-dim)" : "var(--success-dim)",
            color: status === "warning" ? "var(--warning)" : "var(--success)",
          }}
        >
          <StatusDot status={status} size={8} />
        </div>
        <span
          class="text-[10px] font-medium uppercase tracking-wide rounded-full px-2 py-0.5"
          style={{
            background: status === "warning" ? "var(--warning-dim)" : "var(--success-dim)",
            color: status === "warning" ? "var(--warning)" : "var(--success)",
          }}
        >
          {statusText}
        </span>
      </div>
      <div class="text-[28px] font-bold font-mono leading-tight" style={{ color: "var(--text-primary)" }}>
        {value}
      </div>
      <div class="text-xs mt-0.5" style={{ color: "var(--text-muted)" }}>{label}</div>
      {sparklineData && sparklineData.length > 1 && (
        <div class="mt-3">
          <SparklineChart data={sparklineData} color={sparklineColor ?? "var(--success)"} height={32} />
        </div>
      )}
    </div>
  );

  return href ? <a href={href}>{content}</a> : content;
}
```

- [ ] **Step 2: Create `frontend/islands/UtilizationGauge.tsx`**

```tsx
import { GaugeRing } from "@/components/ui/GaugeRing.tsx";

interface UtilizationGaugeProps {
  title: string;
  value: number;
  used: string;
  total: string;
  requests?: string;
  limits?: string;
  color: string;
  secondaryColor?: string;
  trendData?: number[];
}

export default function UtilizationGauge({ title, value, used, total, requests, limits, color, secondaryColor, trendData }: UtilizationGaugeProps) {
  return (
    <div
      class="rounded-[var(--radius)] border p-5"
      style={{ background: "var(--bg-surface)", borderColor: "var(--border-primary)" }}
    >
      <h3
        class="text-[11px] font-semibold uppercase tracking-[0.08em] mb-4"
        style={{ color: "var(--text-muted)" }}
      >
        {title}
      </h3>
      <div class="flex items-center gap-6">
        <GaugeRing value={value} size={100} strokeWidth={10} color={color} secondaryColor={secondaryColor} />
        <div class="flex-1 space-y-1.5">
          <div class="flex justify-between text-[13px] py-1.5 border-b" style={{ borderColor: "var(--border-subtle)" }}>
            <span style={{ color: "var(--text-secondary)" }}>Used</span>
            <span class="font-mono font-medium" style={{ color: "var(--text-primary)" }}>{used} / {total}</span>
          </div>
          {requests && (
            <div class="flex justify-between text-[13px] py-1.5 border-b" style={{ borderColor: "var(--border-subtle)" }}>
              <span style={{ color: "var(--text-secondary)" }}>Requests</span>
              <span class="font-mono font-medium" style={{ color: "var(--text-primary)" }}>{requests}</span>
            </div>
          )}
          {limits && (
            <div class="flex justify-between text-[13px] py-1.5">
              <span style={{ color: "var(--text-secondary)" }}>Limits</span>
              <span class="font-mono font-medium" style={{ color: "var(--text-primary)" }}>{limits}</span>
            </div>
          )}
        </div>
      </div>
      {trendData && trendData.length > 1 && (
        <div class="mt-4">
          <svg viewBox="0 0 300 48" preserveAspectRatio="none" width="100%" height="48">
            <defs>
              <linearGradient id={`trend-${title}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stop-color={color} stop-opacity="0.15" />
                <stop offset="100%" stop-color={color} stop-opacity="0" />
              </linearGradient>
            </defs>
            {/* Render trend as path */}
          </svg>
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 3: Create `frontend/islands/DashboardV2.tsx`**

This is the main overview dashboard, composing HealthScoreRing, MetricCards, UtilizationGauges, ClusterTopology (stub for now), and an events stream.

```tsx
import { useSignal } from "@preact/signals";
import { useEffect } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { apiGet } from "@/lib/api.ts";
import HealthScoreRing from "@/islands/HealthScoreRing.tsx";
import MetricCard from "@/islands/MetricCard.tsx";
import UtilizationGauge from "@/islands/UtilizationGauge.tsx";
import type { K8sResource, K8sEvent } from "@/lib/k8s-types.ts";
import { age } from "@/lib/format.ts";

export default function DashboardV2() {
  const clusterInfo = useSignal<{ version?: string; platform?: string; nodeCount?: number } | null>(null);
  const counts = useSignal<Record<string, number>>({});
  const events = useSignal<K8sEvent[]>([]);
  const cpuUtil = useSignal(0);
  const memUtil = useSignal(0);
  const loading = useSignal(true);

  useEffect(() => {
    if (!IS_BROWSER) return;

    async function loadDashboard() {
      try {
        const [infoRes, deployRes, podRes, svcRes, nsRes, eventsRes] = await Promise.allSettled([
          apiGet<{ kubernetesVersion: string; platform: string }>("/v1/cluster/info"),
          apiGet<{ metadata?: { total?: number } }>("/v1/resources/deployments?limit=1"),
          apiGet<{ metadata?: { total?: number } }>("/v1/resources/pods?limit=1"),
          apiGet<{ metadata?: { total?: number } }>("/v1/resources/services?limit=1"),
          apiGet<{ metadata?: { total?: number } }>("/v1/resources/namespaces?limit=1"),
          apiGet<{ items: K8sEvent[] }>("/v1/resources/events?limit=10"),
        ]);

        if (infoRes.status === "fulfilled") {
          const d = infoRes.value.data as any;
          clusterInfo.value = { version: d.kubernetesVersion, platform: d.platform, nodeCount: d.nodeCount };
        }

        counts.value = {
          deployments: deployRes.status === "fulfilled" ? (deployRes.value.data as any).metadata?.total ?? 0 : 0,
          pods: podRes.status === "fulfilled" ? (podRes.value.data as any).metadata?.total ?? 0 : 0,
          services: svcRes.status === "fulfilled" ? (svcRes.value.data as any).metadata?.total ?? 0 : 0,
          namespaces: nsRes.status === "fulfilled" ? (nsRes.value.data as any).metadata?.total ?? 0 : 0,
        };

        if (eventsRes.status === "fulfilled") {
          events.value = (eventsRes.value.data.items ?? []).slice(0, 8);
        }

        // Try to fetch Prometheus metrics for utilization
        try {
          const cpuRes = await apiGet<{ data?: { result?: { value?: [number, string] }[] } }>(
            "/v1/monitoring/query?query=" + encodeURIComponent('100 - (avg(rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)')
          );
          const cpuVal = cpuRes.data?.data?.result?.[0]?.value?.[1];
          if (cpuVal) cpuUtil.value = Math.round(parseFloat(cpuVal));
        } catch { /* Prometheus may not be available */ }

        try {
          const memRes = await apiGet<{ data?: { result?: { value?: [number, string] }[] } }>(
            "/v1/monitoring/query?query=" + encodeURIComponent('(1 - (node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)) * 100')
          );
          const memVal = memRes.data?.data?.result?.[0]?.value?.[1];
          if (memVal) memUtil.value = Math.round(parseFloat(memVal));
        } catch { /* Prometheus may not be available */ }
      } finally {
        loading.value = false;
      }
    }

    loadDashboard();
  }, []);

  if (loading.value) {
    return (
      <div class="space-y-4">
        <div class="animate-pulse rounded-lg h-8 w-48" style={{ background: "var(--bg-elevated)" }} />
        <div class="grid grid-cols-12 gap-4">
          <div class="col-span-4 animate-pulse rounded-lg h-72" style={{ background: "var(--bg-elevated)" }} />
          <div class="col-span-8 grid grid-cols-4 gap-4">
            {[1,2,3,4].map((i) => <div key={i} class="animate-pulse rounded-lg h-32" style={{ background: "var(--bg-elevated)" }} />)}
          </div>
        </div>
      </div>
    );
  }

  return (
    <div>
      {/* Header */}
      <div class="flex items-center justify-between mb-5">
        <div>
          <h1 class="text-xl font-semibold" style={{ letterSpacing: "-0.02em" }}>Cluster Overview</h1>
          <p class="text-[13px] mt-0.5" style={{ color: "var(--text-muted)" }}>
            {clusterInfo.value?.platform ?? "local"} · Kubernetes {clusterInfo.value?.version ?? "unknown"} · {clusterInfo.value?.nodeCount ?? 0} nodes
          </p>
        </div>
        <div class="flex gap-2">
          <a
            href="/workloads/deployments/new"
            class="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-[13px] font-medium transition-colors"
            style={{ borderColor: "var(--border-primary)", color: "var(--text-secondary)" }}
          >
            <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" width="15" height="15"><path d="M4 8h8M8 4v8" /></svg>
            Deploy
          </a>
          <a
            href="/tools/yaml-apply"
            class="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-[13px] font-medium transition-colors"
            style={{ borderColor: "var(--border-primary)", color: "var(--text-secondary)" }}
          >
            YAML
          </a>
        </div>
      </div>

      {/* Dashboard Grid */}
      <div class="grid grid-cols-12 gap-4">
        {/* Health Score */}
        <div class="col-span-4 rounded-[var(--radius)] border p-5" style={{ background: "var(--bg-surface)", borderColor: "var(--border-primary)" }}>
          <div class="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[0.08em] mb-4" style={{ color: "var(--text-muted)" }}>
            <span class="h-1.5 w-1.5 rounded-full animate-pulse-glow" style={{ background: "var(--success)" }} />
            Cluster Health
          </div>
          <HealthScoreRing />
        </div>

        {/* Metric Cards */}
        <div class="col-span-8 grid grid-cols-4 gap-4">
          <MetricCard
            value={clusterInfo.value?.nodeCount ?? 0}
            label="Nodes"
            status="success"
            statusText="Healthy"
            href="/cluster/nodes"
            sparklineData={[5,5,5,5,5,5,5,5]}
            sparklineColor="var(--success)"
          />
          <MetricCard
            value={counts.value.pods ?? 0}
            label="Pods"
            status={counts.value.pods > 0 ? "success" : "info"}
            statusText="Running"
            href="/workloads/pods"
            sparklineData={[90, 95, 92, 98, 105, 110, 115, 120, 125, 127]}
            sparklineColor="var(--success)"
          />
          <MetricCard
            value={counts.value.services ?? 0}
            label="Services"
            status="success"
            statusText="Active"
            href="/networking/services"
            sparklineData={[40, 41, 42, 42, 43, 44, 44, 45]}
            sparklineColor="var(--success)"
          />
          <MetricCard
            value={counts.value.deployments ?? 0}
            label="Deployments"
            status="success"
            statusText="Ready"
            href="/workloads/deployments"
            sparklineData={[28, 29, 30, 30, 31, 31, 32, 32]}
            sparklineColor="var(--success)"
          />
        </div>

        {/* CPU Utilization */}
        <div class="col-span-6">
          <UtilizationGauge
            title="CPU Utilization"
            value={cpuUtil.value}
            used={`${cpuUtil.value}%`}
            total="100%"
            color="var(--accent)"
            secondaryColor="var(--success)"
          />
        </div>

        {/* Memory Utilization */}
        <div class="col-span-6">
          <UtilizationGauge
            title="Memory Utilization"
            value={memUtil.value}
            used={`${memUtil.value}%`}
            total="100%"
            color="var(--accent-secondary)"
            secondaryColor="#FF79C6"
          />
        </div>

        {/* Topology placeholder */}
        <div class="col-span-7 rounded-[var(--radius)] border p-5 min-h-[280px]" style={{ background: "var(--bg-surface)", borderColor: "var(--border-primary)" }}>
          <div class="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[0.08em] mb-4" style={{ color: "var(--text-muted)" }}>
            <span class="h-1.5 w-1.5 rounded-full animate-pulse-glow" style={{ background: "var(--success)" }} />
            Cluster Topology
          </div>
          <div class="flex items-center justify-center h-52 text-sm" style={{ color: "var(--text-muted)" }}>
            Topology visualization — implemented in Task 10
          </div>
        </div>

        {/* Recent Events */}
        <div class="col-span-5 rounded-[var(--radius)] border p-5 min-h-[280px]" style={{ background: "var(--bg-surface)", borderColor: "var(--border-primary)" }}>
          <h3 class="text-[11px] font-semibold uppercase tracking-[0.08em] mb-4" style={{ color: "var(--text-muted)" }}>
            Recent Events
          </h3>
          <div class="space-y-0.5">
            {events.value.map((evt, i) => (
              <div
                key={i}
                class="flex items-start gap-2.5 rounded-md px-2.5 py-2 transition-colors cursor-pointer"
                style={{ ["--hover-bg" as any]: "var(--bg-elevated)" }}
              >
                <span
                  class="mt-1.5 h-1.5 w-1.5 rounded-full flex-shrink-0"
                  style={{
                    background: evt.type === "Warning" ? "var(--warning)" : "var(--accent)",
                  }}
                />
                <div class="flex-1 min-w-0">
                  <div class="text-xs truncate" style={{ color: "var(--text-secondary)" }}>
                    {evt.message}
                  </div>
                  <div class="text-[10px] font-mono mt-0.5" style={{ color: "var(--text-muted)" }}>
                    {evt.involvedObject?.kind}/{evt.involvedObject?.name} · {age(evt.metadata.creationTimestamp)}
                  </div>
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Update `frontend/routes/index.tsx`**

```tsx
import { define } from "@/utils.ts";
import DashboardV2 from "@/islands/DashboardV2.tsx";

export default define.page(function Home() {
  return <DashboardV2 />;
});
```

- [ ] **Step 5: Verify the new dashboard renders**

Run: `cd frontend && deno task dev`
Expected: Root page shows the redesigned dashboard with health score ring, metric cards, utilization gauges, events stream.

- [ ] **Step 6: Commit**

```bash
git add frontend/islands/DashboardV2.tsx frontend/islands/MetricCard.tsx frontend/islands/UtilizationGauge.tsx frontend/routes/index.tsx
git commit -m "feat: implement overview dashboard v2 with health score and metric cards"
```

---

## Task 8: Quick Actions FAB

**Files:**
- Replace stub: `frontend/islands/QuickActionsFab.tsx`

- [ ] **Step 1: Implement `frontend/islands/QuickActionsFab.tsx`**

```tsx
import { useSignal } from "@preact/signals";
import { IS_BROWSER } from "fresh/runtime";

const ACTIONS = [
  { label: "New Deployment", href: "/workloads/deployments/new", icon: "M3 4h10v8H3zM6 4V2M10 4V2M3 8h10" },
  { label: "New Service", href: "/networking/services/new", icon: "M8 2a6 6 0 100 12A6 6 0 008 2zM8 5v6M5 8h6" },
  { label: "Apply YAML", href: "/tools/yaml-apply", icon: "M2 3h12v10H2zM5 8h6" },
  { label: "Scale Resource", href: "#", icon: "M4 8h8M8 4v8" },
];

export default function QuickActionsFab() {
  if (!IS_BROWSER) return null;

  const expanded = useSignal(false);

  return (
    <div
      class="fixed bottom-6 right-6 flex flex-col-reverse items-end gap-2"
      style={{ zIndex: 50 }}
      onMouseEnter={() => { expanded.value = true; }}
      onMouseLeave={() => { expanded.value = false; }}
    >
      {/* Menu items */}
      {expanded.value && (
        <div class="flex flex-col gap-1.5 mb-1">
          {ACTIONS.map((action) => (
            <a
              key={action.label}
              href={action.href}
              class="flex items-center gap-2 rounded-[var(--radius)] border px-3.5 py-2 text-xs font-medium whitespace-nowrap transition-all"
              style={{
                background: "var(--bg-surface)",
                borderColor: "var(--border-primary)",
                color: "var(--text-primary)",
              }}
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="var(--accent)" stroke-width="1.5" width="14" height="14">
                <path d={action.icon} />
              </svg>
              {action.label}
            </a>
          ))}
        </div>
      )}

      {/* FAB button */}
      <button
        class="flex h-12 w-12 items-center justify-center rounded-[14px] border-none text-white shadow-lg transition-all"
        style={{
          background: "linear-gradient(135deg, var(--accent), var(--accent-secondary))",
          boxShadow: "0 4px 16px var(--accent-glow)",
        }}
        onClick={() => { expanded.value = !expanded.value; }}
      >
        <svg
          viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="2" width="22" height="22"
          style={{ transition: "transform 0.2s ease", transform: expanded.value ? "rotate(45deg)" : "none" }}
        >
          <path d="M10 4v12M4 10h12" />
        </svg>
      </button>
    </div>
  );
}
```

- [ ] **Step 2: Commit**

```bash
git add frontend/islands/QuickActionsFab.tsx
git commit -m "feat: implement quick actions floating action button"
```

---

## Task 9: Split Pane & Theme Selector

**Files:**
- Create: `frontend/islands/SplitPane.tsx`
- Create: `frontend/lib/hooks/use-split-pane.ts`
- Create: `frontend/islands/ThemeSelector.tsx`

- [ ] **Step 1: Create `frontend/lib/hooks/use-split-pane.ts`**

```typescript
import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";

export function useSplitPane(defaultRatio = 0.5) {
  const ratio = useSignal(defaultRatio);
  const dragging = useSignal(false);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    function onMouseMove(e: MouseEvent) {
      if (!dragging.value || !containerRef.current) return;
      const rect = containerRef.current.getBoundingClientRect();
      const newRatio = (e.clientX - rect.left) / rect.width;
      ratio.value = Math.max(0.25, Math.min(0.75, newRatio));
    }

    function onMouseUp() {
      dragging.value = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    }

    globalThis.addEventListener("mousemove", onMouseMove);
    globalThis.addEventListener("mouseup", onMouseUp);
    return () => {
      globalThis.removeEventListener("mousemove", onMouseMove);
      globalThis.removeEventListener("mouseup", onMouseUp);
    };
  }, []);

  function startDrag() {
    dragging.value = true;
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
  }

  return { ratio, containerRef, startDrag, dragging };
}
```

- [ ] **Step 2: Create `frontend/islands/SplitPane.tsx`**

```tsx
import type { ComponentChildren } from "preact";
import { useSplitPane } from "@/lib/hooks/use-split-pane.ts";

interface SplitPaneProps {
  left: ComponentChildren;
  right: ComponentChildren;
  defaultRatio?: number;
}

export default function SplitPane({ left, right, defaultRatio = 0.5 }: SplitPaneProps) {
  const { ratio, containerRef, startDrag, dragging } = useSplitPane(defaultRatio);

  return (
    <div ref={containerRef} class="flex h-full overflow-hidden">
      <div class="overflow-y-auto p-5" style={{ width: `${ratio.value * 100}%` }}>
        {left}
      </div>
      <div
        class="w-1 cursor-col-resize flex-shrink-0 transition-colors"
        style={{
          background: dragging.value ? "var(--accent)" : "var(--border-primary)",
        }}
        onMouseDown={startDrag}
      />
      <div class="flex-1 overflow-y-auto p-5" style={{ background: "var(--bg-base)" }}>
        {right}
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Create `frontend/islands/ThemeSelector.tsx`**

```tsx
import { useSignal } from "@preact/signals";
import { THEMES, currentTheme, applyTheme } from "@/lib/themes.ts";

export default function ThemeSelector() {
  const open = useSignal(false);

  return (
    <div class="relative">
      <button
        onClick={() => { open.value = !open.value; }}
        class="flex items-center gap-2 rounded-md border px-3 py-1.5 text-sm"
        style={{ background: "var(--bg-elevated)", borderColor: "var(--border-primary)", color: "var(--text-secondary)" }}
      >
        <span class="h-3 w-3 rounded-full" style={{ background: "var(--accent)" }} />
        {THEMES.find((t) => t.id === currentTheme.value)?.name ?? "Nexus"}
        <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" width="12" height="12">
          <path d="M4 6l4 4 4-4" />
        </svg>
      </button>

      {open.value && (
        <div
          class="absolute right-0 top-full mt-2 w-56 rounded-lg border p-1.5 shadow-xl"
          style={{ background: "var(--bg-surface)", borderColor: "var(--border-primary)", zIndex: 50 }}
        >
          {THEMES.map((theme) => (
            <button
              key={theme.id}
              onClick={() => { applyTheme(theme.id); open.value = false; }}
              class="flex items-center gap-3 w-full rounded-md px-3 py-2 text-sm text-left transition-colors"
              style={{
                background: currentTheme.value === theme.id ? "var(--accent-dim)" : "transparent",
                color: currentTheme.value === theme.id ? "var(--accent)" : "var(--text-secondary)",
              }}
            >
              <div class="flex gap-1">
                <span class="h-3 w-3 rounded-full" style={{ background: theme.colors.accent }} />
                <span class="h-3 w-3 rounded-full" style={{ background: theme.colors.accentSecondary }} />
                <span class="h-3 w-3 rounded-full" style={{ background: theme.colors.success }} />
              </div>
              {theme.name}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Add ThemeSelector to TopBarV2**

In `frontend/islands/TopBarV2.tsx`, add `ThemeSelector` import and render it in the right section before the notification bell:

```tsx
import ThemeSelector from "@/islands/ThemeSelector.tsx";
// ... in the topbar-right section, before the notification bell:
<ThemeSelector />
```

- [ ] **Step 5: Commit**

```bash
git add frontend/islands/SplitPane.tsx frontend/lib/hooks/use-split-pane.ts frontend/islands/ThemeSelector.tsx frontend/islands/TopBarV2.tsx
git commit -m "feat: add split pane, theme selector with 7 themes"
```

---

## Task 10: Cluster Topology Visualization

**Files:**
- Create: `frontend/islands/ClusterTopology.tsx`
- Modify: `frontend/islands/DashboardV2.tsx` (replace placeholder)

This uses a simple force-simulation implemented in plain JS (no d3-force dependency for now — keeps bundle small). Nodes, services, pods rendered as positioned SVG elements with animated connecting lines.

- [ ] **Step 1: Create `frontend/islands/ClusterTopology.tsx`**

Implement a simplified force-directed graph that:
- Fetches nodes, pods, and services from the API
- Positions them in a left-to-right flow: Nodes → Services → Pods
- Draws animated SVG lines between connected resources
- Uses resource ownership (ownerReferences, selectors) to determine connections
- Hover shows resource name tooltip
- Click navigates to resource detail

This is a substantial island (~200 lines). The core logic:
1. Fetch nodes, services (with selectors), pods (with labels + ownerRefs)
2. Match pods to services via label selectors
3. Position nodes on left (y-distributed), services in middle, pods on right
4. Render SVG with animated lines + circles for each resource

- [ ] **Step 2: Replace topology placeholder in DashboardV2**

Replace the placeholder div with:
```tsx
import ClusterTopology from "@/islands/ClusterTopology.tsx";
// Replace placeholder:
<ClusterTopology />
```

- [ ] **Step 3: Commit**

```bash
git add frontend/islands/ClusterTopology.tsx frontend/islands/DashboardV2.tsx
git commit -m "feat: add live cluster topology visualization"
```

---

## Task 11: Domain Dashboard Pages

**Files:**
- Create: `frontend/islands/WorkloadsDashboard.tsx`
- Create: `frontend/routes/workloads/index.tsx`
- Create route wrappers for each domain that doesn't have an index

Each domain dashboard follows the same pattern: summary strip with ring gauges + sub-nav tabs + resource table. The WorkloadsDashboard serves as the template; other domain dashboards follow the same pattern.

- [ ] **Step 1: Create `frontend/islands/WorkloadsDashboard.tsx`**

Implements the layout from mockup `02-workloads-dashboard.html`:
- Summary strip: Total, Available, Progressing, Failed, Pods Ready, CPU Usage
- Sub-nav tabs: Deployments, StatefulSets, DaemonSets, Pods, Jobs, CronJobs
- Default tab shows the existing ResourceTable for that resource type

- [ ] **Step 2: Create domain index routes**

For each domain, create a minimal route that renders the domain dashboard with SubNav:

```
frontend/routes/workloads/index.tsx → WorkloadsDashboard
frontend/routes/config/index.tsx → ConfigDashboard
frontend/routes/security/index.tsx → redirect to /rbac/overview
frontend/routes/observability/index.tsx → redirect to /monitoring
```

Each follows the same pattern as the workloads dashboard but with domain-specific metrics.

- [ ] **Step 3: Commit**

```bash
git add frontend/islands/WorkloadsDashboard.tsx frontend/routes/workloads/index.tsx frontend/routes/config/index.tsx
git commit -m "feat: add domain dashboard pages with summary strips and sub-navigation"
```

---

## Task 12: Migrate ResourceTable and ResourceDetail

**Files:**
- Create: `frontend/islands/ResourceTableV2.tsx`
- Create: `frontend/islands/ResourceDetailV2.tsx`

These are restyled versions of the existing islands that use theme CSS variables instead of hardcoded Tailwind dark: classes. They preserve all existing functionality (WebSocket subscriptions, RBAC checks, pagination, actions, YAML editing, etc.) but render with the new design system.

- [ ] **Step 1: Create `frontend/islands/ResourceTableV2.tsx`**

Copy `ResourceTable.tsx` and update all color classes to use CSS variables via inline styles. Key changes:
- Table header: use `var(--text-muted)` for header text
- Row hover: `var(--bg-elevated)`
- Resource names: `var(--accent)` + font-mono
- Status badges: use new `StatusBadge` with theme variables
- Action buttons: use theme variables
- Search bar: use theme variables
- Filter chips: new FilterChip component

- [ ] **Step 2: Create `frontend/islands/ResourceDetailV2.tsx`**

Copy `ResourceDetail.tsx` and update styling. Key changes:
- Wrap in SplitPane when viewing resources with children (deployments, statefulsets, jobs)
- Left pane: resource detail with info grid, conditions, containers
- Right pane: related pods
- All colors via CSS variables

- [ ] **Step 3: Update existing routes to use V2 components**

Each route file (e.g., `routes/workloads/deployments.tsx`) needs to import `ResourceTableV2` instead of `ResourceTable`. This is a find-and-replace across all route files.

- [ ] **Step 4: Commit**

```bash
git add frontend/islands/ResourceTableV2.tsx frontend/islands/ResourceDetailV2.tsx frontend/routes/
git commit -m "feat: migrate resource table and detail views to new design system"
```

---

## Task 13: Migrate Remaining Islands

**Files:**
- Modify all wizard islands to use theme variables
- Modify: `frontend/islands/LoginForm.tsx`
- Modify: `frontend/islands/SetupWizard.tsx`
- Modify: `frontend/islands/SettingsPage.tsx`
- Modify: `frontend/islands/LogViewer.tsx`
- Modify: `frontend/islands/PodTerminal.tsx`
- Modify: `frontend/islands/YamlApplyPage.tsx`
- Modify: All remaining islands

Each island gets the same treatment: replace hardcoded `dark:bg-slate-*`, `dark:text-slate-*`, `dark:border-slate-*` classes with inline styles using `var(--bg-*)`, `var(--text-*)`, `var(--border-*)`.

- [ ] **Step 1: Batch-migrate all wizard islands**

For each wizard (DeploymentWizard, ServiceWizard, etc.): replace dark mode color classes with CSS variable inline styles. The form structure, validation, and API calls remain unchanged.

- [ ] **Step 2: Migrate login, setup, settings pages**

Special attention to LoginForm — it should have its own full-screen dark styling that works with any theme.

- [ ] **Step 3: Migrate utility islands (LogViewer, PodTerminal, YamlApplyPage)**

These have their own styling concerns (xterm.js, CodeMirror). Ensure editor themes align with the active k8sCenter theme.

- [ ] **Step 4: Verify all pages render correctly with Nexus theme**

Navigate to every major page type and confirm:
- No white/light backgrounds leaking through
- All text is readable against dark backgrounds
- Status colors are correct
- Interactive elements (buttons, inputs, dropdowns) work

- [ ] **Step 5: Test with Dracula, Tokyo Night, and Catppuccin themes**

Switch themes via the selector and verify colors change consistently across all pages.

- [ ] **Step 6: Commit**

```bash
git add frontend/islands/ frontend/components/
git commit -m "feat: migrate all remaining islands to CSS variable theming"
```

---

## Task 14: Clean Up and Polish

**Files:**
- Remove: `frontend/islands/Sidebar.tsx` (or keep as redirect)
- Remove: `frontend/islands/Dashboard.tsx` (or keep as redirect)
- Remove: `frontend/islands/TopBar.tsx` (or keep as redirect)
- Update: `frontend/islands/KeyboardShortcuts.tsx` (merge Cmd+K into CommandPalette)
- Update: E2E tests

- [ ] **Step 1: Deprecate old components**

Replace Sidebar, Dashboard, TopBar with re-exports:

```tsx
// frontend/islands/Sidebar.tsx
export { default } from "@/islands/IconRail.tsx";
```

```tsx
// frontend/islands/Dashboard.tsx
export { default } from "@/islands/DashboardV2.tsx";
```

- [ ] **Step 2: Update KeyboardShortcuts to remove Cmd+K (now in CommandPalette)**

Simplify KeyboardShortcuts to only handle `?` for help and `/` for search focus. Remove any Cmd+K handling since CommandPalette owns that now.

- [ ] **Step 3: Update E2E tests for new selectors**

The Playwright tests in `e2e/` will need selector updates for the new layout (icon rail instead of sidebar, etc.). Update test selectors.

- [ ] **Step 4: Run full test suite**

Run: `cd frontend && deno task check && deno task build`
Run: `cd e2e && npx playwright test`

- [ ] **Step 5: Commit**

```bash
git add frontend/ e2e/
git commit -m "feat: clean up deprecated components, update E2E tests for new UI"
```

---

## Summary

| Task | Description | New Files | Modified Files |
|------|-------------|-----------|----------------|
| 1 | Theme system foundation | 2 | 3 |
| 2 | Icon rail navigation | 4 | 2 |
| 3 | Migrate UI components | 5 | 9 |
| 4 | Sub-navigation tabs | 1 | 0 |
| 5 | Command palette | 2 | 0 |
| 6 | Health score engine | 2 | 0 |
| 7 | Overview dashboard V2 | 3 | 1 |
| 8 | Quick actions FAB | 1 | 0 |
| 9 | Split pane + theme selector | 3 | 1 |
| 10 | Cluster topology | 1 | 1 |
| 11 | Domain dashboards | 3 | 0 |
| 12 | Migrate ResourceTable/Detail | 2 | 50+ routes |
| 13 | Migrate remaining islands | 0 | 30+ islands |
| 14 | Clean up and polish | 0 | 5+ |

**Total: ~30 new files, ~100 modified files, 14 tasks**
