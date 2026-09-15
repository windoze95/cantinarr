import 'dart:async';
import 'dart:convert';
import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/plex_auth_service.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';
import 'oidc_service_test.dart' show MemoryStorage;

class PlexAuthFake extends AuthService {
  bool available = true, approved = false;
  String url = 'https://app.plex.tv/auth#?code=strong-code';
  int? failure;
  int exchanges = 0, cancellations = 0, logouts = 0;
  Map<String, dynamic>? beginData;
  String? beginToken, checkedServer;
  Completer<void>? holdExchange, holdBegin;
  @override
  Future<ServerStatus> getServerStatus(String serverUrl) async =>
      ServerStatus(needsSetup: false, plexAvailable: available);
  @override
  Future<Map<String, dynamic>> externalSignInRequest(String server, String path,
      {String method = 'GET',
      String? accessToken,
      Map<String, dynamic>? data}) async {
    if (path.endsWith('/cancel')) {
      cancellations++;
      return {'status': 'cancelled'};
    }
    if (path.endsWith('/check')) {
      checkedServer = server;
      if (failure != null) {
        throw DioException(
            requestOptions: RequestOptions(path: path),
            response: Response(
                requestOptions: RequestOptions(path: path),
                statusCode: failure));
      }
      return approved
          ? {'status': 'complete', 'code': 'one-use-ticket'}
          : {'status': 'pending'};
    }
    if (path.endsWith('/exchange')) {
      exchanges++;
      if (holdExchange != null) await holdExchange!.future;
      return {
        'access_token': 'new-access',
        'refresh_token': 'new-refresh',
        'device_id': 'new-device',
        'user': {'id': 2, 'username': 'viewer', 'role': 'user'}
      };
    }
    beginData = data;
    beginToken = accessToken;
    if (holdBegin != null) await holdBegin!.future;
    return {
      'flow': 'unique-flow',
      'url': url,
      'expires_at': DateTime.now()
          .add(const Duration(minutes: 10))
          .toUtc()
          .toIso8601String()
    };
  }

  @override
  Future<void> logout(String serverUrl, String accessToken) async {
    logouts++;
  }
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  test('older servers default Plex to unavailable', () {
    expect(
        ServerStatus.fromJson({'needs_setup': false}).plexAvailable, isFalse);
  });
  test(
      'native restart retains verifier, server and purpose; login sends no existing token',
      () async {
    final auth = PlexAuthFake(), storage = MemoryStorage();
    final service = PlexAuthService(auth, storage, isWeb: false);
    final pending = await service.start('https://original.example',
        accessToken: 'existing-access');
    expect(auth.beginToken, isNull);
    expect(
        base64UrlEncode(sha256.convert(utf8.encode(pending.verifier)).bytes)
            .replaceAll('=', ''),
        auth.beginData!['challenge']);
    expect(auth.beginData!.containsKey('verifier'), isFalse);
    final resumed = PlexAuthService(auth, storage, isWeb: false);
    expect((await resumed.load())!.verifier, pending.verifier);
    auth.approved = true;
    final result = await resumed.check();
    expect(result!.server, 'https://original.example');
    expect(result.purpose, 'login');
    expect(auth.checkedServer, 'https://original.example');
    // A failed config read can retry adoption without replaying exchange.
    expect((await resumed.check())!.data['access_token'], 'new-access');
    expect(auth.exchanges, 1);
    await resumed.completed();
    expect(await service.load(), isNull);
  });
  test('browser-tab storage stays separate and launch failure allows reopen',
      () async {
    final auth = PlexAuthFake(), storage = MemoryStorage();
    final tab = <String, String>{};
    var launches = 0;
    final service = PlexAuthService(auth, storage,
        isWeb: true,
        readTab: (key) => tab[key],
        writeTab: (key, value) {
          if (value == null) {
            tab.remove(key);
          } else {
            tab[key] = value;
          }
        },
        openBrowser: (_) async => ++launches > 1);
    await service.start('https://original.example',
        purpose: 'link', accessToken: 'existing-access');
    expect(auth.beginToken, 'existing-access');
    expect(storage.values, isEmpty);
    await expectLater(service.reopen(), throwsStateError);
    expect(await service.load(), isNotNull);
    await service.reopen();
    expect(launches, 2);
    await service.cancel();
    expect(tab, isEmpty);
    expect(auth.cancellations, 1);
  });
  test('cancel during initiation removes the newly created attempt', () async {
    final auth = PlexAuthFake()..holdBegin = Completer<void>();
    final service = PlexAuthService(auth, MemoryStorage(), isWeb: false);
    final starting = service.start('https://original.example');
    while (auth.beginData == null) {
      await Future<void>.delayed(Duration.zero);
    }
    await service.cancel();
    final cancelled = expectLater(starting, throwsStateError);
    auth.holdBegin!.complete();
    await cancelled;
    expect(await service.load(), isNull);
    expect(auth.cancellations, 1);
  });
  test(
      'cancel during exchange prevents session adoption and revokes the new session',
      () async {
    final auth = PlexAuthFake()
      ..approved = true
      ..holdExchange = Completer<void>();
    final service = PlexAuthService(auth, MemoryStorage(), isWeb: false);
    await service.start('https://original.example');
    final checking = service.check();
    while (auth.exchanges == 0) {
      await Future<void>.delayed(Duration.zero);
    }
    await service.cancel();
    auth.holdExchange!.complete();
    expect(await checking, isNull);
    expect(auth.logouts, 1);
    expect(await service.load(), isNull);
  });
  test('terminal denials clear the attempt, transient failures retain it',
      () async {
    for (final status in [400, 403, 404, 409, 503]) {
      final auth = PlexAuthFake()..failure = status;
      final service = PlexAuthService(auth, MemoryStorage(), isWeb: false);
      await service.start('https://original.example');
      await expectLater(service.check(), throwsA(isA<DioException>()));
      expect(await service.load(), status == 503 ? isNotNull : isNull);
    }
  });
  test(
      'expired and malformed persisted attempts clear; unrelated connection survives',
      () async {
    final storage = MemoryStorage();
    storage.values['jwt'] = 'existing-token';
    final service = PlexAuthService(PlexAuthFake(), storage, isWeb: false);
    await service.start('https://original.example');
    final key =
        storage.values.keys.singleWhere((k) => k.contains('plex_pending'));
    final data = jsonDecode(storage.values[key]!) as Map<String, dynamic>;
    data['expires'] =
        DateTime.now().subtract(const Duration(seconds: 1)).toIso8601String();
    storage.values[key] = jsonEncode(data);
    expect(await service.load(), isNull);
    expect(storage.values['jwt'], 'existing-token');
    storage.values[key] = '{broken';
    expect(await service.load(), isNull);
  });
  test('untrusted provider URL is refused before saving or opening', () async {
    final auth = PlexAuthFake()..url = 'https://evil.example/auth';
    final storage = MemoryStorage();
    final service = PlexAuthService(auth, storage, isWeb: false);
    await expectLater(
        service.start('https://original.example'), throwsStateError);
    expect(storage.values, isEmpty);
  });
}
