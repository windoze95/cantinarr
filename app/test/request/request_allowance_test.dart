import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/features/discover/logic/discovery_access.dart';
import 'package:cantinarr/features/request/data/request_quota.dart';
import 'package:cantinarr/features/request/data/request_service.dart' hide RequestOptions;
import 'package:cantinarr/features/request/logic/request_quota_provider.dart';
import 'package:cantinarr/features/request/ui/request_allowance_screen.dart';
import 'package:cantinarr/features/request/ui/album_request_panel.dart';
import 'package:cantinarr/features/request/ui/book_format_panel.dart';
import 'package:cantinarr/features/request/ui/catalog_request_panel.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _QuotaService extends RequestQuotaService {
  _QuotaService() : super(Dio());
  int reads = 0;
  RequestQuotaView view = const RequestQuotaView(allowances: [
    RequestAllowance(mediaType: 'movie', count: 3, used: 2, remaining: 1),
    RequestAllowance(mediaType: 'tv', count: 5, used: 1, remaining: 4, source: 'override'),
    RequestAllowance(mediaType: 'book', bookFormat: 'ebook'),
  ]);
  Map<String, dynamic>? saved;
  String? savedPath;
  List<RequestAllowance>? resetSelection;
  int? resetUser;
  @override
  Future<RequestQuotaView> read(String path) async { reads++; return view; }
  @override
  Future<void> save(String path, Map<String, dynamic> rule) async { savedPath = path; saved = rule; }
  @override
  Future<void> reset(int userId, List<RequestAllowance> selected) async {
    resetUser = userId; resetSelection = selected;
  }
}

class _Socket extends WebSocketClient {
  _Socket() : super(getServerUrl: () => null, getAccessToken: () => null);
  final controller = StreamController<WsEvent>.broadcast();
  bool connected = false;
  @override
  Stream<WsEvent> get events => controller.stream;
  @override
  bool get isConnected => connected;
  @override
  void ensureConnected() {}
  void reconnect() { connected = true; notifyListeners(); }
  @override
  void dispose() { controller.close(); super.dispose(); }
}

Widget _host(_QuotaService service, Widget child, {bool supported = true}) =>
    ProviderScope(overrides: [
      requestQuotasSupportedProvider.overrideWithValue(supported),
      requestQuotaServiceProvider.overrideWithValue(service),
      requestQuotaSyncProvider.overrideWith((ref) {}),
    ], child: MaterialApp(home: Scaffold(body: SingleChildScrollView(child: child))));

void main() {
  test('replenishment timers tolerate clock skew and 30-day browser windows', () {
    final serverNow = DateTime.utc(2026, 9, 1);
    final view = RequestQuotaView(allowances: const [], asOf: serverNow);
    expect(requestQuotaRefreshDelay(view, serverNow.add(const Duration(seconds: 10))),
        const Duration(milliseconds: 10100));
    expect(requestQuotaRefreshDelay(view, serverNow.add(const Duration(days: 30))),
        const Duration(days: 1));
  });
  testWidgets('older servers hide allowance settings', (tester) async {
    final service = _QuotaService();
    await tester.pumpWidget(_host(service,
      const RequestAllowanceSection(editDefaults: true), supported: false));
    expect(find.text('Request allowances'), findsNothing);
    expect(service.reads, 0);
    expect(const BackendConnection(serverUrl: '', accessToken: '', refreshToken: '').requestQuotas, isFalse);
  });

  testWidgets('user rules show inheritance and save a complete unlimited exception', (tester) async {
    final service = _QuotaService();
    await tester.pumpWidget(_host(service, const RequestAllowanceSection(userId: 7)));
    await tester.pumpAndSettle();
    expect(find.text('Inherits User default: 3 per 7 days'), findsOneWidget);
    expect(find.text('User override: 5 per 7 days'), findsOneWidget);
    await tester.tap(find.byTooltip('Edit Movies allowance'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Inherit User default').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Unlimited').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Save allowance'));
    await tester.pumpAndSettle();
    expect(service.savedPath, '/api/admin/users/7/request-quotas');
    expect(service.saved, {'media_type': 'movie', 'inherit': false, 'count': null, 'window_days': 7});
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
  });

  testWidgets('defaults support zero and each rolling window', (tester) async {
    final service = _QuotaService();
    await tester.pumpWidget(_host(service, const RequestAllowanceSection(editDefaults: true)));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Edit Movies allowance'));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField), '0');
    await tester.tap(find.text('7 days').last);
    await tester.pumpAndSettle();
    expect(find.text('1 day'), findsOneWidget);
    expect(find.text('30 days'), findsOneWidget);
    await tester.tap(find.text('30 days').last);
    await tester.pumpAndSettle();
    await tester.tap(find.text('Save allowance'));
    await tester.pumpAndSettle();
    expect(service.saved, {'media_type': 'movie', 'inherit': false, 'count': 0, 'window_days': 30});
    expect(service.savedPath, '/api/admin/request-quotas');
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
  });

  testWidgets('reset confirms exactly the selected restored units', (tester) async {
    final service = _QuotaService();
    await tester.pumpWidget(_host(service, const RequestAllowanceSection(userId: 7)));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Reset selected allowances'));
    await tester.pumpAndSettle();
    expect(service.resetSelection, isNull);
    expect(find.text('Clear 2 used units; restore 2 units to give 3 remaining.'), findsOneWidget);
    expect(tester.widget<FilledButton>(find.widgetWithText(FilledButton, 'Reset selected')).onPressed, isNull);
    await tester.tap(find.widgetWithText(CheckboxListTile, 'Movies'));
    await tester.pump();
    await tester.tap(find.text('Reset selected'));
    await tester.pumpAndSettle();
    expect(service.resetUser, 7);
    expect(service.resetSelection!.map((a) => a.id), ['movie:']);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
  });

  testWidgets('allowance settings display usage and local replenishment', (tester) async {
    final service = _QuotaService();
    final next = DateTime.now().add(const Duration(days: 1));
    service.view = RequestQuotaView(allowances: [
      RequestAllowance(mediaType: 'tv', count: 2, used: 2, remaining: 0,
        nextReplenishesAt: next, fullyReplenishesAt: next.add(const Duration(days: 1))),
    ]);
    await tester.pumpWidget(_host(service, const RequestAllowanceSection()));
    await tester.pumpAndSettle();
    expect(find.text('2 used · 0 remaining'), findsOneWidget);
    expect(find.textContaining('Next returns'), findsOneWidget);
    expect(find.textContaining('Fully replenishes'), findsOneWidget);
    await tester.pumpWidget(const SizedBox.shrink());
    await tester.pump();
  });

  testWidgets('changes refresh on events, reconnect, resume, mutation and expiry', (tester) async {
    final service = _QuotaService();
    final socket = _Socket();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
    final container = ProviderContainer(overrides: [
      requestQuotasSupportedProvider.overrideWithValue(true),
      requestQuotaServiceProvider.overrideWithValue(service),
      backendClientProvider.overrideWithValue(dio),
      webSocketClientProvider.overrideWith((ref) => socket),
    ]);
    await tester.pumpWidget(UncontrolledProviderScope(container: container,
      child: const MaterialApp(home: Scaffold(body: RequestAllowanceSection()))));
    await tester.pumpAndSettle();
    var previous = service.reads;
    socket.controller.add(const WsEvent(type: 'request_quota_changed', data: {}));
    await tester.pump(); await tester.pump(const Duration(milliseconds: 150)); await tester.pumpAndSettle();
    expect(service.reads, greaterThan(previous)); previous = service.reads;
    socket.reconnect();
    await tester.pump(const Duration(milliseconds: 150)); await tester.pumpAndSettle();
    expect(service.reads, greaterThan(previous)); previous = service.reads;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pump(const Duration(milliseconds: 150)); await tester.pumpAndSettle();
    expect(service.reads, greaterThan(previous)); previous = service.reads;
    dio.httpClientAdapter = _Refuse();
    await tester.runAsync(() async {
      try { await dio.post('/api/requests'); } on DioException catch (_) {}
      await Future<void>.delayed(const Duration(milliseconds: 150));
    });
    await tester.pump(const Duration(milliseconds: 150)); await tester.pumpAndSettle();
    expect(service.reads, greaterThan(previous));
    service.view = RequestQuotaView(allowances: service.view.allowances,
        nextChangeAt: DateTime.now().add(const Duration(seconds: 2)));
    container.invalidate(requestQuotaViewProvider('/api/me/request-quotas'));
    await tester.pumpAndSettle(); previous = service.reads;
    service.view = const RequestQuotaView(allowances: []);
    await tester.pump(const Duration(seconds: 3)); await tester.pumpAndSettle();
    expect(service.reads, greaterThan(previous));
    await tester.pumpWidget(const SizedBox.shrink());
    container.dispose();
    await tester.pump();
  });

  test('book and music quota refusals use a short message', () async {
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = _Refuse();
    final service = RequestService(backendDio: dio);
    for (final future in [
      service.requestBook(foreignId: 'book', title: 'Book', format: BookRequestFormat.both),
      service.requestAlbum(foreignId: 'album', title: 'Album'),
    ]) {
      await expectLater(future, throwsA(isA<RequestSubmissionException>()
          .having((e) => e.message, 'message', 'Request limit reached.')
          .having((e) => e.quotaExceeded, 'quota refusal', true)));
    }
  });

  for (final surface in ['book', 'album', 'catalog book', 'catalog album']) {
    testWidgets('$surface quota refusal is only a toast and permits retry', (tester) async {
      final adapter = _Refuse();
      final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = adapter;
      final service = RequestService(backendDio: dio);
      final panel = switch (surface) {
        'book' => BookFormatPanel(foreignId: 'book', title: 'Book', service: service),
        'album' => AlbumRequestPanel(foreignId: 'album', title: 'Album', service: service),
        _ => CatalogRequestPanel(mediaType: surface == 'catalog book' ? 'book' : 'music',
            foreignId: 'catalog', title: 'Title', instanceId: 'library',
            provider: 'catalog', sourceId: '1'),
      };
      await tester.pumpWidget(ProviderScope(overrides: [
        requestQuotasSupportedProvider.overrideWithValue(true),
        backendClientProvider.overrideWithValue(dio),
        catalogDiscoveryScopeProvider.overrideWithValue('quota-test'),
      ], child: MaterialApp(home: Scaffold(body: SingleChildScrollView(child: panel)))));
      await tester.pumpAndSettle();
      final request = switch (surface) {
        'catalog book' => find.text('Request eBook'),
        'catalog album' => find.text('Request album'),
        _ => find.text('Request').first,
      };
      await tester.tap(request);
      await tester.pumpAndSettle();
      expect(adapter.submissions, 1);
      expect(adapter.previews, 0);
      expect(find.descendant(of: find.byType(SnackBar),
          matching: find.text('Request limit reached.')), findsOneWidget);
      expect(find.textContaining('remaining'), findsNothing);
      await tester.pump(const Duration(seconds: 5));
      await tester.pumpAndSettle();
      expect(find.text('Request limit reached.'), findsNothing);
      await tester.tap(request);
      await tester.pumpAndSettle();
      expect(adapter.submissions, 2);
      await tester.pumpWidget(const SizedBox.shrink());
      await tester.pump();
    });
  }
}

class _Refuse implements HttpClientAdapter {
  int submissions = 0;
  int previews = 0;
  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? body, Future<void>? cancel) async {
    if (options.path == '/api/requests/preview') previews++;
    if (options.path == '/api/requests' && options.method == 'POST') submissions++;
    return ResponseBody.fromString(jsonEncode(options.method == 'GET'
        ? {'success': true, 'status': 'unavailable', 'delivery': [],
            'book_formats': {'ebook': 'unavailable', 'audiobook': 'unavailable'}}
        : {'code': 'request_quota_exceeded', 'error': 'eBooks: 1 requested, 0 remaining.'}),
        options.method == 'GET' ? 200 : 429, headers: {'content-type': ['application/json']});
  }
  @override
  void close({bool force = false}) {}
}
