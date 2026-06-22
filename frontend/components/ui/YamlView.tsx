// Lightweight YAML viewer with key/value coloring + line numbers.
// Solid (not glass) surface — it's a data/code view. Used by the resource
// detail YAML tab and the wizard live-manifest pane.

interface YamlViewProps {
  text: string;
  maxHeight?: number;
  fontSize?: number;
}

export default function YamlView(
  { text, maxHeight = 440, fontSize = 12.5 }: YamlViewProps,
) {
  const lines = text.split("\n").map((line, i) => {
    const lead = line.match(/^\s*/)?.[0].length ?? 0;
    let rest = line.slice(lead);
    let dash = false;
    if (rest.startsWith("- ")) {
      dash = true;
      rest = rest.slice(2);
    }
    const ci = rest.indexOf(":");
    let key = "", sep = "", val = "";
    if (ci >= 0) {
      key = rest.slice(0, ci);
      sep = ":";
      val = rest.slice(ci + 1);
    } else {
      val = rest;
    }
    return { no: i + 1, pad: lead * 7, dash, key, sep, val };
  });

  return (
    <div
      style={{
        fontFamily: "var(--font-mono)",
        fontSize: `${fontSize}px`,
        lineHeight: "20px",
        maxHeight: `${maxHeight}px`,
        overflow: "auto",
        padding: "14px 8px",
      }}
    >
      {lines.map((ln) => (
        <div key={ln.no} style={{ display: "flex", whiteSpace: "pre" }}>
          <span
            style={{
              width: "38px",
              textAlign: "right",
              paddingRight: "14px",
              color: "var(--text-muted)",
              userSelect: "none",
              flexShrink: 0,
              opacity: 0.6,
            }}
          >
            {ln.no}
          </span>
          <span style={{ paddingLeft: `${ln.pad}px` }}>
            {ln.dash && <span style={{ color: "var(--text-muted)" }}>-</span>}
            <span style={{ color: "var(--accent-2, var(--accent-secondary))" }}>
              {ln.key}
            </span>
            <span style={{ color: "var(--text-muted)" }}>{ln.sep}</span>
            <span style={{ color: "var(--accent)" }}>{ln.val}</span>
          </span>
        </div>
      ))}
    </div>
  );
}
