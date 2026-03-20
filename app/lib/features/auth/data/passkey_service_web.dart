import 'dart:convert';
import 'dart:js_interop';
import 'dart:js_interop_unsafe';
import 'dart:typed_data';

// ─── JS Interop Extension Types ──────────────────────────

extension type _CredentialsContainer(JSObject _) implements JSObject {
  external JSPromise<JSObject?> create(JSObject options);
  external JSPromise<JSObject?> get(JSObject options);
}

extension type _PublicKeyCredential(JSObject _) implements JSObject {
  external String get id;
  external String get type;
  external JSArrayBuffer get rawId;
  external JSObject get response;
}

extension type _AttestationResponse(JSObject _) implements JSObject {
  external JSArrayBuffer get clientDataJSON;
  external JSArrayBuffer get attestationObject;
}

extension type _AssertionResponse(JSObject _) implements JSObject {
  external JSArrayBuffer get clientDataJSON;
  external JSArrayBuffer get authenticatorData;
  external JSArrayBuffer get signature;
  external JSArrayBuffer? get userHandle;
}

// ─── Globals ─────────────────────────────────────────────

@JS('navigator.credentials')
external _CredentialsContainer? get _navigatorCredentials;

bool isAvailable() {
  try {
    return _navigatorCredentials != null;
  } catch (_) {
    return false;
  }
}

// ─── Helpers ─────────────────────────────────────────────

Uint8List _decodeBase64Url(String base64url) {
  String normalized = base64url.replaceAll('-', '+').replaceAll('_', '/');
  while (normalized.length % 4 != 0) {
    normalized += '=';
  }
  return base64Decode(normalized);
}

String _encodeBase64Url(List<int> bytes) {
  return base64Url.encode(bytes).replaceAll('=', '');
}

/// Set a property on a JSObject using dart:js_interop_unsafe.
void _set(JSObject obj, String key, JSAny? value) {
  obj[key] = value;
}

// ─── Build JS Options ────────────────────────────────────

JSObject _buildCreationOptions(Map<String, dynamic> publicKey) {
  final user = publicKey['user'] as Map<String, dynamic>;

  final jsUser = JSObject();
  _set(jsUser, 'id', _decodeBase64Url(user['id'] as String).toJS);
  _set(jsUser, 'name', (user['name'] as String).toJS);
  _set(jsUser, 'displayName', (user['displayName'] as String).toJS);

  final pk = JSObject();
  _set(pk, 'rp', (publicKey['rp'] as Map<String, dynamic>).jsify() as JSAny);
  _set(pk, 'user', jsUser);
  _set(pk, 'challenge', _decodeBase64Url(publicKey['challenge'] as String).toJS);
  _set(pk, 'pubKeyCredParams', (publicKey['pubKeyCredParams'] as List).jsify() as JSAny);

  if (publicKey['timeout'] != null) {
    _set(pk, 'timeout', (publicKey['timeout'] as num).toJS);
  }
  if (publicKey['authenticatorSelection'] != null) {
    _set(pk, 'authenticatorSelection',
        (publicKey['authenticatorSelection'] as Map<String, dynamic>).jsify() as JSAny);
  }
  if (publicKey['attestation'] != null) {
    _set(pk, 'attestation', (publicKey['attestation'] as String).toJS);
  }

  if (publicKey['excludeCredentials'] != null) {
    final list = (publicKey['excludeCredentials'] as List).map((c) {
      final cred = c as Map<String, dynamic>;
      final jsCred = JSObject();
      _set(jsCred, 'id', _decodeBase64Url(cred['id'] as String).toJS);
      _set(jsCred, 'type', (cred['type'] as String).toJS);
      if (cred['transports'] != null) {
        _set(jsCred, 'transports', (cred['transports'] as List).jsify() as JSAny);
      }
      return jsCred as JSAny;
    }).toList();
    _set(pk, 'excludeCredentials', list.toJS);
  }

  final opts = JSObject();
  _set(opts, 'publicKey', pk);
  return opts;
}

JSObject _buildRequestOptions(Map<String, dynamic> publicKey) {
  final pk = JSObject();
  _set(pk, 'challenge', _decodeBase64Url(publicKey['challenge'] as String).toJS);

  if (publicKey['timeout'] != null) {
    _set(pk, 'timeout', (publicKey['timeout'] as num).toJS);
  }
  if (publicKey['rpId'] != null) {
    _set(pk, 'rpId', (publicKey['rpId'] as String).toJS);
  }
  if (publicKey['userVerification'] != null) {
    _set(pk, 'userVerification', (publicKey['userVerification'] as String).toJS);
  }

  if (publicKey['allowCredentials'] != null) {
    final list = (publicKey['allowCredentials'] as List).map((c) {
      final cred = c as Map<String, dynamic>;
      final jsCred = JSObject();
      _set(jsCred, 'id', _decodeBase64Url(cred['id'] as String).toJS);
      _set(jsCred, 'type', (cred['type'] as String).toJS);
      if (cred['transports'] != null) {
        _set(jsCred, 'transports', (cred['transports'] as List).jsify() as JSAny);
      }
      return jsCred as JSAny;
    }).toList();
    _set(pk, 'allowCredentials', list.toJS);
  }

  final opts = JSObject();
  _set(opts, 'publicKey', pk);
  return opts;
}

// ─── Public API ──────────────────────────────────────────

Future<Map<String, dynamic>> create(Map<String, dynamic> options) async {
  final creds = _navigatorCredentials;
  if (creds == null) throw UnsupportedError('WebAuthn not available');

  final publicKey = options['publicKey'] as Map<String, dynamic>;
  final jsOptions = _buildCreationOptions(publicKey);

  final result = await creds.create(jsOptions).toDart;
  if (result == null) throw Exception('User cancelled passkey registration');

  return _parseCreationResponse(_PublicKeyCredential(result));
}

Future<Map<String, dynamic>> get(Map<String, dynamic> options) async {
  final creds = _navigatorCredentials;
  if (creds == null) throw UnsupportedError('WebAuthn not available');

  final publicKey = options['publicKey'] as Map<String, dynamic>;
  final jsOptions = _buildRequestOptions(publicKey);

  final result = await creds.get(jsOptions).toDart;
  if (result == null) throw Exception('User cancelled passkey login');

  return _parseAssertionResponse(result);
}

// ─── Parse Responses ─────────────────────────────────────

Map<String, dynamic> _parseCreationResponse(_PublicKeyCredential cred) {
  final resp = _AttestationResponse(cred.response);

  return {
    'id': cred.id,
    'rawId': _encodeBase64Url(cred.rawId.toDart.asUint8List().toList()),
    'type': cred.type,
    'response': {
      'clientDataJSON': _encodeBase64Url(
          resp.clientDataJSON.toDart.asUint8List().toList()),
      'attestationObject': _encodeBase64Url(
          resp.attestationObject.toDart.asUint8List().toList()),
    },
  };
}

Map<String, dynamic> _parseAssertionResponse(JSObject rawCredential) {
  final cred = _PublicKeyCredential(rawCredential);
  final resp = _AssertionResponse(cred.response);

  final result = <String, dynamic>{
    'id': cred.id,
    'rawId': _encodeBase64Url(cred.rawId.toDart.asUint8List().toList()),
    'type': cred.type,
    'response': {
      'clientDataJSON': _encodeBase64Url(
          resp.clientDataJSON.toDart.asUint8List().toList()),
      'authenticatorData': _encodeBase64Url(
          resp.authenticatorData.toDart.asUint8List().toList()),
      'signature': _encodeBase64Url(
          resp.signature.toDart.asUint8List().toList()),
    },
  };

  final userHandle = resp.userHandle;
  if (userHandle != null) {
    (result['response'] as Map<String, dynamic>)['userHandle'] =
        _encodeBase64Url(userHandle.toDart.asUint8List().toList());
  }

  return result;
}
