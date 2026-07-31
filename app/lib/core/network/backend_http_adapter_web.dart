import 'package:dio/browser.dart';
import 'package:dio/dio.dart';
import 'package:http/browser_client.dart';

import 'ai_chat_streaming_http_client_adapter.dart';

HttpClientAdapter createBackendHttpClientAdapter() =>
    AiChatStreamingHttpClientAdapter(
      fallbackAdapter: BrowserHttpClientAdapter(),
      streamingClient: BrowserClient(),
    );
