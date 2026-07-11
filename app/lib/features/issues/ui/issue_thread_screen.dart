import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';

import 'package:dio/dio.dart';

import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/agent_action_models.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';
import 'issue_refresh_banner.dart';
import 'proposed_action_card.dart';

/// The issue conversation: a read-mostly transcript rendered with the AI-chat
/// bubble grammar, plus a reply field.
///
/// Provenance drives layout — a reporter/admin message is a right-aligned
/// bubble; an agent/system message is left/centered. Every message body is
/// rendered as PASSIVE, selectable text only (never a control or label),
/// because a `user` body is untrusted. Admins also see the durable agent-run
/// and action audit trail, including terminal actions after the issue closes.
class IssueThreadScreen extends ConsumerStatefulWidget {
  final int issueId;

  const IssueThreadScreen({super.key, required this.issueId});

  @override
  ConsumerState<IssueThreadScreen> createState() => _IssueThreadScreenState();
}

class _IssueThreadScreenState extends ConsumerState<IssueThreadScreen>
    with WidgetsBindingObserver {
  IssueThread? _thread;
  bool _isLoading = true;
  String? _error;
  String? _activityError;

  /// Durable action history for this issue. Unlike the old proposal-only
  /// query, this keeps executed/failed/superseded evidence in the thread.
  List<AgentAction> _actions = const [];
  List<AgentRun> _runs = const [];

  final _replyController = TextEditingController();
  final _scrollController = ScrollController();
  bool _sending = false;
  bool _dismissing = false;
  bool _completing = false;
  int _loadEpoch = 0;

  /// A short REST re-poll while the issue is still being worked, so steps that
  /// arrive without a WS ping (socket down) still surface. Best-effort.
  Timer? _poll;
  Timer? _realtimeDebounce;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load(initial: true));
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // WebSocket events are not replayed after a disconnect/background stint.
    // Reconcile the open thread from REST as soon as it becomes visible again.
    if (state == AppLifecycleState.resumed) _load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _poll?.cancel();
    _realtimeDebounce?.cancel();
    _replyController.dispose();
    _scrollController.dispose();
    super.dispose();
  }

  void _scheduleRealtimeLoad() {
    _realtimeDebounce?.cancel();
    _realtimeDebounce = Timer(const Duration(milliseconds: 250), _load);
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load({bool initial = false}) async {
    if (!mounted) return;
    final epoch = ++_loadEpoch;
    if (initial) setState(() => _isLoading = _thread == null);
    try {
      final service = ref.read(issuesServiceProvider);
      final thread = await service.getThread(widget.issueId);
      if (!mounted || epoch != _loadEpoch) return;

      // Admins get the permanent audit surface: every action status plus run
      // summaries. Reporter accounts cannot call the admin-only endpoint.
      final isAdmin =
          ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
      List<AgentAction> actions = isAdmin ? _actions : const [];
      List<AgentRun> runs = isAdmin ? _runs : const [];
      String? activityError;
      if (isAdmin) {
        try {
          final activity = await service.getIssueActivity(widget.issueId);
          actions = activity.actions;
          runs = activity.runs;
        } catch (e) {
          // Preserve the last audit snapshot, but never make that fallback look
          // like an authoritative empty history.
          activityError = e.toString();
        }
        if (!mounted || epoch != _loadEpoch) return;
      }

      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _thread = thread;
        _actions = actions;
        _runs = runs;
        _isLoading = false;
        _error = null;
        _activityError = activityError;
      });
      _syncPolling(thread.issue);
      if (initial) _scrollToBottomSoon();
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _isLoading = false;
        _error = e.toString();
      });
    }
  }

  /// Keep a low-frequency poll running for every open state; stop only once the
  /// issue closes.
  void _syncPolling(Issue issue) {
    // Parked issues can still change on another device, through the reply TTL,
    // or when an approval is decided. Poll every open state so a missed socket
    // event cannot leave stale decision controls on screen indefinitely.
    if (!issue.status.isTerminal && issue.closedAt == null) {
      _poll ??= Timer.periodic(
        const Duration(seconds: 10),
        (_) => _load(),
      );
    } else {
      _poll?.cancel();
      _poll = null;
    }
  }

  void _scrollToBottomSoon() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollController.hasClients) {
        _scrollController.jumpTo(_scrollController.position.maxScrollExtent);
      }
    });
  }

  Future<void> _sendReply() async {
    if (_thread?.issue.status.isTracking == true) return;
    final text = _replyController.text.trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      await ref.read(issuesServiceProvider).reply(widget.issueId, text);
      _replyController.clear();
      await _load();
      if (mounted) _scrollToBottomSoon();
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  Future<void> _dismissIssue() async {
    final issue = _thread?.issue;
    if (issue == null ||
        issue.status.isTerminal ||
        _dismissing ||
        _completing) {
      return;
    }
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text(
          'Dismiss this issue?',
          style: TextStyle(color: AppTheme.textPrimary),
        ),
        content: const Text(
          'This closes the issue without applying any pending fixes. Pending fixes will no longer be available for approval.',
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
            child: const Text('Dismiss issue'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    setState(() => _dismissing = true);
    try {
      await ref.read(issuesServiceProvider).dismiss(widget.issueId);
      await _load();
      if (!mounted) return;
      ref.read(openIssuesProvider.notifier).refresh();
      ref.read(pendingAgentActionsProvider.notifier).refresh();
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Issue dismissed.')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    } finally {
      if (mounted) setState(() => _dismissing = false);
    }
  }

  Future<void> _resolveIssue(AdminIssueDisposition disposition) async {
    final issue = _thread?.issue;
    if (issue == null ||
        issue.status.isTerminal ||
        issue.status.isTracking ||
        _completing ||
        _dismissing) {
      return;
    }
    final note = await showDialog<String>(
      context: context,
      builder: (_) => _AdminResolutionDialog(
        disposition: disposition,
        needsVerification: issue.status == IssueStatus.needsAdmin,
      ),
    );
    if (note == null || !mounted) return;

    setState(() => _completing = true);
    try {
      await ref.read(issuesServiceProvider).resolveIssue(
            widget.issueId,
            disposition: disposition,
            note: note,
          );
      await _load();
      if (!mounted) return;
      ref.read(openIssuesProvider.notifier).refresh();
      ref.read(pendingAgentActionsProvider.notifier).refresh();
      _showSnack(
        disposition == AdminIssueDisposition.resolved
            ? 'Issue marked resolved.'
            : 'Issue closed without a fix.',
      );
    } catch (e) {
      // A terminal transition is a CAS. If another admin/agent or an approval
      // won, reload before saying what happened; our attempted disposition may
      // not be the one that became durable.
      final conflict = e is DioException && e.response?.statusCode == 409;
      await _load();
      if (!mounted) return;
      final current = _thread?.issue;
      if (conflict || current?.status.isTerminal == true) {
        _showSnack(
            'The issue changed before completion. Showing its current state.');
      } else {
        _showSnack(_friendlyError(e));
      }
    } finally {
      if (mounted) setState(() => _completing = false);
    }
  }

  void _showSnack(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Refetch the thread over REST when this issue pings (the server emits a
    // thin `issue_updated` per persisted step, not full bodies).
    ref.listen(
      issueEventsProvider(widget.issueId),
      (_, __) => _scheduleRealtimeLoad(),
    );

    final thread = _thread;
    final isAdmin = ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false;
    return Scaffold(
      appBar: AppBar(
        title: Text(thread?.issue.title.isNotEmpty == true
            ? thread!.issue.title
            : 'Issue'),
        actions: [
          if (isAdmin && thread != null && !thread.issue.status.isTerminal)
            IconButton(
              onPressed: _dismissing || _completing ? null : _dismissIssue,
              tooltip: 'Dismiss issue',
              icon: _dismissing
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(
                        strokeWidth: 2,
                        color: AppTheme.textSecondary,
                      ),
                    )
                  : const Icon(Icons.close),
            ),
        ],
      ),
      body: _isLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent))
          : thread == null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(_friendlyError(_error ?? 'Could not load issue'),
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center),
                        const SizedBox(height: 12),
                        ElevatedButton(
                          onPressed: () => _load(initial: true),
                          child: const Text('Retry'),
                        ),
                      ],
                    ),
                  ),
                )
              : _buildBody(thread, isAdmin: isAdmin),
    );
  }

  Widget _buildBody(IssueThread thread, {required bool isAdmin}) {
    final issue = thread.issue;
    return Column(
      children: [
        Expanded(
          child: RefreshIndicator(
            color: AppTheme.accent,
            onRefresh: _load,
            // Full-width scroll surface; the thread column is capped and
            // centered so bubbles stay readable on desktop.
            child: LayoutBuilder(builder: (context, constraints) {
              final hPad = AppBreakpoints.centeredContentPadding(
                constraints.maxWidth,
              );
              return ListView(
                controller: _scrollController,
                physics: const AlwaysScrollableScrollPhysics(),
                padding: EdgeInsets.fromLTRB(hPad, 16, hPad, 16),
                children: [
                  _IssueSummaryCard(issue: issue),
                  if (issue.status.isTracking) ...[
                    const SizedBox(height: 10),
                    _PassiveTrackingBanner(status: issue.status),
                  ],
                  if (isAdmin && issue.status.needsAttention) ...[
                    const SizedBox(height: 10),
                    _AdminCompletionPanel(
                      issue: issue,
                      busy: _completing || _dismissing,
                      onDisposition: _resolveIssue,
                    ),
                  ],
                  if (_error != null) ...[
                    const SizedBox(height: 10),
                    IssueRefreshBanner(
                      message:
                          "Couldn't refresh this issue. Showing the last update.",
                      onRetry: _load,
                    ),
                  ],
                  if (_activityError != null) ...[
                    const SizedBox(height: 10),
                    IssueRefreshBanner(
                      message:
                          "Agent activity couldn't be refreshed. History may be incomplete.",
                      onRetry: _load,
                    ),
                  ],
                  const SizedBox(height: 16),
                  for (final msg in thread.messages) _MessageRow(message: msg),
                  if (_runs.isNotEmpty) ...[
                    const _ActivityHeading('Agent investigations'),
                    for (final run in _runs)
                      _RunSummaryTile(
                        run: run,
                        onTap: () => context.push('/agent-runs/${run.id}'),
                      ),
                  ],
                  if (_actions.isNotEmpty)
                    const _ActivityHeading('Agent fixes'),
                  // Every historical action remains visible here. Only a
                  // server-authorized, locally valid proposal renders controls.
                  for (final action in _actions)
                    ProposedActionCard(
                      key: ValueKey(
                        '${action.id}-${action.statusRaw}-${action.canDecide}',
                      ),
                      action: action,
                      decisionsEnabled:
                          _error == null && _activityError == null,
                      onDecided: (_) => _load(),
                      onViewActivity: action.runId == null
                          ? null
                          : () => context.push('/agent-runs/${action.runId}'),
                    ),
                  if (issue.status.isActive) const _WorkingIndicator(),
                ],
              );
            }),
          ),
        ),
        if (!issue.status.isTracking)
          _ReplyBar(
            controller: _replyController,
            sending: _sending,
            enabled: !issue.status.isTerminal,
            hintText: issue.status == IssueStatus.awaitingUser
                ? 'Answer the question…'
                : 'Add a reply…',
            onSend: _sendReply,
          ),
      ],
    );
  }
}

/// Fixed, non-interactive explanation for an issue that is intentionally being
/// tracked without starting an agent/admin workflow.
class _PassiveTrackingBanner extends StatelessWidget {
  final IssueStatus status;

  const _PassiveTrackingBanner({required this.status});

  @override
  Widget build(BuildContext context) {
    final recovering = status == IssueStatus.recovering;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(
            recovering ? Icons.sync : Icons.visibility_outlined,
            size: 20,
            color: AppTheme.textSecondary,
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  status.label,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                const SizedBox(height: 4),
                const Text(
                  'We’re tracking this quietly and will step in if it still '
                  'needs help.',
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    height: 1.35,
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

class _AdminCompletionPanel extends StatelessWidget {
  final Issue issue;
  final bool busy;
  final ValueChanged<AdminIssueDisposition> onDisposition;

  const _AdminCompletionPanel({
    required this.issue,
    required this.busy,
    required this.onDisposition,
  });

  @override
  Widget build(BuildContext context) {
    final needsReview = issue.status == IssueStatus.needsAdmin;
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(
          color: needsReview ? AppTheme.requested : AppTheme.border,
          width: needsReview ? 1 : 0.5,
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            needsReview ? 'Complete after admin review' : 'Complete this issue',
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 14,
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 4),
          Text(
            needsReview
                ? 'Verify the current arr state, then record the honest outcome and what you checked.'
                : 'Record a final human judgment and required note. This is separate from dismissing the report.',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
              height: 1.35,
            ),
          ),
          const SizedBox(height: 12),
          Wrap(
            spacing: 10,
            runSpacing: 8,
            children: [
              ElevatedButton.icon(
                onPressed: busy
                    ? null
                    : () => onDisposition(AdminIssueDisposition.resolved),
                icon: const Icon(Icons.check_circle_outline, size: 18),
                label: const Text('Mark resolved'),
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppTheme.available,
                  foregroundColor: AppTheme.background,
                ),
              ),
              OutlinedButton.icon(
                onPressed: busy
                    ? null
                    : () => onDisposition(AdminIssueDisposition.wontFix),
                icon: const Icon(Icons.do_not_disturb_alt_outlined, size: 18),
                label: const Text('Close without fix'),
                style: OutlinedButton.styleFrom(
                  foregroundColor: AppTheme.textPrimary,
                  side: const BorderSide(color: AppTheme.border),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _AdminResolutionDialog extends StatefulWidget {
  final AdminIssueDisposition disposition;
  final bool needsVerification;

  const _AdminResolutionDialog({
    required this.disposition,
    required this.needsVerification,
  });

  @override
  State<_AdminResolutionDialog> createState() => _AdminResolutionDialogState();
}

class _AdminResolutionDialogState extends State<_AdminResolutionDialog> {
  static const maxNoteLength = 2048;
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final resolved = widget.disposition == AdminIssueDisposition.resolved;
    final noteReady = _controller.text.trim().isNotEmpty;
    return AlertDialog(
      backgroundColor: AppTheme.surface,
      title: Text(
        resolved ? 'Mark this issue resolved?' : 'Close without a fix?',
        style: const TextStyle(color: AppTheme.textPrimary),
      ),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              resolved
                  ? 'Confirm only after checking that the reported problem is no longer present.'
                  : 'Use this when you reviewed the issue but cannot or should not apply a fix.',
              style: const TextStyle(
                color: AppTheme.textSecondary,
                height: 1.35,
              ),
            ),
            if (widget.needsVerification) ...[
              const SizedBox(height: 8),
              const Text(
                'An uncertain or partial agent outcome must be verified manually before completion.',
                style: TextStyle(
                  color: AppTheme.requested,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ],
            const SizedBox(height: 14),
            TextField(
              controller: _controller,
              autofocus: true,
              minLines: 2,
              maxLines: 5,
              maxLength: maxNoteLength,
              onChanged: (_) => setState(() {}),
              style: const TextStyle(color: AppTheme.textPrimary),
              decoration: const InputDecoration(
                labelText: 'Completion note (required)',
                hintText: 'What did you verify, or why is no fix appropriate?',
                border: OutlineInputBorder(),
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        ElevatedButton(
          onPressed: noteReady
              ? () => Navigator.of(context).pop(_controller.text.trim())
              : null,
          style: ElevatedButton.styleFrom(
            backgroundColor: resolved ? AppTheme.available : AppTheme.error,
            foregroundColor: AppTheme.background,
          ),
          child: Text(resolved ? 'Mark resolved' : 'Close without fix'),
        ),
      ],
    );
  }
}

/// A compact header describing the issue scope/category/status, with the
/// reporter's free-text detail rendered as passive (untrusted) text.
class _IssueSummaryCard extends StatelessWidget {
  final Issue issue;
  const _IssueSummaryCard({required this.issue});

  @override
  Widget build(BuildContext context) {
    final bits = <String>[
      issue.scopeLabel,
      if (issue.category != null) issue.category!.label,
    ];
    return Container(
      padding: const EdgeInsets.all(14),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            bits.join(' · '),
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
          ),
          const SizedBox(height: 6),
          Text(
            'Status: ${issue.status.label}',
            style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w600),
          ),
          if (issue.status.isTerminal) ...[
            const SizedBox(height: 6),
            Text(
              issue.resolutionKind.label +
                  (issue.closedAt == null
                      ? ''
                      : ' · ${DateFormat('MMM d, h:mm a').format(issue.closedAt!)}'),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontWeight: FontWeight.w600,
              ),
            ),
            if (issue.resolutionLabel.trim().isNotEmpty) ...[
              const SizedBox(height: 8),
              SelectableText(
                issue.resolutionLabel.trim(),
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                  height: 1.35,
                ),
              ),
            ],
          ],
          if (issue.status == IssueStatus.needsAdmin &&
              issue.resolutionLabel.trim().isNotEmpty) ...[
            const SizedBox(height: 8),
            const Text(
              'Needs a closer look',
              style: TextStyle(
                color: AppTheme.requested,
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
            const SizedBox(height: 4),
            SelectableText(
              issue.resolutionLabel.trim(),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.35,
              ),
            ),
          ],
          if (issue.detail.isNotEmpty) ...[
            const SizedBox(height: 10),
            // UNTRUSTED reporter/diagnosis text — passive, selectable only.
            SelectableText(
              issue.detail,
              style: const TextStyle(
                  color: AppTheme.textPrimary, fontSize: 14, height: 1.4),
            ),
          ],
        ],
      ),
    );
  }
}

class _ActivityHeading extends StatelessWidget {
  final String label;

  const _ActivityHeading(this.label);

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(2, 14, 2, 4),
        child: Text(
          label,
          style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w700,
          ),
        ),
      );
}

class _RunSummaryTile extends StatelessWidget {
  final AgentRun run;
  final VoidCallback onTap;

  const _RunSummaryTile({required this.run, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final detail = <String>[
      '${run.stepCount} ${run.stepCount == 1 ? 'step' : 'steps'}',
      run.costLabel,
      if (run.startedAt != null)
        DateFormat('MMM d, h:mm a').format(run.startedAt!),
    ];
    return Card(
      color: AppTheme.surface,
      margin: const EdgeInsets.symmetric(vertical: 4),
      child: ListTile(
        onTap: onTap,
        leading: const Icon(Icons.timeline, color: AppTheme.accent),
        title: Text(
          run.statusLabel,
          style: const TextStyle(
            color: AppTheme.textPrimary,
            fontWeight: FontWeight.w600,
          ),
        ),
        subtitle: Text(
          detail.join(' · '),
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        ),
        trailing:
            const Icon(Icons.chevron_right, color: AppTheme.textSecondary),
      ),
    );
  }
}

/// One thread message rendered in the AI-chat grammar. Human (reporter/admin)
/// messages align right; agent/system align left or centered.
class _MessageRow extends StatelessWidget {
  final IssueMessage message;
  const _MessageRow({required this.message});

  @override
  Widget build(BuildContext context) {
    switch (message.authorKind) {
      case IssueAuthorKind.user:
      case IssueAuthorKind.admin:
        return _Bubble(message: message, isFromHuman: true);
      case IssueAuthorKind.agent:
        return _Bubble(message: message, isFromHuman: false);
      case IssueAuthorKind.system:
      case IssueAuthorKind.unknown:
        // Centered, low-emphasis notice. Also the safe fallback for any
        // not-yet-modeled message kind a later wave introduces.
        return _SystemNotice(message: message);
    }
  }
}

/// A left/right chat bubble. The body is always passive selectable text.
class _Bubble extends StatelessWidget {
  final IssueMessage message;
  final bool isFromHuman;

  const _Bubble({required this.message, required this.isFromHuman});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: Row(
        mainAxisAlignment:
            isFromHuman ? MainAxisAlignment.end : MainAxisAlignment.start,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (!isFromHuman) ...[
            Container(
              width: 32,
              height: 32,
              decoration: const BoxDecoration(
                color: AppTheme.accent,
                shape: BoxShape.circle,
              ),
              child: const Icon(
                Icons.smart_toy,
                size: 18,
                color: AppTheme.onAccent,
              ),
            ),
            const SizedBox(width: 8),
          ],
          Flexible(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (message.authorName.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(left: 2, bottom: 4),
                    child: Text(
                      message.authorName,
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 11),
                    ),
                  ),
                Container(
                  padding:
                      const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                  decoration: BoxDecoration(
                    color: isFromHuman
                        ? AppTheme.accent.withValues(alpha: 0.15)
                        : AppTheme.surfaceVariant,
                    borderRadius: BorderRadius.only(
                      topLeft: const Radius.circular(16),
                      topRight: const Radius.circular(16),
                      bottomLeft: Radius.circular(isFromHuman ? 16 : 4),
                      bottomRight: Radius.circular(isFromHuman ? 4 : 16),
                    ),
                    border: Border.all(
                      color: isFromHuman
                          ? AppTheme.accent.withValues(alpha: 0.3)
                          : AppTheme.border,
                    ),
                  ),
                  // Passive, selectable text — never a control/label.
                  child: SelectableText(
                    message.body,
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 15,
                      height: 1.4,
                    ),
                  ),
                ),
                if (message.createdAt != null)
                  Padding(
                    padding: const EdgeInsets.only(left: 2, top: 4),
                    child: Text(
                      DateFormat('MMM d, h:mm a').format(message.createdAt!),
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 10),
                    ),
                  ),
              ],
            ),
          ),
          if (isFromHuman) const SizedBox(width: 8),
        ],
      ),
    );
  }
}

/// A centered system notice (e.g. "Admin denied: …"). Passive text only.
class _SystemNotice extends StatelessWidget {
  final IssueMessage message;
  const _SystemNotice({required this.message});

  @override
  Widget build(BuildContext context) {
    if (message.body.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
          decoration: BoxDecoration(
            color: AppTheme.surfaceVariant,
            borderRadius: BorderRadius.circular(12),
            border: Border.all(color: AppTheme.border),
          ),
          child: SelectableText(
            message.body,
            textAlign: TextAlign.center,
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
          ),
        ),
      ),
    );
  }
}

/// A small "working" hint shown while the issue is open/investigating.
class _WorkingIndicator extends StatelessWidget {
  const _WorkingIndicator();

  @override
  Widget build(BuildContext context) {
    return const Padding(
      padding: EdgeInsets.only(top: 4, left: 2),
      child: Row(
        children: [
          SizedBox(
            width: 12,
            height: 12,
            child: CircularProgressIndicator(
                strokeWidth: 1.5, color: AppTheme.accent),
          ),
          SizedBox(width: 8),
          Text(
            'Working on it…',
            style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                fontStyle: FontStyle.italic),
          ),
        ],
      ),
    );
  }
}

/// The bottom reply input. Disabled once the issue is terminal.
class _ReplyBar extends StatelessWidget {
  final TextEditingController controller;
  final bool sending;
  final bool enabled;
  final String hintText;
  final VoidCallback onSend;

  const _ReplyBar({
    required this.controller,
    required this.sending,
    required this.enabled,
    required this.hintText,
    required this.onSend,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        border: Border(top: BorderSide(color: AppTheme.border)),
      ),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      child: SafeArea(
        top: false,
        // Bar background spans the window; the input is capped to match the
        // thread column on desktop.
        child: CenteredContent(
          child: enabled
              ? Row(
                  children: [
                    Expanded(
                      child: TextField(
                        controller: controller,
                        keyboardType: TextInputType.multiline,
                        textInputAction: TextInputAction.send,
                        onSubmitted: (_) => onSend(),
                        minLines: 1,
                        maxLines: 4,
                        style: const TextStyle(color: AppTheme.textPrimary),
                        decoration: InputDecoration(
                          hintText: hintText,
                          hintStyle:
                              const TextStyle(color: AppTheme.textSecondary),
                          border: InputBorder.none,
                          contentPadding: const EdgeInsets.symmetric(
                              horizontal: 12, vertical: 10),
                        ),
                      ),
                    ),
                    IconButton(
                      onPressed: sending ? null : onSend,
                      icon: Icon(
                        Icons.send_rounded,
                        color:
                            sending ? AppTheme.textSecondary : AppTheme.accent,
                      ),
                    ),
                  ],
                )
              : const Padding(
                  padding: EdgeInsets.symmetric(vertical: 10, horizontal: 8),
                  child: Text(
                    'This issue is closed.',
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                  ),
                ),
        ),
      ),
    );
  }
}
