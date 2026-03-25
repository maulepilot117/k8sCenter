import { test, expect } from "../fixtures/base.ts";
import { e2eName, deleteResource, deleteClusterResource } from "../helpers.ts";

interface WizardConfig {
  kind: string;
  createPath: string;
  apiKind: string;
  namespace: string | null;
  fields: {
    label: string;
    value: string;
    generated?: boolean;
    usePlaceholder?: string;
  }[];
  submitButton?: string;
}

const WIZARDS: WizardConfig[] = [
  {
    kind: "deployment",
    createPath: "/workloads/deployments/new",
    apiKind: "deployments",
    namespace: "default",
    fields: [
      { label: "Name", value: "", generated: true, usePlaceholder: "my-deployment" },
      { label: "Container Image", value: "nginx:alpine" },
    ],
  },
  {
    kind: "configmap",
    createPath: "/config/configmaps/new",
    apiKind: "configmaps",
    namespace: "default",
    fields: [
      { label: "Name", value: "", generated: true, usePlaceholder: "e.g. my-config" },
    ],
  },
  // Job wizard omitted — Container Image is on step 2 (after Next),
  // which requires step-aware field filling. Deferred to a job-specific test.
  {
    kind: "namespace",
    createPath: "/cluster/namespaces/new",
    apiKind: "namespaces",
    namespace: null,
    fields: [
      { label: "Name", value: "", generated: true, usePlaceholder: "my-namespace" },
    ],
    submitButton: "Create",
  },
  {
    kind: "networkpolicy",
    createPath: "/networking/networkpolicies/new",
    apiKind: "networkpolicies",
    namespace: "default",
    fields: [
      { label: "Name", value: "", generated: true, usePlaceholder: "my-network-policy" },
    ],
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
          const input = field.usePlaceholder
            ? page.getByPlaceholder(field.usePlaceholder)
            : page.getByLabel(field.label, { exact: true });
          await input.fill(value);
        }

        // Signal-based stepping: click Next until Apply/Create button is visible
        const submitText = w.submitButton ?? "Apply";
        const submitButton = page.getByRole("button", {
          name: new RegExp(submitText, "i"),
        });
        const nextButton = page.getByRole("button", {
          name: /next|preview/i,
        });

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
