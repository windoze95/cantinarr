import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/media_access/data/media_access_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

/// One canned answer per "METHOD path"; anything else is a 404.
class _Reply {
  const _Reply(this.status, this.body, {this.contentType = 'application/json'});
  final int status;
  final Object body;
  final String contentType;
}

class _FakeAdapter implements HttpClientAdapter {
  _FakeAdapter(this.replies);

  final Map<String, _Reply> replies;
  final List<({String method, String path, dynamic body})> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((c) => c).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    requests.add((method: options.method, path: path, body: body));
    final reply = replies['${options.method} $path'] ??
        const _Reply(404, {'error': 'not found'});
    return ResponseBody.fromString(
      reply.body is String ? reply.body as String : jsonEncode(reply.body),
      reply.status,
      headers: {
        'content-type': [reply.contentType],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _OfflineAdapter implements HttpClientAdapter {
  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    throw DioException.connectionError(
      requestOptions: options,
      reason: 'connection refused',
    );
  }

  @override
  void close({bool force = false}) {}
}

MediaAccessService _service(HttpClientAdapter adapter) => MediaAccessService(
      backendDio: Dio(BaseOptions(baseUrl: 'http://localhost'))
        ..httpClientAdapter = adapter,
    );

void main() {
  group('mediaServerGuideTitle', () {
    test('names the granted product, in a stable order', () {
      expect(mediaServerGuideTitle(const []), 'Watch on your media server');
      expect(mediaServerGuideTitle(const ['jellyfin']), 'Watch on Jellyfin');
      expect(mediaServerGuideTitle(const ['emby']), 'Watch on Emby');
      expect(mediaServerGuideTitle(const ['jellyfin', 'jellyfin']),
          'Watch on Jellyfin');
      expect(mediaServerGuideTitle(const ['emby', 'jellyfin']),
          'Watch on Jellyfin or Emby');
      expect(mediaServerGuideTitle(const ['plex', 'emby', 'jellyfin']),
          'Watch on Plex, Jellyfin, or Emby');
    });

    test('type labels are product names', () {
      expect(mediaServerTypeLabel('jellyfin'), 'Jellyfin');
      expect(mediaServerTypeLabel('emby'), 'Emby');
      expect(mediaServerTypeLabel(''), 'your media server');
    });
  });

  group('listMine', () {
    test('parses accounts, absence, and the verified flag', () async {
      final adapter = _FakeAdapter({
        'GET /api/media-servers': const _Reply(200, [
          {
            'instance_id': 'jf-a',
            'service_type': 'jellyfin',
            'name': 'Home Jellyfin',
            'public_address': 'https://jf.example.com',
            'account': {
              'username': 'alice',
              'disabled': true,
              'verified': false,
            },
          },
          {
            'instance_id': 'jf-b',
            'service_type': 'jellyfin',
            'name': 'Cabin',
            'public_address': '',
            'account': null,
          },
        ]),
      });

      final servers = await _service(adapter).listMine();

      expect(servers, hasLength(2));
      expect(servers.first.publicAddress, 'https://jf.example.com');
      expect(servers.first.account?.username, 'alice');
      expect(servers.first.account?.disabled, isTrue);
      expect(servers.first.account?.verified, isFalse);
      expect(servers.last.account, isNull);
    });
  });

  group('createAccount', () {
    test('posts the password once and parses the created account', () async {
      final adapter = _FakeAdapter({
        'POST /api/media-servers/jf-a/account': const _Reply(201, {
          'username': 'alice',
          'public_address': 'https://jf.example.com',
        }),
      });

      final created =
          await _service(adapter).createAccount('jf-a', 'correct-horse');

      expect(created.username, 'alice');
      expect(created.publicAddress, 'https://jf.example.com');
      final request = adapter.requests.single;
      expect(request.method, 'POST');
      expect(request.path, '/api/media-servers/jf-a/account');
      expect(request.body, {'password': 'correct-horse'});
    });

    test('decodes a text/plain JSON refusal into status, code, and message',
        () async {
      final adapter = _FakeAdapter({
        'POST /api/media-servers/jf-a/account': _Reply(
          409,
          '${jsonEncode({
                'error': 'that name is already taken on this server; ask '
                    'your admin to link it to you',
                'code': 'name_taken',
              })}\n',
          contentType: 'text/plain; charset=utf-8',
        ),
      });

      await expectLater(
        _service(adapter).createAccount('jf-a', 'correct-horse'),
        throwsA(isA<MediaAccessException>()
            .having((e) => e.status, 'status', 409)
            .having((e) => e.code, 'code', 'name_taken')
            .having((e) => e.message, 'message', contains('already taken'))
            .having((e) => e.isTransport, 'isTransport', isFalse)),
      );
    });

    test('a JSON refusal without a code keeps its message', () async {
      final adapter = _FakeAdapter({
        'POST /api/media-servers/jf-a/account': const _Reply(403, {
          'error': 'that server is not available to you',
        }),
      });

      await expectLater(
        _service(adapter).createAccount('jf-a', 'correct-horse'),
        throwsA(isA<MediaAccessException>()
            .having((e) => e.status, 'status', 403)
            .having((e) => e.code, 'code', '')
            .having((e) => e.message, 'message',
                'that server is not available to you')),
      );
    });

    test('nothing answered at all is a transport failure', () async {
      await expectLater(
        _service(_OfflineAdapter()).createAccount('jf-a', 'correct-horse'),
        throwsA(isA<MediaAccessException>()
            .having((e) => e.status, 'status', isNull)
            .having((e) => e.isTransport, 'isTransport', isTrue)),
      );
    });
  });

  group('admin routes', () {
    test('listRemoteUsers accepts a bare list or a users envelope', () async {
      const users = [
        {'id': 'a1', 'name': 'admin', 'is_administrator': true},
        {'id': 'u2', 'name': 'alice', 'is_disabled': true},
      ];
      final bare = _FakeAdapter({
        'GET /api/admin/media-servers/jf-a/users': const _Reply(200, users),
      });
      final wrapped = _FakeAdapter({
        'GET /api/admin/media-servers/jf-a/users':
            const _Reply(200, {'users': users}),
      });

      for (final adapter in [bare, wrapped]) {
        final remote = await _service(adapter).listRemoteUsers('jf-a');
        expect(remote.map((u) => u.id), ['a1', 'u2']);
        expect(remote.first.isAdministrator, isTrue);
        expect(remote.last.isDisabled, isTrue);
      }
    });

    test('account rows read disabled as a bool (a stamp is tolerated)', () {
      expect(
        MediaServerAccountRow.fromJson(const {'disabled': true}).disabled,
        isTrue,
      );
      expect(
        MediaServerAccountRow.fromJson(const {'disabled': false}).disabled,
        isFalse,
      );
      expect(
        MediaServerAccountRow.fromJson(
                const {'disabled_at': '2026-08-28T10:00:00Z'})
            .disabled,
        isTrue,
      );
      expect(MediaServerAccountRow.fromJson(const {}).disabled, isFalse);

      final row = MediaServerAccountRow.fromJson(const {
        'user_id': 7,
        'instance_id': 'jf-a',
        'instance_name': 'Home Jellyfin',
        'service_type': 'jellyfin',
        'remote_user_id': 'r1',
        'username': 'alice',
        'created_by_cantinarr': false,
        'disabled': false,
        'created_at': '2026-08-28T10:00:00Z',
      });
      expect(row.userId, 7);
      expect(row.remoteUsername, 'alice');
      expect(row.createdByCantinarr, isFalse);
    });

    test('link PUTs the remote id and unlink DELETEs the same path',
        () async {
      final adapter = _FakeAdapter({
        'PUT /api/admin/users/7/media-servers/jf-a/account': const _Reply(200, {
          'user_id': 7,
          'instance_id': 'jf-a',
          'username': 'alice',
          'remote_user_id': 'u2',
          'created_by_cantinarr': false,
        }),
        'DELETE /api/admin/users/7/media-servers/jf-a/account':
            const _Reply(204, ''),
      });
      final service = _service(adapter);

      final linked = await service.link(
        userId: 7,
        instanceId: 'jf-a',
        remoteUserId: 'u2',
      );
      await service.unlink(userId: 7, instanceId: 'jf-a');

      expect(linked.remoteUserId, 'u2');
      expect(linked.createdByCantinarr, isFalse);
      expect(adapter.requests[0].method, 'PUT');
      expect(adapter.requests[0].body, {'remote_user_id': 'u2'});
      expect(adapter.requests[1].method, 'DELETE');
      expect(adapter.requests[1].path,
          '/api/admin/users/7/media-servers/jf-a/account');
    });

    test('a link refusal carries the server code', () async {
      final adapter = _FakeAdapter({
        'PUT /api/admin/users/7/media-servers/jf-a/account': const _Reply(409, {
          'error': 'that account is already linked to another user',
          'code': 'remote_already_linked',
        }),
      });

      await expectLater(
        _service(adapter)
            .link(userId: 7, instanceId: 'jf-a', remoteUserId: 'u2'),
        throwsA(isA<MediaAccessException>()
            .having((e) => e.code, 'code', 'remote_already_linked')),
      );
    });
  });
}
