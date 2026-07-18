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

  test('PIN: only ONE trailing slash is stripped', () async {
    // Divergence from app.dart's normalizeServer, which strips them all. The
    // survivor becomes part of the Dio base URL, so every request goes to
    // '//api/…'. Pinned so a change here is a conscious one.
    expect(
      await normalized('https://media.example.com//'),
      'https://media.example.com/',
    );
  });

  test('PIN: an uppercase scheme defeats the scheme probe', () async {
    // Same case-sensitive probe as normalizeServer (pinned in
    // app_deep_links_test.dart): 'HTTPS://' is not recognized, so https:// is
    // prepended and the result is not a usable URL.
    expect(
      await normalized('HTTPS://media.example.com'),
      'https://HTTPS://media.example.com',
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
}

typedef _Harness = ({ProviderContainer container, _RecordingAuthService service});

/// Records every server URL the notifier targets. The connect-token call
/// fails with a transport error after recording, ending the flow before it
/// needs config/storage.
class _RecordingAuthService extends AuthService {
  final List<String> statusUrls = [];
  final List<String> redeemUrls = [];

  @override
  Future<ServerStatus> getServerStatus(String serverUrl) async {
    statusUrls.add(serverUrl);
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
