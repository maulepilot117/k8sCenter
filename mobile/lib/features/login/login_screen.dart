// Login screen — username + password, optional provider dropdown when
// the backend reports more than the default local provider, error chrome
// for ApiError messages from /v1/auth/login.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../auth/auth_repository.dart';
import '../../auth/auth_state.dart';
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

  @override
  Widget build(BuildContext context) {
    final colors = Theme.of(context).extension<KubeColors>()!;
    final authState = ref.watch(authRepositoryProvider);
    final providersAsync = ref.watch(authProvidersProvider);
    final isAuthenticating = authState is AuthAuthenticating;

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
                    Padding(
                      padding: const EdgeInsets.only(bottom: 12),
                      child: Container(
                        padding: const EdgeInsets.all(12),
                        decoration: BoxDecoration(
                          color: colors.errorDim,
                          borderRadius: BorderRadius.circular(8),
                        ),
                        child: Text(
                          authState.errorMessage!,
                          key: const ValueKey('login-error'),
                          style: TextStyle(color: colors.error),
                        ),
                      ),
                    ),
                  TextFormField(
                    key: const ValueKey('login-username'),
                    controller: _usernameController,
                    autofillHints: const [AutofillHints.username],
                    enabled: !isAuthenticating,
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
                    controller: _passwordController,
                    obscureText: true,
                    autofillHints: const [AutofillHints.password],
                    enabled: !isAuthenticating,
                    decoration: const InputDecoration(
                      labelText: 'Password',
                      border: OutlineInputBorder(),
                    ),
                    validator: (v) =>
                        (v == null || v.isEmpty) ? 'Required' : null,
                  ),
                  providersAsync.when(
                    data: (providers) {
                      // Only show the dropdown when the backend reports
                      // more than the implicit local provider. Most homelab
                      // deployments only have local; hiding keeps the form
                      // clean.
                      if (providers.length <= 1) {
                        return const SizedBox.shrink();
                      }
                      return Padding(
                        padding: const EdgeInsets.only(top: 12),
                        child: DropdownButtonFormField<String>(
                          key: const ValueKey('login-provider'),
                          initialValue: _selectedProvider,
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
                          onChanged: isAuthenticating
                              ? null
                              : (v) {
                                  if (v == null) return;
                                  setState(() => _selectedProvider = v);
                                },
                        ),
                      );
                    },
                    loading: SizedBox.shrink,
                    error: (_, _) => const SizedBox.shrink(),
                  ),
                  const SizedBox(height: 20),
                  FilledButton(
                    key: const ValueKey('login-submit'),
                    onPressed: isAuthenticating ? null : _submit,
                    child: isAuthenticating
                        ? const SizedBox(
                            height: 20,
                            width: 20,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          )
                        : const Text('Sign in'),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
