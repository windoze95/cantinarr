import 'dart:async';

import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/phone_apps_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The one-time post-request prompt: it shows once per device on builds that
/// advertise the phone apps, and never at all inside the store binaries.
void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  Future<BuildContext> pumpHost(WidgetTester tester) async {
    late BuildContext context;
    await tester.pumpWidget(
      MaterialApp(
        theme: AppTheme.dark,
        home: Builder(builder: (c) {
          context = c;
          return const SizedBox.shrink();
        }),
      ),
    );
    return context;
  }

  testWidgets('prompts once, then never again', (tester) async {
    final context = await pumpHost(tester);

    unawaited(maybeShowPhoneAppsPrompt(context));
    await tester.pumpAndSettle();
    expect(find.text('Get the Cantinarr app'), findsOneWidget);
    expect(find.text('iPhone'), findsOneWidget);
    expect(find.text('Android'), findsOneWidget);
    expect(find.text('Download on Google Play'), findsOneWidget);
    expect(find.text('Join the beta on TestFlight'), findsOneWidget);
    expect(find.text('Join the beta at cantinarr.com'), findsNothing);

    // Dismissing without tapping a link still counts as shown.
    await tester.tapAt(const Offset(10, 10));
    await tester.pumpAndSettle();
    expect(find.text('Get the Cantinarr app'), findsNothing);

    unawaited(maybeShowPhoneAppsPrompt(context));
    await tester.pumpAndSettle();
    expect(find.text('Get the Cantinarr app'), findsNothing);
  }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

  testWidgets('first-request Android action opens production and stays dismissed',
      (tester) async {
    final launches = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      const MethodChannel('plugins.flutter.io/url_launcher'),
      (call) async {
        if (call.method == 'launch') {
          launches.add((call.arguments as Map)['url'] as String);
          return true;
        }
        return false;
      },
    );
    addTearDown(() => tester.binding.defaultBinaryMessenger
        .setMockMethodCallHandler(
            const MethodChannel('plugins.flutter.io/url_launcher'), null));
    final context = await pumpHost(tester);
    unawaited(maybeShowPhoneAppsPrompt(context));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Android'));
    await tester.pumpAndSettle();
    expect(launches, [
      'https://play.google.com/store/apps/details?id=codes.julian.cantinarr',
    ]);
    expect(find.text('Get the Cantinarr app'), findsNothing);
    unawaited(maybeShowPhoneAppsPrompt(context));
    await tester.pumpAndSettle();
    expect(find.text('Get the Cantinarr app'), findsNothing);
  }, variant: TargetPlatformVariant.only(TargetPlatform.macOS));

  testWidgets('never prompts inside the phone apps themselves',
      (tester) async {
    final context = await pumpHost(tester);

    unawaited(maybeShowPhoneAppsPrompt(context));
    await tester.pumpAndSettle();
    expect(find.text('Get the Cantinarr app'), findsNothing);
  }, variant: TargetPlatformVariant.only(TargetPlatform.iOS));
}
