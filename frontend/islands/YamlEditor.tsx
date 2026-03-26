import {
  MonacoEditor,
  type MonacoEditorProps,
} from "../components/ui/MonacoEditor.tsx";

/**
 * YamlEditor island — thin wrapper around MonacoEditor component.
 * This island boundary is needed so that server-rendered pages (YamlApplyPage,
 * ResourceDetail) can use Monaco without importing the component directly
 * (which would require them to be islands too).
 */
export default function YamlEditor(props: MonacoEditorProps) {
  return <MonacoEditor {...props} />;
}
