// Shared helpers for widget tests that need to drive Flutter's platform
// override. `withPlatform` sets `debugDefaultTargetPlatformOverride` for
// the duration of [body] and restores the prior value in a `finally`
// block so the framework's invariant check (which runs BEFORE `tearDown`)
// sees the original state.

import 'package:flutter/foundation.dart';

Future<void> withPlatform(
  TargetPlatform platform,
  Future<void> Function() body,
) async {
  final prior = debugDefaultTargetPlatformOverride;
  debugDefaultTargetPlatformOverride = platform;
  try {
    await body();
  } finally {
    debugDefaultTargetPlatformOverride = prior;
  }
}
