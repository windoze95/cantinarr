import 'package:dio/dio.dart';

import 'backend_http_adapter_stub.dart'
    if (dart.library.js_interop) 'backend_http_adapter_web.dart' as platform;

/// Returns the platform-specific backend adapter override, when one is needed.
HttpClientAdapter? createBackendHttpClientAdapter() =>
    platform.createBackendHttpClientAdapter();

/// Returns the platform-specific adapter for auth calls (login, refresh,
/// session validation), when one is needed. On web this routes everything
/// through Fetch: the default browser adapter was observed stalling these
/// session-critical requests for the full receive timeout while Fetch requests
/// from the same page completed instantly.
HttpClientAdapter? createAuthHttpClientAdapter() =>
    platform.createAuthHttpClientAdapter();
