import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/downloads/ui/downloads_queue_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter serving one queue per download client instance and
/// recording every request, so the tests can assert the aggregate view's
/// fan-out (which instance each read and action went to).
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter({this.failing = const {}, this.allPaused = false});

  /// Instance ids whose queue reads fail with a 500.
  final Set<String> failing;

  /// Serve every queue as paused (for the master-resume path).
  final bool allPaused;

  final List<({String method, String path})> requests = [];

  static Map<String, dynamic> _item(String id, String name) => {
        'id': id,
        'name': name,
        'size_bytes': 1000,
        'size_left_bytes': 500,
        'progress': 50.0,
        'status': 'downloading',
      };

  Map<String, dynamic> _queueFor(String instanceId) {
    final items = switch (instanceId) {
      'sabnzbd-a' => [_item('item-sab', 'SAB Item')],
      'qbittorrent-a' => [_item('item-qb1', 'Torrent One')],
      'qbittorrent-b' => [_item('item-qb2', 'Torrent Two')],
      _ => <Map<String, dynamic>>[],
    };
    return {
      'paused': allPaused,
      'speed_bps': allPaused ? 0 : (instanceId == 'sabnzbd-a' ? 1024 : 512),
      'items': items,
    };
  }

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add((method: options.method, path: options.uri.path));
    final segments = options.uri.pathSegments;
    // /api/downloads/{instanceId}/queue
    if (options.method == 'GET' &&
        segments.length == 4 &&
        segments[1] == 'downloads' &&
        segments[3] == 'queue') {
      final instanceId = segments[2];
      if (failing.contains(instanceId)) {
        return ResponseBody.fromString(
          jsonEncode({'error': 'boom'}),
          500,
          headers: {
            'content-type': ['application/json'],
          },
        );
      }
      return ResponseBody.fromString(
        jsonEncode(_queueFor(instanceId)),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    return ResponseBody.fromString(
      jsonEncode(<String, dynamic>{}),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

/// Three clients: one usenet, two torrent. The torrent client carries the
/// stored default flag to prove the aggregate view still wins as default.
const _authState = AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost',
    accessToken: 'access',
    refreshToken: 'refresh',
    instances: [
      ServiceInstance(
        id: 'qbittorrent-a',
        serviceType: 'qbittorrent',
        name: 'qBit',
        isDefault: true,
      ),
      ServiceInstance(
        id: 'qbittorrent-b',
        serviceType: 'qbittorrent',
        name: 'qBit (Yana)',
      ),
      ServiceInstance(
        id: 'sabnzbd-a',
        serviceType: 'sabnzbd',
        name: 'SAB',
      ),
    ],
  ),
  user: UserProfile(id: 1, username: 'admin', role: 'admin'),
);

void main() {
  late _FakeAdapter adapter;

  Future<void> pumpAllView(
    WidgetTester tester, {
    Set<String> failing = const {},
    bool allPaused = false,
    Stream<WsEvent>? events,
  }) async {
    adapter = _FakeAdapter(failing: failing, allPaused: allPaused);
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _FakeAuthNotifier(_authState)),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider
              .overrideWithValue(events ?? const Stream<WsEvent>.empty()),
        ],
        child: const MaterialApp(
          home: Scaffold(body: DownloadsQueueScreen()),
        ),
      ),
    );
    await tester.pumpAndSettle();
  }

  List<String> posts() => [
        for (final r in adapter.requests)
          if (r.method == 'POST') r.path,
      ];

  testWidgets('defaults to the aggregate view: every client queued together',
      (tester) async {
    await pumpAllView(tester);

    // All three clients' items render without any selection being made,
    // even though a torrent client carries the stored default flag.
    expect(find.text('SAB Item'), findsOneWidget);
    expect(find.text('Torrent One'), findsOneWidget);
    expect(find.text('Torrent Two'), findsOneWidget);

    // Usenet items are listed above torrent items.
    expect(
      tester.getTopLeft(find.text('SAB Item')).dy,
      lessThan(tester.getTopLeft(find.text('Torrent One')).dy),
    );

    // Each item names its client, and the header sums the speeds.
    expect(find.text('SAB'), findsOneWidget);
    expect(find.text('qBit'), findsOneWidget);
    expect(find.text('qBit (Yana)'), findsOneWidget);
    expect(find.text('2.0 KB/s • 3 items'), findsOneWidget);
  });

  testWidgets('master pause hits every client', (tester) async {
    await pumpAllView(tester);

    await tester.tap(find.text('Pause all'));
    await tester.pumpAndSettle();

    expect(
      posts(),
      containsAll([
        '/api/downloads/sabnzbd-a/pause',
        '/api/downloads/qbittorrent-a/pause',
        '/api/downloads/qbittorrent-b/pause',
      ]),
    );
  });

  testWidgets('all clients paused reads as such and resumes every client',
      (tester) async {
    await pumpAllView(tester, allPaused: true);

    expect(find.text('All queues paused'), findsOneWidget);

    await tester.tap(find.text('Resume all'));
    await tester.pumpAndSettle();

    expect(
      posts(),
      containsAll([
        '/api/downloads/sabnzbd-a/resume',
        '/api/downloads/qbittorrent-a/resume',
        '/api/downloads/qbittorrent-b/resume',
      ]),
    );
  });

  testWidgets('per-item actions go only to the owning client', (tester) async {
    await pumpAllView(tester);

    final card = find
        .ancestor(of: find.text('Torrent Two'), matching: find.byType(Container))
        .first;
    await tester.tap(
        find.descendant(of: card, matching: find.byIcon(Icons.more_vert)));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Pause'));
    await tester.pumpAndSettle();

    expect(posts(), ['/api/downloads/qbittorrent-b/queue/item-qb2/pause']);
  });

  testWidgets('a failing client is named while the rest still render',
      (tester) async {
    await pumpAllView(tester, failing: {'qbittorrent-b'});

    expect(find.text('SAB Item'), findsOneWidget);
    expect(find.text('Torrent One'), findsOneWidget);
    expect(find.text('Torrent Two'), findsNothing);
    expect(find.text('Not responding: qBit (Yana)'), findsOneWidget);
  });

  testWidgets('a WebSocket snapshot updates only its own client',
      (tester) async {
    final controller = StreamController<WsEvent>.broadcast();
    addTearDown(controller.close);
    await pumpAllView(tester, events: controller.stream);

    controller.add(const WsEvent(type: 'downloads_queue', data: {
      'instance_id': 'qbittorrent-a',
      'paused': false,
      'speed_bps': 256,
      'items': [
        {
          'id': 'item-qb1b',
          'name': 'Torrent Reborn',
          'size_bytes': 1000,
          'size_left_bytes': 100,
          'progress': 90.0,
          'status': 'downloading',
        },
      ],
    }));
    await tester.pumpAndSettle();

    expect(find.text('Torrent Reborn'), findsOneWidget);
    expect(find.text('Torrent One'), findsNothing);
    // The other clients' items are untouched.
    expect(find.text('SAB Item'), findsOneWidget);
    expect(find.text('Torrent Two'), findsOneWidget);
  });
}
