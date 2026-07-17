import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/status_pill.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// One composite golden of the [StatusPill] palette variants used across the
/// arr modules. Flat translucent fills plus Ahem text keep the pixel diff far
/// below the tolerant comparator threshold (see flutter_test_config.dart).
/// Regenerate with `flutter test --update-goldens` from `app/`.
void main() {
  const variants = [
    ('Available', AppTheme.available),
    ('Requested', AppTheme.requested),
    ('Downloading', AppTheme.downloading),
    ('Unavailable', AppTheme.unavailable),
    ('Warning', AppTheme.warning),
    ('Error', AppTheme.error),
    ('Accent', AppTheme.accent),
  ];

  testWidgets('StatusPill variants match golden', (tester) async {
    tester.view.physicalSize = const Size(240, 320);
    tester.view.devicePixelRatio = 1;
    addTearDown(() {
      tester.view.resetPhysicalSize();
      tester.view.resetDevicePixelRatio();
    });

    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark,
        home: Scaffold(
          body: RepaintBoundary(
            key: const ValueKey('golden'),
            child: ColoredBox(
              color: AppTheme.background,
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    for (final (text, color) in variants) ...[
                      StatusPill(text: text, color: color),
                      const SizedBox(height: 8),
                    ],
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );

    await expectLater(
      find.byKey(const ValueKey('golden')),
      matchesGoldenFile('goldens/status_pill_variants.png'),
    );
  });
}
