/** @jsxImportSource react */
import { expect, mock, test } from "bun:test";
import { signal } from "@preact/signals-core";
import "react";
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

function alertingSignals() {
  return {
    alertEnabled: signal(false),
    smtpHost: signal("mail.local"),
    smtpPort: signal(25),
    smtpUser: signal("bob"),
    smtpPass: signal("****"),
    smtpFrom: signal("a@b.c"),
  };
}

test("alerting fields show the signal values and write typed values back", () => {
  const s = alertingSignals();
  const onDirty = mock(() => {});
  render(<AlertingFields {...s} onDirty={onDirty} />);

  expect(inputFor("SMTP Host").value).toBe("mail.local");
  expect(inputFor("SMTP Port").value).toBe("25");

  type(inputFor("SMTP Host"), "smtp.example.com");
  expect(s.smtpHost.value).toBe("smtp.example.com");
  type(inputFor("SMTP Username"), "alice");
  expect(s.smtpUser.value).toBe("alice");
  type(inputFor("From Address"), "x@y.z");
  expect(s.smtpFrom.value).toBe("x@y.z");
  type(inputFor("SMTP Port"), "2525");
  expect(s.smtpPort.value).toBe(2525);
  type(inputFor("SMTP Port"), "junk");
  expect(s.smtpPort.value).toBe(587);
  expect(onDirty).toHaveBeenCalledTimes(5);

  expect(s.alertEnabled.value).toBe(false);
  fireEvent.click(body().getByRole("switch"));
  expect(s.alertEnabled.value).toBe(true);
  expect(onDirty).toHaveBeenCalledTimes(6);
});

test("alerting fields show a value set from outside", async () => {
  const s = alertingSignals();
  render(<AlertingFields {...s} />);
  await act(async () => {
    s.smtpHost.value = "changed.example";
    s.smtpPort.value = 465;
    s.alertEnabled.value = true;
  });
  expect(inputFor("SMTP Host").value).toBe("changed.example");
  expect(inputFor("SMTP Port").value).toBe("465");
  expect(body().getByRole("switch").getAttribute("aria-checked")).toBe("true");
});

test("the SMTP password is a password input and keeps the stored-mask placeholder", async () => {
  const s = alertingSignals();
  render(<AlertingFields {...s} />);
  const pass = inputFor("SMTP Password");
  expect(pass.type).toBe("password");
  expect(pass.placeholder).toBe("****");
  type(pass, "hunter2");
  expect(s.smtpPass.value).toBe("hunter2");
  expect(inputFor("SMTP Password").placeholder).toBe("");
});

test("rate limit and recipients appear only when their signals are passed", () => {
  const s = alertingSignals();
  const bare = render(<AlertingFields {...s} />);
  expect(body().queryByText("Rate Limit (per hour)")).toBeNull();
  expect(body().queryByText("Recipients")).toBeNull();
  bare.unmount();

  const alertRate = signal(5);
  const alertRecipients = signal("a@b.c");
  render(
    <AlertingFields
      {...s}
      alertRate={alertRate}
      alertRecipients={alertRecipients}
    />,
  );
  expect(inputFor("Rate Limit (per hour)").value).toBe("5");
  type(inputFor("Rate Limit (per hour)"), "20");
  expect(alertRate.value).toBe(20);
  type(inputFor("Rate Limit (per hour)"), "");
  expect(alertRate.value).toBe(5);
  type(inputFor("Recipients"), "x@y.z, q@r.s");
  expect(alertRecipients.value).toBe("x@y.z, q@r.s");
});

test("monitoring fields write typed values back and keep the token masked", async () => {
  const promUrl = signal("http://p");
  const grafUrl = signal("http://g");
  const grafToken = signal("****");
  const onDirty = mock(() => {});
  render(
    <MonitoringFields
      promUrl={promUrl}
      grafUrl={grafUrl}
      grafToken={grafToken}
      onDirty={onDirty}
    />,
  );
  expect(inputFor("Prometheus URL").value).toBe("http://p");
  type(inputFor("Prometheus URL"), "http://prom:9090");
  expect(promUrl.value).toBe("http://prom:9090");
  type(inputFor("Grafana URL"), "http://graf:3000");
  expect(grafUrl.value).toBe("http://graf:3000");
  expect(inputFor("Grafana API Token").type).toBe("password");
  expect(inputFor("Grafana API Token").placeholder).toBe("****");
  type(inputFor("Grafana API Token"), "tok");
  expect(grafToken.value).toBe("tok");
  expect(inputFor("Grafana API Token").placeholder).toBe("Enter token");
  expect(onDirty).toHaveBeenCalledTimes(3);

  await act(async () => {
    promUrl.value = "http://elsewhere";
  });
  expect(inputFor("Prometheus URL").value).toBe("http://elsewhere");
});

test("the monitoring namespace field appears only when its signal is passed", async () => {
  const base = {
    promUrl: signal(""),
    grafUrl: signal(""),
    grafToken: signal(""),
  };
  const bare = render(<MonitoringFields {...base} />);
  expect(body().queryByText("Monitoring Namespace")).toBeNull();
  bare.unmount();

  const monNs = signal("monitoring");
  render(<MonitoringFields {...base} monNs={monNs} />);
  expect(inputFor("Monitoring Namespace").value).toBe("monitoring");
  type(inputFor("Monitoring Namespace"), "obs");
  expect(monNs.value).toBe("obs");
  await act(async () => {
    monNs.value = "other";
  });
  expect(inputFor("Monitoring Namespace").value).toBe("other");
});
