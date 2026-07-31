/// True when a mapping's arr-side prefix and a library root folder the arr
/// reports could describe the same files: segment-wise, one path must contain
/// the other. A mapping that is unrelated to every reported root can never
/// match a real file, which is the one misconfiguration worth warning about.
///
/// The check is deliberately looser than the server's translator: both slash
/// directions are accepted and comparison is case-insensitive, because a
/// silently missed warning is harmless while a false alarm on a working
/// mapping is not.
bool arrPathRelatesToReportedRoot(String arrPath, String reportedRoot) {
  final mapping = _segments(arrPath);
  final root = _segments(reportedRoot);
  final length = mapping.length < root.length ? mapping.length : root.length;
  for (var i = 0; i < length; i++) {
    if (mapping[i] != root[i]) return false;
  }
  return true;
}

List<String> _segments(String path) => path
    .replaceAll('\\', '/')
    .toLowerCase()
    .split('/')
    .where((part) => part.isNotEmpty && part != '.')
    .toList(growable: false);
