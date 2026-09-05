import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/storage/secure_storage.dart';
import '../logic/auth_provider.dart';
import 'auth_service.dart';
import 'oidc_browser_stub.dart'
    if (dart.library.js_interop) 'oidc_browser_web.dart' as browser;

const _pendingKey = 'cantinarr_oidc_pending';

final oidcServiceProvider = Provider<OIDCService>((ref) => OIDCService(
    ref.read(authServiceProvider), ref.read(storageServiceProvider)));

class OIDCResult {
  final String server;
  final String purpose;
  final Map<String, dynamic> data;
  const OIDCResult(this.server, this.purpose, this.data);
}

/// Only the verifier and the originating server survive an app restart.
/// A callback cannot choose a server, change purpose, or replace a session.
class OIDCService {
  final AuthService auth;
  final StorageService storage;
  final Future<bool> Function(Uri) openBrowser;
  final bool isWeb;
  final String? Function(String) readTab;
  final void Function(String, String?) writeTab;
  final void Function(String) navigate;
  final void Function() clearReturnAddress;
  final String? currentOrigin;
  OIDCService(this.auth, this.storage,
      {Future<bool> Function(Uri)? openBrowser,
      this.isWeb = kIsWeb,
      this.readTab = browser.readTabValue,
      this.writeTab = browser.writeTabValue,
      this.navigate = browser.replaceBrowserLocation,
      this.clearReturnAddress = browser.clearOIDCReturnAddress,
      this.currentOrigin})
      : openBrowser = openBrowser ?? _openBrowser;

  static Future<bool> _openBrowser(Uri uri) =>
      launchUrl(uri, mode: LaunchMode.externalApplication);

  Future<void> _write(String? value) async {
    if (isWeb) {
      writeTab(_pendingKey, value);
    } else if (value == null) {
      await storage.delete(key: _pendingKey);
    } else {
      await storage.write(key: _pendingKey, value: value);
    }
  }

  Future<void> start(
    String server, {
    String purpose = 'login',
    String? accessToken,
    String? invitation,
    String? externalOrigin,
    String deviceName = 'Single sign-on',
    String hardwareId = '',
  }) async {
    if (!isWeb &&
        defaultTargetPlatform != TargetPlatform.iOS &&
        defaultTargetPlatform != TargetPlatform.android &&
        openBrowser == _openBrowser) {
      throw StateError('Single sign-on is available on web, iOS and Android.');
    }
    final status = await auth.getServerStatus(server);
    final origin = externalOrigin ?? status.ssoOrigin;
    if (origin.isEmpty) {
      throw StateError(status.ssoError ?? 'Single sign-on is not configured.');
    }
    if (isWeb && (currentOrigin ?? Uri.base.origin) != origin) {
      // Move before creating a verifier: tab storage belongs to an origin.
      final target = purpose == 'test'
          ? '/settings/oidc'
          : purpose == 'link'
              ? '/settings/sso-account'
              : Uri(path: '/oidc/start', queryParameters: {
                  if (invitation != null) 'invitation': invitation,
                }).toString();
      navigate('$origin/#$target');
      return;
    }
    final random = Random.secure();
    final verifier =
        base64UrlEncode(List.generate(32, (_) => random.nextInt(256)))
            .replaceAll('=', '');
    final challenge =
        base64UrlEncode(sha256.convert(utf8.encode(verifier)).bytes)
            .replaceAll('=', '');
    final endpoint = purpose == 'test'
        ? '/api/admin/oidc/test'
        : purpose == 'link'
            ? '/api/auth/oidc/link'
            : '/api/auth/oidc/begin';
    final result = await auth.oidcRequest(server, endpoint,
        method: 'POST',
        accessToken: purpose == 'login' ? null : accessToken,
        data: {
          'client': isWeb ? 'web' : 'mobile',
          'challenge': challenge,
          'device_name': deviceName,
          'hardware_id': hardwareId,
          if (invitation != null) 'invitation': invitation,
        });
    final start = Uri.parse(result['start_url'] as String);
    if (start.origin != origin || start.path != '/api/auth/oidc/start') {
      throw StateError('The server returned an unexpected sign-in address.');
    }
    await _write(jsonEncode({
      'server': isWeb ? origin : server,
      'purpose': purpose,
      'flow': result['flow'],
      'verifier': verifier,
      'expires': DateTime.now()
          .add(const Duration(minutes: 11))
          .millisecondsSinceEpoch,
    }));
    if (isWeb) {
      navigate(start.toString());
    } else if (!await openBrowser(start)) {
      await _write(null);
      throw StateError('Could not open the sign-in browser. Please try again.');
    }
  }

  Future<OIDCResult> finish(Uri uri) async {
    final code = uri.queryParameters['code'];
    final flow = uri.queryParameters['flow'];
    if (code == null || flow == null) {
      throw StateError('This sign-in link is incomplete.');
    }
    final raw =
        isWeb ? readTab(_pendingKey) : await storage.read(key: _pendingKey);
    if (raw == null) {
      throw StateError(
          'No sign-in is waiting in this app or tab. Please start again.');
    }
    final pending = jsonDecode(raw) as Map<String, dynamic>;
    if (pending['flow'] != flow) {
      throw StateError('This link belongs to a different sign-in attempt.');
    }
    if (DateTime.now().millisecondsSinceEpoch > (pending['expires'] as int)) {
      await _write(null);
      throw StateError('Sign-in expired. Please start again.');
    }
    await _write(null);
    if (isWeb) clearReturnAddress();
    final server = pending['server'] as String;
    final data = await auth.oidcRequest(server, '/api/auth/oidc/exchange',
        method: 'POST',
        data: {'code': code, 'flow': flow, 'verifier': pending['verifier']});
    return OIDCResult(server, pending['purpose'] as String, data);
  }
}
