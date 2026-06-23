import 'package:flutter/material.dart';
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

  const ProposedActionCard({
    super.key,
    required this.action,
    this.onDecided,
    this.onViewActivity,
  });

  @override
  ConsumerState<ProposedActionCard> createState() => _ProposedActionCardState();
}

class _ProposedActionCardState extends ConsumerState<ProposedActionCard> {
  /// The authoritative action shown. Starts from the prop and is replaced by
  /// the server's response after a decision so the frozen footer is accurate.
  late AgentAction _action;
  bool _busy = false;

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
    if (!_busy && widget.action.id == _action.id) {
      _action = widget.action;
    }
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _approve() async {
    if (_busy) return;
    setState(() => _busy = true);
    try {
      final updated =
          await ref.read(issuesServiceProvider).approveAction(_action.id);
      if (!mounted) return;
      setState(() {
        _action = updated;
        _busy = false;
      });
      widget.onDecided?.call(updated);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Approved — applying the fix.')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  Future<void> _deny() async {
    if (_busy) return;
    final controller = TextEditingController();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Deny this fix',
            style: TextStyle(color: AppTheme.textPrimary)),
        content: TextField(
          controller: controller,
          autofocus: true,
          maxLines: 3,
          minLines: 1,
          style: const TextStyle(color: AppTheme.textPrimary),
          decoration: const InputDecoration(
            labelText: 'Reason (optional)',
            labelStyle: TextStyle(color: AppTheme.textSecondary),
            border: OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.error,
              foregroundColor: Colors.white,
            ),
            onPressed: () => Navigator.of(dialogContext).pop(true),
            child: const Text('Deny'),
          ),
        ],
      ),
    );

    final note = controller.text.trim();
    // Defer disposal until after the dialog's exit transition completes — the
    // TextField still references the controller while the route animates out,
    // so disposing synchronously here would use it after disposal.
    WidgetsBinding.instance.addPostFrameCallback((_) => controller.dispose());
    if (confirmed != true || !mounted) return;

    setState(() => _busy = true);
    try {
      final updated = await ref
          .read(issuesServiceProvider)
          .denyAction(_action.id, note: note.isEmpty ? null : note);
      if (!mounted) return;
      setState(() {
        _action = updated;
        _busy = false;
      });
      widget.onDecided?.call(updated);
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Denied — the agent will try another way.')),
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _busy = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final isAdmin =
        ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false;
    final action = _action;
    final pending = action.status.isPending;

    return Container(
      margin: const EdgeInsets.symmetric(vertical: 8),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: pending
              ? AppTheme.requested.withValues(alpha: 0.6)
              : AppTheme.border,
          width: pending ? 1.2 : 0.8,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Header: a fixed icon + a fixed "proposed a fix" line (NOT agent text).
          const Row(
            children: [
              Icon(Icons.build_circle_outlined,
                  size: 18, color: AppTheme.requested),
              SizedBox(width: 8),
              Text(
                'The agent proposed a fix',
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 13,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),

          // Plain-language summary of the action kind (server-authored copy,
          // chosen by the typed kind enum — never an agent string).
          Text(
            _ActionCopy.summary(action),
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 15,
              height: 1.35,
              fontWeight: FontWeight.w600,
            ),
          ),

          // The quoted, non-editable params (release / quality / queue id …).
          ..._buildParamRows(action),

          // The agent's rationale, quoted as untrusted passive text.
          if (action.rationale.trim().isNotEmpty) ...[
            const SizedBox(height: 12),
            _QuotedBlock(
              label: "Agent's reasoning",
              text: action.rationale.trim(),
            ),
          ],

          // The execution result / deny note, once decided.
          if (action.resultText != null &&
              action.resultText!.trim().isNotEmpty) ...[
            const SizedBox(height: 12),
            _QuotedBlock(label: 'Result', text: action.resultText!.trim()),
          ],
          if (action.denyReason != null &&
              action.denyReason!.trim().isNotEmpty) ...[
            const SizedBox(height: 12),
            _QuotedBlock(label: 'Deny note', text: action.denyReason!.trim()),
          ],

          const SizedBox(height: 14),

          // Controls (admin + still proposed) OR a frozen status footer.
          if (pending && isAdmin)
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
                label: const Text('View agent activity'),
              ),
            ),
          ],
        ],
      ),
    );
  }

  List<Widget> _buildParamRows(AgentAction action) {
    final rows = _ActionCopy.paramRows(action);
    if (rows.isEmpty) return const [];
    return [
      const SizedBox(height: 10),
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
            onPressed: _busy ? null : _approve,
            icon: _busy
                ? const SizedBox(
                    width: 14,
                    height: 14,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: Colors.white),
                  )
                : const Icon(Icons.check_circle_outline, size: 18),
            label: const Text('Approve'),
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.available,
              foregroundColor: Colors.white,
              disabledBackgroundColor:
                  AppTheme.available.withValues(alpha: 0.5),
            ),
          ),
        ),
        const SizedBox(width: 10),
        Expanded(
          child: OutlinedButton.icon(
            onPressed: _busy ? null : _deny,
            icon: const Icon(Icons.cancel_outlined, size: 18),
            label: const Text('Deny'),
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
    // A reporter / non-admin viewing a still-pending proposal.
    if (action.status.isPending && !isAdmin) {
      return const Row(
        children: [
          Icon(Icons.hourglass_empty, size: 16, color: AppTheme.requested),
          SizedBox(width: 6),
          Expanded(
            child: Text(
              'Waiting on an admin to approve a fix.',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
          ),
        ],
      );
    }

    final decided = _ActionCopy.frozenFooter(action);
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

/// Server-authored, kind-driven copy for the card. Centralizes the plain
/// language so the action enum — never an agent string — selects the wording.
class _ActionCopy {
  /// A one-line plain-language summary of what approving will do.
  static String summary(AgentAction a) {
    switch (a.kind) {
      case AgentActionKind.grabRelease:
        return a.params.queueIdToReplace != null
            ? 'Grab a different release and remove the current one'
            : 'Grab a specific release';
      case AgentActionKind.remediateQueue:
        switch (a.params.queueAction) {
          case 'remove':
            return 'Remove the stuck item from the download queue';
          case 'blocklist_search':
            return 'Blocklist the current release and search for a replacement';
          case 'change_category':
            return 'Change the download category to unblock the import';
          default:
            return 'Fix the stuck download-queue item';
        }
      case AgentActionKind.manualImport:
        return a.params.force
            ? 'Force-import the downloaded files (overrides safety checks)'
            : 'Manually import the downloaded files';
      case AgentActionKind.triggerSearch:
        return 'Start an automatic search for this title';
      case AgentActionKind.rescan:
        return 'Rescan the files on disk and re-run the import';
      case AgentActionKind.unknown:
        return 'Apply a fix';
    }
  }

  /// The quoted, non-editable data rows for the action's params. Each tuple is
  /// (label, value, useMonospace). Values are UNTRUSTED display data.
  static List<(String, String, bool)> paramRows(AgentAction a) {
    final p = a.params;
    final rows = <(String, String, bool)>[];

    String mediaLabel() => p.mediaType == 'tv' ? 'TV' : 'Movie';

    switch (a.kind) {
      case AgentActionKind.grabRelease:
        if (p.mediaType != null) rows.add(('Type', mediaLabel(), false));
        if (p.guid != null) {
          rows.add(('Release', _shorten(p.guid!), true));
        }
        if (p.indexerId != null) {
          rows.add(('Indexer', '#${p.indexerId}', false));
        }
        if (p.queueIdToReplace != null) {
          rows.add(('Replaces queue item', '#${p.queueIdToReplace}', false));
        }
      case AgentActionKind.remediateQueue:
        if (p.mediaType != null) rows.add(('Type', mediaLabel(), false));
        if (p.queueId != null) {
          rows.add(('Queue item', '#${p.queueId}', false));
        }
        if (p.queueAction != null) {
          rows.add(('Action', p.queueAction!, true));
        }
      case AgentActionKind.manualImport:
        if (p.mediaType != null) rows.add(('Type', mediaLabel(), false));
        if (p.queueId != null) {
          rows.add(('Queue item', '#${p.queueId}', false));
        }
        rows.add(('Force', p.force ? 'yes' : 'no', false));
      case AgentActionKind.triggerSearch:
        if (p.mediaType != null) rows.add(('Type', mediaLabel(), false));
        if (p.tmdbId != null) rows.add(('TMDB id', '${p.tmdbId}', false));
        if (p.season != null) rows.add(('Season', '${p.season}', false));
      case AgentActionKind.rescan:
        if (p.mediaType != null) rows.add(('Type', mediaLabel(), false));
        if (p.tmdbId != null) rows.add(('TMDB id', '${p.tmdbId}', false));
      case AgentActionKind.unknown:
        // Unknown kind: list whatever params arrived, generically + verbatim.
        p.raw.forEach((k, v) {
          rows.add((k, v?.toString() ?? '—', true));
        });
    }
    return rows;
  }

  /// The frozen footer text + icon + color once a decision is made.
  static (String, IconData, Color) frozenFooter(AgentAction a) {
    final when = a.decidedAt != null ? _relative(a.decidedAt!) : null;
    final by = a.executedAt != null ? _relative(a.executedAt!) : when;
    switch (a.status) {
      case AgentActionStatus.executed:
        return ('Approved${by != null ? ' · $by' : ''} · applied',
            Icons.check_circle, AppTheme.available);
      case AgentActionStatus.approved:
      case AgentActionStatus.executing:
        return ('Approved${when != null ? ' · $when' : ''} · applying…',
            Icons.check_circle, AppTheme.available);
      case AgentActionStatus.denied:
        return ('Denied${when != null ? ' · $when' : ''}', Icons.cancel,
            AppTheme.error);
      case AgentActionStatus.failed:
        return ('Approved, but the fix failed', Icons.error_outline,
            AppTheme.error);
      case AgentActionStatus.superseded:
        return ('Superseded by a newer proposal', Icons.history,
            AppTheme.textSecondary);
      case AgentActionStatus.proposed:
      case AgentActionStatus.unknown:
        return (a.status.label, Icons.info_outline, AppTheme.textSecondary);
    }
  }

  /// Truncates a long opaque id (e.g. a release GUID) for display.
  static String _shorten(String s) {
    if (s.length <= 48) return s;
    return '${s.substring(0, 28)}…${s.substring(s.length - 12)}';
  }

  static String _relative(DateTime t) {
    final d = DateTime.now().difference(t);
    if (d.inSeconds < 45) return 'just now';
    if (d.inMinutes < 60) {
      final m = d.inMinutes;
      return '${m}m ago';
    }
    if (d.inHours < 24) {
      final h = d.inHours;
      return '${h}h ago';
    }
    final days = d.inDays;
    return '${days}d ago';
  }
}

/// One quoted, non-editable data row: "Label  value". The value is UNTRUSTED.
class _ParamRow extends StatelessWidget {
  final String label;
  final String value;
  final bool mono;

  const _ParamRow({required this.label, required this.value, this.mono = false});

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
