import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/settings/data/content_policy_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

const _catalogJson = {
  'movie': {
    'US': [
      {'certification': 'NR', 'order': 0},
      {'certification': 'G', 'order': 1, 'meaning': 'All ages'},
      {'certification': 'PG', 'order': 2, 'default': true},
      {'certification': 'PG-13', 'order': 3},
      {'certification': 'R', 'order': 4},
    ],
    'GB': [
      {'certification': 'U', 'order': 1},
      {'certification': 'PG', 'order': 2},
      {'certification': '12A', 'order': 3},
    ],
    'XX': [
      {'certification': 'A', 'order': 1},
    ],
  },
  'tv': {
    'US': [
      {'certification': 'NR', 'order': 0},
      {'certification': 'TV-Y', 'order': 1},
      {'certification': 'TV-Y7', 'order': 2},
      {'certification': 'TV-G', 'order': 3},
      {'certification': 'TV-PG', 'order': 4, 'default': true},
      {'certification': 'TV-MA', 'order': 6},
    ],
    'GB': [
      {'certification': 'U', 'order': 1},
      {'certification': 'PG', 'order': 2},
    ],
  },
  'source': 'tmdb',
};

void main() {
  test('ContentPolicy round-trips every key with integer genre ids', () {
    final policy = ContentPolicy.fromJson(const {
      'max_movie_rating': 'PG',
      'max_tv_rating': 'TV-PG',
      'rating_region': 'US',
      'block_unrated': false,
      'blocked_movie_genres': [27, 53],
      'blocked_tv_genres': [],
    });
    expect(policy.blockUnrated, isFalse);
    expect(policy.blockedMovieGenres, [27, 53]);
    expect(policy.blockedTvGenres, isEmpty);
    final json = policy.toJson();
    expect(json.keys, containsAll([
      'max_movie_rating',
      'max_tv_rating',
      'rating_region',
      'block_unrated',
      'blocked_movie_genres',
      'blocked_tv_genres',
    ]));
    expect(ContentPolicy.fromJson(json), policy);
    expect(
      policy,
      isNot(const ContentPolicy(
          maxMovieRating: 'PG', maxTvRating: 'TV-PG', ratingRegion: 'US')),
    );
  });

  test('CertificationCatalog drops the unrated placeholder and picks defaults',
      () {
    final catalog = CertificationCatalog.fromJson(_catalogJson);
    expect(catalog.movieFor('US').map((o) => o.certification),
        ['G', 'PG', 'PG-13', 'R']);
    // One region serves both caps: XX has no TV scheme and is not offered.
    expect(catalog.regions, ['GB', 'US']);
    expect(CertificationCatalog.defaultFor(catalog.movieFor('US'))?.certification,
        'PG');
    expect(CertificationCatalog.defaultFor(catalog.tvFor('US'))?.certification,
        'TV-PG');
    // No marked default: the second-lowest entry.
    expect(CertificationCatalog.defaultFor(catalog.movieFor('GB'))?.certification,
        'PG');
    expect(CertificationCatalog.defaultFor(const []), isNull);
    expect(catalog.movieFor('US')[0].meaning, 'All ages');
  });

  test('certifications() treats 404 and a non-object body as no support', () async {
    final adapter = _Adapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final service = ContentPolicyService(backendDio: dio);

    adapter.status = 404;
    adapter.body = {'error': 'not found'};
    expect(await service.certifications(), isNull);

    adapter.status = 200;
    adapter.body = <dynamic>[];
    expect(await service.certifications(), isNull);

    adapter.body = _catalogJson;
    expect((await service.certifications())?.regions, ['GB', 'US']);

    adapter.status = 503;
    adapter.body = {'error': 'down'};
    expect(service.certifications(), throwsA(isA<DioException>()));
  });

  test('policy reads, writes, and deletes hit the admin routes', () async {
    final adapter = _Adapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    final service = ContentPolicyService(backendDio: dio);

    adapter.status = 404;
    adapter.body = {'error': 'not a kids account'};
    expect(await service.getUserPolicy(7), isNull);

    adapter.status = 200;
    adapter.body = {
      'max_movie_rating': 'PG',
      'max_tv_rating': 'TV-PG',
      'rating_region': 'US',
      'block_unrated': true,
      'blocked_movie_genres': [27],
      'blocked_tv_genres': [],
    };
    final stored = await service.getUserPolicy(7);
    expect(stored?.blockedMovieGenres, [27]);
    expect(adapter.requests.last.path, '/api/admin/users/7/content-policy');

    const draft = ContentPolicy(
        maxMovieRating: 'G', maxTvRating: 'TV-Y', ratingRegion: 'US');
    adapter.body = draft.toJson();
    await service.updateUserPolicy(7, draft);
    expect(adapter.requests.last.method, 'PUT');
    expect((adapter.requests.last.body as Map)['max_movie_rating'], 'G');
    expect((adapter.requests.last.body as Map)['blocked_tv_genres'], isEmpty);

    adapter.body = {'status': 'cleared'};
    await service.deleteUserPolicy(7);
    expect(adapter.requests.last.method, 'DELETE');
    expect(adapter.requests.last.path, '/api/admin/users/7/content-policy');
  });
}

class _Adapter implements HttpClientAdapter {
  int status = 200;
  Object body = <String, dynamic>{};
  final List<({String method, String path, dynamic body})> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic sent;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) sent = jsonDecode(utf8.decode(bytes));
    }
    requests.add((method: options.method, path: options.uri.path, body: sent));
    return ResponseBody.fromString(
      jsonEncode(body),
      status,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
