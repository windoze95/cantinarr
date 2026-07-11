import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';
import 'issue_refresh_banner.dart';

/// Admin list of reported / auto-detected problems. Tapping a row opens the
/// issue thread. Mirrors `PendingRequestsScreen`: a [RefreshIndicator] over a
/// [ListView.separated] of `_IssueTile`s, kept live by issue/action pings and
/// seeding the drawer badge on load.
class IssuesListScreen extends ConsumerStatefulWidget {
  const IssuesListScreen({super.key});

  @override
  ConsumerState<IssuesListScreen> createState() => _IssuesListScreenState();
}

class _IssuesListScreenState extends ConsumerState<IssuesListScreen>
    with WidgetsBindingObserver {
  List<Issue>? _issues;
  bool _isLoading = true;
  String? _error;
  _IssueFilter _filter = _IssueFilter.needsAttention;
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
    // The socket does not replay changes missed in the background.
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
      _isLoading = _issues == null;
      if (_issues == null) _error = null;
    });
    try {
      final issues = await ref.read(issuesServiceProvider).listIssues();
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _issues = issues;
        _isLoading = false;
        _error = null;
      });
      // Keep the drawer badge in sync with the list we just loaded.
      final open = issues.where((i) => !i.status.isTerminal).length;
      ref.read(openIssuesProvider.notifier).setCount(open);
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    // Refresh whenever issue/action state changes (best-effort over WS).
    ref.listen(issuesChangedProvider, (_, __) => _scheduleLoad());

    return Scaffold(
      appBar: AppBar(title: const Text('Issues')),
      body: CenteredContent(
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(12, 10, 12, 4),
              child: SizedBox(
                width: double.infinity,
                child: SegmentedButton<_IssueFilter>(
                  segments: const [
                    ButtonSegment(
                      value: _IssueFilter.needsAttention,
                      label: Text('Needs attention'),
                    ),
                    ButtonSegment(
                      value: _IssueFilter.closed,
                      label: Text('Closed'),
                    ),
                  ],
                  selected: {_filter},
                  showSelectedIcon: false,
                  onSelectionChanged: (selection) =>
                      setState(() => _filter = selection.first),
                ),
              ),
            ),
            if (_error != null && _issues != null)
              Padding(
                padding: const EdgeInsets.fromLTRB(12, 4, 12, 6),
                child: IssueRefreshBanner(
                  message: "Couldn't refresh issues. Showing the last update.",
                  onRetry: _load,
                ),
              ),
            Expanded(
              child: _isLoading
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : _error != null && _issues == null
                      ? Center(
                          child: Padding(
                            padding: const EdgeInsets.all(24),
                            child: Column(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Text(_friendlyError(_error!),
                                    style:
                                        const TextStyle(color: AppTheme.error),
                                    textAlign: TextAlign.center),
                                const SizedBox(height: 12),
                                ElevatedButton(
                                    onPressed: _load,
                                    child: const Text('Retry')),
                              ],
                            ),
                          ),
                        )
                      : RefreshIndicator(
                          color: AppTheme.accent,
                          onRefresh: _load,
                          child: _visibleIssues.isEmpty
                              ? ListView(
                                  physics:
                                      const AlwaysScrollableScrollPhysics(),
                                  children: [
                                    const SizedBox(height: 120),
                                    Center(
                                      child: Text(
                                        _filter == _IssueFilter.needsAttention
                                            ? 'No issues need attention.'
                                            : 'No closed issues yet.',
                                        style: const TextStyle(
                                            color: AppTheme.textSecondary),
                                      ),
                                    ),
                                  ],
                                )
                              : ListView.separated(
                                  physics:
                                      const AlwaysScrollableScrollPhysics(),
                                  padding:
                                      const EdgeInsets.symmetric(vertical: 8),
                                  itemCount: _visibleIssues.length,
                                  separatorBuilder: (_, __) => const Divider(
                                      color: AppTheme.border, height: 1),
                                  itemBuilder: (context, index) {
                                    final issue = _visibleIssues[index];
                                    return _IssueTile(
                                      issue: issue,
                                      onTap: () async {
                                        await context
                                            .push('/issues/${issue.id}');
                                        // Returning from the thread may have changed
                                        // state (a reply, a dismiss) — refresh.
                                        if (mounted) _load();
                                      },
                                    );
                                  },
                                ),
                        ),
            ),
          ],
        ),
      ),
    );
  }

  List<Issue> get _visibleIssues {
    final issues = _issues ?? const <Issue>[];
    return switch (_filter) {
      _IssueFilter.needsAttention =>
        issues.where((issue) => !issue.status.isTerminal).toList(),
      _IssueFilter.closed =>
        issues.where((issue) => issue.status.isTerminal).toList(),
    };
  }
}

enum _IssueFilter { needsAttention, closed }

class _IssueTile extends StatelessWidget {
  final Issue issue;
  final VoidCallback onTap;

  const _IssueTile({required this.issue, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final category = issue.category;
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      // Unread affordance: a small filled accent dot. The slot is reserved in
      // both states (transparent when read) so titles stay aligned across rows.
      leading: Container(
        width: 8,
        height: 8,
        decoration: BoxDecoration(
          color: issue.read ? Colors.transparent : AppTheme.accent,
          shape: BoxShape.circle,
        ),
      ),
      minLeadingWidth: 8,
      horizontalTitleGap: 12,
      title: Text(
        issue.title.isEmpty ? 'Issue #${issue.id}' : issue.title,
        style: TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: issue.read ? FontWeight.w600 : FontWeight.w800,
        ),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 2),
          Text(
            issue.scopeLabel +
                (issue.reporterName.isNotEmpty
                    ? ' · ${issue.reporterName}'
                    : ''),
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 6),
          if (issue.status == IssueStatus.needsAdmin &&
              issue.resolutionLabel.trim().isNotEmpty) ...[
            Text(
              issue.resolutionLabel.trim(),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                height: 1.3,
              ),
            ),
            const SizedBox(height: 6),
          ],
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              if (category != null) _chip(category.label),
              _statusChip(issue.status),
              if (issue.status.isTerminal) _chip(issue.resolutionKind.label),
            ],
          ),
        ],
      ),
      trailing: const Icon(Icons.chevron_right, color: AppTheme.textSecondary),
      onTap: onTap,
    );
  }

  Widget _chip(String label) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: AppTheme.border),
      ),
      child: Text(
        label,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }

  Widget _statusChip(IssueStatus status) {
    final color = _statusColor(status);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: color.withValues(alpha: 0.5)),
      ),
      child: Text(
        status.label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }

  Color _statusColor(IssueStatus status) {
    switch (status) {
      case IssueStatus.resolved:
        return AppTheme.available;
      case IssueStatus.failed:
        return AppTheme.error;
      case IssueStatus.wontFix:
      case IssueStatus.dismissed:
        return AppTheme.unavailable;
      case IssueStatus.awaitingApproval:
      case IssueStatus.awaitingUser:
      case IssueStatus.needsAdmin:
        return AppTheme.requested;
      case IssueStatus.open:
      case IssueStatus.investigating:
        return AppTheme.downloading;
      case IssueStatus.unknown:
        return AppTheme.textSecondary;
    }
  }
}
