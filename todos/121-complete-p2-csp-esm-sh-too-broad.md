---
status: pending
priority: p2
issue_id: "121"
tags: [code-review, security, step-7]
dependencies: []
---

# CSP Allows Full esm.sh Domain — Too Broad

## Problem Statement

The CSP allows the full `https://esm.sh` domain in script-src, style-src, connect-src, and worker-src. Since esm.sh serves arbitrary npm packages, this effectively neutralizes CSP script restrictions for an attacker with an XSS vector.

## Findings

- File: `frontend/routes/_middleware.ts` line 19
- `https://esm.sh` is listed as a full domain allowlist in multiple CSP directives
- Any npm package hosted on esm.sh becomes executable in the application context

## Recommendation

Pin to the specific Monaco Editor URL: `https://esm.sh/monaco-editor@0.52.2/` (or the exact version in use). This limits the CSP allowlist to only the required resource.
