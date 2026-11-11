import { expect, mock, test } from "bun:test";
import { type ComponentProps, useState } from "react";
import {
  act,
  body,
  fireEvent,
  render,
  setupDom,
} from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { AlertingFields } = await import("./AlertingFields.tsx");
const { MonitoringFields } = await import("./MonitoringFields.tsx");

/** The input in the same field wrapper as the label with this text. */
function inputFor(label: string): HTMLInputElement {
  const el = body().getByText(label).parentElement?.querySelector("input");
  if (!el) throw new Error(`no input under label ${label}`);
  return el;
}

function type(input: HTMLInputElement, value: string) {
  fireEvent.input(input, { target: { value } });
}

interface AlertingValues {
  alertEnabled: boolean;
  smtpHost: string;
  smtpPort: number;
  smtpUser: string;
  smtpPass: string;
  smtpFrom: string;
  alertRate?: number;
  alertRecipients?: string;
}

interface Probe<T> {
  /** The values the fields last reported, as a page would hold them. */
  current: T;
  /** Sets values from outside the fields, as a page load would. */
  set: (patch: Partial<T>) => void;
}

function probe<T>(initial: T): Probe<T> {
  return { current: initial, set: () => {} };
}

/**
 * Holds the field values the way a page does, so the fields are controlled
 * and a test can read what they reported or push a value in from outside.
 * Rate and recipients are passed only when the initial values include them.
 */
function AlertingHarness({
  init,
  probe: p,
  onDirty,
}: {
  init: AlertingValues;
  probe: Probe<AlertingValues>;
  onDirty?: () => void;
}) {
  const [v, setV] = useState(init);
  p.current = v;
  p.set = (patch) => setV((prev) => ({ ...prev, ...patch }));
  const optional: Partial<ComponentProps<typeof AlertingFields>> = {};
  if (init.alertRate !== undefined) {
    optional.alertRate = v.alertRate;
    optional.onAlertRateChange = (x) =>
      setV((prev) => ({ ...prev, alertRate: x }));
  }
  if (init.alertRecipients !== undefined) {
    optional.alertRecipients = v.alertRecipients;
    optional.onAlertRecipientsChange = (x) =>
      setV((prev) => ({ ...prev, alertRecipients: x }));
  }
  return (
    <AlertingFields
      alertEnabled={v.alertEnabled}
      onAlertEnabledChange={(x) =>
        setV((prev) => ({ ...prev, alertEnabled: x }))
      }
      smtpHost={v.smtpHost}
      onSmtpHostChange={(x) => setV((prev) => ({ ...prev, smtpHost: x }))}
      smtpPort={v.smtpPort}
      onSmtpPortChange={(x) => setV((prev) => ({ ...prev, smtpPort: x }))}
      smtpUser={v.smtpUser}
      onSmtpUserChange={(x) => setV((prev) => ({ ...prev, smtpUser: x }))}
      smtpPass={v.smtpPass}
      onSmtpPassChange={(x) => setV((prev) => ({ ...prev, smtpPass: x }))}
      smtpFrom={v.smtpFrom}
      onSmtpFromChange={(x) => setV((prev) => ({ ...prev, smtpFrom: x }))}
      {...optional}
      onDirty={onDirty}
    />
  );
}

function alertingValues(): AlertingValues {
  return {
    alertEnabled: false,
    smtpHost: "mail.local",
    smtpPort: 25,
    smtpUser: "bob",
    smtpPass: "****",
    smtpFrom: "a@b.c",
  };
}

interface MonitoringValues {
  promUrl: string;
  grafUrl: string;
  grafToken: string;
  monNs?: string;
}

function MonitoringHarness({
  init,
  probe: p,
  onDirty,
}: {
  init: MonitoringValues;
  probe: Probe<MonitoringValues>;
  onDirty?: () => void;
}) {
  const [v, setV] = useState(init);
  p.current = v;
  p.set = (patch) => setV((prev) => ({ ...prev, ...patch }));
  const optional: Partial<ComponentProps<typeof MonitoringFields>> = {};
  if (init.monNs !== undefined) {
    optional.monNs = v.monNs;
    optional.onMonNsChange = (x) => setV((prev) => ({ ...prev, monNs: x }));
  }
  return (
    <MonitoringFields
      promUrl={v.promUrl}
      onPromUrlChange={(x) => setV((prev) => ({ ...prev, promUrl: x }))}
      grafUrl={v.grafUrl}
      onGrafUrlChange={(x) => setV((prev) => ({ ...prev, grafUrl: x }))}
      grafToken={v.grafToken}
      onGrafTokenChange={(x) => setV((prev) => ({ ...prev, grafToken: x }))}
      {...optional}
      onDirty={onDirty}
    />
  );
}

test("alerting fields show the values and report typed values back", () => {
  const init = alertingValues();
  const s = probe(init);
  const onDirty = mock(() => {});
  render(<AlertingHarness init={init} probe={s} onDirty={onDirty} />);

  expect(inputFor("SMTP Host").value).toBe("mail.local");
  expect(inputFor("SMTP Port").value).toBe("25");

  type(inputFor("SMTP Host"), "smtp.example.com");
  expect(s.current.smtpHost).toBe("smtp.example.com");
  type(inputFor("SMTP Username"), "alice");
  expect(s.current.smtpUser).toBe("alice");
  type(inputFor("From Address"), "x@y.z");
  expect(s.current.smtpFrom).toBe("x@y.z");
  type(inputFor("SMTP Port"), "2525");
  expect(s.current.smtpPort).toBe(2525);
  type(inputFor("SMTP Port"), "junk");
  expect(s.current.smtpPort).toBe(587);
  expect(onDirty).toHaveBeenCalledTimes(5);

  expect(s.current.alertEnabled).toBe(false);
  fireEvent.click(body().getByRole("switch"));
  expect(s.current.alertEnabled).toBe(true);
  expect(onDirty).toHaveBeenCalledTimes(6);
});

test("alerting fields show a value set from outside", async () => {
  const init = alertingValues();
  const s = probe(init);
  render(<AlertingHarness init={init} probe={s} />);
  await act(async () => {
    s.set({ smtpHost: "changed.example", smtpPort: 465, alertEnabled: true });
  });
  expect(inputFor("SMTP Host").value).toBe("changed.example");
  expect(inputFor("SMTP Port").value).toBe("465");
  expect(body().getByRole("switch").getAttribute("aria-checked")).toBe("true");
});

test("the SMTP password is a password input and keeps the stored-mask placeholder", () => {
  const init = alertingValues();
  const s = probe(init);
  render(<AlertingHarness init={init} probe={s} />);
  const pass = inputFor("SMTP Password");
  expect(pass.type).toBe("password");
  expect(pass.placeholder).toBe("****");
  type(pass, "hunter2");
  expect(s.current.smtpPass).toBe("hunter2");
  expect(inputFor("SMTP Password").placeholder).toBe("");
});

test("rate limit and recipients appear only when their props are passed", () => {
  const bareInit = alertingValues();
  const bare = render(
    <AlertingHarness init={bareInit} probe={probe(bareInit)} />,
  );
  expect(body().queryByText("Rate Limit (per hour)")).toBeNull();
  expect(body().queryByText("Recipients")).toBeNull();
  bare.unmount();

  const init = { ...alertingValues(), alertRate: 5, alertRecipients: "a@b.c" };
  const s = probe(init);
  render(<AlertingHarness init={init} probe={s} />);
  expect(inputFor("Rate Limit (per hour)").value).toBe("5");
  type(inputFor("Rate Limit (per hour)"), "20");
  expect(s.current.alertRate).toBe(20);
  type(inputFor("Rate Limit (per hour)"), "");
  expect(s.current.alertRate).toBe(5);
  type(inputFor("Recipients"), "x@y.z, q@r.s");
  expect(s.current.alertRecipients).toBe("x@y.z, q@r.s");
});

test("monitoring fields report typed values back and keep the token masked", async () => {
  const init: MonitoringValues = {
    promUrl: "http://p",
    grafUrl: "http://g",
    grafToken: "****",
  };
  const s = probe(init);
  const onDirty = mock(() => {});
  render(<MonitoringHarness init={init} probe={s} onDirty={onDirty} />);
  expect(inputFor("Prometheus URL").value).toBe("http://p");
  type(inputFor("Prometheus URL"), "http://prom:9090");
  expect(s.current.promUrl).toBe("http://prom:9090");
  type(inputFor("Grafana URL"), "http://graf:3000");
  expect(s.current.grafUrl).toBe("http://graf:3000");
  expect(inputFor("Grafana API Token").type).toBe("password");
  expect(inputFor("Grafana API Token").placeholder).toBe("****");
  type(inputFor("Grafana API Token"), "tok");
  expect(s.current.grafToken).toBe("tok");
  expect(inputFor("Grafana API Token").placeholder).toBe("Enter token");
  expect(onDirty).toHaveBeenCalledTimes(3);

  await act(async () => {
    s.set({ promUrl: "http://elsewhere" });
  });
  expect(inputFor("Prometheus URL").value).toBe("http://elsewhere");
});

test("the monitoring namespace field appears only when its props are passed", async () => {
  const base: MonitoringValues = { promUrl: "", grafUrl: "", grafToken: "" };
  const bare = render(<MonitoringHarness init={base} probe={probe(base)} />);
  expect(body().queryByText("Monitoring Namespace")).toBeNull();
  bare.unmount();

  const init = { ...base, monNs: "monitoring" };
  const s = probe(init);
  render(<MonitoringHarness init={init} probe={s} />);
  expect(inputFor("Monitoring Namespace").value).toBe("monitoring");
  type(inputFor("Monitoring Namespace"), "obs");
  expect(s.current.monNs).toBe("obs");
  await act(async () => {
    s.set({ monNs: "other" });
  });
  expect(inputFor("Monitoring Namespace").value).toBe("other");
});
