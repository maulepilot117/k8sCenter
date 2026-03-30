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
        background: "var(--bg-base)",
        color: "var(--text-primary)",
      }}
    >
      <div style={{ gridRow: "1 / -1" }}>
        <IconRail currentPath={url.pathname} />
      </div>
      <div style={{ zIndex: 50, position: "relative" }}>
        <TopBarV2 />
      </div>
      <main
        class="page-enter"
        style={{
          overflowY: "auto",
          padding: "var(--content-padding, 24px)",
          maxWidth: "var(--content-max-width, 2400px)",
          marginInline: "auto",
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
