---
status: pending
priority: p1
issue_id: "195"
tags: [code-review, security, frontend]
dependencies: []
---

# YAML Preview Injection via Unsanitized Label Values

## Problem Statement

`buildPolicyYaml` in `CiliumPolicyEditor.tsx` interpolates label keys and values directly into YAML strings without escaping. A label value containing `"` or newlines produces a misleading YAML preview, undermining the preview's purpose as a safety gate before creating network policies.

## Findings

- **CiliumPolicyEditor.tsx:688-699**: `lines.push(\`      ${l.key}: "${l.value}"\`)`
- **CiliumPolicyEditor.tsx:738-741**: Same pattern in `peerYaml` function
- Preview is in `<pre>` tag (no XSS), but YAML structure can be visually misleading
- The actual API payload via `buildPayload` is unaffected — only the preview is wrong
- Found by: Security Sentinel

## Proposed Solutions

### Option A: Add yamlEscape function
```typescript
function yamlEscape(s: string): string {
  if (/[":{}[\],&*?|<>=!%@\`#\n\r\t]/.test(s) || s.trim() !== s) {
    return '"' + s.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n') + '"';
  }
  return s;
}
```
- Pros: Correct YAML output, simple
- Effort: Small
- Risk: Low

## Acceptance Criteria
- [ ] Label values with special characters produce valid YAML in preview
- [ ] Preview accurately reflects what will be created

## Work Log
- 2026-03-16: Created from PR #36 code review

## Resources
- PR: #36
- File: `frontend/islands/CiliumPolicyEditor.tsx:688-699, 738-741`
