import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/agent_action_models.dart';
import '../logic/issues_provider.dart';
import 'issue_refresh_banner.dart';
import 'proposed_action_card.dart';

/// Admin review and history surface for AI-remediation fixes, grouped by the
/// issue they belong to.
///
/// Mirrors `PendingRequestsScreen`: a [RefreshIndicator] over a scrolling list,
/// kept live by `agent_action_pending` / `agent_action_decided` pings, and
/// seeding the drawer badge on load. Each proposal renders as a
/// [ProposedActionCard]; once decided it moves from Awaiting review to History.
class PendingAgentActionsScreen extends ConsumerStatefulWidget {
  const PendingAgentActionsScreen({super.key});

  @override
  ConsumerState<PendingAgentActionsScreen> createState() =>
      _PendingAgentActionsScreenState();
}

class _PendingAgentActionsScreenState
    extends ConsumerState<PendingAgentActionsScreen>
    with WidgetsBindingObserver {
  List<AgentAction>? _actions;
  bool _isLoading = true;
  String? _error;
  _AgentActionsTab _tab = _AgentActionsTab.awaitingReview;
  int _loadEpoch = 0;
  Timer? _realtimeDebounce;
  Timer? _poll;

  static const _pollInterval = Duration(seconds: 30);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
    _poll = Timer.periodic(_pollInterval, (_) => _load());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // Reconcile proposals decided while the app was backgrounded. The drawer
    // badge also refreshes on resume, but this screen owns its own snapshot.
    if (state == AppLifecycleState.resumed) _load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _realtimeDebounce?.cancel();
    _poll?.cancel();
    super.dispose();
  }

  void _scheduleLoad() {
    _realtimeDebounce?.cancel();
    _realtimeDebounce = Timer(const Duration(milliseconds: 250), _load);
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    if (!mounted) return;
    final epoch = ++_loadEpoch;
    setState(() {
      _isLoading = _actions == null;
      if (_actions == null) _error = null;
    });
    try {
      final actions = await ref.read(issuesServiceProvider).listAllActions();
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _actions = actions;
        _isLoading = false;
        _error = null;
      });
      // Keep the drawer badge in sync with the queue we just loaded.
      ref
          .read(pendingAgentActionsProvider.notifier)
          .setCount(actions.where((a) => a.canTakeAction).length);
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  /// After a decision, refresh the queue + badge so the resolved proposal drops
  /// out and any sibling counts update.
  void _onDecided(AgentAction _) => _load();

  @override
  Widget build(BuildContext context) {
    // A proposal raised or decided elsewhere reloads the queue.
    ref.listen(agentActionsChangedProvider, (_, __) => _scheduleLoad());

    return Scaffold(
      appBar: AppBar(title: const Text('Agent fixes')),
      body: CenteredContent(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 4),
              child: SizedBox(
                width: double.infinity,
                child: SegmentedButton<_AgentActionsTab>(
                  segments: const [
                    ButtonSegment(
                      value: _AgentActionsTab.awaitingReview,
                      label: Text('Awaiting review'),
                      icon: Icon(Icons.fact_check_outlined),
                    ),
                    ButtonSegment(
                      value: _AgentActionsTab.history,
                      label: Text('History'),
                      icon: Icon(Icons.history),
                    ),
                  ],
                  selected: {_tab},
                  showSelectedIcon: false,
                  onSelectionChanged: (selection) =>
                      setState(() => _tab = selection.first),
                ),
              ),
            ),
            if (_error != null && _actions != null)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 4, 12, 6),
                child: IssueRefreshBanner(
                  message:
                      "Couldn't refresh agent fixes. Showing the last update.",
                  onRetry: _load,
                ),
              ),
            Expanded(
              child: _isLoading
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : _error != null && _actions == null
                      ? _ErrorView(
                          message: _friendlyError(_error!), onRetry: _load)
                      : RefreshIndicator(
                          color: AppTheme.accent,
                          onRefresh: _load,
                          child: _visibleActions.isEmpty
                              ? ListView(
                                  physics:
                                      const AlwaysScrollableScrollPhysics(),
                                  children: [
                                    const SizedBox(height: 120),
                                    Center(
                                      child: Text(
                                        _tab == _AgentActionsTab.awaitingReview
                                            ? 'No fixes awaiting review.'
                                            : 'No agent-fix history yet.',
                                        style: const TextStyle(
                                            color: AppTheme.textSecondary),
                                      ),
                                    ),
                                  ],
                                )
                              : _buildGroupedList(_visibleActions),
                        ),
            ),
          ],
        ),
      ),
    );
  }

  List<AgentAction> get _visibleActions {
    final actions = _actions ?? const <AgentAction>[];
    return switch (_tab) {
      _AgentActionsTab.awaitingReview =>
        actions.where((action) => action.canTakeAction).toList(),
      _AgentActionsTab.history =>
        actions.where((action) => !action.canTakeAction).toList(),
    };
  }

  Widget _buildGroupedList(List<AgentAction> actions) {
    // Group by issue id, preserving the server's newest-first order. The
    // grouped layout makes "two proposals for the same problem" legible.
    final order = <int>[];
    final groups = <int, List<AgentAction>>{};
    for (final a in actions) {
      groups.putIfAbsent(a.issueId, () {
        order.add(a.issueId);
        return [];
      }).add(a);
    }

    return ListView.builder(
      physics: const AlwaysScrollableScrollPhysics(),
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
      itemCount: order.length,
      itemBuilder: (context, index) {
        final issueId = order[index];
        final group = groups[issueId]!;
        final title = group.first.issueTitle.isEmpty
            ? 'Issue #$issueId'
            : group.first.issueTitle;
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Issue header — tap to open the full thread.
            InkWell(
              onTap: () async {
                await context.push('/issues/$issueId');
                if (mounted) _load();
              },
              borderRadius: BorderRadius.circular(8),
              child: Padding(
                padding: const EdgeInsets.fromLTRB(4, 12, 4, 4),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        title,
                        style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 15,
                          fontWeight: FontWeight.bold,
                        ),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                      ),
                    ),
                    const Icon(Icons.chevron_right,
                        size: 18, color: AppTheme.textSecondary),
                  ],
                ),
              ),
            ),
            for (final action in group)
              ProposedActionCard(
                key: ValueKey(
                  '${action.id}-${action.statusRaw}-${action.canDecide}',
                ),
                action: action,
                decisionsEnabled: _error == null,
                onDecided: _onDecided,
                onViewActivity: action.runId != null
                    ? () => context.push('/agent-runs/${action.runId}')
                    : null,
              ),
          ],
        );
      },
    );
  }
}

enum _AgentActionsTab { awaitingReview, history }

class _ErrorView extends StatelessWidget {
  final String message;
  final VoidCallback onRetry;

  const _ErrorView({required this.message, required this.onRetry});

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(message,
                style: const TextStyle(color: AppTheme.error),
                textAlign: TextAlign.center),
            const SizedBox(height: 12),
            ElevatedButton(onPressed: onRetry, child: const Text('Retry')),
          ],
        ),
      ),
    );
  }
}
