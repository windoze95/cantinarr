import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/storage/locale_preferences.dart';
import 'package:cantinarr/core/storage/metadata_preferences.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/discover/data/discover_api_service.dart';
import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/discover/logic/browse_session_provider.dart';
import 'package:cantinarr/features/discover/logic/search_library_status.dart';
import 'package:cantinarr/features/media_detail/logic/arr_deep_link.dart';
import 'package:cantinarr/features/media_detail/logic/media_detail_provider.dart';
import 'package:cantinarr/features/person/logic/person_detail_provider.dart';
import 'package:cantinarr/features/shell/logic/shell_search_provider.dart';
import 'package:cantinarr/features/sonarr/data/sonarr_models.dart';
import 'package:dio/dio.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  setUp(() => SharedPreferences.setMockInitialValues({}));

  testWidgets(
      'System follows device changes; metadata override remains independent',
      (tester) async {
    tester.platformDispatcher.localesTestValue = const [Locale('fr', 'CA')];
    addTearDown(tester.platformDispatcher.clearLocalesTestValue);
    final container = await _container();
    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: Consumer(builder: (context, ref, _) {
        ref.watch(effectiveMetadataLanguageProvider);
        ref.watch(effectiveAppLocaleProvider);
        return const SizedBox();
      }),
    ));
    expect(container.read(effectiveAppLocaleProvider), const Locale('fr'));
    expect(container.read(effectiveMetadataLanguageProvider), 'fr-FR');
    await container
        .read(metadataLanguageProvider.notifier)
        .setLanguage('en-US');
    expect(container.read(effectiveAppLocaleProvider), const Locale('fr'));
    expect(container.read(effectiveMetadataLanguageProvider), 'en-US');
    await container.read(metadataLanguageProvider.notifier).setLanguage(null);
    tester.platformDispatcher.localesTestValue = const [Locale('de', 'DE')];
    await tester.pump();
    expect(container.read(effectiveAppLocaleProvider), const Locale('en'));
    expect(container.read(effectiveMetadataLanguageProvider), 'en-US');
    await container
        .read(appLocaleProvider.notifier)
        .setLocale(const Locale('fr'));
    expect(container.read(effectiveMetadataLanguageProvider), 'fr-FR');
    await tester.pump();
  });

  test(
      'saved override survives old servers and rapid changes persist the latest choice',
      () async {
    SharedPreferences.setMockInitialValues({'metadata_language': 'fr-FR'});
    final container = await _container(languages: const []);
    container.read(metadataLanguageProvider);
    await Future<void>.delayed(Duration.zero);
    expect(container.read(metadataLanguageProvider), 'fr-FR');
    expect(container.read(effectiveMetadataLanguageProvider), isNull);
    final notifier = container.read(metadataLanguageProvider.notifier);
    await Future.wait([
      notifier.setLanguage('en-US'),
      notifier.setLanguage('fr-FR'),
      notifier.setLanguage(null)
    ]);
    expect(
        (await SharedPreferences.getInstance())
            .containsKey('metadata_language'),
        isFalse);
    expect(() => notifier.setLanguage('xx'), throwsArgumentError);
    final config = ServerConfig.fromJson({
      'server_name': 'Test',
      'metadata_languages': ['en-US', 'fr-FR']
    });
    expect(config.metadataLanguages, ['en-US', 'fr-FR']);
    expect(ServerConfig.fromJson({'server_name': 'Old'}).metadataLanguages,
        isEmpty);
  });

  test(
      'language change keeps Dio and search query but replaces cached catalog sessions',
      () async {
    final container = await _container();
    await container
        .read(appLocaleProvider.notifier)
        .setLocale(const Locale('en'));
    final client = container.read(backendClientProvider);
    final adapter = _Adapter();
    client.httpClientAdapter = adapter;
    final cache = container.read(browseSessionProvider);
    final notifier = container.read(shellSearchProvider.notifier);
    notifier.updateSearch('Matrix');
    await container
        .read(metadataLanguageProvider.notifier)
        .setLanguage('fr-FR');
    await container.pump();
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(container.read(backendClientProvider), same(client));
    expect(container.read(browseSessionProvider), isNot(same(cache)));
    expect(container.read(shellSearchProvider.notifier), same(notifier));
    expect(notifier.state.searchQuery, 'Matrix');
    expect(notifier.state.searchResults.single.title, 'Matrice');
    expect(adapter.requests.last.queryParameters['metadata_language'], 'fr-FR');
  });

  test(
      'catalog reads send explicit immutable language without changing filters or country',
      () async {
    final adapter = _Adapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final french =
        DiscoverApiService(backendDio: dio, metadataLanguage: 'fr-FR');
    await french.discoverMovies(
        language: 'ko', watchRegion: 'CA', watchProviderIds: [8]);
    expect(adapter.requests.single.queryParameters,
        containsPair('metadata_language', 'fr-FR'));
    expect(adapter.requests.single.queryParameters,
        containsPair('with_original_language', 'ko'));
    expect(adapter.requests.single.queryParameters,
        containsPair('watch_region', 'CA'));
    await DiscoverApiService(backendDio: dio).fetchPopularMovies();
    expect(
        adapter.requests.last.queryParameters.containsKey('metadata_language'),
        isFalse);
    await french.fetchPopularMovies();
    expect(adapter.requests.last.queryParameters['metadata_language'], 'fr-FR');
  });

  test(
      'old-language detail and person responses cannot replace a newer language',
      () async {
    final old = _DelayedCatalog();
    final current = _Catalog();
    final detail =
        MediaDetailNotifier(api: old, id: 1, mediaType: MediaType.movie);
    final person = PersonDetailNotifier(api: old, id: 2);
    addTearDown(detail.dispose);
    addTearDown(person.dispose);
    final firstDetail = detail.load();
    final firstPerson = person.load();
    await detail.updateApi(current);
    await person.updateApi(current);
    expect(detail.state.title, 'Matrice');
    expect(detail.state.sourceTitle, 'The Matrix');
    old.ready.complete();
    await Future.wait([firstDetail, firstPerson]);
    expect(detail.state.title, 'Matrice');
    expect(person.state.person?.biography, 'Biographie française');
  });

  test(
      'translated titles preserve legacy series matching and canonical request labels',
      () {
    final item = MediaItem.fromTVJson({
      'id': 2,
      'name': 'Titre français',
      'source_name': 'English title',
      'first_air_date': '2020-01-01'
    });
    final series = SonarrSeries.fromJson(
        {'id': 9, 'title': 'English title', 'year': 2020, 'monitored': true});
    final status = buildSearchLibraryStatus(
        searchResults: [item], movies: [], series: [series]);
    expect(status.containsKey((MediaType.tv, 2)), isTrue);
    final detail = MovieDetail.fromJson(
        {'id': 1, 'title': 'Matrice', 'source_title': 'The Matrix'});
    expect(detail.title, 'Matrice');
    expect(detail.matchingTitle, 'The Matrix');
  });

  test('canonical source fallback never matches translated title text', () {
    final original = MovieDetail.fromJson({
      'id': 1,
      'title': 'Titre français',
      'original_title': 'Original title',
      'source_title': 'Original title',
    });
    expect(original.matchingTitle, 'Original title');
    expect(
        MediaItem.fromTVJson({
          'id': 2,
          'name': 'Titre français',
          'original_name': 'Original name',
          'source_name': 'Original name',
        }).matchingTitle,
        'Original name');
    expect(
        MovieDetail.fromJson({
          'id': 1,
          'title': 'English title',
          'original_title': 'Original title',
        }).matchingTitle,
        'English title');
    final untranslatedIdentity = TVDetail.fromJson({
      'id': 2,
      'name': 'Titre français',
      'source_name': '',
      'first_air_date': '2020-01-01',
    });
    expect(untranslatedIdentity.matchingTitle, isEmpty);
    expect(
        MediaDetailState(tvDetail: untranslatedIdentity).sourceTitle, isEmpty);
    final legacy = SonarrSeries.fromJson({
      'id': 9,
      'title': 'Titre français',
      'year': 2020,
      'monitored': true,
    });
    expect(
        matchSonarrSeries([legacy],
            tmdbId: 2, title: untranslatedIdentity.matchingTitle, year: 2020),
        isNull);
    final item = MediaItem.fromTVJson({
      'id': 2,
      'name': 'Titre français',
      'source_name': '',
      'first_air_date': '2020-01-01',
    });
    expect(
        buildSearchLibraryStatus(
            searchResults: [item], movies: [], series: [legacy]),
        isEmpty);
    final exact = SonarrSeries.fromJson({
      'id': 9,
      'tmdbId': 2,
      'title': 'Titre français',
      'year': 2020,
      'monitored': true,
    });
    expect(
        matchSonarrSeries([exact],
            tmdbId: 2, title: untranslatedIdentity.matchingTitle, year: 2020),
        same(exact));
    expect(
        buildSearchLibraryStatus(
            searchResults: [item], movies: [], series: [exact]),
        contains((MediaType.tv, 2)));
  });
}

Future<ProviderContainer> _container(
    {List<String> languages = const ['en-US', 'fr-FR']}) async {
  final container = ProviderContainer(overrides: [
    authProvider.overrideWith(() => _Auth(languages)),
  ]);
  addTearDown(container.dispose);
  await container.read(authProvider.future);
  return container;
}

class _Auth extends AuthNotifier {
  _Auth(this.languages);
  final List<String> languages;
  @override
  Future<AuthState> build() async => AuthState(
        connection: BackendConnection(
            serverUrl: 'http://localhost',
            accessToken: 'test',
            refreshToken: 'test',
            metadataLanguages: languages),
        user: const UserProfile(id: 1, username: 'tester', role: 'user'),
      );
}

class _Adapter implements HttpClientAdapter {
  final requests = <RequestOptions>[];
  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    requests.add(options);
    return ResponseBody.fromString(
        jsonEncode({
          'page': 1,
          'total_pages': 1,
          'results': [
            {
              'id': 1,
              'title': options.queryParameters['metadata_language'] == 'fr-FR'
                  ? 'Matrice'
                  : 'The Matrix',
              'media_type': 'movie'
            }
          ]
        }),
        200,
        headers: {
          'content-type': ['application/json']
        });
  }

  @override
  void close({bool force = false}) {}
}

class _Catalog extends DiscoverApiService {
  _Catalog() : super(backendDio: Dio());
  @override
  Future<MovieDetail> movieDetail(int id) async =>
      MovieDetail(id: id, title: 'Matrice', sourceTitle: 'The Matrix');
  @override
  Future<TmdbPage<MediaItem>> movieRecommendations(int id,
          {int page = 1}) async =>
      const TmdbPage(page: 1, totalPages: 1, totalResults: 0, results: []);
  @override
  Future<TmdbPage<MediaItem>> similarMovies(int id, {int page = 1}) =>
      movieRecommendations(id);
  @override
  Future<PersonDetail> personDetail(int id) async => PersonDetail.fromJson(
      {'id': id, 'name': 'Person', 'biography': 'Biographie française'});
  @override
  Future<List<PersonCredit>> personCredits(int id) async => [];
}

class _DelayedCatalog extends _Catalog {
  final ready = Completer<void>();
  @override
  Future<MovieDetail> movieDetail(int id) async {
    await ready.future;
    return MovieDetail(id: id, title: 'The Matrix');
  }

  @override
  Future<PersonDetail> personDetail(int id) async {
    await ready.future;
    return PersonDetail.fromJson(
        {'id': id, 'name': 'Person', 'biography': 'English biography'});
  }
}
