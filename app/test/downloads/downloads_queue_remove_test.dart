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

/// Fake Dio adapter: serves the queue with a single item and records every
/// request (method, path, query) so the tests can assert the DELETE and its
/// deleteData flag.
class _FakeAdapter implements HttpClientAdapter {
  final List<({String method, String path, Map<String, dynamic> query})>
      requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add((
      method: options.method,
      path: options.uri.path,
      query: options.uri.queryParameters,
    ));
    final Map<String, dynamic> body;
    if (options.method == 'GET' && options.uri.path.endsWith('/queue')) {
      body = {
        'paused': false,
        'speed_bps': 0,
        'items': [
          {
            'id': 'item-1',
            'name': 'Example Download',
            'size_bytes': 1000,
            'size_left_bytes': 500,
            'progress': 50.0,
            'status': 'downloading',
          },
        ],
      };
    } else {
      body = <String, dynamic>{};
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

class _FakeAuthNotifier extends AuthNotifier {
  final AuthState authState;

  _FakeAuthNotifier(this.authState);

  @override
  Future<AuthState> build() async => authState;
}

AuthState _stateWithDownloadClient(String serviceType) => AuthState(
      connection: BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: 'access',
        refreshToken: 'refresh',
        instances: [
          ServiceInstance(
            id: 'dl-1',
            serviceType: serviceType,
            name: 'Download Client',
            isDefault: true,
          ),
        ],
      ),
      user: const UserProfile(id: 1, username: 'admin', role: 'admin'),
    );

const _nzbgetHint =
    'NZBGet removes the queue item only; downloaded files stay on disk.';

void main() {
  late _FakeAdapter adapter;

  Future<void> pumpQueue(WidgetTester tester, String serviceType) async {
    adapter = _FakeAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          authProvider.overrideWith(
            () => _FakeAuthNotifier(_stateWithDownloadClient(serviceType)),
          ),
          backendClientProvider.overrideWithValue(dio),
          realtimeEventsProvider
              .overrideWithValue(const Stream<WsEvent>.empty()),
        ],
        child: const MaterialApp(
          home: Scaffold(body: DownloadsQueueScreen()),
        ),
      ),
    );
    await tester.pumpAndSettle();
    expect(find.text('Example Download'), findsOneWidget);
  }

  Future<void> openRemoveDialog(WidgetTester tester) async {
    await tester.tap(find.byIcon(Icons.more_vert));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Remove'));
    await tester.pumpAndSettle();
    expect(find.text('Remove Download'), findsOneWidget);
  }

  List<({String method, String path, Map<String, dynamic> query})> deletes() =>
      adapter.requests.where((r) => r.method == 'DELETE').toList();

  for (final serviceType in ['sabnzbd', 'qbittorrent', 'transmission']) {
    testWidgets('$serviceType remove dialog offers the delete-data option',
        (tester) async {
      await pumpQueue(tester, serviceType);
      await openRemoveDialog(tester);
      expect(find.text('Also delete downloaded data'), findsOneWidget);
      expect(find.text(_nzbgetHint), findsNothing);
      final box =
          tester.widget<CheckboxListTile>(find.byType(CheckboxListTile));
      expect(box.value, isFalse);
    });
  }

  testWidgets('sabnzbd opting in sends deleteData=true', (tester) async {
    await pumpQueue(tester, 'sabnzbd');
    await openRemoveDialog(tester);
    await tester.tap(find.text('Also delete downloaded data'));
    await tester.pump();
    await tester.tap(find.text('Remove').last);
    await tester.pumpAndSettle();

    final d = deletes();
    expect(d, hasLength(1));
    expect(d.single.path, endsWith('/queue/item-1'));
    expect(d.single.query['deleteData'], 'true');
  });

  testWidgets('nzbget remove dialog hides the option and explains why',
      (tester) async {
    await pumpQueue(tester, 'nzbget');
    await openRemoveDialog(tester);
    expect(find.byType(CheckboxListTile), findsNothing);
    expect(find.text('Also delete downloaded data'), findsNothing);
    expect(find.text(_nzbgetHint), findsOneWidget);
  });

  testWidgets('nzbget remove never requests data deletion', (tester) async {
    await pumpQueue(tester, 'nzbget');
    await openRemoveDialog(tester);
    await tester.tap(find.text('Remove').last);
    await tester.pumpAndSettle();

    final d = deletes();
    expect(d, hasLength(1));
    expect(d.single.path, endsWith('/queue/item-1'));
    expect(d.single.query['deleteData'], 'false');
  });
}
