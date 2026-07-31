import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_calendar_screen.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_series_detail_screen.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_wanted_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Map<String, dynamic> _embeddedSeries() => {
      'id': 7,
      'title': 'Example',
      'images': <Map<String, dynamic>>[],
    };

Map<String, dynamic> _wantedRecord() => {
      'id': 11,
      'seriesId': 7,
      'seasonNumber': 1,
      'episodeNumber': 5,
      'title': 'Pilot',
      'airDateUtc': '2026-07-01T01:00:00Z',
      'monitored': true,
      'series': _embeddedSeries(),
    };

Map<String, dynamic> _calendarEntry(DateTime airUtc,
        {int episode = 1, bool hasFile = false}) =>
    {
      'id': 100 + episode,
      'seriesId': 7,
      'seasonNumber': 1,
      'episodeNumber': episode,
      'title': 'Episode $episode',
      'airDateUtc': airUtc.toIso8601String(),
      'hasFile': hasFile,
      'series': _embeddedSeries(),
    };

/// Fake Dio adapter: serves configured wanted/calendar bodies plus the canned
/// series detail endpoints, and records every request for assertions.
class _FakeAdapter implements HttpClientAdapter {
  final List<({String method, String path, Map<String, dynamic> query})>
      requests = [];

  List<Map<String, dynamic>> wantedRecords = [];
  List<Map<String, dynamic>> calendar = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path;
    requests.add((
      method: options.method,
      path: path,
      query: options.uri.queryParameters,
    ));

    dynamic response = <String, dynamic>{};
    if (options.method == 'GET') {
      if (path.contains('/wanted/')) {
        response = {
          'page': 1,
          'pageSize': 50,
          'totalRecords': wantedRecords.length,
          'records': wantedRecords,
        };
      } else if (path.endsWith('/calendar')) {
        response = calendar;
      } else if (path.endsWith('/series/7')) {
        response = _embeddedSeries();
      } else if (path.endsWith('/qualityprofile') ||
          path.endsWith('/tag') ||
          path.endsWith('/episode')) {
        response = <dynamic>[];
      } else if (path.endsWith('/queue')) {
        response = {'records': <dynamic>[]};
      }
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

class _SonarrInstances extends InstanceNotifier {
  @override
  InstanceState build() => const InstanceState(
        sonarrInstances: [
          ServiceInstance(id: 'sonarr-1', serviceType: 'sonarr', name: 'Sonarr'),
        ],
      );
}

Future<_FakeAdapter> _pump(
  WidgetTester tester,
  Widget screen,
  void Function(_FakeAdapter adapter) seed,
) async {
  tester.view.physicalSize = const Size(800, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final adapter = _FakeAdapter();
  seed(adapter);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendClientProvider.overrideWithValue(dio),
        instanceProvider.overrideWith(_SonarrInstances.new),
      ],
      child: MaterialApp(home: Scaffold(body: screen)),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

void main() {
  group('SonarrWantedRecord.fromJson', () {
    test('parses the embedded series alongside the flat title', () {
      final record = SonarrWantedRecord.fromJson({
        ..._wantedRecord(),
        'series': {
          'id': 7,
          'title': 'Example',
          'images': [
            {'coverType': 'poster', 'remoteUrl': 'https://img.example/p.jpg'},
          ],
        },
      });

      expect(record.seriesTitle, 'Example');
      expect(record.series?.id, 7);
      expect(record.series?.posterUrl, 'https://img.example/p.jpg');
    });

    test('tolerates a record without a series object', () {
      final record = SonarrWantedRecord.fromJson({'id': 11});

      expect(record.series, isNull);
      expect(record.seriesTitle, isNull);
    });
  });

  group('SonarrCalendarEntry.fromJson', () {
    test('parses the episode and its embedded series', () {
      final entry = SonarrCalendarEntry.fromJson({
        'id': 3,
        'seriesId': 7,
        'seasonNumber': 1,
        'episodeNumber': 5,
        'title': 'Pilot',
        'airDateUtc': '2026-07-01T01:00:00Z',
        'hasFile': true,
        'series': _embeddedSeries(),
      });

      expect(entry.seasonEpisodeLabel, 'S01E05');
      expect(entry.airDateUtc, DateTime.utc(2026, 7, 1, 1));
      expect(entry.hasFile, isTrue);
      expect(entry.series?.title, 'Example');
    });

    test('tolerates a bare entry', () {
      final entry = SonarrCalendarEntry.fromJson({'id': 3});

      expect(entry.series, isNull);
      expect(entry.airDateUtc, isNull);
      expect(entry.hasFile, isFalse);
    });
  });

  group('SonarrWantedScreen', () {
    testWidgets('shows the series with a poster slot and requests the series',
        (tester) async {
      final adapter = await _pump(
        tester,
        const SonarrWantedScreen(),
        (a) => a.wantedRecords = [_wantedRecord()],
      );

      final wanted = adapter.requests
          .where((r) => r.path.endsWith('/wanted/missing'))
          .toList();
      expect(wanted, hasLength(1));
      expect(wanted.first.query['includeSeries'], 'true');

      expect(find.text('Example'), findsOneWidget);
      expect(find.textContaining('S01E05 • Pilot'), findsOneWidget);
      expect(find.byType(CachedImage), findsOneWidget);
    });

    testWidgets('tapping a row opens the series detail screen',
        (tester) async {
      await _pump(
        tester,
        const SonarrWantedScreen(),
        (a) => a.wantedRecords = [_wantedRecord()],
      );

      await tester.tap(find.text('Example'));
      await tester.pumpAndSettle();

      expect(find.byType(SonarrSeriesDetailScreen), findsOneWidget);
    });
  });

  group('SonarrCalendarScreen', () {
    testWidgets(
        'requests embedded series, groups by day and opens detail on tap',
        (tester) async {
      final now = DateTime.now();
      final adapter = await _pump(
        tester,
        const SonarrCalendarScreen(),
        (a) => a.calendar = [
          _calendarEntry(now.toUtc(), episode: 1),
          _calendarEntry(now.add(const Duration(days: 1)).toUtc(), episode: 2),
        ],
      );

      final calendar = adapter.requests
          .where((r) => r.path.endsWith('/calendar'))
          .toList();
      expect(calendar, hasLength(1));
      expect(calendar.first.query['includeSeries'], 'true');

      expect(find.text('Today'), findsOneWidget);
      expect(find.text('Tomorrow'), findsOneWidget);
      expect(find.text('Example'), findsNWidgets(2));
      expect(find.textContaining('S01E01 • Episode 1'), findsOneWidget);

      await tester.tap(find.text('Example').first);
      await tester.pumpAndSettle();

      expect(find.byType(SonarrSeriesDetailScreen), findsOneWidget);
    });
  });
}
