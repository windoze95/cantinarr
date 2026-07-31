import 'package:dio/dio.dart';

/// A deliberately narrow debug logger for backend traffic.
///
/// It never serializes a Dio request, response, or exception. Log lines contain
/// only a fixed HTTP-method label, a route-shaped path made exclusively from
/// app-owned literals, a numeric response status, and elapsed milliseconds.
/// Headers, cookies, bodies, hosts/user-info, query strings, fragments, error
/// messages, and dynamic path values are never passed to [logPrint].
class SafeHttpLogInterceptor extends Interceptor {
  SafeHttpLogInterceptor({
    required this.logPrint,
    int Function()? nowMicros,
  }) : _nowMicros = nowMicros ?? (() => DateTime.now().microsecondsSinceEpoch);

  final void Function(String message) logPrint;
  final int Function() _nowMicros;

  static const _startedAtKey = 'cantinarr_safe_log_started_at';

  /// Only literals at route positions controlled by the app are retained.
  /// Everything else becomes an ellipsis, so an identifier/token embedded in
  /// a path can never be printed merely because it looks harmless.
  static const _apiScopes = <String>{
    'admin',
    'assistant',
    'auth',
    'chaptarr',
    'config',
    'connect',
    'discover',
    'downloads',
    'health',
    'instances',
    'issues',
    'notifications',
    'plex',
    'push',
    'radarr',
    'requests',
    'search',
    'setup',
    'sonarr',
    'tautulli',
    'tmdb',
  };

  /// Third-level route literals that are safe only within their known scope.
  /// Later segments are always dynamic/redacted.
  static const _thirdSegmentByScope = <String, Set<String>>{
    'admin': {
      'agent-actions',
      'agent-runs',
      'credentials',
      'issues',
      'remediation-settings',
      'requests',
      'setup-status',
      'tools',
      'update-status',
      'users',
    },
    'auth': {
      'login',
      'logout',
      'me',
      'passkeys',
      'refresh',
      'register',
    },
  };

  static const _methods = <String>{
    'DELETE',
    'GET',
    'HEAD',
    'OPTIONS',
    'PATCH',
    'POST',
    'PUT',
  };

  @override
  void onRequest(RequestOptions options, RequestInterceptorHandler handler) {
    options.extra[_startedAtKey] = _nowMicros();
    logPrint(
      '[HTTP] -> ${_safeMethod(options.method)} ${sanitizedPath(options.uri)}',
    );
    handler.next(options);
  }

  @override
  void onResponse(
      Response<dynamic> response, ResponseInterceptorHandler handler) {
    final options = response.requestOptions;
    final status = response.statusCode?.toString() ?? 'no_status';
    logPrint(
      '[HTTP] <- ${_safeMethod(options.method)} '
      '${sanitizedPath(options.uri)} $status ${_elapsedMs(options)}ms',
    );
    handler.next(response);
  }

  @override
  void onError(DioException err, ErrorInterceptorHandler handler) {
    final options = err.requestOptions;
    final status = err.response?.statusCode?.toString() ?? 'network_error';
    logPrint(
      '[HTTP] !! ${_safeMethod(options.method)} '
      '${sanitizedPath(options.uri)} $status ${_elapsedMs(options)}ms',
    );
    handler.next(err);
  }

  int _elapsedMs(RequestOptions options) {
    final started = options.extra[_startedAtKey];
    if (started is! int) return 0;
    final elapsed =
        (_nowMicros() - started) ~/ Duration.microsecondsPerMillisecond;
    return elapsed < 0 ? 0 : elapsed;
  }

  static String _safeMethod(String raw) {
    final method = raw.toUpperCase();
    return _methods.contains(method) ? method : 'OTHER';
  }

  /// Returns a route-shaped path without copying any dynamic value from [uri].
  /// Query parameters, fragments, authority/user-info, and later path segments
  /// are discarded rather than redacted in-place.
  static String sanitizedPath(Uri uri) {
    final segments = uri.pathSegments;
    if (segments.isEmpty) return '/';
    if (segments.first != 'api') return '/…';
    if (segments.length == 1) return '/api';

    final scope = segments[1];
    if (!_apiScopes.contains(scope)) return '/api/…';
    final safe = <String>['api', scope];
    var consumed = 2;

    final thirdAllowed = _thirdSegmentByScope[scope];
    if (segments.length > 2 &&
        thirdAllowed != null &&
        thirdAllowed.contains(segments[2])) {
      safe.add(segments[2]);
      consumed = 3;
    }

    final suffix = segments.length > consumed ? '/…' : '';
    return '/${safe.join('/')}$suffix';
  }
}
