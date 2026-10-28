import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/metadata/metadata_presentation.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/websocket_client.dart';
import 'package:cantinarr/core/providers/library_refresh_provider.dart';
import 'package:cantinarr/core/providers/realtime_provider.dart';
import 'package:cantinarr/core/storage/metadata_preferences.dart';
import 'package:cantinarr/features/dashboard/data/release_event.dart';
import 'package:cantinarr/features/discover/logic/discover_session.dart';
import 'package:cantinarr/features/downloads/data/downloads_activity.dart';
import 'package:cantinarr/features/media_detail/data/tv_library_service.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _Adapter implements HttpClientAdapter {
  final calls = <RequestOptions>[];
  Completer<void>? frenchGate;
  bool fail = false;
  bool transportFailure = false;
  bool unavailable = false;
  int? accessStatus;

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    calls.add(options);
    if (transportFailure) {
      throw DioException(
          requestOptions: options, type: DioExceptionType.connectionError);
    }
    final language = options.queryParameters['metadata_language'];
    if (language == 'fr-FR') await frenchGate?.future;
    if (accessStatus != null) {
      return ResponseBody.fromString(
          '{"code":"metadata_access_unavailable"}', accessStatus!,
          headers: {
            Headers.contentTypeHeader: [Headers.jsonContentType]
          });
    }
    if (fail)
      return ResponseBody.fromString('{}', 503, headers: {
        Headers.contentTypeHeader: [Headers.jsonContentType]
      });
    final dynamic body = options.path == '/api/requests/tv-library'
        ? {'instance_id': 'tv', 'series_id': 5, 'tmdb_id': 99}
        : unavailable
            ? {'presentations': {}, 'unavailable': {}}
            : {
                'presentations': {
                  for (final reference
                      in (options.data as Map)['references'] as List)
                    reference['key']: {
                      'title': language == 'fr-FR'
                          ? 'Titre français'
                          : 'English title'
                    },
                }
              };
    return ResponseBody.fromString(jsonEncode(body), 200, headers: {
      Headers.contentTypeHeader: [Headers.jsonContentType]
    });
  }

  @override
  void close({bool force = false}) {}
}

Dio _dio(_Adapter adapter) =>
    Dio(BaseOptions(baseUrl: 'http://fixture'))..httpClientAdapter = adapter;

void main() {
  test('visible references coalesce, deduplicate and obey the batch limit',
      () async {
    final adapter = _Adapter();
    final client = MetadataPresentationClient(_dio(adapter), 'fr-FR');
    addTearDown(client.dispose);
    final references =
        List.generate(103, (i) => MetadataReference.catalog('movie', i + 1));
    final results = await Future.wait([
      for (final reference in references) client.load(reference),
      client.load(references.first),
    ]);
    expect(results, hasLength(104));
    expect(results.every((r) => r?.title == 'Titre français'), isTrue);
    expect(adapter.calls.map((c) => (c.data['references'] as List).length),
        [50, 50, 3]);
    expect(
        adapter.calls
            .every((c) => c.queryParameters['metadata_language'] == 'fr-FR'),
        isTrue);
    await client.load(references.first);
    expect(adapter.calls, hasLength(3));
  });

  test('unsupported servers and invalid references make no projection request',
      () async {
    final adapter = _Adapter();
    final legacy = MetadataPresentationClient(_dio(adapter), null);
    final enabled = MetadataPresentationClient(_dio(adapter), 'fr-FR');
    addTearDown(legacy.dispose);
    addTearDown(enabled.dispose);
    expect(
        await legacy.load(const MetadataReference.catalog('movie', 1)), isNull);
    expect(await enabled.load(const MetadataReference.catalog('music', 1)),
        isNull);
    expect(await enabled.load(const MetadataReference.native('tv', '', 5)),
        isNull);
    expect(adapter.calls, isEmpty);
  });

  test('failed enrichment retains source fallback and can retry', () async {
    final adapter = _Adapter()..fail = true;
    final client = MetadataPresentationClient(_dio(adapter), 'fr-FR');
    addTearDown(client.dispose);
    const reference = MetadataReference.catalog('movie', 1);
    expect(await client.load(reference), isNull);
    adapter.fail = false;
    expect((await client.load(reference))?.title, 'Titre français');
  });

  testWidgets('switching locale rejects a delayed old-language overlay',
      (tester) async {
    final adapter = _Adapter()..frenchGate = Completer<void>();
    final language = StateProvider<String?>((_) => 'fr-FR');
    final container = ProviderContainer(overrides: [
      backendClientProvider.overrideWithValue(_dio(adapter)),
      discoverSessionProvider.overrideWithValue('viewer'),
      realtimeEventsProvider.overrideWithValue(const Stream<WsEvent>.empty()),
      effectiveMetadataLanguageProvider
          .overrideWith((ref) => ref.watch(language)),
    ]);
    addTearDown(container.dispose);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
          home: MetadataPresentationBuilder(
        reference: const MetadataReference.catalog('movie', 1),
        builder: (presentation) => Text(presentation?.title ?? 'Source title'),
      )),
    ));
    await tester.pump(const Duration(milliseconds: 1));
    expect(find.text('Source title'), findsOneWidget);
    container.read(language.notifier).state = 'en-US';
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(find.text('English title'), findsOneWidget);
    adapter.frenchGate!.complete();
    await tester.pumpAndSettle();
    expect(find.text('English title'), findsOneWidget);
    expect(find.text('Titre français'), findsNothing);
  });

  test('native fallback requires an authorized response even in a fresh client',
      () async {
    for (final transportFailure in [false, true]) {
      final adapter = _Adapter()
        ..fail = !transportFailure
        ..transportFailure = transportFailure;
      final client = MetadataPresentationClient(_dio(adapter), 'fr-FR');
      const native = MetadataReference.native('movie', 'radarr', 5);
      const catalog = MetadataReference.catalog('movie', 1);
      await Future.wait([
        expectLater(
            client.load(native),
            throwsA(isA<MetadataAccessUnavailable>()
                .having((error) => error.denied, 'denied', false))),
        expectLater(client.load(catalog), completion(isNull)),
      ]);
      adapter
        ..fail = false
        ..transportFailure = false
        ..unavailable = true;
      expect(await client.load(native), isNull,
          reason:
              'authorized translation outage preserves the source fallback');
      client.dispose();
    }
  });

  test('access and policy refusals remain errors, not source fallbacks',
      () async {
    for (final status in [401, 403, 404, 503]) {
      final adapter = _Adapter()..accessStatus = status;
      final client = MetadataPresentationClient(_dio(adapter), 'fr-FR');
      await expectLater(
          client.load(const MetadataReference.catalog('movie', 1)),
          throwsA(isA<MetadataAccessUnavailable>()
              .having((error) => error.denied, 'denied', status != 503)));
      client.dispose();
    }
  });

  test('unavailable translation is not cached for an hour', () async {
    final adapter = _Adapter()..unavailable = true;
    final client = MetadataPresentationClient(_dio(adapter), 'fr-FR');
    addTearDown(client.dispose);
    const reference = MetadataReference.catalog('movie', 1);
    expect(await client.load(reference), isNull);
    adapter.unavailable = false;
    expect((await client.load(reference))?.title, 'Titre français');
    expect(adapter.calls, hasLength(2));
  });

  testWidgets('mounted native references refresh without polling catalog text',
      (tester) async {
    final adapter = _Adapter();
    var now = DateTime.utc(2026, 10, 27);
    final client =
        MetadataPresentationClient(_dio(adapter), 'fr-FR', now: () => now);
    final container = ProviderContainer(overrides: [
      metadataPresentationClientProvider.overrideWithValue(client),
    ]);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
          home: Column(children: [
        MetadataPresentationBuilder(
          reference: const MetadataReference.native('tv', 'tv', 5),
          builder: (p) => Text(p?.title ?? 'Source series'),
        ),
        MetadataPresentationBuilder(
          reference: const MetadataReference.catalog('movie', 1),
          builder: (p) => Text(p?.title ?? 'Source movie'),
        ),
      ])),
    ));
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(find.text('Titre français'), findsNWidgets(2));
    expect(adapter.calls, hasLength(1));

    adapter.frenchGate = Completer<void>();
    now = now.add(const Duration(seconds: 31));
    await tester.pump(const Duration(seconds: 30));
    await tester.pump(const Duration(milliseconds: 1));
    expect(adapter.calls, hasLength(2));
    expect(adapter.calls.last.data['references'], [
      {
        'key': 'tv:tv:5:0',
        'media_type': 'tv',
        'instance_id': 'tv',
        'record_id': 5
      },
    ]);
    expect(find.text('Titre français'), findsNWidgets(2),
        reason: 'same-session refresh retains the confirmed presentation');
    expect(find.text('Source series'), findsNothing);
    adapter.frenchGate!.complete();
    await tester.pumpAndSettle();
    await tester.pumpWidget(const SizedBox());
    await tester.pump();
    now = now.add(const Duration(minutes: 2));
    await tester.pump(const Duration(minutes: 2));
    expect(adapter.calls, hasLength(2),
        reason: 'unmounted rows stop refreshing');
    container.dispose();
    client.dispose();
  });

  testWidgets(
      'library changes invalidate projections and policy blindness offers retry',
      (tester) async {
    final adapter = _Adapter();
    final events = StreamController<WsEvent>.broadcast();
    final container = ProviderContainer(overrides: [
      backendClientProvider.overrideWithValue(_dio(adapter)),
      discoverSessionProvider.overrideWithValue('viewer'),
      realtimeEventsProvider.overrideWithValue(events.stream),
      effectiveMetadataLanguageProvider.overrideWithValue('fr-FR'),
    ]);
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
          locale: const Locale('fr'),
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(
              body: MetadataPresentationBuilder(
            reference: const MetadataReference.native('tv', 'tv', 5),
            builder: (presentation) =>
                Text(presentation?.title ?? 'Source series'),
          ))),
    ));
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(find.text('Titre français'), findsOneWidget);
    final firstCalls = adapter.calls.length;
    container.read(libraryRefreshTickProvider.notifier).state++;
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(adapter.calls.length, greaterThan(firstCalls));
    expect(find.text('Titre français'), findsOneWidget);

    adapter.accessStatus = 503;
    events.add(const WsEvent(type: 'request_updated', data: {}));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(find.text('Titre français'), findsNothing);
    expect(find.text('Source series'), findsNothing);
    expect(find.text('Vérification impossible · Réessayer'), findsOneWidget);
    adapter.accessStatus = null;
    adapter.transportFailure = true;
    await tester.tap(find.text('Vérification impossible · Réessayer'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(find.text('Vérification impossible · Réessayer'), findsOneWidget);
    expect(find.text('Source series'), findsNothing,
        reason:
            'failed transport cannot reauthorize a previously refused source');
    expect(find.text('Titre français'), findsNothing);
    adapter.transportFailure = false;
    await tester.tap(find.text('Vérification impossible · Réessayer'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(find.text('Titre français'), findsOneWidget);

    adapter.accessStatus = 403;
    events.add(const WsEvent(type: 'request_updated', data: {}));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 1));
    await tester.pumpAndSettle();
    expect(find.text('Titre français'), findsNothing);
    expect(find.text('Source series'), findsNothing);
    expect(find.text('Vérification impossible · Réessayer'), findsNothing);
    await tester.pumpWidget(const SizedBox());
    container.dispose();
    await events.close();
  });

  test('download presentation preserves source names and controls', () {
    final group = DownloadContentGroup.fromJson({
      'id': 'group',
      'title': 'Source movie',
      'media_type': 'movie',
      'presentation': {'title': 'Titre français', 'poster_path': '/fr.jpg'},
      'job_ids': ['job'],
      'children': [
        {
          'id': 'episode',
          'title': 'Source episode',
          'season': 2,
          'episode': 7,
          'presentation': {'title': 'Épisode français'},
          'job_ids': ['job']
        },
      ],
    });
    expect(group.title, 'Source movie');
    expect(group.displayTitle, 'Titre français');
    expect(group.displayArtwork, 'https://image.tmdb.org/t/p/w185/fr.jpg');
    expect(group.jobIds, ['job']);
    expect(group.children.single.title, 'Source episode');
    expect(group.children.single.label, 'E07 · Épisode français');
    expect(group.children.single.jobIds, ['job']);
  });

  test('calendar retains native episode identity and source numbering', () {
    final event = releaseEventFromSonarr({
      'id': 123,
      'seriesId': 5,
      'seasonNumber': 2,
      'episodeNumber': 7,
      'title': 'Source episode',
      'series': {'title': 'Source series'},
      'airDateUtc': '2026-10-27T20:00:00Z',
    }, {}, instanceId: 'tv')!;
    expect(event.episodeId, 123);
    expect(event.seriesId, 5);
    expect(event.title, 'Source series');
    expect(event.subtitle, 'S02E07 • Source episode');
    expect(
        event.episodeSubtitle('Épisode français'), 'S02E07 • Épisode français');
    expect(event.episodeSubtitle(null), event.subtitle);
  });

  test('native TV service carries locale and invalidates its cached detail',
      () async {
    final adapter = _Adapter();
    final service = TVLibraryService(
        dio: _dio(adapter),
        libraryId: 'tv',
        seriesId: 5,
        metadataLanguage: 'fr-FR');
    await service.load();
    expect(service.current?.catalogId, 99);
    expect(adapter.calls.single.queryParameters, {
      'instance_id': 'tv',
      'series_id': 5,
      'metadata_language': 'fr-FR',
    });
    service.updateMetadataLanguage('en-US');
    expect(service.current, isNull);
    await service.load();
    expect(adapter.calls.last.queryParameters['metadata_language'], 'en-US');
  });
}
