import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/agent_approval_rule_models.dart';
import '../logic/issues_provider.dart';

/// Admin management surface for standing agent auto-approval rules: the
/// (problem, fix) pairs armed from the approve dialog's "Always approve"
/// checkbox. Each card shows the rule's fixed server-authored label, its
/// active/paused state (with the server's pause reason), and its track record,
/// with explicit Pause / Resume / Delete actions.
class AgentApprovalRulesScreen extends ConsumerStatefulWidget {
  const AgentApprovalRulesScreen({super.key});

  @override
  ConsumerState<AgentApprovalRulesScreen> createState() =>
      _AgentApprovalRulesScreenState();
}

class _AgentApprovalRulesScreenState
    extends ConsumerState<AgentApprovalRulesScreen> {
  List<AgentApprovalRule>? _rules;
  bool _isLoading = true;
  String? _error;
  int _loadEpoch = 0;

  /// Rule ids with an in-flight pause/resume/delete, so their buttons disable
  /// instead of double-submitting.
  final Set<int> _busyRules = {};

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    final epoch = ++_loadEpoch;
    setState(() {
      _isLoading = _rules == null;
      _error = null;
    });
    try {
      final rules = await ref.read(issuesServiceProvider).listApprovalRules();
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _rules = rules;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _setPaused(AgentApprovalRule rule, bool paused) async {
    if (_busyRules.contains(rule.id)) return;
    setState(() => _busyRules.add(rule.id));
    final service = ref.read(issuesServiceProvider);
    try {
      final updated = paused
          ? await service.pauseApprovalRule(rule.id)
          : await service.resumeApprovalRule(rule.id);
      if (!mounted) return;
      setState(() {
        _busyRules.remove(rule.id);
        _rules = [
          for (final r in _rules ?? <AgentApprovalRule>[])
            if (r.id == rule.id) updated else r,
        ];
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _busyRules.remove(rule.id));
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(_friendlyError(e))));
      _load();
    }
  }

  Future<void> _delete(AgentApprovalRule rule) async {
    if (_busyRules.contains(rule.id)) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text(
          'Delete this rule?',
          style: TextStyle(color: AppTheme.textPrimary),
        ),
        content: const Text(
          'Future matching fixes will wait for manual approval. Decided fixes '
          'keep their audit history.',
          style: TextStyle(color: AppTheme.textSecondary),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.error,
              foregroundColor: AppTheme.background,
            ),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    setState(() => _busyRules.add(rule.id));
    try {
      await ref.read(issuesServiceProvider).deleteApprovalRule(rule.id);
      if (!mounted) return;
      setState(() {
        _busyRules.remove(rule.id);
        _rules = [
          for (final r in _rules ?? <AgentApprovalRule>[])
            if (r.id != rule.id) r,
        ];
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _busyRules.remove(rule.id));
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(_friendlyError(e))));
      _load();
    }
  }

  @override
  Widget build(BuildContext context) {
    // Counters move when rules decide fixes, and a rule can pause itself; both
    // arrive over the socket, so the list stays live without polling.
    ref.listen(agentActionsChangedProvider, (_, __) => _load());
    ref.listen(autoApprovalPausedProvider, (_, __) => _load());
    return Scaffold(
      appBar: AppBar(title: const Text('Agent Auto-Approvals')),
      body: CenteredContent(
        child: _isLoading
            ? const Center(
                child: CircularProgressIndicator(color: AppTheme.accent))
            : _rules == null
                ? Center(
                    child: Padding(
                      padding: const EdgeInsets.all(24),
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(
                            _friendlyError(_error ?? 'Something went wrong'),
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center,
                          ),
                          const SizedBox(height: 12),
                          ElevatedButton(
                            onPressed: _load,
                            child: const Text('Retry'),
                          ),
                        ],
                      ),
                    ),
                  )
                : RefreshIndicator(
                    color: AppTheme.accent,
                    onRefresh: _load,
                    child: _buildList(_rules!),
                  ),
      ),
    );
  }

  Widget _buildList(List<AgentApprovalRule> rules) {
    if (rules.isEmpty) {
      return ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.all(24),
        children: const [
          SizedBox(height: 48),
          Icon(Icons.rule_outlined, size: 40, color: AppTheme.textSecondary),
          SizedBox(height: 12),
          Text(
            'No standing rules yet.',
            textAlign: TextAlign.center,
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
          SizedBox(height: 6),
          Text(
            'Approve an agent fix and check "Always approve this fix for this '
            'problem" to create one. Rules approve matching fixes without '
            'paging you and pause themselves if a fix fails.',
            textAlign: TextAlign.center,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ],
      );
    }
    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(16),
      itemCount: rules.length,
      separatorBuilder: (_, __) => const SizedBox(height: 10),
      itemBuilder: (context, index) {
        final rule = rules[index];
        return _RuleCard(
          rule: rule,
          busy: _busyRules.contains(rule.id),
          onPause: () => _setPaused(rule, true),
          onResume: () => _setPaused(rule, false),
          onDelete: () => _delete(rule),
        );
      },
    );
  }
}

class _RuleCard extends StatelessWidget {
  final AgentApprovalRule rule;
  final bool busy;
  final VoidCallback onPause;
  final VoidCallback onResume;
  final VoidCallback onDelete;

  const _RuleCard({
    required this.rule,
    required this.busy,
    required this.onPause,
    required this.onResume,
    required this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    final statusColor =
        rule.isPaused ? AppTheme.requested : AppTheme.available;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: Text(
                  rule.label.isEmpty ? 'Auto-approval rule' : rule.label,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                decoration: BoxDecoration(
                  color: statusColor.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  rule.isPaused ? 'Paused' : 'Active',
                  style: TextStyle(
                    color: statusColor,
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          if (rule.isPaused && (rule.pausedReason?.isNotEmpty ?? false)) ...[
            const SizedBox(height: 6),
            Text(
              rule.pausedReason!,
              style: const TextStyle(
                color: AppTheme.requested,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
          ],
          const SizedBox(height: 8),
          Text(
            _trackRecord(rule),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
            ),
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              if (rule.isPaused)
                OutlinedButton.icon(
                  onPressed: busy ? null : onResume,
                  icon: const Icon(Icons.play_arrow, size: 16),
                  label: const Text('Resume'),
                )
              else
                OutlinedButton.icon(
                  onPressed: busy ? null : onPause,
                  icon: const Icon(Icons.pause, size: 16),
                  label: const Text('Pause'),
                ),
              const Spacer(),
              TextButton.icon(
                onPressed: busy ? null : onDelete,
                style: TextButton.styleFrom(foregroundColor: AppTheme.error),
                icon: const Icon(Icons.delete_outline, size: 16),
                label: const Text('Delete'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  static String _trackRecord(AgentApprovalRule rule) {
    final parts = <String>[
      'Approved ${rule.approvedCount}',
      'Resolved ${rule.resolvedCount}',
    ];
    final last = rule.lastApprovedAt;
    if (last != null) {
      parts.add('last used ${_relative(last)}');
    }
    final creator = rule.createdByName;
    if (creator != null && creator.isNotEmpty) {
      parts.add('by $creator');
    }
    return parts.join(' · ');
  }

  static String _relative(DateTime t) {
    final d = DateTime.now().difference(t);
    if (d.inSeconds < 45) return 'just now';
    if (d.inMinutes < 60) return '${d.inMinutes}m ago';
    if (d.inHours < 24) return '${d.inHours}h ago';
    return '${d.inDays}d ago';
  }
}
