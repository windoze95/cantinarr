import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/features/radarr/data/radarr_calendar.dart';
import 'package:cantinarr/features/radarr/data/radarr_models.dart';
import 'package:cantinarr/features/radarr/ui/radarr_calendar_screen.dart';
import 'package:cantinarr/features/radarr/ui/radarr_movie_detail_screen.dart';
import 'package:cantinarr/features/radarr/ui/radarr_wanted_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter: serves the configured movies from the wanted and calendar
/// endpoints and records every request for assertions.
class _FakeAdapter implements HttpClientAdapter {
  final List<({String method, String path, Map<String, dynamic> query})>
      requests = [];

  /// Movies returned by /wanted/missing|cutoff, /calendar and /movie/{id}.
  List<Map<String, dynamic>> movies = [];

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
          'totalRecords': movies.length,
          'records': movies,
        };
      } else if (path.endsWith('/calendar')) {
        response = movies;
      } else if (path.endsWith('/history/movie')) {
        response = <dynamic>[];
      } else if (path.endsWith('/queue')) {
        response = {'records': <dynamic>[]};
      } else if (path.contains('/movie/')) {
        final id = int.tryParse(path.split('/').last);
        response = movies.firstWhere((m) => m['id'] == id,
            orElse: () => movies.first);
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

class _RadarrInstances extends InstanceNotifier {
  @override
  InstanceState build() => const InstanceState(
        radarrInstances: [
          ServiceInstance(id: 'radarr-1', serviceType: 'radarr', name: 'Radarr'),
        ],
      );
}

Map<String, dynamic> _movie({
  required int id,
  required String title,
  String? inCinemas,
  String? digitalRelease,
  String? physicalRelease,
  bool hasFile = false,
}) =>
    {
      'id': id,
      'title': title,
      'year': 2026,
      'monitored': true,
      'hasFile': hasFile,
      'images': <Map<String, dynamic>>[],
      if (inCinemas != null) 'inCinemas': inCinemas,
      if (digitalRelease != null) 'digitalRelease': digitalRelease,
      if (physicalRelease != null) 'physicalRelease': physicalRelease,
    };

RadarrMovie _parsed(Map<String, dynamic> json) => RadarrMovie.fromJson(json);

/// A midnight-UTC calendar date string for the local calendar day of [d].
String _utcDate(DateTime d) =>
    '${d.year.toString().padLeft(4, '0')}-'
    '${d.month.toString().padLeft(2, '0')}-'
    '${d.day.toString().padLeft(2, '0')}T00:00:00Z';

Future<_FakeAdapter> _pump(
  WidgetTester tester,
  Widget screen,
  List<Map<String, dynamic>> movies,
) async {
  tester.view.physicalSize = const Size(800, 1400);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });

  final adapter = _FakeAdapter()..movies = movies;
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        backendClientProvider.overrideWithValue(dio),
        instanceProvider.overrideWith(_RadarrInstances.new),
      ],
      child: MaterialApp(home: Scaffold(body: screen)),
    ),
  );
  await tester.pumpAndSettle();
  return adapter;
}

void main() {
  group('radarrCalendarReleases', () {
    test('one labelled row per release date inside the window', () {
      final rows = radarrCalendarReleases(
        [
          _parsed(_movie(
              id: 1,
              title: 'Both',
              inCinemas: '2026-07-10T00:00:00Z',
              digitalRelease: '2026-07-24T00:00:00Z')),
        ],
        start: DateTime(2026, 7, 5),
        end: DateTime(2026, 7, 30),
      );

      expect(rows, hasLength(2));
      expect(rows[0].label, 'In cinemas');
      expect(rows[0].date, DateTime(2026, 7, 10));
      expect(rows[1].label, 'Digital');
      expect(rows[1].date, DateTime(2026, 7, 24));
    });

    test('drops dates outside the window when another is inside', () {
      final rows = radarrCalendarReleases(
        [
          _parsed(_movie(
              id: 1,
              title: 'Late digital',
              inCinemas: '2026-01-10T00:00:00Z',
              digitalRelease: '2026-07-24T00:00:00Z')),
        ],
        start: DateTime(2026, 7, 5),
        end: DateTime(2026, 7, 30),
      );

      expect(rows.map((r) => r.label).toList(), ['Digital']);
    });

    test('a movie with no date in the window keeps its soonest upcoming one',
        () {
      final rows = radarrCalendarReleases(
        [
          _parsed(_movie(
              id: 1,
              title: 'Edge',
              inCinemas: '2026-06-01T00:00:00Z',
              digitalRelease: '2026-08-15T00:00:00Z')),
        ],
        start: DateTime(2026, 7, 5),
        end: DateTime(2026, 7, 30),
        now: DateTime(2026, 7, 10),
      );

      expect(rows.single.label, 'Digital');
      expect(rows.single.date, DateTime(2026, 8, 15));
    });

    test('falls back to the most recent past date when nothing is upcoming',
        () {
      final rows = radarrCalendarReleases(
        [
          _parsed(_movie(
              id: 1,
              title: 'Old',
              inCinemas: '2026-05-01T00:00:00Z',
              physicalRelease: '2026-06-20T00:00:00Z')),
        ],
        start: DateTime(2026, 7, 5),
        end: DateTime(2026, 7, 30),
        now: DateTime(2026, 7, 10),
      );

      expect(rows.single.label, 'Physical');
      expect(rows.single.date, DateTime(2026, 6, 20));
    });

    test('midnight-UTC dates keep their calendar day in any time zone', () {
      final rows = radarrCalendarReleases(
        [_parsed(_movie(id: 1, title: 'M', digitalRelease: '2026-08-07T00:00:00Z'))],
        start: DateTime(2026, 8, 1),
        end: DateTime(2026, 8, 31),
      );

      expect(rows.single.date, DateTime(2026, 8, 7));
    });

    test('rows are date ascending across movies; dateless movies are skipped',
        () {
      final rows = radarrCalendarReleases(
        [
          _parsed(_movie(id: 1, title: 'B', digitalRelease: '2026-07-20T00:00:00Z')),
          _parsed(_movie(id: 2, title: 'A', inCinemas: '2026-07-08T00:00:00Z')),
          _parsed(_movie(id: 3, title: 'None')),
        ],
        start: DateTime(2026, 7, 5),
        end: DateTime(2026, 7, 30),
      );

      expect(rows.map((r) => r.movie.title).toList(), ['A', 'B']);
    });
  });

  group('RadarrWantedScreen', () {
    testWidgets('rows carry a poster slot and a typed release label',
        (tester) async {
      await _pump(
        tester,
        const RadarrWantedScreen(),
        [
          _movie(
              id: 5,
              title: 'Example Movie',
              digitalRelease: '2026-08-07T00:00:00Z'),
        ],
      );

      expect(find.text('Example Movie (2026)'), findsOneWidget);
      expect(find.textContaining('Digital Aug 7, 2026'), findsOneWidget);
      expect(find.byType(CachedImage), findsOneWidget);
    });

    testWidgets('tapping a row opens the movie detail screen', (tester) async {
      await _pump(
        tester,
        const RadarrWantedScreen(),
        [
          _movie(
              id: 5,
              title: 'Example Movie',
              digitalRelease: '2026-08-07T00:00:00Z'),
        ],
      );

      await tester.tap(find.text('Example Movie (2026)'));
      await tester.pumpAndSettle();

      expect(find.byType(RadarrMovieDetailScreen), findsOneWidget);
    });
  });

  group('RadarrCalendarScreen', () {
    testWidgets(
        'groups releases by day with typed labels and opens detail on tap',
        (tester) async {
      final now = DateTime.now();
      await _pump(
        tester,
        const RadarrCalendarScreen(),
        [
          _movie(id: 5, title: 'Movie A', digitalRelease: _utcDate(now)),
          _movie(
              id: 6,
              title: 'Movie B',
              inCinemas: _utcDate(now.add(const Duration(days: 1)))),
        ],
      );

      expect(find.text('Today'), findsOneWidget);
      expect(find.text('Tomorrow'), findsOneWidget);
      expect(find.text('Movie A (2026)'), findsOneWidget);
      expect(find.text('Digital'), findsOneWidget);
      expect(find.text('In cinemas'), findsOneWidget);

      await tester.tap(find.text('Movie A (2026)'));
      await tester.pumpAndSettle();

      expect(find.byType(RadarrMovieDetailScreen), findsOneWidget);
    });
  });
}
