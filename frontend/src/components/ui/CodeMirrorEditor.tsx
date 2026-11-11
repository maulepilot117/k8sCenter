import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from "@codemirror/commands";
import { yaml } from "@codemirror/lang-yaml";
import {
  bracketMatching,
  foldGutter,
  indentOnInput,
  syntaxHighlighting,
} from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { Annotation, EditorState } from "@codemirror/state";
import { oneDark, oneDarkHighlightStyle } from "@codemirror/theme-one-dark";
import {
  EditorView,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import { useEffect, useRef } from "react";
import { useIsClient } from "@/src/lib/use-is-client.ts";
import { useLatest } from "@/src/lib/use-latest.ts";

export interface CodeMirrorEditorProps {
  value: string;
  onChange?: (value: string) => void;
  readOnly?: boolean;
}

/** Marks a document replace that came in through `value`, not from typing. */
const fromValueProp = Annotation.define<boolean>();

/**
 * CodeMirror 6 YAML editor — uses native DOM rendering (no virtual viewport),
 * so it works correctly inside any container without scroll issues.
 *
 * Controlled: edits go out through `onChange` and come back as `value`. The
 * parent's re-render can trail the editor by a keystroke or more, so a
 * `value` that is text this editor already emitted is the parent catching up,
 * not a change to apply. Only any other `value` replaces the document, and
 * that replace is not reported back through `onChange`.
 */
export function CodeMirrorEditor({
  value,
  onChange,
  readOnly = false,
}: CodeMirrorEditorProps) {
  const isClient = useIsClient();
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useLatest(onChange);
  // Remounting for readOnly seeds the new editor with the current value.
  const valueRef = useLatest(value);
  // Texts sent through onChange that no `value` has caught up with yet,
  // oldest first.
  const inFlight = useRef<string[]>([]);

  useEffect(() => {
    if (!isClient || !containerRef.current) return;

    const extensions = [
      lineNumbers(),
      highlightSpecialChars(),
      history(),
      foldGutter(),
      indentOnInput(),
      bracketMatching(),
      highlightSelectionMatches(),
      keymap.of([...defaultKeymap, ...historyKeymap, indentWithTab]),
      ...searchKeymap.map((k) => keymap.of([k])),
      yaml(),
      oneDark,
      syntaxHighlighting(oneDarkHighlightStyle),
      EditorView.lineWrapping,
      EditorState.readOnly.of(readOnly),
      EditorView.editable.of(!readOnly),
      EditorView.updateListener.of((update) => {
        if (!update.docChanged || !onChangeRef.current) return;
        if (update.transactions.some((tr) => tr.annotation(fromValueProp))) {
          return;
        }
        const text = update.state.doc.toString();
        inFlight.current.push(text);
        onChangeRef.current(text);
      }),
      // Match the dark bg/font styling
      EditorView.theme({
        "&": {
          fontSize: "13px",
          height: "100%",
        },
        ".cm-scroller": {
          fontFamily:
            "ui-monospace, SFMono-Regular, 'SF Mono', Menlo, Consolas, monospace",
        },
      }),
    ];

    inFlight.current = [];
    const state = EditorState.create({ doc: valueRef.current, extensions });
    const view = new EditorView({ state, parent: containerRef.current });
    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, [isClient, readOnly]);

  // Sync external value changes without re-mounting
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const caughtUp = inFlight.current.indexOf(value);
    if (caughtUp !== -1) {
      // The parent has now seen this emission and every earlier one.
      inFlight.current.splice(0, caughtUp + 1);
      return;
    }
    inFlight.current = [];
    const current = view.state.doc.toString();
    if (current !== value) {
      view.dispatch({
        changes: { from: 0, to: current.length, insert: value },
        annotations: fromValueProp.of(true),
      });
    }
  }, [value]);

  if (!isClient) {
    return (
      <div className="h-full bg-base rounded-md border border-border-primary" />
    );
  }

  return <div ref={containerRef} className="h-full w-full" />;
}
