---
status: pending
priority: p2
issue_id: "112"
tags: [code-review, performance, step-7]
dependencies: []
---

# RESTMapper Not Cached in ClientFactory

## Problem Statement

`ClientFactory.RESTMapper()` creates a new `NewMemCacheClient` and `NewDeferredDiscoveryRESTMapper` on every call, discarding the cache each time. Every YAML request triggers fresh API discovery round-trips to the k8s API server.

## Findings

- File: `backend/internal/k8s/client.go` lines 153-156
- Each call to `RESTMapper()` allocates a new discovery client and mapper, negating the purpose of `MemCacheClient`
- YAML validate/apply/diff all call `RESTMapper()`, so every YAML operation pays full discovery cost

## Recommendation

Create the mapper once in `NewClientFactory` and store it as a field on the struct. Invalidate/reset only on cache miss (the `DeferredDiscoveryRESTMapper` handles this internally when stored persistently).
