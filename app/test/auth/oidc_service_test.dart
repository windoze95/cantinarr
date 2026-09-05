import 'dart:convert';
import 'package:cantinarr/core/storage/secure_storage.dart';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/oidc_service.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:crypto/crypto.dart';
import 'package:flutter_test/flutter_test.dart';

class MemoryStorage implements StorageService {
  final values = <String, String>{};
  @override
  Future<String?> read({required String key}) async => values[key];
  @override
  Future<void> write({required String key, required String? value}) async {
    if (value == null) {
      values.remove(key);
    } else {
      values[key] = value;
    }
  }

  @override
  Future<void> delete({required String key}) async {
    values.remove(key);
  }

  @override
  Future<void> hardenAuthKeys() async {}
}

class FakeOIDCAuth extends AuthService {
  Map<String, dynamic>? begin;
  Map<String, dynamic>? exchange;
  String? exchangeServer;
  String? beginPath;
  String? beginAccessToken;
  bool failExchange = false;
  @override
  Future<ServerStatus> getServerStatus(String serverUrl) async =>
      const ServerStatus(
          needsSetup: false,
          ssoAvailable: true,
          ssoProvider: 'Family',
          ssoOrigin: 'https://media.example.com');
  @override
  Future<Map<String, dynamic>> oidcRequest(String server, String path,
      {String method = 'GET',
      String? accessToken,
      Map<String, dynamic>? data}) async {
    if (path.endsWith('/exchange')) {
      exchange = data;
      exchangeServer = server;
      if (failExchange) throw StateError('Provider refused sign-in.');
      return {'status': 'linked'};
    }
    begin = data;
    beginPath = path;
    beginAccessToken = accessToken;
    return {
      'flow': 'flow-1',
      'start_url': 'https://media.example.com/api/auth/oidc/start?flow=flow-1'
    };
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('web relocation happens before a verifier is created', () async {
    final auth = FakeOIDCAuth(), storage = MemoryStorage();
    String? destination;
    final service = OIDCService(auth, storage,
        isWeb: true,
        currentOrigin: 'http://nas.lan',
        navigate: (url) => destination = url);
    await service.start('http://nas.lan', invitation: 'invite');
    expect(destination,
        'https://media.example.com/#/oidc/start?invitation=invite');
    expect(auth.begin, isNull);
    expect(storage.values, isEmpty);
  });
  test('web handoff uses temporary tab storage and clears the return URL',
      () async {
    final auth = FakeOIDCAuth(), storage = MemoryStorage();
    final tab = <String, String>{};
    var cleared = false;
    String? destination;
    final service = OIDCService(auth, storage,
        isWeb: true,
        currentOrigin: 'https://media.example.com',
        readTab: (key) => tab[key],
        writeTab: (key, value) {
          if (value == null) {
            tab.remove(key);
          } else {
            tab[key] = value;
          }
        },
        navigate: (url) => destination = url,
        clearReturnAddress: () => cleared = true);
    await service.start('https://media.example.com');
    expect(storage.values, isEmpty);
    expect(tab.length, 1);
    expect(destination, contains('/api/auth/oidc/start'));
    final pending = jsonDecode(tab.values.single) as Map<String, dynamic>;
    expect(destination, isNot(contains(pending['verifier'] as String)));
    await service.finish(Uri.parse('/oidc/return?flow=flow-1&code=code'));
    expect(tab, isEmpty);
    expect(cleared, isTrue);
    expect(auth.exchange!['verifier'], pending['verifier']);
  });
  test('old servers have no SSO fields and retain their login methods', () {
    final status = ServerStatus.fromJson(
        {'needs_setup': false, 'webauthn_available': true});
    expect(status.ssoAvailable, isFalse);
    expect(status.ssoOnly, isFalse);
    expect(status.supportsPasskeyPlatform('web'), isTrue);
  });
  test('native restart uses secure pending verifier and original server',
      () async {
    final storage = MemoryStorage()..values['existing-session'] = 'keep';
    final auth = FakeOIDCAuth();
    Uri? opened;
    final service = OIDCService(auth, storage, openBrowser: (uri) async {
      opened = uri;
      return true;
    });
    await service.start('https://media.example.com',
        purpose: 'link', accessToken: 'local-access');
    expect(opened!.path, '/api/auth/oidc/start');
    expect(auth.beginPath, '/api/auth/oidc/link');
    expect(auth.beginAccessToken, 'local-access');
    final pending = jsonDecode(storage.values['cantinarr_oidc_pending']!)
        as Map<String, dynamic>;
    final expectedChallenge = base64UrlEncode(
            sha256.convert(utf8.encode(pending['verifier'] as String)).bytes)
        .replaceAll('=', '');
    expect(auth.begin!['challenge'], expectedChallenge);
    expect(opened.toString(), isNot(contains(pending['verifier'] as String)));
    final restarted =
        OIDCService(auth, storage, openBrowser: (_) async => true);
    final result = await restarted.finish(Uri.parse(
        'cantinarr://oidc?flow=flow-1&code=handoff&server=https://attacker.example'));
    expect(result.purpose, 'link');
    expect(auth.exchangeServer, 'https://media.example.com');
    expect(auth.exchange!['verifier'], pending['verifier']);
    expect(storage.values['existing-session'], 'keep');
    expect(storage.values.containsKey('cantinarr_oidc_pending'), isFalse);
    await expectLater(
        restarted
            .finish(Uri.parse('cantinarr://oidc?flow=flow-1&code=handoff')),
        throwsStateError);
  });
  test('a different flow cannot consume the waiting attempt', () async {
    final auth = FakeOIDCAuth(), storage = MemoryStorage();
    final service = OIDCService(auth, storage, openBrowser: (_) async => true);
    await service.start('https://media.example.com', invitation: 'invite');
    expect(auth.begin!['invitation'], 'invite');
    await expectLater(
        service.finish(Uri.parse('cantinarr://oidc?flow=other&code=code')),
        throwsStateError);
    expect(auth.exchange, isNull);
    expect(storage.values.containsKey('cantinarr_oidc_pending'), isTrue);
  });
  test('login never forwards an existing server session to the new server',
      () async {
    final auth = FakeOIDCAuth(), storage = MemoryStorage();
    final service = OIDCService(auth, storage, openBrowser: (_) async => true);
    await service.start('https://media.example.com',
        accessToken: 'another-server-session', invitation: 'invite');
    expect(auth.beginPath, '/api/auth/oidc/begin');
    expect(auth.beginAccessToken, isNull);
  });
  test('expired pending verifier is removed without an exchange', () async {
    final auth = FakeOIDCAuth(), storage = MemoryStorage();
    final service = OIDCService(auth, storage, openBrowser: (_) async => true);
    await service.start('https://media.example.com');
    final pending = jsonDecode(storage.values['cantinarr_oidc_pending']!)
        as Map<String, dynamic>;
    pending['expires'] = 0;
    storage.values['cantinarr_oidc_pending'] = jsonEncode(pending);
    await expectLater(
        service.finish(Uri.parse('cantinarr://oidc?flow=flow-1&code=code')),
        throwsStateError);
    expect(auth.exchange, isNull);
    expect(storage.values, isEmpty);
  });
  test(
      'browser launch failure and provider rejection preserve existing storage',
      () async {
    final auth = FakeOIDCAuth(),
        storage = MemoryStorage()..values['existing-session'] = 'keep';
    final blocked = OIDCService(auth, storage, openBrowser: (_) async => false);
    await expectLater(
        blocked.start('https://media.example.com'), throwsStateError);
    expect(storage.values, {'existing-session': 'keep'});
    final service = OIDCService(auth, storage, openBrowser: (_) async => true);
    await service.start('https://media.example.com');
    auth.failExchange = true;
    await expectLater(
        service.finish(Uri.parse('cantinarr://oidc?flow=flow-1&code=code')),
        throwsStateError);
    expect(storage.values, {'existing-session': 'keep'});
  });
}
