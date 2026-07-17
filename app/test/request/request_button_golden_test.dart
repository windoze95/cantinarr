import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/ui/request_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// One composite golden of the requester-facing status matrix — all seven
/// [RequestStatus] states in a labeled column. Deliberately flat colour and
/// Ahem text so the tolerant comparator threshold (see
/// flutter_test_config.dart) stays far away. Regenerate with
/// `flutter test --update-goldens` from `app/`.
void main() {
  testWidgets('RequestButton status matrix matches golden', (tester) async {
    tester.view.physicalSize = const Size(420, 780);
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
                    for (final status in RequestStatus.values) ...[
                      Text(
                        status.name,
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 11,
                        ),
                      ),
                      const SizedBox(height: 4),
                      RequestButton(status: status, onRequest: () {}),
                      const SizedBox(height: 12),
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
      matchesGoldenFile('goldens/request_button_matrix.png'),
    );
  });
}
