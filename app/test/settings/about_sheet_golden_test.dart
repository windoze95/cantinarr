import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_sheet.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/ui/about_sheet.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:package_info_plus/package_info_plus.dart';

/// The About sheet used to render as two stacked cards — the themed sheet's
/// bordered outline with its drag handle, then the sheet's own card with a
/// second handle — shrink-wrapped to its widest line and tall enough that the
/// version was clipped off the bottom. The golden pins the corrected chrome
/// (one card, one handle, full width); the tests below pin the content that
/// used to fall off. Regenerate with `flutter test --update-goldens`.
void main() {
  setUp(() {
    PackageInfo.setMockInitialValues(
      appName: 'Cantinarr',
      packageName: 'codes.julian.cantinarr',
      version: '0.1.0',
      buildNumber: '238',
      buildSignature: '',
    );
  });

  testWidgets('about sheet on a phone', (tester) async {
    await _openAboutSheet(tester, const Size(402, 874));

    await expectLater(
      find.byType(MaterialApp),
      matchesGoldenFile('goldens/about_sheet_phone.png'),
    );
  });

  testWidgets('version, build and server lines survive a short screen',
      (tester) async {
    // Shorter than any phone the app targets: the content cannot fit, so it
    // has to scroll rather than overflow.
    await _openAboutSheet(tester, const Size(402, 560));

    expect(tester.takeException(), isNull);
    await tester.scrollUntilVisible(find.text('Server 0.2.0'), 100,
        scrollable: find.byType(Scrollable).last);
    expect(find.text('Version 0.1.0 (238)'), findsOneWidget);
    expect(find.text('Server 0.2.0'), findsOneWidget);
  });

  testWidgets('the sheet fills the screen width', (tester) async {
    await _openAboutSheet(tester, const Size(402, 874));

    expect(tester.getSize(find.byType(AboutSheet)).width, 402);
  });
}

Future<void> _openAboutSheet(WidgetTester tester, Size size) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1.0;
  addTearDown(tester.view.reset);

  await tester.pumpWidget(
    ProviderScope(
      overrides: [authProvider.overrideWith(() => _FakeAuth())],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: Scaffold(
          backgroundColor: AppTheme.background,
          body: Builder(
            builder: (context) => Center(
              child: TextButton(
                onPressed: () => showAppSheet(
                  context,
                  builder: (_) => const AboutSheet(),
                ),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      ),
    ),
  );

  await tester.tap(find.text('open'));
  await tester.pumpAndSettle();
  // Decode the asset for real so the image contributes its true height —
  // the clipping this guards against came from how tall it is.
  await tester.runAsync(() async {
    await precacheImage(
      const AssetImage('assets/greedo.png'),
      tester.element(find.byType(AboutSheet)),
    );
  });
  await tester.pumpAndSettle();
}

class _FakeAuth extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost:8585',
          accessToken: 't',
          refreshToken: 't',
          serverVersion: '0.2.0',
        ),
        user: UserProfile(id: 1, username: 'admin', role: 'admin'),
      );
}
