import 'package:cantinarr/l10n/l10n.dart';
import 'package:go_router/go_router.dart';
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
String _dayLabel(BuildContext context, DateTime when) {
  final local = when.toLocal();
  return MaterialLocalizations.of(context).formatMediumDate(local);
}

class AgentApprovalRulesScreen extends ConsumerStatefulWidget {
  const AgentApprovalRulesScreen({super.key});

  @override
  ConsumerState<AgentApprovalRulesScreen> createState() =>
      _AgentApprovalRulesScreenState();
}

class _AgentApprovalRulesScreenState
    extends ConsumerState<AgentApprovalRulesScreen> {
  List<AgentApprovalRule>? _rules;
  List<Map<String, dynamic>> _candidates = const [];
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
    return m != null ? m.group(1)! : context.l10n.opsSomethingWentWrongMessage;
  }

  Future<void> _armCandidate(Map<String, dynamic> candidate) async {
    try {
      await ref.read(issuesServiceProvider).armRule(
            candidate['problem_kind'] as String? ?? '',
            candidate['action_kind'] as String? ?? '',
            candidate['action_facet'] as String? ?? '',
          );
    } catch (_) {
      // Grounding is re-checked server-side; a refusal just leaves the list.
    }
    if (mounted) _load();
  }

  Future<void> _load() async {
    final epoch = ++_loadEpoch;
    setState(() {
      _isLoading = _rules == null;
      _error = null;
    });
    try {
      final service = ref.read(issuesServiceProvider);
      final rules = await service.listApprovalRules();
      List<Map<String, dynamic>> candidates = const [];
      try {
        candidates = await service.listRuleCandidates();
      } catch (_) {
        // The catalog is a convenience; the rules list works without it.
      }
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _rules = rules;
        _candidates = candidates;
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
        title: Text(
          context.l10n.opsDeleteThisRule,
          style: TextStyle(color: AppTheme.textPrimary),
        ),
        content: Text(
          context.l10n.opsFutureMatchingFixesWillWaitForManual,
          style: TextStyle(color: AppTheme.textSecondary),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(context.l10n.opsCancel),
          ),
          ElevatedButton(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.error,
              foregroundColor: AppTheme.background,
            ),
            child: Text(context.l10n.opsDelete),
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
      appBar: AppBar(title: Text(context.l10n.opsAgentAutoApprovals)),
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
                            _friendlyError(_error ??
                                context.l10n.opsSomethingWentWrongMessage),
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center,
                          ),
                          const SizedBox(height: 12),
                          ElevatedButton(
                            onPressed: _load,
                            child: Text(context.l10n.opsRetry),
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
        children: [
          SizedBox(height: 48),
          Icon(Icons.rule_outlined, size: 40, color: AppTheme.textSecondary),
          SizedBox(height: 12),
          Text(
            context.l10n.opsNoStandingRulesYet,
            textAlign: TextAlign.center,
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
          SizedBox(height: 6),
          Text(
            context.l10n.opsApproveAnAgentFixAndCheckAlways,
            textAlign: TextAlign.center,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ],
      );
    }
    final extra = _candidates.isEmpty ? 0 : 1;
    return ListView.separated(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.all(16),
      itemCount: rules.length + extra,
      separatorBuilder: (_, __) => const SizedBox(height: 10),
      itemBuilder: (context, index) {
        if (extra == 1 && index == 0) {
          return _ReadyToAutomateCard(
            candidates: _candidates,
            onArm: _armCandidate,
          );
        }
        final rule = rules[index - extra];
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
    final statusColor = rule.isPaused ? AppTheme.requested : AppTheme.available;
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
                  rule.label.isEmpty
                      ? context.l10n.opsAutoApprovalRule
                      : rule.label,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 14,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
              const SizedBox(width: 8),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
                decoration: BoxDecoration(
                  color: statusColor.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  rule.isPaused
                      ? context.l10n.opsPaused
                      : context.l10n.opsActive,
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
              rule.pausedAt != null
                  ? context.l10n.opsRulePausedSince(
                      rule.pausedReason!, _dayLabel(context, rule.pausedAt!))
                  : rule.pausedReason!,
              style: const TextStyle(
                color: AppTheme.requested,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
            if (rule.pausedByIssueId != null)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: InkWell(
                  onTap: () => context.push('/issues/${rule.pausedByIssueId}'),
                  child: Text(
                    context.l10n.opsSeeTheIssueThatPausedIt,
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                      decoration: TextDecoration.underline,
                    ),
                  ),
                ),
              ),
            if (rule.approvedSincePause > 0)
              Padding(
                padding: const EdgeInsets.only(top: 4),
                child: Text(
                  context.l10n.opsApprovedManuallySincePause(
                      rule.approvedSincePause.toString()),
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
          ],
          const SizedBox(height: 8),
          Text(
            _trackRecord(context, rule),
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
                  label: Text(context.l10n.opsResume),
                )
              else
                OutlinedButton.icon(
                  onPressed: busy ? null : onPause,
                  icon: const Icon(Icons.pause, size: 16),
                  label: Text(context.l10n.opsPause),
                ),
              const Spacer(),
              TextButton.icon(
                onPressed: busy ? null : onDelete,
                style: TextButton.styleFrom(foregroundColor: AppTheme.error),
                icon: const Icon(Icons.delete_outline, size: 16),
                label: Text(context.l10n.opsDelete),
              ),
            ],
          ),
        ],
      ),
    );
  }

  static String _trackRecord(BuildContext context, AgentApprovalRule rule) {
    final parts = <String>[
      context.l10n.opsApprovedCount(rule.approvedCount.toString()),
      context.l10n.opsResolvedCount(rule.resolvedCount.toString()),
    ];
    final last = rule.lastApprovedAt;
    if (last != null) {
      parts.add(context.l10n.opsLastUsed(_relative(context, last)));
    }
    final creator = rule.createdByName;
    if (creator != null && creator.isNotEmpty) {
      parts.add(context.l10n.opsByName(creator));
    }
    return parts.join(' · ');
  }

  static String _relative(BuildContext context, DateTime t) {
    final d = DateTime.now().difference(t);
    if (d.inSeconds < 45) return context.l10n.opsJustNow;
    if (d.inMinutes < 60)
      return context.l10n.opsMinutesAgo(d.inMinutes.toString());
    if (d.inHours < 24) return context.l10n.opsHoursAgo(d.inHours.toString());
    return context.l10n.opsDaysAgo(d.inDays.toString());
  }
}

/// Triples the admin has already approved by hand and could put on autopilot —
/// "remember, later", grounded server-side.
class _ReadyToAutomateCard extends StatelessWidget {
  final List<Map<String, dynamic>> candidates;
  final Future<void> Function(Map<String, dynamic>) onArm;

  const _ReadyToAutomateCard({required this.candidates, required this.onArm});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n.opsReadyToAutomate,
            style: TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 14,
                fontWeight: FontWeight.w700),
          ),
          const SizedBox(height: 4),
          Text(
            context.l10n.opsFixesYouVeAlreadyApprovedByHand,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
          ),
          const SizedBox(height: 8),
          for (final c in candidates)
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      context.l10n.opsArmCandidate(
                          (c['label'] ?? '').toString(),
                          (c['approved_count'] ?? 0).toString()),
                      style: const TextStyle(
                          color: AppTheme.textPrimary, fontSize: 13),
                    ),
                  ),
                  TextButton(
                    onPressed: () => onArm(c),
                    child: Text(context.l10n.opsArm),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}
