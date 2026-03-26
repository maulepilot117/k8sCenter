import { useSignal } from"@preact/signals";
import { useEffect } from"preact/hooks";
import { IS_BROWSER } from"fresh/runtime";
import { apiGet } from"@/lib/api.ts";
import { selectedNamespace } from"@/lib/namespace.ts";
import { CLUSTER_SCOPED_KINDS } from"@/lib/constants.ts";

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

function isActive(tabHref: string, currentPath: string): boolean {
 if (tabHref === currentPath) return true;
 // Prefix match: /workloads/deployments matches /workloads/deployments/ns/name
 if (
 tabHref !=="/" && currentPath.startsWith(tabHref) &&
 (currentPath.length === tabHref.length ||
 currentPath[tabHref.length] ==="/")
 ) {
 return true;
 }
 return false;
}

export default function SubNav({ tabs, currentPath }: SubNavProps) {
 const counts = useSignal<Record<string, number | null>>({});
 const namespace = selectedNamespace.value;

 useEffect(() => {
 if (!IS_BROWSER) return;

 const countTabs = tabs.filter((t) => t.count && t.kind);
 if (countTabs.length === 0) return;

 // Reset counts for loading state
 const initial: Record<string, number | null> = {};
 for (const t of countTabs) {
 initial[t.kind!] = null;
 }
 counts.value = initial;

 const promises = countTabs.map(async (t) => {
 try {
 const isClusterScoped = CLUSTER_SCOPED_KINDS.has(t.kind!);
 const nsPath = !isClusterScoped && namespace && namespace !=="all"
 ? `/${namespace}`
 :"";
 const res = await apiGet<unknown>(
 `/v1/resources/${t.kind}${nsPath}?limit=1`,
 );
 return { kind: t.kind!, count: res.metadata?.total ?? 0 };
 } catch {
 return { kind: t.kind!, count: 0 };
 }
 });

 Promise.all(promises).then((results) => {
 const updated: Record<string, number | null> = {};
 for (const r of results) {
 updated[r.kind] = r.count;
 }
 counts.value = updated;
 });
 }, [namespace, tabs]);

 if (!IS_BROWSER) {
 return (
 <nav
 style={{
 height:"36px",
 borderBottom:"1px solid var(--border-subtle)",
 background:"var(--bg-surface)",
 }}
 />
 );
 }

 return (
 <nav
 style={{
 display:"flex",
 alignItems:"stretch",
 gap:"0",
 borderBottom:"1px solid var(--border-subtle)",
 background:"var(--bg-surface)",
 overflowX:"auto",
 paddingLeft:"16px",
 paddingRight:"16px",
 flexShrink: 0,
 }}
 >
 {tabs.map((tab) => {
 const active = isActive(tab.href, currentPath);
 const count = tab.kind ? counts.value[tab.kind] : undefined;

 return (
 <a
 key={tab.href}
 href={tab.href}
 style={{
 display:"flex",
 alignItems:"center",
 gap:"6px",
 padding:"8px 12px",
 fontSize:"13px",
 fontWeight: active ? 500 : 400,
 color: active ?"var(--accent)" :"var(--text-muted)",
 textDecoration:"none",
 borderBottom: active
 ?"2px solid var(--accent)"
 :"2px solid transparent",
 whiteSpace:"nowrap",
 transition:"color 150ms ease, border-color 150ms ease",
 }}
 >
 {tab.label}
 {tab.count && count !== undefined && count !== null && (
 <span
 style={{
 fontSize:"11px",
 fontWeight: 500,
 padding:"1px 6px",
 borderRadius:"10px",
 background: active
 ?"var(--accent-dim)"
 :"var(--bg-elevated)",
 color: active ?"var(--accent)" :"var(--text-muted)",
 }}
 >
 {count}
 </span>
 )}
 </a>
 );
 })}
 </nav>
 );
}
