/// Central configuration constants for the app.
class AppConfig {
  AppConfig._();

  /// HTTP request timeout in seconds.
  static const int requestTimeoutSeconds = 15;

  /// Duration before the request timeout fires.
  static Duration get requestTimeout =>
      const Duration(seconds: requestTimeoutSeconds);

  /// Number of items remaining before triggering a prefetch.
  static const int prefetchThreshold = 5;

  /// Debounce delay for search input.
  static const Duration searchDebounce = Duration(milliseconds: 400);

  /// TMDB image base URLs (public CDN, no key needed).
  static const String tmdbImageBase = 'https://image.tmdb.org/t/p';
  /// Streaming-service logos; `w92` is TMDB's nearest size to a 24px chip
  /// avatar at 3x. Empty for a missing path so the image widget falls back.
  static String tmdbLogo(String? path, {int width = 92}) =>
      path != null ? '$tmdbImageBase/w$width$path' : '';
  static String tmdbPoster(String? path, {int width = 500}) =>
      _imagePath(path, 'w$width');

  /// Request the smallest CDN poster that covers the physical display width.
  /// Resizing at the CDN avoids downloading and decoding a large original.
  static String tmdbPosterForDisplay(String? path, double physicalWidth) {
    for (final width in [92, 154, 185, 342, 500, 780]) {
      if (width >= physicalWidth) return _imagePath(path, 'w$width');
    }
    return _imagePath(path, 'original');
  }
  static String tmdbBackdrop(String? path, {int width = 780}) =>
      _imagePath(path, 'w$width');

  /// Full-resolution backdrop — only worth the bytes on very wide displays.
  static String tmdbBackdropOriginal(String? path) =>
      _imagePath(path, 'original');

  // Library providers often return absolute TMDB originals. Apply the same
  // requested size as a relative TMDB path; keep other providers untouched.
  static String _imagePath(String? path, String size) {
    if (path == null || path.isEmpty) return '';
    final uri = Uri.tryParse(path);
    if (uri != null && (uri.scheme == 'https' || uri.scheme == 'http')) {
      final parts = uri.pathSegments;
      if (uri.host == 'image.tmdb.org' && !uri.hasPort && uri.userInfo.isEmpty &&
          parts.length == 4 && parts[0] == 't' && parts[1] == 'p' &&
          (parts[2] == 'original' || RegExp(r'^w\d+$').hasMatch(parts[2])) &&
          parts[3].isNotEmpty && !parts[3].toLowerCase().endsWith('.svg')) {
        return uri.replace(pathSegments: ['t', 'p', size, parts[3]]).toString();
      }
      return path;
    }
    return '$tmdbImageBase/$size$path';
  }
}
