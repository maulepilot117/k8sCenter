import { test, expect } from "../fixtures/base.ts";
import { e2eName, deleteResource, deleteClusterResource } from "../helpers.ts";

interface WizardConfig {
  kind: string;
  createPath: string;
  apiKind: string;
  namespace: string | null;
  fields: { label: string; value: string; generated?: boolean }[];
  submitButton?: string;
}

const WIZARDS: WizardConfig[] = [
  {
    kind: "deployment",
    createPath: "/workloads/deployments/new",
    apiKind: "deployments",
    namespace: "default",
    fields: [
      { label: "Name", value: "", generated: true },
      { label: "Container Image", value: "nginx:alpine" },
    ],
  },
  {
    kind: "configmap",
    createPath: "/config/configmaps/new",
    apiKind: "configmaps",
    namespace: "default",
    fields: [{ label: "Name", value: "", generated: true }],
  },
  {
    kind: "job",
    createPath: "/workloads/jobs/new",
    apiKind: "jobs",
    namespace: "default",
    fields: [
      { label: "Job Name", value: "", generated: true },
      { label: "Container Image", value: "busybox:1.37" },
    ],
  },
  {
    kind: "namespace",
    createPath: "/cluster/namespaces/new",
    apiKind: "namespaces",
    namespace: null,
    fields: [{ label: "Name", value: "", generated: true }],
    submitButton: "Create",
  },
  {
    kind: "networkpolicy",
    createPath: "/networking/networkpolicies/new",
    apiKind: "networkpolicies",
    namespace: "default",
    fields: [{ label: "Name", value: "", generated: true }],
  },
];

for (const w of WIZARDS) {
  test.describe(`${w.kind} wizard`, () => {
    test(`creates ${w.kind} via wizard`, async ({ page, request }) => {
      const resourceName = e2eName(w.kind);

      try {
        await page.goto(w.createPath);

        // Fill form fields
        for (const field of w.fields) {
          const value = field.generated ? resourceName : field.value;
          const input = page.getByLabel(field.label, { exact: true });
          await input.fill(value);
        }

        // Signal-based stepping: click Next until Apply/Create button is visible
        const submitText = w.submitButton ?? "Apply";
        const submitButton = page.getByRole("button", {
          name: new RegExp(submitText, "i"),
        });
        const nextButton = page.getByRole("button", { name: /next/i });

        // Click Next until review step (submit button visible) — max 5 iterations
        for (let i = 0; i < 5; i++) {
          if (await submitButton.isVisible().catch(() => false)) break;
          if (await nextButton.isVisible().catch(() => false)) {
            await nextButton.click();
            // Wait for step transition (actionability of next button or submit button)
            await expect(
              submitButton.or(nextButton),
            ).toBeVisible();
          } else {
            break;
          }
        }

        // Submit
        await expect(submitButton).toBeVisible({ timeout: 5_000 });
        await submitButton.click();

        // Assert success
        await expect(
          page.getByText(/successfully|created|configured/i),
        ).toBeVisible({ timeout: 15_000 });
      } finally {
        // Always clean up, regardless of test outcome
        if (w.namespace) {
          await deleteResource(request, w.apiKind, w.namespace, resourceName);
        } else {
          await deleteClusterResource(request, w.apiKind, resourceName);
        }
      }
    });
  });
}
