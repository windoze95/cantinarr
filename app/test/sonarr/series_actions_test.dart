import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_api_service.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/series_actions.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_detail_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter: routes GETs to canned bodies and records every request
/// (method, path, query, decoded body) for assertions.
class _FakeAdapter implements HttpClientAdapter {
  final List<
      ({
        String method,
        String path,
        Map<String, dynamic> query,
        dynamic body,
      })> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    requests.add((
      method: options.method,
      path: path,
      query: options.uri.queryParameters,
      body: body,
    ));

    dynamic response = <String, dynamic>{};
    if (options.method == 'GET') {
      if (path.endsWith('/series/7')) response = _rawSeries;
      if (path.endsWith('/qualityprofile')) response = _profiles;
      if (path.endsWith('/tag')) response = _tags;
    }
    return ResponseBody.fromString(
      jsonEncode(response),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

/// The series as Sonarr returns it, including a field the app doesn't model —
/// updates must send it back unchanged.
const _rawSeries = {
  'id': 7,
  'title': 'Example',
  'monitored': true,
  'seasonFolder': true,
  'qualityProfileId': 5,
  'seriesType': 'standard',
  'path': '/tv/Example',
  'tvdbId': 999,
  'tmdbId': 555,
  'imdbId': 'tt123',
  'tags': [2],
  'someUnknownField': {
    'nested': [1, 2]
  },
  'seasons': [
    {
      'seasonNumber': 1,
      'monitored': true,
      'statistics': {
        'episodeFileCount': 8,
        'episodeCount': 8,
        'totalEpisodeCount': 8,
        'sizeOnDisk': 1000000000,
      },
    },
    {'seasonNumber': 0, 'monitored': false},
  ],
};

const _profiles = [
  {'id': 5, 'name': 'HD-1080p'},
  {'id': 9, 'name': 'Best'},
];

const _tags = [
  {'id': 2, 'label': 'kids'},
  {'id': 3, 'label': '4k'},
];

final _series = SonarrSeries.fromJson(
    Map<String, dynamic>.from(_rawSeries));

({_FakeAdapter adapter, Dio dio}) _fakeDio() {
  final adapter = _FakeAdapter();
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  return (adapter: adapter, dio: dio);
}

void main() {
  group('showSeriesActions', () {
    late _FakeAdapter adapter;
    var changed = 0;
    var removed = 0;

    Future<void> pumpHarness(WidgetTester tester) async {
      final fake = _fakeDio();
      adapter = fake.adapter;
      changed = 0;
      removed = 0;
      final service =
          SonarrApiService(backendDio: fake.dio, instanceId: 'inst1');
      await tester.pumpWidget(
        ProviderScope(
          overrides: [backendClientProvider.overrideWithValue(fake.dio)],
          child: MaterialApp(
            home: Scaffold(
              body: Builder(
                builder: (ctx) => TextButton(
                  onPressed: () => showSeriesActions(
                    ctx,
                    service: service,
                    instanceId: 'inst1',
                    series: _series,
                    onChanged: () => changed++,
                    onRemoved: () => removed++,
                  ),
                  child: const Text('open'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
    }

    List<({String method, String path, Map<String, dynamic> query, dynamic body})>
        ofMethod(String method) =>
            adapter.requests.where((r) => r.method == method).toList();

    testWidgets('shows every action for a monitored series', (tester) async {
      await pumpHarness(tester);
      for (final label in [
        'Search Monitored',
        'Edit Series',
        'Refresh Series',
        'Remove Series',
        'Unmonitor Series',
      ]) {
        expect(find.text(label), findsOneWidget);
      }
    });

    testWidgets('Search Monitored posts a SeriesSearch command',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Search Monitored'));
      await tester.pumpAndSettle();

      final posts = ofMethod('POST');
      expect(posts, hasLength(1));
      expect(posts.single.path, endsWith('/command'));
      expect(posts.single.body, {'name': 'SeriesSearch', 'seriesId': 7});
    });

    testWidgets('Refresh Series posts RefreshSeries and reloads',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Refresh Series'));
      await tester.pumpAndSettle();

      final posts = ofMethod('POST');
      expect(posts.single.body, {'name': 'RefreshSeries', 'seriesId': 7});
      expect(changed, 1);
    });

    testWidgets(
        'Unmonitor round-trips the whole series with only monitored flipped',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Unmonitor Series'));
      await tester.pumpAndSettle();

      final puts = ofMethod('PUT');
      expect(puts, hasLength(1));
      final body = puts.single.body as Map<String, dynamic>;
      expect(body['monitored'], false);
      expect(body['someUnknownField'], {'nested': [1, 2]},
          reason: 'unmodelled fields must survive the round-trip');
      expect(body['qualityProfileId'], 5);
      expect(changed, 1);
    });

    testWidgets('Remove asks for confirmation and honors delete-files',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Remove Series'));
      await tester.pumpAndSettle();

      // Cancel first: nothing deleted.
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
      expect(ofMethod('DELETE'), isEmpty);
      expect(removed, 0);

      // Again, this time deleting files too.
      await tester.tap(find.text('open'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Remove Series'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete files from disk'));
      await tester.pump();
      await tester.tap(find.text('Remove'));
      await tester.pumpAndSettle();

      final deletes = ofMethod('DELETE');
      expect(deletes, hasLength(1));
      expect(deletes.single.path, endsWith('/series/7'));
      expect(deletes.single.query['deleteFiles'], 'true');
      expect(removed, 1);
    });

    testWidgets(
        'Edit Series opens the editor; saving PUTs the patch and reloads',
        (tester) async {
      await pumpHarness(tester);
      await tester.tap(find.text('Edit Series'));
      await tester.pumpAndSettle();

      // The editor loaded the fresh series + profiles + tags.
      expect(find.text('Edit Series'), findsOneWidget);
      expect(find.text('HD-1080p'), findsOneWidget);
      expect(find.text('kids'), findsOneWidget);

      // Flip Monitored off and pick another quality profile.
      await tester.tap(find.text('Monitored'));
      await tester.pump();
      await tester.tap(find.text('Quality Profile'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Best'));
      await tester.pumpAndSettle();

      await tester.tap(find.text('Update'));
      await tester.pumpAndSettle();

      final puts = ofMethod('PUT');
      expect(puts, hasLength(1));
      final body = puts.single.body as Map<String, dynamic>;
      expect(body['monitored'], false);
      expect(body['qualityProfileId'], 9);
      expect(body['seasonFolder'], true);
      expect(body['seriesType'], 'standard');
      expect(body['path'], '/tv/Example');
      expect(body['tags'], [2]);
      expect(body['someUnknownField'], {'nested': [1, 2]},
          reason: 'unmodelled fields must survive the edit round-trip');

      // The editor popped back to the harness and signalled a change.
      expect(find.text('open'), findsOneWidget);
      expect(changed, 1);
    });
  });

  group('SonarrSeriesDetailScreen', () {
    Future<_FakeAdapter> pumpDetail(WidgetTester tester) async {
      final fake = _fakeDio();
      await tester.pumpWidget(
        ProviderScope(
          overrides: [backendClientProvider.overrideWithValue(fake.dio)],
          child: MaterialApp(
            home: SonarrSeriesDetailScreen(
              instanceId: 'inst1',
              series: _series,
            ),
          ),
        ),
      );
      await tester.pumpAndSettle();
      return fake.adapter;
    }

    testWidgets('long-pressing a season offers automatic season search',
        (tester) async {
      final adapter = await pumpDetail(tester);

      await tester.longPress(find.text('Season 1'));
      await tester.pumpAndSettle();
      expect(find.text('Automatic Search'), findsOneWidget);
      expect(find.text('Interactive Search'), findsOneWidget);

      await tester.tap(find.text('Automatic Search'));
      await tester.pumpAndSettle();

      final posts =
          adapter.requests.where((r) => r.method == 'POST').toList();
      expect(posts, hasLength(1));
      expect(posts.single.body,
          {'name': 'SeasonSearch', 'seriesId': 7, 'seasonNumber': 1});
    });

    testWidgets('app bar carries links, edit and the series action menu',
        (tester) async {
      final adapter = await pumpDetail(tester);

      expect(find.byTooltip('External links'), findsOneWidget);
      expect(find.byTooltip('Edit series'), findsOneWidget);

      // Links sheet lists the sites derivable from the series ids.
      await tester.tap(find.byTooltip('External links'));
      await tester.pumpAndSettle();
      for (final site in ['IMDb', 'TheTVDB', 'TMDB', 'Trakt']) {
        expect(find.text(site), findsOneWidget);
      }
      await tester.tapAt(const Offset(10, 10)); // dismiss without launching
      await tester.pumpAndSettle();

      // The overflow opens the same series action sheet.
      await tester.tap(find.byTooltip('Series actions'));
      await tester.pumpAndSettle();
      expect(find.text('Search Monitored'), findsOneWidget);
      await tester.tap(find.text('Search Monitored'));
      await tester.pumpAndSettle();
      final posts =
          adapter.requests.where((r) => r.method == 'POST').toList();
      expect(posts.single.body, {'name': 'SeriesSearch', 'seriesId': 7});
    });
  });
}
