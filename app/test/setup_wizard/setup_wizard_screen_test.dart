import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/status_pill.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/settings_anchors.dart';
import 'package:cantinarr/features/setup_wizard/ui/setup_wizard_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// The counted headers use non-breaking spaces so a wrapped header never
/// leaves a bare "1" on its own line. Spelling them out here means a stray
/// plain space in the widget fails the test rather than passing invisibly.
const _nb = '\u00A0';

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets('one feature list counts what remains without setup categories',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, true),
      ('sonarr', false, true),
      ('tmdb', true, true),
      ('trakt', false, true),
      ('books', false, true),
    ]);

    expect(find.text('FEATURES$_nb· 3${_nb}LEFT'), findsOneWidget);
    expect(find.textContaining('ESSENTIALS'), findsNothing);
    expect(find.textContaining('NICE TO HAVE'), findsNothing);
    expect(find.textContaining('optional'), findsNothing);
  });

  testWidgets('a finished checklist says nothing remains to set up',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, true),
      ('sonarr', true, true),
      ('tmdb', true, true),
      ('trakt', true, true),
    ]);

    expect(find.text('FEATURES$_nb· DONE'), findsOneWidget);
    expect(find.text('Nothing left to set up'), findsOneWidget);
    expect(
        tester
            .widget<LinearProgressIndicator>(
                find.byType(LinearProgressIndicator))
            .value,
        1);

    // The reward has to be visible, not just worded: a done section reads in
    // the same green as the row checkmarks.
    final done = tester.widget<Text>(find.text('FEATURES$_nb· DONE'));
    final suffix = (done.textSpan! as TextSpan).children!.last as TextSpan;
    expect(suffix.style?.color, AppTheme.available);
  });

  // _pumpWizard mounts the wizard in a plain MaterialApp with no router, so
  // this variant records each row's destination and setup context.
  testWidgets('rows open their settings destination with the right context',
      (tester) async {
    // Tall enough that every row is on screen without scrolling.
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    // What each row must send: the service type when the row names one.
    // Rows naming a category (download clients, media servers, and the
    // monitoring row, whose key predates Tracearr) send a selection prompt
    // instead of guessing a member the admin would then have to correct —
    // the same correction the Radarr default used to force.
    const expectedExtras = <String, Map<String, dynamic>>{
      'radarr': {'service_type': 'radarr'},
      'sonarr': {'service_type': 'sonarr'},
      'tautulli': {'service_type_prompt': 'Select a monitoring service'},
      'books': {'service_type': 'chaptarr'},
      'music': {'service_type': 'lidarr'},
      'media_servers': {'service_type_prompt': 'Select a media server'},
      'download_client': {'service_type_prompt': 'Select a download client'},
    };

    Object? capturedExtra;
    Uri? capturedSettingsUri;
    final router = GoRouter(
      initialLocation: '/',
      routes: [
        GoRoute(path: '/', builder: (_, __) => const SetupWizardScreen()),
        GoRoute(
          path: '/settings/instance/new',
          builder: (_, state) {
            capturedExtra = state.extra;
            return const Scaffold(body: SizedBox());
          },
        ),
        GoRoute(
          path: '/settings',
          builder: (_, state) {
            capturedSettingsUri = state.uri;
            return Scaffold(
              body: Text(state.uri.queryParameters['highlight'] ?? ''),
            );
          },
        ),
      ],
    );

    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = _WizardAdapter([
        for (final key in expectedExtras.keys) (key, false, true),
        ('push', false, true),
      ]);
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(_AdminAuthNotifier.new),
          backendClientProvider.overrideWithValue(dio),
        ],
        child: MaterialApp.router(theme: AppTheme.dark, routerConfig: router),
      ),
    );
    await tester.pumpAndSettle();

    for (final entry in expectedExtras.entries) {
      capturedExtra = null;
      final row = find.text(entry.key);
      await tester.ensureVisible(row);
      await tester.tap(row);
      await tester.pumpAndSettle();

      expect(capturedExtra, entry.value,
          reason: 'the ${entry.key} row must announce itself to the form');

      // Pop back so the next tap starts from the checklist again; this also
      // exercises the return path's status refresh.
      router.pop();
      await tester.pumpAndSettle();
    }

    await tester.ensureVisible(find.text('push'));
    await tester.tap(find.text('push'));
    await tester.pumpAndSettle();
    expect(capturedSettingsUri.toString(),
        '/settings?highlight=${SettingsAnchors.rootNotifications}');
    expect(find.text(SettingsAnchors.rootNotifications), findsOneWidget);
  });

  for (final key in ['radarr', 'sonarr', 'tmdb', 'push', 'future_feature']) {
    testWidgets('$key can be skipped and restored, even without a destination',
        (tester) async {
      final items = [(key, false, true)];
      final adapter = _WizardAdapter(items);
      await _pumpWizard(tester, items, adapter: adapter);
      await tester.tap(find.widgetWithText(StatusPill, 'Skip'));
      await tester.pumpAndSettle();
      expect(adapter.skipPuts.single, {'key': key, 'skipped': true});
      expect(find.text('Nothing left to set up'), findsOneWidget);
      expect(
          tester
              .widget<LinearProgressIndicator>(
                  find.byType(LinearProgressIndicator))
              .value,
          1);
      expect(find.widgetWithText(StatusPill, 'Set up'), findsNothing);
      await tester.tap(find.widgetWithText(StatusPill, 'Skipped'));
      await tester.pumpAndSettle();
      expect(adapter.skipPuts.last, {'key': key, 'skipped': false});
      expect(find.text('0 of 1 features configured'), findsOneWidget);
      expect(find.widgetWithText(StatusPill, 'Set up'),
          key == 'future_feature' ? findsNothing : findsOneWidget);
    });
  }

  testWidgets(
      'an older server keeps setup available and explains a disabled Skip',
      (tester) async {
    final items = [('radarr', false, false)];
    final adapter = _WizardAdapter(items);
    await _pumpWizard(tester, items, adapter: adapter);
    expect(find.text('Update the server to skip this item.'), findsOneWidget);
    expect(_pillOn(tester, 'radarr').color, AppTheme.accent);
    await tester.tap(find.widgetWithText(StatusPill, 'Skip'));
    await tester.pumpAndSettle();
    expect(adapter.skipPuts, isEmpty);
    expect(find.text('0 of 1 features configured'), findsOneWidget);
  });

  for (final restore in [false, true]) {
    testWidgets(
        'failed ${restore ? 'restore' : 'skip'} keeps the row and can be retried',
        (tester) async {
      final items = [('radarr', false, true)];
      final adapter = _WizardAdapter(items, skipped: restore ? {'radarr'} : {})
        ..failSkips = true;
      await _pumpWizard(tester, items, adapter: adapter);
      final action =
          find.widgetWithText(StatusPill, restore ? 'Skipped' : 'Skip');
      await tester.tap(action);
      await tester.pumpAndSettle();
      expect(
          find.text(
              'Could not ${restore ? 'restore' : 'skip'} "radarr". Try again.'),
          findsOneWidget);
      expect(action, findsOneWidget);
      expect(adapter.skipped.contains('radarr'), restore);
      adapter.failSkips = false;
      await tester.tap(action);
      await tester.pumpAndSettle();
      expect(adapter.skipped.contains('radarr'), !restore);
    });
  }

  for (final width in [320.0, 800.0]) {
    testWidgets('checklist actions fit at width $width with large text',
        (tester) async {
      tester.view.physicalSize = Size(width, 1000);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await _pumpWizard(tester, [('media_servers', false, true)], textScale: 2);
      expect(tester.takeException(), isNull);
      await tester.ensureVisible(find.widgetWithText(StatusPill, 'Skip'));
      await tester.pumpAndSettle();
      await tester.tap(find.widgetWithText(StatusPill, 'Skip'));
      await tester.pumpAndSettle();
      await tester.scrollUntilVisible(
          find.text('Nothing left to set up'), -250);
      expect(find.text('Nothing left to set up'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('requesters cannot use checklist controls', (tester) async {
    await _pumpWizard(tester, [('radarr', false, true)], isAdmin: false);
    expect(
        find.text('The setup checklist is for server admins.'), findsOneWidget);
    expect(find.widgetWithText(StatusPill, 'Skip'), findsNothing);
    expect(find.widgetWithText(StatusPill, 'Set up'), findsNothing);
  });

  group('row emphasis', _rowEmphasisTests);
}

/// The action ("Set up") pill on a row, by the row's title. Targeted by its
/// text because an unconfigured row also carries a Skip pill.
StatusPill _pillOn(WidgetTester tester, String title) =>
    tester.widget<StatusPill>(
      find.descendant(
        of: find.ancestor(
            of: find.text(title), matching: find.byType(ListTile)),
        matching: find.widgetWithText(StatusPill, 'Set up'),
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
      ('radarr', true, true),
      ('sonarr', false, true),
      ('tmdb', true, true),
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

  testWidgets('unconfigured features use the same accent', (tester) async {
    await _pumpWizard(tester, [
      ('radarr', true, true),
      ('sonarr', false, true),
      ('tmdb', true, true),
      ('trakt', false, true),
    ]);

    // A movies-only server: Sonarr is unfinished but nothing is broken, so it
    // reads the same as any other outstanding row.
    expect(_pillOn(tester, 'sonarr').color, AppTheme.accent);
    expect(_pillOn(tester, 'trakt').color, AppTheme.accent);
  });

  testWidgets('unconfigured libraries and metadata use no warning colors',
      (tester) async {
    await _pumpWizard(tester, [
      ('radarr', false, true),
      ('sonarr', false, true),
      ('tmdb', false, true),
      ('trakt', false, true),
    ]);

    for (final key in ['radarr', 'sonarr', 'tmdb']) {
      expect(_pillOn(tester, key).color, AppTheme.accent);
    }
    // Discovery choices use the same treatment.
    expect(_pillOn(tester, 'trakt').color, AppTheme.accent);
  });

  testWidgets('the problem-detection row deep-links like any other',
      (tester) async {
    await _pumpWizard(tester, [
      ('tmdb', true, true),
      ('remediation', false, true),
    ]);

    // 'remediation' has a real destination (Settings > AI Remediation), so it
    // carries the action chip and a tap handler — it is not another push.
    expect(_pillOn(tester, 'remediation').text, 'Set up');
    final tile = tester.widget<ListTile>(
      find.ancestor(
          of: find.text('remediation'), matching: find.byType(ListTile)),
    );
    expect(tile.onTap, isNotNull);
  });

  testWidgets('a row with nowhere to go offers no action', (tester) async {
    await _pumpWizard(tester, [
      ('tmdb', true, true),
      ('radarr', true, true),
      ('future_feature', false, true),
    ]);

    // A feature from a newer server has no known destination in this app.
    expect(find.widgetWithText(StatusPill, 'Set up'), findsNothing);
    expect(tester.widget<Text>(_rowTitle('future_feature')).style?.color,
        AppTheme.textPrimary);
  });

  testWidgets('every unconfigured row pairs Set up with Skip', (tester) async {
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    await _pumpWizard(tester, [
      ('radarr', false, true),
      ('sonarr', true, true),
      ('tmdb', true, true),
      ('books', false, true),
      ('music', false, true),
    ]);

    expect(find.widgetWithText(StatusPill, 'Skip'), findsNWidgets(3));
    expect(find.widgetWithText(StatusPill, 'Set up'), findsNWidgets(3));
  });

  testWidgets(
      'skipping a row acknowledges it: the skip is stored, the row dims to '
      'Skipped, and every count stops charging for it', (tester) async {
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final items = <(String, bool, bool)>[
      ('radarr', true, true),
      ('sonarr', true, true),
      ('tmdb', true, true),
      ('books', false, true),
      ('music', false, true),
    ];
    final adapter = _WizardAdapter(items);
    await _pumpWizard(tester, items, adapter: adapter);

    expect(find.text('3 of 5 features configured'), findsOneWidget);
    expect(find.text('FEATURES$_nb· 2${_nb}LEFT'), findsOneWidget);

    final musicRow =
        find.ancestor(of: find.text('music'), matching: find.byType(ListTile));
    await tester.tap(find.descendant(
        of: musicRow, matching: find.widgetWithText(StatusPill, 'Skip')));
    await tester.pumpAndSettle();

    expect(adapter.skipPuts.single, {'key': 'music', 'skipped': true});
    // The skipped row leaves the math entirely — denominator included — so
    // "X of Y" stays a true sentence about the features this deployment
    // actually wants, and the section stops holding it against the admin.
    expect(find.text('3 of 4 features configured'), findsOneWidget);
    expect(find.text('FEATURES$_nb· 1${_nb}LEFT'), findsOneWidget);
    expect(
        find.descendant(
            of: musicRow, matching: find.widgetWithText(StatusPill, 'Skipped')),
        findsOneWidget);
    expect(tester.widget<Text>(_rowTitle('music')).style?.color,
        AppTheme.textSecondary);
    // Setting it up later needs no un-skip first: the row still opens the
    // real settings screen.
    expect(tester.widget<ListTile>(musicRow).onTap, isNotNull);
  });

  testWidgets('the Skipped chip is the undo', (tester) async {
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    final items = <(String, bool, bool)>[
      ('radarr', true, true),
      ('sonarr', true, true),
      ('tmdb', true, true),
      ('music', false, true),
    ];
    final adapter = _WizardAdapter(items, skipped: {'music'});
    await _pumpWizard(tester, items, adapter: adapter);

    expect(find.text('FEATURES$_nb· DONE'), findsOneWidget);

    await tester.tap(find.widgetWithText(StatusPill, 'Skipped'));
    await tester.pumpAndSettle();

    expect(adapter.skipPuts.single, {'key': 'music', 'skipped': false});
    expect(find.text('FEATURES$_nb· 1${_nb}LEFT'), findsOneWidget);
    expect(find.widgetWithText(StatusPill, 'Skip'), findsOneWidget);
    expect(find.widgetWithText(StatusPill, 'Set up'), findsOneWidget);
  });
}

Future<void> _pumpWizard(
  WidgetTester tester,
  List<(String, bool, bool)> items, {
  _WizardAdapter? adapter,
  double textScale = 1,
  bool isAdmin = true,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter ?? _WizardAdapter(items);

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        authProvider.overrideWith(() => _AdminAuthNotifier(isAdmin: isAdmin)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp(
        theme: AppTheme.dark,
        builder: (context, child) => MediaQuery(
          data: MediaQuery.of(context)
              .copyWith(textScaler: TextScaler.linear(textScale)),
          child: child!,
        ),
        home: const SetupWizardScreen(),
      ),
    ),
  );
  await tester.pumpAndSettle();
}

class _AdminAuthNotifier extends AuthNotifier {
  _AdminAuthNotifier({this.isAdmin = true});
  final bool isAdmin;

  @override
  Future<AuthState> build() async => AuthState(
        connection: const BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'access',
          refreshToken: 'refresh',
        ),
        user: UserProfile(
          id: 1,
          username: 'admin',
          role: isAdmin ? 'admin' : 'requester',
          permissions: [],
        ),
      );

  @override
  Future<void> refreshUser() async {}
}

class _WizardAdapter implements HttpClientAdapter {
  _WizardAdapter(this.items, {Set<String>? skipped}) : skipped = {...?skipped};

  /// (key, configured, optional) per checklist row.
  final List<(String, bool, bool)> items;

  /// Keys currently stored as skipped; PUTs to the skip route mutate it, so a
  /// refresh after a tap reads the new truth like the real server.
  final Set<String> skipped;

  /// Every body PUT to the skip route, for asserting the wire shape.
  final skipPuts = <Map<String, dynamic>>[];
  bool failSkips = false;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'PUT' &&
        options.path == '/api/admin/setup-status/skips') {
      final raw = options.data;
      final body = raw is Map<String, dynamic>
          ? raw
          : jsonDecode(raw as String) as Map<String, dynamic>;
      skipPuts.add(body);
      if (failSkips) return ResponseBody.fromString('Could not save', 500);
      final key = body['key'] as String;
      if (body['skipped'] == true) {
        skipped.add(key);
      } else {
        skipped.remove(key);
      }
      return ResponseBody.fromString(
        jsonEncode({'key': key, 'skipped': body['skipped']}),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
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
              // An old server may still refuse skips for some keys.
              'skipped': optional && skipped.contains(key),
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
