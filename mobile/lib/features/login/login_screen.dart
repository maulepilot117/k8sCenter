// Login screen — username + password, optional provider dropdown when
// the backend reports more than the default local provider, OIDC "Sign
// in with X" buttons for each OIDC provider, error chrome for ApiError
// messages from /v1/auth/login + OIDC flow errors from OIDCController.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/auth_repository.dart';
import '../../auth/auth_state.dart';
import '../../auth/oidc_controller.dart';
import '../../auth/oidc_widgets.dart';
import '../../theme/kube_theme_builder.dart';
import 'login_controller.dart';

class LoginScreen extends ConsumerStatefulWidget {
  const LoginScreen({super.key});

  @override
  ConsumerState<LoginScreen> createState() => _LoginScreenState();
}

class _LoginScreenState extends ConsumerState<LoginScreen> {
  final _formKey = GlobalKey<FormState>();
  final _usernameController = TextEditingController();
  final _passwordController = TextEditingController();
  String _selectedProvider = 'local';

  @override
  void dispose() {
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    await ref.read(authRepositoryProvider.notifier).login(
          username: _usernameController.text.trim(),
          password: _passwordController.text,
          provider: _selectedProvider,
        );
  }

  void _startOidc(String providerID) {
    // Clear any prior error before kicking off a fresh flow — otherwise
    // a stale error banner would persist briefly above the spinner.
    ref.read(oidcControllerProvider.notifier).clearError();
    ref.read(oidcControllerProvider.notifier).startFlow(providerID);
  }

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final authState = ref.watch(authRepositoryProvider);
    final providersAsync = ref.watch(authProvidersProvider);
    final oidcState = ref.watch(oidcControllerProvider);
    final isAuthenticating = authState is AuthAuthenticating;

    // Identify the OIDC provider whose button (if any) should render
    // its in-flight spinner. Other OIDC buttons + the credential form
    // are disabled while ANY OIDC flow is in flight to prevent
    // concurrent flows that would invalidate the persisted PKCE state.
    String? oidcLaunchingProviderID;
    if (oidcState is OIDCFlowLaunching) {
      oidcLaunchingProviderID = oidcState.providerID;
    } else if (oidcState is OIDCFlowExchanging) {
      oidcLaunchingProviderID = oidcState.providerID;
    }
    final oidcInFlight = oidcLaunchingProviderID != null;
    final formsDisabled = isAuthenticating || oidcInFlight;

    // OIDC flow error message — rendered above the OIDC buttons section
    // when present. Auth-machine errors render in the credential block
    // (existing behaviour) so the two error surfaces don't collide.
    final oidcError = oidcState is OIDCFlowError ? oidcState : null;

    return Scaffold(
      backgroundColor: colors.bgBase,
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 360),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Form(
              key: _formKey,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    'k8sCenter',
                    textAlign: TextAlign.center,
                    style: TextStyle(
                      color: colors.textPrimary,
                      fontSize: 28,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 32),
                  if (authState is AuthUnauthenticated &&
                      authState.errorMessage != null)
                    _errorBanner(colors, authState.errorMessage!,
                        keyValue: 'login-error'),
                  providersAsync.when(
                    data: (providers) {
                      final credentialProviders = providers
                          .where((p) => p.isCredentialProvider)
                          .toList(growable: false);
                      final oidcProviders = providers
                          .where((p) => !p.isCredentialProvider)
                          .toList(growable: false);

                      // Hide the credential form ONLY when the backend
                      // reports OIDC providers without any credential
                      // providers (OIDC-only deployment). The implicit
                      // local provider in homelab + an empty providers
                      // list (network failure / fresh setup) both fall
                      // back to the credential form.
                      final hideCredentialForm =
                          oidcProviders.isNotEmpty &&
                              credentialProviders.isEmpty;

                      return Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          if (!hideCredentialForm)
                            _CredentialForm(
                              usernameController: _usernameController,
                              passwordController: _passwordController,
                              providers: credentialProviders,
                              selectedProvider: _selectedProvider,
                              onProviderChanged: formsDisabled
                                  ? null
                                  : (v) {
                                      if (v == null) return;
                                      setState(() => _selectedProvider = v);
                                    },
                              isSubmitting: isAuthenticating,
                              isDisabled: formsDisabled,
                              onSubmit: formsDisabled ? null : _submit,
                            ),
                          if (oidcProviders.isNotEmpty && !hideCredentialForm)
                            _orDivider(colors),
                          if (oidcProviders.isNotEmpty) ...[
                            if (oidcError != null)
                              _errorBanner(colors, oidcError.message,
                                  keyValue: 'oidc-error'),
                            for (final p in oidcProviders) ...[
                              OIDCProviderButton(
                                provider: p,
                                onTap: () => _startOidc(p.id),
                                isLoading:
                                    p.id == oidcLaunchingProviderID,
                                isDisabled: oidcInFlight ||
                                    isAuthenticating,
                              ),
                              const SizedBox(height: 8),
                            ],
                          ],
                        ],
                      );
                    },
                    loading: () => _CredentialForm(
                      usernameController: _usernameController,
                      passwordController: _passwordController,
                      providers: const [],
                      selectedProvider: _selectedProvider,
                      onProviderChanged: null,
                      isSubmitting: isAuthenticating,
                      isDisabled: formsDisabled,
                      onSubmit: formsDisabled ? null : _submit,
                    ),
                    error: (_, _) => _CredentialForm(
                      usernameController: _usernameController,
                      passwordController: _passwordController,
                      providers: const [],
                      selectedProvider: _selectedProvider,
                      onProviderChanged: null,
                      isSubmitting: isAuthenticating,
                      isDisabled: formsDisabled,
                      onSubmit: formsDisabled ? null : _submit,
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _errorBanner(KubeColors colors, String message,
      {required String keyValue}) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Container(
        padding: const EdgeInsets.all(12),
        decoration: BoxDecoration(
          color: colors.errorDim,
          borderRadius: BorderRadius.circular(8),
        ),
        child: Text(
          message,
          key: ValueKey(keyValue),
          style: TextStyle(color: colors.error),
        ),
      ),
    );
  }

  Widget _orDivider(KubeColors colors) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 16),
      child: Row(
        children: [
          Expanded(child: Divider(color: colors.borderSubtle)),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12),
            child: Text('OR',
                style: TextStyle(color: colors.textMuted, fontSize: 12)),
          ),
          Expanded(child: Divider(color: colors.borderSubtle)),
        ],
      ),
    );
  }
}

/// Username + password + (optional) provider dropdown + submit. Pulled
/// into its own widget so the conditional rendering above stays readable
/// when the providers list is unavailable.
class _CredentialForm extends StatelessWidget {
  const _CredentialForm({
    required this.usernameController,
    required this.passwordController,
    required this.providers,
    required this.selectedProvider,
    required this.onProviderChanged,
    required this.isSubmitting,
    required this.isDisabled,
    required this.onSubmit,
  });

  final TextEditingController usernameController;
  final TextEditingController passwordController;
  final List<AuthProvider> providers;
  final String selectedProvider;
  final ValueChanged<String?>? onProviderChanged;
  final bool isSubmitting;
  final bool isDisabled;
  final VoidCallback? onSubmit;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TextFormField(
          key: const ValueKey('login-username'),
          controller: usernameController,
          autofillHints: const [AutofillHints.username],
          enabled: !isDisabled,
          decoration: const InputDecoration(
            labelText: 'Username',
            border: OutlineInputBorder(),
          ),
          validator: (v) =>
              (v == null || v.trim().isEmpty) ? 'Required' : null,
        ),
        const SizedBox(height: 12),
        TextFormField(
          key: const ValueKey('login-password'),
          controller: passwordController,
          obscureText: true,
          autofillHints: const [AutofillHints.password],
          enabled: !isDisabled,
          decoration: const InputDecoration(
            labelText: 'Password',
            border: OutlineInputBorder(),
          ),
          validator: (v) => (v == null || v.isEmpty) ? 'Required' : null,
        ),
        // Show the dropdown only when more than one credential provider
        // is configured. Most homelabs only have local; hiding keeps
        // the form clean.
        if (providers.length > 1)
          Padding(
            padding: const EdgeInsets.only(top: 12),
            child: DropdownButtonFormField<String>(
              key: const ValueKey('login-provider'),
              initialValue: selectedProvider,
              decoration: const InputDecoration(
                labelText: 'Provider',
                border: OutlineInputBorder(),
              ),
              items: providers
                  .map((p) => DropdownMenuItem(
                        value: p.id,
                        child: Text(p.name),
                      ))
                  .toList(),
              onChanged: onProviderChanged,
            ),
          ),
        const SizedBox(height: 20),
        FilledButton(
          key: const ValueKey('login-submit'),
          onPressed: onSubmit,
          child: isSubmitting
              ? const SizedBox(
                  height: 20,
                  width: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Sign in'),
        ),
      ],
    );
  }
}
