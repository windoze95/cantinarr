import 'server_url.dart';

/// Classifies the shared connection field before an address is probed. An
/// incomplete invitation must never be sent as a server URL, including its token.
class ConnectionInput {
  final String value;
  final bool isLink;

  const ConnectionInput._(this.value, {required this.isLink});

  factory ConnectionInput.parse(String input) {
    final value = input.trim();
    if (value.isEmpty) {
      throw const FormatException(
          'Paste a connection link or enter your Cantinarr server address.');
    }

    Uri? uri;
    Map<String, String> parameters;
    try {
      uri = Uri.tryParse(value);
      parameters = uri?.queryParameters ?? const {};
    } on FormatException {
      throw const FormatException('Check the connection link or server address.');
    }

    if (value.toLowerCase().startsWith('cantinarr:') ||
        (parameters.containsKey('token') && parameters.containsKey('server'))) {
      final server = parameters['server'];
      if (uri?.scheme != 'cantinarr' || uri?.host != 'connect' ||
          (parameters['token']?.trim().isEmpty ?? true) ||
          server == null || !_isServerAddress(server)) {
        throw const FormatException(
            'This connection link is incomplete or invalid. '
            'Ask your server admin for a new link.');
      }
      return ConnectionInput._(value, isLink: true);
    }

    if (!_isServerAddress(value)) {
      throw const FormatException(
          'Enter your Cantinarr server address, such as '
          'http://192.168.1.10:8585.');
    }
    // Keep a bare address bare: checkServer owns HTTPS/HTTP probing.
    return ConnectionInput._(value, isLink: false);
  }

  static bool _isServerAddress(String value) {
    if (value.trim().isEmpty || RegExp(r'\s').hasMatch(value.trim())) return false;
    try {
      final uri = Uri.tryParse(normalizeServerUrl(value));
      if (uri == null || uri.host.isEmpty) return false;
      // A different explicit scheme must not become an HTTPS hostname.
      final scheme = Uri.tryParse(value.trim())?.scheme;
      return !value.contains('://') || scheme == 'http' || scheme == 'https';
    } on FormatException {
      return false;
    }
  }
}
