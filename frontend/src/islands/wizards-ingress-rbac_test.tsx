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

const { default: IngressWizard } = await import("./IngressWizard.tsx");
const { default: RoleBindingWizard } = await import("./RoleBindingWizard.tsx");

interface Call {
  url: string;
  method: string;
  body: Record<string, unknown> | null;
}

let calls: Call[] = [];

function json(data: unknown): Response {
  return new Response(JSON.stringify({ data }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  calls = [];
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
    if (url.includes("/v1/resources/clusterroles")) {
      return json([{ metadata: { name: "cluster-admin" } }]);
    }
    if (url.includes("/v1/resources/roles/")) {
      return json([{ metadata: { name: "pod-reader", namespace: "default" } }]);
    }
    if (url.endsWith("/v1/users")) return json([]);
    return json({ yaml: "kind: Preview\n" });
  }) as typeof fetch;
});

afterEach(() => {
  setAccessToken(null);
});

afterAll(() => {
  globalThis.fetch = bunFetch;
});

/** Types into a text-like field the way a keystroke does. */
function type(el: Element, value: string) {
  fireEvent.input(el, { target: { value } });
}

function click(el: Element) {
  fireEvent.click(el);
}

function choose(el: Element, value: string) {
  fireEvent.change(el, { target: { value } });
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

function all<T extends Element>(selector: string): T[] {
  return Array.from(document.querySelectorAll<T>(selector));
}

// ----------------------------------------------------------------- Ingress

function fillIngressBasics(name = "web") {
  type(body().getByPlaceholderText("my-ingress"), name);
  type(body().getByPlaceholderText("my-service"), "frontend");
}

test("Ingress: required fields then Next advances to the review step", async () => {
  render(<IngressWizard onClose={() => {}} />);
  body().getByText("Step 1 of 2", { exact: false });
  fillIngressBasics();
  await next();
  await waitFor(() => body().getByText("Step 2 of 2", { exact: false }));
  expect(body().queryByPlaceholderText("my-ingress")).toBeNull();
});

test("Ingress: an empty name and service show errors and do not advance", async () => {
  render(<IngressWizard onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByText("Required");
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("ingress")).toBeUndefined();
});

test("Ingress: the review step posts the typed host, path, backend and class", async () => {
  render(<IngressWizard onClose={() => {}} />);
  fillIngressBasics("typed-ing");
  type(body().getByPlaceholderText("nginx (optional)"), "traefik");
  type(body().getByPlaceholderText("example.com"), "shop.example.org");
  type(body().getByPlaceholderText("/"), "/api");
  choose(all("select")[1], "Exact");
  type(all("input[type=number]")[0], "8443");
  await next();
  const sent = await previewBody("ingress");
  expect(sent.name).toBe("typed-ing");
  expect(sent.ingressClassName).toBe("traefik");
  expect(sent.tls).toBeUndefined();
  expect(sent.rules).toEqual([
    {
      host: "shop.example.org",
      paths: [
        {
          path: "/api",
          pathType: "Exact",
          serviceName: "frontend",
          servicePort: 8443,
        },
      ],
    },
  ]);
});

test("Ingress: typing marks the wizard dirty", () => {
  render(<IngressWizard onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("my-ingress"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("Ingress: a path row without a service blocks Next and none is dropped", async () => {
  render(<IngressWizard onClose={() => {}} />);
  type(body().getByPlaceholderText("my-ingress"), "web");
  type(body().getByPlaceholderText("my-service"), "frontend");
  click(body().getByRole("button", { name: "+ Add Path" }));
  // The second path row is half filled: a path, no backend service.
  type(body().getAllByPlaceholderText("/")[1], "/half");
  await next();
  body().getByText("Required");
  body().getByText("Step 1 of 2", { exact: false });
  expect(previewCall("ingress")).toBeUndefined();
});

test("Ingress: a path that does not start with a slash blocks Next", async () => {
  render(<IngressWizard onClose={() => {}} />);
  fillIngressBasics();
  type(body().getByPlaceholderText("/"), "api");
  await next();
  body().getByText("Must start with /");
  expect(previewCall("ingress")).toBeUndefined();
});

test("Ingress: removing a rule keeps the other rules' values", () => {
  render(<IngressWizard onClose={() => {}} />);
  click(body().getByRole("button", { name: "+ Add Rule" }));
  click(body().getByRole("button", { name: "+ Add Rule" }));
  const hosts = () =>
    body().getAllByPlaceholderText("example.com") as HTMLInputElement[];
  const services = () =>
    body().getAllByPlaceholderText("my-service") as HTMLInputElement[];
  type(hosts()[0], "a.example.com");
  type(services()[0], "svc-a");
  type(hosts()[1], "b.example.com");
  type(services()[1], "svc-b");
  type(hosts()[2], "c.example.com");
  type(services()[2], "svc-c");
  click(body().getAllByRole("button", { name: "Remove Rule" })[1]);
  expect(hosts().map((h) => h.value)).toEqual([
    "a.example.com",
    "c.example.com",
  ]);
  expect(services().map((s) => s.value)).toEqual(["svc-a", "svc-c"]);
});

test("Ingress: removing a path keeps the other paths' values", () => {
  render(<IngressWizard onClose={() => {}} />);
  click(body().getByRole("button", { name: "+ Add Path" }));
  click(body().getByRole("button", { name: "+ Add Path" }));
  const paths = () => body().getAllByPlaceholderText("/") as HTMLInputElement[];
  type(paths()[0], "/one");
  type(paths()[1], "/two");
  type(paths()[2], "/three");
  click(body().getAllByText("×")[1]);
  expect(paths().map((p) => p.value)).toEqual(["/one", "/three"]);
});

test("Ingress: enabling TLS copies the rule hosts and requires a secret", async () => {
  render(<IngressWizard onClose={() => {}} />);
  fillIngressBasics();
  type(body().getByPlaceholderText("example.com"), "tls.example.com");
  click(body().getByLabelText("Enable TLS"));
  const tlsHosts = (await waitFor(() =>
    body().getByPlaceholderText("example.com, api.example.com"),
  )) as HTMLInputElement;
  expect(tlsHosts.value).toBe("tls.example.com");

  await next();
  body().getByText("Required");
  expect(previewCall("ingress")).toBeUndefined();

  type(body().getByPlaceholderText("tls-secret"), "web-cert");
  await next();
  const sent = await previewBody("ingress");
  expect(sent.tls).toEqual([
    { hosts: ["tls.example.com"], secretName: "web-cert" },
  ]);
});

test("Ingress: the TLS hosts field keeps a half-typed comma separated list", async () => {
  render(<IngressWizard onClose={() => {}} />);
  fillIngressBasics();
  click(body().getByLabelText("Enable TLS"));
  const tlsHosts = (await waitFor(() =>
    body().getByPlaceholderText("example.com, api.example.com"),
  )) as HTMLInputElement;
  type(tlsHosts, "a.example.com, ");
  expect(tlsHosts.value).toBe("a.example.com, ");
  type(tlsHosts, "a.example.com, b.example.com");
  type(body().getByPlaceholderText("tls-secret"), "web-cert");
  await next();
  const sent = await previewBody("ingress");
  expect(sent.tls).toEqual([
    { hosts: ["a.example.com", "b.example.com"], secretName: "web-cert" },
  ]);
});

// ------------------------------------------------------------- RoleBinding

async function pickClusterRole() {
  const roleSelect = await waitFor(() => {
    const sel = all<HTMLSelectElement>("select").find((s) =>
      Array.from(s.options).some((o) => o.value === "cluster-admin"),
    );
    if (!sel) throw new Error("role options not loaded");
    return sel;
  });
  choose(roleSelect, "cluster-admin");
}

async function reachSubjects() {
  type(body().getByPlaceholderText("e.g. my-binding"), "ops-binding");
  await next();
  await waitFor(() => body().getByText("Role"));
  await pickClusterRole();
  await next();
  await waitFor(() => body().getByPlaceholderText("Subject name"));
}

test("RoleBinding: required fields then Next advances through the steps", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-binding"), "ops-binding");
  await next();
  await waitFor(() => body().getByText("Role Type"));
  expect(body().queryByPlaceholderText("e.g. my-binding")).toBeNull();
  await pickClusterRole();
  await next();
  await waitFor(() => body().getByPlaceholderText("Subject name"));
});

test("RoleBinding: an empty name shows its error and does not advance", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  await next();
  body().getByText(
    "Must be lowercase alphanumeric with hyphens, 1-63 characters",
  );
  body().getByPlaceholderText("e.g. my-binding");
});

test("RoleBinding: no role selected blocks the Role step", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-binding"), "ops-binding");
  await next();
  await waitFor(() => body().getByText("Role Type"));
  await next();
  body().getByText("A role must be selected");
  body().getByText("Role Type");
});

test("RoleBinding: typing marks the wizard dirty", () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  expect(leaveIsBlocked()).toBe(false);
  type(body().getByPlaceholderText("e.g. my-binding"), "x");
  expect(leaveIsBlocked()).toBe(true);
});

test("RoleBinding: a half-filled subject blocks Next with field errors", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  await reachSubjects();
  await next();
  body().getByText("At least one subject is required");
  body().getByText("Name is required");
  expect(previewCall("rolebinding")).toBeUndefined();

  // A named ServiceAccount without a namespace is still half filled.
  choose(all("select")[0], "ServiceAccount");
  type(body().getByPlaceholderText("Subject name"), "builder");
  await next();
  body().getByText("Namespace is required for ServiceAccount");
  expect(previewCall("rolebinding")).toBeUndefined();
});

test("RoleBinding: the review step posts a ClusterRole reference and every subject kind", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  await reachSubjects();
  type(body().getByPlaceholderText("Subject name"), "alice");
  click(body().getByRole("button", { name: /Add Subject/ }));
  click(body().getByRole("button", { name: /Add Subject/ }));
  const kinds = () => all<HTMLSelectElement>("select");
  choose(kinds()[1], "Group");
  choose(kinds()[2], "ServiceAccount");
  const names = body().getAllByPlaceholderText("Subject name");
  type(names[1], "devs");
  type(names[2], "builder");
  type(body().getByPlaceholderText("Namespace"), "ci");
  await next();
  const sent = await previewBody("rolebinding");
  expect(sent.name).toBe("ops-binding");
  expect(sent.namespace).toBe("default");
  expect(sent.clusterScope).toBe(false);
  expect(sent.roleRef).toEqual({ kind: "ClusterRole", name: "cluster-admin" });
  expect(sent.subjects).toEqual([
    { kind: "User", name: "alice" },
    { kind: "Group", name: "devs" },
    { kind: "ServiceAccount", name: "builder", namespace: "ci" },
  ]);
});

test("RoleBinding: choosing Role lists the namespace roles and posts kind Role", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-binding"), "reader");
  await next();
  await waitFor(() => body().getByText("Role Type"));
  choose(all("select")[0], "Role");
  await waitFor(() => body().getByText("pod-reader"));
  expect(body().queryByText("cluster-admin")).toBeNull();
  choose(all("select")[1], "pod-reader");
  await next();
  await waitFor(() => body().getByPlaceholderText("Subject name"));
  type(body().getByPlaceholderText("Subject name"), "alice");
  await next();
  const sent = await previewBody("rolebinding");
  expect(sent.roleRef).toEqual({ kind: "Role", name: "pod-reader" });
});

test("RoleBinding: switching the role type clears the chosen role", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-binding"), "reader");
  await next();
  await waitFor(() => body().getByText("Role Type"));
  await pickClusterRole();
  choose(all("select")[0], "Role");
  await waitFor(() => body().getByText("pod-reader"));
  await next();
  body().getByText("A role must be selected");
});

test("RoleBinding: a cluster-scoped binding posts clusterScope without a namespace", async () => {
  render(<RoleBindingWizard clusterScoped={true} onClose={() => {}} />);
  type(body().getByPlaceholderText("e.g. my-binding"), "ops-binding");
  expect(body().queryByText("Namespace")).toBeNull();
  await next();
  await waitFor(() => body().getByText("ClusterRole"));
  await pickClusterRole();
  await next();
  await waitFor(() => body().getByPlaceholderText("Subject name"));
  type(body().getByPlaceholderText("Subject name"), "alice");
  await next();
  const sent = await previewBody("rolebinding");
  expect(sent.clusterScope).toBe(true);
  expect(sent.namespace).toBeUndefined();
  expect(sent.roleRef).toEqual({ kind: "ClusterRole", name: "cluster-admin" });
});

test("RoleBinding: removing a subject keeps the other rows' values", async () => {
  render(<RoleBindingWizard clusterScoped={false} onClose={() => {}} />);
  await reachSubjects();
  click(body().getByRole("button", { name: /Add Subject/ }));
  click(body().getByRole("button", { name: /Add Subject/ }));
  const names = () =>
    body().getAllByPlaceholderText("Subject name") as HTMLInputElement[];
  type(names()[0], "first");
  type(names()[1], "second");
  type(names()[2], "third");
  click(body().getAllByTitle("Remove subject")[1]);
  expect(names().map((n) => n.value)).toEqual(["first", "third"]);
});
