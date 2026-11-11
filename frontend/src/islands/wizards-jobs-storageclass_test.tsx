import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
  waitFor,
} from "@/src/test-utils/render.tsx";

// Bun's own fetch, captured before happy-dom replaces it.
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: CronJobWizard } = await import("./CronJobWizard.tsx");
const { default: JobWizard } = await import("./JobWizard.tsx");
const { default: ScheduledSnapshotWizard } = await import(
  "./ScheduledSnapshotWizard.tsx"
);
const { default: StorageClassWizard } = await import(
  "./StorageClassWizard.tsx"
);

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];
let snapshotsAvailable = true;

function json(data: unknown): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const PRESETS = {
  "ebs.csi.aws.com": {
    displayName: "AWS EBS",
    parameters: {
      type: {
        default: "gp3",
        description: "EBS volume type",
        type: "string",
        options: ["gp2", "gp3"],
      },
      fsType: {
        default: "ext4",
        description: "Filesystem",
        type: "string",
      },
    },
  },
};

beforeEach(() => {
  calls = [];
  snapshotsAvailable = true;
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({
      url,
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
    });
    if (url.endsWith("/v1/resources/namespaces")) {
      return json([{ metadata: { name: "default" } }]);
    }
    if (url.includes("/v1/resources/pvcs/")) {
      return json([
        {
          metadata: { name: "data-bound", namespace: "default" },
          status: { phase: "Bound" },
        },
        {
          metadata: { name: "data-pending", namespace: "default" },
          status: { phase: "Pending" },
        },
      ]);
    }
    if (url.endsWith("/v1/storage/snapshot-classes")) {
      return json({
        data: snapshotsAvailable
          ? [{ metadata: { name: "csi-snap" }, driver: "x" }]
          : [],
        metadata: { available: snapshotsAvailable },
      });
    }
    if (url.endsWith("/v1/storage/presets")) return json(PRESETS);
    return json({ yaml: "kind: Preview\n" });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
});

afterAll(() => {
  globalThis.fetch = bunFetch;
});

function type(el: HTMLElement, value: string) {
  fireEvent.input(el, { target: { value } });
}

function click(el: HTMLElement) {
  fireEvent.click(el);
}

async function next() {
  await act(async () => {
    click(body().getByRole("button", { name: /^Continue/ }));
  });
}

function previewCall(kind: string): Call | undefined {
  return calls.find((c) => c.url.endsWith(`/v1/wizards/${kind}/preview`));
}

async function previewBody(kind: string) {
  await waitFor(() => expect(previewCall(kind)).toBeDefined());
  return previewCall(kind)?.body as Record<string, unknown>;
}

/** True when leaving the page would prompt: the wizard's dirty flag is set. */
function leaveIsBlocked(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event.defaultPrevented;
}

// --------------------------------------------------------------------- Job

async function jobToContainerStep(name = "batch-1") {
  type(body().getByPlaceholderText("my-batch-job"), name);
  await next();
  await waitFor(() => body().getByPlaceholderText("nginx:latest"));
}

test("Job: required fields then Next advances to the container step", async () => {
  render(<JobWizard onClose={() => {}} />);
  await jobToContainerStep();
  expect(body().queryByPlaceholderText("my-batch-job")).toBeNull();
});

test("Job: an empty name shows its error and does not advance", async () => {
  render(<JobWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByPlaceholderText("my-batch-job");
  expect(previewCall("job")).toBeUndefined();
});

test("Job: the review step posts the typed values", async () => {
  render(<JobWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-batch-job"), "typed-job");
  const [completions, parallelism, backoff] = body().getAllByRole(
    "spinbutton",
  ) as HTMLInputElement[];
  type(completions, "4");
  type(parallelism, "2");
  type(backoff, "9");
  await next();
  await waitFor(() => body().getByPlaceholderText("nginx:latest"));
  type(body().getByPlaceholderText("nginx:latest"), "busybox:1.36");
  await next();
  const sent = await previewBody("job");
  expect(sent.name).toBe("typed-job");
  expect(sent.completions).toBe(4);
  expect(sent.parallelism).toBe(2);
  expect(sent.backoffLimit).toBe(9);
  expect((sent.container as { image: string }).image).toBe("busybox:1.36");
});

test("Job: typing marks the wizard dirty", () => {
  render(<JobWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-batch-job"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("Job: a negative completions count blocks Next", async () => {
  render(<JobWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-batch-job"), "batch-1");
  const [completions] = body().getAllByRole("spinbutton") as HTMLInputElement[];
  type(completions, "-1");
  await next();
  body().getByText("Must be a non-negative integer");
  body().getByPlaceholderText("my-batch-job");
});

test("Job: an empty image blocks the review step", async () => {
  render(<JobWizard onClose={() => {}} />);
  await jobToContainerStep();
  await next();
  body().getByText("Required");
  expect(previewCall("job")).toBeUndefined();
});

// ----------------------------------------------------------------- CronJob

async function cronToContainerStep(name = "nightly") {
  type(body().getByPlaceholderText("e.g. data-cleanup"), name);
  await next();
  await waitFor(() => body().getByPlaceholderText("nginx:latest"));
}

test("CronJob: required fields then Next advances to the container step", async () => {
  render(<CronJobWizard onClose={() => {}} />);
  await cronToContainerStep();
  expect(body().queryByPlaceholderText("e.g. data-cleanup")).toBeNull();
});

test("CronJob: an empty name shows its error and does not advance", async () => {
  render(<CronJobWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByPlaceholderText("e.g. data-cleanup");
});

test("CronJob: the review step posts the typed name, custom schedule and image", async () => {
  render(<CronJobWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. data-cleanup"), "typed-cron");
  click(body().getByRole("button", { name: "Custom" }));
  type(body().getByPlaceholderText("e.g. */5 * * * *"), "*/5 * * * *");
  body().getByText("Cron: */5 * * * *");
  await next();
  await waitFor(() => body().getByPlaceholderText("nginx:latest"));
  type(body().getByPlaceholderText("nginx:latest"), "alpine:3.20");
  await next();
  const sent = await previewBody("cronjob");
  expect(sent.name).toBe("typed-cron");
  expect(sent.schedule).toBe("*/5 * * * *");
  expect((sent.container as { image: string }).image).toBe("alpine:3.20");
});

test("CronJob: choosing a preset posts that preset's schedule", async () => {
  render(<CronJobWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. data-cleanup"), "hourly");
  click(body().getByRole("button", { name: "Every hour" }));
  body().getByText("Every hour, at minute 0");
  await next();
  await waitFor(() => body().getByPlaceholderText("nginx:latest"));
  type(body().getByPlaceholderText("nginx:latest"), "alpine:3.20");
  await next();
  const sent = await previewBody("cronjob");
  expect(sent.schedule).toBe("0 * * * *");
});

test("CronJob: typing marks the wizard dirty", () => {
  render(<CronJobWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. data-cleanup"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("CronJob: a blank custom schedule blocks Next", async () => {
  render(<CronJobWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. data-cleanup"), "nightly");
  click(body().getByRole("button", { name: "Custom" }));
  type(body().getByPlaceholderText("e.g. */5 * * * *"), "   ");
  await next();
  body().getByText("Required");
  body().getByPlaceholderText("e.g. data-cleanup");
  expect(previewCall("cronjob")).toBeUndefined();
});

test("CronJob: an empty image blocks the review step", async () => {
  render(<CronJobWizard onClose={() => {}} />);
  await cronToContainerStep();
  await next();
  body().getByText("Required");
  expect(previewCall("cronjob")).toBeUndefined();
});

// -------------------------------------------------------- ScheduledSnapshot

async function snapshotReady() {
  await waitFor(() => body().getByRole("option", { name: "data-bound" }));
  await waitFor(() => body().getByRole("option", { name: "csi-snap" }));
}

function snapshotSelects() {
  return body().getAllByRole("combobox") as HTMLSelectElement[];
}

async function fillSnapshotStep0(name = "daily-db") {
  await snapshotReady();
  type(body().getByPlaceholderText("e.g. daily-db-backup"), name);
  fireEvent.change(snapshotSelects()[1], { target: { value: "data-bound" } });
}

test("ScheduledSnapshot: required fields then Next advances to retention", async () => {
  render(<ScheduledSnapshotWizard onClose={() => {}} />);
  await fillSnapshotStep0();
  await next();
  await waitFor(() => body().getByText("Retention Count"));
  expect(body().queryByPlaceholderText("e.g. daily-db-backup")).toBeNull();
});

test("ScheduledSnapshot: a missing name and PVC show errors and do not advance", async () => {
  render(<ScheduledSnapshotWizard onClose={() => {}} />);
  await snapshotReady();
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  // The Source PVC error; the class was auto-selected so only one "Required".
  body().getByText("Required");
  body().getByPlaceholderText("e.g. daily-db-backup");
});

test("ScheduledSnapshot: only bound PVCs are offered", async () => {
  render(<ScheduledSnapshotWizard onClose={() => {}} />);
  await snapshotReady();
  expect(body().queryByRole("option", { name: "data-pending" })).toBeNull();
});

test("ScheduledSnapshot: the review step posts the typed values and schedule", async () => {
  render(<ScheduledSnapshotWizard onClose={() => {}} />);
  await fillSnapshotStep0("typed-snap");
  click(body().getByRole("button", { name: "Custom" }));
  type(body().getByPlaceholderText("e.g. 0 */6 * * *"), "0 */6 * * *");
  await next();
  await waitFor(() => body().getByText("Retention Count"));
  click(body().getByRole("button", { name: "+" }));
  click(body().getByRole("button", { name: "+" }));
  await next();
  const sent = await previewBody("scheduled-snapshot");
  expect(sent.name).toBe("typed-snap");
  expect(sent.sourcePVC).toBe("data-bound");
  expect(sent.volumeSnapshotClassName).toBe("csi-snap");
  expect(sent.schedule).toBe("0 */6 * * *");
  expect(sent.retentionCount).toBe(7);
});

test("ScheduledSnapshot: a blank custom schedule blocks Next", async () => {
  render(<ScheduledSnapshotWizard onClose={() => {}} />);
  await fillSnapshotStep0();
  click(body().getByRole("button", { name: "Custom" }));
  type(body().getByPlaceholderText("e.g. 0 */6 * * *"), "");
  await next();
  body().getByText("Required");
  body().getByPlaceholderText("e.g. daily-db-backup");
});

test("ScheduledSnapshot: missing VolumeSnapshot CRDs show the notice instead of the form", async () => {
  snapshotsAvailable = false;
  render(<ScheduledSnapshotWizard onClose={() => {}} />);
  await waitFor(() => body().getByText("VolumeSnapshot CRDs Not Installed"));
  expect(body().queryByPlaceholderText("e.g. daily-db-backup")).toBeNull();
});

// ------------------------------------------------------------ StorageClass

async function storageClassPresetsLoaded() {
  await waitFor(() => body().getByRole("button", { name: "AWS EBS" }));
}

test("StorageClass: required fields then Next advances to parameters", async () => {
  render(<StorageClassWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. fast-storage"), "fast");
  type(body().getByPlaceholderText("e.g. ebs.csi.aws.com"), "my.csi.io");
  await next();
  await waitFor(() => body().getByText("Mount Options"));
  expect(body().queryByPlaceholderText("e.g. fast-storage")).toBeNull();
});

test("StorageClass: an empty provisioner shows its error and does not advance", async () => {
  render(<StorageClassWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. fast-storage"), "fast");
  await next();
  body().getByText("Required");
  body().getByPlaceholderText("e.g. fast-storage");
});

test("StorageClass: an invalid name shows its error", async () => {
  render(<StorageClassWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. fast-storage"), "Bad_Name");
  type(body().getByPlaceholderText("e.g. ebs.csi.aws.com"), "my.csi.io");
  await next();
  body().getByText(
    "Must be a valid DNS subdomain (lowercase, hyphens, dots, max 253)",
  );
  expect(previewCall("storageclass")).toBeUndefined();
});

test("StorageClass: the review step posts the typed values, parameters and mount options", async () => {
  render(<StorageClassWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. fast-storage"), "typed-sc");
  type(body().getByPlaceholderText("e.g. ebs.csi.aws.com"), "my.csi.io");
  await next();
  await waitFor(() => body().getByText("Mount Options"));
  const adds = body().getAllByRole("button", { name: "+ Add" });
  click(adds[0]);
  type(body().getByPlaceholderText("Key"), "iops");
  type(body().getByPlaceholderText("Value"), "3000");
  click(body().getAllByRole("button", { name: "+ Add" })[1]);
  type(body().getByPlaceholderText("e.g. debug, noatime"), "noatime");
  await next();
  const sent = await previewBody("storageclass");
  expect(sent.name).toBe("typed-sc");
  expect(sent.provisioner).toBe("my.csi.io");
  expect(sent.parameters).toEqual({ iops: "3000" });
  expect(sent.mountOptions).toEqual(["noatime"]);
});

test("StorageClass: a preset fills the provisioner and its option-bearing parameters", async () => {
  render(<StorageClassWizard onClose={() => {}} />);
  await storageClassPresetsLoaded();
  click(body().getByRole("button", { name: "AWS EBS" }));
  expect(
    (body().getByPlaceholderText("e.g. ebs.csi.aws.com") as HTMLInputElement)
      .value,
  ).toBe("ebs.csi.aws.com");
  type(body().getByPlaceholderText("e.g. fast-storage"), "gp-class");
  await next();
  await waitFor(() => body().getByText("EBS volume type"));
  // The parameter with options renders a select, the other a text input.
  const select = body().getByRole("combobox") as HTMLSelectElement;
  expect(select.value).toBe("gp3");
  fireEvent.change(select, { target: { value: "gp2" } });
  await next();
  const sent = await previewBody("storageclass");
  expect(sent.provisioner).toBe("ebs.csi.aws.com");
  expect(sent.parameters).toEqual({ type: "gp2", fsType: "ext4" });
});

test("StorageClass: removing a parameter row keeps the other rows' values", async () => {
  render(<StorageClassWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. fast-storage"), "rows");
  type(body().getByPlaceholderText("e.g. ebs.csi.aws.com"), "my.csi.io");
  await next();
  await waitFor(() => body().getByText("Mount Options"));
  for (let i = 0; i < 3; i++) {
    click(body().getAllByRole("button", { name: "+ Add" })[0]);
  }
  const keys = () =>
    body().getAllByPlaceholderText("Key") as HTMLInputElement[];
  type(keys()[0], "a");
  type(keys()[1], "b");
  type(keys()[2], "c");
  click(body().getAllByRole("button", { name: "×" })[1]);
  expect(keys().map((k) => k.value)).toEqual(["a", "c"]);
});
