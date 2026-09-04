import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/instance_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/monitoring/ui/monitoring_activity_screen.dart';
import 'package:cantinarr/features/monitoring/ui/monitoring_history_screen.dart';
import 'package:cantinarr/features/monitoring/ui/monitoring_stats_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter: answers each watch-history route from a map keyed on the
/// request path and records every request for assertions.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter(this.answers);

  final Map<String, Map<String, dynamic>> answers;
  final List<({String path, Map<String, dynamic> query})> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    final path = options.uri.path;
    requests.add((path: path, query: options.uri.queryParameters));
    final body = answers[path];
    if (body == null) {
      return ResponseBody.fromString('{"error":"not found"}', 404, headers: {
        'content-type': ['application/json'],
      });
    }
    return ResponseBody.fromString(jsonEncode(body), 200, headers: {
      'content-type': ['application/json'],
    });
  }

  @override
  void close({bool force = false}) {}
}

const _tracearr = ServiceInstance(
  id: 'tracearr-a',
  serviceType: 'tracearr',
  name: 'Tracearr',
  isDefault: true,
);
const _tautulli = ServiceInstance(
  id: 'tautulli-a',
  serviceType: 'tautulli',
  name: 'Tautulli',
  isDefault: true,
);

class _Instances extends InstanceNotifier {
  _Instances(this.instances);

  final List<ServiceInstance> instances;

  @override
  InstanceState build() => InstanceState(
        watchHistoryInstances: instances,
        activeWatchHistoryInstanceId:
            instances.isEmpty ? null : instances.first.id,
      );
}

Map<String, dynamic> _stream({
  required String title,
  String mediaType = 'movie',
  String server = '',
  String serverType = '',
}) =>
    {
      'user': 'kylo',
      'title': title,
      'full_title': title,
      'player': 'Living room',
      'product': 'Jellyfin Android TV',
      'state': 'playing',
      'progress_percent': 42,
      'quality': '1080p HEVC',
      'stream_type': 'direct play',
      'bandwidth_kbps': 12000,
      'media_type': mediaType,
      'server': server,
      'server_type': serverType,
    };

Map<String, dynamic> _activityBody(List<Map<String, dynamic>> streams) => {
      'stream_count': streams.length,
      'total_bandwidth_kbps': 12000 * streams.length,
      'streams': streams,
    };

Map<String, dynamic> _historyBody({String server = '', String note = ''}) => {
      'items': [
        {
          'user': 'kylo',
          'full_title': 'Heat',
          'date': '2026-08-30T20:00:00Z',
          'duration_seconds': 5400,
          'percent_complete': 99,
          'player': 'TV',
          'platform': 'Android',
          'media_type': 'movie',
          'server': server,
          'server_type': server.isEmpty ? '' : 'jellyfin',
        },
      ],
      'coverage': {'note': note, 'truncated': false},
    };

Map<String, dynamic> _statsBody({String note = ''}) => {
      'top_movies': [
        {'title': 'Heat', 'plays': 3},
      ],
      'top_shows': <dynamic>[],
      'top_users': [
        {'user': 'kylo', 'plays': 3},
      ],
      'coverage': {'note': note, 'truncated': false},
    };

Future<_FakeAdapter> _pump(
  WidgetTester tester,
  Widget screen, {
  required List<ServiceInstance> instances,
  required Map<String, Map<String, dynamic>> answers,
}) async {
  final adapter = _FakeAdapter(answers);
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        instanceProvider.overrideWith(() => _Instances(instances)),
        backendClientProvider.overrideWithValue(dio),
      ],
      child: MaterialApp(theme: AppTheme.dark, home: Scaffold(body: screen)),
    ),
  );
  // Screens load after the first frame; settle so the request lands. The
  // activity screen's 10-second refresh timer never keeps this spinning: it
  // only schedules a frame when it fires.
  await tester.pumpAndSettle();
  return adapter;
}

/// Disposes the activity screen so its refresh timer is cancelled before the
/// test ends.
Future<void> _teardown(WidgetTester tester) async {
  await tester.pumpWidget(const SizedBox());
}

void main() {
  const emptyCopy =
      'No Tautulli or Tracearr instance configured. Add one from Settings > Add Instance.';

  testWidgets('activity calls the neutral path for Tracearr and shows badges',
      (tester) async {
    final adapter = await _pump(
      tester,
      const MonitoringActivityScreen(),
      instances: const [_tracearr],
      answers: {
        '/api/watch-history/tracearr-a/activity': _activityBody([
          _stream(title: 'Heat', server: 'Den', serverType: 'jellyfin'),
          _stream(
              title: 'Hurt', mediaType: 'track', serverType: 'plex'),
        ]),
      },
    );

    expect(adapter.requests.map((r) => r.path),
        ['/api/watch-history/tracearr-a/activity']);
    expect(find.text('Heat'), findsOneWidget);
    // The server badge names the server; a movie gets no media badge.
    expect(find.text('Den'), findsOneWidget);
    expect(find.text('Movie'), findsNothing);
    // A track on a server that only reported its kind: kind and media badge.
    expect(find.text('Plex'), findsOneWidget);
    expect(find.text('Music'), findsOneWidget);
    await _teardown(tester);
  });

  testWidgets('activity keeps the Tautulli path for Tautulli instances',
      (tester) async {
    final adapter = await _pump(
      tester,
      const MonitoringActivityScreen(),
      instances: const [_tautulli],
      answers: {
        // A payload without the new fields: no server or media badge.
        '/api/tautulli/tautulli-a/activity': _activityBody([
          _stream(title: 'Heat'),
        ]),
      },
    );

    expect(adapter.requests.map((r) => r.path),
        ['/api/tautulli/tautulli-a/activity']);
    expect(find.text('Heat'), findsOneWidget);
    expect(find.text('Plex'), findsNothing);
    expect(find.text('Direct Play'), findsOneWidget);
    await _teardown(tester);
  });

  testWidgets('history requests 50 rows and shows the server and the note',
      (tester) async {
    final adapter = await _pump(
      tester,
      const MonitoringHistoryScreen(),
      instances: const [_tracearr],
      answers: {
        '/api/watch-history/tracearr-a/history': _historyBody(
          server: 'Den',
          note: 'The 1 most recent plays across every server Tracearr monitors.',
        ),
      },
    );

    expect(adapter.requests.single.path,
        '/api/watch-history/tracearr-a/history');
    expect(adapter.requests.single.query['limit'], '50');
    expect(find.textContaining('Den'), findsOneWidget);
    expect(find.textContaining('most recent plays'), findsOneWidget);
  });

  testWidgets('history without the new fields shows no server or note',
      (tester) async {
    await _pump(
      tester,
      const MonitoringHistoryScreen(),
      instances: const [_tautulli],
      answers: {'/api/tautulli/tautulli-a/history': _historyBody()},
    );

    expect(find.text('Heat'), findsOneWidget);
    expect(find.textContaining('kylo • 99% • Android'), findsOneWidget);
    expect(find.textContaining('most recent plays'), findsNothing);
  });

  testWidgets('stats show the coverage note and re-request on a new window',
      (tester) async {
    final adapter = await _pump(
      tester,
      const MonitoringStatsScreen(),
      instances: const [_tracearr],
      answers: {
        '/api/watch-history/tracearr-a/stats':
            _statsBody(note: 'Based on 1,240 plays since 2 Aug 2026.'),
      },
    );

    expect(adapter.requests.single.path,
        '/api/watch-history/tracearr-a/stats');
    expect(adapter.requests.single.query['days'], '30');
    expect(find.text('Based on 1,240 plays since 2 Aug 2026.'), findsOneWidget);
    expect(find.text('Heat'), findsOneWidget);

    await tester.tap(find.text('7 days'));
    await tester.pumpAndSettle();
    expect(adapter.requests.last.query['days'], '7');
  });

  testWidgets('stats without a note show no caption', (tester) async {
    await _pump(
      tester,
      const MonitoringStatsScreen(),
      instances: const [_tautulli],
      answers: {'/api/tautulli/tautulli-a/stats': _statsBody()},
    );

    expect(find.text('Heat'), findsOneWidget);
    expect(find.textContaining('Based on'), findsNothing);
  });

  testWidgets('every screen names both providers when none is configured',
      (tester) async {
    for (final screen in const <Widget>[
      MonitoringActivityScreen(),
      MonitoringHistoryScreen(),
      MonitoringStatsScreen(),
    ]) {
      final adapter = await _pump(
        tester,
        screen,
        instances: const [],
        answers: const {},
      );
      expect(find.text(emptyCopy), findsOneWidget,
          reason: '$screen must say what to add');
      expect(adapter.requests, isEmpty);
      await _teardown(tester);
    }
  });
}
