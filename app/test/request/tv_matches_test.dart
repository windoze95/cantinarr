import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/request/data/request_service.dart' hide RequestOptions;
import 'package:cantinarr/features/request/logic/request_provider.dart';
import 'package:cantinarr/features/request/ui/tv_matches_screen.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const _match = <String, dynamic>{
  'tmdb_id': 299939, 'title': 'Monster: The Lizzie Borden Story',
  'tvdb_id': 389492, 'target_title': 'Monster (2022)', 'series_id': 7,
  'provenance': 'bundled', 'revision': 'reviewed-v1', 'state': 'resolved',
  'season_map': {'1': 4},
};

void main() {
  testWidgets('editor requires an explicit source-to-target season choice', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    expect(find.text('Bundled correction · resolved'), findsOneWidget);
    expect(find.text('Season 1 → Sonarr season 4'), findsOneWidget);
    await tester.tap(find.text('Edit current match'));
    await tester.pumpAndSettle();
    final save = find.widgetWithText(FilledButton, 'Save local correction');
    expect(tester.widget<FilledButton>(save).onPressed, isNull);
    final picker = find.byType(DropdownButtonFormField<int>);
    await tester.ensureVisible(picker);
    await tester.pumpAndSettle();
    await tester.tap(picker);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Sonarr season 4').last);
    await tester.pumpAndSettle();
    expect(tester.widget<FilledButton>(save).onPressed, isNotNull);
    await tester.ensureVisible(find.text('Edit current match'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Edit current match'));
    await tester.pumpAndSettle();
    expect(tester.widget<FilledButton>(save).onPressed, isNull);
    await tester.ensureVisible(picker);
    await tester.tap(picker);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Sonarr season 4').last);
    await tester.pumpAndSettle();
    await tester.ensureVisible(save);
    await tester.pumpAndSettle();
    await tester.tap(save);
    await tester.pumpAndSettle();
    expect(adapter.writes.single.data, containsPair('season_map', {'1': 4}));
    expect(adapter.writes.single.data, containsPair('revision', 'reviewed-v1'));
    expect(adapter.writes.single.data, containsPair('instance_id', 'sonarr-main'));
    expect(ProviderScope.containerOf(tester.element(find.byType(TVMatchEditorScreen)))
      .read(libraryRefreshTickProvider), 1);
    await tester.scrollUntilVisible(find.text('Local correction · resolved').hitTestable(), -300, scrollable: find.byType(Scrollable).first);
    expect(find.text('Local correction · resolved'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });

  testWidgets('search by TVDB ID, pause, restore and stale edits stay explicit', (tester) async {
    final adapter = _Adapter();
    await _pump(tester, adapter);
    await tester.enterText(find.byType(TextField), '389492');
    await tester.tap(find.text('Find series'));
    await tester.pumpAndSettle();
    expect(adapter.requests.lastWhere((r) => r.path.endsWith('/candidates')).queryParameters['q'], '389492');
    expect(find.text('2022 · TVDB 389492'), findsOneWidget);
    await tester.ensureVisible(find.text('Pause matching'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Pause matching'));
    await tester.pumpAndSettle();
    expect(adapter.writes.last.data['mode'], 'paused');
    expect(ProviderScope.containerOf(tester.element(find.byType(TVMatchEditorScreen)))
      .read(libraryRefreshTickProvider), 1);
    adapter.stale = true;
    await tester.ensureVisible(find.text('Restore bundled / default'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Restore bundled / default'));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(find.text('This correction changed. Refresh before saving.').hitTestable(), -300, scrollable: find.byType(Scrollable).first);
    expect(find.text('This correction changed. Refresh before saving.'), findsOneWidget);
    expect(ProviderScope.containerOf(tester.element(find.byType(TVMatchEditorScreen)))
      .read(libraryRefreshTickProvider), 1);
    adapter.stale = false;
    await tester.ensureVisible(find.text('Restore bundled / default'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Restore bundled / default'));
    await tester.pumpAndSettle();
    expect(adapter.writes.last.data['mode'], 'default');
    expect(ProviderScope.containerOf(tester.element(find.byType(TVMatchEditorScreen)))
      .read(libraryRefreshTickProvider), 2);
    await tester.scrollUntilVisible(find.text('Bundled correction · resolved').hitTestable(), -300, scrollable: find.byType(Scrollable).first);
    expect(find.text('Bundled correction · resolved'), findsOneWidget);
  });

  testWidgets('repair preview keeps uncertainty, library, scope and approval visible on a narrow phone', (tester) async {
    final adapter = _Adapter()..showRepair = true;
    await _pump(tester, adapter, width: 320, scale: 2);
    await tester.scrollUntilVisible(find.text('Review repair').hitTestable(), 300, scrollable: find.byType(Scrollable).first);
    await tester.pumpAndSettle();
    expect(find.textContaining('Unverified legacy target'), findsOneWidget);
    await tester.tap(find.text('Review repair'));
    await tester.pumpAndSettle();
    expect(find.text('Repair TV request?'), findsOneWidget);
    final dialog = find.byType(AlertDialog);
    expect(find.descendant(of: dialog, matching: find.text('Library: TV')), findsOneWidget);
    expect(find.descendant(of: dialog, matching: find.textContaining('Episode 1 of season 4')), findsOneWidget);
    await tester.ensureVisible(find.text('The corrective request will wait for approval.'));
    await tester.pumpAndSettle();
    expect(find.text('The corrective request will wait for approval.'), findsOneWidget);
    expect(tester.takeException(), isNull);
    await tester.tap(find.text('Create corrective request'));
    await tester.pumpAndSettle();
    expect(adapter.writes.single.path, '/api/admin/requests/11/repair-tv-match');
    expect(adapter.writes.single.data, {'revision': 'preview-v1'});
    expect(ProviderScope.containerOf(tester.element(find.byType(TVMatchEditorScreen)))
      .read(libraryRefreshTickProvider), 1);
    expect(find.text('Corrective request #12 already saved.'), findsOneWidget);
  });

  for (final scenario in [(false, true), (true, false)]) {
    testWidgets('correction controls are hidden for admin=${scenario.$1}, capability=${scenario.$2}', (tester) async {
      final adapter = _Adapter();
      await _pump(tester, adapter, admin: scenario.$1, capable: scenario.$2);
      expect(find.text('TV corrections are not available for this account and server.'), findsOneWidget);
      expect(adapter.requests, isEmpty);
      expect(find.text('Save local correction'), findsNothing);
    });
  }

  test('unknown match and failed library reads cannot submit requests', () async {
    final adapter = _Adapter()..unknown = true;
    final dio = Dio()..httpClientAdapter = adapter;
    final notifier = RequestNotifier(service: RequestService(backendDio: dio), tmdbId: 299939, mediaType: MediaType.tv);
    addTearDown(notifier.dispose);
    await notifier.checkStatus();
    expect(notifier.state.hasStatus, isFalse);
    expect(notifier.state.unknownInstanceIds, {'sonarr-other'});
    expect(await notifier.request(seasons: [1]), isFalse);
    expect(adapter.writes, isEmpty);
    adapter.unknown = false;
    await notifier.checkStatus();
    expect(notifier.state.hasStatus, isTrue);
    expect(notifier.state.match!.seasonMap, {1: 4});
    expect(notifier.state.seasons.single.seasonNumber, 1);
  });

  test('older response shapes stay compatible', () {
    final old = RequestStatusDetail.fromJson({'status': 'unavailable'});
    expect(old.isKnown, isTrue);
    expect(old.match, isNull);
    expect(old.unknownInstanceIds, isEmpty);
    final status = RequestStatusDetail.fromJson({'status': 'pending', 'delivery': [
      {'state': 'attention', 'message': 'An admin must review the changed TV match.'},
    ]});
    expect(status.deliveryMessages.single, contains('review'));
  });
}

Future<void> _pump(WidgetTester tester, _Adapter adapter,
    {bool admin = true, bool capable = true, double width = 390, double scale = 1}) async {
  tester.view.physicalSize = Size(width, 844);
  tester.view.devicePixelRatio = 1;
  addTearDown(() { tester.view.resetPhysicalSize(); tester.view.resetDevicePixelRatio(); });
  final auth = AuthState(
    connection: BackendConnection(serverUrl: 'http://localhost', accessToken: 'access', refreshToken: 'refresh',
      configConfirmed: true, tvMatchCorrections: capable,
      instances: const [ServiceInstance(id: 'sonarr-main', serviceType: 'sonarr', name: 'TV', isDefault: true)]),
    user: UserProfile(id: 1, username: 'tester', role: admin ? 'admin' : 'user'),
  );
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _Auth(auth)),
    backendClientProvider.overrideWithValue(Dio()..httpClientAdapter = adapter),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  final router = GoRouter(routes: [GoRoute(path: '/', builder: (_, __) =>
      const TVMatchEditorScreen(tmdbId: 299939, instanceId: 'sonarr-main'))]);
  addTearDown(router.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(container: container,
    child: MaterialApp.router(routerConfig: router, theme: AppTheme.dark,
      builder: (context, child) => MediaQuery(data: MediaQuery.of(context).copyWith(textScaler: TextScaler.linear(scale)), child: child!))));
  await tester.pumpAndSettle();
}

class _Auth extends AuthNotifier {
  final AuthState initial;
  _Auth(this.initial);
  @override
  Future<AuthState> build() async => initial;
}

class _Adapter implements HttpClientAdapter {
  final requests = <RequestOptions>[];
  Iterable<RequestOptions> get writes => requests.where((r) => r.method != 'GET');
  var mode = 'default';
  var stale = false;
  var unknown = false;
  var showRepair = false;
  var repaired = false;
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    requests.add(options);
    Object body;
    if (options.path.endsWith('/status')) {
      body = {'status': 'unavailable', 'status_known': !unknown,
        'match': {..._match, 'state': unknown ? 'unresolved' : 'resolved'},
        'seasons': [{'season_number': 1, 'status': 'unavailable'}],
        'instance_statuses': {'sonarr-main': {'status': 'unavailable'},
          'sonarr-other': {'status': 'unavailable', 'status_known': false}}};
    } else if (options.path.endsWith('/candidates')) {
      body = [{'tvdbId': 389492, 'title': 'Monster (2022)', 'year': 2022,
        'seasons': [for (final n in [0, 1, 2, 3, 4]) {'seasonNumber': n}]}];
    } else if (options.path.endsWith('/repair-tv-match')) {
      repaired = true; body = {'status': 'pending'};
    } else if (options.path.endsWith('/repairs')) {
      body = showRepair ? [{'request_id': 11, 'title': _match['title'], 'status': 'pending',
        'instance_name': 'TV', 'recorded_target_known': false, 'recorded_tvdb_id': 99,
        'can_repair': true, 'requires_approval': true, 'revision': 'preview-v1',
        'message': 'The original target cannot be verified. No files will be deleted or old monitoring changed.',
        if (repaired) 'repair_request_id': 12,
        'intended_target': {'match': _match, 'source_seasons': [1], 'target_seasons': [4], 'pilot': true}}] : [];
    } else {
      if (options.method == 'PUT') {
        if (stale) return _response({'error': 'This correction changed. Refresh before saving.'}, code: 409);
        mode = options.data['mode'] as String;
      }
      body = {'match': {..._match, 'provenance': mode == 'default' ? 'bundled' : 'custom',
        'state': mode == 'paused' ? 'paused' : 'resolved'},
        'instance_id': 'sonarr-main', 'source_seasons': [{'season_number': 1, 'name': 'Season 1'}],
        'target_seasons': [for (final n in [1, 2, 3, 4]) {'seasonNumber': n}]};
    }
    return _response(body);
  }
  @override
  void close({bool force = false}) {}
}

ResponseBody _response(Object body, {int code = 200}) => ResponseBody.fromString(jsonEncode(body), code,
    headers: {'content-type': ['application/json']});
