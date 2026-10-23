import '../../discover/ui/browse_labels.dart';
import 'settings_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import '../../request/ui/catalog_request_panel.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import '../../request/data/request_quota.dart';
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
  final Map<int, ({String scope, int? profile})> _approvalSelections = {};

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
    final quota = requestQuotaError(e);
    if (quota != null) return quota;
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
      return context.l10n.settingsThisOlderRequestDoesnTIdentifyABook;
    }
    if (lower.contains('root folder') ||
        lower.contains('quality profile') ||
        lower.contains('metadata profile') ||
        lower.contains('book configuration')) {
      return context.l10n.settingsCheckThisBookLibrarySPathsAndProfiles;
    }
    // Approving replayed an add that had already failed the same way. The old
    // "Something went wrong. Try again." read as a transient glitch and invited
    // another Approve, which cannot work until the library has the record.
    if (lower.contains('add this book in the library first')) {
      return context.l10n.settingsTheLibraryStillCanTFindThisBook;
    }
    return context.l10n.settingsSomethingWentWrongTryAgain;
  }

  String _approvalMessage(
    PendingRequestItem item,
    BookApprovalResult result,
  ) {
    if (!item.isBook)
      return context.l10n
          .settingsMessageApprovedValue1((item.title).toString());
    if (!result.isKnown || result.formats.isEmpty) {
      return context.l10n.settingsApprovalSavedTheRemainingQueueWasRefreshed;
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
          approved.add(context.l10n.settingsMessageValue1Approved(
              (browseLabel(context, format.label)).toString()));
        case RequestStatus.pending:
          attention.add(context.l10n.settingsMessageValue1StillNeedsAttention(
              (browseLabel(context, format.label)).toString()));
        case RequestStatus.denied:
        case RequestStatus.unavailable:
          attention.add(result.status == RequestStatus.partial
              ? context.l10n.settingsMessageValue1StillNeedsAttention(
                  (browseLabel(context, format.label)).toString())
              : context.l10n.settingsMessageValue1CouldNotBeApproved(
                  (browseLabel(context, format.label)).toString()));
      }
    }
    final message = [...approved, ...attention].join(' ');
    return message.isEmpty
        ? context.l10n.settingsApprovalSavedTheRemainingQueueWasRefreshed
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
        SnackBar(
          content: Text(
            context.l10n.settingsThisRequestUsesAnUnsupportedBookFormatAnd,
          ),
        ),
      );
      return;
    }
    // Books and music resolve deterministic profile/root settings at add
    // time, so their approvals carry no quality choice.
    final profiles = item.isBook || item.isMusic
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
    chosenScope = _approvalSelections[item.id]?.scope ?? chosenScope;
    int? chosenProfile = _approvalSelections[item.id]?.profile;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        return StatefulBuilder(
          builder: (dialogContext, setDialogState) {
            return AlertDialog(
              backgroundColor: AppTheme.surface,
              title: Text(
                context.l10n
                    .settingsMessageApproveValue1((item.title).toString()),
                style: const TextStyle(color: AppTheme.textPrimary),
              ),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text(
                    settingsPendingRequestedBy(context, item),
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 13,
                    ),
                  ),
                  const SizedBox(height: 16),
                  if (item.isTv) ...[
                    Text(
                      context.l10n.settingsSeasonScope,
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
                            child: Text(context.l10n
                                .settingsMessageKeepRequestedValue1(
                                    (browseLabel(
                                            context,
                                            SeasonScope.describe(
                                                item.seasonScope)))
                                        .toString())),
                          ),
                        ...SeasonScope.choices.map((c) =>
                            DropdownMenuItem<String>(
                                value: c.value,
                                child: Text(browseLabel(context, c.label)))),
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
                    Text(
                      context.l10n.settingsRequestedFormat,
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    Text(
                      browseLabel(context, requestedBookFormat!.label),
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 16,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                    if (item.instanceName.isNotEmpty) ...[
                      const SizedBox(height: 14),
                      Text(
                        context.l10n.settingsMessageLibraryValue1(
                            (item.instanceName).toString()),
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 14,
                        ),
                      ),
                    ],
                  ] else ...[
                    Text(
                      context.l10n.settingsQualityProfile,
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
                        DropdownMenuItem<int?>(
                          value: null,
                          child: Text(context.l10n.settingsDefault),
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
                  child: Text(context.l10n.settingsCancel),
                ),
                ElevatedButton(
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppTheme.available,
                    foregroundColor: AppTheme.background,
                  ),
                  onPressed: () => Navigator.of(dialogContext).pop(true),
                  child: Text(context.l10n.settingsApprove),
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
      _approvalSelections[item.id] =
          (scope: chosenScope, profile: chosenProfile);
      final result = await _service.approve(
        item.id,
        // The "keep requested" sentinel sends no override, so the server keeps
        // the explicit season list the user picked.
        seasonScope: item.isTv
            ? (chosenScope == _keepRequestedScope ? null : chosenScope)
            : null,
        qualityProfileId: item.isBook || item.isMusic ? null : chosenProfile,
      );
      if (!mounted) return;
      _approvalSelections.remove(item.id);
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
              : context.l10n
                  .settingsMessageTheLibraryHasThisAuthorNowValue1Went(
                      (item.title).toString())),
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
            context.l10n.settingsMessageDenyValue1((item.title).toString()),
            style: const TextStyle(color: AppTheme.textPrimary),
          ),
          content: TextField(
            controller: controller,
            autofocus: true,
            style: const TextStyle(color: AppTheme.textPrimary),
            decoration: InputDecoration(
              labelText: context.l10n.settingsReasonOptional,
              labelStyle: TextStyle(color: AppTheme.textSecondary),
              border: OutlineInputBorder(),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: Text(context.l10n.settingsCancel),
            ),
            ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppTheme.error,
                foregroundColor: AppTheme.background,
              ),
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: Text(context.l10n.settingsDeny),
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
        SnackBar(
            content: Text(context.l10n
                .settingsMessageDeniedValue1((item.title).toString()))),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  /// Approval decisions, followed by saved delivery progress and recovery.
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
      return [
        SizedBox(height: 120),
        Center(
          child: Text(
            context.l10n.settingsNoPendingRequests,
            style: TextStyle(color: AppTheme.textSecondary),
          ),
        ),
      ];
    }

    if (pending.isNotEmpty) {
      children.add(_SectionHeader(
        title: context.l10n.settingsNeedsApproval,
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
      children.add(_SectionHeader(
        title: context.l10n.settingsSavedRequests,
        caption:
            context.l10n.settingsDeliveryProgressAndRequestsThatNeedAttention,
      ));
      if (_waitingBlind) {
        children.add(Padding(
          padding: EdgeInsets.fromLTRB(16, 4, 16, 12),
          child: Text(
            context.l10n.settingsCouldnTCheckWhatTheServerIsRetrying,
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
      appBar: AppBar(
        title: Text(context.l10n.settingsApprovals),
        actions: [
          TextButton.icon(
            onPressed: () => context.push('/approvals/history'),
            icon: const Icon(Icons.history),
            label: Text(context.l10n.settingsHistory),
          ),
          const SizedBox(width: 8),
        ],
      ),
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
                                    child: Text(context.l10n.settingsRetry)),
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
    final waited = _relativeTime(context, item.requestedAt);
    // Absent is not "never tried" — the server restarted and cannot vouch for
    // the attempt its predecessor made. Say that, rather than leaving a blank
    // that reads as nothing having happened.
    final lastTried = item.lastAttemptAt == null
        ? context.l10n.settingsLastAttemptUnknown
        : context.l10n.settingsMessageLastTriedValue1(
            (_relativeTime(context, item.lastAttemptAt)).toString());
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
            icon: item.isBook
                ? Icons.menu_book
                : item.isMusic
                    ? Icons.album
                    : Icons.movie,
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
            settingsPendingRequestedBy(context, item),
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 4),
          if (item.delivery.isEmpty)
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Icon(Icons.schedule_rounded,
                    size: 15, color: AppTheme.requested),
                const SizedBox(width: 5),
                Expanded(
                  child: Text(
                    (item.waitDescription == null
                            ? null
                            : settingsModelLabel(
                                context, item.waitDescription!)) ??
                        context.l10n.settingsWaitingForTheLibrary,
                    style: const TextStyle(
                      color: AppTheme.requested,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ],
            ),
          if (item.delivery.isNotEmpty)
            CatalogRequestPanel(
                mediaType: item.mediaType,
                foreignId: item.catalogProvider == 'openlibrary'
                    ? 'ol:${item.catalogId}'
                    : item.foreignId,
                title: item.title,
                instanceId: item.instanceId,
                provider: item.catalogProvider,
                sourceId: item.catalogId,
                requestId: item.id,
                progressOnly: true),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              _waitingChip(settingsModelLabel(context, item.mediaLabel)),
              if (item.isBook && item.bookFormat.isNotEmpty)
                _waitingChip((item.requestedBookFormat == null
                        ? null
                        : browseLabel(
                            context, item.requestedBookFormat!.label)) ??
                    context.l10n.settingsUnsupportedFormat),
              if (item.isBook && item.instanceName.isNotEmpty)
                _waitingChip(context.l10n.settingsMessageLibraryValue1(
                    (item.instanceName).toString())),
              if (waited.isNotEmpty)
                _waitingChip(context.l10n
                    .settingsMessageWaitingSinceValue1((waited).toString())),
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

String _relativeTime(BuildContext context, DateTime? date) {
  if (date == null) return '';
  final diff = DateTime.now().difference(date.toLocal());
  if (diff.inMinutes < 1) return context.l10n.settingsJustNow;
  if (diff.inMinutes < 60)
    return context.l10n.settingsMessageValue1MAgo((diff.inMinutes).toString());
  if (diff.inHours < 24)
    return context.l10n.settingsMessageValue1HAgo((diff.inHours).toString());
  return context.l10n.settingsMessageValue1DAgo((diff.inDays).toString());
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
        'music' => Icons.album,
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
            settingsPendingRequestedBy(context, item),
            style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
          ),
          // Most rows are a plain yes/no and say nothing here. A row whose add
          // already failed is not one, and without this it looked identical —
          // so Approve got pressed, failed, and left no idea what to do next.
          if (item.isCatalogRetired) ...[
            const SizedBox(height: 4),
            Text(context.l10n.settingsNeedsAttention,
                style: TextStyle(color: AppTheme.requested)),
            CatalogRequestPanel(
                mediaType: item.mediaType,
                foreignId: 'ol:${item.catalogId}',
                title: item.title,
                instanceId: item.instanceId,
                provider: item.catalogProvider,
                sourceId: item.catalogId,
                requestId: item.id,
                progressOnly: true),
          ],
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
                        settingsModelLabel(context, failure.reason),
                        style: const TextStyle(
                          color: AppTheme.requested,
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                        ),
                      ),
                      Text(
                        settingsModelLabel(context, failure.action),
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
              _chip(settingsModelLabel(context, item.mediaLabel)),
              if (showScope)
                _chip(browseLabel(
                    context, SeasonScope.describe(item.seasonScope))),
              if (showBookFormat)
                _chip((item.requestedBookFormat == null
                        ? null
                        : browseLabel(
                            context, item.requestedBookFormat!.label)) ??
                    context.l10n.settingsUnsupportedFormat),
              if ((item.isBook || item.isMusic) && item.instanceName.isNotEmpty)
                _chip(context.l10n.settingsMessageLibraryValue1(
                    (item.instanceName).toString())),
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
          if (item.isCatalogRetired)
            const SizedBox.shrink()
          else if (item.isImportWait)
            IconButton(
              icon: const Icon(Icons.replay),
              color: AppTheme.requested,
              tooltip: context.l10n.settingsTryAgain,
              onPressed: onWait,
            )
          else
            IconButton(
              icon: const Icon(Icons.check_circle_outline),
              color: AppTheme.available,
              tooltip: item.isBook && item.requestedBookFormat == null
                  ? context.l10n.settingsUnsupportedBookFormat
                  : context.l10n.settingsApprove,
              onPressed: item.isBook && item.requestedBookFormat == null
                  ? null
                  : onApprove,
            ),
          IconButton(
            icon: const Icon(Icons.cancel_outlined),
            color: AppTheme.error,
            tooltip: context.l10n.settingsDeny,
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
