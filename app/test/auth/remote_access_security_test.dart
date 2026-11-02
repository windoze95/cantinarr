import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:cantinarr/features/auth/data/server_status.dart';
import 'package:cantinarr/features/settings/data/remote_access_security_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('old auth status remains compatible', () {
    final status = ServerStatus.fromJson({'needs_setup': false});

    expect(status.remoteAccessSecurityAvailable, isFalse);
    expect(status.httpsRequired, isFalse);
    expect(status.connectionSecurity, isEmpty);
  });

  test('auth status reads remote access security fields', () {
    final status = ServerStatus.fromJson({
      'needs_setup': false,
      'remote_access_security_available': true,
      'connection_security': 'remote_http',
      'https_required': true,
      'require_https_outside_local_network': true,
    });

    expect(status.remoteAccessSecurityAvailable, isTrue);
    expect(status.isRemotePlainHTTP, isTrue);
    expect(status.httpsRequired, isTrue);
    expect(status.requireHTTPSOutsideLocalNetwork, isTrue);
  });

  test('admin settings report environment-managed controls', () {
    final settings = RemoteAccessSecuritySettings.fromJson({
      'require_https_outside_local_network': true,
      'trusted_proxy_cidrs': ['172.18.0.0/16'],
      'trust_cgnat_as_local': true,
      'managed_by_environment': {
        'require_https_outside_local_network': true,
        'trusted_proxy_cidrs': false,
        'trust_cgnat_as_local': true,
      },
      'connection_security': 'https',
      'would_current_connection_be_allowed': true,
    });

    expect(settings.requireHTTPSManaged, isTrue);
    expect(settings.trustedProxyCIDRsManaged, isFalse);
    expect(settings.trustCGNATManaged, isTrue);
    expect(settings.trustedProxyCIDRs, ['172.18.0.0/16']);
    expect(settings.wouldCurrentConnectionBeAllowed, isTrue);
  });

  test('password is not posted when explicit HTTP requires HTTPS', () async {
    final server = await _AuthServer.start(
      status: {
        'needs_setup': false,
        'remote_access_security_available': true,
        'connection_security': 'remote_http',
        'https_required': true,
      },
    );
    addTearDown(server.close);

    await expectLater(
      AuthService().login(
        server.url,
        'admin',
        'secret-password',
        'Test device',
        'hardware',
      ),
      throwsA(
        isA<DioException>().having(
          (error) => error.error,
          'preflight error',
          isA<HTTPSRequiredException>(),
        ),
      ),
    );
    expect(server.credentialRequests, isEmpty);
  });

  test('first-run HTTPS choice is sent after a compatible preflight', () async {
    final server = await _AuthServer.start(
      status: {
        'needs_setup': true,
        'remote_access_security_available': true,
        'connection_security': 'local_http',
      },
    );
    addTearDown(server.close);

    await AuthService().setup(
      server.url,
      'admin',
      'secret-password',
      'Test device',
      'hardware',
      requireHTTPSOutsideLocalNetwork: true,
    );

    expect(server.credentialRequests, ['/api/auth/setup']);
    expect(
      server.lastBody?['require_https_outside_local_network'],
      isTrue,
    );
  });
}

class _AuthServer {
  final HttpServer _server;
  final Map<String, dynamic> status;
  final List<String> credentialRequests = [];
  Map<String, dynamic>? lastBody;
  late final StreamSubscription<HttpRequest> _subscription;

  _AuthServer._(this._server, this.status) {
    _subscription = _server.listen(_handle);
  }

  static Future<_AuthServer> start({
    required Map<String, dynamic> status,
  }) async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    return _AuthServer._(server, status);
  }

  String get url => 'http://${_server.address.address}:${_server.port}';

  Future<void> _handle(HttpRequest request) async {
    request.response.headers.contentType = ContentType.json;
    if (request.uri.path == '/api/auth/status') {
      request.response.write(jsonEncode(status));
      await request.response.close();
      return;
    }

    credentialRequests.add(request.uri.path);
    final raw = await utf8.decoder.bind(request).join();
    if (raw.isNotEmpty) {
      lastBody = jsonDecode(raw) as Map<String, dynamic>;
    }
    request.response.write(jsonEncode({
      'access_token': 'access',
      'refresh_token': 'refresh',
      'device_id': 'device',
      'user': {
        'id': 1,
        'username': 'admin',
        'role': 'admin',
      },
    }));
    await request.response.close();
  }

  Future<void> close() async {
    await _subscription.cancel();
    await _server.close(force: true);
  }
}
