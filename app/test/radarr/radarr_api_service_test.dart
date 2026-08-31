import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/radarr/data/radarr_api_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Fake Dio adapter: records the composed request options and returns a
/// canned JSON body, so tests can pin what actually goes on the wire —
/// including per-request timeout overrides.
class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter(this.responseJson);

  final dynamic responseJson;
  RequestOptions? lastRequest;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    lastRequest = options;
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

RadarrApiService _service(_FakeAdapter adapter) {
  final dio = Dio(BaseOptions(
    baseUrl: 'http://localhost',
    connectTimeout: const Duration(seconds: 15),
    receiveTimeout: const Duration(seconds: 15),
  ))
    ..httpClientAdapter = adapter;
  return RadarrApiService(backendDio: dio, instanceId: 'inst1');
}

void main() {
  group('getMovies', () {
    // The whole library comes back in one unpaginated response, so serve time
    // grows with library size; the 15s default made large libraries
    // permanently unloadable (issue #483).
    test('uses the long-call receive timeout, keeping the base connect timeout',
        () async {
      final adapter = _FakeAdapter(<dynamic>[]);

      final movies = await _service(adapter).getMovies();

      expect(adapter.lastRequest?.uri.path, '/api/instances/inst1/api/v3/movie');
      expect(adapter.lastRequest?.receiveTimeout, const Duration(seconds: 120));
      // connectTimeout stays the base default on native: longRequestOptions
      // passes null and Options.compose falls back to BaseOptions.
      expect(adapter.lastRequest?.connectTimeout, const Duration(seconds: 15));
      expect(movies, isEmpty);
    });
  });

  group('getReleases', () {
    // Live indexer queries take 10-60s; the long-call options keep web from
    // aborting at the base connect timeout before the first byte arrives.
    test('uses the long-call receive timeout, keeping the base connect timeout',
        () async {
      final adapter = _FakeAdapter(<dynamic>[]);

      await _service(adapter).getReleases(1);

      expect(
          adapter.lastRequest?.uri.path, '/api/instances/inst1/api/v3/release');
      expect(adapter.lastRequest?.receiveTimeout, const Duration(seconds: 120));
      expect(adapter.lastRequest?.connectTimeout, const Duration(seconds: 15));
    });
  });
}
