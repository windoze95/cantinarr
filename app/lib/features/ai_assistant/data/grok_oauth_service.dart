import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/network/long_request_options.dart';

const _oauthValidationReceiveTimeout = Duration(seconds: 75);

enum GrokOAuthScope { personal, adminShared }

/// Personal or admin-shared xAI Grok OAuth connection status.
///
/// [selected] applies to the requested scope. Personal metadata belongs only
/// to the authenticated user; shared metadata is available only on admin
/// routes. Tokens remain server-side.
class GrokConnectionStatus {
  final bool selected;
  final bool available;
  final bool connected;
  final bool effective;
  final String accountEmail;
  final String planType;
  final DateTime? updatedAt;

  const GrokConnectionStatus({
    required this.selected,
    required this.available,
    required this.connected,
    this.effective = false,
    this.accountEmail = '',
    this.planType = '',
    this.updatedAt,
  });

  factory GrokConnectionStatus.fromJson(Map<String, dynamic> json) {
    final available = json['available'] as bool? ?? false;
    return GrokConnectionStatus(
      selected: json['selected'] as bool? ?? available,
      available: available,
      connected: json['connected'] as bool? ?? false,
      effective: json['effective'] as bool? ?? false,
      accountEmail: json['account_email'] as String? ?? '',
      planType: json['plan_type'] as String? ?? '',
      updatedAt: DateTime.tryParse(json['updated_at'] as String? ?? ''),
    );
  }
}

/// A pending device-authorization flow. The code is intended to be shown to
/// the user; no access or refresh token crosses into the app.
class GrokDeviceAuthorization {
  final String flowId;
  final Uri verificationUri;
  final String userCode;
  final Duration expiresIn;
  final Duration pollInterval;

  const GrokDeviceAuthorization({
    required this.flowId,
    required this.verificationUri,
    required this.userCode,
    required this.expiresIn,
    required this.pollInterval,
  });

  factory GrokDeviceAuthorization.fromJson(Map<String, dynamic> json) {
    final flowId = json['flow_id'] as String? ?? '';
    final userCode = json['user_code'] as String? ?? '';
    final verificationUrl = json['verification_uri'] as String? ?? '';
    final verificationUri = Uri.tryParse(verificationUrl);
    // Only xAI's own sign-in hosts are ever opened or displayed, so a
    // compromised response cannot route the user to a lookalike domain.
    final hasExactTrustedAuthority = RegExp(
      r'^https://(?:auth|accounts)\.x\.ai(?:[/?#]|$)',
    ).hasMatch(verificationUrl);
    const trustedHosts = {'auth.x.ai', 'accounts.x.ai'};
    if (verificationUri == null ||
        !hasExactTrustedAuthority ||
        verificationUri.scheme != 'https' ||
        !trustedHosts.contains(verificationUri.host) ||
        verificationUri.userInfo.isNotEmpty) {
      throw const FormatException('Invalid xAI verification URL');
    }
    if (flowId.isEmpty || userCode.isEmpty) {
      throw const FormatException('Invalid xAI device authorization');
    }

    final intervalSeconds = (json['interval'] as num?)?.toInt() ?? 5;
    return GrokDeviceAuthorization(
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

enum GrokDeviceFlowStatus { pending, connected, expired, failed }

class GrokDeviceFlowResult {
  final GrokDeviceFlowStatus status;
  final String error;
  final String accountEmail;

  const GrokDeviceFlowResult({
    required this.status,
    this.error = '',
    this.accountEmail = '',
  });

  factory GrokDeviceFlowResult.fromJson(Map<String, dynamic> json) {
    final status = switch (json['status'] as String? ?? '') {
      'connected' => GrokDeviceFlowStatus.connected,
      'expired' => GrokDeviceFlowStatus.expired,
      'failed' => GrokDeviceFlowStatus.failed,
      _ => GrokDeviceFlowStatus.pending,
    };
    return GrokDeviceFlowResult(
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

class GrokOAuthService {
  final Dio _dio;
  final GrokOAuthScope scope;

  GrokOAuthService({
    required Dio backendDio,
    this.scope = GrokOAuthScope.personal,
  }) : _dio = backendDio;

  String get _basePath => scope == GrokOAuthScope.adminShared
      ? '/api/admin/ai/grok'
      : '/api/ai/grok';

  Future<GrokConnectionStatus> getStatus() async {
    final response = await _dio.get('$_basePath/status');
    return GrokConnectionStatus.fromJson(
      response.data as Map<String, dynamic>,
    );
  }

  Future<GrokDeviceAuthorization> beginDeviceAuthorization() async {
    final response = await _dio.post('$_basePath/device/begin');
    return GrokDeviceAuthorization.fromJson(
      response.data as Map<String, dynamic>,
    );
  }

  Future<GrokDeviceFlowResult> checkDeviceAuthorization(String flowId) async {
    final safeFlowId = Uri.encodeComponent(flowId);
    try {
      final response = await _dio.get(
        '$_basePath/device/$safeFlowId',
        options: longRequestOptions(timeout: _oauthValidationReceiveTimeout),
      );
      return GrokDeviceFlowResult.fromJson(
        response.data as Map<String, dynamic>,
      );
    } on DioException catch (error) {
      final statusCode = error.response?.statusCode;
      if (statusCode == 404 || statusCode == 410) {
        return GrokDeviceFlowResult(
          status: GrokDeviceFlowStatus.expired,
          error: _responseError(error.response?.data),
        );
      }
      if (statusCode == 422) {
        return GrokDeviceFlowResult(
          status: GrokDeviceFlowStatus.failed,
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

final grokOAuthServiceProvider = Provider<GrokOAuthService>(
  (ref) => GrokOAuthService(backendDio: ref.watch(backendClientProvider)),
);

final adminGrokOAuthServiceProvider = Provider<GrokOAuthService>(
  (ref) => GrokOAuthService(
    backendDio: ref.watch(backendClientProvider),
    scope: GrokOAuthScope.adminShared,
  ),
);

/// Safe, per-user status used by Settings and the assistant's auth gate.
final grokConnectionStatusProvider =
    FutureProvider.autoDispose<GrokConnectionStatus>(
  (ref) => ref.watch(grokOAuthServiceProvider).getStatus(),
);

final adminGrokConnectionStatusProvider =
    FutureProvider.autoDispose<GrokConnectionStatus>(
  (ref) => ref.watch(adminGrokOAuthServiceProvider).getStatus(),
);
