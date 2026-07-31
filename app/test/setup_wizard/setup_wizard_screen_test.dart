import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/status_pill.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/setup_wizard/ui/setup_wizard_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The counted headers use non-breaking spaces so a wrapped header never
/// leaves a bare "1" on its own line. Spelling them out here means a stray
/// plain space in the widget fails the test rather than passing invisibly.
const _nb = '\u00A0';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('each section header counts what is still unconfigured',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, false),
      ('sonarr', false, false),
      ('tmdb', true, false),
      ('trakt', false, true),
      ('books', false, true),
    ]);

    expect(find.text('ESSENTIALS$_nb· 1${_nb}LEFT'), findsOneWidget);
    expect(find.text('NICE TO HAVE$_nb· 2${_nb}LEFT'), findsOneWidget);
  });

  testWidgets('a finished section says so instead of looking like an empty one',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, false),
      ('sonarr', true, false),
      ('tmdb', true, false),
      ('trakt', false, true),
    ]);

    expect(find.text('ESSENTIALS$_nb· DONE'), findsOneWidget);
    expect(find.text('NICE TO HAVE$_nb· 1${_nb}LEFT'), findsOneWidget);

    // The reward has to be visible, not just worded: a done section reads in
    // the same green as the row checkmarks.
    final done = tester.widget<Text>(find.text('ESSENTIALS$_nb· DONE'));
    final suffix = (done.textSpan! as TextSpan).children!.last as TextSpan;
    expect(suffix.style?.color, AppTheme.available);
  });

  group('row emphasis', _rowEmphasisTests);
}

/// The pill on a row, by the row's title.
StatusPill _pillOn(WidgetTester tester, String title) => tester.widget<StatusPill>(
      find.descendant(
        of: find.ancestor(
            of: find.text(title), matching: find.byType(ListTile)),
        matching: find.byType(StatusPill),
      ),
    );

Finder _rowTitle(String title) => find.descendant(
      of: find.ancestor(of: find.text(title), matching: find.byType(ListTile)),
      matching: find.text(title),
    );

void _rowEmphasisTests() {
  testWidgets('an outstanding row offers the action, a done row recedes',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, false),
      ('sonarr', false, false),
      ('tmdb', true, false),
    ]);

    // Only the unfinished row carries a chip, so the count of things left to
    // do is readable down the edge of the list.
    expect(find.widgetWithText(StatusPill, 'Set up'), findsOneWidget);
    expect(_pillOn(tester, 'sonarr').text, 'Set up');

    // The finished row keeps its checkmark and gives up the emphasis.
    expect(
      find.descendant(
        of: find.ancestor(
            of: find.text('radarr'), matching: find.byType(ListTile)),
        matching: find.byIcon(Icons.check_circle),
      ),
      findsOneWidget,
    );
    expect(tester.widget<Text>(_rowTitle('radarr')).style?.color,
        AppTheme.textSecondary);
    expect(tester.widget<Text>(_rowTitle('sonarr')).style?.color,
        AppTheme.textPrimary);

    // The chevron is gone entirely: it said "this goes somewhere", which every
    // navigable row in the app says, and never said "you still owe this".
    expect(find.byIcon(Icons.chevron_right), findsNothing);
  });

  testWidgets('only a row the server cannot work without raises its voice',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, false),
      ('sonarr', false, false),
      ('tmdb', true, false),
      ('trakt', false, true),
    ]);

    // A movies-only server: Sonarr is unfinished but nothing is broken, so it
    // reads the same as any other outstanding row.
    expect(_pillOn(tester, 'sonarr').color, AppTheme.accent);
    expect(_pillOn(tester, 'trakt').color, AppTheme.accent);
  });

  testWidgets('a server with no library at all shows the alarm', (tester) async {
    await _pumpWizard(tester, [
      ('radarr', false, false),
      ('sonarr', false, false),
      ('tmdb', false, false),
      ('trakt', false, true),
    ]);

    for (final key in ['radarr', 'sonarr', 'tmdb']) {
      expect(_pillOn(tester, key).color, AppTheme.danger,
          reason: '$key is what stands between this server and working');
    }
    // Nice-to-haves never join in, however empty the server is.
    expect(_pillOn(tester, 'trakt').color, AppTheme.accent);
  });

  testWidgets('a row with nowhere to go offers no action', (tester) async {
    await _pumpWizard(tester, [
      ('tmdb', true, false),
      ('radarr', true, false),
      ('push', false, true),
    ]);

    // push is a server env var: it is unfinished, and there is no screen to
    // send the admin to, so promising a tap would be a lie.
    expect(find.widgetWithText(StatusPill, 'Set up'), findsNothing);
    expect(tester.widget<Text>(_rowTitle('push')).style?.color,
        AppTheme.textPrimary);
  });
}

Future<void> _pumpWizard(
  WidgetTester tester,
  List<(String, bool, bool)> items,
) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = _WizardAdapter(items);

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(_AdminAuthNotifier.new),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: const SetupWizardScreen(),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _AdminAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState(
        connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(
          id: 1,
          username: 'admin',
          role: 'admin',
          permissions: [],
        ),
      );

  @override
  Future<void> refreshUser() async {}
}

class _WizardAdapter implements HttpClientAdapter {
  _WizardAdapter(this.items);

  /// (key, configured, optional) per checklist row.
  final List<(String, bool, bool)> items;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    return ResponseBody.fromString(
      jsonEncode({
        'items': [
          for (final (key, configured, optional) in items)
            {
              'key': key,
              'title': key,
              'description': 'about $key',
              'configured': configured,
              'optional': optional,
            },
        ],
        'configured': items.where((i) => i.$2).length,
        'total': items.length,
      }),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
