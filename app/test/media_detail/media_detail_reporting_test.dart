import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:cantinarr/features/request/ui/request_button.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const _tmdbId = 299939;

void main() {
  for (final type in [MediaType.movie, MediaType.tv]) {
    for (final accepted in ['requested', 'pending']) {
      testWidgets('${type.name}: accepted $accepted reveals reporting and survives revisits', (tester) async {
        final backend = _Backend()..acceptedStatus = accepted;
        final router = await _pump(tester, backend, type: type);
        expect(find.text('Report a problem'), findsNothing);
        await _tap(tester, find.widgetWithText(ElevatedButton, 'Request'));
        expect(backend.requestPosts, hasLength(1));
        expect(find.text('Report a problem'), findsOneWidget);
        router.go('/away');
        await tester.pumpAndSettle();
        router.go('/detail/${type.name}/$_tmdbId');
        await tester.pumpAndSettle();
        expect(find.text('Report a problem'), findsOneWidget);
        expect(backend.requestPosts, hasLength(1));
      });
    }

    testWidgets('${type.name}: cancelled options and denial do not reveal reporting', (tester) async {
      final backend = _Backend()..chooseOptions = true..status = 'denied';
      await _pump(tester, backend, type: type);
      expect(find.text('Report a problem'), findsNothing);
      await _tap(tester, find.widgetWithText(ElevatedButton, 'Request'));
      expect(find.text('Request options'), findsOneWidget);
      expect(find.text('Report a problem'), findsNothing);
      await _tap(tester, find.text('Cancel'));
      expect(backend.requestPosts, isEmpty);
      expect(find.text('Report a problem'), findsNothing);
    });

    testWidgets('${type.name}: failed submission exposes reporting and can retry normally', (tester) async {
      final backend = _Backend()..requestCode = 503;
      await _pump(tester, backend, type: type);
      await _tap(tester, find.widgetWithText(ElevatedButton, 'Request'));
      expect(find.text('Report a problem'), findsOneWidget);
      expect(backend.requestPosts, hasLength(1));
      backend.requestCode = 201;
      await _tap(tester, find.widgetWithText(ElevatedButton, 'Request'));
      expect(backend.requestPosts, hasLength(2));
      expect(find.text('Report a problem'), findsOneWidget);
    });

    testWidgets('${type.name}: existing library statuses keep reporting', (tester) async {
      final backend = _Backend()..status = 'available';
      final router = await _pump(tester, backend, type: type);
      for (final status in ['available', 'partial', 'downloading']) {
        backend.status = status;
        router.go('/away');
        await tester.pumpAndSettle();
        router.go('/detail/${type.name}/$_tmdbId');
        await tester.pumpAndSettle();
        expect(find.text('Report a problem'), findsOneWidget);
      }
    });
  }

  for (final failure in ['unresolved', 'paused', 'status_unknown', 'status_http']) {
    testWidgets('TV $failure exposes correction before Request can be pressed', (tester) async {
      final backend = _Backend();
      if (failure == 'status_http') {
        backend.statusCode = 503;
      } else if (failure == 'status_unknown') {
        backend.statusKnown = false;
      } else {
        backend.matchState = failure;
      }
      await _pump(tester, backend, admin: true, corrections: true,
        viewport: const Size(320, 640), textScale: 2);
      expect(tester.widget<RequestButton>(find.byType(RequestButton)).onRequest, isNull);
      expect(find.byTooltip('More options'), findsNothing);
      expect(find.text('Correct TV match'), findsNothing);
      await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
      final correction = find.widgetWithText(ListTile, 'Correct TV match');
      expect(tester.getSize(correction).height, greaterThanOrEqualTo(48));
      expect(find.text('Season 4'), findsNothing);
      await _tap(tester, correction);
      expect(find.text('Editor $_tmdbId · sonarr-main'), findsOneWidget);
      expect(backend.requestPosts, isEmpty);
      expect(backend.issuePosts, isEmpty);
      expect(backend.mutations, isEmpty);

      backend.matchState = 'resolved';
      backend.statusCode = 200;
      backend.statusKnown = true;
      final before = backend.statusReads;
      await _tap(tester, find.text('Done'));
      expect(backend.statusReads, greaterThan(before));
      expect(tester.widget<RequestButton>(find.byType(RequestButton)).onRequest, isNotNull);
      expect(find.text('Report a problem'), findsNothing);
      expect(tester.takeException(), isNull);
    });
  }

  testWidgets('loading alone does not show reporting', (tester) async {
    final wait = Completer<void>();
    final backend = _Backend()..statusWait = wait.future;
    await _pump(tester, backend, settle: false, admin: true, corrections: true);
    await tester.pump(const Duration(milliseconds: 400));
    expect(find.text('Report a problem'), findsNothing);
    wait.complete();
    await tester.pumpAndSettle();
    expect(find.text('Report a problem'), findsNothing);
  });

  testWidgets('season-table submission uses the same reporting eligibility', (tester) async {
    final backend = _Backend()..chooseOptions = true;
    await _pump(tester, backend);
    expect(find.text('Report a problem'), findsNothing);
    await _tap(tester, find.byType(Checkbox).first);
    await _tap(tester, find.widgetWithText(ElevatedButton, 'Request 1 season'));
    expect(backend.requestPosts.single, containsPair('seasons', [1]));
    expect(find.text('Report a problem'), findsOneWidget);
  });

  testWidgets('accepted request remains reportable when its follow-up status read fails', (tester) async {
    final backend = _Backend()..failAfterAcceptance = true;
    await _pump(tester, backend);
    await _tap(tester, find.widgetWithText(ElevatedButton, 'Request'));
    expect(backend.requestPosts, hasLength(1));
    expect(find.text('Report a problem'), findsOneWidget);
    expect(tester.widget<RequestButton>(find.byType(RequestButton)).onRequest, isNull);
  });

  testWidgets('changing libraries clears the prior match error and pins the correction destination', (tester) async {
    final backend = _Backend()..multipleLibraries = true..matchState = 'unresolved';
    await _pump(tester, backend, admin: true, corrections: true);
    expect(find.text('Report a problem'), findsOneWidget);
    backend.matchState = 'resolved';
    await _tap(tester, find.textContaining('4K Sonarr').first);
    expect(find.text('Report a problem'), findsNothing);
    backend.matchState = 'paused';
    await _tap(tester, find.textContaining('Main Sonarr').first);
    await _tap(tester, find.textContaining('4K Sonarr').first);
    await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
    await _tap(tester, find.text('Correct TV match'));
    expect(find.text('Editor $_tmdbId · sonarr-4k'), findsOneWidget);
    expect(backend.mutations, isEmpty);
  });

  testWidgets('setup without a Sonarr instance does not expose corrections or reporting', (tester) async {
    final backend = _Backend()..matchState = 'unresolved';
    await _pump(tester, backend, admin: true, corrections: true, configured: false);
    expect(find.text('Report a problem'), findsNothing);
    expect(find.text('Correct TV match'), findsNothing);
    expect(backend.requestPosts, isEmpty);
  });

  testWidgets('admin corrections remain usable when reporting is disabled', (tester) async {
    final backend = _Backend()..matchState = 'unresolved';
    await _pump(tester, backend, admin: true, corrections: true, reporting: false);
    await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
    expect(find.text('Correct TV match'), findsOneWidget);
    expect(find.textContaining('Problem reporting is disabled'), findsOneWidget);
    expect(find.text('The whole series'), findsNothing);
    expect(find.widgetWithText(ListTile, 'Season 1'), findsNothing);
    expect(find.text('Submit'), findsNothing);
    await _tap(tester, find.text('Correct TV match'));
    expect(find.text('Editor $_tmdbId · sonarr-main'), findsOneWidget);
    expect(backend.mutations, isEmpty);
  });

  for (final scenario in [
    (label: 'requester', admin: false, corrections: true, type: MediaType.tv),
    (label: 'older server', admin: true, corrections: false, type: MediaType.tv),
    (label: 'movie', admin: true, corrections: true, type: MediaType.movie),
  ]) {
    for (final reporting in [true, false]) {
      testWidgets('${scenario.label} respects reporting=$reporting and never offers corrections', (tester) async {
        final backend = _Backend()..status = 'requested';
        await _pump(tester, backend, admin: scenario.admin,
          corrections: scenario.corrections, type: scenario.type, reporting: reporting);
        if (reporting) {
          await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
        } else {
          expect(find.text('Report a problem'), findsNothing);
        }
        expect(find.text('Correct TV match'), findsNothing);
        expect(find.byTooltip('More options'), findsNothing);
        expect(backend.mutations, isEmpty);
      });
    }
  }

  testWidgets('existing report shares the admin sheet and does not hide correction', (tester) async {
    final backend = _Backend()..issues = [_issue(10, 'sonarr-main')];
    await _pump(tester, backend, admin: true, corrections: true);
    expect(find.widgetWithText(TextButton, 'View your report'), findsNothing);
    await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
    expect(find.text('Correct TV match'), findsOneWidget);
    expect(find.text('View your report'), findsOneWidget);
    expect(find.text('The whole series'), findsNothing);
    await _tap(tester, find.text('View your report'));
    expect(find.text('Report 10'), findsOneWidget);
    backend.issues = [];
    await _tap(tester, find.text('Done'));
    expect(find.text('Report a problem'), findsNothing);
    expect(find.text('View your report'), findsNothing);
    expect(backend.mutations, isEmpty);
  });

  testWidgets('requester existing report follows selected library and refreshes on return', (tester) async {
    final backend = _Backend()..multipleLibraries = true
      ..issues = [_issue(10, 'sonarr-main'), _issue(20, 'sonarr-4k')];
    await _pump(tester, backend);
    await _tap(tester, find.widgetWithText(TextButton, 'View your report'));
    expect(find.text('Report 10'), findsOneWidget);
    await _tap(tester, find.text('Done'));
    await _tap(tester, find.textContaining('4K Sonarr').first);
    await _tap(tester, find.widgetWithText(TextButton, 'View your report'));
    expect(find.text('Report 20'), findsOneWidget);
    backend.issues = [_issue(10, 'sonarr-main')];
    await _tap(tester, find.text('Done'));
    expect(find.text('View your report'), findsNothing);
    expect(find.text('Report a problem'), findsNothing);
  });

  testWidgets('TV report submits its selected source scope and refreshes the shortcut', (tester) async {
    final backend = _Backend()..status = 'requested';
    await _pump(tester, backend, admin: true, corrections: true);
    await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
    await _tap(tester, find.widgetWithText(ListTile, 'Season 1'));
    await _tap(tester, find.text('E2'));
    await _tap(tester, find.text('Submit'));
    expect(backend.issuePosts, hasLength(1));
    expect(backend.issuePosts.single, containsPair('season_number', 1));
    expect(backend.issuePosts.single, containsPair('episode_number', 2));
    expect(backend.issuePosts.single, containsPair('instance_id', 'sonarr-main'));
    expect(backend.issuePosts.single, containsPair('tmdb_id', _tmdbId));
    await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
    expect(find.text('View your report'), findsOneWidget);
    expect(find.text('Correct TV match'), findsOneWidget);
    expect(find.text('The whole series'), findsNothing);
  });

  testWidgets('movie report still submits and opens its existing report', (tester) async {
    final backend = _Backend()..status = 'pending';
    await _pump(tester, backend, type: MediaType.movie);
    await _tap(tester, find.widgetWithText(TextButton, 'Report a problem'));
    await _tap(tester, find.text('Submit'));
    expect(backend.issuePosts.single, containsPair('media_type', 'movie'));
    expect(backend.issuePosts.single, containsPair('instance_id', 'radarr-main'));
    expect(find.widgetWithText(TextButton, 'View your report'), findsOneWidget);
    expect(find.widgetWithText(TextButton, 'Report a problem'), findsNothing);
  });
}

Future<void> _tap(WidgetTester tester, Finder finder) async {
  await tester.ensureVisible(finder);
  await tester.pumpAndSettle();
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

Future<GoRouter> _pump(WidgetTester tester, _Backend backend, {
  MediaType type = MediaType.tv, bool admin = false, bool corrections = false,
  bool reporting = true, bool settle = true, bool configured = true,
  Size viewport = const Size(390, 844), double textScale = 1,
}) async {
  tester.view.physicalSize = viewport;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.resetPhysicalSize);
  addTearDown(tester.view.resetDevicePixelRatio);
  backend.type = type;
  final service = type == MediaType.movie ? 'radarr' : 'sonarr';
  final auth = AuthState(
    connection: BackendConnection(serverUrl: 'http://localhost',
      accessToken: 'test-access', refreshToken: 'test-refresh',
      tvMatchCorrections: corrections, allowReporting: reporting,
      instances: [if (configured) ...[
        ServiceInstance(id: '$service-main', serviceType: service,
          name: 'Main ${service == 'sonarr' ? 'Sonarr' : 'Radarr'}', isDefault: true),
        if (backend.multipleLibraries)
          ServiceInstance(id: '$service-4k', serviceType: service, name: '4K Sonarr'),
      ]]),
    user: UserProfile(id: 1, username: admin ? 'admin' : 'viewer', role: admin ? 'admin' : 'user'),
  );
  Widget destination(BuildContext context, String label) => Scaffold(
    body: Column(children: [Text(label), TextButton(
      onPressed: () => context.pop(), child: const Text('Done'))]));
  final router = GoRouter(initialLocation: '/detail/${type.name}/$_tmdbId', routes: [
    GoRoute(path: '/detail/:type/:id', builder: (_, state) => MediaDetailScreen(
      id: int.parse(state.pathParameters['id']!), mediaType: type)),
    GoRoute(path: '/away', builder: (_, __) => const Scaffold(body: Text('Away'))),
    GoRoute(path: '/settings/tv-matches/:id', builder: (context, state) => destination(context,
      'Editor ${state.pathParameters['id']} · ${state.uri.queryParameters['instance_id']}')),
    GoRoute(path: '/issues/:id', builder: (context, state) => destination(context,
      'Report ${state.pathParameters['id']}')),
  ]);
  await tester.pumpWidget(ProviderScope(overrides: [
    authProvider.overrideWith(() => _Auth(auth)),
    backendClientProvider.overrideWithValue(Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = backend),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
  ], child: MaterialApp.router(theme: AppTheme.dark, routerConfig: router,
    builder: (context, child) => MediaQuery(data: MediaQuery.of(context)
      .copyWith(textScaler: TextScaler.linear(textScale)), child: child!))));
  if (settle) await tester.pumpAndSettle();
  return router;
}

class _Auth extends AuthNotifier {
  final AuthState auth;
  _Auth(this.auth);
  @override
  Future<AuthState> build() async => auth;
}

Map<String, dynamic> _issue(int id, String instance, {String type = 'tv'}) => {
  'id': id, 'instance_id': instance, 'tmdb_id': _tmdbId, 'media_type': type,
  'reporter_id': 1, 'status': 'observing',
};

class _Backend implements HttpClientAdapter {
  MediaType type = MediaType.tv;
  String status = 'unavailable';
  String acceptedStatus = 'requested';
  String matchState = 'resolved';
  bool statusKnown = true;
  bool chooseOptions = false;
  bool multipleLibraries = false;
  bool failAfterAcceptance = false;
  int statusCode = 200;
  int requestCode = 201;
  int statusReads = 0;
  Future<void>? statusWait;
  List<Map<String, dynamic>> issues = [];
  final requestPosts = <Map<String, dynamic>>[];
  final issuePosts = <Map<String, dynamic>>[];
  final mutations = <String>[];

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    final path = options.path;
    Object body = <dynamic>[];
    var code = 200;
    if (options.method != 'GET') mutations.add('${options.method} $path');
    if (path == '/api/issues') {
      if (options.method == 'POST') {
        issuePosts.add(Map<String, dynamic>.from(options.data as Map));
        issues = [_issue(30, options.data['instance_id'] as String, type: type.name)];
        body = {'id': 30, 'status': 'observing'};
      } else {
        body = {'issues': issues};
      }
    } else if (path == '/api/requests' && options.method == 'POST') {
      requestPosts.add(Map<String, dynamic>.from(options.data as Map));
      code = requestCode;
      if (code == 201) status = acceptedStatus;
      if (code == 201 && failAfterAcceptance) statusCode = 503;
      body = code == 201 ? {'status': status} : {'error': 'Could not deliver this request'};
    } else if (path.endsWith('/options')) {
      body = {'can_choose_season': chooseOptions, 'can_choose_quality': false,
        'default_season_scope': 'all', 'quality_profiles': <dynamic>[]};
    } else if (path.endsWith('/status')) {
      statusReads++;
      if (statusWait != null) await statusWait;
      code = statusCode;
      final library = options.queryParameters['instance_id'];
      body = code != 200 ? {'error': 'Library unavailable'} : {
        'status': library == 'sonarr-4k' ? 'unavailable' : status,
        'status_known': statusKnown, 'seasons': <dynamic>[],
        if (type == MediaType.tv) 'match': {'tmdb_id': _tmdbId, 'tvdb_id': 389492,
          'title': 'Lizzie Borden', 'target_title': 'Monster', 'state': matchState,
          'provenance': 'bundled', 'revision': 'v1', 'season_map': {'1': 4}},
        if (multipleLibraries) 'instance_statuses': {
          'sonarr-main': {'status': status}, 'sonarr-4k': {'status': 'unavailable'},
        },
      };
    } else if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      body = {'results': <dynamic>[]};
    } else if (path.contains('/api/media/tv/')) {
      body = {'id': _tmdbId, 'name': 'Lizzie Borden',
        'external_ids': {'tvdb_id': 81189}, 'seasons': [
          {'id': 7001, 'season_number': 1, 'name': 'Season 1', 'episode_count': 2},
        ]};
    } else if (path.contains('/api/media/movie/')) {
      body = {'id': _tmdbId, 'title': 'A Movie'};
    }
    return ResponseBody.fromString(jsonEncode(body), code,
      headers: {'content-type': ['application/json']});
  }

  @override
  void close({bool force = false}) {}
}
