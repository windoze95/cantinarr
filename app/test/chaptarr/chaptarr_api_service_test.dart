import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/chaptarr/data/chaptarr_api_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter: records the request path/query and returns a canned JSON
/// body (Map or List). Tolerates empty GET bodies (unlike the request-service
/// test's adapter, which assumes a JSON request body).
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter(this.responseJson);

  final dynamic responseJson;
  String? lastPath;
  Map<String, dynamic> lastQuery = {};

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    lastPath = options.uri.path;
    lastQuery = options.uri.queryParameters;
    return ResponseBody.fromString(
      jsonEncode(responseJson),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

ChaptarrApiService _service(_FakeAdapter adapter) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  return ChaptarrApiService(backendDio: dio, instanceId: 'inst1');
}

void main() {
  group('getReleases', () {
    // The exact envelope this Chaptarr fork returns (captured from a live
    // instance): releases live under "releases", with hiddenReleases and
    // filterSummary alongside. A bare `as List` cast on this threw
    // "_Map<String, dynamic> is not a subtype of type 'List<dynamic>'".
    test('parses the {releases:[...]} envelope from the live fork', () async {
      final adapter = _FakeAdapter({
        'releases': [
          {
            'guid': 'https://audiobookbay.lu/abss/dark-force-rising/',
            'indexerId': 2,
            'indexer': 'AudioBook Bay (Prowlarr)',
            'protocol': 'torrent',
            'title': 'Dark Force Rising - Timothy Zahn',
            'size': 1728724352,
            'age': 5110,
            'ageHours': 122657.83,
            'seeders': 1,
            'leechers': 0,
            'rejected': false,
            'rejections': <dynamic>[],
            'quality': {
              'quality': {'id': 10, 'name': 'MP3'},
            },
          },
        ],
        'hiddenReleases': <dynamic>[],
        'filterSummary': <String, dynamic>{},
      });

      final releases = await _service(adapter).getReleases(359);

      expect(adapter.lastPath, '/api/instances/inst1/api/v1/release');
      expect(adapter.lastQuery['bookId'], '359');
      expect(releases, hasLength(1));
      expect(releases.first.guid,
          'https://audiobookbay.lu/abss/dark-force-rising/');
      expect(releases.first.indexerId, 2);
      expect(releases.first.quality, 'MP3');
      expect(releases.first.seeders, 1);
      expect(releases.first.isTorrent, isTrue);
    });

    test('still parses a bare array (stock Servarr shape)', () async {
      final adapter = _FakeAdapter([
        {'guid': 'a', 'indexerId': 7, 'title': 'R', 'protocol': 'usenet'},
      ]);

      final releases = await _service(adapter).getReleases(1);

      expect(releases, hasLength(1));
      expect(releases.first.guid, 'a');
      expect(releases.first.isTorrent, isFalse);
    });

    test('an unexpected object yields no releases instead of throwing',
        () async {
      final adapter = _FakeAdapter({'message': 'something broke'});
      final releases = await _service(adapter).getReleases(1);
      expect(releases, isEmpty);
    });
  });

  group('getBookHistory', () {
    test('uses the paged /history endpoint (not author-scoped) by bookId',
        () async {
      final adapter = _FakeAdapter({
        'page': 1,
        'pageSize': 50,
        'totalRecords': 1,
        'records': [
          {
            'id': 9,
            'eventType': 'grabbed',
            'sourceTitle': 'Some Release',
            'date': '2026-01-02T03:04:05Z',
            'bookId': 5247,
          },
        ],
      });

      final history = await _service(adapter).getBookHistory(5247);

      // The author-scoped /history/author 404s on this fork when called with
      // only a bookId, which previously stranded the book sheet on "Loading…".
      expect(adapter.lastPath, '/api/instances/inst1/api/v1/history');
      expect(adapter.lastPath, isNot(contains('/history/author')));
      expect(adapter.lastQuery['bookId'], '5247');
      expect(history, hasLength(1));
      expect(history.first.eventType, 'grabbed');
    });
  });
}
