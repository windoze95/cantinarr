/// Central configuration constants for the app.
class AppConfig {
  AppConfig._();

  /// HTTP request timeout in seconds.
  static const int requestTimeoutSeconds = 15;

  /// Duration before the request timeout fires.
  static Duration get requestTimeout =>
      Duration(seconds: requestTimeoutSeconds);

  /// Number of items remaining before triggering a prefetch.
  static const int prefetchThreshold = 5;

  /// Debounce delay for search input.
  static const Duration searchDebounce = Duration(milliseconds: 400);

  /// TMDB image base URLs.
  static const String tmdbImageBase = 'https://image.tmdb.org/t/p';
  static String tmdbPoster(String? path, {int width = 500}) =>
      path != null ? '$tmdbImageBase/w$width$path' : '';
  static String tmdbBackdrop(String? path, {int width = 780}) =>
      path != null ? '$tmdbImageBase/w$width$path' : '';

  /// TMDB API base URL.
  static const String tmdbApiBase = 'https://api.themoviedb.org/3';
}
