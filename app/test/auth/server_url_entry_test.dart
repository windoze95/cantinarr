import 'package:cantinarr/core/device/device_identity.dart';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

/// Normalization of the server URL a user TYPES on the connect screen.
///
/// This is `AuthNotifier._normalizeUrl`, observed through the URLs the
/// notifier hands to its [AuthService] — it is NOT `normalizeServer` in
/// app.dart, which only canonicalizes URLs for same-server comparison and is
/// already covered by test/app_deep_links_test.dart (PR #224). The entry
/// path differs: it never lowercases (the URL is used verbatim as the base
/// URL) and it strips at most ONE trailing slash.
///
/// Also covers the scheme fallback in `checkServer`: a bare address is probed
/// over https first and retried over http only when https failed at the
/// transport level (refused / timeout / TLS handshake) — never when https
/// answered with an HTTP status, and never when the user typed a scheme.
void main() {
  Future<_Harness> harness() async {
    final service = _RecordingAuthService();
    final storage = _EmptyStorage();
    final container = ProviderContainer(overrides: [
      authServiceProvider.overrideWithValue(service),
      storageServiceProvider.overrideWithValue(storage),
      deviceIdentityProvider
          .overrideWithValue(_FakeDeviceIdentityService(storage)),
    ]);
    addTearDown(container.dispose);
    await container.read(authProvider.future);
    return (container: container, service: service);
  }

  /// Runs the entry-path normalization on [input] via [AuthNotifier.checkServer]
  /// and returns the URL the server-status probe was sent to.
  Future<String> normalized(String input) async {
    final h = await harness();
    await h.container.read(authProvider.notifier).checkServer(input);
    return h.service.statusUrls.single;
  }

  test('infers https:// for a bare hostname', () async {
    expect(await normalized('media.example.com'), 'https://media.example.com');
  });

  test('trims surrounding whitespace before inferring the scheme', () async {
    expect(
      await normalized('  media.example.com \n'),
      'https://media.example.com',
      reason: 'pasted URLs often carry spaces/newlines; they must not end up '
          'inside the URL or defeat the scheme probe',
    );
  });

  test('keeps an explicit http:// (LAN servers without TLS)', () async {
    expect(
      await normalized('http://192.168.1.20:8585'),
      'http://192.168.1.20:8585',
    );
  });

  test('strips a trailing slash', () async {
    expect(
      await normalized('https://media.example.com/'),
      'https://media.example.com',
    );
  });

  test('keeps a non-default port when inferring the scheme', () async {
    expect(
      await normalized('cantinarr.local:8585'),
      'https://cantinarr.local:8585',
    );
  });

  test('accepts bare IPv4 and IPv6 literals', () async {
    expect(await normalized('192.168.1.20'), 'https://192.168.1.20');
    expect(await normalized('[::1]:8585'), 'https://[::1]:8585');
  });

  test('accepts a .local (mDNS) hostname', () async {
    expect(await normalized('cantinarr.local'), 'https://cantinarr.local');
  });

  test('keeps a base path, stripping its trailing slash', () async {
    expect(
      await normalized('https://media.example.com/cantinarr/'),
      'https://media.example.com/cantinarr',
    );
  });

  test('every trailing slash is stripped', () async {
    // Matches app.dart's normalizeServer: a surviving slash would become part
    // of the Dio base URL and send every request to '//api/…'.
    expect(
      await normalized('https://media.example.com//'),
      'https://media.example.com',
    );
  });

  test('an uppercase scheme is recognized and lowercased', () async {
    expect(
      await normalized('HTTPS://media.example.com'),
      'https://media.example.com',
    );
  });

  test('an uppercase http scheme keeps http', () async {
    expect(
      await normalized('HTTP://media.example.com:8585'),
      'http://media.example.com:8585',
    );
  });

  test('the connect-token flow normalizes its entered URL the same way',
      () async {
    final h = await harness();
    await h.container
        .read(authProvider.notifier)
        .connectWithToken('media.example.com/', 'tok');
    expect(h.service.redeemUrls, ['https://media.example.com'],
        reason: 'checkServer and the credential flows share _normalizeUrl');
  });

  group('scheme fallback', () {
    DioException probeFailure(DioExceptionType type, [Object? error]) =>
        DioException(
          requestOptions: RequestOptions(path: '/api/auth/status'),
          type: type,
          error: error,
        );

    test('a bare address falls back to http when https is refused', () async {
      final h = await harness();
      h.service.statusFailure = (url) => url.startsWith('https://')
          ? probeFailure(DioExceptionType.connectionError)
          : null;
      final result = await h.container
          .read(authProvider.notifier)
          .checkServer('192.168.1.20:8585');
      expect(h.service.statusUrls,
          ['https://192.168.1.20:8585', 'http://192.168.1.20:8585']);
      expect(result.serverUrl, 'http://192.168.1.20:8585',
          reason: 'the answering URL flows back so login/setup reuse http');
    });

    test('a TLS handshake against a plaintext port also falls back', () async {
      // dart:io surfaces this as an unknown-type DioException wrapping a
      // HandshakeException; the notifier matches it by name (web-safe).
      final h = await harness();
      h.service.statusFailure = (url) => url.startsWith('https://')
          ? probeFailure(
              DioExceptionType.unknown,
              Exception('HandshakeException: wrong version number'),
            )
          : null;
      final result = await h.container
          .read(authProvider.notifier)
          .checkServer('cantinarr.local:8585');
      expect(result.serverUrl, 'http://cantinarr.local:8585');
    });

    test('an https answer with an HTTP status blocks the fallback', () async {
      // A 404 means https IS available — the URL is wrong, and retrying over
      // http would mask the real error.
      final h = await harness();
      h.service.statusFailure =
          (url) => probeFailure(DioExceptionType.badResponse);
      await expectLater(
        h.container.read(authProvider.notifier).checkServer('media.example.com'),
        throwsA(isA<DioException>()),
      );
      expect(h.service.statusUrls, ['https://media.example.com']);
    });

    test('an explicit https:// never falls back to http', () async {
      final h = await harness();
      h.service.statusFailure =
          (url) => probeFailure(DioExceptionType.connectionError);
      await expectLater(
        h.container
            .read(authProvider.notifier)
            .checkServer('https://media.example.com'),
        throwsA(isA<DioException>()),
      );
      expect(h.service.statusUrls, ['https://media.example.com'],
          reason: 'a typed scheme is respected verbatim');
    });

    test('when both schemes are unreachable the error surfaces', () async {
      final h = await harness();
      h.service.statusFailure =
          (url) => probeFailure(DioExceptionType.connectionError);
      await expectLater(
        h.container.read(authProvider.notifier).checkServer('192.168.1.20'),
        throwsA(isA<DioException>()),
      );
      expect(h.service.statusUrls,
          ['https://192.168.1.20', 'http://192.168.1.20']);
    });

    test('a reachable https server keeps https and probes once', () async {
      final h = await harness();
      final result = await h.container
          .read(authProvider.notifier)
          .checkServer('media.example.com');
      expect(result.serverUrl, 'https://media.example.com');
      expect(h.service.statusUrls, ['https://media.example.com']);
    });
  });
}

typedef _Harness = ({ProviderContainer container, _RecordingAuthService service});

/// Records every server URL the notifier targets. The connect-token call
/// fails with a transport error after recording, ending the flow before it
/// needs config/storage.
class _RecordingAuthService extends AuthService {
  final List<String> statusUrls = [];
  final List<String> redeemUrls = [];

  /// When set, a non-null return makes the status probe for that URL fail.
  DioException? Function(String url)? statusFailure;

  @override
  Future<ServerStatus> getServerStatus(String serverUrl) async {
    statusUrls.add(serverUrl);
    final failure = statusFailure?.call(serverUrl);
    if (failure != null) throw failure;
    return const ServerStatus(needsSetup: false);
  }

  @override
  Future<AuthResponse> redeemConnectToken(
    String serverUrl,
    String token,
    String deviceName,
    String hardwareId,
  ) async {
    redeemUrls.add(serverUrl);
    throw DioException(
      requestOptions: RequestOptions(path: '/api/auth/connect'),
      type: DioExceptionType.connectionError,
    );
  }
}

class _FakeDeviceIdentityService extends DeviceIdentityService {
  _FakeDeviceIdentityService(super.storage);

  @override
  Future<DeviceIdentity> resolve() async =>
      const DeviceIdentity(displayName: 'Test Device', hardwareId: 'hw-test');
}

class _EmptyStorage implements StorageService {
  @override
  Future<String?> read({required String key}) async => null;

  @override
  Future<void> write({required String key, required String? value}) async {}

  @override
  Future<void> delete({required String key}) async {}

  @override
  Future<void> hardenAuthKeys() async {}
}
