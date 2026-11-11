import { expect, test } from "bun:test";
import { render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });
const { default: YamlPreview } = await import("./YamlPreview.tsx");

test("HTML in the YAML is shown as text, not parsed into elements", () => {
  const { container } = render(
    <YamlPreview yaml={'data:\n  note: "<script>alert(1)</script>"'} />,
  );

  const pre = container.querySelector("pre");
  expect(pre?.querySelector("script")).toBeNull();
  expect(pre?.textContent).toContain('"<script>alert(1)</script>"');
});

test("keys and values are highlighted and lines are numbered", () => {
  const { container } = render(<YamlPreview yaml={"kind: ConfigMap"} />);

  const pre = container.querySelector("pre");
  expect(pre?.textContent).toContain("  1");
  expect(pre?.textContent).toContain("kind: ConfigMap");
  expect(pre?.querySelectorAll("span").length).toBeGreaterThan(1);
});
