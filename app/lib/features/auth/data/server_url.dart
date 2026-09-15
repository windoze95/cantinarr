/// Normalize an entered server address without changing its scheme, port,
/// or base path. A bare address starts with HTTPS; checkServer owns probing.
String normalizeServerUrl(String url) {
  var normalized = url.trim();
  final schemeProbe = normalized.toLowerCase();
  if (schemeProbe.startsWith('http://')) {
    normalized = 'http://${normalized.substring('http://'.length)}';
  } else if (schemeProbe.startsWith('https://')) {
    normalized = 'https://${normalized.substring('https://'.length)}';
  } else {
    normalized = 'https://$normalized';
  }
  while (normalized.endsWith('/')) {
    normalized = normalized.substring(0, normalized.length - 1);
  }
  return normalized;
}
