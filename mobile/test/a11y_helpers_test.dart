import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'a11y_helpers.dart';

void main() {
  testWidgets('a11yHarness wraps a child with MediaQuery + Directionality',
      (tester) async {
    await tester.pumpWidget(
      a11yHarness(
        theme: ThemeData.light(),
        child: const Text('hello'),
      ),
    );
    expect(find.text('hello'), findsOneWidget);
  });

  testWidgets(
      'expectMeetsAllGuidelines passes on a well-formed labeled button',
      (tester) async {
    await tester.pumpWidget(
      a11yHarness(
        theme: ThemeData.light(),
        child: Center(
          child: SizedBox(
            width: 200,
            height: 60,
            child: ElevatedButton(
              onPressed: () {},
              child: const Text('Sign in'),
            ),
          ),
        ),
      ),
    );
    await expectMeetsAllGuidelines(tester);
  });

  testWidgets(
      'expectMeetsAllGuidelines flags an unlabeled tap target',
      (tester) async {
    await tester.pumpWidget(
      a11yHarness(
        theme: ThemeData.light(),
        child: Center(
          child: GestureDetector(
            onTap: () {},
            child: const SizedBox(width: 80, height: 80),
          ),
        ),
      ),
    );
    var didFail = false;
    try {
      await expectMeetsAllGuidelines(tester);
    } on TestFailure {
      didFail = true;
    }
    expect(didFail, isTrue,
        reason: 'unlabeled tap target should fail labeledTapTarget guideline');
  });

  testWidgets('findSemanticsFor returns the SemanticsNode for a label',
      (tester) async {
    await tester.pumpWidget(
      a11yHarness(
        theme: ThemeData.light(),
        child: Semantics(
          label: 'foo',
          container: true,
          child: const SizedBox(width: 80, height: 80),
        ),
      ),
    );
    final node = findSemanticsFor(tester, find.bySemanticsLabel('foo'));
    expect(node.label, 'foo');
  });

  testWidgets('findSemanticsFor fails the test when the finder is empty',
      (tester) async {
    await tester.pumpWidget(
      a11yHarness(theme: ThemeData.light(), child: const SizedBox()),
    );
    var didFail = false;
    try {
      findSemanticsFor(tester, find.text('does-not-exist'));
    } on TestFailure {
      didFail = true;
    }
    expect(didFail, isTrue,
        reason: 'a missing finder should fail the assertion');
  });

  testWidgets('skip-flag actually disables the corresponding check',
      (tester) async {
    // Build a tree known to fail labeledTapTarget (an unlabeled gesture).
    // expectMeetsAllGuidelines with labeledTapTargets: false should
    // NOT throw — confirming the flag short-circuits that specific check.
    await tester.pumpWidget(
      a11yHarness(
        theme: ThemeData.light(),
        child: Center(
          child: GestureDetector(
            onTap: () {},
            child: const SizedBox(width: 80, height: 80),
          ),
        ),
      ),
    );
    // Default call fails — verified by the prior test.
    // This call must succeed since the failing guideline is skipped.
    await expectMeetsAllGuidelines(tester, labeledTapTargets: false);
  });
}
