import 'package:dio/browser.dart';
import 'package:dio/dio.dart';
import 'package:http/browser_client.dart';

import 'ai_chat_streaming_http_client_adapter.dart';

HttpClientAdapter createBackendHttpClientAdapter() =>
    AiChatStreamingHttpClientAdapter(
      fallbackAdapter: BrowserHttpClientAdapter(),
      streamingClient: BrowserClient(),
    );

/// The transport for auth calls (login, refresh, session validation) on web:
/// everything rides Fetch. The default browser adapter was observed stalling
/// these session-critical requests for the full receive timeout while Fetch
/// requests from the same page completed instantly; a session must never
/// depend on the flakier transport.
HttpClientAdapter createAuthHttpClientAdapter() =>
    AiChatStreamingHttpClientAdapter(
      fallbackAdapter: BrowserHttpClientAdapter(),
      streamingClient: BrowserClient(),
      routeAllThroughFetch: true,
    );
