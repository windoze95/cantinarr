/// Converts small HTML fragments returned by metadata providers into readable
/// plain text. It intentionally preserves paragraph/list boundaries while
/// discarding markup that Flutter's requester surfaces do not render.
String metadataPlainText(String? value) {
  var text = value?.trim() ?? '';
  if (text.isEmpty) return '';

  text = text
      .replaceAll(RegExp(r'<\s*br\s*/?\s*>', caseSensitive: false), '\n')
      .replaceAll(
          RegExp(r'<\s*/\s*(p|div|li)\s*>', caseSensitive: false), '\n')
      .replaceAll(RegExp(r'<\s*li(?:\s[^>]*)?>', caseSensitive: false), '• ')
      .replaceAll(RegExp(r'<[^>]*>', multiLine: true), '');

  const named = {
    'amp': '&',
    'apos': "'",
    '#39': "'",
    'quot': '"',
    'lt': '<',
    'gt': '>',
    'nbsp': ' ',
    'ndash': '–',
    'mdash': '—',
    'hellip': '…',
  };
  text = text.replaceAllMapped(RegExp(r'&([A-Za-z]+|#\d+|#x[0-9A-Fa-f]+);'),
      (match) {
    final entity = match.group(1)!;
    final replacement = named[entity.toLowerCase()];
    if (replacement != null) return replacement;
    int? codePoint;
    if (entity.startsWith('#x')) {
      codePoint = int.tryParse(entity.substring(2), radix: 16);
    } else if (entity.startsWith('#')) {
      codePoint = int.tryParse(entity.substring(1));
    }
    if (codePoint == null || codePoint < 0 || codePoint > 0x10ffff) {
      return match.group(0)!;
    }
    return String.fromCharCode(codePoint);
  });

  return text
      .split('\n')
      .map((line) => line.replaceAll(RegExp(r'[ \t]+'), ' ').trim())
      .where((line) => line.isNotEmpty)
      .join('\n\n');
}
