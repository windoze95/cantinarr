import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/config/app_config.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/attention_menu_visibility_switch.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/request_settings_service.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/pending_approvals_provider.dart';

/// Sentinel season-scope value used in the approve dialog meaning "keep the
/// exact seasons the user requested" (no coarse-scope override).
const _keepRequestedScope = '__keep_requested__';

/// Admin approval queue: approve (optionally modifying options) or deny
/// pending media requests.
class PendingRequestsScreen extends ConsumerStatefulWidget {
  const PendingRequestsScreen({super.key});

  @override
  ConsumerState<PendingRequestsScreen> createState() =>
      _PendingRequestsScreenState();
}

class _PendingRequestsScreenState extends ConsumerState<PendingRequestsScreen> {
  late final RequestSettingsService _service;
  List<PendingRequestItem>? _pending;

  /// Requests the server is retrying on its own. Kept apart from [_pending]
  /// everywhere — including the badge below — because they need no decision.
  List<PendingRequestItem> _waiting = const [];

  /// This load could not read the waiting list, though the server has one. It
  /// is tracked separately from an empty list so the section can say "couldn't
  /// check" instead of showing the silence that started all this.
  bool _waitingBlind = false;
  AdminRequestSettings? _admin;
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = RequestSettingsService(
        backendDio: ref.read(backendClientProvider),
      );
      _load();
    });
  }

  String _friendlyError(Object e) {
    String? raw;
    if (e is DioException) {
      final data = e.response?.data;
      if (data is Map) {
        final message = data['error'] ?? data['message'];
        if (message is String && message.isNotEmpty) raw = message;
      } else if (data is String && data.isNotEmpty) {
        raw = data;
      }
    }
    final lower = raw?.toLowerCase() ?? '';
    final missingBookInstance = lower.contains('book') &&
        lower.contains('instance') &&
        (lower.contains('missing') ||
            lower.contains('does not identify') ||
            lower.contains('no library') ||
            lower.contains('no pinned'));
    if (missingBookInstance) {
      return 'This older request doesn’t identify a book library; deny it and ask the requester to submit it again.';
    }
    if (lower.contains('root folder') ||
        lower.contains('quality profile') ||
        lower.contains('metadata profile') ||
        lower.contains('book configuration')) {
      return 'Check this book library’s paths and profiles, then try again.';
    }
    // Approving replayed an add that had already failed the same way. The old
    // "Something went wrong. Try again." read as a transient glitch and invited
    // another Approve, which cannot work until the library has the record.
    if (lower.contains('add this book in the library first')) {
      return 'The library still can’t find this book. Add it in the library '
          'first, then approve — retrying here won’t help.';
    }
    return 'Something went wrong. Try again.';
  }

  String _approvalMessage(
    PendingRequestItem item,
    BookApprovalResult result,
  ) {
    if (!item.isBook) return 'Approved ${item.title}';
    if (!result.isKnown || result.formats.isEmpty) {
      return 'Approval saved. The remaining queue was refreshed.';
    }
    final approved = <String>[];
    final attention = <String>[];
    for (final format in [
      BookRequestFormat.ebook,
      BookRequestFormat.audiobook,
    ]) {
      final status = result.formats[format];
      if (status == null) continue;
      switch (status) {
        case RequestStatus.available:
        case RequestStatus.downloading:
        case RequestStatus.requested:
        case RequestStatus.partial:
          approved.add('${format.label} approved.');
        case RequestStatus.pending:
          attention.add('${format.label} still needs attention.');
        case RequestStatus.denied:
        case RequestStatus.unavailable:
          attention.add(result.status == RequestStatus.partial
              ? '${format.label} still needs attention.'
              : '${format.label} could not be approved.');
      }
    }
    final message = [...approved, ...attention].join(' ');
    return message.isEmpty
        ? 'Approval saved. The remaining queue was refreshed.'
        : message;
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = _pending == null;
      _error = null;
    });
    try {
      final pending = await _service.listPending();
      final admin = await _service.getAdminSettings();
      // Best-effort, and deliberately after the two loads this screen exists
      // for: the informational half must never be able to take down the
      // actionable half. An admin who cannot approve anything because a list
      // with no buttons failed is worse off than one who never had the list.
      var waiting = const <PendingRequestItem>[];
      var waitingBlind = false;
      try {
        waiting = await _service.listWaiting() ?? const [];
      } catch (_) {
        waitingBlind = true;
      }
      if (!mounted) return;
      setState(() {
        _pending = pending;
        _waiting = waiting;
        _waitingBlind = waitingBlind;
        _admin = admin;
        _isLoading = false;
      });
      // Keep the drawer + app-icon badges in sync with the queue we just loaded
      // (covers opening the screen and the reload after an approve/deny).
      // Deliberately pending.length alone: a badge is a claim that someone must
      // act, and nobody has to act on a wait. Adding them would page an admin
      // to a screen whose only new row has no buttons.
      ref.read(pendingApprovalsProvider.notifier).setCount(pending.length);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _approve(PendingRequestItem item) async {
    final admin = _admin;
    if (admin == null) return;
    final requestedBookFormat = item.requestedBookFormat;
    if (item.isBook && requestedBookFormat == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(
          content: Text(
            'This request uses an unsupported book format and cannot be approved.',
          ),
        ),
      );
      return;
    }
    final profiles = item.isBook
        ? const <QualityProfile>[]
        : (item.isTv ? admin.sonarrProfiles : admin.radarrProfiles);

    // An explicit per-season request stores a JSON list in seasonScope, which
    // isn't one of the coarse dropdown values — represent it as a "keep
    // requested" option so the dropdown doesn't break and the admin can leave
    // the chosen seasons untouched.
    final isExplicit = SeasonScope.isExplicitList(item.seasonScope);
    String chosenScope = isExplicit
        ? _keepRequestedScope
        : (item.seasonScope.isNotEmpty ? item.seasonScope : SeasonScope.all);
    int? chosenProfile;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        return StatefulBuilder(
          builder: (dialogContext, setDialogState) {
            return AlertDialog(
              backgroundColor: AppTheme.surface,
              title: Text(
                'Approve ${item.title}',
                style: const TextStyle(color: AppTheme.textPrimary),
              ),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    item.requestedByLabel,
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 13,
                    ),
                  ),
                  const SizedBox(height: 16),
                  if (item.isTv) ...[
                    const Text(
                      'Season scope',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    DropdownButtonFormField<String>(
                      initialValue: chosenScope,
                      dropdownColor: AppTheme.surfaceVariant,
                      style: const TextStyle(color: AppTheme.textPrimary),
                      decoration: const InputDecoration(
                        border: OutlineInputBorder(),
                        isDense: true,
                      ),
                      items: [
                        if (isExplicit)
                          DropdownMenuItem<String>(
                            value: _keepRequestedScope,
                            child: Text(
                                'Keep requested (${SeasonScope.describe(item.seasonScope)})'),
                          ),
                        ...SeasonScope.choices.map((c) =>
                            DropdownMenuItem<String>(
                                value: c.value, child: Text(c.label))),
                      ],
                      onChanged: (v) {
                        if (v != null) {
                          setDialogState(() => chosenScope = v);
                        }
                      },
                    ),
                    const SizedBox(height: 16),
                  ],
                  if (item.isBook) ...[
                    const Text(
                      'Requested format',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      requestedBookFormat!.label,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    if (item.instanceName.isNotEmpty) ...[
                      const SizedBox(height: 14),
                      Text(
                        'Library: ${item.instanceName}',
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 14,
                        ),
                      ),
                    ],
                  ] else ...[
                    const Text(
                      'Quality profile',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    DropdownButtonFormField<int?>(
                      initialValue: chosenProfile,
                      dropdownColor: AppTheme.surfaceVariant,
                      style: const TextStyle(color: AppTheme.textPrimary),
                      decoration: const InputDecoration(
                        border: OutlineInputBorder(),
                        isDense: true,
                      ),
                      items: [
                        const DropdownMenuItem<int?>(
                          value: null,
                          child: Text('Default'),
                        ),
                        ...profiles.map((p) => DropdownMenuItem<int?>(
                              value: p.id,
                              child: Text(p.name),
                            )),
                      ],
                      onChanged: (v) => setDialogState(() => chosenProfile = v),
                    ),
                  ],
                ],
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(dialogContext).pop(false),
                  child: const Text('Cancel'),
                ),
                ElevatedButton(
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppTheme.available,
                    foregroundColor: AppTheme.background,
                  ),
                  onPressed: () => Navigator.of(dialogContext).pop(true),
                  child: const Text('Approve'),
                ),
              ],
            );
          },
        );
      },
    );

    if (confirmed != true) return;
    if (!mounted) return;
    try {
      final result = await _service.approve(
        item.id,
        // The "keep requested" sentinel sends no override, so the server keeps
        // the explicit season list the user picked.
        seasonScope: item.isTv
            ? (chosenScope == _keepRequestedScope ? null : chosenScope)
            : null,
        qualityProfileId: item.isBook ? null : chosenProfile,
      );
      if (!mounted) return;
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_approvalMessage(item, result))),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  /// "Try again" on a row whose author-import wait ended: the server replays
  /// the add and either completes the request or resumes the automatic watch.
  Future<void> _wait(PendingRequestItem item) async {
    try {
      final message = await _service.wait(item.id);
      if (!mounted) return;
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(message.isNotEmpty
              ? message
              : 'The library has this author now — ${item.title} went through.'),
        ),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  Future<void> _deny(PendingRequestItem item) async {
    final controller = TextEditingController();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          backgroundColor: AppTheme.surface,
          title: Text(
            'Deny ${item.title}',
            style: const TextStyle(color: AppTheme.textPrimary),
          ),
          content: TextField(
            controller: controller,
            autofocus: true,
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
                foregroundColor: AppTheme.background,
              ),
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: const Text('Deny'),
            ),
          ],
        );
      },
    );

    final reason = controller.text.trim();
    controller.dispose();
    if (confirmed != true) return;
    if (!mounted) return;
    try {
      await _service.deny(item.id, reason: reason.isEmpty ? null : reason);
      if (!mounted) return;
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Denied ${item.title}')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  /// The screen's body: what needs a person, then what needs nothing.
  ///
  /// The two are separate sections rather than one merged list because they are
  /// different kinds of fact. Merging them would put Approve next to a row the
  /// server refuses to approve early, and hiding the second — which is what
  /// this screen did before — left an admin with no way to see a request the
  /// server had been retrying for hours.
  List<Widget> _sections() {
    final pending = _pending ?? const <PendingRequestItem>[];
    final children = <Widget>[];

    if (pending.isEmpty && _waiting.isEmpty && !_waitingBlind) {
      return const [
        SizedBox(height: 120),
        Center(
          child: Text(
            'No pending requests.',
            style: TextStyle(color: AppTheme.textSecondary),
          ),
        ),
      ];
    }

    if (pending.isNotEmpty) {
      children.add(const _SectionHeader(
        title: 'Needs approval',
        caption: null,
      ));
      for (var i = 0; i < pending.length; i++) {
        if (i > 0) {
          children.add(const Divider(color: AppTheme.border, height: 1));
        }
        final item = pending[i];
        children.add(_PendingTile(
          item: item,
          onApprove: () => _approve(item),
          onDeny: () => _deny(item),
          onWait: () => _wait(item),
        ));
      }
    }

    if (_waiting.isNotEmpty || _waitingBlind) {
      children.add(const _SectionHeader(
        title: 'Waiting for library',
        caption: 'Being retried automatically. Nothing to approve.',
      ));
      if (_waitingBlind) {
        children.add(const Padding(
          padding: EdgeInsets.fromLTRB(16, 4, 16, 12),
          child: Text(
            'Couldn’t check what the server is retrying. Pull to refresh.',
            style: TextStyle(color: AppTheme.error, fontSize: 12),
          ),
        ));
      }
      for (var i = 0; i < _waiting.length; i++) {
        if (i > 0) {
          children.add(const Divider(color: AppTheme.border, height: 1));
        }
        children.add(_WaitingTile(item: _waiting[i]));
      }
    }

    return children;
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Approvals')),
      body: CenteredContent(
        child: Column(
          children: [
            Expanded(
              child: _isLoading
                  ? const Center(
                      child: CircularProgressIndicator(color: AppTheme.accent))
                  : _error != null && _pending == null
                      ? Center(
                          child: Padding(
                            padding: const EdgeInsets.all(24),
                            child: Column(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                Text(_error!,
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
                          // No realtime listener, on purpose. The actionable
                          // half of this screen has never had one, and adding a
                          // socket for the half nobody must act on would make
                          // the informational rows the liveliest thing here.
                          child: ListView(
                            physics: const AlwaysScrollableScrollPhysics(),
                            padding: const EdgeInsets.symmetric(vertical: 8),
                            children: _sections(),
                          ),
                        ),
            ),
            const Divider(color: AppTheme.border, height: 1),
            const SafeArea(
              top: false,
              child: AttentionMenuVisibilitySwitch(
                item: AttentionMenuItem.approvals,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  final String title;
  final String? caption;

  const _SectionHeader({required this.title, required this.caption});

  @override
  Widget build(BuildContext context) {
    final caption = this.caption;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 13,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.3,
            ),
          ),
          if (caption != null) ...[
            const SizedBox(height: 2),
            Text(
              caption,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
          ],
        ],
      ),
    );
  }
}

/// One request the server is handling itself. It carries no Approve or Deny:
/// the server refuses an early approval, so offering the button would promise
/// an action that cannot be taken.
class _WaitingTile extends StatelessWidget {
  final PendingRequestItem item;

  const _WaitingTile({required this.item});

  @override
  Widget build(BuildContext context) {
    final detailRoute = item.detailRoute;
    final waited = _relativeTime(item.requestedAt);
    // Absent is not "never tried" — the server restarted and cannot vouch for
    // the attempt its predecessor made. Say that, rather than leaving a blank
    // that reads as nothing having happened.
    final lastTried = item.lastAttemptAt == null
        ? 'last attempt unknown'
        : 'last tried ${_relativeTime(item.lastAttemptAt)}';
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      onTap: detailRoute == null ? null : () => context.push(detailRoute),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: item.posterPath.isEmpty
                ? null
                : AppConfig.tmdbPoster(item.posterPath, width: 185),
            fit: BoxFit.cover,
            icon: item.isBook ? Icons.menu_book : Icons.movie,
          ),
        ),
      ),
      title: Text(
        item.title,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.bold,
        ),
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 2),
          Text(
            item.requestedByLabel,
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 4),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Icon(Icons.schedule_rounded,
                  size: 15, color: AppTheme.requested),
              const SizedBox(width: 5),
              Expanded(
                child: Text(
                  item.waitDescription ?? 'Waiting for the library',
                  style: const TextStyle(
                    color: AppTheme.requested,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              _waitingChip(item.mediaLabel),
              if (item.isBook && item.bookFormat.isNotEmpty)
                _waitingChip(
                    item.requestedBookFormat?.label ?? 'Unsupported format'),
              if (item.isBook && item.instanceName.isNotEmpty)
                _waitingChip('Library: ${item.instanceName}'),
              if (waited.isNotEmpty) _waitingChip('Waiting since $waited'),
              _waitingChip(lastTried),
            ],
          ),
        ],
      ),
    );
  }
}

Widget _waitingChip(String label) {
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

String _relativeTime(DateTime? date) {
  if (date == null) return '';
  final diff = DateTime.now().difference(date.toLocal());
  if (diff.inMinutes < 1) return 'just now';
  if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
  if (diff.inHours < 24) return '${diff.inHours}h ago';
  return '${diff.inDays}d ago';
}

class _PendingTile extends StatelessWidget {
  final PendingRequestItem item;
  final VoidCallback onApprove;
  final VoidCallback onDeny;
  final VoidCallback onWait;

  const _PendingTile({
    required this.item,
    required this.onApprove,
    required this.onDeny,
    required this.onWait,
  });

  /// What the artwork slot falls back to. A pending book has no cover to
  /// resolve, so its rows always land here.
  IconData get _placeholderIcon => switch (item.mediaType) {
        'tv' => Icons.live_tv,
        'book' => Icons.menu_book,
        _ => Icons.movie,
      };

  @override
  Widget build(BuildContext context) {
    final showScope = item.isTv && item.seasonScope.isNotEmpty;
    final showBookFormat = item.isBook && item.bookFormat.isNotEmpty;
    final detailRoute = item.detailRoute;
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      onTap: detailRoute == null ? null : () => context.push(detailRoute),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: item.posterPath.isEmpty
                ? null
                : AppConfig.tmdbPoster(item.posterPath, width: 185),
            fit: BoxFit.cover,
            icon: _placeholderIcon,
          ),
        ),
      ),
      title: Text(
        item.title,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.bold,
        ),
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 2),
          Text(
            item.requestedByLabel,
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          // Most rows are a plain yes/no and say nothing here. A row whose add
          // already failed is not one, and without this it looked identical —
          // so Approve got pressed, failed, and left no idea what to do next.
          if (item.addFailure case final failure?) ...[
            const SizedBox(height: 4),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Icon(Icons.error_outline,
                    size: 15, color: AppTheme.requested),
                const SizedBox(width: 5),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        failure.reason,
                        style: const TextStyle(
                          color: AppTheme.requested,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      Text(
                        failure.action,
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 12,
                          height: 1.3,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
            ),
          ],
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              _chip(item.mediaLabel),
              if (showScope) _chip(SeasonScope.describe(item.seasonScope)),
              if (showBookFormat)
                _chip(item.requestedBookFormat?.label ?? 'Unsupported format'),
              if (item.isBook && item.instanceName.isNotEmpty)
                _chip('Library: ${item.instanceName}'),
            ],
          ),
        ],
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          // A row here because its author-import wait ended isn't a yes/no:
          // approving just replays an add the library already refused, so the
          // honest verb is "try again" — resume the wait, or complete on the
          // spot if the author has landed since.
          if (item.isImportWait)
            IconButton(
              icon: const Icon(Icons.replay),
              color: AppTheme.requested,
              tooltip: 'Try again',
              onPressed: onWait,
            )
          else
            IconButton(
              icon: const Icon(Icons.check_circle_outline),
              color: AppTheme.available,
              tooltip: item.isBook && item.requestedBookFormat == null
                  ? 'Unsupported book format'
                  : 'Approve',
              onPressed: item.isBook && item.requestedBookFormat == null
                  ? null
                  : onApprove,
            ),
          IconButton(
            icon: const Icon(Icons.cancel_outlined),
            color: AppTheme.error,
            tooltip: 'Deny',
            onPressed: onDeny,
          ),
        ],
      ),
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
}
