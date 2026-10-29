import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/media_card_status_provider.dart';
import 'package:cantinarr/features/discover/ui/catalog_status_builder.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final type in [MediaType.movie, MediaType.tv]) {
    for (final trigger in [
      'websocket',
      'auth-token',
      'foreground',
      'refresh',
      'account',
      'server',
      'grants',
      'default-instance',
      'policy',
      'profile-refresh',
      'config-refresh',
      '4k'
    ]) {
      testWidgets(
          '$type $trigger while status pending never renders synthetic unknown',
          (tester) async {
        final h = await _harness(tester, type);
        expect(h.adapter.pending, hasLength(1));
        expect(h.labels, everyElement(isNull));
        final old = h.adapter.pending.single;
        switch (trigger) {
          case 'websocket':
            h.events.add(const WsEvent(type: 'arr_queue_changed', data: {
              'service_type': 'lidarr',
              'instance_id': 'unrelated-music'
            }));
          case 'auth-token':
            h.auth.switchTo(_state.copyWith(
                connection:
                    _state.connection!.copyWith(accessToken: 'renewed')));
          case 'account':
            h.auth.switchTo(_state.copyWith(
                user:
                    const UserProfile(id: 2, username: 'other', role: 'user')));
          case 'server':
            h.auth.switchTo(_state.copyWith(
                connection:
                    _state.connection!.copyWith(serverUrl: 'http://other')));
          case 'grants':
            h.auth.switchTo(_state.copyWith(
                user: const UserProfile(
                    id: 1,
                    username: 'tester',
                    role: 'user',
                    permissions: ['request'])));
          case 'default-instance':
            h.auth.switchTo(_state.copyWith(
                connection: _state.connection!.copyWith(instances: [
              ServiceInstance(
                  id: 'replacement',
                  serviceType: type == MediaType.movie ? 'radarr' : 'sonarr',
                  name: 'Replacement',
                  isDefault: true),
            ])));
          case 'policy':
            h.auth.switchTo(_state.copyWith(
                user: const UserProfile(
                    id: 1,
                    username: 'tester',
                    role: 'user',
                    child: true,
                    contentLimits: ContentLimits(
                        maxMovieRating: 'PG',
                        maxTvRating: 'TV-PG',
                        ratingRegion: 'US'))));
          case 'profile-refresh':
            h.auth.switchTo(_state.copyWith(
                user: UserProfile.fromJson(_state.user!.toJson())));
          case 'config-refresh':
            h.auth.switchTo(
                _state.copyWith(connection: _state.connection!.copyWith()));
          case '4k':
            h.auth.switchTo(_state.copyWith(
                connection: _state.connection!.copyWith(cover4KBadges: true)));
          case 'foreground':
            final cache = h.container.read(mediaCardStatusCacheProvider(type));
            cache.didChangeAppLifecycleState(AppLifecycleState.inactive);
            await tester.pump();
            cache.didChangeAppLifecycleState(AppLifecycleState.resumed);
          case 'refresh':
            h.container.read(mediaCardStatusCacheProvider(type)).invalidate();
        }
        for (var n = 0; n < 6; n++) {
          await tester.pump(const Duration(milliseconds: 1));
        }
        expect(h.adapter.pending.length, greaterThanOrEqualTo(2));
        if (trigger == 'default-instance') {
          expect(h.adapter.optionsSeen.last.queryParameters['instance_id'],
              'replacement');
        }
        if (trigger == '4k') {
          expect(
              h.adapter.optionsSeen.last.queryParameters['include_4k'], isTrue);
        }
        expect(h.labels, everyElement(isNull));
        h.adapter.pending.last.complete(_body(known: true));
        for (var n = 0; n < 3; n++) {
          await tester.pump(const Duration(milliseconds: 1));
        }
        expect(h.labels, everyElement(isNull));
        old.complete({
          'status': 'available',
          'status_known': true,
          'availability': {'status': 'available', 'status_known': true}
        });
        for (var n = 0; n < 3; n++) {
          await tester.pump(const Duration(milliseconds: 1));
        }
        expect(h.labels, everyElement(isNull));
        await _dispose(tester, h);
      });
    }

    testWidgets('$type failed status reads remain unknown and can recover',
        (tester) async {
      final h = await _harness(tester, type);
      h.adapter.pending.single
          .completeError(StateError('provider unavailable'));
      await tester.pumpAndSettle();
      expect(find.text('Status unknown'), findsOneWidget);
      h.container.read(mediaCardStatusCacheProvider(type)).invalidate();
      for (var n = 0; n < 3; n++) {
        await tester.pump(const Duration(milliseconds: 1));
      }
      expect(find.text('Status unknown'), findsOneWidget);
      expect(h.adapter.pending, hasLength(2));
      h.adapter.pending.last.complete(_body(known: true));
      await tester.pumpAndSettle();
      expect(find.text('Status unknown'), findsNothing);
      await _dispose(tester, h);
    });

    testWidgets(
        '$type real unknown remains visible during refresh until known response',
        (tester) async {
      final h = await _harness(tester, type);
      h.adapter.pending.single.complete(_body(known: false));
      await tester.pumpAndSettle();
      expect(find.text('Status unknown'), findsOneWidget);
      h.container.read(mediaCardStatusCacheProvider(type)).invalidate();
      for (var n = 0; n < 3; n++) {
        await tester.pump(const Duration(milliseconds: 1));
      }
      expect(find.text('Status unknown'), findsOneWidget);
      h.adapter.pending.last.complete(_body(known: true));
      await tester.pumpAndSettle();
      expect(find.text('Status unknown'), findsNothing);
      await _dispose(tester, h);
    });
  }
}

Map<String, Object> _body({required bool known}) => {
      'status': 'unavailable',
      'status_known': true,
      'availability': {'status': 'unavailable', 'status_known': known},
    };

const _state = AuthState(
  user: UserProfile(id: 1, username: 'tester', role: 'user'),
  connection: BackendConnection(
      serverUrl: 'http://localhost',
      accessToken: 'access',
      refreshToken: 'refresh',
      services: AvailableServices(sonarr: true, radarr: true),
      tvMatchCorrections: true,
      mediaLibraryAvailability: true,
      instances: [
        ServiceInstance(
            id: 'shows', serviceType: 'sonarr', name: 'TV', isDefault: true),
        ServiceInstance(
            id: 'movies',
            serviceType: 'radarr',
            name: 'Movies',
            isDefault: true),
      ]),
);

class _Auth extends AuthNotifier {
  @override
  Future<AuthState> build() async => _state;
  void switchTo(AuthState next) => state = AsyncData(next);
}

class _Adapter implements HttpClientAdapter {
  final pending = <Completer<Map<String, Object>>>[];
  final optionsSeen = <RequestOptions>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? stream,
      Future<void>? cancelFuture) async {
    final response = Completer<Map<String, Object>>();
    pending.add(response);
    optionsSeen.add(options);
    return ResponseBody.fromString(jsonEncode(await response.future), 200,
        headers: {
          'content-type': ['application/json']
        });
  }

  @override
  void close({bool force = false}) {}
}

typedef _Harness = ({
  ProviderContainer container,
  _Auth auth,
  _Adapter adapter,
  StreamController<WsEvent> events,
  List<String?> labels
});

Future<_Harness> _harness(WidgetTester tester, MediaType type) async {
  tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  final adapter = _Adapter();
  final auth = _Auth();
  final events = StreamController<WsEvent>.broadcast();
  final labels = <String?>[];
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => auth),
    backendClientProvider.overrideWithValue(
        Dio(BaseOptions(baseUrl: 'http://localhost'))
          ..httpClientAdapter = adapter),
    libraryChangedEventsProvider.overrideWith((_) => events.stream),
  ]);
  await container.read(authProvider.future);
  await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
          home: Scaffold(
        body: CatalogStatusBuilder(
            item: MediaItem(id: 42, title: 'Unowned title', mediaType: type),
            builder: (status) {
              labels.add(status?.label);
              return Text(status?.label ?? 'No badge');
            }),
      ))));
  await tester.pumpAndSettle();
  return (
    container: container,
    auth: auth,
    adapter: adapter,
    events: events,
    labels: labels
  );
}

Future<void> _dispose(WidgetTester tester, _Harness h) async {
  await tester.pumpWidget(const SizedBox.shrink());
  h.container.dispose();
  await h.events.close();
  for (final pending in h.adapter.pending) {
    if (!pending.isCompleted) pending.complete(_body(known: true));
  }
  await tester.pump();
}
