/** @jsxImportSource react */
import { afterAll, afterEach, beforeEach, expect, test } from "bun:test";
import { setAccessToken } from "@/lib/api.ts";
import {
  act,
  body,
  fireEvent,
  json,
  render,
  settle,
  setupDom,
} from "@/src/test-utils/render.tsx";

/**
 * The wizard keeps what was typed on every step when the user goes back and
 * forward between steps: the admin account, monitoring and alerting fields.
 */
const bunFetch = globalThis.fetch;
setupDom({ browser: true });

const { default: SetupWizard } = await import("./SetupWizard.tsx");

interface Call {
  method: string;
  url: string;
  body?: Record<string, unknown>;
}
let calls: Call[] = [];

beforeEach(() => {
  calls = [];
  setAccessToken("t");
  globalThis.fetch = (async (
    input: string | URL | Request,
    init?: RequestInit,
  ) => {
    const url = String(input);
    calls.push({
      method: init?.method ?? "GET",
      url,
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    });
    return json(url.endsWith("/auth/login") ? { accessToken: "tok" } : {});
  }) as unknown as typeof globalThis.fetch;
});
afterEach(() => {
  globalThis.fetch = bunFetch;
});
afterAll(() => {
  globalThis.fetch = bunFetch;
});

function type(el: Element, value: string) {
  fireEvent.input(el, { target: { value } });
}

function click(el: Element) {
  fireEvent.click(el);
}

function stepButton(n: number, label: string) {
  return body().getByRole("button", {
    name: new RegExp(`^Step ${n}: ${label}`),
  });
}

function inputFor(label: string): HTMLInputElement {
  const el = body().getByText(label).parentElement?.querySelector("input");
  if (!el) throw new Error(`no input under label ${label}`);
  return el;
}

async function settleClick(el: Element) {
  await act(async () => {
    click(el);
  });
  await settle();
}

test("every field keeps its value when the user moves back and forward", async () => {
  render(<SetupWizard />);
  click(body().getByRole("button", { name: "Get Started" }));

  const text = document.body.querySelector<HTMLInputElement>(
    "input[type=text]",
  ) as HTMLInputElement;
  const pw = document.body.querySelectorAll<HTMLInputElement>(
    "input[type=password]",
  );
  type(text, "root-admin");
  type(pw[0], "password-1");
  type(pw[1], "password-1");
  type(pw[2], "tok-1");
  await settleClick(body().getByRole("button", { name: /Create Account/ }));
  body().getByText("Monitoring (Optional)");

  type(inputFor("Prometheus URL"), "http://prom:9090");
  type(inputFor("Grafana URL"), "http://graf:3000");
  type(inputFor("Grafana API Token"), "graf-token");
  click(body().getByRole("button", { name: "Skip" }));
  body().getByText("Alerting (Optional)");

  type(inputFor("SMTP Host"), "smtp.example.com");
  type(inputFor("SMTP Port"), "2525");
  type(inputFor("SMTP Username"), "mailer");
  type(inputFor("SMTP Password"), "mail-pass");
  type(inputFor("From Address"), "alerts@example.com");

  // Back to monitoring.
  click(stepButton(3, "Monitoring"));
  body().getByText("Monitoring (Optional)");
  expect(inputFor("Prometheus URL").value).toBe("http://prom:9090");
  expect(inputFor("Grafana URL").value).toBe("http://graf:3000");
  expect(inputFor("Grafana API Token").value).toBe("graf-token");

  // Forward to alerting.
  click(body().getByRole("button", { name: "Skip" }));
  body().getByText("Alerting (Optional)");
  expect(inputFor("SMTP Host").value).toBe("smtp.example.com");
  expect(inputFor("SMTP Port").value).toBe("2525");
  expect(inputFor("SMTP Username").value).toBe("mailer");
  expect(inputFor("SMTP Password").value).toBe("mail-pass");
  expect(inputFor("From Address").value).toBe("alerts@example.com");

  // Back to the admin account.
  click(stepButton(2, "Admin Account"));
  body().getByText("Create Admin Account");
  const text2 = document.body.querySelector<HTMLInputElement>(
    "input[type=text]",
  ) as HTMLInputElement;
  const pw2 = document.body.querySelectorAll<HTMLInputElement>(
    "input[type=password]",
  );
  expect(text2.value).toBe("root-admin");
  expect(pw2[0].value).toBe("password-1");
  expect(pw2[1].value).toBe("password-1");
  expect(pw2[2].value).toBe("tok-1");
});

test("the values kept across steps are the ones saved", async () => {
  render(<SetupWizard />);
  click(body().getByRole("button", { name: "Get Started" }));
  const text = document.body.querySelector<HTMLInputElement>(
    "input[type=text]",
  ) as HTMLInputElement;
  const pw = document.body.querySelectorAll<HTMLInputElement>(
    "input[type=password]",
  );
  type(text, "root-admin");
  type(pw[0], "password-1");
  type(pw[1], "password-1");
  await settleClick(body().getByRole("button", { name: /Create Account/ }));

  type(inputFor("Prometheus URL"), "http://prom:9090");
  type(inputFor("Grafana API Token"), "graf-token");
  await settleClick(body().getByRole("button", { name: /Save & Continue/ }));
  body().getByText("Alerting (Optional)");
  const mon = calls.find((c) => c.url.endsWith("/v1/settings"));
  expect(mon?.body).toEqual({
    monitoringPrometheusUrl: "http://prom:9090",
    monitoringGrafanaUrl: null,
    monitoringGrafanaToken: "graf-token",
  });

  type(inputFor("SMTP Host"), "smtp.example.com");
  type(inputFor("SMTP Port"), "2525");
  type(inputFor("From Address"), "alerts@example.com");
  await settleClick(body().getByRole("button", { name: /Save & Continue/ }));
  body().getByText("Setup Complete");
  const alerting = calls.filter((c) => c.url.endsWith("/v1/settings"))[1];
  expect(alerting?.body).toEqual({
    alertingEnabled: true,
    alertingSmtpHost: "smtp.example.com",
    alertingSmtpPort: 2525,
    alertingSmtpFrom: "alerts@example.com",
    alertingSmtpUsername: null,
    alertingSmtpPassword: null,
  });
});
