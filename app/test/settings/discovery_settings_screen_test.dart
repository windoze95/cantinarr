import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/status_pill.dart';
import 'package:cantinarr/core/widgets/unsaved_changes_guard.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/settings/settings_anchors.dart';
import 'package:cantinarr/features/settings/ui/discovery_settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Serves the discovery + credentials payloads the server would send, so the
/// screen renders against the real contracts rather than hand-built models.
class _DiscoverAdapter implements HttpClientAdapter {
  _DiscoverAdapter({
    required this.traktConfigured,
    this.tmdbUsingBuiltin = false,
    this.traktUsingBuiltin = false,
    this.cover4KBadges,
  });

  /// Null serves a server that predates the 4K badges setting.
  final bool? cover4KBadges;

  /// Whether an admin CLIENT ID is stored; the built-in app makes Trakt
  /// available without one.
  final bool traktConfigured;
  final bool tmdbUsingBuiltin;
  final bool traktUsingBuiltin;

  bool get _traktAvailable => traktConfigured || traktUsingBuiltin;
  bool failSaves = false;
  Map<String, dynamic>? lastDiscoveryUpdate;
  Map<String, dynamic>? lastCredentialsUpdate;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path;
    if (options.method == 'PUT' && requestStream != null) {
      if (failSaves) return ResponseBody.fromString('{}', 503);
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      final body = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
      if (path == '/api/admin/credentials') {
        lastCredentialsUpdate = body;
        return _json({'status': 'ok'});
      }
      lastDiscoveryUpdate = body;
      return _json(_discovery);
    }
    if (path == '/api/admin/credentials') {
      return _json({
        'credentials': {
          'trakt_client_id': traktConfigured,
          'tmdb_access_token': !tmdbUsingBuiltin,
        },
        'tmdb_using_builtin': tmdbUsingBuiltin,
        'trakt_using_builtin': traktUsingBuiltin,
        'ai': const <String, dynamic>{},
      });
    }
    return _json(_discovery);
  }

  Map<String, dynamic> get _discovery => {
        // An undecided server reports the source its rows are actually using,
        // which is Trakt as soon as it is available — stored or built-in.
        'source': _traktAvailable ? 'trakt_trending' : 'tmdb_trending',
        'english_only': true,
        'sources': ['tmdb_trending', 'trakt_trending', 'tmdb_popular'],
        'trakt_configured': _traktAvailable,
        'hidden_when_unconfigured':
            lastDiscoveryUpdate?['hidden_when_unconfigured'] ??
                {'movie': false, 'tv': false, 'book': false, 'music': false},
        if (cover4KBadges != null)
          'cover_4k_badges':
              lastDiscoveryUpdate?['cover_4k_badges'] ?? cover4KBadges,
      };

  ResponseBody _json(Map<String, dynamic> body) => ResponseBody.fromString(
        jsonEncode(body),
        200,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );

  @override
  void close({bool force = false}) {}
}

class _FakeAuthNotifier extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthState();

  @override
  Future<void> refreshConfig() async {}
}

Future<_DiscoverAdapter> _pumpScreen(
  WidgetTester tester, {
  required bool traktConfigured,
  bool tmdbUsingBuiltin = false,
  bool traktUsingBuiltin = false,
  bool? cover4KBadges,
  String? highlightId,
}) async {
  final adapter = _DiscoverAdapter(
    traktConfigured: traktConfigured,
    tmdbUsingBuiltin: tmdbUsingBuiltin,
    traktUsingBuiltin: traktUsingBuiltin,
    cover4KBadges: cover4KBadges,
  );
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;

  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendClientProvider.overrideWithValue(dio),
        authProvider.overrideWith(_FakeAuthNotifier.new),
      ],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: DiscoverySettingsScreen(highlightId: highlightId),
      ),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

Finder _sourceTile(String label) => find.ancestor(
      of: find.text(label),
      matching: find.byType(ListTile),
    );

void main() {
  testWidgets('Discover tab switches participate in Save and unsaved changes',
      (t) async {
    final adapter = await _pumpScreen(t,
        traktConfigured: true, highlightId: SettingsAnchors.discoveryHideBooks);
    bool dirty() => t
        .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
        .hasChanges();
    expect(dirty(), isFalse);
    final books = find.widgetWithText(
        SwitchListTile, "Hide Books when Chaptarr isn't connected");
    await t.ensureVisible(books);
    await t.tap(books);
    await t.pumpAndSettle();
    expect(dirty(), isTrue);
    final save = find.byKey(const Key('discovery-save'));
    await t.ensureVisible(save);
    await t.tap(save);
    await t.pumpAndSettle();
    expect(adapter.lastDiscoveryUpdate?['hidden_when_unconfigured'],
        {'movie': false, 'tv': false, 'book': true, 'music': false});
    expect(dirty(), isFalse);
  });

  testWidgets('only unsaved edits warn, including after a failed save',
      (tester) async {
    final adapter = await _pumpScreen(tester, traktConfigured: true);
    bool dirty() => tester
        .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
        .hasChanges();
    expect(dirty(), isFalse);
    await tester.tap(find.widgetWithText(
        SwitchListTile, 'Only show English-language titles'));
    await tester.pumpAndSettle();
    expect(dirty(), isTrue);
    await tester.tap(find.widgetWithText(
        SwitchListTile, 'Only show English-language titles'));
    await tester.pumpAndSettle();
    expect(dirty(), isFalse);
    await tester.tap(find.text('All-time popular (TMDB)'));
    await tester.pumpAndSettle();
    expect(dirty(), isTrue);
    final save = find.byKey(const Key('discovery-save'));
    await tester.scrollUntilVisible(save, 120,
        scrollable: find.byType(Scrollable).first);
    await tester.ensureVisible(save);
    await tester.pumpAndSettle();
    adapter.failSaves = true;
    await tester.tap(save);
    await tester.pumpAndSettle();
    expect(dirty(), isTrue,
        reason: 'a failed write must not clear the warning');
    adapter.failSaves = false;
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();
    expect(dirty(), isFalse);
  });

  testWidgets('typing an unsaved credential is detected without a page rebuild',
      (tester) async {
    await _pumpScreen(tester, traktConfigured: false);
    final field = find.byWidgetPredicate(
        (w) => w is TextField && w.decoration?.hintText == 'Trakt client ID');
    await tester.scrollUntilVisible(field, 120,
        scrollable: find.byType(Scrollable).first);
    await tester.enterText(field, 'draft-key');
    final guard =
        tester.widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard));
    expect(guard.hasChanges(), isTrue);
    await tester.enterText(field, '');
    expect(guard.hasChanges(), isFalse);
  });

  testWidgets('marks the Trakt feed as the recommended row source',
      (tester) async {
    await _pumpScreen(tester, traktConfigured: true);

    expect(find.widgetWithText(StatusPill, 'Recommended'), findsOneWidget);
    expect(
      find.descendant(
        of: _sourceTile('Trending now (Trakt)'),
        matching: find.text('Recommended'),
      ),
      findsOneWidget,
      reason: 'the tag belongs to the Trakt option, not to a neighbouring row',
    );

    // The server adopted Trakt on its own; the screen has to open on it or the
    // admin cannot tell what the rows are showing.
    final trakt = tester.widget<ListTile>(_sourceTile('Trending now (Trakt)'));
    expect((trakt.leading as Icon).icon, Icons.radio_button_checked);
    expect(
        find.widgetWithText(
            SwitchListTile, 'Only show English-language titles'),
        findsOneWidget);
    expect(
        tester
            .widget<SwitchListTile>(find.widgetWithText(
                SwitchListTile, 'Only show English-language titles'))
            .value,
        isTrue,
        reason: 'non-English titles are hidden by default');
  });

  testWidgets('keeps the recommendation on the locked Trakt row',
      (tester) async {
    await _pumpScreen(tester, traktConfigured: false);

    // Without the credential the option cannot be picked, which is exactly when
    // naming it as the upgrade is worth something — it is the reason to go add
    // the client ID below.
    expect(find.widgetWithText(StatusPill, 'Recommended'), findsOneWidget);
    final trakt = tester.widget<ListTile>(_sourceTile('Trending now (Trakt)'));
    expect(trakt.enabled, isFalse);
    expect(
      find.text('Add a Trakt client ID below to use this.'),
      findsOneWidget,
    );
  });

  testWidgets('4K badges turn on for the whole server with Save', (t) async {
    final adapter = await _pumpScreen(t,
        traktConfigured: true,
        cover4KBadges: false,
        highlightId: SettingsAnchors.discoveryCover4KBadges);
    bool dirty() => t
        .widget<UnsavedChangesGuard>(find.byType(UnsavedChangesGuard))
        .hasChanges();
    final toggle =
        find.byKey(const Key('discovery-cover-4k-badges'), skipOffstage: false);
    await t.ensureVisible(toggle);
    await t.pumpAndSettle();
    expect(t.widget<SwitchListTile>(toggle).value, isFalse);
    expect(find.textContaining('for everyone on this server'), findsOneWidget);
    await t.tap(toggle);
    await t.pumpAndSettle();
    expect(t.widget<SwitchListTile>(toggle).value, isTrue);
    expect(dirty(), isTrue);
    final save = find.byKey(const Key('discovery-save'), skipOffstage: false);
    await t.ensureVisible(save);
    await t.pumpAndSettle();
    await t.tap(save);
    await t.pumpAndSettle();
    expect(adapter.lastDiscoveryUpdate?['cover_4k_badges'], isTrue);
    expect(dirty(), isFalse);
  });

  testWidgets('an older server shows 4K badges unavailable and never sends it',
      (t) async {
    final adapter = await _pumpScreen(t, traktConfigured: true);
    await t.tap(find.widgetWithText(
        SwitchListTile, 'Only show English-language titles'));
    await t.pumpAndSettle();
    final toggle = find.byKey(const Key('discovery-cover-4k-badges'));
    await t.scrollUntilVisible(toggle, 120,
        scrollable: find.byType(Scrollable).first);
    await t.pumpAndSettle();
    expect(t.widget<SwitchListTile>(toggle).onChanged, isNull);
    expect(find.text('Update your Cantinarr server to show 4K badges.'),
        findsOneWidget);
    final save = find.byKey(const Key('discovery-save'));
    await t.scrollUntilVisible(save, 120,
        scrollable: find.byType(Scrollable).first);
    await t.ensureVisible(save);
    await t.pumpAndSettle();
    await t.tap(save);
    await t.pumpAndSettle();
    expect(adapter.lastDiscoveryUpdate, isNotNull);
    expect(adapter.lastDiscoveryUpdate!.containsKey('cover_4k_badges'), isFalse);
  });

  testWidgets('saves the picked source and language filter', (tester) async {
    final adapter = await _pumpScreen(tester, traktConfigured: true);

    await tester.tap(find.text('All-time popular (TMDB)'));
    await tester.pumpAndSettle();
    await tester.tap(find.widgetWithText(
        SwitchListTile, 'Only show English-language titles'));
    await tester.pumpAndSettle();

    final save = find.byKey(const Key('discovery-save'));
    await tester.scrollUntilVisible(save, 120,
        scrollable: find.byType(Scrollable).first);
    await tester.ensureVisible(save);
    await tester.pumpAndSettle();
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastDiscoveryUpdate?['source'], 'tmdb_popular');
    expect(adapter.lastDiscoveryUpdate?['english_only'], false);
    expect(adapter.lastCredentialsUpdate, isNull,
        reason: 'empty credential fields must not touch the registry');
  });

  testWidgets('saves an entered Trakt client ID with the settings',
      (tester) async {
    final adapter = await _pumpScreen(tester, traktConfigured: false);

    final traktField = find.byWidgetPredicate(
      (w) => w is TextField && w.decoration?.hintText == 'Trakt client ID',
    );
    await tester.scrollUntilVisible(traktField, 120,
        scrollable: find.byType(Scrollable).first);
    await tester.enterText(traktField, 'synthetic-trakt-id');

    final save = find.byKey(const Key('discovery-save'));
    await tester.scrollUntilVisible(save, 120,
        scrollable: find.byType(Scrollable).first);
    await tester.ensureVisible(save);
    await tester.pumpAndSettle();
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(
      adapter.lastCredentialsUpdate,
      {'trakt_client_id': 'synthetic-trakt-id'},
    );
    expect(adapter.lastDiscoveryUpdate, isNotNull);
  });

  testWidgets('TMDB reports the built-in key when no admin token is stored',
      (tester) async {
    await _pumpScreen(
      tester,
      traktConfigured: false,
      tmdbUsingBuiltin: true,
    );

    await tester.scrollUntilVisible(
      find.text('Built-in key'),
      120,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pumpAndSettle();

    expect(find.text('Built-in key'), findsOneWidget);
    expect(
      find.textContaining('built-in key out of the box'),
      findsOneWidget,
    );
    // Only TMDB has a built-in fallback; Trakt stays "Not set".
    expect(find.text('Not set'), findsOneWidget);
  });

  testWidgets('Trakt reports the built-in app when no client ID is stored',
      (tester) async {
    await _pumpScreen(
      tester,
      traktConfigured: false,
      traktUsingBuiltin: true,
    );

    // The built-in app makes the Trakt source selectable with nothing stored,
    // and the server auto-adopts it as the headline feed.
    final trakt = tester.widget<ListTile>(_sourceTile('Trending now (Trakt)'));
    expect(trakt.enabled, isTrue);
    expect((trakt.leading as Icon).icon, Icons.radio_button_checked);

    await tester.scrollUntilVisible(
      find.text('Built-in key'),
      120,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.pumpAndSettle();
    // Exactly one built-in chip: Trakt's (TMDB carries an admin token in
    // this fixture).
    expect(find.text('Built-in key'), findsOneWidget);
  });

  testWidgets('a highlight deep link scrolls to the Trakt section on load',
      (tester) async {
    await _pumpScreen(
      tester,
      traktConfigured: true,
      highlightId: SettingsAnchors.credentialsTrakt,
    );

    expect(
      tester
          .state<ScrollableState>(find.byType(Scrollable).first)
          .position
          .pixels,
      greaterThan(0),
    );
    expect(find.text('Trakt'), findsOneWidget);
  });
}
