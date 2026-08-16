/// A quality-profile change parked by an external MCP agent, awaiting an
/// admin's in-app decision. The server only ever sends the rendered diff and
/// lifecycle metadata — plans and hashes stay server-side, and approval
/// re-validates live settings there before anything is written.
class ProfileChangeProposal {
  final int id;
  final String status;
  final String service;
  final String instanceId;
  final String instanceName;
  final int profileId;
  final String profileName;
  final String proposedByName;
  final String sourceClient;
  final List<String> diff;
  final DateTime? createdAt;
  final DateTime? expiresAt;
  final String decidedByName;
  final String rejectNote;
  final String resultText;

  /// Detail-only live check for a pending proposal: 'applicable', 'stale',
  /// or 'unavailable'. Empty when the server didn't compute it.
  final String currentStatus;

  const ProfileChangeProposal({
    required this.id,
    required this.status,
    required this.service,
    required this.instanceId,
    required this.instanceName,
    required this.profileId,
    required this.profileName,
    required this.proposedByName,
    this.sourceClient = '',
    this.diff = const [],
    this.createdAt,
    this.expiresAt,
    this.decidedByName = '',
    this.rejectNote = '',
    this.resultText = '',
    this.currentStatus = '',
  });

  bool get isPending => status == 'pending';

  factory ProfileChangeProposal.fromJson(Map<String, dynamic> json) =>
      ProfileChangeProposal(
        id: (json['id'] as num?)?.toInt() ?? 0,
        status: json['status'] as String? ?? '',
        service: json['service'] as String? ?? '',
        instanceId: json['instance_id'] as String? ?? '',
        instanceName: json['instance_name'] as String? ?? '',
        profileId: (json['profile_id'] as num?)?.toInt() ?? 0,
        profileName: json['profile_name'] as String? ?? '',
        proposedByName: json['proposed_by_name'] as String? ?? '',
        sourceClient: json['source_client'] as String? ?? '',
        diff: ((json['diff'] as List?) ?? const [])
            .map((line) => line.toString())
            .toList(growable: false),
        createdAt: DateTime.tryParse(json['created_at'] as String? ?? ''),
        expiresAt: DateTime.tryParse(json['expires_at'] as String? ?? ''),
        decidedByName: json['decided_by_name'] as String? ?? '',
        rejectNote: json['reject_note'] as String? ?? '',
        resultText: json['result_text'] as String? ?? '',
        currentStatus: json['current_status'] as String? ?? '',
      );

  /// Human label for a decided proposal's lifecycle state.
  String get statusLabel {
    switch (status) {
      case 'pending':
        return 'Awaiting approval';
      case 'executing':
        return 'Applying…';
      case 'applied':
        return 'Applied';
      case 'rejected':
        return 'Rejected';
      case 'superseded':
        return 'Replaced by a newer proposal';
      case 'expired':
        return 'Expired';
      case 'failed':
        return 'Not applied';
      default:
        return status;
    }
  }
}
