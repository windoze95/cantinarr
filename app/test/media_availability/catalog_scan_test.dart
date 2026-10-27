import 'dart:async';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/ui/catalog_status_builder.dart';
import 'package:cantinarr/features/media_availability/ui/availability_scan_section.dart';
import 'package:cantinarr/features/settings/data/instance_api_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final type in [MediaType.movie, MediaType.tv]) {
    testWidgets('$type cards use server coverage and refresh after scan events',
        (tester) async {
      final events = StreamController<WsEvent>.broadcast();
      addTearDown(events.close);
      var available = true;
      var reads = 0;
      final dio = Dio()
        ..interceptors.add(InterceptorsWrapper(onRequest: (o, h) {
          reads++;
          expect(o.queryParameters['media_type'], type.name);
          h.resolve(Response(
              requestOptions: o,
              data: {
                'status': 'unavailable',
                'availability': {
                  'status': available ? 'available' : 'unavailable',
                  'status_known': true
                }
              },
              statusCode: 200));
        }));
      final container = ProviderContainer(overrides: [
        authProvider.overrideWith(() => _Auth(true)),
        backendClientProvider.overrideWithValue(dio),
        realtimeEventsProvider.overrideWithValue(events.stream),
      ]);
      addTearDown(container.dispose);
      await container.read(authProvider.future);
      await tester.pumpWidget(UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
              home: Scaffold(
                  body: CatalogStatusBuilder(
                      item: MediaItem(
                          id: 23, title: 'Catalog title', mediaType: type),
                      builder: (status) =>
                          Text(status?.label ?? 'No badge'))))));
      await tester.pumpAndSettle();
      expect(find.text('Available'), findsOneWidget);
      final oldReads = reads;
      available = false;
      events.add(const WsEvent(type: 'media_availability_changed', data: {}));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 20));
      await tester.pumpAndSettle();
      expect(reads, greaterThan(oldReads));
      expect(find.text('No badge'), findsOneWidget);
      await tester.pumpWidget(const SizedBox.shrink());
    });
  }

  testWidgets('older server does not call new movie availability paths',
      (tester) async {
    var reads = 0;
    final dio = Dio()
      ..interceptors.add(InterceptorsWrapper(onRequest: (o, h) {
        reads++;
        h.resolve(Response(requestOptions: o, data: {}, statusCode: 200));
      }));
    await tester.pumpWidget(ProviderScope(
        overrides: [
          authProvider.overrideWith(() => _Auth(false)),
          backendClientProvider.overrideWithValue(dio)
        ],
        child: MaterialApp(
            home: Scaffold(
                body: CatalogStatusBuilder(
                    item: const MediaItem(
                        id: 2, title: 'Title', mediaType: MediaType.movie),
                    builder: (s) => Text(s?.label ?? 'No badge'))))));
    await tester.pumpAndSettle();
    expect(reads, 0);
    expect(find.text('No badge'), findsOneWidget);
  });

  testWidgets('scan is opt-in and drafts cannot start a scan', (tester) async {
    var value = const AvailabilityScanConfig();
    var dirty = false;
    final posts = <String>[];
    final dio = Dio()
      ..interceptors.add(InterceptorsWrapper(onRequest: (o, h) {
        if (o.method == 'POST') posts.add(o.path);
        h.resolve(Response(
            requestOptions: o,
            statusCode: 200,
            data: o.path.endsWith('/libraries')
                ? {
                    'libraries': [
                      {
                        'id': 'movies',
                        'name': 'Films',
                        'collection_type': 'movies'
                      },
                      {
                        'id': 'music',
                        'name': 'Music',
                        'collection_type': 'music'
                      },
                    ]
                  }
                : {'state': 'disabled'}));
      }));
    await tester.pumpWidget(ProviderScope(
        overrides: [backendClientProvider.overrideWithValue(dio)],
        child: MaterialApp(
            home: Scaffold(
                body: StatefulBuilder(
                    builder: (context, setState) => SingleChildScrollView(
                        child: AvailabilityScanSection(
                            instanceId: 'p1',
                            value: value,
                            unsaved: dirty,
                            onChanged: (next) => setState(() {
                                  value = next;
                                  dirty = true;
                                }))))))));
    await tester.pumpAndSettle();
    expect(value.enabled, isFalse);
    await tester.tap(find.byType(SwitchListTile));
    await tester.pumpAndSettle();
    expect(find.text('Films'), findsOneWidget);
    expect(find.text('Music'), findsNothing);
    await tester.tap(find.text('Films'));
    await tester.pumpAndSettle();
    expect(value.libraryIds, ['movies']);
    expect(
        tester
            .widget<OutlinedButton>(
                find.widgetWithText(OutlinedButton, 'Scan now'))
            .onPressed,
        isNull);
    expect(posts, isEmpty);
  });
}

class _Auth extends AuthNotifier {
  final bool capable;
  _Auth(this.capable);
  @override
  Future<AuthState> build() async => AuthState(
      user: const UserProfile(id: 2, username: 'viewer', role: 'user'),
      connection: BackendConnection(
          serverUrl: 'http://localhost',
          accessToken: 'test',
          refreshToken: 'test',
          mediaLibraryAvailability: capable,
          instances: const [
            ServiceInstance(
                id: 'radarr',
                name: 'Movies',
                serviceType: 'radarr',
                isDefault: true),
            ServiceInstance(
                id: 'sonarr',
                name: 'TV',
                serviceType: 'sonarr',
                isDefault: true)
          ]));
}
