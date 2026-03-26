import { useComputed, useSignal } from"@preact/signals";
import { useEffect, useRef } from"preact/hooks";
import { IS_BROWSER } from"fresh/runtime";
import { refreshPermissions, useAuth } from"@/lib/auth.ts";
import { apiGet } from"@/lib/api.ts";
import { selectedNamespace } from"@/lib/namespace.ts";
import ThemeToggle from"@/islands/ThemeToggle.tsx";
import ClusterSelector from"@/islands/ClusterSelector.tsx";

interface NamespaceMeta {
 metadata: { name: string };
}

export default function TopBar() {
 const { user, logout } = useAuth();
 const namespaces = useSignal<string[]>([]);
 const showUserMenu = useSignal(false);
 const menuRef = useRef<HTMLDivElement>(null);

 // Fetch namespaces on mount
 useEffect(() => {
 if (!IS_BROWSER) return;
 apiGet<NamespaceMeta[]>("/v1/resources/namespaces")
 .then((res) => {
 namespaces.value = Array.isArray(res.data)
 ? res.data.map((ns) => ns.metadata.name)
 : [];
 })
 .catch(() => {
 // Silently fail — namespace list is non-critical
 });
 }, []);

 // Close user menu on outside click
 useEffect(() => {
 if (!IS_BROWSER) return;
 const handleClickOutside = (e: MouseEvent) => {
 if (
 showUserMenu.value && menuRef.current &&
 !menuRef.current.contains(e.target as Node)
 ) {
 showUserMenu.value = false;
 }
 };
 document.addEventListener("mousedown", handleClickOutside);
 return () => document.removeEventListener("mousedown", handleClickOutside);
 }, []);

 const displayName = useComputed(() => user.value?.username ??"User");

 return (
 <header class="flex h-14 items-center justify-between border-b border-border-primary bg-surface px-4">
 {/* Left: namespace selector */}
 <div class="flex items-center gap-3">
 <label
 for="namespace-select"
 class="text-xs font-medium text-text-muted"
 >
 Namespace
 </label>
 <select
 id="namespace-select"
 value={selectedNamespace.value}
 onChange={(e) => {
 const ns = (e.target as HTMLSelectElement).value;
 selectedNamespace.value = ns;
 // Refresh RBAC permissions for the new namespace
 if (ns && ns !=="all") {
 refreshPermissions(ns);
 }
 }}
 class="rounded-md border border-border-primary bg-surface px-2.5 py-1.5 text-sm text-text-secondary focus:border-brand focus:ring-1 focus:ring-brand text-text-secondary"
 >
 <option value="all">All Namespaces</option>
 {namespaces.value.map((ns) => (
 <option key={ns} value={ns}>{ns}</option>
 ))}
 </select>

 {/* Cluster selector */}
 <ClusterSelector />
 </div>

 {/* Right: theme + user menu */}
 <div class="flex items-center gap-2">
 <ThemeToggle />
 <div class="relative" ref={menuRef}>
 <button
 type="button"
 onClick={() => {
 showUserMenu.value = !showUserMenu.value;
 }}
 class="flex items-center gap-2 rounded-md px-2.5 py-1.5 text-sm text-text-secondary hover:bg-elevated text-text-secondary"
 >
 <span class="flex h-7 w-7 items-center justify-center rounded-full bg-brand text-xs font-medium text-white">
 {displayName.value.charAt(0).toUpperCase()}
 </span>
 {displayName.value}
 <svg class="h-4 w-4" viewBox="0 0 16 16" fill="currentColor">
 <path d="M4 6l4 4 4-4" />
 </svg>
 </button>

 {showUserMenu.value && (
 <div class="absolute right-0 mt-1 w-48 rounded-md border border-border-primary bg-surface py-1 shadow-lg">
 <div class="border-b border-border-subtle px-4 py-2 border-border-primary">
 <p class="text-sm font-medium text-text-primary">
 {displayName.value}
 </p>
 <p class="text-xs text-text-muted">
 {user.value?.roles?.[0] ??"user"}
 </p>
 </div>
 <button
 type="button"
 onClick={async () => {
 await logout();
 globalThis.location.href ="/login";
 }}
 class="flex w-full items-center gap-2 px-4 py-2 text-sm text-text-secondary hover:bg-hover"
 >
 <svg
 class="h-4 w-4"
 viewBox="0 0 16 16"
 fill="none"
 stroke="currentColor"
 stroke-width="1.5"
 >
 <path d="M6 14H3a1 1 0 01-1-1V3a1 1 0 011-1h3M11 11l3-3-3-3M14 8H6" />
 </svg>
 Sign out
 </button>
 </div>
 )}
 </div>
 </div>
 </header>
 );
}
