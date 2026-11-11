import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { Spinner } = await import("@/components/ui/Spinner.tsx");
const { Card } = await import("@/components/ui/Card.tsx");
const { Skeleton } = await import("@/components/ui/Skeleton.tsx");
const { default: GlassCard } = await import("@/components/ui/GlassCard.tsx");
const { RemoveButton } = await import("@/components/ui/RemoveButton.tsx");
const { Logo } = await import("@/src/components/ui/Logo.tsx");
const { Button } = await import("@/components/ui/Button.tsx");
const { Alert } = await import("@/components/ui/Alert.tsx");
const { Input } = await import("@/components/ui/Input.tsx");
const { Select } = await import("@/components/ui/Select.tsx");

const PROBE = "probe-class";

function classesOf(el: Element | null): string[] {
  expect(el).not.toBeNull();
  return (el?.getAttribute("class") ?? "").split(/\s+/);
}

test("Spinner keeps its own classes alongside className", () => {
  const { container } = render(<Spinner className={PROBE} />);
  const classes = classesOf(container.firstElementChild);
  expect(classes).toContain(PROBE);
  expect(classes).toContain("animate-spin");
});

test("Card keeps its own classes alongside className", () => {
  const { container } = render(<Card className={PROBE}>x</Card>);
  const classes = classesOf(container.firstElementChild);
  expect(classes).toContain(PROBE);
  expect(classes).toContain("rounded-lg");
});

test("Skeleton keeps its own classes alongside className", () => {
  const { container } = render(<Skeleton className={PROBE} />);
  const classes = classesOf(container.firstElementChild);
  expect(classes).toContain(PROBE);
  expect(classes).toContain("animate-pulse");
});

test("GlassCard keeps its own classes alongside className", () => {
  const { container } = render(<GlassCard className={PROBE}>x</GlassCard>);
  const classes = classesOf(container.firstElementChild);
  expect(classes).toContain(PROBE);
  expect(classes).toContain("glass");
});

test("RemoveButton keeps its own classes alongside className", () => {
  const { container } = render(
    <RemoveButton onClick={() => {}} className={PROBE} />,
  );
  const classes = classesOf(container.firstElementChild);
  expect(classes).toContain(PROBE);
  expect(classes).toContain("hover:text-danger");
});

test("Logo applies className to the svg root", () => {
  const { container } = render(<Logo className={PROBE} />);
  // The default text-brand is a fallback that className replaces.
  expect(classesOf(container.firstElementChild)).toContain(PROBE);
});

test("Button keeps its own classes alongside className", () => {
  const { container } = render(<Button className={PROBE}>x</Button>);
  const classes = classesOf(container.firstElementChild);
  expect(classes).toContain(PROBE);
  expect(classes).toContain("inline-flex");
});

test("Alert keeps its own classes alongside className", () => {
  const { container } = render(<Alert className={PROBE}>x</Alert>);
  const classes = classesOf(container.firstElementChild);
  expect(classes).toContain(PROBE);
  expect(classes).toContain("rounded-md");
});

test("Input puts className on the input beside its own classes", () => {
  const { container } = render(<Input label="Name" className={PROBE} />);
  const classes = classesOf(container.querySelector("input"));
  expect(classes).toContain(PROBE);
  expect(classes).toContain("shadow-sm");
});

test("Select puts className on the select beside its own classes", () => {
  const { container } = render(
    <Select
      label="Kind"
      className={PROBE}
      options={[{ value: "a", label: "A" }]}
    />,
  );
  const classes = classesOf(container.querySelector("select"));
  expect(classes).toContain(PROBE);
  expect(classes).toContain("shadow-sm");
});
