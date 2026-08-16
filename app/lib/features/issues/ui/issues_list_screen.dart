import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/attention_menu_visibility_switch.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/issue_models.dart';
import '../logic/issues_provider.dart';
import 'issue_refresh_banner.dart';

// Written as escapes on purpose: an inline literal is invisible in review and
// one stray normalisation silently turns the glue back into ordinary spaces.
const String _nbsp = '\u00A0';
const String _nbHyphen = '\u2011';

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
  int _closedTotal = 0;
  Map<String, dynamic>? _digest;

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

  Future<bool> _viewerIsAdmin() async {
    try {
      final auth = await ref.read(authProvider.future);
      return auth.user?.isAdmin == true;
    } catch (_) {
      return false;
    }
  }

  Future<void> _load() async {
    if (!mounted) return;
    final epoch = ++_loadEpoch;
    setState(() {
      _isLoading = _issues == null;
      if (_issues == null) _error = null;
    });
    try {
      // Admins see every issue; everyone else sees their OWN reports — the
      // reporter inbox, served self-scoped with requester copy applied. Await
      // the auth resolution: a ref.read at first frame races it and would
      // misroute the very first load.
      final admin = await _viewerIsAdmin();
      final service = ref.read(issuesServiceProvider);
      List<Issue> issues;
      var closedTotal = 0;
      if (admin) {
        final page = await service.listIssues();
        issues = page.issues;
        closedTotal = page.closedTotal;
      } else {
        issues = await service.listMyIssues();
      }
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _issues = issues;
        _closedTotal = closedTotal;
        _isLoading = false;
        _error = null;
      });
      // Keep both the actionable badge and tracking-aware menu visibility in
      // sync with the authoritative list we just loaded — admin surfaces only.
      if (admin) {
        _loadDigest();
        ref.read(issueQueueCountsProvider.notifier).setCounts(
              needsAttention:
                  issues.where((issue) => issue.status.needsAttention).length,
              tracking: issues.where((issue) => issue.status.isTracking).length,
            );
      }
    } catch (e) {
      if (!mounted || epoch != _loadEpoch) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _loadDigest() async {
    try {
      final digest = await ref.read(issuesServiceProvider).agentDigest();
      if (mounted) setState(() => _digest = digest);
    } catch (_) {
      // The scoreboard is a convenience; the list works without it.
    }
  }

  /// Glue one stat into an unbreakable run: a count must never be orphaned from
  /// the word it counts, and "zero-touch" must not split at its hyphen. What
  /// remains is a wrap opportunity only at the plain space preceding a "·", so
  /// the delimiter leads the next line instead of dangling at the end of one.
  static String _glueStat(String stat) =>
      stat.replaceAll(' ', _nbsp).replaceAll('-', _nbHyphen);

  /// The week at a glance, at the head of the list it summarises.
  ///
  /// This lives here rather than on the approvals queue because a quiet week
  /// leaves that queue empty — and a scoreboard nobody opens during the quiet
  /// weeks cannot be what makes them legible. Here the numbers also sit
  /// alongside the rows they count, so "N cleared on their own" is one tab away
  /// from the closed incidents it refers to. Admin-only: the digest endpoint is
  /// gated on PermissionRemediationManage.
  ///
  /// Two clauses, because there are two kinds of number here. The first counts
  /// what the window did; the second is state right now. Folding them together
  /// put "1 rule paused" — which may have been paused in March — inside "Last 7
  /// days", and it read as one running total that had to add up.
  ///
  /// "Resolved" is OUTCOME vocabulary: every problem that ended well, which is
  /// how admins read the word — half of one instance's admins called a week of
  /// self-cleared incidents "resolved" while the card said 0. Attribution is
  /// glued to the number ("— all on their own", "N by the agent") so automation
  /// claims only its own work and the headline can never contradict the lanes
  /// that break it down. Hand closures that the closer's own verb said were NOT
  /// fixes ("Close without fix", dismiss) stay outside "resolved" but on the
  /// card — human work is visible, just never mislabeled.
  Widget? _digestCard() {
    final d = _digest;
    if (d == null) return null;
    int n(String key) => (d[key] as num?)?.toInt() ?? 0;
    final resolved = n('issues_resolved') + n('self_cleared');
    final byAgent = n('resolved_by_agent');
    final byRules = n('rule_approved');
    final byAdmin = n('resolved_by_admin');
    final onOwn = resolved - byAgent - byRules - byAdmin;
    final closedNoFix = n('closed_no_fix');
    final dismissed = n('dismissed');
    final needsAdmin = n('needs_admin_open');
    final paused = n('paused_rules');
    // The attribution lanes are disjoint server-side and each is a subset of
    // the resolved total on the same clock, so the sentence always adds up.
    final lanes = <String>[
      if (byAgent > 0) '$byAgent by the agent',
      if (byRules > 0) '$byRules by your rules',
      if (byAdmin > 0) '$byAdmin by you',
    ];
    if (onOwn > 0) {
      lanes.add(lanes.isEmpty
          ? (onOwn == 1 ? 'on its own' : 'all on their own')
          : '$onOwn on their own');
    }
    // The em dash follows the separator's wrap policy: breakable before, glued
    // after, so it leads a wrapped line instead of dangling at the end of one.
    var head = _glueStat('$resolved resolved');
    if (lanes.isNotEmpty) {
      head += ' —$_nbsp${lanes.map(_glueStat).join(' ·$_nbsp')}';
    }
    final window = <String>[
      head,
      if (closedNoFix > 0) _glueStat('$closedNoFix closed by you (no fix)'),
      if (dismissed > 0) _glueStat('$dismissed dismissed'),
    ];
    final now = <String>[
      if (needsAdmin > 0) '$needsAdmin need${needsAdmin == 1 ? 's' : ''} you',
      if (paused > 0) '$paused rule${paused == 1 ? '' : 's'} paused',
    ].map(_glueStat).toList();
    return Container(
      margin: const EdgeInsets.fromLTRB(12, 10, 12, 0),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.insights_outlined, size: 18, color: AppTheme.accent),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '${_glueStat('Last 7 days:')} ${window.join(' ·$_nbsp')}',
                  style: const TextStyle(
                      color: AppTheme.textPrimary, fontSize: 13, height: 1.3),
                ),
                if (now.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 2),
                    child: Text(
                      '${_glueStat('Right now:')} ${now.join(' ·$_nbsp')}',
                      style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 13,
                          height: 1.3),
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  /// Closed history is bounded server-side, so the Closed tab says what it is
  /// not showing. An unmarked truncated list is the reason a reader stops
  /// looking for something that is still there.
  String? _historyNote() {
    if (_filter != _IssueFilter.closed) return null;
    final shown = _visibleIssues.length;
    if (_closedTotal <= shown) return null;
    return 'Showing the $shown most recent of $_closedTotal closed issues.';
  }

  @override
  Widget build(BuildContext context) {
    // Refresh whenever issue/action state changes (best-effort over WS).
    ref.listen(issuesChangedProvider, (_, __) => _scheduleLoad());

    return Scaffold(
      appBar: AppBar(
        title: Text(
          ref.watch(authProvider).valueOrNull?.user?.isAdmin == true
              ? 'Issues'
              : 'My reports',
        ),
      ),
      body: CenteredContent(
        child: Column(
          children: [
            if (_digestCard() case final card?) card,
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
                      value: _IssueFilter.tracking,
                      label: Text('Tracking'),
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
                                        switch (_filter) {
                                          _IssueFilter.needsAttention =>
                                            'No issues need attention.',
                                          _IssueFilter.tracking =>
                                            'Nothing is being tracked.',
                                          _IssueFilter.closed =>
                                            'No closed issues yet.',
                                        },
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
                                  itemCount:
                                      _visibleIssues.length + (_historyNote() != null ? 1 : 0),
                                  separatorBuilder: (_, __) => const Divider(
                                      color: AppTheme.border, height: 1),
                                  itemBuilder: (context, index) {
                                    if (index == _visibleIssues.length) {
                                      return Padding(
                                        padding: const EdgeInsets.fromLTRB(
                                            16, 14, 16, 20),
                                        child: Text(
                                          _historyNote()!,
                                          textAlign: TextAlign.center,
                                          style: const TextStyle(
                                              color: AppTheme.textSecondary,
                                              fontSize: 12),
                                        ),
                                      );
                                    }
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
            const Divider(color: AppTheme.border, height: 1),
            const SafeArea(
              top: false,
              child: AttentionMenuVisibilitySwitch(
                item: AttentionMenuItem.issues,
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
        issues.where((issue) => issue.status.needsAttention).toList(),
      _IssueFilter.tracking =>
        issues.where((issue) => issue.status.isTracking).toList(),
      _IssueFilter.closed =>
        issues.where((issue) => issue.status.isTerminal).toList(),
    };
  }
}

enum _IssueFilter { needsAttention, tracking, closed }

class _IssueTile extends StatelessWidget {
  final Issue issue;
  final VoidCallback onTap;

  const _IssueTile({required this.issue, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final category = issue.category;
    final tracking = issue.status.isTracking;
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      // Unread affordance: a small filled accent dot. The slot is reserved in
      // both states (transparent when read) so titles stay aligned across rows.
      leading: Container(
        width: 8,
        height: 8,
        decoration: BoxDecoration(
          color: issue.read || tracking ? Colors.transparent : AppTheme.accent,
          shape: BoxShape.circle,
        ),
      ),
      minLeadingWidth: 8,
      horizontalTitleGap: 12,
      title: Text(
        issue.title.isEmpty ? 'Issue #${issue.id}' : issue.title,
        style: TextStyle(
          color: tracking ? AppTheme.textSecondary : AppTheme.textPrimary,
          fontWeight:
              issue.read || tracking ? FontWeight.w600 : FontWeight.w800,
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
      case IssueStatus.awaitingConfirmation:
      case IssueStatus.needsAdmin:
        return AppTheme.requested;
      case IssueStatus.open:
      case IssueStatus.investigating:
        return AppTheme.downloading;
      case IssueStatus.observing:
      case IssueStatus.recovering:
      case IssueStatus.waiting:
      case IssueStatus.unknown:
        return AppTheme.textSecondary;
    }
  }
}
