import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:cantinarr/features/request/logic/request_quota_provider.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/media_detail/ui/season_table.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/logic/request_provider.dart';

/// The season picker must be request-capable only for users the server allows
/// to choose seasons (can_choose_season). For everyone else the server ignores
/// an explicit season list, so showing checkboxes and a submit button would be
/// a silent no-op.
void main() {
  const seasons = [
    Season(id: 1, seasonNumber: 1, name: 'Season 1', episodeCount: 10),
    Season(id: 2, seasonNumber: 2, name: 'Season 2', episodeCount: 8),
  ];

  RequestNotifier notifier({RequestState? state, _Adapter? adapter}) =>
      RequestNotifier(
        service: RequestService(backendDio: Dio()
          ..httpClientAdapter = adapter ?? _Adapter()),
        tmdbId: 123,
        mediaType: MediaType.tv,
      )..state = state ?? const RequestState(hasStatus: true);

  Widget host(SeasonTable table) => ProviderScope(overrides: [requestQuotasSupportedProvider.overrideWithValue(false)], child: MaterialApp(
        home: Scaffold(body: SingleChildScrollView(child: table)),
      ));

  testWidgets('season choice allowed: checkboxes, chips and submit render',
      (tester) async {
    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
    )));

    expect(find.byType(Checkbox), findsNWidgets(2));
    expect(find.text('All'), findsOneWidget);
    expect(find.text('Select seasons to request'), findsOneWidget);
  });

  testWidgets('season choice not allowed: table is status-only',
      (tester) async {
    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(),
      canRequest: false,
    )));

    // Season rows still render (status display)...
    expect(find.text('Season 1'), findsOneWidget);
    expect(find.text('Season 2'), findsOneWidget);
    // ...but every request affordance is gone.
    expect(find.byType(Checkbox), findsNothing);
    expect(find.text('All'), findsNothing);
    expect(find.text('Select seasons to request'), findsNothing);
    expect(find.byType(ElevatedButton), findsNothing);
  });

  testWidgets('the only unreleased, requested season cannot be selected',
      (tester) async {
    final adapter = _Adapter();
    final n = notifier(adapter: adapter, state: const RequestState(
      hasStatus: true,
      status: RequestStatus.requested,
      seasons: [RequestSeasonStatus(
        seasonNumber: 1, status: RequestStatus.requested,
      )],
    ));
    await tester.pumpWidget(host(SeasonTable(
      seasons: const [Season(
        id: 1, seasonNumber: 1, name: 'Season 1',
        episodeCount: 8, airDate: '2026-12-01',
      )],
      notifier: n,
    )));

    expect(find.text('Requested'), findsOneWidget);
    final checkbox = tester.widget<Checkbox>(find.byType(Checkbox));
    expect(checkbox.value, isTrue);
    expect(checkbox.onChanged, isNull);
    expect(find.byType(ActionChip), findsNothing);
    expect(find.byType(ElevatedButton), findsNothing);
    await tester.tap(find.text('Season 1 · 2026'));
    await tester.pump();
    expect(adapter.posts, isEmpty);
  });

  for (final status in [RequestStatus.available, RequestStatus.requested,
      RequestStatus.downloading, RequestStatus.pending]) {
    testWidgets('${status.name} without a breakdown is status-only',
        (tester) async {
      await tester.pumpWidget(host(SeasonTable(
        seasons: seasons,
        notifier: notifier(state: RequestState(hasStatus: true, status: status)),
      )));
      expect(tester.widgetList<Checkbox>(find.byType(Checkbox))
          .every((c) => c.value == true && c.onChanged == null), isTrue);
      expect(find.byType(ActionChip), findsNothing);
      expect(find.byType(ElevatedButton), findsNothing);
    });
  }

  testWidgets('pending approval also blocks a stale season breakdown',
      (tester) async {
    await tester.pumpWidget(host(SeasonTable(
      seasons: seasons,
      notifier: notifier(state: const RequestState(
        hasStatus: true, status: RequestStatus.pending,
        seasons: [RequestSeasonStatus(seasonNumber: 1)],
      )),
    )));
    expect(find.text('Pending'), findsNWidgets(2));
    expect(find.byType(ElevatedButton), findsNothing);
  });

  testWidgets('quick selection and submission include only actionable seasons',
      (tester) async {
    final statuses = [RequestStatus.available, RequestStatus.requested,
      RequestStatus.downloading, RequestStatus.pending, RequestStatus.partial,
      RequestStatus.unavailable, RequestStatus.denied];
    final adapter = _Adapter();
    await tester.pumpWidget(host(SeasonTable(
      // Season 8 is newly announced and absent from Sonarr's breakdown.
      seasons: [for (var i = 1; i <= 8; i++) Season(
        id: i, seasonNumber: i, name: 'Season $i', airDate: '2100-01-01',
      )],
      notifier: notifier(adapter: adapter, state: RequestState(
        hasStatus: true, status: RequestStatus.requested,
        seasons: [for (var i = 0; i < statuses.length; i++)
          RequestSeasonStatus(seasonNumber: i + 1, status: statuses[i])],
      )),
    )));
    List<int> selected() {
      final boxes = tester.widgetList<Checkbox>(find.byType(Checkbox)).toList();
      return [for (var i = 0; i < boxes.length; i++)
        if (boxes[i].onChanged != null && boxes[i].value == true) i + 1];
    }
    await tester.tap(find.text('First'));
    await tester.pump();
    expect(selected(), [5]);
    await tester.tap(find.text('Latest'));
    await tester.pump();
    expect(selected(), [8]);
    await tester.tap(find.text('All'));
    await tester.pump();
    expect(selected(), [5, 6, 7, 8]);
    await tester.ensureVisible(find.text('Request 4 seasons'));
    await tester.tap(find.text('Request 4 seasons'));
    await tester.pumpAndSettle();
    expect(adapter.posts.single['seasons'], [5, 6, 7, 8]);
  });

  testWidgets('status changes remove selections and guard an old submit callback',
      (tester) async {
    final adapter = _Adapter();
    final n = notifier(adapter: adapter);
    await tester.pumpWidget(host(SeasonTable(seasons: seasons, notifier: n)));
    await tester.tap(find.text('All'));
    await tester.pump();
    final submit = tester.widget<ElevatedButton>(find.byType(ElevatedButton))
        .onPressed!;
    n.state = const RequestState(hasStatus: true, status: RequestStatus.requested);
    submit(); // A tap may arrive before the next frame paints the new status.
    await tester.pumpAndSettle();
    expect(adapter.posts, isEmpty);
    n.state = const RequestState(hasStatus: true);
    await tester.pump();
    expect(find.text('Select seasons to request'), findsOneWidget);
    expect(tester.widgetList<Checkbox>(find.byType(Checkbox))
        .every((c) => c.value == false), isTrue);
  });

  testWidgets('library changes clear selection and ignore an old status read',
      (tester) async {
    final adapter = _Adapter();
    final n = notifier(adapter: adapter);
    await tester.pumpWidget(host(SeasonTable(seasons: seasons, notifier: n)));
    await tester.tap(find.text('All'));
    await tester.pump();
    adapter.statusGate = Completer<ResponseBody>();
    final oldRead = n.checkStatus();
    await tester.pump();
    n.instanceId = 'sonarr-4k';
    final oldGate = adapter.statusGate!;
    adapter.statusGate = null;
    adapter.detail = {'status': 'unavailable'};
    final newRead = n.checkStatus();
    await tester.pumpAndSettle();
    await newRead;
    oldGate.complete(_response({'status': 'requested'}));
    await oldRead;
    await tester.pumpAndSettle();
    expect(n.state.status, RequestStatus.unavailable);
    expect(find.text('Select seasons to request'), findsOneWidget);
    expect(tester.widgetList<Checkbox>(find.byType(Checkbox))
        .every((c) => c.value == false), isTrue);
    await tester.tap(find.text('First'));
    await tester.pump();
    await tester.tap(find.text('Request 1 season'));
    await tester.pumpAndSettle();
    expect(adapter.posts.single['instance_id'], 'sonarr-4k');
    expect(adapter.posts.single['seasons'], [1]);
  });

  testWidgets('submission stays blocked through readback and a failed refresh',
      (tester) async {
    final adapter = _Adapter()..statusGate = Completer<ResponseBody>();
    final n = notifier(adapter: adapter);
    await tester.pumpWidget(host(SeasonTable(seasons: seasons, notifier: n)));
    await tester.tap(find.text('All'));
    await tester.pump();
    final submit = tester.widget<ElevatedButton>(find.byType(ElevatedButton))
        .onPressed!;
    submit();
    await tester.pumpAndSettle();
    expect(n.state.isCheckingStatus, isTrue);
    submit();
    expect(adapter.posts, hasLength(1));
    expect(tester.widgetList<Checkbox>(find.byType(Checkbox))
        .every((c) => c.onChanged == null), isTrue);
    adapter.statusGate!.complete(_response({'error': 'offline'}, code: 503));
    await tester.pumpAndSettle();
    expect(n.state.error, 'Could not check status');
    expect(n.state.hasStatus, isFalse);
    submit();
    await tester.pump();
    expect(adapter.posts, hasLength(1));
  });

  testWidgets('quota refusal preserves seasons until the user reduces them', (tester) async {
    final adapter = _Adapter()..quotaRefusal = true;
    final n = notifier(adapter: adapter);
    await tester.pumpWidget(host(SeasonTable(seasons: seasons, notifier: n)));
    await tester.tap(find.text('All'));
    await tester.pump();
    await tester.tap(find.text('Request 2 seasons'));
    await tester.pumpAndSettle();
    expect(n.state.error, isNull);
    expect(n.state.canReportProblem, isFalse);
    expect(find.descendant(of: find.byType(SnackBar),
        matching: find.text('TV season request limit reached.')), findsOneWidget);
    expect(find.textContaining('remaining'), findsNothing);
    expect(tester.widgetList<Checkbox>(find.byType(Checkbox)).every((c) => c.value == true), isTrue);
    await tester.tap(find.text('First'));
    await tester.pump();
    expect(find.text('Request 1 season'), findsOneWidget);
    expect(adapter.posts.single['seasons'], [1, 2]);
    await tester.pump(const Duration(seconds: 5));
    await tester.pumpAndSettle();
    expect(find.text('TV season request limit reached.'), findsNothing);
  });

  testWidgets('a coarse request also refreshes existing season rows',
      (tester) async {
    final adapter = _Adapter()..detail = {
      'status': 'requested',
      'seasons': [
        {'season_number': 1, 'status': 'requested'},
        {'season_number': 2, 'status': 'unavailable'},
      ],
    };
    final n = notifier(adapter: adapter, state: const RequestState(
      hasStatus: true, seasons: [RequestSeasonStatus(seasonNumber: 1)],
    ));
    await tester.pumpWidget(host(SeasonTable(seasons: seasons, notifier: n)));
    final request = n.request(seasonScope: 'first');
    await tester.pumpAndSettle();
    expect(await request, isTrue);
    final boxes = tester.widgetList<Checkbox>(find.byType(Checkbox)).toList();
    expect(boxes[0].value, isTrue);
    expect(boxes[0].onChanged, isNull);
    expect(boxes[1].onChanged, isNotNull);
    expect(adapter.posts, hasLength(1));
    expect(adapter.posts.single['season_scope'], 'first');
  });
}

ResponseBody _response(Map<String, dynamic> body, {int code = 200}) =>
    ResponseBody.fromString(jsonEncode(body), code,
      headers: {'content-type': ['application/json']});

class _Adapter implements HttpClientAdapter {
  final posts = <Map<String, dynamic>>[];
  Map<String, dynamic> detail = {'status': 'requested'};
  Completer<ResponseBody>? statusGate;
  bool quotaRefusal = false;

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? stream,
      Future<void>? cancelFuture) async {
    if (options.method == 'POST') {
      posts.add(Map<String, dynamic>.from(options.data as Map));
      if (quotaRefusal) {
        return _response({'code': 'request_quota_exceeded',
          'error': 'TV seasons: 2 requested, 1 remaining.',
          'allowances': [{'media_type': 'tv', 'count': 3, 'used': 2, 'requested_units': 2}]}, code: 429);
      }
      return _response({'status': 'requested'});
    }
    return statusGate?.future ?? Future.value(_response(detail));
  }

  @override
  void close({bool force = false}) {}
}
