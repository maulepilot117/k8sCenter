import { useSignal } from"@preact/signals";
import { useEffect, useRef } from"preact/hooks";
import { applyTheme, currentTheme, getTheme, THEMES } from"@/lib/themes.ts";

export default function ThemeSelector() {
 const open = useSignal(false);
 const containerRef = useRef<HTMLDivElement>(null);

 // Close on outside click
 useEffect(() => {
 const handleClick = (e: MouseEvent) => {
 if (
 open.value && containerRef.current &&
 !containerRef.current.contains(e.target as Node)
 ) {
 open.value = false;
 }
 };
 document.addEventListener("mousedown", handleClick);
 return () => document.removeEventListener("mousedown", handleClick);
 }, []);

 const active = getTheme(currentTheme.value);

 return (
 <div style={{ position:"relative" }} ref={containerRef}>
 {/* Trigger button */}
 <button
 type="button"
 onClick={() => {
 open.value = !open.value;
 }}
 style={{
 display:"flex",
 alignItems:"center",
 gap:"6px",
 padding:"4px 10px",
 borderRadius:"6px",
 background:"var(--bg-elevated)",
 border:"1px solid var(--border-subtle)",
 color:"var(--text-secondary)",
 fontSize:"12px",
 cursor:"pointer",
 whiteSpace:"nowrap",
 }}
 >
 <span
 style={{
 width:"8px",
 height:"8px",
 borderRadius:"50%",
 background: active.colors.accent,
 flexShrink: 0,
 }}
 />
 <span>{active.name}</span>
 <svg
 width="10"
 height="10"
 viewBox="0 0 16 16"
 fill="currentColor"
 style={{
 opacity: 0.5,
 transform: open.value ?"rotate(180deg)" :"none",
 transition:"transform 0.15s ease",
 }}
 >
 <path d="M4 6l4 4 4-4" />
 </svg>
 </button>

 {/* Dropdown */}
 {open.value && (
 <div
 style={{
 position:"absolute",
 right: 0,
 top:"calc(100% + 4px)",
 width:"200px",
 background:"var(--bg-elevated)",
 border:"1px solid var(--border-primary)",
 borderRadius:"8px",
 boxShadow:"0 4px 16px rgba(0,0,0,0.3)",
 zIndex: 100,
 overflow:"hidden",
 padding:"4px 0",
 }}
 >
 {THEMES.map((theme) => {
 const isActive = theme.id === currentTheme.value;
 return (
 <button
 key={theme.id}
 type="button"
 onClick={() => {
 applyTheme(theme.id);
 open.value = false;
 }}
 style={{
 display:"flex",
 alignItems:"center",
 gap:"8px",
 width:"100%",
 padding:"8px 12px",
 background: isActive ?"var(--accent-dim)" :"transparent",
 border:"none",
 color:"var(--text-primary)",
 fontSize:"12px",
 cursor:"pointer",
 textAlign:"left",
 }}
 >
 {/* Color swatches */}
 <div style={{ display:"flex", gap:"3px", flexShrink: 0 }}>
 <span
 style={{
 width:"10px",
 height:"10px",
 borderRadius:"50%",
 background: theme.colors.accent,
 }}
 />
 <span
 style={{
 width:"10px",
 height:"10px",
 borderRadius:"50%",
 background: theme.colors.accentSecondary,
 }}
 />
 <span
 style={{
 width:"10px",
 height:"10px",
 borderRadius:"50%",
 background: theme.colors.success,
 }}
 />
 </div>
 <span style={{ fontWeight: isActive ? 600 : 400 }}>
 {theme.name}
 </span>
 </button>
 );
 })}
 </div>
 )}
 </div>
 );
}
