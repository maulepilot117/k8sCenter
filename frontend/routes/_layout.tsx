import { define } from "@/utils.ts";
import AlertBanner from "@/islands/AlertBanner.tsx";
import IconRail from "@/islands/IconRail.tsx";
import TopBarV2 from "@/islands/TopBarV2.tsx";
import ToastProvider from "@/islands/ToastProvider.tsx";
import KeyboardShortcuts from "@/islands/KeyboardShortcuts.tsx";
import CommandPalette from "@/islands/CommandPalette.tsx";
import QuickActionsFab from "@/islands/QuickActionsFab.tsx";

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
        gridTemplateColumns: "var(--rail-width, 60px) 1fr",
        gridTemplateRows: "var(--topbar-height, 52px) 1fr",
        height: "100dvh",
        // Transparent (not bg-base) so the body's ambient wash shows through
        // main's gutters and gives the glass chrome real depth to refract.
        // html/body keep `bg-base !important`, so there's no white-flash risk.
        background: "transparent",
        color: "var(--text-primary)",
      }}
    >
      {
        /* display:flex so the IconRail <nav> stretches to the full column
          height — without an opaque container fill behind it, a content-sized
          rail would otherwise stop short and leak the ambient wash below. */
      }
      <div style={{ gridColumn: 1, gridRow: "1 / -1", display: "flex" }}>
        <IconRail currentPath={url.pathname} />
      </div>
      {
        /* The topbar overlays the content column (gridRow 1) while <main>
          spans both rows beneath it (gridRow 1 / -1). Page content therefore
          scrolls UP behind the translucent .glass-bar, giving it live content
          to refract — the lensing cue that sells liquid glass. main's
          padding-top reserves the bar's height so initial content clears it. */
      }
      <div
        style={{ gridColumn: 2, gridRow: 1, zIndex: 50, position: "relative" }}
      >
        <TopBarV2 />
      </div>
      <main
        class="page-enter"
        style={{
          gridColumn: 2,
          gridRow: "1 / -1",
          overflowY: "auto",
          padding:
            "calc(var(--topbar-height, 52px) + var(--content-padding, 24px)) var(--content-padding, 24px) var(--content-padding, 24px)",
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
