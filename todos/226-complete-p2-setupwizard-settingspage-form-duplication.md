---
status: complete
priority: p2
issue_id: "226"
tags: [code-review, frontend, duplication, refactor]
dependencies: []
---

# SetupWizard and SettingsPage Form Duplication

## Problem Statement

SetupWizard and SettingsPage duplicate identical form fields and save logic for monitoring/alerting configuration (~120 lines). Both define the same signals, same `inputClass` styling, same API payloads, and same validation. Any change to the monitoring or alerting form must be made in two places, risking drift.

**Location:** `frontend/islands/SetupWizard.tsx` lines 27-35, 112-190 and `frontend/islands/SettingsPage.tsx` lines 36-47, 92-140

## Proposed Solutions

### Option A: Extract shared MonitoringFields and AlertingFields components
- Create shared form components (e.g., `components/settings/MonitoringFields.tsx` and `components/settings/AlertingFields.tsx`) that accept signals/props for the field values. Both SetupWizard and SettingsPage use these shared components.
- **Effort:** Medium — extract components, pass signals as props, update both consumers.
- **Risk:** Low — straightforward refactor.

## Acceptance Criteria

- [ ] Monitoring form fields are defined in a single shared component
- [ ] Alerting form fields are defined in a single shared component
- [ ] SetupWizard and SettingsPage both use the shared components
- [ ] No duplicate form field definitions remain
- [ ] Both pages continue to function identically to current behavior

## Work Log

- 2026-03-22: Created from Phase 4A code review.
