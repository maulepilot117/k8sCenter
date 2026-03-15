import { define } from "@/utils.ts";
import AlertBanner from "@/islands/AlertBanner.tsx";
import Sidebar from "@/islands/Sidebar.tsx";
import TopBar from "@/islands/TopBar.tsx";
import ToastProvider from "@/islands/ToastProvider.tsx";
import KeyboardShortcuts from "@/islands/KeyboardShortcuts.tsx";

export default define.page(function Layout({ Component, url }) {
  // Login and OIDC callback use their own full-screen layout
  if (url.pathname === "/login" || url.pathname.startsWith("/auth/")) {
    return <Component />;
  }

  return (
    <div class="flex h-full">
      <Sidebar currentPath={url.pathname} />
      <div class="flex flex-1 flex-col overflow-hidden">
        <TopBar />
        <AlertBanner />
        <main class="flex-1 overflow-y-auto p-6">
          <Component />
        </main>
      </div>
      <ToastProvider />
      <KeyboardShortcuts />
    </div>
  );
});
