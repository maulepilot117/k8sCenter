/** @jsxImportSource react */
import { describe, expect, mock, test } from "bun:test";
import { body, fireEvent, render, setupDom } from "@/src/test-utils/render.tsx";

setupDom({ browser: true });

const { default: TextField } = await import("./TextField.tsx");
const { default: Stepper } = await import("./Stepper.tsx");
const { default: Toggle } = await import("./Toggle.tsx");
const { default: Segmented } = await import("./Segmented.tsx");
const { default: Select } = await import("./Select.tsx");
const { SearchBar } = await import("../SearchBar.tsx");
const { KeyValueListEditor } = await import("../KeyValueListEditor.tsx");

describe("form controls", () => {
  test("TextField reports the typed value on every keystroke", () => {
    const onInput = mock((_v: string) => {});
    const { getByPlaceholderText } = render(
      <TextField value="" onInput={onInput} placeholder="image" />,
    );
    const input = getByPlaceholderText("image") as HTMLInputElement;
    fireEvent.input(input, { target: { value: "n" } });
    fireEvent.input(input, { target: { value: "ng" } });
    expect(onInput.mock.calls.map((c) => c[0])).toEqual(["n", "ng"]);
  });

  test("TextField password type renders a password input", () => {
    const { getByPlaceholderText } = render(
      <TextField value="" onInput={() => {}} placeholder="p" type="password" />,
    );
    expect((getByPlaceholderText("p") as HTMLInputElement).type).toBe(
      "password",
    );
  });

  test("Stepper steps by one and clamps to min and max", () => {
    const onChange = mock((_v: number) => {});
    const { getByText, rerender } = render(
      <Stepper value={3} min={1} max={4} onChange={onChange} />,
    );
    fireEvent.click(getByText("+"));
    fireEvent.click(getByText("−"));
    expect(onChange.mock.calls.map((c) => c[0])).toEqual([4, 2]);

    onChange.mockClear();
    rerender(<Stepper value={4} min={1} max={4} onChange={onChange} />);
    fireEvent.click(getByText("+"));
    rerender(<Stepper value={1} min={1} max={4} onChange={onChange} />);
    fireEvent.click(getByText("−"));
    expect(onChange.mock.calls.map((c) => c[0])).toEqual([4, 1]);
  });

  test("Toggle reports the inverted state", () => {
    const onChange = mock((_v: boolean) => {});
    const { getByRole, rerender } = render(
      <Toggle checked={false} onChange={onChange} />,
    );
    const sw = getByRole("switch");
    expect(sw.getAttribute("aria-checked")).toBe("false");
    fireEvent.click(sw);
    rerender(<Toggle checked={true} onChange={onChange} />);
    expect(getByRole("switch").getAttribute("aria-checked")).toBe("true");
    fireEvent.click(getByRole("switch"));
    expect(onChange.mock.calls.map((c) => c[0])).toEqual([true, false]);
  });

  test("Segmented reports the clicked option", () => {
    const onChange = mock((_v: string) => {});
    const { getByText } = render(
      <Segmented
        value="ClusterIP"
        options={["ClusterIP", "NodePort"]}
        onChange={onChange}
      />,
    );
    fireEvent.click(getByText("NodePort"));
    expect(onChange.mock.calls.map((c) => c[0])).toEqual(["NodePort"]);
  });

  test("Select reports the chosen value for string and object options", () => {
    const onChange = mock((_v: string) => {});
    const { getByRole } = render(
      <Select
        value="a"
        options={["a", { value: "b", label: "Bee" }]}
        onChange={onChange}
      />,
    );
    const select = getByRole("combobox") as HTMLSelectElement;
    expect(select.value).toBe("a");
    fireEvent.change(select, { target: { value: "b" } });
    expect(onChange.mock.calls.map((c) => c[0])).toEqual(["b"]);
  });

  test("SearchBar reports the typed value on every keystroke", () => {
    const onInput = mock((_v: string) => {});
    const { getByPlaceholderText } = render(
      <SearchBar value="" onInput={onInput} />,
    );
    const input = getByPlaceholderText("Search resources...");
    fireEvent.input(input, { target: { value: "p" } });
    fireEvent.input(input, { target: { value: "po" } });
    expect(onInput.mock.calls.map((c) => c[0])).toEqual(["p", "po"]);
  });

  test("KeyValueListEditor reports key and value edits with their row", () => {
    const onUpdate = mock((_i: number, _f: "key" | "value", _v: string) => {});
    render(
      <KeyValueListEditor
        label="Labels"
        entries={[{ key: "a", value: "1" }]}
        onUpdate={onUpdate}
        onAdd={() => {}}
        onRemove={() => {}}
      />,
    );
    fireEvent.input(body().getByPlaceholderText("key"), {
      target: { value: "ab" },
    });
    fireEvent.input(body().getByPlaceholderText("value"), {
      target: { value: "12" },
    });
    expect(onUpdate.mock.calls).toEqual([
      [0, "key", "ab"],
      [0, "value", "12"],
    ]);
  });
});
