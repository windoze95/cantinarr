/// Stub implementation for platforms that don't support WebAuthn.
bool isAvailable() => false;

Future<Map<String, dynamic>> create(Map<String, dynamic> options) async {
  throw UnsupportedError('Passkeys are not supported on this platform');
}

Future<Map<String, dynamic>> get(Map<String, dynamic> options) async {
  throw UnsupportedError('Passkeys are not supported on this platform');
}
