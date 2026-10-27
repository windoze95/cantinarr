import 'package:cantinarr/features/media_availability/ui/request_scope_summary.dart';
import 'package:cantinarr/features/media_availability/ui/request_intent_summary.dart';
import 'package:cantinarr/features/settings/data/request_history_service.dart';
import 'package:cantinarr/l10n/app_localizations.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/search_library_status.dart';
import 'package:cantinarr/features/media_availability/ui/library_access_only_dialog.dart';
import 'package:cantinarr/features/media_availability/ui/media_availability_panel.dart';
import 'package:cantinarr/features/media_availability/ui/follow_seasons_dialog.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:cantinarr/features/request/logic/request_provider.dart';
import 'package:cantinarr/features/settings/data/instance_api_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('recorded intent renders in English and French without live availability', (tester) async {
    final history = RequestHistoryItem.fromJson({'id': 9,
      'decision': 'approved', 'media_type': 'tv',
      'request_mode': 'missing', 'source_seasons': [1, 3],
      'request_scope': {
        'missing_episodes': [for (final n in [2, 3, 5]) {'tmdb_id': n, 'season_number': 1, 'episode_number': n}],
        'initially_satisfied_episodes': [{'tmdb_id': 1, 'season_number': 1, 'episode_number': 1}],
      },
      'availability': {'status': 'available'}});
    expect(history.decisionLabel, 'Approved');
    for (final lang in ['en', 'fr']) {
      await tester.pumpWidget(MaterialApp(locale: Locale(lang),
        localizationsDelegates: AppLocalizations.localizationsDelegates,
        supportedLocales: AppLocalizations.supportedLocales,
        home: Scaffold(body: Column(children: [
          RequestIntentSummary(requestMode: history.requestMode, sourceSeasons: history.sourceSeasons),
          RequestScopeSummary(scope: history.requestScope!),
        ]))));
      await tester.pumpAndSettle();
      expect(find.text(lang == 'en' ? 'Request missing items' : 'Demander les éléments manquants'), findsOneWidget);
      expect(find.text(lang == 'en' ? 'Selected seasons: 1, 3' : 'Saisons sélectionnées : 1, 3'), findsOneWidget);
      expect(find.text(lang == 'en' ? 'Missing when requested: 3 episodes' : 'Manquants lors de la demande : 3 épisodes'), findsOneWidget);
      expect(find.text(lang == 'en' ? 'Season 1: episodes 2-3, 5' : 'Saison 1 : épisodes 2-3, 5'), findsOneWidget);
      expect(find.text(lang == 'en' ? 'Already available and excluded: 1 episode' : 'Déjà disponibles et exclus : 1 épisode'), findsOneWidget);
      expect(find.text('Available'), findsNothing);
    }
  });

  testWidgets('unknown coverage keeps the explicit library copy escape', (tester) async {
    final notifier = RequestNotifier(service: RequestService(backendDio: Dio()),
      tmdbId: 1, mediaType: MediaType.movie);
    addTearDown(notifier.dispose);
    notifier.state = const RequestState(hasStatus: true,
      availability: MediaAvailability(status: RequestStatus.unavailable, isKnown: false));
    var copies = 0;
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: MediaAvailabilityPanel(
      notifier: notifier, onLibraryCopy: () => copies++))));
    expect(find.text('Request in library'), findsOneWidget);
    await tester.tap(find.text('Request in library'));
    expect(copies, 1);
  });

  test('capability defaults off and survives token/config copies', () {
    expect(ServerConfig.fromJson({}).mediaLibraryAvailability, isFalse);
    final config = ServerConfig.fromJson({'media_library_availability': true});
    final connection = BackendConnection(
        serverUrl: 'http://localhost',
        accessToken: '',
        refreshToken: '',
        mediaLibraryAvailability: config.mediaLibraryAvailability);
    expect(connection.copyWith(accessToken: 'new').mediaLibraryAvailability,
        isTrue);
    expect(
        connection
            .copyWith(mediaLibraryAvailability: false)
            .mediaLibraryAvailability,
        isFalse);
  });

  test('scan libraries do not change shared account libraries', () {
    final config = MediaServerConfig.fromJson({
      'library_ids': ['shared'],
      'availability_scan': {
        'enabled': true,
        'library_ids': ['movies']
      }
    });
    expect(config.toJson()['library_ids'], ['shared']);
    expect(config.availabilityScan!.libraryIds, ['movies']);
    expect(const MediaServerConfig().toJson(),
        isNot(contains('availability_scan')));
  });

  test('positive media coverage overlays cards while retaining pending target',
      () {
    final detail = RequestStatusDetail.fromJson({
      'status': 'pending',
      'instance_statuses': {
        'target': {'status': 'pending'}
      },
      'availability': {
        'status': 'available',
        'status_known': true,
        'is_4k': true
      }
    });
    expect(detail.status, RequestStatus.pending);
    expect(detail.instanceStatuses['target'], RequestStatus.pending);
    expect(tvLibraryStatus(detail)!.label, 'Available');
    expect(tvLibraryStatus(detail)!.is4K, isTrue);
    final partial = RequestStatusDetail.fromJson({
      'status': 'unavailable',
      'availability': {
        'status': 'partial',
        'status_known': true,
        'seasons': [
          {
            'season_number': 1,
            'episode_file_count': 3,
            'episode_count': 8,
            'status': 'partial'
          }
        ]
      }
    });
    expect(tvLibraryStatus(partial)!.episodeSubtitle, '3/8 eps');
    expect(
        tvLibraryStatus(RequestStatusDetail.fromJson({
          'status': 'unavailable',
          'availability': {'status': 'unavailable', 'status_known': false}
        }))!
            .label,
        'Status unknown');
  });

  test(
      'copy intent is explicit and normal requests keep old wire compatibility',
      () async {
    final bodies = <Map>[];
    final dio = Dio()
      ..interceptors.add(InterceptorsWrapper(onRequest: (o, h) {
        bodies.add(Map.from(o.data as Map));
        h.resolve(Response(
            requestOptions: o, data: {'status': 'pending'}, statusCode: 200));
      }));
    final service = RequestService(backendDio: dio);
    await service.request(
        tmdbId: 1, mediaType: MediaType.movie, instanceId: 'target');
    await service.request(
        tmdbId: 1,
        mediaType: MediaType.movie,
        instanceId: 'target',
        requestMode: 'library_copy');
    expect(bodies.first, isNot(contains('request_mode')));
    expect(bodies.last['request_mode'], 'library_copy');
    expect(bodies.last['instance_id'], 'target');
  });

  testWidgets(
      'explicit library access draft preselects only the sole eligible provider and writes on Save',
      (tester) async {
    final writes = <Map>[];
    final dio = _householdDio(
        writes, [_server('plex', 'p1'), _server('jellyfin', 'j1')]);
    await _pumpHousehold(tester, dio);
    expect(writes, isEmpty);
    expect(
        tester
            .widget<CheckboxListTile>(
                find.widgetWithText(CheckboxListTile, 'p1'))
            .value,
        isTrue);
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(writes.single['associations'], [
      {
        'instance_id': 'p1',
        'library_ids': ['movies']
      }
    ]);
  });

  testWidgets('several eligible servers require an explicit selection',
      (tester) async {
    final writes = <Map>[];
    await _pumpHousehold(tester,
        _householdDio(writes, [_server('plex', 'p1'), _server('plex', 'p2')]));
    expect(
        tester
            .widget<CheckboxListTile>(
                find.widgetWithText(CheckboxListTile, 'p1'))
            .value,
        isFalse);
    expect(
        tester
            .widget<FilledButton>(find.widgetWithText(FilledButton, 'Save'))
            .onPressed,
        isNull);
    expect(writes, isEmpty);
  });

  testWidgets(
      'linked and unreadable servers never become new household associations',
      (tester) async {
    final writes = <Map>[];
    await _pumpHousehold(
        tester,
        _householdDio(writes, [
          {..._server('plex', 'p1'), 'linked': true},
          {..._server('plex', 'p2'), 'error': 'unreachable'}
        ]));
    for (final id in ['p1', 'p2']) {
      expect(
          tester
              .widget<CheckboxListTile>(
                  find.widgetWithText(CheckboxListTile, id))
              .onChanged,
          isNull);
    }
    expect(writes, isEmpty);
  });

  testWidgets('follow selected seasons never preselects future seasons',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: FollowSeasonsDialog(seasons: [
      Season.fromJson({'id': 1, 'season_number': 1, 'name': 'Season 1'}),
      Season.fromJson({'id': 2, 'season_number': 2, 'name': 'Season 2'}),
    ]))));
    expect(tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
        isNull);
    await tester.tap(find.text('Season 1'));
    await tester.pump();
    expect(
        tester
            .widget<CheckboxListTile>(
                find.widgetWithText(CheckboxListTile, 'Season 2'))
            .value,
        isFalse);
    expect(tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
        isNotNull);
  });

  testWidgets('stop following addresses only the selected saved request',
      (tester) async {
    final paths = <String>[];
    final dio = Dio()
      ..interceptors.add(InterceptorsWrapper(onRequest: (o, h) {
        paths.add('${o.method} ${o.path}');
        h.resolve(Response(
            requestOptions: o, data: {'status': 'available'}, statusCode: 200));
      }));
    final notifier = RequestNotifier(
        service: RequestService(backendDio: dio),
        tmdbId: 4,
        mediaType: MediaType.tv);
    addTearDown(notifier.dispose);
    notifier.state = const RequestState(
        status: RequestStatus.pending,
        availability: MediaAvailability(status: RequestStatus.available),
        following: [
          MediaFollowing(
              requestId: 7, state: 'paused', seasons: [1], canStop: true)
        ]);
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: MediaAvailabilityPanel(notifier: notifier))));
    expect(
        find.text('Request in this library: Pending Approval'), findsOneWidget);
    expect(find.text('Episode following paused'), findsOneWidget);
    await tester.tap(find.text('Stop following'));
    await tester.pumpAndSettle();
    expect(paths.first, 'POST /api/requests/7/follow/stop');
    expect(paths.any((p) => p.contains('/instances/')), isFalse);
  });
}

Map<String, dynamic> _server(String type, String id) => {
      'instance_id': id,
      'name': id,
      'service_type': type,
      'enabled': true,
      'linked': false,
      'library_ids': ['movies'],
      'libraries': [
        {'id': 'movies', 'name': 'Films', 'collection_type': 'movies'}
      ]
    };
Dio _householdDio(List<Map> writes, List<Map<String, dynamic>> servers) => Dio()
  ..interceptors.add(InterceptorsWrapper(onRequest: (o, h) {
    if (o.method == 'PUT') writes.add(Map.from(o.data as Map));
    h.resolve(Response(
        requestOptions: o,
        statusCode: 200,
        data: {'associations': [], 'servers': servers}));
  }));
Future<void> _pumpHousehold(WidgetTester tester, Dio dio) async {
  await tester.pumpWidget(ProviderScope(
      overrides: [backendClientProvider.overrideWithValue(dio)],
      child: MaterialApp(
          home: Scaffold(
              body: Builder(
                  builder: (context) => TextButton(
                      onPressed: () => showDialog<void>(
                          context: context,
                          builder: (_) => const LibraryAccessOnlyDialog(
                              userId: 4, initialProvider: 'plex')),
                      child: const Text('Open')))))));
  await tester.tap(find.text('Open'));
  await tester.pumpAndSettle();
}
