import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { IS_BROWSER } from "fresh/runtime";

interface YamlEditorProps {
  /** Initial YAML content */
  value: string;
  /** Called when content changes */
  onChange?: (value: string) => void;
  /** Whether the editor is read-only */
  readOnly?: boolean;
  /** Editor height (CSS value) */
  height?: string;
  /** Validation markers to display */
  markers?: Array<{
    line: number;
    message: string;
    severity?: "error" | "warning" | "info";
  }>;
}

// Monaco types — loaded dynamically, so we use `any` for the instance refs
// deno-lint-ignore no-explicit-any
type MonacoEditor = any;
// deno-lint-ignore no-explicit-any
type MonacoModule = any;

/**
 * YAML editor island wrapping Monaco Editor.
 * Monaco is loaded dynamically from esm.sh CDN to avoid SSR issues.
 * Falls back to a plain textarea if Monaco fails to load.
 */
export default function YamlEditor({
  value,
  onChange,
  readOnly = false,
  height = "500px",
  markers,
}: YamlEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<MonacoEditor>(null);
  const monacoRef = useRef<MonacoModule>(null);
  const loading = useSignal(true);
  const failed = useSignal(false);

  // Track the latest value prop to avoid update loops
  const latestValueRef = useRef(value);
  latestValueRef.current = value;

  // Guard to suppress onChange during programmatic setValue calls
  const isSettingExternally = useRef(false);

  // Initialize Monaco
  useEffect(() => {
    if (!IS_BROWSER || !containerRef.current) return;

    let disposed = false;

    async function initMonaco() {
      try {
        // Dynamic import from esm.sh CDN
        const monaco = await import(
          // @ts-ignore: CDN import
          "https://esm.sh/monaco-editor@0.52.2/esm/vs/editor/editor.api.js"
        );

        if (disposed) return;

        monacoRef.current = monaco;

        const editor = monaco.editor.create(containerRef.current!, {
          value: latestValueRef.current,
          language: "yaml",
          theme: "vs-dark",
          minimap: { enabled: false },
          automaticLayout: true,
          readOnly,
          scrollBeyondLastLine: false,
          fontSize: 13,
          tabSize: 2,
          wordWrap: "on" as const,
          lineNumbers: "on" as const,
          renderWhitespace: "selection" as const,
          bracketPairColorization: { enabled: true },
          scrollbar: {
            verticalScrollbarSize: 10,
            horizontalScrollbarSize: 10,
          },
          padding: { top: 8 },
        });

        if (disposed) {
          editor.dispose();
          return;
        }

        editor.onDidChangeModelContent(() => {
          if (isSettingExternally.current) return;
          const newValue = editor.getValue();
          if (onChange) {
            onChange(newValue);
          }
        });

        editorRef.current = editor;
        loading.value = false;
      } catch (err) {
        console.error("Failed to load Monaco editor:", err);
        if (!disposed) {
          failed.value = true;
          loading.value = false;
        }
      }
    }

    initMonaco();

    return () => {
      disposed = true;
      const editor = editorRef.current;
      if (editor) {
        editor.getModel()?.dispose();
        editor.dispose();
      }
      editorRef.current = null;
      monacoRef.current = null;
    };
  }, []); // Only init once

  // Update readOnly when prop changes
  useEffect(() => {
    if (editorRef.current) {
      editorRef.current.updateOptions({ readOnly });
    }
  }, [readOnly]);

  // Update value from props (external changes only — don't clobber user typing)
  useEffect(() => {
    const editor = editorRef.current;
    if (editor && value !== editor.getValue()) {
      // Suppress onChange callback during programmatic setValue
      isSettingExternally.current = true;
      const position = editor.getPosition();
      editor.setValue(value);
      if (position) {
        editor.setPosition(position);
      }
      isSettingExternally.current = false;
    }
  }, [value]);

  // Update validation markers
  useEffect(() => {
    const editor = editorRef.current;
    const monaco = monacoRef.current;
    if (!editor || !monaco) return;

    const model = editor.getModel();
    if (!model) return;

    if (!markers || markers.length === 0) {
      monaco.editor.setModelMarkers(model, "kubecenter", []);
      return;
    }

    const monacoMarkers = markers.map((m) => ({
      severity: m.severity === "warning"
        ? monaco.MarkerSeverity.Warning
        : m.severity === "info"
        ? monaco.MarkerSeverity.Info
        : monaco.MarkerSeverity.Error,
      message: m.message,
      startLineNumber: m.line,
      startColumn: 1,
      endLineNumber: m.line,
      endColumn: model.getLineMaxColumn(m.line),
      source: "kubecenter",
    }));

    monaco.editor.setModelMarkers(model, "kubecenter", monacoMarkers);
  }, [markers]);

  // Fallback textarea for when Monaco fails to load
  if (!IS_BROWSER) {
    return (
      <div
        style={{ height }}
        class="bg-slate-900 rounded-md border border-slate-700"
      />
    );
  }

  if (failed.value) {
    return (
      <div style={{ height }} class="relative">
        <textarea
          value={value}
          onInput={(e) => onChange?.((e.target as HTMLTextAreaElement).value)}
          readOnly={readOnly}
          class="w-full h-full bg-slate-900 text-slate-100 font-mono text-sm p-4 rounded-md border border-slate-700 resize-none focus:outline-none focus:ring-2 focus:ring-brand"
          spellcheck={false}
        />
      </div>
    );
  }

  return (
    <div
      class="relative rounded-md overflow-hidden border border-slate-700"
      style={{ height }}
    >
      {loading.value && (
        <div class="absolute inset-0 z-10 flex items-center justify-center bg-slate-900 text-slate-400">
          <div class="flex items-center gap-2">
            <svg
              class="animate-spin h-4 w-4"
              viewBox="0 0 24 24"
              fill="none"
            >
              <circle
                class="opacity-25"
                cx="12"
                cy="12"
                r="10"
                stroke="currentColor"
                stroke-width="4"
              />
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"
              />
            </svg>
            Loading editor...
          </div>
        </div>
      )}
      <div ref={containerRef} class="h-full w-full" />
    </div>
  );
}
