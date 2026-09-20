import 'dart:async';

import 'package:cantinarr/core/models/app_module.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/module_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/downloads/data/downloads_activity.dart';
import 'package:cantinarr/features/downloads/logic/downloads_activity_provider.dart';
import 'package:cantinarr/features/downloads/ui/downloads_content_screen.dart';
import 'package:cantinarr/features/downloads/ui/downloads_menu_badge.dart';
import 'package:cantinarr/features/downloads/ui/downloads_module_shell.dart';
import 'package:cantinarr/features/downloads/ui/downloads_queue_page.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

DownloadsActivity sample({int? count = 2, bool complete = true}) => DownloadsActivity.fromJson({
  'count': count, 'complete': complete, 'scope': 'all', 'user_scope': 'all',
  'jobs': [
    {'id': 'pack', 'status': 'paused', 'size_bytes': 1000, 'size_left_bytes': 500,
      'progress': 50, 'control': {'instance_id': 'nzb', 'item_id': '42',
        'service_type': 'nzbget', 'client_name': 'NZBGet'}},
    {'id': 'movie-job', 'status': 'downloading', 'size_bytes': 1000,
      'size_left_bytes': 250, 'progress': 75},
  ],
  'groups': [
    {'id': 'show', 'media_type': 'tv', 'title': 'A show with a season pack',
      'instance_name': 'Family TV', 'job_ids': ['pack'], 'progress': 50,
      'details_known': true, 'children': [
        {'id': 'e1', 'season': 1, 'episode': 1, 'title': 'A beginning', 'job_ids': ['pack']},
        {'id': 'e2', 'season': 1, 'episode': 2, 'title': 'The next day', 'job_ids': ['pack']},
      ]},
    {'id': 'movie', 'media_type': 'movie', 'title': 'An actual movie title',
      'year': 2026, 'instance_name': 'Movies', 'job_ids': ['movie-job'],
      'progress': 75, 'details_known': true, 'children': []},
  ],
});

AuthState session({bool admin = false, bool supported = true, String scope = 'all',
    int user = 2, String server = 'http://server.test'}) => AuthState(
  user: UserProfile(id: user, username: 'viewer', role: admin ? 'admin' : 'user'),
  connection: BackendConnection(serverUrl: server, accessToken: 'test', refreshToken: 'test',
      downloadsActivity: supported, downloadsUserScope: scope),
);

class TestAuth extends AuthNotifier {
  final AuthState initial;
  TestAuth(this.initial);
  @override
  Future<AuthState> build() async => initial;
  void replace(AuthState next) => state = AsyncData(next);
}

void main() {
  setUp(() => SharedPreferences.setMockInitialValues({}));

  for (final width in [390.0, 1100.0]) {
    testWidgets('Content groups and shared progress fit width $width', (tester) async {
      tester.view.physicalSize = Size(width, 844);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      final actions = <String>[];
      await tester.pumpWidget(MaterialApp(theme: AppTheme.dark,
        debugShowCheckedModeBanner: false,
        home: Scaffold(body: RepaintBoundary(key: const Key('content-golden'),
          child: DownloadsActivityView(activity: sample(), admin: true, onRefresh: () {},
            onAction: (job, action, title) => actions.add('${job.control?.itemId}:$action'))))));
      await tester.tap(find.text('A show with a season pack'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Season 1'));
      await tester.pumpAndSettle();
      expect(find.textContaining('E01 · A beginning'), findsOneWidget);
      expect(find.textContaining('E02 · The next day'), findsOneWidget);
      expect(find.text('1 job · 50.0%'), findsOneWidget);
      expect(tester.takeException(), isNull);
      // Capture at 2x so subpixel Ahem edges do not dominate the comparison
      // between macOS and Linux. Keep the shared comparator's budget unchanged.
      final boundary = tester.renderObject<RenderRepaintBoundary>(
          find.byKey(const Key('content-golden')));
      final golden = await boundary.toImage(pixelRatio: 2);
      try {
        await expectLater(golden,
            matchesGoldenFile('goldens/downloads_content_${width.toInt()}.png'));
      } finally {
        golden.dispose();
      }
      await tester.tap(find.byTooltip('Actions for Job 1'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Resume'));
      await tester.pumpAndSettle();
      expect(actions, ['42:resume']);
    });
  }

  testWidgets('requester sees Content and filter without admin surfaces', (tester) async {
    final container = ProviderContainer(overrides: [
      authProvider.overrideWith(() => TestAuth(session())),
      downloadsActivityProvider.overrideWith((_) async => sample()),
      downloadsSummaryProvider.overrideWith((_) async => sample()),
    ]);
    addTearDown(container.dispose);
    await tester.pumpWidget(UncontrolledProviderScope(container: container,
      child: MaterialApp(theme: AppTheme.dark, home: DownloadsModuleShell(
        currentIndex: 0, onTabChanged: (_) {}, child: const DownloadsQueuePage()))));
    await tester.pumpAndSettle();
    expect(find.text('All downloads'), findsOneWidget);
    expect(find.text('My requests'), findsOneWidget);
    expect(find.text('Clients'), findsNothing);
    expect(find.text('History'), findsNothing);
    expect(find.byTooltip('Downloads settings'), findsNothing);
    await tester.tap(find.text('A show with a season pack'));
    await tester.pumpAndSettle();
    expect(find.byType(PopupMenuButton<String>), findsNothing);
    expect(container.read(moduleProvider).modules.map((m) => m.type), contains(ModuleType.downloads));
    (container.read(authProvider.notifier) as TestAuth).replace(session(scope: 'mine'));
    await tester.pumpAndSettle();
    expect(find.text('All downloads'), findsNothing);
    expect(container.read(downloadsScopeProvider), 'mine');
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('admin starts in Clients and can remember Content with visibility menu', (tester) async {
    final c = ProviderContainer(overrides: [
      authProvider.overrideWith(() => TestAuth(session(admin: true))),
      downloadsActivityProvider.overrideWith((_) async => sample()),
      downloadsSummaryProvider.overrideWith((_) async => sample()),
    ]);
    await tester.pumpWidget(UncontrolledProviderScope(container: c,
        child: const MaterialApp(home: Scaffold(body: DownloadsQueuePage()))));
    await tester.pumpAndSettle();
    expect(find.byType(DownloadsContentScreen), findsNothing);
    await tester.tap(find.text('Content'));
    await tester.pumpAndSettle();
    expect(find.text('A show with a season pack'), findsOneWidget);
    expect(c.read(downloadsPreferencesProvider).requireValue.content, isTrue);
    await tester.tap(find.byTooltip('Downloads settings'));
    await tester.pumpAndSettle();
    expect(find.text('User visibility'), findsOneWidget);
    expect(find.text('Own requests only'), findsOneWidget);
    expect(find.text('All accessible downloads'), findsOneWidget);
    await tester.pumpWidget(const SizedBox());
    c.dispose();
  });

  test('view and scope preferences are isolated by account and server', () async {
    final container = ProviderContainer(overrides: [authProvider.overrideWith(() => TestAuth(session(admin: true)))]);
    addTearDown(container.dispose);
    final listener = container.listen(downloadsPreferencesProvider, (_, __) {});
    addTearDown(listener.close);
    await container.read(authProvider.future);
    expect((await container.read(downloadsPreferencesProvider.future)).content, false);
    await container.read(downloadsPreferencesProvider.notifier).select(content: true, scope: 'mine');
    final auth = container.read(authProvider.notifier) as TestAuth;
    auth.replace(session(user: 3));
    await container.pump();
    final other = await container.read(downloadsPreferencesProvider.future);
    expect(other.content, false); expect(other.scope, 'all');
    auth.replace(session(server: 'http://other.test'));
    await container.pump();
    expect((await container.read(downloadsPreferencesProvider.future)).scope, 'all');
    auth.replace(session(admin: true));
    await container.pump();
    final saved = await container.read(downloadsPreferencesProvider.future);
    expect(saved.content, true); expect(saved.scope, 'mine');
    expect(container.read(downloadsScopeProvider), 'all'); // admin badge is server-wide
  });

  test('older servers keep requester Downloads hidden', () async {
    final c = ProviderContainer(overrides: [authProvider.overrideWith(() => TestAuth(session(supported: false)))]);
    addTearDown(c.dispose);
    await c.read(authProvider.future);
    expect(c.read(moduleProvider).modules.map((m) => m.type), isNot(contains(ModuleType.downloads)));
  });

  testWidgets('badge hides confirmed zero and marks incomplete count unavailable', (tester) async {
    for (final count in [0, 2, null]) {
      await tester.pumpWidget(ProviderScope(key: ValueKey(count), overrides: [
        downloadsSummaryProvider.overrideWith((_) async => sample(count: count, complete: count != null)),
      ], child: const MaterialApp(home: Scaffold(body: DownloadsMenuBadge()))));
      await tester.pumpAndSettle();
      expect(find.byKey(const Key('downloads-menu-count')), count == 0 ? findsNothing : findsOneWidget);
      if (count == null) expect(find.byTooltip('Download count unavailable'), findsOneWidget);
    }
    await tester.pumpWidget(const SizedBox());
  });

  testWidgets('incomplete empty activity never claims no active downloads', (tester) async {
    final value = DownloadsActivity.fromJson({'complete': false, 'count': null, 'groups': []});
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: DownloadsActivityView(
      activity: value, admin: false, onRefresh: () {}, onAction: (_, __, ___) {}))));
    expect(find.text('No active downloads'), findsNothing);
    expect(find.text('No verified downloads to show yet'), findsOneWidget);
  });

  testWidgets('summary polls in foreground, refreshes on resume and config events', (tester) async {
    final paths = <String>[];
    final dio = Dio(BaseOptions(baseUrl: 'http://server.test'));
    dio.interceptors.add(InterceptorsWrapper(onRequest: (request, handler) {
      paths.add(request.path);
      handler.resolve(Response(requestOptions: request, data: {
        'count': 1, 'complete': true, 'scope': 'all', 'user_scope': 'all',
      }));
    }));
    final events = StreamController<WsEvent>.broadcast();
    final c = ProviderContainer(overrides: [
      authProvider.overrideWith(() => TestAuth(session())),
      backendClientProvider.overrideWithValue(dio),
      realtimeEventsProvider.overrideWithValue(events.stream),
    ]);
    await tester.pumpWidget(UncontrolledProviderScope(container: c,
        child: const MaterialApp(home: DownloadsMenuBadge())));
    await tester.pumpAndSettle();
    final initial = paths.length;
    expect(initial, greaterThan(0));
    await tester.pump(const Duration(seconds: 15));
    await tester.pumpAndSettle();
    expect(paths.length, initial + 1);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    await tester.pump(const Duration(seconds: 30));
    expect(paths.length, initial + 1);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump();
    await tester.pumpAndSettle();
    expect(paths.length, initial + 2);
    final revision = c.read(downloadsRefreshProvider);
    events.add(const WsEvent(type: 'config_changed', data: {}));
    await tester.pump();
    expect(c.read(downloadsRefreshProvider), revision + 1);
    await tester.pump(const Duration(milliseconds: 10));
    await tester.pumpAndSettle();
    expect(paths.length, initial + 3);
    expect(paths.toSet(), {'/api/downloads/summary'});
    await tester.pumpWidget(const SizedBox());
    c.dispose();
    await events.close();
  });

  testWidgets('account change clears count and discards the old response', (tester) async {
    final pending = <({RequestOptions request, RequestInterceptorHandler handler})>[];
    final dio = Dio(BaseOptions(baseUrl: 'http://server.test'));
    dio.interceptors.add(InterceptorsWrapper(onRequest: (request, handler) {
      pending.add((request: request, handler: handler));
    }));
    final c = ProviderContainer(overrides: [
      authProvider.overrideWith(() => TestAuth(session())),
      backendClientProvider.overrideWithValue(dio),
      realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    ]);
    await tester.pumpWidget(UncontrolledProviderScope(container: c,
        child: const MaterialApp(home: DownloadsMenuBadge())));
    await tester.pumpAndSettle();
    final old = pending.last;
    (c.read(authProvider.notifier) as TestAuth).replace(session(user: 3, scope: 'mine'));
    await tester.pumpAndSettle();
    expect(find.byTooltip('Download count unavailable'), findsOneWidget);
    expect(old.request.cancelToken!.isCancelled, isTrue);
    final latest = pending.last;
    expect(latest.request.queryParameters['scope'], 'mine');
    latest.handler.resolve(Response(requestOptions: latest.request, data: {
      'count': 2, 'complete': true, 'scope': 'mine', 'user_scope': 'mine',
    }));
    await tester.pumpAndSettle();
    old.handler.resolve(Response(requestOptions: old.request, data: {
      'count': 9, 'complete': true, 'scope': 'all', 'user_scope': 'all',
    }));
    await tester.pumpAndSettle();
    expect(find.text('2'), findsOneWidget);
    expect(find.text('9'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    c.dispose();
  });

  testWidgets('hidden Content does not fetch full activity', (tester) async {
    var reads = 0;
    await tester.pumpWidget(ProviderScope(overrides: [
      downloadsActivityProvider.overrideWith((_) async { reads++; return sample(); }),
    ], child: const MaterialApp(home: TickerMode(enabled: false,
        child: DownloadsContentScreen()))));
    await tester.pumpAndSettle();
    expect(reads, 0);
  });
}
