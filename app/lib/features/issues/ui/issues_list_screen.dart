import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';

/// Admin list of reported / auto-detected problems. Tapping a row opens the
/// issue thread. Mirrors `PendingRequestsScreen`: a [RefreshIndicator] over a
/// [ListView.separated] of `_IssueTile`s, kept live by `issue_created`
/// pings and seeding the drawer badge on load.
class IssuesListScreen extends ConsumerStatefulWidget {
  const IssuesListScreen({super.key});

  @override
  ConsumerState<IssuesListScreen> createState() => _IssuesListScreenState();
}

class _IssuesListScreenState extends ConsumerState<IssuesListScreen> {
  List<Issue>? _issues;
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
      _isLoading = _issues == null;
      _error = null;
    });
    try {
      final issues = await ref.read(issuesServiceProvider).listIssues();
      if (!mounted) return;
      setState(() {
        _issues = issues;
        _isLoading = false;
      });
      // Keep the drawer badge in sync with the list we just loaded.
      final open = issues.where((i) => !i.status.isTerminal).length;
      ref.read(openIssuesProvider.notifier).setCount(open);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    // Refresh the list whenever a new issue is created (best-effort over WS).
    ref.listen(issuesChangedProvider, (_, __) => _load());

    return Scaffold(
      appBar: AppBar(title: const Text('Issues')),
      body: _isLoading
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
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center),
                        const SizedBox(height: 12),
                        ElevatedButton(
                            onPressed: _load, child: const Text('Retry')),
                      ],
                    ),
                  ),
                )
              : RefreshIndicator(
                  color: AppTheme.accent,
                  onRefresh: _load,
                  child: (_issues?.isEmpty ?? true)
                      ? ListView(
                          physics: const AlwaysScrollableScrollPhysics(),
                          children: const [
                            SizedBox(height: 120),
                            Center(
                              child: Text(
                                'No reported problems.',
                                style:
                                    TextStyle(color: AppTheme.textSecondary),
                              ),
                            ),
                          ],
                        )
                      : ListView.separated(
                          physics: const AlwaysScrollableScrollPhysics(),
                          padding: const EdgeInsets.symmetric(vertical: 8),
                          itemCount: _issues!.length,
                          separatorBuilder: (_, __) =>
                              const Divider(color: AppTheme.border, height: 1),
                          itemBuilder: (context, index) {
                            final issue = _issues![index];
                            return _IssueTile(
                              issue: issue,
                              onTap: () async {
                                await context.push('/issues/${issue.id}');
                                // Returning from the thread may have changed
                                // state (a reply, a dismiss) — refresh.
                                if (mounted) _load();
                              },
                            );
                          },
                        ),
                ),
    );
  }
}

class _IssueTile extends StatelessWidget {
  final Issue issue;
  final VoidCallback onTap;

  const _IssueTile({required this.issue, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final category = issue.category;
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      title: Text(
        issue.title.isEmpty ? 'Issue #${issue.id}' : issue.title,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.bold,
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
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              if (category != null) _chip(category.label),
              _statusChip(issue.status),
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
        return AppTheme.requested;
      case IssueStatus.open:
      case IssueStatus.investigating:
        return AppTheme.downloading;
      case IssueStatus.unknown:
        return AppTheme.textSecondary;
    }
  }
}
