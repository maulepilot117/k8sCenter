/** @jsxImportSource react */
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

const { default: UserWizard } = await import("./UserWizard.tsx");
const { default: VeleroBackupWizard } = await import(
  "./VeleroBackupWizard.tsx"
);
const { default: VeleroRestoreWizard } = await import(
  "./VeleroRestoreWizard.tsx"
);
const { default: VeleroScheduleWizard } = await import(
  "./VeleroScheduleWizard.tsx"
);

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];
let logged: string[] = [];
const realConsole = { ...console };

function json(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  calls = [];
  logged = [];
  for (const level of ["log", "info", "warn", "error", "debug"] as const) {
    console[level] = (...args: unknown[]) => {
      logged.push(args.map(String).join(" "));
    };
  }
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    const url = String(input);
    calls.push({
      url,
      method: init?.method ?? "GET",
      body: typeof init?.body === "string" ? JSON.parse(init.body) : null,
    });
    if (url.endsWith("/v1/resources/namespaces")) {
      return json(
        ["default", "prod", "staging", "dev"].map((n) => ({
          metadata: { name: n },
        })),
      );
    }
    if (url.endsWith("/v1/velero/locations")) {
      return json({
        backupStorageLocations: [
          { name: "s3-main", provider: "aws", default: true },
          { name: "gcs-alt", provider: "gcp", default: false },
        ],
      });
    }
    if (url.endsWith("/v1/velero/backups")) {
      return json([
        { name: "nightly-1", phase: "Completed" },
        { name: "broken-1", phase: "Failed" },
      ]);
    }
    if (url.endsWith("/v1/users")) return json({ id: "u1" }, 201);
    return json({ yaml: "kind: Preview\n" });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
  Object.assign(console, realConsole);
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

async function next(label: RegExp = /^Continue/) {
  await act(async () => {
    click(body().getByRole("button", { name: label }));
  });
}

function callTo(path: string): Call | undefined {
  return calls.find((c) => c.url.endsWith(path));
}

async function sentTo(path: string) {
  await waitFor(() => expect(callTo(path)).toBeDefined());
  return callTo(path)?.body as Record<string, unknown>;
}

function leaveIsBlocked(): boolean {
  const event = new Event("beforeunload", { cancelable: true });
  globalThis.dispatchEvent(event);
  return event.defaultPrevented;
}

/** Selects exactly these options of a multi-select, then fires change. */
function selectOptions(select: HTMLElement, values: string[]) {
  for (const opt of Array.from((select as HTMLSelectElement).options)) {
    opt.selected = values.includes(opt.value);
  }
  fireEvent.change(select);
}

// -------------------------------------------------------------------- User

const PASSWORD = "Sup3r-secret-pw";

function fillUser(password = PASSWORD, confirm = PASSWORD) {
  type(body().getByPlaceholderText("e.g. john.doe"), "jane.doe");
  type(body().getByPlaceholderText("Minimum 8 characters"), password);
  type(body().getByPlaceholderText("Re-enter password"), confirm);
}

test("User: valid account then Continue reaches the review step", async () => {
  render(<UserWizard onClose={() => {}} />);
  fillUser();
  await next();
  await waitFor(() => body().getByText("Summary"));
  expect(body().queryByPlaceholderText("e.g. john.doe")).toBeNull();
});

test("User: an empty username shows its error and does not advance", async () => {
  render(<UserWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("Minimum 8 characters"), PASSWORD);
  type(body().getByPlaceholderText("Re-enter password"), PASSWORD);
  await next();
  body().getByText(
    "Must start with alphanumeric, can contain letters, numbers, _, ., @, -",
  );
  body().getByPlaceholderText("e.g. john.doe");
});

test("User: mismatched passwords block Continue", async () => {
  render(<UserWizard onClose={() => {}} />);
  fillUser(PASSWORD, "something-else-1");
  await next();
  body().getByText("Passwords do not match");
  body().getByPlaceholderText("e.g. john.doe");
});

test("User: a too-short password blocks Continue", async () => {
  render(<UserWizard onClose={() => {}} />);
  fillUser("short", "short");
  await next();
  body().getByText("Must be 8-128 characters");
  body().getByPlaceholderText("e.g. john.doe");
});

test("User: a reserved k8s group blocks Continue", async () => {
  render(<UserWizard onClose={() => {}} />);
  fillUser();
  click(body().getByRole("button", { name: /Advanced: Kubernetes Identity/ }));
  type(
    body().getByPlaceholderText("system:authenticated"),
    "dev,system:masters",
  );
  await next();
  body().getByText("Cannot include 'system:masters' (bypasses all RBAC)");
});

test("User: typing marks the wizard dirty", () => {
  render(<UserWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. john.doe"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("User: Create User posts the typed username, roles and k8s identity, and the password stays out of URLs, logs and the page", async () => {
  render(<UserWizard onClose={() => {}} />);
  fillUser();
  click(body().getByLabelText("Admin role"));
  click(body().getByRole("button", { name: /Advanced: Kubernetes Identity/ }));
  type(body().getByPlaceholderText("jane.doe"), "jane-k8s");
  type(body().getByPlaceholderText("system:authenticated"), "dev, ops");
  await next();
  await waitFor(() => body().getByText("Summary"));
  expect(document.body.textContent).not.toContain(PASSWORD);
  await next(/^Create User/);
  const sent = await sentTo("/v1/users");
  expect(sent.username).toBe("jane.doe");
  expect(sent.password).toBe(PASSWORD);
  expect(sent.roles).toEqual(["admin"]);
  expect(sent.k8sUsername).toBe("jane-k8s");
  expect(sent.k8sGroups).toEqual(["dev", "ops"]);
  expect(callTo("/v1/users")?.method).toBe("POST");
  for (const c of calls) expect(c.url).not.toContain(PASSWORD);
  for (const line of logged) expect(line).not.toContain(PASSWORD);
  await waitFor(() => body().getByText("User Created"));
  expect(document.body.textContent).not.toContain(PASSWORD);
});

test("User: a failed create shows the error and stays on review", async () => {
  const ok = globalThis.fetch;
  globalThis.fetch = (async (input: unknown, init?: RequestInit) => {
    if (String(input).endsWith("/v1/users")) {
      return new Response(
        JSON.stringify({ error: { code: 409, message: "user exists" } }),
        { status: 409, headers: { "Content-Type": "application/json" } },
      );
    }
    return ok(input as string, init);
  }) as typeof fetch;
  render(<UserWizard onClose={() => {}} />);
  fillUser();
  await next();
  await next(/^Create User/);
  await waitFor(() => body().getByText(/user exists/));
  expect(body().queryByText("User Created")).toBeNull();
});

// ------------------------------------------------------------ Velero backup

test("VeleroBackup: a valid name then Continue advances and posts the preview", async () => {
  render(<VeleroBackupWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-backup"), "my-backup-1");
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
  expect(callTo("/v1/wizards/velero-backup/preview")).toBeDefined();
});

test("VeleroBackup: an invalid name shows its error and does not advance", async () => {
  render(<VeleroBackupWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-backup"), "Not A Label");
  await next();
  body().getByText("Must be a valid DNS label (lowercase, hyphens allowed)");
  body().getByText("Step 1 of 2", { exact: false });
  expect(callTo("/v1/wizards/velero-backup/preview")).toBeUndefined();
});

test("VeleroBackup: the preview carries the typed name, selected namespaces, storage location and TTL", async () => {
  render(<VeleroBackupWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-backup"), "typed-backup");
  await waitFor(() => body().getByText(/gcs-alt/));
  selectOptions(body().getByLabelText("Include Namespaces"), [
    "prod",
    "staging",
  ]);
  fireEvent.change(body().getByLabelText("Storage Location"), {
    target: { value: "gcs-alt" },
  });
  fireEvent.change(body().getByLabelText("Retention (TTL)"), {
    target: { value: "168h" },
  });
  click(body().getByLabelText("Snapshot persistent volumes"));
  await next();
  const sent = await sentTo("/v1/wizards/velero-backup/preview");
  expect(sent.name).toBe("typed-backup");
  expect(sent.includedNamespaces).toEqual(["prod", "staging"]);
  expect(sent.excludedNamespaces).toBeUndefined();
  expect(sent.storageLocation).toBe("gcs-alt");
  expect(sent.ttl).toBe("168h");
  expect(sent.snapshotVolumes).toBe(false);
});

test("VeleroBackup: the default storage location is preselected", async () => {
  render(<VeleroBackupWizard onClose={() => {}} />);
  await waitFor(() =>
    expect(
      (body().getByLabelText("Storage Location") as HTMLSelectElement).value,
    ).toBe("s3-main"),
  );
  type(body().getByPlaceholderText("my-backup"), "default-loc");
  await next();
  const sent = await sentTo("/v1/wizards/velero-backup/preview");
  expect(sent.storageLocation).toBe("s3-main");
});

test("VeleroBackup: typing marks the wizard dirty", () => {
  render(<VeleroBackupWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-backup"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

// ----------------------------------------------------------- Velero restore

async function pickBackup(name = "nightly-1") {
  await waitFor(() => body().getByText(/nightly-1/));
  fireEvent.change(body().getByLabelText("Source Backup *"), {
    target: { value: name },
  });
}

test("VeleroRestore: a chosen backup then Continue advances", async () => {
  render(<VeleroRestoreWizard onClose={() => {}} />);
  body().getByText("Step 1 of 2", { exact: false });
  await pickBackup();
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
});

test("VeleroRestore: no backup and no name shows both errors and does not advance", async () => {
  render(<VeleroRestoreWizard onClose={() => {}} />);
  await next();
  body().getByText("Backup is required");
  body().getByText("Must be a valid DNS label");
  body().getByText("Step 1 of 2", { exact: false });
  expect(callTo("/v1/wizards/velero-restore/preview")).toBeUndefined();
});

test("VeleroRestore: only completed or partially failed backups are offered", async () => {
  render(<VeleroRestoreWizard onClose={() => {}} />);
  await waitFor(() => body().getByText(/nightly-1/));
  expect(body().queryByText(/broken-1/)).toBeNull();
});

test("VeleroRestore: choosing a backup derives the restore name, and the preview carries the typed values", async () => {
  render(<VeleroRestoreWizard onClose={() => {}} />);
  await pickBackup();
  const nameInput = body().getByPlaceholderText(
    "my-restore",
  ) as HTMLInputElement;
  await waitFor(() => expect(nameInput.value).toMatch(/^nightly-1-restore-/));
  type(nameInput, "typed-restore");
  selectOptions(body().getByLabelText("Include Namespaces (optional)"), [
    "dev",
  ]);
  click(body().getByLabelText("Restore persistent volumes"));
  await next();
  const sent = await sentTo("/v1/wizards/velero-restore/preview");
  expect(sent.name).toBe("typed-restore");
  expect(sent.backupName).toBe("nightly-1");
  expect(sent.includedNamespaces).toEqual(["dev"]);
  expect(sent.restorePVs).toBe(false);
});

test("VeleroRestore: a backup named in the page's parameter is preselected", async () => {
  render(<VeleroRestoreWizard onClose={() => {}} backup="nightly-1" />);
  await waitFor(() =>
    expect(
      (body().getByLabelText("Source Backup *") as HTMLSelectElement).value,
    ).toBe("nightly-1"),
  );
  expect(
    (body().getByPlaceholderText("my-restore") as HTMLInputElement).value,
  ).toMatch(/^nightly-1-restore-/);
});

test("VeleroRestore: typing marks the wizard dirty", () => {
  render(<VeleroRestoreWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-restore"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

// ---------------------------------------------------------- Velero schedule

test("VeleroSchedule: a valid name then Continue advances and posts the preview", async () => {
  render(<VeleroScheduleWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("daily-backup"), "nightly");
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
  expect(callTo("/v1/wizards/velero-schedule/preview")).toBeDefined();
});

test("VeleroSchedule: an empty name shows its error and does not advance", async () => {
  render(<VeleroScheduleWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be a valid DNS label (lowercase, hyphens allowed, 1-63 chars)",
  );
  body().getByText("Step 1 of 2", { exact: false });
});

test("VeleroSchedule: a cron field without five parts blocks Continue", async () => {
  render(<VeleroScheduleWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("daily-backup"), "nightly");
  type(body().getByPlaceholderText("0 0 * * *"), "* * *");
  await next();
  body().getByText("Must be a valid cron expression (5 fields)");
  expect(callTo("/v1/wizards/velero-schedule/preview")).toBeUndefined();
});

test("VeleroSchedule: an emptied cron field is required", async () => {
  render(<VeleroScheduleWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("daily-backup"), "nightly");
  type(body().getByPlaceholderText("0 0 * * *"), "");
  await next();
  body().getByText("Cron schedule is required");
});

test("VeleroSchedule: a preset fills the cron field and the preview carries the typed values", async () => {
  render(<VeleroScheduleWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("daily-backup"), "typed-schedule");
  click(body().getByRole("button", { name: /Weekly/ }));
  expect(
    (body().getByPlaceholderText("0 0 * * *") as HTMLInputElement).value,
  ).toBe("0 0 * * 0");
  await waitFor(() => body().getByText(/prod/));
  selectOptions(body().getByLabelText("Include Namespaces"), ["prod"]);
  click(body().getByLabelText(/Create paused/));
  await next();
  const sent = await sentTo("/v1/wizards/velero-schedule/preview");
  expect(sent.name).toBe("typed-schedule");
  expect(sent.schedule).toBe("0 0 * * 0");
  expect(sent.includedNamespaces).toEqual(["prod"]);
  expect(sent.paused).toBe(true);
  expect(sent.ttl).toBe("720h");
});

test("VeleroSchedule: typing marks the wizard dirty", () => {
  render(<VeleroScheduleWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("daily-backup"), "x");
  expect(leaveIsBlocked()).toBe(true);
});
