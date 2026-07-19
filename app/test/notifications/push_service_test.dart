import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/notifications/push_service.dart';
import 'package:cantinarr/navigation/app_router.dart';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:go_router/go_router.dart';

const _channelName = 'codes.julian.cantinarr/push';

/// Simulates the native side calling into Dart on the push channel, the same
/// path AppDelegate uses for token delivery and notification taps. Awaiting
/// this awaits the service's handler (including its backend call).
Future<void> _emitNativeCall(String method, Object? arguments) {
  const codec = StandardMethodCodec();
  return TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
      .handlePlatformMessage(
    _channelName,
    codec.encodeMethodCall(MethodCall(method, arguments)),
    (_) {},
  );
}

/// A [GoRouter] that records pushed locations instead of navigating, so the
/// notification-tap → route mapping can be asserted without a widget tree.
class _RecordingRouter extends GoRouter {
  _RecordingRouter()
      : super.routingConfig(
          routingConfig: ValueNotifier(
            RoutingConfig(routes: [
              GoRoute(path: '/', builder: (_, __) => const SizedBox.shrink()),
            ]),
          ),
        );

  final pushed = <String>[];
  final went = <String>[];

  @override
  Future<T?> push<T extends Object?>(String location, {Object? extra}) {
    pushed.add(location);
    return Future<T?>.value();
  }

  @override
  void go(String location, {Object? extra}) {
    went.add(location);
  }
}

/// In-memory [StorageService] seeded with (or without) a device id.
class _FakeStorage implements StorageService {
  _FakeStorage(this._data);

  final Map<String, String?> _data;

  @override
  Future<String?> read({required String key}) async => _data[key];

  @override
  Future<void> write({required String key, required String? value}) async {
    _data[key] = value;
  }

  @override
  Future<void> delete({required String key}) async => _data.remove(key);

  @override
  Future<void> hardenAuthKeys() async {}
}

/// Records every backend request; optionally fails the first [failFirst]
/// requests with a 500 so the retry-after-failure path can be exercised.
class _RecordingAdapter implements HttpClientAdapter {
  _RecordingAdapter({this.failFirst = 0});

  int failFirst;
  final requests = <RequestOptions>[];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(options);
    if (failFirst > 0) {
      failFirst--;
      return ResponseBody.fromString(
        '{"error":"boom"}',
        500,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    return ResponseBody.fromString(
      '{}',
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

class _Harness {
  _Harness({Map<String, String?>? storage, int failFirst = 0})
      : router = _RecordingRouter(),
        adapter = _RecordingAdapter(failFirst: failFirst) {
    final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
      ..httpClientAdapter = adapter;
    container = ProviderContainer(overrides: [
      appRouterProvider.overrideWithValue(router),
      storageServiceProvider.overrideWithValue(
        _FakeStorage(storage ?? {StorageKeys.deviceId: 'device-1'}),
      ),
      backendClientProvider.overrideWithValue(dio),
    ]);
    addTearDown(container.dispose);
    addTearDown(router.dispose);
    // Constructing the service installs its handler on the push channel.
    service = container.read(pushServiceProvider);
  }

  final _RecordingRouter router;
  final _RecordingAdapter adapter;
  late final ProviderContainer container;
  late final PushService service;

  List<RequestOptions> get tokenPosts => adapter.requests
      .where((r) => r.method == 'POST' && r.path == '/api/devices/push-token')
      .toList();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('notification tap routing', () {
    const directRoutes = {
      'request_pending': '/approvals',
      'agent_action_pending': '/agent-actions',
      'plex_access_request': '/settings/users',
      'plex_invite_sent': '/plex-guide',
      'remediation_autodispatch_disabled': '/settings/ai-remediation',
    };

    for (final entry in directRoutes.entries) {
      test('${entry.key} routes to ${entry.value}', () async {
        final h = _Harness();
        await _emitNativeCall('onNotificationTap', {'type': entry.key});
        expect(h.router.pushed, [entry.value]);
      });
    }

    for (final type in const ['request_decision', 'new_movie', 'new_episode']) {
      test('$type opens the media detail page', () async {
        final h = _Harness();
        await _emitNativeCall('onNotificationTap', {
          'type': type,
          'tmdb_id': 42,
          'media_type': 'movie',
        });
        expect(h.router.pushed, ['/detail/movie/42']);
      });
    }

    test('media_type tv routes to the tv detail page', () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {
        'type': 'new_episode',
        'tmdb_id': 7,
        'media_type': 'tv',
      });
      expect(h.router.pushed, ['/detail/tv/7']);
    });

    test('missing or unknown media_type falls back to movie', () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {
        'type': 'new_movie',
        'tmdb_id': 9,
      });
      await _emitNativeCall('onNotificationTap', {
        'type': 'new_movie',
        'tmdb_id': 10,
        'media_type': 'comic',
      });
      expect(h.router.pushed, ['/detail/movie/9', '/detail/movie/10']);
    });

    // A book decision's custom payload is {type, tmdb_id: 0, media_type} from
    // old servers, plus foreign_id (the Chaptarr foreignBookId) and title from
    // newer ones — the server's passthrough forwards no decision/reason, and a
    // book row stores tmdb_id 0. Without a foreign_id there is no book
    // identity to deep-link on, so approved and denied both land on the
    // requester-facing Books tab.
    for (final decision in const ['approved', 'denied']) {
      test(
          'book request_decision ($decision) without a foreign_id opens '
          'the Books tab', () async {
        final h = _Harness();
        await _emitNativeCall('onNotificationTap', {
          'type': 'request_decision',
          'tmdb_id': 0,
          'media_type': 'book',
          // Not part of the real push payload; routing must not depend on it.
          'decision': decision,
        });
        expect(h.router.went, ['/dashboard/books']);
        expect(h.router.pushed, isEmpty);
      });
    }

    test('book request_decision with a foreign_id opens the book detail',
        () async {
      // Newer servers add foreign_id (and title) to book decision payloads —
      // the only identity a client can deep-link on (books store tmdb_id 0).
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {
        'type': 'request_decision',
        'tmdb_id': 0,
        'media_type': 'book',
        'foreign_id': '29749107',
      });
      expect(h.router.pushed, ['/detail/book/29749107']);
    });

    test('a book payload title rides along as an encoded query parameter',
        () async {
      // The detail screen uses the title to name a book the library can't
      // resolve anymore (e.g. a denied request that was never added).
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {
        'type': 'request_decision',
        'tmdb_id': 0,
        'media_type': 'book',
        'foreign_id': '29749107',
        'title': 'Dune Messiah',
      });
      expect(h.router.pushed, ['/detail/book/29749107?title=Dune%20Messiah']);
    });

    test('blank and non-string foreign_id values fall back to the Books tab',
        () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {
        'type': 'request_decision',
        'tmdb_id': 0,
        'media_type': 'book',
        'foreign_id': '  ',
      });
      await _emitNativeCall('onNotificationTap', {
        'type': 'request_decision',
        'tmdb_id': 0,
        'media_type': 'book',
        'foreign_id': 29749107,
      });
      expect(h.router.went, ['/dashboard/books', '/dashboard/books']);
      expect(h.router.pushed, isEmpty);
    });

    test('media_type book wins over a stray positive tmdb_id', () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {
        'type': 'request_decision',
        'tmdb_id': 10,
        'media_type': 'book',
      });
      expect(h.router.went, ['/dashboard/books']);
      expect(h.router.pushed, isEmpty);
    });

    test('tmdb_id survives string and double payload encodings', () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {
        'type': 'new_movie',
        'tmdb_id': '77',
      });
      await _emitNativeCall('onNotificationTap', {
        'type': 'new_movie',
        'tmdb_id': 78.0,
      });
      expect(h.router.pushed, ['/detail/movie/77', '/detail/movie/78']);
    });

    test('media payloads without a usable tmdb_id do not navigate', () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {'type': 'request_decision'});
      await _emitNativeCall(
          'onNotificationTap', {'type': 'new_movie', 'tmdb_id': 0});
      await _emitNativeCall(
          'onNotificationTap', {'type': 'new_movie', 'tmdb_id': -3});
      await _emitNativeCall(
          'onNotificationTap', {'type': 'new_movie', 'tmdb_id': 'nope'});
      expect(h.router.pushed, isEmpty);
    });

    for (final type in const [
      'issue_created',
      'issue_updated',
      'issue_resolved',
      'agent_action_decided',
      'agent_action_terminal',
      'agent_action_superseded',
    ]) {
      test('$type opens the issue thread', () async {
        final h = _Harness();
        await _emitNativeCall('onNotificationTap', {
          'type': type,
          'issue_id': 12,
        });
        expect(h.router.pushed, ['/issues/12']);
      });
    }

    test('issue payloads without a usable issue_id do not navigate', () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {'type': 'issue_created'});
      await _emitNativeCall(
          'onNotificationTap', {'type': 'issue_updated', 'issue_id': 0});
      expect(h.router.pushed, isEmpty);
    });

    test('unknown, missing-type, and non-map payloads are ignored', () async {
      final h = _Harness();
      await _emitNativeCall('onNotificationTap', {'type': 'shiny_new_thing'});
      await _emitNativeCall('onNotificationTap', {'tmdb_id': 42});
      await _emitNativeCall('onNotificationTap', 'not-a-map');
      await _emitNativeCall('onNotificationTap', null);
      expect(h.router.pushed, isEmpty);
    });
  });

  group('APNs token registration', () {
    test('a delivered token is registered once with the device identity',
        () async {
      final h = _Harness();
      await _emitNativeCall('onApnsToken', 'token-a');
      await _emitNativeCall('onApnsToken', 'token-a');

      expect(h.tokenPosts, hasLength(1));
      expect(h.tokenPosts.single.data, {
        'device_id': 'device-1',
        'apns_token': 'token-a',
        'platform': 'ios',
      });
    });

    test('a rotated token re-registers', () async {
      final h = _Harness();
      await _emitNativeCall('onApnsToken', 'token-a');
      await _emitNativeCall('onApnsToken', 'token-b');

      expect(
        h.tokenPosts.map((r) => r.data['apns_token']).toList(),
        ['token-a', 'token-b'],
      );
    });

    test('empty and null tokens are ignored', () async {
      final h = _Harness();
      await _emitNativeCall('onApnsToken', '');
      await _emitNativeCall('onApnsToken', null);
      expect(h.adapter.requests, isEmpty);
    });

    test('registration is skipped when no device id is stored', () async {
      final h = _Harness(storage: {});
      await _emitNativeCall('onApnsToken', 'token-a');
      expect(h.adapter.requests, isEmpty);
    });

    test('a failed registration is not deduped — the same token retries',
        () async {
      final h = _Harness(failFirst: 1);
      await _emitNativeCall('onApnsToken', 'token-a');
      await _emitNativeCall('onApnsToken', 'token-a');

      expect(h.tokenPosts, hasLength(2));
      expect(h.tokenPosts.last.data['apns_token'], 'token-a');
    });

    test('registerForPush is a no-op off iOS', () async {
      final h = _Harness();
      final outgoing = <String>[];
      const channel = MethodChannel(_channelName);
      TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, (call) async {
        outgoing.add(call.method);
        return null;
      });
      addTearDown(() => TestDefaultBinaryMessengerBinding
          .instance.defaultBinaryMessenger
          .setMockMethodCallHandler(channel, null));

      await h.service.registerForPush();
      expect(await h.service.authorizationStatus(), 'notDetermined');

      expect(outgoing, isEmpty);
      expect(h.adapter.requests, isEmpty);
    });
  });

  group('PushTestResult.fromJson', () {
    test('parses counts and per-device results', () {
      final result = PushTestResult.fromJson({
        'tokens': 2,
        'sent': 1,
        'failed': 1,
        'results': [
          {'ok': true, 'pruned': false, 'error': ''},
          {'ok': false, 'pruned': true, 'error': 'BadDeviceToken'},
        ],
      });

      expect(result.tokens, 2);
      expect(result.sent, 1);
      expect(result.failed, 1);
      expect(result.results, hasLength(2));
      expect(result.results.first.ok, isTrue);
      expect(result.results.last.pruned, isTrue);
      expect(result.results.last.error, 'BadDeviceToken');
    });

    test('defaults every field on an empty payload', () {
      final result = PushTestResult.fromJson(const {});
      expect(result.tokens, 0);
      expect(result.sent, 0);
      expect(result.failed, 0);
      expect(result.results, isEmpty);
      expect(result.firstError, isNull);
    });

    test('accepts double-encoded counts', () {
      final result = PushTestResult.fromJson({'tokens': 2.0, 'sent': 1.0});
      expect(result.tokens, 2);
      expect(result.sent, 1);
    });

    test('firstError skips devices without an error', () {
      final result = PushTestResult.fromJson({
        'tokens': 2,
        'results': [
          {'ok': true, 'error': ''},
          {'ok': false, 'error': 'Boom'},
        ],
      });
      expect(result.firstError, 'Boom');
    });
  });

  group('describePushTest', () {
    PushTestResult result({
      int tokens = 1,
      int sent = 0,
      int failed = 0,
      List<PushTestDeviceResult> results = const [],
    }) =>
        PushTestResult(
            tokens: tokens, sent: sent, failed: failed, results: results);

    test('no registered devices — self and admin phrasing', () {
      expect(
        describePushTest(result(tokens: 0)),
        startsWith('You have no registered push devices yet.'),
      );
      expect(
        describePushTest(result(tokens: 0), username: 'alice'),
        startsWith('alice has no registered push devices yet.'),
      );
    });

    test('clean delivery — singular, plural, and admin phrasing', () {
      expect(describePushTest(result(sent: 1)), 'Test sent to 1 device.');
      expect(describePushTest(result(sent: 3)), 'Test sent to 3 devices.');
      expect(
        describePushTest(result(sent: 3), username: 'alice'),
        'Test sent to alice (3 devices).',
      );
    });

    test('tokens exist but nothing was reached — gateway desync message', () {
      expect(
        describePushTest(result(tokens: 2)),
        contains('the push gateway has no active token for this account'),
      );
      expect(
        describePushTest(result(tokens: 2), username: 'alice'),
        contains('no active token for alice'),
      );
    });

    test('partial failure carries the BadDeviceToken hint', () {
      final r = result(
        tokens: 3,
        sent: 2,
        failed: 1,
        results: const [
          PushTestDeviceResult(ok: false, pruned: false, error: 'BadDeviceToken'),
        ],
      );
      final message = describePushTest(r);
      expect(message, startsWith('Sent to 2, but 1 failed'));
      expect(message, contains('Apple rejected the token'));
    });

    test('total failure carries the Unregistered hint', () {
      final r = result(
        tokens: 1,
        failed: 1,
        results: const [
          PushTestDeviceResult(ok: false, pruned: true, error: 'Unregistered'),
        ],
      );
      final message = describePushTest(r);
      expect(message, startsWith('Delivery failed for 1 device'));
      expect(message, contains('no longer valid'));
    });

    test('unrecognised errors are echoed verbatim', () {
      final r = result(
        tokens: 2,
        failed: 2,
        results: const [
          PushTestDeviceResult(ok: false, pruned: false, error: 'TooMany'),
        ],
      );
      expect(
        describePushTest(r, username: 'alice'),
        'alice: delivery failed for 2 devices (TooMany).',
      );
    });
  });
}
