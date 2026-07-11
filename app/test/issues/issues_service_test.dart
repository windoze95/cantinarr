import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/issues/data/issue_models.dart';
import 'package:cantinarr/features/issues/data/issues_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('problem report sends the exact arr instance id', () async {
    final adapter = _CaptureAdapter();
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    final issueId = await IssuesService(backendDio: dio).reportProblem(
      instanceId: 'sonarr-living-room',
      mediaType: 'tv',
      tmdbId: 1399,
      tvdbId: 121361,
      seasonNumber: 2,
      episodeNumber: 4,
      category: IssueCategory.wrongAudio,
      title: 'Some Show',
    );

    expect(issueId, 42);
    expect(adapter.path, '/api/issues');
    expect(adapter.body['instance_id'], 'sonarr-living-room');
    expect(adapter.body['media_type'], 'tv');
    expect(adapter.body['tmdb_id'], 1399);
    expect(adapter.body['season_number'], 2);
    expect(adapter.body['episode_number'], 4);
  });

  test('admin resolution sends typed disposition and required note', () async {
    final adapter = _CaptureAdapter(response: {
      'id': 42,
      'status': 'wont_fix',
      'media_type': 'tv',
      'resolution': 'Reviewed manually; no safe fix remains.',
      'resolution_kind': 'admin_completed',
    });
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;

    final issue = await IssuesService(backendDio: dio).resolveIssue(
      42,
      disposition: AdminIssueDisposition.wontFix,
      note: '  Reviewed manually; no safe fix remains.  ',
    );

    expect(adapter.path, '/api/admin/issues/42/resolve');
    expect(adapter.body, {
      'disposition': 'wont_fix',
      'note': 'Reviewed manually; no safe fix remains.',
    });
    expect(issue.status, IssueStatus.wontFix);
    expect(issue.resolutionKind, IssueResolutionKind.adminCompleted);
  });
}

class _CaptureAdapter implements HttpClientAdapter {
  String path = '';
  Map<String, dynamic> body = {};
  final Map<String, dynamic> response;

  _CaptureAdapter({
    this.response = const {'issue_id': 42, 'status': 'open'},
  });

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    path = options.uri.path;
    final bytes = <int>[];
    if (requestStream != null) {
      await for (final chunk in requestStream) {
        bytes.addAll(chunk);
      }
    }
    body = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
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
