import { expect, mock, test } from "bun:test";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { NamespaceSelect } = await import("@/components/ui/NamespaceSelect.tsx");

test("choosing a namespace reports it", () => {
  const onChange = mock((_ns: string) => {});
  render(
    <NamespaceSelect
      value="default"
      namespaces={["default", "shop"]}
      onChange={onChange}
    />,
  );
  fireEvent.change(body().getByRole("combobox"), { target: { value: "shop" } });
  expect(onChange).toHaveBeenCalledWith("shop");
});

test("choosing Create New Namespace opens the name field without reporting", () => {
  const onChange = mock((_ns: string) => {});
  render(
    <NamespaceSelect
      value="default"
      namespaces={["default"]}
      onChange={onChange}
    />,
  );
  fireEvent.change(body().getByRole("combobox"), {
    target: { value: "__create__" },
  });
  body().getByPlaceholderText("new-namespace");
  expect(onChange).not.toHaveBeenCalled();
});

test("a name that already exists is rejected", () => {
  render(
    <NamespaceSelect
      value="default"
      namespaces={["default", "shop"]}
      onChange={() => {}}
    />,
  );
  fireEvent.change(body().getByRole("combobox"), {
    target: { value: "__create__" },
  });
  fireEvent.change(body().getByPlaceholderText("new-namespace"), {
    target: { value: "shop" },
  });
  fireEvent.click(body().getByRole("button", { name: "Create" }));
  body().getByText('Namespace "shop" already exists');
});
