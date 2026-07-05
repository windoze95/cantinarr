import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/agent_action_models.dart';
import '../logic/issues_provider.dart';
import 'proposed_action_card.dart';

/// Admin approval queue for the AI-remediation agent: the proposed *arr fixes
/// awaiting a decision, grouped by the issue they belong to.
///
/// Mirrors `PendingRequestsScreen`: a [RefreshIndicator] over a scrolling list,
/// kept live by `agent_action_pending` / `agent_action_decided` pings, and
/// seeding the drawer badge on load. Each proposal renders as a
/// [ProposedActionCard]; once approved/denied the card freezes and the list
/// reloads so the resolved proposal drops out of the queue.
class PendingAgentActionsScreen extends ConsumerStatefulWidget {
  const PendingAgentActionsScreen({super.key});

  @override
  ConsumerState<PendingAgentActionsScreen> createState() =>
      _PendingAgentActionsScreenState();
}

class _PendingAgentActionsScreenState
    extends ConsumerState<PendingAgentActionsScreen> {
  List<AgentAction>? _actions;
  bool _isLoading = true;
  String? _error;

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
    setState(() {
      _isLoading = _actions == null;
      _error = null;
    });
    try {
      final actions =
          await ref.read(issuesServiceProvider).listPendingActions();
      if (!mounted) return;
      setState(() {
        _actions = actions;
        _isLoading = false;
      });
      // Keep the drawer badge in sync with the queue we just loaded.
      ref.read(pendingAgentActionsProvider.notifier).setCount(actions.length);
    } catch (e) {
      if (!mounted) return;
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
    ref.listen(agentActionsChangedProvider, (_, __) => _load());

    return Scaffold(
      appBar: AppBar(title: const Text('Agent fixes')),
      body: CenteredContent(
          child: _isLoading
              ? const Center(
                  child: CircularProgressIndicator(color: AppTheme.accent))
              : _error != null && _actions == null
                  ? _ErrorView(message: _friendlyError(_error!), onRetry: _load)
                  : RefreshIndicator(
                      color: AppTheme.accent,
                      onRefresh: _load,
                      child: (_actions?.isEmpty ?? true)
                          ? ListView(
                              physics: const AlwaysScrollableScrollPhysics(),
                              children: const [
                                SizedBox(height: 120),
                                Center(
                                  child: Text(
                                    'No fixes awaiting approval.',
                                    style: TextStyle(
                                        color: AppTheme.textSecondary),
                                  ),
                                ),
                              ],
                            )
                          : _buildGroupedList(_actions!),
                    )),
    );
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
                key: ValueKey(action.id),
                action: action,
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
