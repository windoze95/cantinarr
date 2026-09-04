import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/request/data/request_service.dart'
    hide RequestOptions;
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// Serves `/api/requests/music-status` and `POST /api/requests` with a
/// programmable body/status, recording what was asked.
class _MusicAdapter implements HttpClientAdapter {
  _MusicAdapter({
    this.statusBody,
    this.statusCode = 200,
    this.submitBody,
    this.submitCode = 200,
  });

  final Map<String, dynamic>? statusBody;
  final int statusCode;
  final Map<String, dynamic>? submitBody;
  final int submitCode;
  final statusQueries = <Map<String, dynamic>>[];
  final submissions = <Map<String, dynamic>>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    if (options.path == '/api/requests/music-status') {
      statusQueries.add(Map<String, dynamic>.from(options.queryParameters));
      return _json(statusBody ?? const {'status': 'unavailable'}, statusCode);
    }
    if (options.path == '/api/requests' && options.method == 'POST') {
      final raw = options.data;
      submissions.add(raw is Map<String, dynamic>
          ? raw
          : jsonDecode(raw as String) as Map<String, dynamic>);
      return _json(submitBody ?? const {'status': 'requested'}, submitCode);
    }
    return _json(const <String, dynamic>{}, 200);
  }

  ResponseBody _json(Object body, int code) => ResponseBody.fromString(
        jsonEncode(body),
        code,
        headers: {
          Headers.contentTypeHeader: [Headers.jsonContentType],
        },
      );

  @override
  void close({bool force = false}) {}
}

RequestService _service(_MusicAdapter adapter) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'));
  dio.httpClientAdapter = adapter;
  return RequestService(backendDio: dio);
}

void main() {
  test('parses a plain status and carries the instance pin', () async {
    final adapter = _MusicAdapter(statusBody: const {'status': 'requested'});
    final detail = await _service(adapter)
        .checkMusicStatusDetail('mb-1', instanceId: 'music-1');

    expect(detail.status, RequestStatus.requested);
    expect(detail.isKnown, true);
    expect(detail.canonicalForeignId, isNull);
    expect(adapter.statusQueries.single['foreign_id'], 'mb-1');
    expect(adapter.statusQueries.single['instance_id'], 'music-1');
  });

  test('carries the canonical id the library re-keyed the album to',
      () async {
    final adapter = _MusicAdapter(statusBody: const {
      'status': 'available',
      'canonical_foreign_id': 'mb-new',
    });
    final detail = await _service(adapter).checkMusicStatusDetail('mb-old');

    expect(detail.status, RequestStatus.available);
    expect(detail.canonicalForeignId, 'mb-new');
  });

  test('an unrecognised status word fails closed as unknown', () async {
    final adapter =
        _MusicAdapter(statusBody: const {'status': 'quantum-flux'});
    final detail = await _service(adapter).checkMusicStatusDetail('mb-1');

    expect(detail.isKnown, false);
    expect(detail.isRequestable, false,
        reason: 'an unknown state must never mint a fresh Request button');
  });

  test('a failed read fails closed as unknown, never as requestable',
      () async {
    final adapter = _MusicAdapter(statusCode: 500);
    final detail = await _service(adapter).checkMusicStatusDetail('mb-1');

    expect(detail.isKnown, false);
    expect(detail.isRequestable, false);
  });

  test('a denied album is requestable again', () async {
    final adapter = _MusicAdapter(statusBody: const {'status': 'denied'});
    final detail = await _service(adapter).checkMusicStatusDetail('mb-1');

    expect(detail.status, RequestStatus.denied);
    expect(detail.isRequestable, true);
  });

  test('requestAlbum posts the music wire shape with no book_format',
      () async {
    final adapter = _MusicAdapter(submitBody: const {'status': 'requested'});
    final submission = await _service(adapter).requestAlbum(
      foreignId: 'mb-1',
      title: 'Pinkerton',
      instanceId: 'music-1',
      searchTerm: 'weezer pinkerton',
    );

    expect(submission?.status, RequestStatus.requested);
    final body = adapter.submissions.single;
    expect(body['media_type'], 'music');
    expect(body['foreign_id'], 'mb-1');
    expect(body['title'], 'Pinkerton');
    expect(body['instance_id'], 'music-1');
    expect(body['search_term'], 'weezer pinkerton');
    expect(body.containsKey('book_format'), false,
        reason: 'music requests carry no format axis');
  });

  test('a parked submission carries the server\'s explanation verbatim',
      () async {
    final adapter = _MusicAdapter(submitBody: const {
      'status': 'pending',
      'message': 'This album couldn\'t be matched in the library, so it was '
          'saved as a request for an admin instead of being added '
          'automatically.',
    });
    final submission = await _service(adapter)
        .requestAlbum(foreignId: 'mb-x', title: 'Ghost Album');

    expect(submission?.status, RequestStatus.pending);
    expect(submission?.message, contains('saved as a request for an admin'));
  });

  test('a definitive server refusal surfaces requester vocabulary', () async {
    final adapter = _MusicAdapter(
      submitBody: const {'error': 'album not found for foreign id mb-x'},
      submitCode: 500,
    );

    await expectLater(
      _service(adapter).requestAlbum(foreignId: 'mb-x', title: 'Ghost'),
      throwsA(isA<RequestSubmissionException>().having(
        (e) => e.message,
        'message',
        contains('could not be matched in the library'),
      )),
    );
  });
}
