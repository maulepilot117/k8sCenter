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
}
