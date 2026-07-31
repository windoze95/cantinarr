import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/widgets/horizontal_item_row.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/dashboard/ui/dashboard_tv_tab.dart';
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
}) =>
    {
      'id': id,
      'title': title,
      'tmdbId': id,
      // No images: a null poster keeps the cards off the network.
      'images': <Object>[],
      'statistics': {'episodeFileCount': files, 'episodeCount': episodes},
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
  });

  final List<Map<String, dynamic>> series;
  final List<Map<String, dynamic>> history;
  final List<Map<String, dynamic>> calendar;
  final bool failHistory;
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
