import 'dart:convert';

import 'package:dio/dio.dart';

/// The line to show a person for a failed backend call: the server's own
/// reason when the response carries one, Dio's generic description otherwise,
/// and `toString()` for anything that is not a Dio failure.
String apiErrorMessage(Object e) {
  if (e is DioException) {
    final data = e.response?.data;
    final responseMessage = _responseErrorMessage(data);
    if (responseMessage != null) return responseMessage;
    return e.message ?? e.toString();
  }
  return e.toString();
}

/// Several handlers use Go's `http.Error`, which labels even a JSON error
/// body as text/plain. Dio intentionally leaves that response as a string, so
/// decode the small app-owned `{ "error": ... }` envelope here before falling
/// back to its generic status-code message.
String? _responseErrorMessage(Object? data) {
  Object? decoded = data;
  if (data is String) {
    final text = data.trim();
    if (text.isEmpty) return null;
    try {
      decoded = jsonDecode(text);
    } catch (_) {
      // A concise plain-text backend error is still more useful than Dio's
      // generic validateStatus explanation. Avoid surfacing HTML/proxy pages.
      if (text.length <= 500 && !text.toLowerCase().contains('<html')) {
        return text;
      }
      return null;
    }
  }
  if (decoded is Map && decoded['error'] is String) {
    final message = (decoded['error'] as String).trim();
    return message.isEmpty ? null : message;
  }
  return null;
}
