import { define } from "@/utils.ts";
import AlertBanner from "@/islands/AlertBanner.tsx";
import IconRail from "@/islands/IconRail.tsx";
import SecondaryNav from "@/islands/SecondaryNav.tsx";
import TopBarV2 from "@/islands/TopBarV2.tsx";
import ToastProvider from "@/islands/ToastProvider.tsx";
import KeyboardShortcuts from "@/islands/KeyboardShortcuts.tsx";
import CommandPalette from "@/islands/CommandPalette.tsx";
import QuickActionsFab from "@/islands/QuickActionsFab.tsx";

// Three-pane shell: icon rail (domains) | collapsible secondary nav (grouped
// children) | content. The secondary panel width is driven by --panel-width,
// which the SecondaryNav island flips to 0px when collapsed (see lib/nav.ts).
// Collapse is instant — no CSS transition on the grid track (per design spec).
export default define.page(function Layout({ Component, url }) {
  // Login, setup, and OIDC callback use their own full-screen layout
  if (
    url.pathname === "/login" || url.pathname === "/setup" ||
    url.pathname.startsWith("/auth/")
  ) {
    return <Component />;
  }

  return (
    <div
      style={{
        display: "grid",
        // 3 columns: rail | secondary nav | content.
        // minmax(0,1fr) lets the content column shrink instead of overflowing.
        gridTemplateColumns:
          "var(--rail-width, 64px) var(--panel-width, 250px) minmax(0, 1fr)",
        gridTemplateRows: "var(--topbar-height, 56px) 1fr",
        height: "100dvh",
        // Transparent (not bg-base) so the body's ambient wash shows through
        // main's gutters and gives the glass chrome real depth to refract.
        // html/body keep `bg-base !important`, so there's no white-flash risk.
        background: "transparent",
        color: "var(--text-primary)",
      }}
    >
      {/* Icon rail — full height, column 1 */}
      <div style={{ gridColumn: 1, gridRow: "1 / -1", display: "flex" }}>
        <IconRail currentPath={url.pathname} />
      </div>

      {/* Secondary nav — full height, collapsible, column 2 */}
      <div
        style={{
          gridColumn: 2,
          gridRow: "1 / -1",
          display: "flex",
          overflow: "hidden",
        }}
      >
        <SecondaryNav currentPath={url.pathname} />
      </div>

      {/* Top bar — overlays content column (column 3, row 1) */}
      <div
        style={{ gridColumn: 3, gridRow: 1, zIndex: 50, position: "relative" }}
      >
        <TopBarV2 />
      </div>

      {/* Main content — scrolls under the bar (column 3, rows 1/-1) */}
      <main
        class="page-enter"
        style={{
          gridColumn: 3,
          gridRow: "1 / -1",
          minWidth: 0,
          overflowY: "auto",
          overflowX: "hidden",
          padding:
            "calc(var(--topbar-height, 56px) + var(--content-padding, 24px)) var(--content-padding, 24px) var(--content-padding, 24px)",
        }}
      >
        <AlertBanner />
        <Component />
      </main>

      <ToastProvider />
      <KeyboardShortcuts />
      <CommandPalette />
      <QuickActionsFab />
    </div>
  );
});
