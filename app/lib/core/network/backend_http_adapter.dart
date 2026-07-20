import 'package:dio/dio.dart';

import 'backend_http_adapter_stub.dart'
    if (dart.library.js_interop) 'backend_http_adapter_web.dart' as platform;

/// Returns the platform-specific backend adapter override, when one is needed.
HttpClientAdapter? createBackendHttpClientAdapter() =>
    platform.createBackendHttpClientAdapter();
