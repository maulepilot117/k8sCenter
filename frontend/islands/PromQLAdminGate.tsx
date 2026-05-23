import { IS_BROWSER } from "fresh/runtime";
import type { ComponentChildren } from "preact";
import { useAuth } from "@/lib/auth.ts";

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
export default function PromQLAdminGate(
  props: { children: ComponentChildren },
) {
  // Hooks must run unconditionally — call useAuth first, then bail out
  // for SSR. The signal read is a no-op on the server.
  const { user } = useAuth();

  if (!IS_BROWSER) {
    return (
      <div class="rounded-lg border border-border bg-surface p-6 text-sm text-text-secondary">
        Loading…
      </div>
    );
  }

  const u = user.value;

  if (u === null) {
    // Auth still loading or unauthenticated. Show a placeholder; ResourceTable
    // shells use the same approach. fetchCurrentUser in lib/auth fires
    // automatically; once it lands the signal updates and this re-renders.
    return (
      <div class="rounded-lg border border-border bg-surface p-6 text-sm text-text-secondary">
        Loading…
      </div>
    );
  }

  const isAdmin = (u.roles ?? []).includes("admin");
  if (!isAdmin) {
    return (
      <div class="rounded-lg border border-warning/40 bg-warning/5 p-6 space-y-3">
        <h2 class="text-base font-semibold text-text-primary">
          Admin access required
        </h2>
        <p class="text-sm text-text-secondary">
          Raw PromQL access is reserved for cluster administrators. The
          backend rejects non-admin queries against this endpoint, and the
          UI matches that boundary.
        </p>
        <p class="text-sm text-text-secondary">
          For per-resource metrics, open the resource's detail page (Pods,
          Deployments, Nodes, etc.) and switch to the Metrics tab — those
          panels use curated, server-owned queries that respect your
          Kubernetes RBAC scope.
        </p>
        <div class="pt-2">
          <a
            href="/cluster/dashboard"
            class="inline-flex items-center gap-2 rounded border border-border bg-surface px-3 py-1.5 text-sm text-text-primary hover:bg-surface-hover"
          >
            Back to dashboard
          </a>
        </div>
      </div>
    );
  }

  return <>{props.children}</>;
}
