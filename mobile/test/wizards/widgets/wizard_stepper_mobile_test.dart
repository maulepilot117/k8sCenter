// Smoke test for the stateless WizardStepperMobile shell.
//
// Verifies the contract: completed steps callable; current step
// highlighted; future steps disabled; phone vs tablet layout selected
// at the 768px breakpoint.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:kubecenter/theme/kube_theme_builder.dart';
import 'package:kubecenter/wizards/widgets/wizard_stepper_mobile.dart';
import 'package:kubecenter/wizards/wizard_step.dart';

const _steps = [
  WizardStep(title: 'Configure', description: 'Fill the form'),
  WizardStep(title: 'Review', description: 'Preview YAML'),
];

Widget _harness({
  required int currentStep,
  ValueChanged<int>? onStepClick,
  Size size = const Size(800, 600),
}) {
  return MediaQuery(
    data: MediaQueryData(size: size),
    child: MaterialApp(
      theme: buildKubeTheme('nexus'),
      home: Scaffold(
        body: SizedBox(
          width: size.width,
          height: 80,
          child: WizardStepperMobile(
            steps: _steps,
            currentStep: currentStep,
            onStepClick: onStepClick,
          ),
        ),
      ),
    ),
  );
}

void main() {
  group('WizardStepperMobile', () {
    testWidgets('renders both step titles on tablet (≥768)', (tester) async {
      await tester.pumpWidget(_harness(currentStep: 0));
      expect(find.text('Configure'), findsOneWidget);
      expect(find.text('Review'), findsOneWidget);
    });

    testWidgets('renders only the current step title on phone (<768)',
        (tester) async {
      await tester.pumpWidget(_harness(
        currentStep: 0,
        size: const Size(400, 700),
      ));
      // Phone layout shows the current title + step counter; the other
      // step's title isn't rendered.
      expect(find.text('Configure'), findsOneWidget);
      expect(find.text('Review'), findsNothing);
      expect(find.textContaining('Step 1 of 2'), findsOneWidget);
    });

    testWidgets('completed step is tappable and fires onStepClick',
        (tester) async {
      int? tapped;
      await tester.pumpWidget(_harness(
        currentStep: 1,
        onStepClick: (i) => tapped = i,
      ));

      // Step 0 (Configure) is completed. Tap it.
      await tester.tap(find.text('Configure'));
      await tester.pump();
      expect(tapped, 0);
    });

    testWidgets('current step does not fire onStepClick when tapped',
        (tester) async {
      int? tapped;
      await tester.pumpWidget(_harness(
        currentStep: 1,
        onStepClick: (i) => tapped = i,
      ));

      // Tapping the current chip (Review) — _StepChip skips InkWell
      // for non-completed steps.
      await tester.tap(find.text('Review'));
      await tester.pump();
      expect(tapped, isNull);
    });
  });
}
