import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';

enum CodexOAuthScope { personal, adminShared }

/// One Codex usage window reported by the linked ChatGPT account.
class CodexRateLimitWindow {
  final double usedPercent;
  final DateTime? resetsAt;

  const CodexRateLimitWindow({
    required this.usedPercent,
    this.resetsAt,
  });

  factory CodexRateLimitWindow.fromJson(Map<String, dynamic> json) {
    final resetSeconds = (json['resets_at'] as num?)?.toInt();
    return CodexRateLimitWindow(
      usedPercent: (json['used_percent'] as num?)?.toDouble() ?? 0,
      resetsAt: resetSeconds == null
          ? null
          : DateTime.fromMillisecondsSinceEpoch(
              resetSeconds * Duration.millisecondsPerSecond,
              isUtc: true,
            ),
    );
  }
}

class CodexRateLimits {
  final CodexRateLimitWindow? primary;
  final CodexRateLimitWindow? secondary;

  const CodexRateLimits({this.primary, this.secondary});

  bool get isEmpty => primary == null && secondary == null;

  factory CodexRateLimits.fromJson(Map<String, dynamic> json) =>
      CodexRateLimits(
        primary: _rateLimitWindow(json['primary']),
        secondary: _rateLimitWindow(json['secondary']),
      );
}

CodexRateLimitWindow? _rateLimitWindow(Object? value) =>
    value is Map<String, dynamic> ? CodexRateLimitWindow.fromJson(value) : null;

/// Personal or admin-shared ChatGPT/Codex connection status.
///
/// [selected] applies to the requested scope. Personal metadata belongs only
/// to the authenticated user; shared metadata is available only on admin
/// routes. Tokens remain server-side.
class CodexConnectionStatus {
  final bool selected;
  final bool available;
  final bool connected;
  final bool effective;
  final String accountEmail;
  final String planType;
  final CodexRateLimits? rateLimits;
  final DateTime? updatedAt;
  final bool stale;

  const CodexConnectionStatus({
    required this.selected,
    required this.available,
    required this.connected,
    this.effective = false,
    this.accountEmail = '',
    this.planType = '',
    this.rateLimits,
    this.updatedAt,
    this.stale = false,
  });

  factory CodexConnectionStatus.fromJson(Map<String, dynamic> json) {
    final rawLimits = json['rate_limits'];
    final available = json['available'] as bool? ?? false;
    return CodexConnectionStatus(
      selected: json['selected'] as bool? ?? available,
      available: available,
      connected: json['connected'] as bool? ?? false,
      effective: json['effective'] as bool? ?? false,
      accountEmail: json['account_email'] as String? ?? '',
      planType: json['plan_type'] as String? ?? '',
      updatedAt: DateTime.tryParse(json['updated_at'] as String? ?? ''),
      stale: json['stale'] as bool? ?? false,
      rateLimits: rawLimits is Map<String, dynamic>
          ? CodexRateLimits.fromJson(rawLimits)
          : null,
    );
  }
}

/// A pending device-authorization flow. The code is intended to be shown to
/// the user; no access or refresh token crosses into the app.
class CodexDeviceAuthorization {
  final String flowId;
  final Uri verificationUri;
  final String userCode;
  final Duration expiresIn;
  final Duration pollInterval;

  const CodexDeviceAuthorization({
    required this.flowId,
    required this.verificationUri,
    required this.userCode,
    required this.expiresIn,
    required this.pollInterval,
  });

  factory CodexDeviceAuthorization.fromJson(Map<String, dynamic> json) {
    final flowId = json['flow_id'] as String? ?? '';
    final userCode = json['user_code'] as String? ?? '';
    final verificationUrl = json['verification_uri'] as String? ?? '';
    final verificationUri = Uri.tryParse(verificationUrl);
    final hasExactTrustedAuthority = RegExp(
      r'^https://auth\.openai\.com(?:[/?#]|$)',
    ).hasMatch(verificationUrl);
    if (verificationUri == null ||
        !hasExactTrustedAuthority ||
        verificationUri.scheme != 'https' ||
        verificationUri.host != 'auth.openai.com' ||
        verificationUri.userInfo.isNotEmpty) {
      throw const FormatException('Invalid ChatGPT verification URL');
    }
    if (flowId.isEmpty || userCode.isEmpty) {
      throw const FormatException('Invalid ChatGPT device authorization');
    }

    final intervalSeconds = (json['interval'] as num?)?.toInt() ?? 5;
    return CodexDeviceAuthorization(
      flowId: flowId,
      verificationUri: verificationUri,
      userCode: userCode,
      expiresIn: Duration(
        seconds: (json['expires_in'] as num?)?.toInt() ?? 900,
      ),
      pollInterval: Duration(
        seconds: intervalSeconds.clamp(1, 60).toInt(),
      ),
    );
  }
}

enum CodexDeviceFlowStatus { pending, connected, expired, failed }

class CodexDeviceFlowResult {
  final CodexDeviceFlowStatus status;
  final String error;
  final String accountEmail;

  const CodexDeviceFlowResult({
    required this.status,
    this.error = '',
    this.accountEmail = '',
  });

  factory CodexDeviceFlowResult.fromJson(Map<String, dynamic> json) {
    final status = switch (json['status'] as String? ?? '') {
      'connected' => CodexDeviceFlowStatus.connected,
      'expired' => CodexDeviceFlowStatus.expired,
      'failed' => CodexDeviceFlowStatus.failed,
      _ => CodexDeviceFlowStatus.pending,
    };
    return CodexDeviceFlowResult(
      status: status,
      error: json['error'] as String? ?? '',
      accountEmail: _accountEmail(json['account']),
    );
  }
}

String _accountEmail(Object? account) {
  if (account is String) return account;
  if (account is Map<String, dynamic>) {
    return account['email'] as String? ?? '';
  }
  return '';
}

class CodexOAuthService {
  final Dio _dio;
  final CodexOAuthScope scope;

  CodexOAuthService({
    required Dio backendDio,
    this.scope = CodexOAuthScope.personal,
  }) : _dio = backendDio;

  String get _basePath => scope == CodexOAuthScope.adminShared
      ? '/api/admin/ai/codex'
      : '/api/ai/codex';

  Future<CodexConnectionStatus> getStatus() async {
    final response = await _dio.get('$_basePath/status');
    return CodexConnectionStatus.fromJson(
      response.data as Map<String, dynamic>,
    );
  }

  Future<CodexDeviceAuthorization> beginDeviceAuthorization() async {
    final response = await _dio.post('$_basePath/device/begin');
    return CodexDeviceAuthorization.fromJson(
      response.data as Map<String, dynamic>,
    );
  }

  Future<CodexDeviceFlowResult> checkDeviceAuthorization(String flowId) async {
    final safeFlowId = Uri.encodeComponent(flowId);
    try {
      final response = await _dio.get('$_basePath/device/$safeFlowId');
      return CodexDeviceFlowResult.fromJson(
        response.data as Map<String, dynamic>,
      );
    } on DioException catch (error) {
      final statusCode = error.response?.statusCode;
      if (statusCode == 404 || statusCode == 410) {
        return CodexDeviceFlowResult(
          status: CodexDeviceFlowStatus.expired,
          error: _responseError(error.response?.data),
        );
      }
      rethrow;
    }
  }

  Future<void> cancelDeviceAuthorization(String flowId) async {
    final safeFlowId = Uri.encodeComponent(flowId);
    await _dio.delete('$_basePath/device/$safeFlowId');
  }

  Future<void> unlink() async {
    await _dio.delete(_basePath);
  }
}

String _responseError(Object? data) {
  if (data is Map<String, dynamic>) {
    return data['error'] as String? ?? '';
  }
  return '';
}

final codexOAuthServiceProvider = Provider<CodexOAuthService>(
  (ref) => CodexOAuthService(backendDio: ref.watch(backendClientProvider)),
);

final adminCodexOAuthServiceProvider = Provider<CodexOAuthService>(
  (ref) => CodexOAuthService(
    backendDio: ref.watch(backendClientProvider),
    scope: CodexOAuthScope.adminShared,
  ),
);

/// Safe, per-user status used by Settings and the assistant's auth gate.
final codexConnectionStatusProvider =
    FutureProvider.autoDispose<CodexConnectionStatus>(
  (ref) => ref.watch(codexOAuthServiceProvider).getStatus(),
);

final adminCodexConnectionStatusProvider =
    FutureProvider.autoDispose<CodexConnectionStatus>(
  (ref) => ref.watch(adminCodexOAuthServiceProvider).getStatus(),
);
