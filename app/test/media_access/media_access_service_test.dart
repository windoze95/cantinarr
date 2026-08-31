import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/discover/data/tmdb_models.dart';
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
  final List<Uri> uris = [];

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
    uris.add(options.uri);
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
            'existing_account': true,
          },
        ]),
      });

      final servers = await _service(adapter).listMine();

      expect(servers, hasLength(2));
      // A same-named account the server confirmed, only where it said so.
      expect(servers[0].existingAccount, isFalse);
      expect(servers[1].existingAccount, isTrue);
      expect(servers.first.publicAddress, 'https://jf.example.com');
      expect(servers.first.account?.username, 'alice');
      expect(servers.first.account?.disabled, isTrue);
      expect(servers.first.account?.verified, isFalse);
      expect(servers.last.account, isNull);
    });
  });

  group('watchLinks', () {
    test('asks with the ids that narrow the lookup and parses every state',
        () async {
      final adapter = _FakeAdapter({
        'GET /api/media-servers/watch': const _Reply(200, [
          {
            'instance_id': 'jf-a',
            'service_type': 'jellyfin',
            'name': 'Home Jellyfin',
            'state': 'found',
            'url': 'https://jf.example.com/web/#/details?id=i-1&serverId=s',
          },
          {
            'instance_id': 'em-a',
            'service_type': 'emby',
            'name': 'Den Emby',
            'state': 'missing',
          },
          {
            'instance_id': 'jf-b',
            'service_type': 'jellyfin',
            'name': 'Cabin',
            'state': 'unreachable',
          },
        ]),
      });

      final links = await _service(adapter).watchLinks(
        mediaType: MediaType.tv,
        tmdbId: 1396,
        tvdbId: 81189,
        year: 2008,
        title: ' Breaking Bad ',
      );

      final query = adapter.uris.single.queryParameters;
      expect(query, {
        'media_type': 'tv',
        'tmdb_id': '1396',
        'tvdb_id': '81189',
        'year': '2008',
        'title': 'Breaking Bad',
      });
      expect(links.map((l) => l.state), [
        WatchLinkState.found,
        WatchLinkState.missing,
        WatchLinkState.unreachable,
      ]);
      expect(links.first.url,
          'https://jf.example.com/web/#/details?id=i-1&serverId=s');
      expect(links.first.name, 'Home Jellyfin');
      expect(links[1].url, isEmpty);
    });

    test('unknown ids and an empty title are left out of the query',
        () async {
      final adapter = _FakeAdapter({
        'GET /api/media-servers/watch': const _Reply(200, []),
      });

      final links = await _service(adapter).watchLinks(
        mediaType: MediaType.movie,
        tmdbId: 10378,
        tvdbId: 0,
        year: null,
        title: '',
      );

      expect(links, isEmpty);
      expect(adapter.uris.single.queryParameters,
          {'media_type': 'movie', 'tmdb_id': '10378'});
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

  group('linking an existing account', () {
    test('link POSTs the credentials once and decodes the answer', () async {
      final adapter = _FakeAdapter({
        'POST /api/media-servers/jf-a/account/link': const _Reply(201, {
          'username': 'Alice',
          'public_address': 'https://jf.example.com',
          'administrator': true,
        }),
      });
      final linked = await _service(adapter).linkOwnAccount(
        'jf-a',
        username: 'alice',
        password: 'correct-horse',
      );
      expect(linked.username, 'Alice');
      expect(linked.publicAddress, 'https://jf.example.com');
      expect(linked.administrator, isTrue);
      expect(adapter.requests.single.method, 'POST');
      expect(adapter.requests.single.body,
          {'username': 'alice', 'password': 'correct-horse'});
    });

    test('a refused password carries the code on a 400', () async {
      final adapter = _FakeAdapter({
        'POST /api/media-servers/jf-a/account/link': const _Reply(400, {
          'error': 'wrong username or password for this server',
          'code': 'bad_credentials',
        }),
      });
      await expectLater(
        _service(adapter)
            .linkOwnAccount('jf-a', username: 'alice', password: 'nope'),
        throwsA(isA<MediaAccessException>()
            .having((e) => e.status, 'status', 400)
            .having((e) => e.code, 'code', 'bad_credentials')),
      );
    });

    test('the Plex sign-in begins and polls until linked', () async {
      final waiting = _FakeAdapter({
        'POST /api/media-servers/plex/sign-in/begin': const _Reply(200, {
          'pin_id': 42,
          'code': 'ABCD',
          'url': 'https://app.plex.tv/auth#?code=ABCD',
        }),
        'POST /api/media-servers/plex/sign-in/check':
            const _Reply(200, {'linked': false}),
      });
      final service = _service(waiting);
      final start = await service.beginPlexSignIn();
      expect(start.pinId, 42);
      expect(start.code, 'ABCD');
      expect(start.url, startsWith('https://app.plex.tv/auth#?'));
      final pending = await service.checkPlexSignIn(42);
      expect(pending.linked, isFalse);
      expect(waiting.requests.last.body, {'pin_id': 42});

      final done = _FakeAdapter({
        'POST /api/media-servers/plex/sign-in/check': const _Reply(200, {
          'linked': true,
          'username': 'alice',
          'email': 'alice@example.com',
          'invite_state': 'adopted',
        }),
      });
      final state = await _service(done).checkPlexSignIn(42);
      expect(state.linked, isTrue);
      expect(state.username, 'alice');
      expect(state.email, 'alice@example.com');
      expect(state.inviteState, 'adopted');

      final expired = _FakeAdapter({
        'POST /api/media-servers/plex/sign-in/check': const _Reply(404, {
          'error': 'the Plex sign-in has expired; start again',
          'code': 'pin_expired',
        }),
      });
      await expectLater(
        _service(expired).checkPlexSignIn(42),
        throwsA(isA<MediaAccessException>()
            .having((e) => e.code, 'code', 'pin_expired')),
      );
    });

    test('an account status reads administrator, off by default', () {
      expect(
        MediaServerAccountStatus.fromJson(
                const {'username': 'julian', 'administrator': true})
            .administrator,
        isTrue,
      );
      expect(
        MediaServerAccountStatus.fromJson(const {'username': 'alice'})
            .administrator,
        isFalse,
      );
    });
  });
}
