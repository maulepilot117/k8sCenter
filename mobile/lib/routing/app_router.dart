// Minimal go_router config for PR-1a. Single `/` route renders the
// dashboard placeholder. PR-1b adds `/login`; PR-1c+ adds resource routes.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../features/dashboard/dashboard_placeholder.dart';
import '../features/settings/theme_picker_sheet.dart';
import '../widgets/adaptive_scaffold.dart';

final appRouterProvider = Provider<GoRouter>((ref) {
  return GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const _RootScreen(),
      ),
    ],
  );
});

class _RootScreen extends StatelessWidget {
  const _RootScreen();

  @override
  Widget build(BuildContext context) {
    return AdaptiveScaffold(
      title: 'k8sCenter',
      body: const DashboardPlaceholder(),
      actions: [
        IconButton(
          icon: const Icon(Icons.palette_outlined),
          tooltip: 'Theme',
          onPressed: () => ThemePickerSheet.show(context),
        ),
      ],
    );
  }
}
