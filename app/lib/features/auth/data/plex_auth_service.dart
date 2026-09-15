import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';

import '../../../core/storage/secure_storage.dart';
import '../logic/auth_provider.dart';
import 'auth_service.dart';
import 'oidc_service.dart';
import 'oidc_browser_stub.dart'
    if (dart.library.js_interop) 'oidc_browser_web.dart' as browser;

const _pendingKey = 'cantinarr_plex_pending';
final plexAuthServiceProvider = Provider<PlexAuthService>((ref) =>
    PlexAuthService(
        ref.read(authServiceProvider), ref.read(storageServiceProvider)));
final plexPendingProvider = FutureProvider<PlexPending?>(
    (ref) => ref.read(plexAuthServiceProvider).load());

class PlexPending {
  final String server, purpose, flow, verifier, url;
  final DateTime expires;
  final Map<String, dynamic>? result;
  const PlexPending(
      {required this.server,
      required this.purpose,
      required this.flow,
      required this.verifier,
      required this.url,
      required this.expires,
      this.result});
  Map<String, dynamic> toJson() => {
        'server': server,
        'purpose': purpose,
        'flow': flow,
        'verifier': verifier,
        'url': url,
        'expires': expires.toIso8601String(),
        if (result != null) 'result': result,
      };
  factory PlexPending.fromJson(Map<String, dynamic> data) => PlexPending(
        server: data['server'] as String,
        purpose: data['purpose'] as String,
        flow: data['flow'] as String,
        verifier: data['verifier'] as String,
        url: data['url'] as String,
        expires: DateTime.parse(data['expires'] as String),
        result: data['result'] as Map<String, dynamic>?,
      );
}

/// The saved attempt owns the server and purpose. Neither a browser return nor
/// navigation parameters can retarget it. Provider tokens never reach this app.
class PlexAuthService {
  int _epoch = 0;
  bool _adopting = false, _starting = false;
  int get epoch => _epoch;
  bool claimCompletion(int expected) {
    if (expected != _epoch || _adopting) return false;
    _adopting = true;
    return true;
  }

  void releaseCompletion() {
    _adopting = false;
  }

  final AuthService auth;
  final StorageService storage;
  final bool isWeb;
  final String? Function(String) readTab;
  final void Function(String, String?) writeTab;
  final Future<bool> Function(Uri) openBrowser;
  PlexAuthService(this.auth, this.storage,
      {this.isWeb = kIsWeb,
      this.readTab = browser.readTabValue,
      this.writeTab = browser.writeTabValue,
      Future<bool> Function(Uri)? openBrowser})
      : openBrowser = openBrowser ?? _open;
  static Future<bool> _open(Uri uri) => launchUrl(uri,
      mode: LaunchMode.externalApplication, webOnlyWindowName: '_blank');

  Future<void> _write(PlexPending? pending) async {
    final raw = pending == null ? null : jsonEncode(pending.toJson());
    if (isWeb) {
      writeTab(_pendingKey, raw);
    } else if (raw == null) {
      await storage.delete(key: _pendingKey);
    } else {
      await storage.write(key: _pendingKey, value: raw);
    }
  }

  Future<PlexPending?> load() async {
    final raw =
        isWeb ? readTab(_pendingKey) : await storage.read(key: _pendingKey);
    if (raw == null) return null;
    try {
      final p = PlexPending.fromJson(jsonDecode(raw) as Map<String, dynamic>);
      final server = Uri.parse(p.server);
      if (!['login', 'link'].contains(p.purpose) ||
          !['https', 'http'].contains(server.scheme) ||
          server.host.isEmpty ||
          server.userInfo.isNotEmpty ||
          !validPlexURL(Uri.parse(p.url)) ||
          !DateTime.now().isBefore(p.expires)) {
        await _write(null);
        return null;
      }
      return p;
    } on FormatException {
      await _write(null);
      return null;
    } on TypeError {
      await _write(null);
      return null;
    }
  }

  static bool validPlexURL(Uri uri) =>
      uri.scheme == 'https' &&
      uri.host == 'app.plex.tv' &&
      uri.port == 443 &&
      uri.path == '/auth' &&
      uri.userInfo.isEmpty;

  Future<PlexPending> start(String server,
      {String purpose = 'login',
      String? accessToken,
      String deviceName = 'Plex sign-in',
      String hardwareId = ''}) async {
    if (_starting || _checking || _adopting) {
      throw StateError('Plex sign-in is still completing. Please wait.');
    }
    _starting = true;
    try {
      return await _start(server,
          purpose: purpose,
          accessToken: accessToken,
          deviceName: deviceName,
          hardwareId: hardwareId);
    } finally {
      _starting = false;
    }
  }

  Future<PlexPending> _start(String server,
      {required String purpose,
      String? accessToken,
      required String deviceName,
      required String hardwareId}) async {
    final generation = ++_epoch;
    if (!['login', 'link'].contains(purpose)) {
      throw StateError('Invalid sign-in purpose.');
    }
    if (await load() != null) {
      throw StateError('Finish or cancel the waiting Plex sign-in first.');
    }
    final status = await auth.getServerStatus(server);
    if (!status.plexAvailable) {
      throw StateError('Plex sign-in is not available on this server.');
    }
    final random = Random.secure();
    final verifier =
        base64UrlEncode(List.generate(32, (_) => random.nextInt(256)))
            .replaceAll('=', '');
    final challenge =
        base64UrlEncode(sha256.convert(utf8.encode(verifier)).bytes)
            .replaceAll('=', '');
    final result = await auth.externalSignInRequest(server,
        purpose == 'link' ? '/api/auth/plex/link' : '/api/auth/plex/begin',
        method: 'POST',
        accessToken: purpose == 'link' ? accessToken : null,
        data: {
          'client': isWeb ? 'web' : 'mobile',
          'challenge': challenge,
          'device_name': deviceName,
          'hardware_id': hardwareId
        });
    final url = Uri.parse(result['url'] as String);
    if (!validPlexURL(url)) {
      throw StateError('The server returned an unexpected Plex address.');
    }
    final expires = DateTime.parse(result['expires_at'] as String);
    final limit = DateTime.now().add(const Duration(minutes: 10));
    final pending = PlexPending(
        server: server,
        purpose: purpose,
        flow: result['flow'] as String,
        verifier: verifier,
        url: url.toString(),
        expires: expires.isBefore(limit) ? expires : limit);
    if (generation != _epoch) {
      try {
        await auth.externalSignInRequest(server, '/api/auth/plex/cancel',
            method: 'POST', data: {'flow': pending.flow, 'verifier': verifier});
      } catch (_) {}
      throw StateError('Plex sign-in cancelled.');
    }
    await _write(pending);
    if (generation != _epoch) {
      await _write(null);
      throw StateError('Plex sign-in cancelled.');
    }
    return pending;
  }

  Future<void> reopen() async {
    final p = await load();
    if (p == null) throw StateError('Plex sign-in expired. Please try again.');
    if (!await openBrowser(Uri.parse(p.url))) {
      throw StateError(
          'The browser did not open. Select Reopen Plex to try again.');
    }
  }

  bool _checking = false;
  Future<OIDCResult?> check() async {
    if (_checking) return null;
    _checking = true;
    final generation = _epoch;
    try {
      final p = await load();
      if (p == null) {
        throw StateError('Plex sign-in expired. Please try again.');
      }
      if (p.result != null) return OIDCResult(p.server, p.purpose, p.result!);
      final proof = {'flow': p.flow, 'verifier': p.verifier};
      final checked = await auth.externalSignInRequest(
          p.server, '/api/auth/plex/check',
          method: 'POST', data: proof);
      if (generation != _epoch || checked['status'] == 'pending') return null;
      final result = await auth.externalSignInRequest(
          p.server, '/api/auth/plex/exchange',
          method: 'POST', data: {...proof, 'code': checked['code']});
      // Cancellation while a poll was in flight must not adopt its session.
      if (generation != _epoch || (await load())?.flow != p.flow) {
        if (result['access_token'] is String) {
          try {
            await auth.logout(p.server, result['access_token'] as String);
          } catch (_) {}
        }
        return null;
      }
      await _write(PlexPending(
          server: p.server,
          purpose: p.purpose,
          flow: p.flow,
          verifier: p.verifier,
          url: p.url,
          expires: p.expires,
          result: result));
      if (generation != _epoch) {
        await _write(null);
        if (result['access_token'] is String) {
          try {
            await auth.logout(p.server, result['access_token'] as String);
          } catch (_) {}
        }
        return null;
      }
      return OIDCResult(p.server, p.purpose, result);
    } on DioException catch (e) {
      if ([400, 403, 404, 409].contains(e.response?.statusCode)) {
        await _write(null);
      }
      rethrow;
    } finally {
      _checking = false;
    }
  }

  Future<void> completed() async {
    await _write(null);
    _adopting = false;
  }

  Future<bool> cancel() async {
    if (_adopting) return false;
    _epoch++;
    final p = await load();
    await _write(null);
    if (p == null) return true;
    try {
      if (p.result?['access_token'] is String) {
        await auth.logout(p.server, p.result!['access_token'] as String);
      } else {
        await auth.externalSignInRequest(p.server, '/api/auth/plex/cancel',
            method: 'POST', data: {'flow': p.flow, 'verifier': p.verifier});
      }
    } catch (_) {/* Local cancellation is immediate; server attempts expire. */}
    return true;
  }
}
