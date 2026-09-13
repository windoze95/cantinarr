import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/core/widgets/app_panel.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/logic/media_detail_provider.dart';
import 'package:cantinarr/features/media_detail/ui/media_detail_screen.dart';
import 'package:cantinarr/features/media_detail/ui/season_table.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/ui/request_button.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

import 'tv_schedule_fixtures.dart';

void main() {
  testWidgets('saved TV delivery refreshes without a websocket and retires its wait message', (tester) async {
    final adapter = _TVAdapter(detail: carrieTVFixture)..statusFields = {
      'delivery': [{'state': 'retry', 'message': 'Waiting for the initial TV refresh.'}],
    };
    await _pumpDetail(tester, adapter: adapter);
    expect(find.text('Waiting for the initial TV refresh.'), findsOneWidget);
    adapter.statusFields = {};
    await tester.pump(const Duration(seconds: 15));
    await tester.pumpAndSettle();
    expect(find.text('Waiting for the initial TV refresh.'), findsNothing);
    expect(adapter.requests.where((r) => r.path.endsWith('/status')).length, greaterThanOrEqualTo(2));
    expect(find.text('Premieres Oct 7, 2026'), findsOneWidget);
  });

  testWidgets('requested Carrie shows its premiere and cannot request Season 1',
      (tester) async {
    final adapter = _TVAdapter(detail: carrieTVFixture);
    await _pumpDetail(tester, adapter: adapter);

    final date = find.text('Premieres Oct 7, 2026');
    expect(date, findsOneWidget);
    expect(find.descendant(of: find.byType(AppPanel), matching: date),
        findsOneWidget);
    expect(tester.getTopLeft(date).dy,
        greaterThan(tester.getBottomLeft(find.byType(RequestButton)).dy));
    expect(tester.widget<RequestButton>(find.byType(RequestButton)).status,
        RequestStatus.requested);
    expect(find.text('Premiere'), findsOneWidget);
    expect(find.text('Oct 7, 2026'), findsOneWidget);

    final table = find.byType(SeasonTable);
    expect(tester.widget<SeasonTable>(table).seasons, hasLength(1));
    final checkbox = tester.widget<Checkbox>(
        find.descendant(of: table, matching: find.byType(Checkbox)));
    expect(checkbox.value, isTrue);
    expect(checkbox.onChanged, isNull);
    expect(find.descendant(of: table, matching: find.byType(ActionChip)),
        findsNothing);
    expect(find.descendant(of: table, matching: find.byType(ElevatedButton)),
        findsNothing);
    await tester.ensureVisible(find.text('Season 1 · 2026'));
    await tester.tap(find.text('Season 1 · 2026'));
    await tester.pumpAndSettle();
    expect(adapter.requests.where((r) => r.method != 'GET'), isEmpty);
    expect(adapter.requests.where((r) => r.path.startsWith('/api/instances/')),
        isEmpty);
    expect(tester.takeException(), isNull);
  });

  testWidgets('Available still shows its future episode and original first date',
      (tester) async {
    await _pumpDetail(tester,
        adapter: _TVAdapter(detail: returningTVFixture, status: 'available'));
    expect(find.text('Available'), findsOneWidget);
    expect(find.text('Next episode S2 E3 · Oct 14, 2026'), findsOneWidget);
    expect(find.text('First aired'), findsOneWidget);
    expect(find.text('Oct 2, 2024'), findsOneWidget);
    expect(find.text('Premiere'), findsNothing);
  });

  testWidgets('a returning season starts with a season premiere label',
      (tester) async {
    await _pumpDetail(tester, adapter: _TVAdapter(detail: {
      ...returningTVFixture,
      'next_episode_to_air': {
        'season_number': 2, 'episode_number': 1, 'air_date': '2026-10-07',
      },
    }));
    expect(find.text('Season 2 premieres Oct 7, 2026'), findsOneWidget);
  });

  testWidgets('an upcoming season supplies the date when the episode is undated',
      (tester) async {
    await _pumpDetail(tester, adapter: _TVAdapter(detail: {
      ...returningTVFixture,
      'next_episode_to_air': {'id': 12},
      'seasons': [
        {'id': 2, 'season_number': 2, 'air_date': '2026-10-07'},
      ],
    }));
    expect(find.text('Season 2 premieres Oct 7, 2026'), findsOneWidget);
    expect(find.textContaining('TBA'), findsNothing);
  });

  for (final returning in [false, true]) {
    testWidgets('${returning ? 'returning show' : 'new premiere'} has TBA',
        (tester) async {
      await _pumpDetail(tester, adapter: _TVAdapter(detail: {
        'id': 123, 'name': 'Undated Show',
        'status': returning ? 'Returning Series' : 'Planned',
      }));
      expect(find.text(returning ? 'Next episode date TBA' : 'Premiere date TBA'),
          findsOneWidget);
      expect(find.text('Premiere'), findsNothing);
      expect(find.text('First aired'), findsNothing);
    });
  }

  for (final status in ['Ended', 'Canceled']) {
    testWidgets('$status without a future event has no date line', (tester) async {
      await _pumpDetail(tester, adapter: _TVAdapter(detail: {
        ...returningTVFixture,
        'status': status,
        'next_episode_to_air': {'id': 12, 'air_date': '2026-09-11'},
      }));
      expect(find.textContaining('Next episode'), findsNothing);
      expect(find.textContaining('TBA'), findsNothing);
      expect(find.text('First aired'), findsOneWidget);
    });
  }

  testWidgets('an episode without numbers keeps its generic dated label',
      (tester) async {
    await _pumpDetail(tester, adapter: _TVAdapter(detail: {
      ...returningTVFixture,
      'next_episode_to_air': {'air_date': '2026-10-14'},
    }));
    expect(find.text('Next episode · Oct 14, 2026'), findsOneWidget);
  });

  testWidgets('catalog-only browsing shows dates alongside Sonarr setup',
      (tester) async {
    final adapter = _TVAdapter(detail: carrieTVFixture);
    await _pumpDetail(tester, adapter: adapter, catalogOnly: true);
    expect(find.text('Connect Sonarr to request TV shows'), findsOneWidget);
    expect(find.text('Premieres Oct 7, 2026'), findsOneWidget);
    expect(find.byType(RequestButton), findsNothing);
    expect(adapter.requests.where((r) => r.path.startsWith('/api/requests')),
        isEmpty);
    expect(adapter.requests.where((r) => r.path.startsWith('/api/instances/')),
        isEmpty);
  });

  testWidgets('loading metadata cannot become TBA or a guessed date',
      (tester) async {
    final gate = Completer<ResponseBody>();
    final adapter = _TVAdapter(detail: carrieTVFixture, detailGate: gate);
    await _pumpDetail(tester, adapter: adapter, settle: false);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    expect(find.textContaining('TBA'), findsNothing);
    expect(find.textContaining('Premieres'), findsNothing);
    gate.complete(_response(carrieTVFixture));
    await tester.pumpAndSettle();
    expect(find.text('Premieres Oct 7, 2026'), findsOneWidget);
  });

  testWidgets('a metadata error keeps the details error, never TBA',
      (tester) async {
    await _pumpDetail(tester,
        adapter: _TVAdapter(detail: carrieTVFixture, detailStatus: 503));
    expect(find.textContaining('Failed to load details:'), findsOneWidget);
    expect(find.textContaining('TBA'), findsNothing);
    expect(find.textContaining('Premieres'), findsNothing);
    expect(find.text('Premiere'), findsNothing);
  });

  testWidgets('an account-hidden title cannot reveal its schedule',
      (tester) async {
    await _pumpDetail(tester,
        adapter: _TVAdapter(detail: carrieTVFixture, detailStatus: 404));
    expect(find.text("This title isn't available on this account."), findsOneWidget);
    expect(find.textContaining('TBA'), findsNothing);
    expect(find.textContaining('Premieres'), findsNothing);
  });

  testWidgets('premiere day says today while Details keeps the full date',
      (tester) async {
    await _pumpDetail(tester, adapter: _TVAdapter(detail: carrieTVFixture),
        now: DateTime(2026, 10, 7, 23, 59));
    expect(find.text('Premieres today'), findsOneWidget);
    expect(find.text('Premiere'), findsOneWidget);
    expect(find.text('Oct 7, 2026'), findsOneWidget);
  });

  testWidgets('after premiere day only the original First aired fact remains',
      (tester) async {
    await _pumpDetail(tester, adapter: _TVAdapter(detail: carrieTVFixture),
        now: DateTime(2026, 10, 8));
    expect(find.textContaining('Premieres'), findsNothing);
    expect(find.textContaining('TBA'), findsNothing);
    expect(find.text('First aired'), findsOneWidget);
    expect(find.text('Oct 7, 2026'), findsOneWidget);
  });

  for (final width in [320.0, 390.0]) {
    for (final scale in [1.0, 2.0]) {
      testWidgets('episode date fits width $width at text scale $scale',
          (tester) async {
        await _pumpDetail(tester,
            adapter: _TVAdapter(detail: returningTVFixture, status: 'available'),
            width: width, textScale: scale);
        final date = find.text('Next episode S2 E3 · Oct 14, 2026');
        expect(date, findsOneWidget);
        final text = tester.widget<Text>(date);
        expect(text.maxLines, isNull);
        expect(text.style?.fontSize, 13);
        expect(text.style?.color, AppTheme.textSecondary);
        expect(find.byIcon(Icons.event_outlined), findsOneWidget);
        expect(tester.getRect(date).left, greaterThanOrEqualTo(16));
        expect(tester.getRect(date).right, lessThanOrEqualTo(width - 16));
        if (scale == 2) expect(tester.getSize(date).height, greaterThan(30));
        await tester.ensureVisible(find.text('First aired'));
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
      });
    }
  }

  testWidgets('catalog premiere also wraps at large text on a narrow phone',
      (tester) async {
    await _pumpDetail(tester, adapter: _TVAdapter(detail: carrieTVFixture),
        catalogOnly: true, width: 320, textScale: 2);
    expect(find.text('Premieres Oct 7, 2026'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

Future<void> _pumpDetail(
  WidgetTester tester, {
  required _TVAdapter adapter,
  DateTime? now,
  bool catalogOnly = false,
  double width = 390,
  double textScale = 1,
  bool settle = true,
}) async {
  tester.view.physicalSize = Size(width, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() {
    tester.view.resetPhysicalSize();
    tester.view.resetDevicePixelRatio();
  });
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _Auth(_authState(catalogOnly))),
    backendClientProvider.overrideWithValue(dio),
    realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
    mediaDetailClockProvider.overrideWithValue(() => now ?? DateTime(2026, 9, 12)),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  final router = GoRouter(initialLocation: '/detail/tv/123', routes: [
    GoRoute(path: '/detail/tv/123', builder: (_, __) =>
        const MediaDetailScreen(id: 123, mediaType: MediaType.tv)),
  ]);
  addTearDown(router.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: MaterialApp.router(
      theme: AppTheme.dark,
      routerConfig: router,
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(textScale)),
        child: child!,
      ),
    ),
  ));
  if (settle) {
    await tester.pumpAndSettle();
  } else {
    await tester.pump();
  }
}

AuthState _authState(bool catalogOnly) => AuthState(
  connection: BackendConnection(
    serverUrl: 'http://localhost', accessToken: 'access', refreshToken: 'refresh',
    configConfirmed: true, adminCatalogBrowsing: true,
    instances: catalogOnly ? const [] : const [
      ServiceInstance(id: 'sonarr-main', serviceType: 'sonarr',
          name: 'TV', isDefault: true),
    ],
  ),
  user: UserProfile(id: 1, username: 'viewer', role: catalogOnly ? 'admin' : 'user'),
);

class _Auth extends AuthNotifier {
  final AuthState initial;
  _Auth(this.initial);
  @override
  Future<AuthState> build() async => initial;
}

class _TVAdapter implements HttpClientAdapter {
  Map<String, dynamic> statusFields = {};
  final Map<String, dynamic> detail;
  final String status;
  final int detailStatus;
  final Completer<ResponseBody>? detailGate;
  final requests = <RequestOptions>[];

  _TVAdapter({required this.detail, this.status = 'requested',
    this.detailStatus = 200, this.detailGate});

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream,
      Future<void>? cancelFuture) async {
    requests.add(options);
    final path = options.path;
    if (path == '/api/media/tv/123') {
      return detailGate?.future ?? _response(detail, statusCode: detailStatus);
    }
    if (path == '/api/requests/123/status') {
      return _response({
        ...statusFields,
        'status': status,
        'seasons': [
          {'season_number': 1, 'status': status, 'episode_count': 0},
        ],
      });
    }
    if (path == '/api/requests/options') {
      return _response({'can_choose_season': true, 'can_choose_quality': false});
    }
    if (path.endsWith('/recommendations') || path.endsWith('/similar')) {
      return _response({'results': <dynamic>[]});
    }
    return _response(<dynamic>[]);
  }

  @override
  void close({bool force = false}) {}
}

ResponseBody _response(Object body, {int statusCode = 200}) =>
    ResponseBody.fromString(jsonEncode(body), statusCode,
        headers: {'content-type': ['application/json']});
