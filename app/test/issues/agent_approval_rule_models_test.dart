import 'package:cantinarr/features/issues/data/agent_approval_rule_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('rule JSON round-trips including nullable fields', () {
    final rule = AgentApprovalRule.fromJson({
      'id': 4,
      'problem_kind': 'Waiting to import',
      'action_kind': 'manual_import',
      'action_facet': '',
      'label': 'Manual import · Waiting to import',
      'status': 'paused',
      'paused_reason': 'An auto-approved fix failed to execute.',
      'paused_at': '2026-07-29T10:00:00Z',
      'created_by': 99,
      'created_by_name': 'admin',
      'seed_action_id': 12,
      'approved_count': 14,
      'resolved_count': 13,
      'last_approved_at': '2026-07-29T09:00:00Z',
      'last_resolved_at': '2026-07-29T09:30:00Z',
      'created_at': '2026-07-01T08:00:00Z',
      'updated_at': '2026-07-29T10:00:00Z',
    });
    expect(rule.id, 4);
    expect(rule.label, 'Manual import · Waiting to import');
    expect(rule.isPaused, isTrue);
    expect(rule.pausedReason, contains('failed to execute'));
    expect(rule.createdByName, 'admin');
    expect(rule.approvedCount, 14);
    expect(rule.resolvedCount, 13);
    expect(rule.lastApprovedAt, isNotNull);
  });

  test('a minimal active rule parses with safe defaults', () {
    final rule = AgentApprovalRule.fromJson({
      'id': 1,
      'problem_kind': 'Waiting to import',
      'action_kind': 'manual_import',
      'action_facet': 'force',
      'label': 'Manual import (force) · Waiting to import',
      'status': 'active',
    });
    expect(rule.isPaused, isFalse);
    expect(rule.pausedReason, isNull);
    expect(rule.createdBy, isNull);
    expect(rule.approvedCount, 0);
    expect(rule.resolvedCount, 0);
  });
}
