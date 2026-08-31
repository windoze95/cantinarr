import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart' show kIsWeb;

/// Per-request [Options] for backend-proxied calls whose response can take a
/// long time to START arriving: the server buffers and sanitizes the arr's
/// entire response before the first byte reaches the app.
///
/// Native: `receiveTimeout` bounds time-to-headers plus body-read inactivity,
/// so raising it alone suffices; `connectTimeout` (socket connect only) stays
/// null to inherit the base default so an unreachable server still fails
/// fast. Web (XHR): time-to-first-headers is bounded by `connectTimeout` — a
/// GET with only a raised `receiveTimeout` still aborts at the base default
/// with connectionTimeout — so it must be raised alongside. [isWeb] exists
/// only so VM tests can exercise the web branch.
Options longRequestOptions({
  Duration timeout = const Duration(seconds: 120),
  bool isWeb = kIsWeb,
}) =>
    Options(receiveTimeout: timeout, connectTimeout: isWeb ? timeout : null);
