import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_detail_screen.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// American Dad!-shaped payload: season 22 has 13 episodes, 11 of them aired
/// and on disk, and Sonarr is still waiting on the last two. Sonarr's own
/// episodeCount stops at 11, which is how the card came to claim "100% •
/// 11/11 Episodes Available" for a season the episode list shows 13 rows for.
Map<String, dynamic> _seriesJson() => {
      'id': 7,
      'title': 'American Dad!',
      'monitored': true,
      'status': 'continuing',
      'statistics': {
        'seasonCount': 2,
        'episodeFileCount': 19,
        'episodeCount': 19,
        'totalEpisodeCount': 21,
        'sizeOnDisk': 12000000000,
        'percentOfEpisodes': 100.0,
      },
      'seasons': [
        {
          'seasonNumber': 21,
          'monitored': true,
          'statistics': {
            'episodeFileCount': 8,
            'episodeCount': 8,
            'totalEpisodeCount': 8,
            'sizeOnDisk': 5000000000,
            'percentOfEpisodes': 100.0,
          },
        },
        {
          'seasonNumber': 22,
          'monitored': true,
          'statistics': {
            'episodeFileCount': 11,
            'episodeCount': 11,
            'totalEpisodeCount': 13,
            'sizeOnDisk': 6657000000,
            'percentOfEpisodes': 100.0,
            'nextAiring': '2026-09-13T04:00:00Z',
          },
        },
      ],
    };

/// One `queue/details` row per queued episode, as Sonarr returns it.
Map<String, dynamic> _queueRow(int episode,
        {required int season, required String airDate, required String state}) =>
    {
      'id': 900 + episode,
      'seriesId': 7,
      'title': 'American.Dad.S${season}E$episode',
      'trackedDownloadState': state,
      'status': 'completed',
      'episode': {
        'id': 100 + episode,
        'seasonNumber': season,
        'episodeNumber': episode,
        'airDateUtc': airDate,
      },
    };

class _SeriesAdapter implements HttpClientAdapter {
  _SeriesAdapter({this.queue = const [], this.queueFails = false});

  final List<Map<String, dynamic>> queue;
  final bool queueFails;

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? _,
      Future<void>? __) async {
    const json = Headers.jsonContentType;
    if (options.path.endsWith('/queue/details')) {
      if (queueFails) {
        return ResponseBody.fromString('{"message":"boom"}', 500,
            headers: {
              Headers.contentTypeHeader: [json]
            });
      }
      return ResponseBody.fromString(jsonEncode(queue), 200, headers: {
        Headers.contentTypeHeader: [json]
      });
    }
    return ResponseBody.fromString(jsonEncode(_seriesJson()), 200, headers: {
      Headers.contentTypeHeader: [json]
    });
  }

  @override
  void close({bool force = false}) {}
}

Future<void> _pump(WidgetTester tester, _SeriesAdapter adapter) async {
  // Phone-sized, like the screen this shipped wrong on.
  await tester.binding.setSurfaceSize(const Size(390, 844));
  addTearDown(() => tester.binding.setSurfaceSize(null));

  final dio = Dio(BaseOptions(baseUrl: 'https://cantinarr.example'))
    ..httpClientAdapter = adapter;

  await tester.pumpWidget(ProviderScope(
    overrides: [backendClientProvider.overrideWithValue(dio)],
    child: MaterialApp(
      theme: AppTheme.dark,
      home: SonarrSeriesDetailScreen(
        instanceId: 'sonarr-main',
        series: SonarrSeries.fromJson(_seriesJson()),
      ),
    ),
  ));
  await tester.pumpAndSettle();
}

Color _colorOf(WidgetTester tester, String text) =>
    tester.widget<Text>(find.text(text)).style!.color!;

void main() {
  testWidgets('an airing season counts the episodes it is still waiting on',
      (tester) async {
    await _pump(tester, _SeriesAdapter());

    // Season 22: caught up on everything that has aired, but not complete —
    // and never "100%", which is what made it look finished.
    expect(find.text('11/13 Episodes Available • 2 unaired'), findsOneWidget);
    expect(
      _colorOf(tester, '11/13 Episodes Available • 2 unaired'),
      AppTheme.downloading,
    );
    expect(find.textContaining('11/11'), findsNothing);
    expect(find.textContaining('100%'), findsNothing);

    // Season 21 is genuinely done, and All Seasons rolls both up.
    expect(find.text('8/8 Episodes Available'), findsOneWidget);
    expect(_colorOf(tester, '8/8 Episodes Available'), AppTheme.available);
    expect(find.text('19/21 Episodes Available • 2 unaired'), findsOneWidget);
  });

  testWidgets('episodes sitting in the queue are not called missing',
      (tester) async {
    await _pump(
      tester,
      _SeriesAdapter(queue: [
        for (final e in [12, 13])
          _queueRow(e,
              season: 22,
              airDate: '2026-09-13T04:00:00Z',
              state: 'importPending'),
      ]),
    );

    // The two remaining episodes are unaired *and* already downloaded, so they
    // are named once — by the state the admin can act on.
    expect(find.text('11/13 Episodes Available • 2 waiting to import'),
        findsOneWidget);
    expect(find.textContaining('2 unaired'), findsNothing);
    expect(find.text('19/21 Episodes Available • 2 waiting to import'),
        findsOneWidget);
  });

  testWidgets('a queue that fails to load leaves the cards standing',
      (tester) async {
    await _pump(tester, _SeriesAdapter(queueFails: true));

    expect(find.text('11/13 Episodes Available • 2 unaired'), findsOneWidget);
    expect(find.textContaining('Failed to load'), findsNothing);
  });
}
