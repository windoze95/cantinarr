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
  static String tmdbBackdrop(String? path, {int width = 780}) =>
      _imagePath(path, 'w$width');

  /// Full-resolution backdrop — only worth the bytes on very wide displays.
  static String tmdbBackdropOriginal(String? path) =>
      _imagePath(path, 'original');

  // A library parent can have public CDN artwork without a TMDB record.
  static String _imagePath(String? path, String size) {
    if (path == null || path.isEmpty) return '';
    final uri = Uri.tryParse(path);
    if (uri != null && (uri.scheme == 'https' || uri.scheme == 'http')) return path;
    return '$tmdbImageBase/$size$path';
  }
}
