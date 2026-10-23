import 'package:cantinarr/l10n/operations_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/agent_action_models.dart';
import '../logic/issues_provider.dart';

/// SAFETY-CRITICAL widget: the card an admin uses to approve or deny one of the
/// agent's proposed *arr fixes.
///
/// Invariants this widget upholds:
///  * Everything the agent produced — `params`, `rationale`, the kind summary —
///    is rendered as PASSIVE, non-editable text. No agent-supplied string ever
///    becomes a button label or a control. The only controls are the two
///    fixed, server-authored Approve / Deny buttons.
///  * Approve / Deny appear only for an admin AND only while the action is
///    still `proposed`. A reporter (or any non-admin) sees a read-only "waiting
///    on an admin" footer.
///  * Once a decision is made — locally here, or already decided server-side —
///    the card FREEZES ("Approved · just now" / "Denied") and never re-enables.
///    The server's compare-and-swap rejects a second decision anyway; this is
///    the matching guard at the UI layer.
class ProposedActionCard extends ConsumerStatefulWidget {
  final AgentAction action;

  /// Invoked after a successful approve/deny with the server's updated action,
  /// so the parent can refresh its queue + badge.
  final ValueChanged<AgentAction>? onDecided;

  /// Optional affordance to open the agent's activity timeline for this
  /// action's run. Shown only when the action carries a run id.
  final VoidCallback? onViewActivity;

  /// False when the parent is showing a retained snapshot after a refresh
  /// failure. History remains readable, but an unverified proposal must not
  /// offer decision controls until the authoritative list loads again.
  final bool decisionsEnabled;

  const ProposedActionCard({
    super.key,
    required this.action,
    this.onDecided,
    this.onViewActivity,
    this.decisionsEnabled = true,
  });

  @override
  ConsumerState<ProposedActionCard> createState() => _ProposedActionCardState();
}

class _ProposedActionCardState extends ConsumerState<ProposedActionCard> {
  /// For a decidable TV delete: which of the proposed episodes stay selected.
  /// Starts as the full proposal; unchecking builds an approval override so
  /// the admin can spare an episode without denying the whole repair — the
  /// only editing this card offers, because shrinking a destructive fix is
  /// the one edit with no wrong answer.
  Set<int>? _deleteEpisodeSelection;

  List<int> get _proposedDeleteEpisodes {
    final raw = _action.params.raw['episodes'];
    if (raw is List) {
      return raw.whereType<num>().map((e) => e.toInt()).toList()..sort();
    }
    return const [];
  }

  bool get _deleteSelectionActive =>
      _action.kind == AgentActionKind.deleteMediaFiles &&
      _action.canTakeAction &&
      _proposedDeleteEpisodes.length > 1;

  Object? _deleteOverride() {
    final selection = _deleteEpisodeSelection;
    final proposed = _proposedDeleteEpisodes;
    if (selection == null || proposed.isEmpty) return null;
    if (selection.length == proposed.length) return null; // as proposed
    final params = Map<String, dynamic>.from(_action.params.raw);
    params['episodes'] = (selection.toList()..sort());
    return params;
  }

  /// The authoritative action shown. Starts from the prop and is replaced by
  /// the server's response after a decision so the frozen footer is accurate.
  late AgentAction _action;
  bool _busy = false;
  bool _reviewing = false;
  bool _decisionUncertain = false;

  @override
  void initState() {
    super.initState();
    _action = widget.action;
  }

  @override
  void didUpdateWidget(ProposedActionCard oldWidget) {
    super.didUpdateWidget(oldWidget);
    // A live refresh upstream (queue reload / WS ping) may hand us a newer
    // snapshot of the same action — adopt it unless we're mid-decision.
    if (!_busy &&
        (widget.action.id != _action.id ||
            !identical(widget.action, oldWidget.action))) {
      _action = widget.action;
      _decisionUncertain = false;
    }
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : context.l10n.opsSomethingWentWrongMessage;
  }

  Future<void> _approve() async {
    if (_busy ||
        _reviewing ||
        !_action.canTakeAction ||
        _decisionUncertain ||
        !widget.decisionsEnabled) {
      return;
    }
    // Every currently supported action mutates a connected service. Do not
    // trust the payload's display-only `risk` string to skip confirmation.
    setState(() => _reviewing = true);
    final confirmation = await _confirmApproval();
    if (!mounted) return;
    setState(() => _reviewing = false);
    if (confirmation == null) return;
    if (!_action.canTakeAction) {
      _showSnack(context.l10n.opsThisFixChangedWhileYouWereReviewing);
      return;
    }
    setState(() => _busy = true);
    final service = ref.read(issuesServiceProvider);
    try {
      final updated = await service.approveAction(
        _action.id,
        override: _deleteOverride(),
        remember: confirmation.remember,
      );
      if (!mounted) return;
      _adoptDecision(updated);
      _showApprovalOutcome(updated);
    } catch (e) {
      // The POST may have reached the server even if its response was lost.
      // Re-read before offering another decision so a destructive mutation can
      // never be double-approved because of a network timeout.
      try {
        final current = await service.getAction(_action.id);
        if (!mounted) return;
        if (!current.canTakeAction) {
          _adoptDecision(current);
          _showApprovalOutcome(current);
          return;
        }
        setState(() => _busy = false);
        _showSnack(_friendlyError(e));
      } catch (_) {
        if (!mounted) return;
        setState(() {
          _busy = false;
          _decisionUncertain = true;
        });
        _showSnack(
          context.l10n.opsCouldNotConfirmWhetherTheFixRan,
        );
      }
    }
  }

  /// Confirmation dialog. Returns null on cancel. When the server sent an
  /// [AutoApprovalOffer], the dialog additionally offers a "keep approving
  /// this" checkbox — the checkbox copy comes only from the TYPED offer the
  /// server computed, never from any agent-authored string, preserving this
  /// widget's no-agent-text-near-controls invariant.
  Future<_ApprovalConfirmation?> _confirmApproval() {
    final offer = _action.canTakeAction ? _action.autoApprovalOffer : null;
    var remember = false;
    return showDialog<_ApprovalConfirmation>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (dialogContext, setDialogState) => AlertDialog(
          backgroundColor: AppTheme.surface,
          // A destructive fix explains itself at length. Scrolling keeps the
          // whole warning reachable on a small screen or at a large text scale,
          // instead of clipping the part that says it cannot be undone.
          scrollable: true,
          title: Text(
            context.l10n.opsApproveThisChange,
            style: TextStyle(color: AppTheme.textPrimary),
          ),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                _ActionCopy.confirmation(context, _action),
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
              const SizedBox(height: 16),
              _ActionTarget(action: _action),
              if (offer != null) ...[
                const SizedBox(height: 8),
                CheckboxListTile(
                  value: remember,
                  onChanged: (v) => setDialogState(() => remember = v ?? false),
                  contentPadding: EdgeInsets.zero,
                  controlAffinity: ListTileControlAffinity.leading,
                  activeColor: AppTheme.accent,
                  title: Text(
                    context.l10n.opsAlwaysApproveThisFixForThisProblem,
                    style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  subtitle: Text(
                    context.l10n.opsRuleOffer(
                        offer.label,
                        offer.reactivatesPausedRule
                            ? context.l10n.opsReactivatesRule
                            : ''),
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                    ),
                  ),
                ),
              ],
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(),
              child: Text(context.l10n.opsCancel),
            ),
            ElevatedButton(
              onPressed: () => Navigator.of(dialogContext)
                  .pop(_ApprovalConfirmation(remember: remember)),
              style: ElevatedButton.styleFrom(
                backgroundColor: AppTheme.available,
                foregroundColor: AppTheme.background,
              ),
              child: Text(context.l10n.opsApproveAndApply),
            ),
          ],
        ),
      ),
    );
  }

  void _adoptDecision(AgentAction updated) {
    setState(() {
      _action = updated;
      _busy = false;
      _decisionUncertain = false;
    });
    widget.onDecided?.call(updated);
  }

  void _showApprovalOutcome(AgentAction action) {
    final message = switch (action.status) {
      AgentActionStatus.executed => context.l10n.opsFixApplied,
      AgentActionStatus.failed => context.l10n.opsTheFixWasApprovedButItFailed,
      AgentActionStatus.superseded => context.l10n.opsTheFixDidNotRunBecauseThe,
      AgentActionStatus.outcomeUnknown =>
        context.l10n.opsApprovalWasRecordedButTheResultCould,
      AgentActionStatus.approved ||
      AgentActionStatus.executing =>
        context.l10n.opsFixApprovedAndApplying,
      AgentActionStatus.denied => context.l10n.opsThisFixWasAlreadyDenied,
      AgentActionStatus.proposed ||
      AgentActionStatus.unknown =>
        context.l10n.opsTheFixWasNotApproved,
    };
    _showSnack(message);
  }

  void _showSnack(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  Future<void> _deny() async {
    if (_busy ||
        _reviewing ||
        !_action.canTakeAction ||
        _decisionUncertain ||
        !widget.decisionsEnabled) {
      return;
    }
    setState(() => _reviewing = true);
    final controller = TextEditingController();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: Text(context.l10n.opsDenyThisFix,
            style: TextStyle(color: AppTheme.textPrimary)),
        content: TextField(
          controller: controller,
          autofocus: true,
          maxLines: 3,
          minLines: 1,
          style: const TextStyle(color: AppTheme.textPrimary),
          decoration: InputDecoration(
            labelText: context.l10n.opsReasonOptional,
            labelStyle: TextStyle(color: AppTheme.textSecondary),
            border: OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(context.l10n.opsCancel),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.error,
              foregroundColor: AppTheme.background,
            ),
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: Text(context.l10n.opsDeny),
          ),
        ],
      ),
    );

    final note = controller.text.trim();
    // Defer disposal until after the dialog's exit transition completes — the
    // TextField still references the controller while the route animates out,
    // so disposing synchronously here would use it after disposal.
    WidgetsBinding.instance.addPostFrameCallback((_) => controller.dispose());
    if (!mounted) return;
    setState(() => _reviewing = false);
    if (confirmed != true) return;
    if (!_action.canTakeAction) {
      _showSnack(context.l10n.opsThisFixChangedWhileYouWereReviewing);
      return;
    }

    setState(() => _busy = true);
    final service = ref.read(issuesServiceProvider);
    try {
      final updated = await service.denyAction(
        _action.id,
        note: note.isEmpty ? null : note,
      );
      if (!mounted) return;
      _adoptDecision(updated);
      _showDenyOutcome(updated);
    } catch (e) {
      // A concurrent approval may have won, or the denial response may have
      // been lost after the server recorded it. Reconcile the durable action
      // before displaying any outcome; never claim "denied" from our intent.
      try {
        final current = await service.getAction(_action.id);
        if (!mounted) return;
        if (!current.canTakeAction) {
          _adoptDecision(current);
          _showDenyOutcome(current);
          return;
        }
      } catch (_) {
        // Keep the original error below. Denials are idempotent, and without
        // an authoritative read the card must not invent a decision outcome.
      }
      if (!mounted) return;
      setState(() => _busy = false);
      _showSnack(_friendlyError(e));
    }
  }

  void _showDenyOutcome(AgentAction action) {
    if (action.status == AgentActionStatus.denied) {
      _showSnack(context.l10n.opsFixDeniedTheAgentCanInvestigateAnother);
      return;
    }
    _showApprovalOutcome(action);
  }

  @override
  Widget build(BuildContext context) {
    final isAdmin = ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false;
    final action = _action;
    final actionable =
        action.canTakeAction && !_decisionUncertain && widget.decisionsEnabled;

    return Container(
      margin: const EdgeInsets.symmetric(vertical: 8),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: actionable
              ? AppTheme.requested.withValues(alpha: 0.6)
              : AppTheme.border,
          width: actionable ? 1.2 : 0.8,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header: a fixed icon + a fixed "proposed a fix" line (NOT agent text).
          Row(
            children: [
              Icon(Icons.build_circle_outlined,
                  size: 18, color: AppTheme.requested),
              SizedBox(width: 8),
              Text(
                context.l10n.opsTheAgentProposedAFix,
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),

          // The immutable issue target is kept visually separate from the
          // agent-authored rationale/params. An admin sees exactly which arr
          // installation will receive the mutation before reviewing it.
          _ActionTarget(action: action),
          const SizedBox(height: 12),

          // What the fix is ABOUT, in the reader's own terms: exact scope and
          // how many times this problem has come back.
          if (action.issueSeason > 0 || action.issueOccurrences > 1) ...[
            _MediaScopeLine(action: action),
            const SizedBox(height: 12),
          ],

          // The server's own remediation memory, shown BEFORE the proposal:
          // the approver must never decide with less evidence than the model.
          if (action.priorAttempts.isNotEmpty) ...[
            _PriorAttemptsBlock(attempts: action.priorAttempts),
            const SizedBox(height: 12),
          ],

          // Plain-language summary of the action kind (server-authored copy,
          // chosen by the typed kind enum — never an agent string).
          Text(
            _ActionCopy.summary(context, action),
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 15,
              height: 1.35,
              fontWeight: FontWeight.w600,
            ),
          ),

          // The quoted, non-editable params (release / quality / queue id …).
          ..._buildParamRows(action),

          // The one edit with no wrong answer: shrink a destructive delete.
          if (_deleteSelectionActive) ...[
            const SizedBox(height: 10),
            Text(
              context.l10n.opsEpisodesToDeleteUncheckToSpare,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 6),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final ep in _proposedDeleteEpisodes)
                  FilterChip(
                    label: Text('E$ep'),
                    selected: (_deleteEpisodeSelection ??
                            _proposedDeleteEpisodes.toSet())
                        .contains(ep),
                    onSelected: (selected) {
                      setState(() {
                        final current = _deleteEpisodeSelection ??
                            _proposedDeleteEpisodes.toSet();
                        final next = Set<int>.from(current);
                        if (selected) {
                          next.add(ep);
                        } else if (next.length > 1) {
                          // Never allow an empty delete: sparing everything
                          // is what Deny is for.
                          next.remove(ep);
                        }
                        _deleteEpisodeSelection = next;
                      });
                    },
                  ),
              ],
            ),
          ],

          if (action.approvedParams != null &&
              !mapEquals(
                action.approvedParams!.raw,
                action.params.raw,
              )) ...[
            const SizedBox(height: 12),
            Text(
              context.l10n.opsValuesUsedAfterApproval,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
            ..._buildParamRows(
              action,
              params: action.approvedParams,
              includeLeadingGap: false,
            ),
          ],

          // The agent's rationale, quoted as untrusted passive text.
          if (action.rationale.trim().isNotEmpty) ...[
            const SizedBox(height: 12),
            _QuotedBlock(
              label: context.l10n.opsAgentSReasoning,
              text: action.rationale.trim(),
            ),
          ],

          // The execution result / deny note, once decided.
          if (action.resultText != null &&
              action.resultText!.trim().isNotEmpty) ...[
            const SizedBox(height: 12),
            _QuotedBlock(
                label: context.l10n.opsResult, text: action.resultText!.trim()),
          ],
          if (action.denyReason != null &&
              action.denyReason!.trim().isNotEmpty) ...[
            const SizedBox(height: 12),
            _QuotedBlock(
                label: context.l10n.opsDenyNote,
                text: action.denyReason!.trim()),
          ],

          const SizedBox(height: 14),

          // Controls (admin + still proposed) OR a frozen status footer.
          if (actionable && isAdmin)
            _buildButtons()
          else
            _buildFrozenFooter(action, isAdmin: isAdmin),

          if (widget.onViewActivity != null && action.runId != null) ...[
            const SizedBox(height: 6),
            Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                onPressed: widget.onViewActivity,
                icon: const Icon(Icons.timeline, size: 16),
                style: TextButton.styleFrom(
                  foregroundColor: AppTheme.textSecondary,
                  padding: EdgeInsets.zero,
                  minimumSize: const Size(0, 32),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                ),
                label: Text(context.l10n.opsViewAgentActivity),
              ),
            ),
          ],
        ],
      ),
    );
  }

  List<Widget> _buildParamRows(
    AgentAction action, {
    AgentActionParams? params,
    bool includeLeadingGap = true,
  }) {
    final rows = _ActionCopy.paramRows(context, action, params: params);
    if (rows.isEmpty) return const [];
    return [
      if (includeLeadingGap) const SizedBox(height: 10),
      ...rows.map((r) => Padding(
            padding: const EdgeInsets.only(top: 6),
            child: _ParamRow(label: r.$1, value: r.$2, mono: r.$3),
          )),
    ];
  }

  Widget _buildButtons() {
    return Row(
      children: [
        Expanded(
          child: ElevatedButton.icon(
            onPressed: _busy || _reviewing ? null : _approve,
            icon: _busy
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: AppTheme.background),
                  )
                : const Icon(Icons.check_circle_outline, size: 18),
            label: Text(context.l10n.opsApprove),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.available,
              foregroundColor: AppTheme.background,
              disabledBackgroundColor:
                  AppTheme.available.withValues(alpha: 0.5),
            ),
          ),
        ),
        const SizedBox(width: 10),
        Expanded(
          child: OutlinedButton.icon(
            onPressed: _busy || _reviewing ? null : _deny,
            icon: const Icon(Icons.cancel_outlined, size: 18),
            label: Text(context.l10n.opsDeny),
            style: OutlinedButton.styleFrom(
              foregroundColor: AppTheme.error,
              side: const BorderSide(color: AppTheme.error),
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildFrozenFooter(AgentAction action, {required bool isAdmin}) {
    final blocked = _decisionUncertain
        ? context.l10n.opsCouldNotConfirmWhetherTheFixRan
        : !widget.decisionsEnabled && action.status.isPending
            ? context.l10n.opsThisFixCouldNotBeRefreshedRetry
            : action.decisionBlockedReason;
    if (action.status.isPending && blocked != null) {
      return Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.lock_outline,
              size: 16, color: AppTheme.textSecondary),
          const SizedBox(width: 6),
          Expanded(
            child: Text(
              localizeOperationsLabel(context, blocked),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        ],
      );
    }

    // A reporter / non-admin viewing a still-pending proposal.
    if (action.status.isPending && !isAdmin) {
      return Row(
        children: [
          Icon(Icons.hourglass_empty, size: 16, color: AppTheme.requested),
          SizedBox(width: 6),
          Expanded(
            child: Text(
              context.l10n.opsWaitingOnAnAdminToApproveA,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ],
      );
    }

    final decided = _ActionCopy.frozenFooter(context, action);
    return Row(
      children: [
        Icon(decided.$2, size: 16, color: decided.$3),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            decided.$1,
            style: TextStyle(
              color: decided.$3,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    );
  }
}

/// The approve dialog's result: null (dialog dismissed) means cancel; a value
/// means "approve", carrying whether the admin also checked the standing-rule
/// opt-in.
class _ApprovalConfirmation {
  final bool remember;

  const _ApprovalConfirmation({required this.remember});
}

/// Exact media scope + recurrence count for the approval card.
class _MediaScopeLine extends StatelessWidget {
  final AgentAction action;
  const _MediaScopeLine({required this.action});

  @override
  Widget build(BuildContext context) {
    final parts = <String>[];
    if (action.issueSeason > 0 && action.issueEpisode > 0) {
      parts.add(
          'S${action.issueSeason.toString().padLeft(2, '0')}E${action.issueEpisode.toString().padLeft(2, '0')}');
    } else if (action.issueSeason > 0) {
      parts.add(context.l10n.opsSeasonNumber(action.issueSeason.toString()));
    }
    if (action.issueOccurrences > 1) {
      parts.add(context.l10n.opsSeenTimes(action.issueOccurrences.toString()));
    }
    return Row(
      children: [
        const Icon(Icons.movie_outlined,
            size: 15, color: AppTheme.textSecondary),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            parts.join(' · '),
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
        ),
      ],
    );
  }
}

/// The PRIOR ATTEMPTS block: fixes that already ran on this issue, with the
/// recurrence verdict called out. A fix that came back is the strongest reason
/// an approver has to say no to a repeat — it was previously visible only to
/// the model.
class _PriorAttemptsBlock extends StatelessWidget {
  final List<PriorAttempt> attempts;
  const _PriorAttemptsBlock({required this.attempts});

  String _label(PriorAttempt a) {
    final facet = a.facet.isNotEmpty ? ' (${a.facet})' : '';
    return '${a.kind}$facet';
  }

  @override
  Widget build(BuildContext context) {
    final anyRecurred = attempts.any((a) => a.recurred);
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(10),
        border:
            Border.all(color: anyRecurred ? AppTheme.error : AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            anyRecurred
                ? context.l10n.opsAlreadyTriedAndItCameBack
                : context.l10n.opsAlreadyTriedOnThisProblem,
            style: TextStyle(
              color: anyRecurred ? AppTheme.error : AppTheme.textSecondary,
              fontSize: 12,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 6),
          for (final a in attempts)
            Padding(
              padding: const EdgeInsets.only(bottom: 2),
              child: Text(
                a.recurred
                    ? context.l10n.opsPriorAttemptReturned(_label(a))
                    : context.l10n.opsPriorAttemptRan(_label(a)),
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontSize: 13, height: 1.35),
              ),
            ),
        ],
      ),
    );
  }
}

class _ActionTarget extends StatelessWidget {
  final AgentAction action;

  const _ActionTarget({required this.action});

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(10),
      decoration: BoxDecoration(
        color: AppTheme.background.withValues(alpha: 0.55),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: AppTheme.border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.dns_outlined, size: 18, color: AppTheme.requested),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  context.l10n.opsTargetInstance,
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  '${action.instanceServiceLabel} · ${action.instanceDisplayName}',
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 2),
                SelectableText(
                  action.instanceId.trim().isEmpty
                      ? context.l10n.opsInstanceIDUnavailable
                      : action.instanceId.trim(),
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 11,
                    fontFamily: 'monospace',
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// Server-authored, kind-driven copy for the card. Centralizes the plain
/// language so the action enum — never an agent string — selects the wording.
class _ActionCopy {
  /// A one-line plain-language summary of what approving will do.
  static String summary(BuildContext context, AgentAction a) {
    final params = a.approvedParams ?? a.params;
    switch (a.kind) {
      case AgentActionKind.grabRelease:
        return params.queueIdToReplace != null
            ? context.l10n.opsGrabADifferentReleaseAndRemoveThe
            : context.l10n.opsGrabASpecificRelease;
      case AgentActionKind.remediateQueue:
        switch (params.queueAction) {
          case 'remove':
            return context.l10n.opsRemoveTheStuckItemFromTheDownload;
          case 'blocklist_search':
            return context.l10n.opsBlocklistTheCurrentReleaseAndLetThe;
          case 'blocklist_only':
            return context
                .l10n.opsRemoveAndBlocklistThisReleaseWithoutSearching;
          case 'change_category':
            return context.l10n.opsChangeTheDownloadCategoryToUnblockThe;
          default:
            return context.l10n.opsFixTheStuckDownloadQueueItem;
        }
      case AgentActionKind.manualImport:
        return params.force
            ? context.l10n.opsForceImportTheDownloadedFilesOverridesSafety
            : context.l10n.opsManuallyImportTheDownloadedFiles;
      case AgentActionKind.triggerSearch:
        return context.l10n.opsStartAnAutomaticSearchForThisTitle;
      case AgentActionKind.rescan:
        return context.l10n.opsRescanTheFilesOnDiskAndRe;
      case AgentActionKind.deleteMediaFiles:
        return _deleteSummary(context, params);
      case AgentActionKind.unknown:
        return context.l10n.opsApplyAFix;
    }
  }

  /// Summary for a deletion of files the media service already imported. This
  /// is the WHOLE repair in one line — delete, optionally block, then go and
  /// get what should have been there — because that is the single decision the
  /// admin is being asked for. The blocklist choice stays a distinct promise
  /// (files gone versus files gone *and* that release stood down), so it is
  /// never folded away.
  static String _deleteSummary(BuildContext context, AgentActionParams p) {
    // A book delete covers every file the record holds — plural, count unknown.
    final one = p.mediaType == 'movie' ||
        (p.mediaType == 'tv' && p.episodes.length == 1);
    if (!p.blocklist) {
      return one
          ? context.l10n.opsDeleteWrongFileSearch
          : context.l10n.opsDeleteWrongFilesSearch;
    }
    return one
        ? context.l10n.opsDeleteWrongFileBlockSearch
        : context.l10n.opsDeleteWrongFilesBlockSearch;
  }

  /// Fixed confirmation copy selected only from typed enums/validated flags.
  /// No rationale, identifier, or other agent-authored string enters a control.
  static String confirmation(BuildContext context, AgentAction a) {
    switch (a.kind) {
      case AgentActionKind.grabRelease:
        return a.params.queueIdToReplace != null
            ? context.l10n.opsCantinarrWillDownloadADifferentReleaseAnd
            : context.l10n.opsCantinarrWillDownloadAReleaseInYour;
      case AgentActionKind.remediateQueue:
        switch (a.params.queueAction) {
          case 'remove':
            return context.l10n.opsCantinarrWillRemoveAnItemFromThe;
          case 'blocklist_search':
            return context.l10n.opsCantinarrWillBlocklistTheCurrentReleaseAnd;
          case 'blocklist_only':
            return context.l10n.opsCantinarrWillBlocklistTheStuckReleaseAnd;
          case 'change_category':
            return context.l10n.opsCantinarrWillChangeADownloadCategoryIn;
          default:
            return context.l10n.opsCantinarrWillChangeTheDownloadQueueIn;
        }
      case AgentActionKind.manualImport:
        return a.params.force
            ? context.l10n.opsCantinarrWillForceImportDownloadedFilesAnd
            : context.l10n.opsCantinarrWillImportDownloadedFilesIntoYour;
      case AgentActionKind.triggerSearch:
        return context.l10n.opsCantinarrWillStartASearchAndMay;
      case AgentActionKind.rescan:
        return context.l10n.opsCantinarrWillRescanFilesAndRunThe;
      case AgentActionKind.deleteMediaFiles:
        return _deleteConfirmation(context, a.params);
      case AgentActionKind.unknown:
        return context.l10n.opsCantinarrWillMakeAChangeInYour;
    }
  }

  /// Confirmation for the one fix that destroys something. These files are
  /// already in the library — nothing is queued, nothing is recoverable from
  /// Cantinarr — so the copy names the exact target (the show's title is not in
  /// the params, the season and episode numbers are), says plainly that it
  /// cannot be undone, and states what the blocklist choice does and does not
  /// do. Every value interpolated here is a validated integer, never an
  /// agent-authored string.
  ///
  /// It also describes the REPLACEMENT, because that is part of this same fix
  /// rather than a follow-up an admin is asked to approve separately. Getting
  /// rid of a wrong copy and getting the right one is one decision; a card that
  /// stopped at "deleted" would be asking for authorisation to leave a hole.
  /// The only thing that changes the ending is blocking: standing a release
  /// down is what makes a media service go looking on its own, and when it
  /// does, Cantinarr stays out of the way instead of duplicating the search.
  static String _deleteConfirmation(BuildContext context, AgentActionParams p) {
    final episodes = p.episodes;
    final isTv = p.mediaType == 'tv';
    // A book delete covers every file the record holds — plural, count unknown.
    final one = p.mediaType == 'movie' || (isTv && episodes.length == 1);

    final String target;
    if (p.mediaType == 'book') {
      target = context.l10n.opsEveryFileThisBookHoldsInYour;
    } else if (p.mediaType == 'music') {
      target = context.l10n.opsEveryTrackFileThisAlbumHoldsIn;
    } else if (isTv && episodes.isNotEmpty) {
      final season = p.season;
      target = context.l10n.opsDeleteEpisodeTarget(
          episodes.length,
          season == null
              ? ''
              : context.l10n.opsDeleteSeasonFragment(season.toString()),
          _episodeRanges(episodes));
    } else if (isTv) {
      target = context.l10n.opsTheEpisodeFilesListedOnThisFix;
    } else {
      target = context.l10n.opsTheMovieFileInYourLibrary;
    }

    final buffer = StringBuffer(
      context.l10n.opsDeleteFilesConfirmation(target, one ? 'one' : 'other'),
    );
    if (p.blocklist) {
      buffer.write(
        one
            ? context.l10n.opsItAlsoBlocksTheReleaseThatFile
            : context.l10n.opsItAlsoBlocksTheReleasesThoseFiles,
      );
    } else {
      buffer.write(
        one
            ? context.l10n.opsTheReleaseThatFileCameFromIs
            : context.l10n.opsTheReleasesThoseFilesCameFromAre,
      );
    }

    buffer.write(
      isTv
          ? context.l10n.opsCantinarrThenLooksForReplacementsButOnly
          : context.l10n.opsCantinarrThenLooksForAReplacement,
    );
    if (p.blocklist) {
      buffer.write(
        one
            ? context.l10n.opsIfBlockingThatReleaseAlreadySentYour
            : context.l10n.opsIfBlockingThoseReleasesAlreadySentYour,
      );
    }
    return buffer.toString();
  }

  /// Compresses a validated episode list into an exact, compact form ("1–9",
  /// "1–3, 7, 10–12"). Exactness matters — this is the only place an admin sees
  /// WHICH episodes an irreversible deletion covers — and compactness keeps a
  /// whole season readable inside a confirmation dialog.
  static String _episodeRanges(List<int> episodes) {
    if (episodes.isEmpty) return '';
    final sorted = [...episodes]..sort();
    final parts = <String>[];
    var start = sorted.first;
    var previous = start;
    for (var i = 1; i <= sorted.length; i++) {
      final current = i < sorted.length ? sorted[i] : null;
      if (current != null && (current == previous || current == previous + 1)) {
        previous = current;
        continue;
      }
      parts.add(start == previous ? '$start' : '$start–$previous');
      if (current == null) break;
      start = current;
      previous = current;
    }
    return parts.join(', ');
  }

  /// The quoted, non-editable data rows for the action's params. Each tuple is
  /// (label, value, useMonospace). Values are UNTRUSTED display data.
  static List<(String, String, bool)> paramRows(
    BuildContext context,
    AgentAction a, {
    AgentActionParams? params,
  }) {
    final p = params ?? a.params;
    final rows = <(String, String, bool)>[];

    String mediaLabel() => switch (p.mediaType) {
          'tv' => context.l10n.opsTV,
          'book' => context.l10n.opsBook,
          'music' => context.l10n.opsMusic,
          _ => context.l10n.opsMovie,
        };

    switch (a.kind) {
      case AgentActionKind.grabRelease:
        if (p.mediaType != null)
          rows.add((context.l10n.opsType, mediaLabel(), false));
        if (p.releaseTitle != null) {
          rows.add((context.l10n.opsRelease, p.releaseTitle!, false));
        }
        if (p.quality != null)
          rows.add((context.l10n.opsQuality, p.quality!, false));
        if (p.size != null)
          rows.add((context.l10n.opsSize, _formatBytes(p.size!), false));
        if (p.protocol != null)
          rows.add((context.l10n.opsProtocol, p.protocol!, false));
        if (p.indexer != null || p.indexerId != null) {
          final name = p.indexer ?? context.l10n.opsIndexer;
          final id = p.indexerId == null ? '' : ' (#${p.indexerId})';
          rows.add((context.l10n.opsIndexer, '$name$id', false));
        }
        if (p.rejected) {
          final reason = p.rejections.isEmpty
              ? context.l10n.opsRejectedByTheConnectedMediaService
              : p.rejections.join('; ');
          rows.add((context.l10n.opsWarning, reason, false));
        }
        if (p.guid != null) {
          rows.add((context.l10n.opsSafeReference, _shorten(p.guid!), true));
        }
        if (p.queueIdToReplace != null) {
          rows.add((
            context.l10n.opsReplacesQueueItem,
            '#${p.queueIdToReplace}',
            false
          ));
        }
      case AgentActionKind.remediateQueue:
        if (p.mediaType != null)
          rows.add((context.l10n.opsType, mediaLabel(), false));
        if (p.queueId != null) {
          rows.add((context.l10n.opsQueueItem, '#${p.queueId}', false));
        }
        if (p.queueAction != null) {
          rows.add((context.l10n.opsAction, p.queueAction!, true));
        }
      case AgentActionKind.manualImport:
        if (p.mediaType != null)
          rows.add((context.l10n.opsType, mediaLabel(), false));
        if (p.queueId != null) {
          rows.add((context.l10n.opsQueueItem, '#${p.queueId}', false));
        }
        rows.add((
          context.l10n.opsForce,
          p.force ? context.l10n.opsYes : context.l10n.opsNo,
          false
        ));
      case AgentActionKind.triggerSearch:
        if (p.mediaType != null)
          rows.add((context.l10n.opsType, mediaLabel(), false));
        if (p.tmdbId != null)
          rows.add((context.l10n.opsTMDBId, '${p.tmdbId}', false));
        if (p.season != null)
          rows.add((context.l10n.opsSeason, '${p.season}', false));
        if (p.episode != null)
          rows.add((context.l10n.opsEpisode, '${p.episode}', false));
        if (p.authorId != null) {
          rows.add((context.l10n.opsAuthorId, '${p.authorId}', false));
        }
        if (p.bookId != null)
          rows.add((context.l10n.opsBookId, '${p.bookId}', false));
        if (p.artistId != null) {
          rows.add((context.l10n.opsArtistId, '${p.artistId}', false));
        }
        if (p.albumId != null)
          rows.add((context.l10n.opsAlbumId, '${p.albumId}', false));
      case AgentActionKind.rescan:
        if (p.mediaType != null)
          rows.add((context.l10n.opsType, mediaLabel(), false));
        if (p.tmdbId != null)
          rows.add((context.l10n.opsTMDBId, '${p.tmdbId}', false));
        if (p.authorId != null) {
          rows.add((context.l10n.opsAuthorId, '${p.authorId}', false));
        }
        if (p.artistId != null) {
          rows.add((context.l10n.opsArtistId, '${p.artistId}', false));
        }
      case AgentActionKind.deleteMediaFiles:
        if (p.mediaType != null)
          rows.add((context.l10n.opsType, mediaLabel(), false));
        if (p.tmdbId != null)
          rows.add((context.l10n.opsTMDBId, '${p.tmdbId}', false));
        if (p.bookId != null)
          rows.add((context.l10n.opsBookId, '${p.bookId}', false));
        if (p.albumId != null)
          rows.add((context.l10n.opsAlbumId, '${p.albumId}', false));
        if (p.season != null)
          rows.add((context.l10n.opsSeason, '${p.season}', false));
        final toDelete = p.episodes;
        if (toDelete.isNotEmpty) {
          rows.add((context.l10n.opsEpisodes, _episodeRanges(toDelete), false));
          rows.add(
              (context.l10n.opsFilesToDelete, '${toDelete.length}', false));
        }
        rows.add((
          context.l10n.opsBlockTheRelease,
          p.blocklist ? context.l10n.opsYes : context.l10n.opsNo,
          false
        ));
      case AgentActionKind.unknown:
        // Unknown kind: list whatever params arrived, generically + verbatim.
        p.raw.forEach((k, v) {
          rows.add((k, v?.toString() ?? '—', true));
        });
    }
    return rows;
  }

  /// The frozen footer text + icon + color once a decision is made. A standing
  /// rule's decision reads "Approved automatically" (with the rule's fixed
  /// label when the rule still exists) so history never claims a human
  /// approved what a rule did.
  static (String, IconData, Color) frozenFooter(
      BuildContext context, AgentAction a) {
    final when = a.decidedAt != null ? _relative(context, a.decidedAt!) : null;
    final by = a.executedAt != null ? _relative(context, a.executedAt!) : when;
    final auto = a.autoApproved;
    final autoLabel = a.autoRuleLabel;
    final approvedPrefix = auto
        ? '${context.l10n.opsApprovedAutomatically}${autoLabel != null ? ' · $autoLabel' : ''}'
        : context.l10n.opsApproved;
    switch (a.status) {
      case AgentActionStatus.executed:
        return (
          auto
              ? '$approvedPrefix${context.l10n.opsAppliedSuffix}'
              : '${context.l10n.opsApproved}${by != null ? ' · $by' : ''}${context.l10n.opsAppliedSuffix}',
          Icons.check_circle,
          AppTheme.available
        );
      case AgentActionStatus.approved:
      case AgentActionStatus.executing:
        return (
          auto
              ? '$approvedPrefix${context.l10n.opsApplyingSuffix}'
              : '${context.l10n.opsApproved}${when != null ? ' · $when' : ''}${context.l10n.opsApplyingSuffix}',
          Icons.check_circle,
          AppTheme.available
        );
      case AgentActionStatus.denied:
        return (
          '${context.l10n.opsDenied}${when != null ? ' · $when' : ''}',
          Icons.cancel,
          AppTheme.error
        );
      case AgentActionStatus.failed:
        return (
          auto
              ? context.l10n.opsApprovedAutomaticallyButTheFixFailed
              : context.l10n.opsApprovedButTheFixFailed,
          Icons.error_outline,
          AppTheme.error
        );
      case AgentActionStatus.superseded:
        return (
          context.l10n.opsNotRunTheIssueOrProposalChanged,
          Icons.history,
          AppTheme.textSecondary
        );
      case AgentActionStatus.outcomeUnknown:
        return (
          auto
              ? context.l10n.opsApprovedAutomaticallyButTheOutcomeCouldNot
              : context.l10n.opsApprovedButTheOutcomeCouldNotBe,
          Icons.help_outline,
          AppTheme.requested
        );
      case AgentActionStatus.proposed:
      case AgentActionStatus.unknown:
        return (
          localizeOperationsLabel(context, a.status.label),
          Icons.info_outline,
          AppTheme.textSecondary
        );
    }
  }

  /// Truncates a long opaque id (e.g. a release GUID) for display.
  static String _shorten(String s) {
    if (s.length <= 48) return s;
    return '${s.substring(0, 28)}…${s.substring(s.length - 12)}';
  }

  static String _relative(BuildContext context, DateTime t) {
    final d = DateTime.now().difference(t);
    if (d.inSeconds < 45) return context.l10n.opsJustNow;
    if (d.inMinutes < 60) {
      final m = d.inMinutes;
      return context.l10n.opsMinutesAgoAction(m.toString());
    }
    if (d.inHours < 24) {
      final h = d.inHours;
      return context.l10n.opsHoursAgoAction(h.toString());
    }
    final days = d.inDays;
    return context.l10n.opsDaysAgoAction(days.toString());
  }

  static String _formatBytes(int bytes) {
    if (bytes <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    var value = bytes.toDouble();
    var unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
      value /= 1024;
      unit++;
    }
    final digits = value >= 100 || unit == 0 ? 0 : 1;
    return '${value.toStringAsFixed(digits)} ${units[unit]}';
  }
}

/// One quoted, non-editable data row: "Label  value". The value is UNTRUSTED.
class _ParamRow extends StatelessWidget {
  final String label;
  final String value;
  final bool mono;

  const _ParamRow(
      {required this.label, required this.value, this.mono = false});

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SizedBox(
          width: 120,
          child: Text(
            label,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 12, height: 1.3),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          // Passive, selectable, never a control.
          child: SelectableText(
            value,
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 13,
              height: 1.3,
              fontFamily: mono ? 'monospace' : null,
            ),
          ),
        ),
      ],
    );
  }
}

/// A labeled, quoted block of UNTRUSTED agent/result text. Bordered and
/// passive so it reads as data, never as something to act on.
class _QuotedBlock extends StatelessWidget {
  final String label;
  final String text;

  const _QuotedBlock({required this.label, required this.text});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          label,
          style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 11,
              fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 4),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.all(10),
          decoration: BoxDecoration(
            color: AppTheme.surfaceVariant,
            borderRadius: BorderRadius.circular(8),
            border: const Border(
              left: BorderSide(color: AppTheme.border, width: 3),
            ),
          ),
          child: SelectableText(
            text,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 13,
              height: 1.4,
            ),
          ),
        ),
      ],
    );
  }
}
