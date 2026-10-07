---
title: A nil pointer stored in an interface is not nil
date: 2026-10-07
last_updated: 2026-10-07
category: docs/solutions
module: backend/internal/notifications
problem_type: runtime_error
component: service_layer
severity: high
related_components: [background_job]
tags: [go, typed-nil, interface, nil-guard, panic, recoverutil, wiring, notifications, release-d]
applies_when:
  - "Passing a possibly-nil concrete pointer to a constructor that takes an interface"
  - "Writing `if x == nil` against an interface field that a caller may have filled with a nil pointer"
  - "Adding a background loop that calls an optional collaborator"
---

# A nil pointer stored in an interface is not nil

**Status:** active convention. Found and fixed in PR #585 (a Release D hotfix, unrelated to incidents) after it was seen in the daily notification digest.

## The defect

`notifications.NewService` takes an `EmailSender` interface. `main.go` passed `alertNotifier`, a `*alerting.Notifier` that is nil when SMTP is not configured. An interface value holds a (type, value) pair; a nil `*alerting.Notifier` stored in it has a non-nil type, so the interface is non-nil. Every `emailSender == nil` guard in the service evaluated false, and `sendDigests` called a method on the nil pointer at 08:00 UTC. The panic ran in a background goroutine outside chi's recovery middleware, so it would have taken the process down.

The service had the right guards. The wiring defeated them.

## The fix, in three layers

1. **Fix it at the wiring site.** Assign the interface only from a non-nil pointer so a missing sender is a true nil interface:

   ```go
   var emailSender notifications.EmailSender
   if alertNotifier != nil {
       emailSender = alertNotifier
   }
   notifService = notifications.NewService(notifStore, hub, emailSender, fcmClient, logger)
   ```

2. **Make the constructor and the receiver tolerant.** `NewService` runs the argument through `normalizeEmailSender`, which turns a typed-nil pointer into a nil interface, so a future caller that repeats the mistake is still safe. `SMTPConfigured` on the notifier is nil-safe, so a call on a nil receiver answers "not configured" instead of dereferencing it.
3. **Wrap the loops with `internal/recoverutil`.** The dispatcher, digest and retention loops, and the per-channel send goroutine (with its semaphore release kept outside the wrapped closure), now run under `recoverutil`, so a panic that slips past the first two layers is logged and the loop continues. See `docs/solutions/backend-resilience-conventions.md`.

## Rules to apply elsewhere

- Never assign a concrete pointer that might be nil to an interface variable without checking it first. The check belongs where the pointer is known, not where the interface is consumed.
- A `== nil` guard on an interface field proves nothing about the pointer inside it. If a constructor accepts an optional interface, normalize it on the way in.
- Give optional collaborators nil-safe methods when "absent" is a legitimate state.
- Any goroutine outside the request stack gets `recoverutil`, because the first two rules are about discipline and the third is about blast radius.

## Known limitation recorded with the fix

When SMTP is unset at boot, the notification center has no email sender until restart: the alerting handler skips `UpdateConfig` when the notifier is nil. A real nil interface is the correct state for that case, but it does not pick up SMTP configured later.

## Tests

`backend/internal/notifications/service_nilsender_test.go` covers the typed-nil normalization and the digest path with no sender, and the per-channel dispatch recovery has a test hook of its own.
