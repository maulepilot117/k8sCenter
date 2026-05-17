// OIDC "Sign in with X" button for the login screen.
//
// Provider-agnostic — per-provider branding (Google logo, Microsoft logo,
// etc.) is deferred per the M5 plan's "Per-provider OIDC mobile button
// styling" follow-up note. Today every OIDC button renders as a
// neutral filled button labelled "Sign in with {DisplayName}".

import 'package:flutter/material.dart';

import '../theme/kube_theme_builder.dart';
import 'auth_repository.dart';

class OIDCProviderButton extends StatelessWidget {
  const OIDCProviderButton({
    super.key,
    required this.provider,
    required this.onTap,
    this.isLoading = false,
    this.isDisabled = false,
  });

  /// The provider this button represents. [AuthProvider.name] becomes
  /// the button label suffix.
  final AuthProvider provider;

  /// Tap handler. Ignored when [isLoading] or [isDisabled] is true.
  final VoidCallback onTap;

  /// When true, renders an inline progress indicator and ignores taps.
  /// The OIDC controller drives this from [OIDCFlowLaunching] +
  /// [OIDCFlowExchanging] states.
  final bool isLoading;

  /// When true, disables the button entirely. Used to gray out
  /// non-active OIDC buttons while another OIDC flow is in flight.
  final bool isDisabled;

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final enabled = !isLoading && !isDisabled;

    return FilledButton(
      key: ValueKey('login-oidc-${provider.id}'),
      onPressed: enabled ? onTap : null,
      style: FilledButton.styleFrom(
        backgroundColor: colors.bgSurface,
        foregroundColor: colors.textPrimary,
        disabledBackgroundColor: colors.bgSurface,
        disabledForegroundColor: colors.textMuted,
        side: BorderSide(color: colors.borderSubtle),
      ),
      child: isLoading
          ? const SizedBox(
              height: 20,
              width: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : Text('Sign in with ${provider.name}'),
    );
  }
}
