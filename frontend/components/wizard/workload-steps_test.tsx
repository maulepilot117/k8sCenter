import { expect, mock, test } from "bun:test";
import type { EnvVarEntry, PortEntry } from "@/lib/wizard-types.ts";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { ContainerForm } = await import("./ContainerForm.tsx");
const { DeploymentBasicsStep } = await import("./DeploymentBasicsStep.tsx");
const { DeploymentNetworkStep } = await import("./DeploymentNetworkStep.tsx");
const { DeploymentResourcesStep } = await import(
  "./DeploymentResourcesStep.tsx"
);
const { ServiceBasicsStep } = await import("./ServiceBasicsStep.tsx");
const { ServicePortsStep } = await import("./ServicePortsStep.tsx");

type Change = (field: string, value: unknown) => void;
const spy = () => mock<Change>(() => {});

/** Types into a text-like field the way a keystroke does. */
function type(el: HTMLElement, value: string) {
  fireEvent.input(el, { target: { value } });
}

const containerProps = {
  image: "nginx:1",
  command: "",
  args: "",
  ports: [{ containerPort: 80, protocol: "TCP" }] as PortEntry[],
  envVars: [
    { name: "A", value: "1", configMapRef: "", secretRef: "", key: "" },
  ] as EnvVarEntry[],
  requestCpu: "100m",
  requestMemory: "",
  limitCpu: "",
  limitMemory: "",
  errors: {} as Record<string, string>,
};

test("ContainerForm shows its fields and error text", () => {
  render(
    <ContainerForm
      {...containerProps}
      errors={{
        image: "image is required",
        requestCpu: "bad cpu",
        "ports[0].containerPort": "bad port",
      }}
      onChange={spy()}
    />,
  );
  expect(body().getByPlaceholderText("nginx:latest")).toHaveProperty(
    "value",
    "nginx:1",
  );
  expect(body().getByPlaceholderText("100m")).toHaveProperty("value", "100m");
  body().getByText("image is required");
  body().getByText("bad cpu");
  body().getByText("bad port");
});

test("ContainerForm reports typed text and numbers", () => {
  const onChange = spy();
  render(<ContainerForm {...containerProps} onChange={onChange} />);
  type(body().getByPlaceholderText("nginx:latest"), "redis");
  expect(onChange).toHaveBeenLastCalledWith("image", "redis");
  type(body().getByPlaceholderText("128Mi"), "64Mi");
  expect(onChange).toHaveBeenLastCalledWith("requestMemory", "64Mi");
  type(body().getByPlaceholderText("80"), "8080");
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { containerPort: 8080, protocol: "TCP" },
  ]);
  type(body().getByPlaceholderText("80"), "abc");
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { containerPort: 0, protocol: "TCP" },
  ]);
});

test("ContainerForm adds and removes ports and env vars", () => {
  const onChange = spy();
  render(<ContainerForm {...containerProps} onChange={onChange} />);
  fireEvent.click(body().getByRole("button", { name: "+ Add Port" }));
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { containerPort: 80, protocol: "TCP" },
    { containerPort: 0, protocol: "TCP" },
  ]);
  fireEvent.click(body().getByTitle("Remove port"));
  expect(onChange).toHaveBeenLastCalledWith("ports", []);
  fireEvent.click(
    body().getByRole("button", { name: "+ Add Environment Variable" }),
  );
  expect(onChange).toHaveBeenLastCalledWith("envVars", [
    containerProps.envVars[0],
    { name: "", value: "", configMapRef: "", secretRef: "", key: "" },
  ]);
  fireEvent.click(body().getByTitle("Remove env var"));
  expect(onChange).toHaveBeenLastCalledWith("envVars", []);
});

test("ContainerForm switching an env var to a secret clears the other refs", () => {
  const onChange = spy();
  render(
    <ContainerForm
      {...containerProps}
      envVars={[
        { name: "A", value: "1", configMapRef: "", secretRef: "", key: "k" },
      ]}
      onChange={onChange}
    />,
  );
  fireEvent.change(body().getByLabelText("Source"), {
    target: { value: "secret" },
  });
  expect(onChange).toHaveBeenLastCalledWith("envVars", [
    { name: "A", value: "", configMapRef: "", secretRef: "", key: "k" },
  ]);
});

test("ContainerForm shows the key field for a configmap env var", () => {
  const onChange = spy();
  render(
    <ContainerForm
      {...containerProps}
      envVars={[
        { name: "A", value: "", configMapRef: "cm", secretRef: "", key: "" },
      ]}
      onChange={onChange}
    />,
  );
  type(body().getByPlaceholderText("data-key"), "k1");
  expect(onChange).toHaveBeenLastCalledWith("envVars", [
    { name: "A", value: "", configMapRef: "cm", secretRef: "", key: "k1" },
  ]);
});

test("DeploymentBasicsStep reports fields, replicas and labels", () => {
  const onChange = spy();
  render(
    <DeploymentBasicsStep
      name="web"
      namespace="default"
      image="nginx"
      replicas={2}
      labels={[{ key: "app", value: "web" }]}
      namespaces={["default"]}
      errors={{ name: "name taken", replicas: "too many" }}
      onChange={onChange}
    />,
  );
  body().getByText("name taken");
  body().getByText("too many");
  expect(body().getByLabelText("Replicas")).toHaveProperty("value", "2");
  type(body().getByPlaceholderText("my-deployment"), "api");
  expect(onChange).toHaveBeenLastCalledWith("name", "api");
  type(body().getByLabelText("Replicas"), "5");
  expect(onChange).toHaveBeenLastCalledWith("replicas", 5);
  type(body().getByLabelText("Replicas"), "");
  expect(onChange).toHaveBeenLastCalledWith("replicas", 0);
  fireEvent.click(body().getByRole("button", { name: "+ Add Label" }));
  expect(onChange).toHaveBeenLastCalledWith("labels", [
    { key: "app", value: "web" },
    { key: "", value: "" },
  ]);
  fireEvent.click(body().getByTitle("Remove labels"));
  expect(onChange).toHaveBeenLastCalledWith("labels", []);
});

test("DeploymentNetworkStep reports port and env edits", () => {
  const onChange = spy();
  const ports = [{ name: "http", containerPort: 80, protocol: "TCP" }];
  const envVars = [
    { name: "A", type: "literal" as const, value: "1", ref: "", key: "" },
  ];
  render(
    <DeploymentNetworkStep
      ports={ports}
      envVars={envVars}
      errors={{ "ports[0].containerPort": "bad port" }}
      onChange={onChange}
    />,
  );
  body().getByText("bad port");
  type(body().getByPlaceholderText("http"), "web");
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { name: "web", containerPort: 80, protocol: "TCP" },
  ]);
  type(body().getByPlaceholderText("80"), "9000");
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { name: "http", containerPort: 9000, protocol: "TCP" },
  ]);
  fireEvent.click(body().getByRole("button", { name: "+ Add Port" }));
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    ...ports,
    { name: "", containerPort: 0, protocol: "TCP" },
  ]);
  fireEvent.click(body().getByTitle("Remove port"));
  expect(onChange).toHaveBeenLastCalledWith("ports", []);
  type(body().getByPlaceholderText("value"), "2");
  expect(onChange).toHaveBeenLastCalledWith("envVars", [
    { ...envVars[0], value: "2" },
  ]);
  fireEvent.change(body().getByLabelText("Source"), {
    target: { value: "secret" },
  });
  expect(onChange).toHaveBeenLastCalledWith("envVars", [
    { ...envVars[0], type: "secret" },
  ]);
  fireEvent.click(
    body().getByRole("button", { name: "+ Add Environment Variable" }),
  );
  expect(onChange).toHaveBeenLastCalledWith("envVars", [
    ...envVars,
    { name: "", type: "literal", value: "", ref: "", key: "" },
  ]);
  fireEvent.click(body().getByTitle("Remove env var"));
  expect(onChange).toHaveBeenLastCalledWith("envVars", []);
});

const probe = {
  type: "http",
  path: "/h",
  port: 8080,
  initialDelaySeconds: 0,
  periodSeconds: 10,
};

test("DeploymentResourcesStep reports resources, probes and strategy", () => {
  const onChange = spy();
  render(
    <DeploymentResourcesStep
      cpuRequest="100m"
      memoryRequest=""
      cpuLimit=""
      memoryLimit=""
      livenessProbe={probe}
      readinessProbe={null}
      strategy={{ type: "RollingUpdate", maxSurge: "25%", maxUnavailable: "1" }}
      errors={{ "resources.requestCpu": "bad cpu" }}
      onChange={onChange}
    />,
  );
  body().getByText("bad cpu");
  type(body().getByLabelText("CPU Limit"), "1");
  expect(onChange).toHaveBeenLastCalledWith("cpuLimit", "1");
  type(body().getByLabelText("Path"), "/z");
  expect(onChange).toHaveBeenLastCalledWith("livenessProbe", {
    ...probe,
    path: "/z",
  });
  type(body().getByLabelText("Port"), "9090");
  expect(onChange).toHaveBeenLastCalledWith("livenessProbe", {
    ...probe,
    port: 9090,
  });
  const boxes = body().getAllByRole("checkbox");
  fireEvent.click(boxes[1]);
  expect(onChange).toHaveBeenLastCalledWith("readinessProbe", {
    type: "http",
    path: "/",
    port: 8080,
    initialDelaySeconds: 0,
    periodSeconds: 10,
  });
  fireEvent.click(boxes[0]);
  expect(onChange).toHaveBeenLastCalledWith("livenessProbe", null);
  type(body().getByLabelText("Max Surge"), "50%");
  expect(onChange).toHaveBeenLastCalledWith("strategy", {
    type: "RollingUpdate",
    maxSurge: "50%",
    maxUnavailable: "1",
  });
  const strategy = body().getAllByRole("combobox").at(-1) as HTMLElement;
  fireEvent.change(strategy, { target: { value: "Recreate" } });
  expect(onChange).toHaveBeenLastCalledWith("strategy", {
    type: "Recreate",
    maxSurge: "25%",
    maxUnavailable: "1",
  });
});

test("ServiceBasicsStep reports name, type and labels", () => {
  const onChange = spy();
  render(
    <ServiceBasicsStep
      name="svc"
      namespace="default"
      type="ClusterIP"
      labels={[{ key: "a", value: "b" }]}
      namespaces={["default"]}
      errors={{ name: "bad name", type: "bad type" }}
      onChange={onChange}
    />,
  );
  body().getByText("bad name");
  body().getByText("bad type");
  type(body().getByPlaceholderText("my-service"), "api");
  expect(onChange).toHaveBeenLastCalledWith("name", "api");
  fireEvent.change(body().getByLabelText("Service Type"), {
    target: { value: "NodePort" },
  });
  expect(onChange).toHaveBeenLastCalledWith("type", "NodePort");
  fireEvent.click(body().getByRole("button", { name: "+ Add Label" }));
  expect(onChange).toHaveBeenLastCalledWith("labels", [
    { key: "a", value: "b" },
    { key: "", value: "" },
  ]);
  fireEvent.click(body().getByTitle("Remove labels"));
  expect(onChange).toHaveBeenLastCalledWith("labels", []);
});

test("ServicePortsStep reports ports and selector edits", () => {
  const onChange = spy();
  const ports = [
    { name: "http", port: 80, targetPort: 8080, protocol: "TCP", nodePort: 0 },
  ];
  const view = render(
    <ServicePortsStep
      ports={ports}
      selector={[{ key: "app", value: "web" }]}
      serviceType="ClusterIP"
      errors={{ selector: "need selector", ports: "need ports" }}
      onChange={onChange}
    />,
  );
  body().getByText("need selector");
  body().getByText("need ports");
  expect(body().queryByPlaceholderText("30080")).toBeNull();
  type(body().getByPlaceholderText("http"), "web");
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { ...ports[0], name: "web" },
  ]);
  type(body().getByPlaceholderText("8080"), "9");
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { ...ports[0], targetPort: 9 },
  ]);
  fireEvent.click(body().getByRole("button", { name: "+ Add Port" }));
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    ...ports,
    { name: "", port: 0, targetPort: 0, protocol: "TCP", nodePort: 0 },
  ]);
  fireEvent.click(body().getByTitle("Remove port"));
  expect(onChange).toHaveBeenLastCalledWith("ports", []);
  fireEvent.click(body().getByRole("button", { name: "+ Add Selector" }));
  expect(onChange).toHaveBeenLastCalledWith("selector", [
    { key: "app", value: "web" },
    { key: "", value: "" },
  ]);
  fireEvent.click(body().getByTitle("Remove pod selector"));
  expect(onChange).toHaveBeenLastCalledWith("selector", []);
  view.rerender(
    <ServicePortsStep
      ports={ports}
      selector={[]}
      serviceType="NodePort"
      errors={{}}
      onChange={onChange}
    />,
  );
  type(body().getByPlaceholderText("30080"), "30100");
  expect(onChange).toHaveBeenLastCalledWith("ports", [
    { ...ports[0], nodePort: 30100 },
  ]);
});
