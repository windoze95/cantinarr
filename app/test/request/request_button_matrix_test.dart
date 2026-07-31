import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:cantinarr/features/request/ui/request_button.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// One row of the status matrix: what the requester should see for a status.
class _Case {
  final RequestStatus status;
  final String label;
  final IconData icon;
  final Color color;
  final bool enabled;

  const _Case(this.status, this.label, this.icon, this.color,
      {required this.enabled});
}

/// The full requester-facing matrix. Labels are requester vocabulary
/// (Request / Requested / Downloading / Available), never arr jargon.
const _cases = [
  _Case(RequestStatus.unavailable, 'Request', Icons.add, AppTheme.accent,
      enabled: true),
  _Case(RequestStatus.pending, 'Pending', Icons.hourglass_empty,
      AppTheme.requested,
      enabled: false),
  _Case(RequestStatus.denied, 'Request', Icons.add, AppTheme.accent,
      enabled: true),
  _Case(RequestStatus.requested, 'Requested', Icons.hourglass_top,
      AppTheme.requested,
      enabled: false),
  _Case(RequestStatus.downloading, 'Downloading', Icons.downloading,
      AppTheme.downloading,
      enabled: false),
  _Case(RequestStatus.available, 'Available', Icons.check_circle_rounded,
      AppTheme.available,
      enabled: false),
  _Case(RequestStatus.partial, 'Request More', Icons.add, AppTheme.accent,
      enabled: true),
];

Future<void> _pump(
  WidgetTester tester, {
  required RequestStatus status,
  bool isRequesting = false,
  VoidCallback? onRequest,
  String? error,
}) {
  return tester.pumpWidget(
    MaterialApp(
      home: Scaffold(
        body: RequestButton(
          status: status,
          isRequesting: isRequesting,
          onRequest: onRequest,
          error: error,
        ),
      ),
    ),
  );
}

ElevatedButton _button(WidgetTester tester) =>
    tester.widget<ElevatedButton>(find.byType(ElevatedButton));

void main() {
  test('button labels speak requester vocabulary, not arr jargon', () {
    expect(
      RequestStatus.values.map((s) => s.buttonLabel).toSet(),
      {
        'Request',
        'Pending',
        'Requested',
        'Downloading',
        'Available',
        'Request More',
      },
    );
  });

  for (final c in _cases) {
    testWidgets(
        '${c.status.name} shows "${c.label}" and is '
        '${c.enabled ? 'tappable' : 'non-interactive'}', (tester) async {
      var taps = 0;
      await _pump(tester, status: c.status, onRequest: () => taps++);

      expect(find.text(c.label), findsOneWidget);
      expect(find.byIcon(c.icon), findsOneWidget);

      final button = _button(tester);
      final style = button.style!;
      if (c.enabled) {
        expect(button.onPressed, isNotNull);
        expect(style.backgroundColor!.resolve(const {}), c.color);
        await tester.tap(find.byType(ElevatedButton));
        expect(taps, 1);
      } else {
        // Disabled rendering keeps the status token as the visible hue.
        expect(button.onPressed, isNull);
        expect(
          style.foregroundColor!.resolve(const {WidgetState.disabled}),
          c.color,
        );
        expect(
          style.backgroundColor!.resolve(const {WidgetState.disabled}),
          c.color.withValues(alpha: 0.13),
        );
        await tester.tap(find.byType(ElevatedButton), warnIfMissed: false);
        expect(taps, 0);
      }
    });
  }

  testWidgets('in-flight request disables the button and shows a spinner',
      (tester) async {
    var taps = 0;
    await _pump(
      tester,
      status: RequestStatus.unavailable,
      isRequesting: true,
      onRequest: () => taps++,
    );

    expect(find.text('Requesting...'), findsOneWidget);
    expect(find.text('Request'), findsNothing);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    expect(find.byIcon(Icons.add), findsNothing);

    expect(_button(tester).onPressed, isNull);
    await tester.tap(find.byType(ElevatedButton), warnIfMissed: false);
    expect(taps, 0);
  });

  testWidgets('partial availability stays requestable while in flight',
      (tester) async {
    await _pump(tester, status: RequestStatus.partial, isRequesting: true);

    expect(find.text('Requesting...'), findsOneWidget);
    expect(find.text('Request More'), findsNothing);
    expect(_button(tester).onPressed, isNull);
  });

  testWidgets('error message renders under the button', (tester) async {
    await _pump(
      tester,
      status: RequestStatus.unavailable,
      error: 'Request failed. Please try again.',
    );

    final errorText = tester.widget<Text>(
      find.text('Request failed. Please try again.'),
    );
    expect(errorText.style?.color, AppTheme.error);
  });

  testWidgets('no error row without an error', (tester) async {
    await _pump(tester, status: RequestStatus.unavailable);

    // Just the label inside the button — no trailing error text.
    expect(find.byType(Text), findsOneWidget);
  });
}
