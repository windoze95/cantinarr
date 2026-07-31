import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/status_pill.dart';
import 'package:cantinarr/features/settings/ui/discovery_settings_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Serves the discovery payload the server would send, so the screen renders
/// against the real contract rather than a hand-built model.
class _DiscoveryAdapter implements HttpClientAdapter {
  _DiscoveryAdapter({required this.traktConfigured});

  final bool traktConfigured;
  Map<String, dynamic>? lastUpdate;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.method == 'PUT' && requestStream != null) {
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      lastUpdate = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
    }
    return ResponseBody.fromString(
      jsonEncode({
        // An undecided server reports the source its rows are actually using,
        // which is Trakt as soon as the credential exists.
        'source': traktConfigured ? 'trakt_trending' : 'tmdb_trending',
        'english_only': true,
        'sources': ['tmdb_trending', 'trakt_trending', 'tmdb_popular'],
        'trakt_configured': traktConfigured,
      }),
      200,
      headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

Future<_DiscoveryAdapter> _pumpScreen(
  WidgetTester tester, {
  required bool traktConfigured,
}) async {
  final adapter = _DiscoveryAdapter(traktConfigured: traktConfigured);
  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;

  await tester.pumpWidget(
    ProviderScope(
      overrides: [backendClientProvider.overrideWithValue(dio)],
      child: MaterialApp(
        theme: AppTheme.dark,
        home: const DiscoverySettingsScreen(),
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
    expect(find.byType(SwitchListTile), findsOneWidget);
    expect(tester.widget<SwitchListTile>(find.byType(SwitchListTile)).value,
        isTrue,
        reason: 'non-English titles are hidden by default');
  });

  testWidgets('keeps the recommendation on the locked Trakt row',
      (tester) async {
    await _pumpScreen(tester, traktConfigured: false);

    // Without the credential the option cannot be picked, which is exactly when
    // naming it as the upgrade is worth something — it is the reason to go add
    // the client ID.
    expect(find.widgetWithText(StatusPill, 'Recommended'), findsOneWidget);
    final trakt = tester.widget<ListTile>(_sourceTile('Trending now (Trakt)'));
    expect(trakt.enabled, isFalse);
    expect(
      find.text('Add a Trakt client ID under Credentials to use this.'),
      findsOneWidget,
    );
  });

  testWidgets('saves the picked source and language filter', (tester) async {
    final adapter = await _pumpScreen(tester, traktConfigured: true);

    await tester.tap(find.text('All-time popular (TMDB)'));
    await tester.pumpAndSettle();
    await tester.tap(find.byType(SwitchListTile));
    await tester.pumpAndSettle();

    final save = find.byKey(const Key('discovery-save'));
    await tester.ensureVisible(save);
    await tester.tap(save);
    await tester.pumpAndSettle();

    expect(adapter.lastUpdate?['source'], 'tmdb_popular');
    expect(adapter.lastUpdate?['english_only'], false);
  });
}
