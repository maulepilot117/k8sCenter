---
status: pending
priority: p1
issue_id: "110"
tags: [code-review, performance, step-7]
dependencies: []
---

# Dynamic Client Not Cached, Causes Connection Churn Under Load

## Problem Statement

`DynamicClientForUser` creates a new `dynamic.Interface` (with new TLS connection) on every call, unlike `ClientForUser` which caches in `sync.Map` with 5-min TTL. Every YAML validate/apply/diff/export request creates a fresh client. Under load this causes connection churn and potential resource exhaustion.

## Findings

- **File**: `backend/internal/k8s/client.go` lines 141-148 — `DynamicClientForUser` creates a new client on every invocation with no caching.

## Recommendation

Add a parallel `sync.Map` cache for dynamic clients with the same 5-minute TTL and cache key scheme used by the typed clientset cache in `ClientForUser`.
