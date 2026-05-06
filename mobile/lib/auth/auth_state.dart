// Auth state machine. A sealed class with four cases covers everything
// the UI needs to render: a splash while bootstrapping, the login screen,
// authenticated screens with user + RBAC info, and an error string for
// surfacing login failures inline.
//
// Sealed classes are 1-to-1 with the four UI states the design contract
// promises — adding a new case is a deliberate compile error at every
// switch site, which is what we want.

import 'user.dart';

sealed class AuthState {
  const AuthState();
}

/// Initial state before bootstrap reads the refresh token. Renders a
/// splash; never lasts more than a few hundred ms.
class AuthInitializing extends AuthState {
  const AuthInitializing();
}

/// No valid session. Login screen renders. [errorMessage] is non-null
/// when a previous login attempt failed and the screen should show it.
class AuthUnauthenticated extends AuthState {
  const AuthUnauthenticated({this.errorMessage});

  final String? errorMessage;
}

/// Login or refresh in flight. Login button shows a spinner; the screen
/// rejects further submissions until it resolves.
class AuthAuthenticating extends AuthState {
  const AuthAuthenticating();
}

/// Authenticated. [user] and [rbac] populate widgets that gate on roles
/// or per-namespace permissions.
class AuthAuthenticated extends AuthState {
  const AuthAuthenticated({required this.user, required this.rbac});

  final UserInfo user;
  final RBACSummary rbac;
}
