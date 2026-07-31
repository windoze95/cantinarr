import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// [AppSheet] is the one body every bottom sheet wraps its content in, so the
/// three ways a sheet used to mistreat its content are pinned here rather than
/// per sheet: shrink-wrapping to the widest line, clipping whatever didn't fit,
/// and running the last row under the home indicator or the keyboard.
void main() {
  testWidgets('fills the sheet width instead of hugging its content',
      (tester) async {
    await _pump(tester, const SizedBox(width: 40, height: 40));

    expect(tester.getSize(find.byType(AppSheet)).width, 400);
  });

  testWidgets('scrolls content taller than the cap instead of overflowing',
      (tester) async {
    await _pump(tester, const SizedBox(height: 2000, child: Text('bottom')));

    expect(tester.takeException(), isNull);
    // 0.85 of the 800px-tall screen, and no taller.
    expect(tester.getSize(find.byType(AppSheet)).height, 680);
  });

  testWidgets('pads clear of the home indicator and the keyboard',
      (tester) async {
    await _pump(
      tester,
      const SizedBox(height: 40),
      padding: const EdgeInsets.only(bottom: 34),
      viewInsets: const EdgeInsets.only(bottom: 300),
    );

    final scrollView = tester.widget<SingleChildScrollView>(
      find.descendant(
        of: find.byType(AppSheet),
        matching: find.byType(SingleChildScrollView),
      ),
    );
    // The sheet's own 20, plus both system insets.
    expect((scrollView.padding as EdgeInsets).bottom, 20 + 34 + 300);
  });
}

Future<void> _pump(
  WidgetTester tester,
  Widget child, {
  EdgeInsets padding = EdgeInsets.zero,
  EdgeInsets viewInsets = EdgeInsets.zero,
}) async {
  tester.view.physicalSize = const Size(400, 800);
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  await tester.pumpWidget(
    MaterialApp(
      theme: AppTheme.dark,
      home: Builder(
        builder: (context) => MediaQuery(
          data: MediaQuery.of(context)
              .copyWith(padding: padding, viewInsets: viewInsets),
          child: Align(
            alignment: Alignment.bottomCenter,
            child: AppSheet(
              padding: const EdgeInsets.all(20),
              child: child,
            ),
          ),
        ),
      ),
    ),
  );
  await tester.pumpAndSettle();
}
