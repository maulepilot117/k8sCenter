---
status: complete
priority: p3
issue_id: 256
tags: [simplicity, frontend, code-review, phase4c]
---

## Problem Statement
All 4 wizard islands duplicate identical patterns: useNamespaces fetch (~10 lines x4), useDirtyGuard (~10 lines x4), useStorageClasses (~16 lines x3), ACCESS_MODES constant (22 lines x2), inputClass string (x4). ~160 LOC of duplication.

## Proposed Solution
Extract shared hooks: useNamespaces(), useDirtyGuard(), useStorageClasses(). Move ACCESS_MODES and StorageClassItem to wizard-constants.ts.
