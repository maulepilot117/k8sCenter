# k8sCenter Mobile

Cross-platform Flutter app delivering full parity with the k8sCenter web frontend on iOS and Android, phones and tablets. See `plans/mobile-app.md` for the milestone breakdown.

## Status

This directory holds the placeholder skeleton landed by PR-0 (theme generator output, plan documents, backend prerequisites). The Flutter project itself — `flutter create mobile/`, lib scaffolding, login + dashboard screens — begins in PR-1.

## Theme tokens

`lib/theme/themes.g.dart` is generated from `shared/themes/*.json` by `tools/theme-gen/main.ts`. Do not edit it by hand. Run `make check-themes` to verify parity with the web frontend's `frontend/assets/themes.generated.css`.
