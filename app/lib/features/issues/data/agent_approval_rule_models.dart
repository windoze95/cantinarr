// Data model for standing agent auto-approval rules
// (`GET /api/admin/agent-approval-rules`). Every display field is
// SERVER-AUTHORED fixed copy (doctor problem labels + action vocabulary);
// nothing here is agent- or user-supplied, and parsing is lenient so an older
// or newer server shape never crashes the rules screen.

/// One standing rule: an admin-armed (problem, fix, facet) triple the server
/// may approve without paging, plus its self-pause state and track record.
class AgentApprovalRule {
  final int id;
  final String problemKind;
  final String actionKind;
  final String actionFacet;

  /// Fixed display label, e.g. "Manual import · Waiting to import".
  final String label;

  /// 'active' | 'paused' (tolerant of future values via [isPaused]).
  final String status;

  /// Server-authored reason when the rule paused itself (or an admin paused
  /// it). Null while active.
  final String? pausedReason;
  final DateTime? pausedAt;

  final int? createdBy;
  final String? createdByName;
  final int? seedActionId;

  /// Fixes this rule has auto-approved.
  final int approvedCount;

  /// Issues it acted on that closed out resolved.
  final int resolvedCount;

  final DateTime? lastApprovedAt;
  final DateTime? lastResolvedAt;
  final DateTime? createdAt;
  final DateTime? updatedAt;

  const AgentApprovalRule({
    required this.id,
    required this.problemKind,
    required this.actionKind,
    required this.actionFacet,
    required this.label,
    required this.status,
    required this.pausedReason,
    required this.pausedAt,
    required this.createdBy,
    required this.createdByName,
    required this.seedActionId,
    required this.approvedCount,
    required this.resolvedCount,
    required this.lastApprovedAt,
    required this.lastResolvedAt,
    required this.createdAt,
    required this.updatedAt,
  });

  bool get isPaused => status == 'paused';

  factory AgentApprovalRule.fromJson(Map<String, dynamic> json) {
    DateTime? time(String key) =>
        DateTime.tryParse(json[key] as String? ?? '')?.toLocal();
    return AgentApprovalRule(
      id: (json['id'] as num?)?.toInt() ?? 0,
      problemKind: json['problem_kind'] as String? ?? '',
      actionKind: json['action_kind'] as String? ?? '',
      actionFacet: json['action_facet'] as String? ?? '',
      label: json['label'] as String? ?? '',
      status: json['status'] as String? ?? '',
      pausedReason: json['paused_reason'] as String?,
      pausedAt: time('paused_at'),
      createdBy: (json['created_by'] as num?)?.toInt(),
      createdByName: json['created_by_name'] as String?,
      seedActionId: (json['seed_action_id'] as num?)?.toInt(),
      approvedCount: (json['approved_count'] as num?)?.toInt() ?? 0,
      resolvedCount: (json['resolved_count'] as num?)?.toInt() ?? 0,
      lastApprovedAt: time('last_approved_at'),
      lastResolvedAt: time('last_resolved_at'),
      createdAt: time('created_at'),
      updatedAt: time('updated_at'),
    );
  }
}
