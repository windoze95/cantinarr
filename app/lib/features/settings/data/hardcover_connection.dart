/// Safe device-flow metadata. Credentials and the provider's device code never
/// cross the server boundary.
class HardcoverDeviceFlow {
  final String flowId;
  final String status;
  final String userCode;
  final Uri? verificationUri;
  final DateTime? expiresAt;
  final Duration interval;
  final String connectionId;
  final String error;

  const HardcoverDeviceFlow({
    required this.flowId,
    required this.status,
    this.userCode = '',
    this.verificationUri,
    this.expiresAt,
    this.interval = const Duration(seconds: 5),
    this.connectionId = '',
    this.error = '',
  });

  factory HardcoverDeviceFlow.fromJson(Map<String, dynamic> json) {
    final rawUrl = json['verification_uri'] as String? ?? '';
    final uri = rawUrl.isEmpty ? null : Uri.tryParse(rawUrl);
    if (rawUrl.isNotEmpty &&
        (uri == null ||
            uri.scheme != 'https' ||
            uri.authority != 'hardcover.app' ||
            uri.path != '/link' ||
            uri.hasFragment ||
            uri.userInfo.isNotEmpty)) {
      throw const FormatException('Invalid Hardcover verification URL');
    }
    final status = json['status'] as String? ?? '';
    final code = json['user_code'] as String? ?? '';
    final id = json['flow_id'] as String? ?? '';
    final expiry = DateTime.tryParse(json['expires_at'] as String? ?? '');
    final seconds = (json['interval'] as num?)?.toInt() ?? 5;
    if (id.isEmpty ||
        seconds < 1 ||
        seconds > 86400 ||
        (status == 'pending' &&
            (code.isEmpty || uri == null || expiry == null))) {
      throw const FormatException('Invalid Hardcover sign-in response');
    }
    return HardcoverDeviceFlow(
      flowId: id,
      status: status,
      userCode: code,
      verificationUri: uri,
      expiresAt: expiry,
      interval: Duration(seconds: seconds),
      connectionId: json['connection_id'] as String? ?? '',
      error: json['error'] as String? ?? '',
    );
  }
}

class HardcoverApplyResult {
  final String instanceId;
  final bool applied;
  final String error;
  const HardcoverApplyResult(
      {required this.instanceId, required this.applied, this.error = ''});
  factory HardcoverApplyResult.fromJson(Map<String, dynamic> json) =>
      HardcoverApplyResult(
        instanceId: json['instance_id'] as String? ?? '',
        applied: json['applied'] == true,
        error: json['error'] as String? ?? '',
      );
}
