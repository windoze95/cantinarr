import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/horizontal_item_row.dart';
import 'package:cantinarr/core/widgets/media_card.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_tv_tab.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Covers the wiring the pure row builders in library_rows_test.dart cannot
/// see: that the tab asks Sonarr for import history at all, and that the row
/// on screen is the one those builders produced.
void main() {
  testWidgets('Recently Downloaded is ordered by import date, not completeness',
      (tester) async {
    final adapter = _SonarrAdapter(
      series: [
        _series(id: 1, title: 'Finished Show', files: 100, episodes: 100),
        _series(id: 2, title: 'Just Landed', files: 1, episodes: 20),
      ],
      history: [
        _import(seriesId: 2, date: '2026-07-25T09:00:00Z'),
        _import(seriesId: 1, date: '2025-01-01T09:00:00Z'),
      ],
      calendar: const [],
    );

    await _pumpTvTab(tester, adapter);

    // Sonarr ranks the finished show at 100% and the new arrival at 5%; only
    // the import dates put the show that actually just downloaded on top.
    expect(find.text('Recently Downloaded'), findsOneWidget);
    expect(
      _rowItems(tester).map((s) => s.title),
      ['Just Landed', 'Finished Show'],
    );
  });

  testWidgets('asks Sonarr for imports rather than the whole history',
      (tester) async {
    final adapter = _SonarrAdapter(
      series: [_series(id: 1, title: 'Just Landed', files: 1, episodes: 20)],
      history: [_import(seriesId: 1, date: '2026-07-25T09:00:00Z')],
      calendar: const [],
    );

    await _pumpTvTab(tester, adapter);

    // Grabs and failures outnumber imports, so an unfiltered page would spend
    // itself on events this row cannot use.
    expect(adapter.historyQuery['eventType'],
        SonarrHistoryRecord.importedEventTypeId);
    expect(adapter.historyQuery['pageSize'], greaterThan(50));
  });

  testWidgets('Airing Next is ordered by air date, not library order',
      (tester) async {
    final adapter = _SonarrAdapter(
      // Sonarr returns the library sorted by title.
      series: [
        _series(id: 1, title: 'Alpha Series', files: 4, episodes: 8),
        _series(id: 2, title: 'Zulu Series', files: 2, episodes: 8),
      ],
      history: const [],
      calendar: [
        _airing(seriesId: 1, airDateUtc: '2026-07-31T01:00:00Z'),
        _airing(seriesId: 2, airDateUtc: '2026-07-26T01:00:00Z'),
      ],
    );

    await _pumpTvTab(tester, adapter);

    expect(find.text('Airing Next'), findsOneWidget);
    expect(
      _rowItems(tester).map((s) => s.title),
      ['Zulu Series', 'Alpha Series'],
    );
  });

  testWidgets('drops the row rather than misordering it when history fails',
      (tester) async {
    final adapter = _SonarrAdapter(
      series: [_series(id: 1, title: 'Finished Show', files: 100, episodes: 100)],
      history: const [],
      calendar: const [],
      failHistory: true,
    );

    await _pumpTvTab(tester, adapter);

    // A series record carries no import date, so there is nothing to fall back
    // to. Showing the library under a "Recently Downloaded" heading would be a
    // claim the app cannot support.
    expect(find.text('Recently Downloaded'), findsNothing);
  });

  testWidgets(
      'TV Discover browse rows badge Partial/Requested from the Sonarr library',
      (tester) async {
    final adapter = _SonarrAdapter(
      series: [
        _series(
          id: 201,
          title: 'Partial Series',
          files: 18,
          episodes: 18,
          totalEpisodeCount: 24,
        ),
        _series(
          id: 202,
          title: 'Requested Series',
          files: 0,
          episodes: 0,
          totalEpisodeCount: 10,
        ),
        _series(
          id: 203,
          title: 'Unmonitored Empty Series',
          files: 0,
          episodes: 0,
          totalEpisodeCount: 10,
          monitored: false,
        ),
      ],
      history: const [],
      calendar: const [],
      featured: [
        _tvFeatured(id: 200, name: 'Hero Series'),
        _tvFeatured(id: 201, name: 'Partial Series'),
        _tvFeatured(id: 202, name: 'Requested Series'),
        _tvFeatured(id: 203, name: 'Unmonitored Empty Series'),
        _tvFeatured(id: 204, name: 'No Match Series'),
      ],
    );

    await _pumpTvTab(tester, adapter);

    final byTitle = {for (final c in _browseRowCards(tester)) c.title: c};

    expect(byTitle['Partial Series']?.statusLabel, 'Partial');
    expect(byTitle['Partial Series']?.statusColor, AppTheme.requested);
    expect(byTitle['Partial Series']?.subtitle, '18/24 eps');

    expect(byTitle['Requested Series']?.statusLabel, 'Requested');
    expect(byTitle['Requested Series']?.statusColor, AppTheme.requested);
    expect(byTitle['Requested Series']?.subtitle, isNull);

    expect(byTitle['Unmonitored Empty Series']?.statusLabel, isNull);
    expect(byTitle['Unmonitored Empty Series']?.subtitle, isNull);
    expect(byTitle['No Match Series']?.statusLabel, isNull);

    // D-01: the hero is a different widget entirely, never a MediaCard.
    expect(byTitle.containsKey('Hero Series'), isFalse);

    // The browse row reserves the same taller height the Sonarr library row
    // already uses for its subtitle-bearing MediaCard row — both now read
    // MediaCard.subtitleRowExtraHeight, so this pins the shared constant
    // rather than a literal that could drift from dashboard_tv_tab.dart's
    // own _buildRow.
    const cardWidth = 108.0; // 390px test viewport: width < 600.
    final browseRows = tester.widgetList<HorizontalItemRow<MediaItem>>(
      find.byType(HorizontalItemRow<MediaItem>),
    );
    for (final row in browseRows) {
      expect(row.height, cardWidth * 1.5 + MediaCard.subtitleRowExtraHeight);
    }
  });

  testWidgets(
      'TV Discover browse-row badges survive a history-fetch outage',
      (tester) async {
    final adapter = _SonarrAdapter(
      series: [
        _series(
          id: 202,
          title: 'Requested Series',
          files: 0,
          episodes: 0,
          totalEpisodeCount: 10,
        ),
      ],
      history: const [],
      calendar: const [],
      failHistory: true,
      featured: [
        _tvFeatured(id: 200, name: 'Hero Series'),
        _tvFeatured(id: 202, name: 'Requested Series'),
      ],
    );

    await _pumpTvTab(tester, adapter);

    final badged =
        _browseRowCards(tester).where((c) => c.statusLabel != null);
    expect(badged, isNotEmpty);
  });
}

/// The MediaCards rendered by Discover browse rows (CategoryRow), as opposed
/// to the dashboard's own Sonarr library rows — both use MediaCard, but only
/// the browse rows are this plan's concern, and both can carry the same
/// series title with a different (correct, out-of-scope) badge.
List<MediaCard> _browseRowCards(WidgetTester tester) {
  final rows = find.byType(HorizontalItemRow<MediaItem>);
  final cards = <MediaCard>[];
  for (final element in tester.elementList(rows)) {
    cards.addAll(tester
        .widgetList<MediaCard>(find.descendant(
          of: find.byWidget(element.widget),
          matching: find.byType(MediaCard),
        ))
        .toList());
  }
  return cards;
}

/// The items of the tab's only Sonarr row. Each test leaves exactly one of the
/// two library rows populated, so this needs no disambiguation.
List<SonarrSeries> _rowItems(WidgetTester tester) => tester
    .widget<HorizontalItemRow<SonarrSeries>>(
        find.byType(HorizontalItemRow<SonarrSeries>))
    .items;

Future<void> _pumpTvTab(WidgetTester tester, _SonarrAdapter adapter) async {
  tester.view.physicalSize = const Size(390, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;
  final container = ProviderContainer(
    overrides: [
      authProvider.overrideWith(() => _FakeAuthNotifier(_tvState)),
      backendClientProvider.overrideWithValue(dio),
    ],
  );
  addTearDown(container.dispose);

  await container.read(authProvider.future);
  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: Scaffold(body: DashboardTvTab())),
    ),
  );
  await tester.pumpAndSettle();
}

const _tvState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    services: AvailableServices(sonarr: true),
    instances: [
      ServiceInstance(
        id: 'tv',
        serviceType: 'sonarr',
        name: 'TV',
        isDefault: true,
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
);

Map<String, dynamic> _series({
  required int id,
  required String title,
  required int files,
  required int episodes,
  bool monitored = true,
  int? totalEpisodeCount,
}) =>
    {
      'id': id,
      'title': title,
      'tmdbId': id,
      'monitored': monitored,
      // No images: a null poster keeps the cards off the network.
      'images': <Object>[],
      'statistics': {
        'episodeFileCount': files,
        'episodeCount': episodes,
        if (totalEpisodeCount != null) 'totalEpisodeCount': totalEpisodeCount,
      },
    };

/// A TV Discover browse-row featured result. Every field a card could touch
/// the network for is left null so no test hits it.
Map<String, dynamic> _tvFeatured({required int id, required String name}) => {
      'id': id,
      'name': name,
      'poster_path': null,
      'first_air_date': null,
      'vote_average': 0,
    };

Map<String, dynamic> _import({required int seriesId, required String date}) => {
      'id': seriesId,
      'seriesId': seriesId,
      'date': date,
      'eventType': SonarrHistoryRecord.importedEventType,
    };

Map<String, dynamic> _airing({
  required int seriesId,
  required String airDateUtc,
}) =>
    {'seriesId': seriesId, 'airDateUtc': airDateUtc};

class _FakeAuthNotifier extends AuthNotifier {
  _FakeAuthNotifier(this._initial);

  final AuthState _initial;

  @override
  Future<AuthState> build() async => _initial;
}

class _SonarrAdapter implements HttpClientAdapter {
  _SonarrAdapter({
    required this.series,
    required this.history,
    required this.calendar,
    this.failHistory = false,
    this.featured = const [],
  });

  final List<Map<String, dynamic>> series;
  final List<Map<String, dynamic>> history;
  final List<Map<String, dynamic>> calendar;
  final bool failHistory;

  /// TV Discover's headline feed. Defaults empty so the existing library-row
  /// tests, which don't care about Discover at all, keep working unchanged.
  final List<Map<String, dynamic>> featured;
  Map<String, dynamic> historyQuery = const {};

  static const _base = '/api/instances/tv/api/v3';

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    Object body;
    if (options.path == '$_base/series') {
      body = series;
    } else if (options.path == '$_base/history') {
      historyQuery = Map<String, dynamic>.from(options.queryParameters);
      if (failHistory) {
        return ResponseBody.fromString('{"error":"unavailable"}', 503,
            headers: {
              'content-type': ['application/json'],
            });
      }
      body = {'records': history, 'totalRecords': history.length};
    } else if (options.path == '$_base/calendar') {
      body = calendar;
    } else if (options.path == '/api/discover/tv/featured') {
      body = {
        'source': 'tmdb_trending',
        'page': 1,
        'results': featured,
        'total_pages': 1,
        'total_results': featured.length,
      };
    } else {
      // Discovery rows: empty is enough, and every fetch there is guarded.
      body = {
        'page': 1,
        'results': <Object>[],
        'total_pages': 0,
        'total_results': 0,
      };
    }
    return ResponseBody.fromString(
      jsonEncode(body),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
