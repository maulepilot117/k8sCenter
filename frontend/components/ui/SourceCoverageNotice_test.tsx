import { expect, test } from "bun:test";
import type { SourceCoverage } from "@/lib/k8s-types.ts";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { SourceCoverageNotice } = await import(
  "@/components/ui/SourceCoverageNotice.tsx"
);
const { GATEWAY_KIND_LABELS } = await import("@/lib/gateway-types.ts");
const { GITOPS_SOURCE_LABELS } = await import("@/lib/gitops-types.ts");

/**
 * On a remote cluster a multi-source list (Argo CD plus Flux, every Gateway
 * API route kind) is read as separate lists. When one fails the backend
 * still answers 200 with the rows it could read and names the failed list in
 * `coverage` (R-8 KTD8). The page must name it too, or the operator reads a
 * missing Flux list as "no Flux applications" (R8).
 */

function mount(
  coverage: SourceCoverage[] | undefined,
  labels: Readonly<Record<string, string>> = GITOPS_SOURCE_LABELS,
): Element {
  return render(<SourceCoverageNotice coverage={coverage} labels={labels} />)
    .container;
}

test("a failed Flux list is named", () => {
  const root = mount([
    {
      source: "kustomizations",
      status: "unavailable",
      reasonCode: "unreachable",
    },
  ]);
  expect(root.textContent).toContain(
    "Could not load Flux Kustomizations from this cluster",
  );
  expect(root.textContent).not.toContain("Argo CD");
});

test("each failed source gets its own notice", () => {
  const root = mount([
    {
      source: "kustomizations",
      status: "unavailable",
      reasonCode: "unreachable",
    },
    {
      source: "helmreleases",
      status: "unavailable",
      reasonCode: "credentials_invalid",
    },
  ]);
  expect(root.querySelectorAll('[role="status"]')).toHaveLength(2);
  expect(root.textContent).toContain("Flux Kustomizations");
  expect(root.textContent).toContain("Flux HelmReleases");
});

test("a failed Gateway API route kind is named by its display name", () => {
  const root = mount(
    [
      {
        source: "grpcroutes",
        status: "unavailable",
        reasonCode: "unreachable",
      },
    ],
    GATEWAY_KIND_LABELS,
  );
  expect(root.textContent).toContain(
    "Could not load gRPC Routes from this cluster",
  );
});

test("a list the cluster refused is named as refused, not as unreachable", () => {
  const root = mount([
    { source: "helmreleases", status: "forbidden", reasonCode: "forbidden" },
  ]);
  expect(root.textContent).toContain(
    "not allowed to list Flux HelmReleases on this cluster",
  );
  expect(root.textContent).not.toContain("Could not load");
});

test("a source with no label is named by its resource", () => {
  const root = mount([
    {
      source: "ocirepositories",
      status: "unavailable",
      reasonCode: "unreachable",
    },
  ]);
  expect(root.textContent).toContain("Could not load ocirepositories");
});

test("empty coverage renders no notice", () => {
  const root = mount([]);
  expect(root.innerHTML).toBe("");
});

test("a local response without coverage renders no notice", () => {
  const root = mount(undefined);
  expect(root.innerHTML).toBe("");
});
