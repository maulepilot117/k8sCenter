// Root MaterialApp. Watches the active theme id and rebuilds with the
// matching ThemeData. Routing comes from [appRouterProvider].

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'routing/app_router.dart';
import 'theme/kube_theme_builder.dart';
import 'theme/theme_controller.dart';

class KubeCenterApp extends ConsumerWidget {
  const KubeCenterApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final themeId = ref.watch(themeControllerProvider);
    final router = ref.watch(appRouterProvider);

    return MaterialApp.router(
      title: 'k8sCenter',
      theme: buildKubeTheme(themeId),
      routerConfig: router,
      debugShowCheckedModeBanner: false,
    );
  }
}
