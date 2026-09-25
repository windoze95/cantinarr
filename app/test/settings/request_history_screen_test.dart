import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/settings/data/request_history_service.dart';
import 'package:cantinarr/features/settings/ui/request_history_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

import 'request_history_fixture.dart';

void main() {
  testWidgets('pages through requests and keeps recorded decisions in details', (tester) async {
    final adapter = _HistoryAdapter();
    await _pump(tester, adapter);
    expect(find.text('Dune'), findsOneWidget);
    expect(find.text('Kind of Blue'), findsNothing);
    await tester.tap(find.text('Load older requests'));
    await tester.pumpAndSettle();
    expect(adapter.calls.last.queryParameters['before'], 7);
    await tester.scrollUntilVisible(find.text('Kind of Blue'), 150,
      scrollable: find.descendant(of: find.byType(ListView), matching: find.byType(Scrollable)));
    await tester.tap(find.text('Kind of Blue'));
    await tester.pumpAndSettle();
    expect(find.text('Morgan'), findsOneWidget);
    expect(find.text('We already have this edition.'), findsOneWidget);
    expect(find.text('Open the title to check current availability.'), findsOneWidget);
    await tester.tap(find.text('View title'));
    await tester.pumpAndSettle();
    expect(find.textContaining('/detail/album/album-1?'), findsOneWidget);
    expect(find.textContaining('instance_id=music-1'), findsOneWidget);
  });

  testWidgets('search and filters combine on the server and clear together', (tester) async {
    final adapter = _HistoryAdapter();
    await _pump(tester, adapter);
    await tester.enterText(find.byType(TextField), 'Project');
    await tester.pump(const Duration(milliseconds: 350));
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Media type'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Book').last);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Decision'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Approved').last);
    await tester.pumpAndSettle();
    await tester.tap(find.byTooltip('Requester'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Sam').last);
    await tester.pumpAndSettle();
    expect(adapter.calls.last.queryParameters, {
      'q': 'Project', 'media_type': 'book', 'decision': 'approved', 'user_id': 3, 'limit': 50,
    });
    expect(find.text('Project Hail Mary'), findsOneWidget);
    await tester.tap(find.text('Project Hail Mary'));
    await tester.pumpAndSettle();
    expect(find.text('Alex (eBook)\nSam (Audiobook)'), findsOneWidget);
    expect(find.text('No reviewer recorded'), findsOneWidget);
    Navigator.of(tester.element(find.text('No reviewer recorded'))).pop();
    await tester.pumpAndSettle();
    await tester.tap(find.text('Clear filters'));
    await tester.pumpAndSettle();
    expect(adapter.calls.last.queryParameters, {'limit': 50});
    expect(find.text('Dune'), findsOneWidget);
  });

  testWidgets('late responses cannot replace a new search', (tester) async {
    final adapter = _HistoryAdapter();
    await _pump(tester, adapter);
    final slow = Completer<ResponseBody>();
    adapter.delayed = slow;
    await tester.enterText(find.byType(TextField), 'old');
    await tester.pump(const Duration(milliseconds: 350));
    adapter.delayed = null;
    await tester.enterText(find.byType(TextField), 'Dune');
    await tester.pump(const Duration(milliseconds: 350));
    await tester.pumpAndSettle();
    slow.complete(ResponseBody.fromString(jsonEncode(historyFixturePage({})), 200,
      headers: {Headers.contentTypeHeader: [Headers.jsonContentType]}));
    await tester.pumpAndSettle();
    expect(find.descendant(of: find.byType(ListView), matching: find.text('Dune')), findsOneWidget);
    expect(find.text('Stranger Things'), findsNothing);
    expect(find.text('Project Hail Mary'), findsNothing);
  });

  testWidgets('failed refresh keeps history and failure never looks empty', (tester) async {
    final adapter = _HistoryAdapter();
    await _pump(tester, adapter);
    adapter.status = 500;
    await tester.tap(find.byTooltip('Refresh history'));
    await tester.pumpAndSettle();
    expect(find.text('Dune'), findsOneWidget);
    expect(find.text('Couldn’t refresh request history. Showing the last update.'), findsOneWidget);
    await tester.enterText(find.byType(TextField), 'new');
    await tester.pump(const Duration(milliseconds: 350));
    await tester.pumpAndSettle();
    expect(find.text('Couldn’t load request history. Try again.'), findsOneWidget);
    expect(find.text('No matching requests'), findsNothing);
    adapter.status = 200;
    await tester.tap(find.text('Retry'));
    await tester.pumpAndSettle();
    expect(find.text('No matching requests'), findsOneWidget);
  });

  testWidgets('deep-link Back returns to approvals and compact layout stays usable', (tester) async {
    await _pump(tester, _HistoryAdapter(), size: const Size(340, 650));
    expect(tester.takeException(), isNull);
    await tester.tap(find.byTooltip('Back'));
    await tester.pumpAndSettle();
    expect(find.text('Approvals destination'), findsOneWidget);
  });

  test('title links preserve library identity and refuse unresolved legacy books', () {
    final movie = RequestHistoryItem.fromJson(historyFixtureRows.first);
    expect(movie.detailRoute, '/detail/movie/438631?instance_id=radarr-1');
    final book = RequestHistoryItem.fromJson({...historyFixtureRows[2], 'foreign_id': 'edition/123'});
    expect(book.detailRoute, startsWith('/detail/book/edition%2F123?'));
    expect(Uri.parse(book.detailRoute!).queryParameters['instance_id'], 'books-1');
    expect(RequestHistoryItem.fromJson({...historyFixtureRows[2], 'catalog_provider': 'openlibrary'}).detailRoute, isNull);
    expect(RequestHistoryItem.fromJson({...historyFixtureRows[2], 'instance_id': ''}).detailRoute, isNull);
  });
}

Future<void> _pump(WidgetTester tester, _HistoryAdapter adapter, {Size size = const Size(390, 844)}) async {
  tester.view.physicalSize = size;
  tester.view.devicePixelRatio = 1;
  addTearDown(tester.view.reset);
  final router = GoRouter(initialLocation: '/approvals/history', routes: [
    GoRoute(path: '/approvals/history', builder: (_, __) => const RequestHistoryScreen()),
    GoRoute(path: '/approvals', builder: (_, __) => const Scaffold(body: Text('Approvals destination'))),
    GoRoute(path: '/detail/:type/:id', builder: (_, state) => Scaffold(body: Text(state.uri.toString()))),
  ]);
  addTearDown(router.dispose);
  await tester.pumpWidget(ProviderScope(overrides: [
    backendClientProvider.overrideWithValue(Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = adapter),
  ], child: MaterialApp.router(theme: AppTheme.dark, routerConfig: router)));
  await tester.pumpAndSettle();
}

class _HistoryAdapter implements HttpClientAdapter {
  final calls = <RequestOptions>[];
  int status = 200;
  Completer<ResponseBody>? delayed;

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    calls.add(options);
    if (delayed != null) return delayed!.future;
    return ResponseBody.fromString(jsonEncode(historyFixturePage(options.queryParameters)), status,
      headers: {Headers.contentTypeHeader: [Headers.jsonContentType]});
  }

  @override
  void close({bool force = false}) {}
}
