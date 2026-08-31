import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/discover/data/tmdb_models.dart';
import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// The movie/TV request wire shape: an explicit library selection rides the
/// same `instance_id` field the book flow already uses, on the submit body and
/// on the status/options query strings — and the status payload's
/// per-library `instance_statuses` map parses into requester vocabulary.
void main() {
  test('request() pins the selected library on the body', () async {
    final adapter = _CaptureAdapter(response: {'status': 'pending'});
    final service = RequestService(backendDio: _dio(adapter));

    final status = await service.request(
      tmdbId: 550,
      mediaType: MediaType.movie,
      title: 'Fight Club',
      instanceId: 'radarr-4k',
    );

    expect(status, RequestStatus.pending);
    expect(adapter.lastPath, '/api/requests');
    expect(adapter.lastBody?['instance_id'], 'radarr-4k');
  });

  test('request() omits instance_id when no library was chosen', () async {
    final adapter = _CaptureAdapter(response: {'status': 'requested'});
    final service = RequestService(backendDio: _dio(adapter));

    await service.request(tmdbId: 550, mediaType: MediaType.movie);

    expect(adapter.lastBody, isNot(contains('instance_id')));
  });

  test('checkStatusDetail scopes the read and parses instance_statuses',
      () async {
    final adapter = _CaptureAdapter(response: {
      'status': 'available',
      'seasons': <dynamic>[],
      'instance_statuses': {
        'radarr-main': {'status': 'available'},
        'radarr-4k': {'status': 'unavailable'},
      },
    });
    final service = RequestService(backendDio: _dio(adapter));

    final detail = await service.checkStatusDetail(
      550,
      MediaType.movie,
      instanceId: 'radarr-4k',
    );

    expect(adapter.lastQuery?['instance_id'], 'radarr-4k');
    expect(detail.status, RequestStatus.available);
    expect(detail.instanceStatuses, {
      'radarr-main': RequestStatus.available,
      'radarr-4k': RequestStatus.unavailable,
    });
  });

  test('checkStatusDetail without a selection sends no instance_id', () async {
    final adapter = _CaptureAdapter(response: {'status': 'available'});
    final service = RequestService(backendDio: _dio(adapter));

    final detail = await service.checkStatusDetail(550, MediaType.movie);

    expect(adapter.lastQuery, isNot(contains('instance_id')));
    expect(detail.instanceStatuses, isEmpty);
  });

  test('fetchOptions scopes quality profiles to the selected library',
      () async {
    final adapter = _CaptureAdapter(response: {
      'can_choose_season': false,
      'can_choose_quality': true,
      'default_season_scope': 'all',
      'quality_profiles': [
        {'id': 42, 'name': '4K Remux'},
      ],
    });
    final service = RequestService(backendDio: _dio(adapter));

    final options = await service.fetchOptions(
      MediaType.movie,
      instanceId: 'radarr-4k',
    );

    expect(adapter.lastQuery?['instance_id'], 'radarr-4k');
    expect(options?.qualityProfiles.single.name, '4K Remux');
  });
}

Dio _dio(_CaptureAdapter adapter) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;
  return dio;
}

class _CaptureAdapter implements HttpClientAdapter {
  final Map<String, dynamic> response;
  String? lastPath;
  Map<String, dynamic>? lastQuery;
  Map<String, dynamic>? lastBody;

  _CaptureAdapter({required this.response});

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    lastPath = options.path;
    lastQuery = Map<String, dynamic>.from(options.queryParameters);
    final data = options.data;
    lastBody = data is Map<String, dynamic> ? data : null;
    return ResponseBody.fromString(
      jsonEncode(response),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
