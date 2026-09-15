import { useSignal } from "@preact/signals";
import { useEffect, useRef } from "preact/hooks";
import { Spinner } from "@/components/ui/Spinner.tsx";
import { IS_BROWSER } from "@/src/lib/is-browser.ts";

/**
 * The editor root's classes, shared by the SSR placeholder and the hydrated
 * root so the two cannot diverge.
 *
 * `relative` is load-bearing, not cosmetic: Monaco positions .view-lines and
 * .monaco-scrollable-element absolutely, and they resolve against the nearest
 * positioned ancestor. Without it they escape the editor box entirely.
 *
 * `bg-base` is what the SSR placeholder painted before these two roots were
 * reconciled. It has to stay on the shared class: the placeholder has no
 * children, so between first paint and hydration the bordered box would
 * otherwise show whatever the parent container paints, then snap to base once
 * the loading overlay mounts. The hydrated root pays nothing for it — Monaco
 * paints over it, and the failed-load branch swaps in a textarea that carries
 * its own background.
 */
const EDITOR_ROOT_CLASS =
  "relative bg-base rounded-md border border-border-primary";

export interface MonacoEditorProps {
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
type MonacoEditorInstance = any;
// deno-lint-ignore no-explicit-any
type MonacoModule = any;

/**
 * Monaco Editor component for YAML editing.
 * Monaco is loaded dynamically from esm.sh CDN to avoid SSR issues.
 * Falls back to a plain textarea if Monaco fails to load.
 *
 * This is a non-island component — import it inside islands only.
 */
export function MonacoEditor({
  value,
  onChange,
  readOnly = false,
  height = "500px",
  markers,
}: MonacoEditorProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<MonacoEditorInstance>(null);
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
          // @ts-expect-error: CDN import
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
          fixedOverflowWidgets: true,
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
            alwaysConsumeMouseWheel: true,
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
      severity:
        m.severity === "warning"
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
    // Must carry EDITOR_ROOT_CLASS, identical to the hydrated root below.
    // Preact hydration keeps the server-rendered root's class and only
    // recurses into children, so a class that differs here sticks for the
    // life of the page. This root previously said `bg-base` where the
    // hydrated root says `relative`; losing `position: relative` left
    // Monaco's absolutely-positioned .view-lines resolving against <body>,
    // which drew the code at x=62 behind the secondary nav instead of
    // inside the editor box.
    return <div style={{ height }} class={EDITOR_ROOT_CLASS} />;
  }

  if (failed.value) {
    // EDITOR_ROOT_CLASS here too, for the same reason as the placeholder
    // above. This branch renders on a post-mount Monaco load failure, by
    // which point the root is a live node whose props *do* get diffed -- so
    // a bare `relative` would strip the border and background off the box
    // at the exact moment it falls back to a plain textarea. Before
    // hydration it is stranded behind the SSR class and renders nothing at
    // all, which is why it read as harmless.
    return (
      <div style={{ height }} class={EDITOR_ROOT_CLASS}>
        <textarea
          value={value}
          onInput={(e) => onChange?.((e.target as HTMLTextAreaElement).value)}
          readOnly={readOnly}
          class="w-full h-full bg-base text-text-primary font-mono text-sm p-4 rounded-md border border-border-primary resize-none focus:outline-none focus:ring-2 focus:ring-brand"
          spellcheck={false}
        />
      </div>
    );
  }

  return (
    <div class={EDITOR_ROOT_CLASS} style={{ height }}>
      {loading.value && (
        <div class="absolute inset-0 z-10 flex items-center justify-center bg-base text-text-muted">
          <div class="flex items-center gap-2">
            <Spinner size="sm" />
            Loading editor...
          </div>
        </div>
      )}
      <div ref={containerRef} class="h-full w-full" />
    </div>
  );
}
