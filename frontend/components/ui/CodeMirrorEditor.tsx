import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";
import { EditorState } from "@codemirror/state";
import {
  EditorView,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import {
  defaultKeymap,
  history,
  historyKeymap,
  indentWithTab,
} from "@codemirror/commands";
import {
  bracketMatching,
  foldGutter,
  indentOnInput,
  syntaxHighlighting,
} from "@codemirror/language";
import { highlightSelectionMatches, searchKeymap } from "@codemirror/search";
import { yaml } from "@codemirror/lang-yaml";
import { oneDark, oneDarkHighlightStyle } from "@codemirror/theme-one-dark";

export interface CodeMirrorEditorProps {
  value: string;
  onChange?: (value: string) => void;
  readOnly?: boolean;
}

/**
 * CodeMirror 6 YAML editor — uses native DOM rendering (no virtual viewport),
 * so it works correctly inside any container without scroll issues.
 */
export function CodeMirrorEditor({
  value,
  onChange,
  readOnly = false,
}: CodeMirrorEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const viewRef = useRef<EditorView | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  useEffect(() => {
    if (!IS_BROWSER || !containerRef.current) return;

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
        if (update.docChanged && onChangeRef.current) {
          onChangeRef.current(update.state.doc.toString());
        }
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

    const state = EditorState.create({ doc: value, extensions });
    const view = new EditorView({ state, parent: containerRef.current });
    viewRef.current = view;

    return () => {
      view.destroy();
      viewRef.current = null;
    };
  }, [readOnly]);

  // Sync external value changes without re-mounting
  useEffect(() => {
    const view = viewRef.current;
    if (!view) return;
    const current = view.state.doc.toString();
    if (current !== value) {
      view.dispatch({
        changes: { from: 0, to: current.length, insert: value },
      });
    }
  }, [value]);

  if (!IS_BROWSER) {
    return (
      <div class="h-full bg-base rounded-md border border-border-primary" />
    );
  }

  return <div ref={containerRef} class="h-full w-full" />;
}
