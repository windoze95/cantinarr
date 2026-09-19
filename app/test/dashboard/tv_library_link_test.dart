import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/dashboard/ui/tv_library_link.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

void main() {
  test('TV library navigation capability is opt-in and survives connection copies', () {
    expect(ServerConfig.fromJson({}).tvLibraryNavigation, isFalse);
    expect(ServerConfig.fromJson({'tv_library_navigation': true}).tvLibraryNavigation, isTrue);
    const connection = BackendConnection(serverUrl: 'http://fixture',
        accessToken: 'test', refreshToken: 'test', tvLibraryNavigation: true);
    expect(connection.copyWith().tvLibraryNavigation, isTrue);
    expect(connection.copyWith(tvLibraryNavigation: false).tvLibraryNavigation, isFalse);
  });
  for (final parentId in [0, 113988]) {
    testWidgets('combined parent $parentId chooses a mapped story and retains library', (tester) async {
      final adapter = _Adapter(titles: [
        {'tmdb_id': 113988, 'title': 'Dahmer'},
        {'tmdb_id': 225634, 'title': 'Menendez'},
      ]);
      final (:router, :container) = await _pump(tester, adapter, tmdbId: parentId);
      await tester.tap(find.text('Monster (2022)'));
      await tester.pumpAndSettle();
      expect(find.text('Choose a title'), findsOneWidget);
      expect(router.routeInformationProvider.value.uri.path, '/dashboard/tv');
      expect(adapter.requests.single.queryParameters,
          {'instance_id': 'tv-other', 'series_id': 42});
      await tester.tap(find.text('Menendez'));
      await tester.pumpAndSettle();
      expect(find.text('/detail/tv/225634?instance_id=tv-other'), findsOneWidget);
      router.pop();
      await tester.pumpAndSettle();
      expect(find.text('Monster (2022)'), findsOneWidget);
      // Every tap re-resolves; a later local correction is not cached here.
      adapter.titles = [{'tmdb_id': 286801, 'title': 'Ed Gein'}];
      await tester.tap(find.text('Monster (2022)'));
      await tester.pumpAndSettle();
      expect(find.text('/detail/tv/286801?instance_id=tv-other'), findsOneWidget);
      expect(adapter.requests.length, 2);
    });
  }

  testWidgets('calendar season opens only its mapped title', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter, season: 2);
    await tester.tap(find.text('Monster (2022)'));
    await tester.pumpAndSettle();
    expect(adapter.requests.single.queryParameters['season_number'], 2);
    expect(find.text('Choose a title'), findsNothing);
    expect(find.text('/detail/tv/225634?instance_id=tv-other'), findsOneWidget);
  });

  for (final status in [403, 404, 409, 503]) {
    testWidgets('resolution $status stays on TV and can retry', (tester) async {
      final adapter = _Adapter(status: status);
      final (:router, :container) = await _pump(tester, adapter);
      await tester.tap(find.text('Monster (2022)'));
      await tester.pumpAndSettle();
      expect(router.routeInformationProvider.value.uri.path, '/dashboard/tv');
      expect(find.textContaining('Could not resolve this TV title.'), findsOneWidget);
      adapter.status = 200;
      await tester.tap(find.text('Monster (2022)'));
      await tester.pumpAndSettle();
      expect(find.text('/detail/tv/225634?instance_id=tv-other'), findsOneWidget);
    });
  }

  testWidgets('invalid destination never pushes an invalid route', (tester) async {
    final adapter = _Adapter(titles: [{'tmdb_id': 0, 'title': 'Bad identity'}]);
    final (:router, :container) = await _pump(tester, adapter);
    await tester.tap(find.text('Monster (2022)'));
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/dashboard/tv');
    expect(find.textContaining('Could not resolve this TV title.'), findsOneWidget);
  });

  for (final parentId in [0, 456]) {
    testWidgets('old server handles parent $parentId without unsupported request', (tester) async {
      final adapter = _Adapter();
      await _pump(tester, adapter, supported: false, tmdbId: parentId);
      final button = tester.widget<TextButton>(find.byType(TextButton));
      if (parentId == 0) {
        expect(button.onPressed, isNull);
      } else {
        await tester.tap(find.text('Monster (2022)'));
        await tester.pumpAndSettle();
        expect(find.text('/detail/tv/456?instance_id=tv-other'), findsOneWidget);
      }
      expect(adapter.requests, isEmpty);
    });
  }

  testWidgets('late response after navigation does not open a detail', (tester) async {
    final gate = Completer<void>();
    final adapter = _Adapter(gate: gate);
    final (:router, :container) = await _pump(tester, adapter);
    await tester.tap(find.text('Monster (2022)'));
    await tester.pump();
    router.go('/away');
    await tester.pumpAndSettle();
    gate.complete();
    await tester.pumpAndSettle();
    expect(find.text('Away'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('late response after account change is discarded', (tester) async {
    final gate = Completer<void>();
    final adapter = _Adapter(gate: gate);
    final (:router, :container) = await _pump(tester, adapter);
    await tester.tap(find.text('Monster (2022)'));
    await tester.pump();
    (container.read(authProvider.notifier) as _Auth).clear();
    gate.complete();
    await tester.pumpAndSettle();
    expect(router.routeInformationProvider.value.uri.path, '/dashboard/tv');
    expect(find.text('Choose a title'), findsNothing);
  });
}

Future<({GoRouter router, ProviderContainer container})> _pump(
  WidgetTester tester, _Adapter adapter, {
  int tmdbId = 0, int? season, bool supported = true,
}) async {
  final dio = Dio(BaseOptions(baseUrl: 'http://fixture'))..httpClientAdapter = adapter;
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _Auth(supported)),
    backendClientProvider.overrideWithValue(dio),
  ]);
  final router = GoRouter(initialLocation: '/dashboard/tv', routes: [
    GoRoute(path: '/dashboard/tv', builder: (_, __) => Scaffold(body:
      TVLibraryLink(instanceId: 'tv-other', seriesId: 42,
        tmdbId: tmdbId, seasonNumber: season,
        builder: (tap) => TextButton(onPressed: tap, child: const Text('Monster (2022)')),
      ))),
    GoRoute(path: '/detail/tv/:id', builder: (_, state) => Scaffold(body: Text(state.uri.toString()))),
    GoRoute(path: '/away', builder: (_, __) => const Scaffold(body: Text('Away'))),
  ]);
  addTearDown(router.dispose);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  await tester.pumpWidget(UncontrolledProviderScope(container: container,
    child: MaterialApp.router(routerConfig: router)));
  await tester.pumpAndSettle();
  return (router: router, container: container);
}

class _Auth extends AuthNotifier {
  _Auth(this.supported);
  final bool supported;
  @override
  Future<AuthState> build() async => AuthState(
    connection: BackendConnection(serverUrl: 'http://fixture', accessToken: 'test',
      refreshToken: 'test', tvLibraryNavigation: supported),
    user: const UserProfile(id: 1, username: 'tester', role: 'user'),
  );
  void clear() => state = const AsyncData(AuthState());
}

class _Adapter implements HttpClientAdapter {
  _Adapter({this.status = 200, this.gate, List<Map<String, dynamic>>? titles})
      : titles = titles ?? [{'tmdb_id': 225634, 'title': 'Menendez'}];
  int status;
  final Completer<void>? gate;
  List<Map<String, dynamic>> titles;
  final requests = <RequestOptions>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    requests.add(options);
    expect(options.path, '/api/requests/tv-library-titles');
    await gate?.future;
    return ResponseBody.fromString(jsonEncode({'instance_id': 'tv-other', 'titles': titles}),
      status, headers: {Headers.contentTypeHeader: [Headers.jsonContentType]});
  }
  @override
  void close({bool force = false}) {}
}
