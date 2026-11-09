/** @jsxImportSource react */
import type { ReactNode } from "react";
import { useAuth } from "@/lib/auth.ts";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useSignalValue } from "@/src/lib/use-signal-value.ts";

/**
 * The root every return shares. Preact hydration keeps the server-rendered
 * root's own props and only reconciles its children, so a root that differed
 * between the SSR placeholder and a later return would keep the placeholder's
 * classes. The root is layout-neutral (`display: contents`); each state styles its own
 * inner box.
 */
const GATE_ROOT_CLASS = "contents";

const LOADING_CLASS =
  "rounded-lg border border-border-primary bg-surface p-6 text-sm text-text-secondary";

/**
 * Client-side admin gate around `/monitoring/prometheus`.
 *
 * F#4 (security audit 2026-05-22): the raw PromQL surface is admin-only.
 * The backend `/v1/monitoring/query` endpoint already enforces this — non-
 * admins receive a 403. This gate is defense-in-depth on the UI: it hides
 * the query editor so non-admin operators don't see (or get confused by)
 * an unusable surface, and points them at the per-resource Metrics tabs
 * which use the slug-based `/v1/monitoring/queries/{slug}` endpoint instead.
 *
 * SSR rendering returns a neutral loading shell — the harness does not
 * populate `ctx.state.user` server-side, so the role check has to wait for
 * the auth signal to hydrate client-side. The shell prevents a flash of
 * the query UI before the gate evaluates.
 */
export default function PromQLAdminGate(props: { children: ReactNode }) {
  // Hooks must run unconditionally, so read the user before the SSR bail-out.
  // Read through useSignalValue rather than `user.value`: a bare read in
  // render does not subscribe.
  const u = useSignalValue(useAuth().user);
  const isClient = useIsClient();

  if (!isClient) {
    return (
      <div className={GATE_ROOT_CLASS}>
        <div className={LOADING_CLASS}>Loading…</div>
      </div>
    );
  }

  if (u === null) {
    // Auth still loading or unauthenticated. Show a placeholder; ResourceTable
    // shells use the same approach. fetchCurrentUser in lib/auth fires
    // automatically; once it lands the signal updates and this re-renders.
    return (
      <div className={GATE_ROOT_CLASS}>
        <div className={LOADING_CLASS}>Loading…</div>
      </div>
    );
  }

  const isAdmin = (u.roles ?? []).includes("admin");
  if (!isAdmin) {
    return (
      <div className={GATE_ROOT_CLASS}>
        <div className="rounded-lg border border-warning/40 bg-warning/5 p-6 space-y-3">
          <h2 className="text-base font-semibold text-text-primary">
            Admin access required
          </h2>
          <p className="text-sm text-text-secondary">
            Raw PromQL access is reserved for cluster administrators. The
            backend rejects non-admin queries against this endpoint, and the UI
            matches that boundary.
          </p>
          <p className="text-sm text-text-secondary">
            For per-resource metrics, open the resource's detail page (Pods,
            Deployments, Nodes, etc.) and switch to the Metrics tab — those
            panels use curated, server-owned queries that respect your
            Kubernetes RBAC scope.
          </p>
          <div className="pt-2">
            <a
              href="/"
              className="inline-flex items-center gap-2 rounded border border-border-primary bg-surface px-3 py-1.5 text-sm text-text-primary hover:bg-hover"
            >
              Back to dashboard
            </a>
          </div>
        </div>
      </div>
    );
  }

  return <div className={GATE_ROOT_CLASS}>{props.children}</div>;
}
