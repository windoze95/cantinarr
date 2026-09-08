import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/phone_apps_sheet.dart';
import '../../../core/widgets/status_pill.dart';
import '../data/book_ownership.dart';
import '../data/request_service.dart';

/// The requester's per-format surface for one book: an eBook row and an
/// Audiobook row that each carry that format's live state *and* are the request
/// action for it. Tapping a still-open row submits exactly that format, so the
/// row a requester reads is the row they tap — there is no separate picker
/// restating the choice the rows already present.
class BookFormatPanel extends StatefulWidget {
  final String foreignId;
  final String title;
  final String? instanceId;

  /// The search text that surfaced this book, when it came from a search. The
  /// server needs it to re-find the exact metadata record at add time; a
  /// notification or deep-link arrival simply has none.
  final String? searchTerm;
  final RequestService service;
  final BookOwnership? ownership;
  final bool ownershipStatusKnown;
  final bool identityAmbiguous;
  final int refreshTick;

  /// Per-format download actions, supplied only for formats whose files the
  /// current user may pull. An available format is never requestable, so a
  /// download action and a request action never share a row.
  final Widget? ebookDownload;
  final Widget? audiobookDownload;

  final FutureOr<void> Function()? onRequestCompleted;

  /// Reports the library's own foreignBookId when the server resolved this
  /// book's request through a record Chaptarr filed under a different id.
  /// The owner may use it to refresh ownership; submission keeps the selected ID.
  final ValueChanged<String>? onCanonicalForeignId;

  const BookFormatPanel({
    super.key,
    required this.foreignId,
    required this.title,
    this.instanceId,
    this.searchTerm,
    required this.service,
    this.ownership,
    this.ownershipStatusKnown = true,
    this.identityAmbiguous = false,
    this.refreshTick = 0,
    this.ebookDownload,
    this.audiobookDownload,
    this.onRequestCompleted,
    this.onCanonicalForeignId,
  });

  @override
  State<BookFormatPanel> createState() => _BookFormatPanelState();
}

class _BookFormatPanelState extends State<BookFormatPanel> {
  // The async-loaded request state (no ownership). Ownership is layered on in
  // [_detail] on every read, so the rows reflect the owned-books digest even
  // when it loads AFTER this panel was first built — otherwise an owned-but-
  // unrequested format would offer a duplicate request action.
  BookRequestStatusDetail _serverDetail =
      const BookRequestStatusDetail(isKnown: false);
  bool _loading = true;
  bool _hasLiveDetail = false;
  bool _refreshFailed = false;
  List<Map<String, dynamic>> _delivery = const [];
  int _savedGeneration = 0;
  final Set<int> _actions = {};

  /// Formats with a request currently in flight. Kept per format — the eBook
  /// and Audiobook rows are independent actions, so submitting one must not
  /// dead-zone the other. Concurrent submissions for one title are safe: the
  /// server serializes them per canonical book behind its own lock.
  final Set<BookRequestFormat> _inFlight = {};
  String? _reportedCanonicalId;
  int _activeChecks = 0;
  int _checkGeneration = 0;
  Timer? _pendingRecheckTimer;

  /// Formats this panel submitted and the server confirmed, kept for as long as
  /// this book is on screen. The wait rides along with the status: a submission
  /// that came back waiting must not decay into a bare "Requested" while the
  /// status re-read is still in flight — that is the exact wording this panel
  /// exists to stop showing.
  final Map<BookRequestFormat, ({RequestStatus status, BookFormatWait? wait})>
      _submitted = {};

  bool get _checking => _activeChecks > 0;

  /// Server truth with two layers on top: the owned-books digest, and this
  /// panel's own confirmed submissions.
  ///
  /// A request Cantinarr accepted seconds ago can still read back as "not
  /// requested" until the arr record materialises, and re-offering Request there
  /// invites a duplicate request and reads as if the tap failed. A confirmed
  /// submission therefore outranks a later *unavailable* — but nothing else, so
  /// an admin's denial and every live state (downloading, available) still win.
  BookRequestStatusDetail get _detail {
    final detail = _serverDetail.withOwnership(
      widget.ownership,
      ownershipStatusKnown: widget.ownershipStatusKnown,
      identityAmbiguous: widget.identityAmbiguous,
    );
    final formats = {...detail.formats};
    final waits = {...detail.formatWaits};
    for (final d in _delivery) {
      final format =
          BookRequestFormat.tryFromValue(d['format'] as String? ?? '');
      if (format == null || format == BookRequestFormat.both) continue;
      final state = d['state'];
      if (state == 'complete') continue; // availability is read separately
      if ({RequestStatus.available, RequestStatus.downloading}
          .contains(detail.statusFor(format))) {
        continue;
      }
      if (state == 'cancelled') {
        formats[format] = RequestStatus.denied;
        waits.remove(format);
      } else if (state == 'approval') {
        formats[format] = RequestStatus.pending;
        waits.remove(format);
      } else {
        formats[format] = RequestStatus.requested;
        waits[format] = BookFormatWait(
            reason: BookWaitReason.fromValue(state == 'waiting_library'
                ? 'author_import'
                : state as String?));
      }
    }
    for (final entry in _submitted.entries) {
      final status = formats[entry.key];
      if (status != null && status != RequestStatus.unavailable) continue;
      formats[entry.key] = entry.value.status;
      if (entry.value.wait != null) waits[entry.key] = entry.value.wait!;
    }
    return BookRequestStatusDetail(
      status: detail.status,
      formats: formats,
      formatWaits: waits,
      ownership: detail.ownership,
      isKnown: detail.isKnown,
      unknownReason: detail.unknownReason,
    );
  }

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void didUpdateWidget(covariant BookFormatPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    // If this panel got reused for a different book, re-fetch its request state.
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.instanceId != widget.instanceId) {
      _loading = true;
      _hasLiveDetail = false;
      _reportedCanonicalId = null;
      _serverDetail = const BookRequestStatusDetail(isKnown: false);
      // Another book (or library) knows nothing about what was requested here.
      _submitted.clear();
      _delivery = const [];
      _refresh();
    } else if (oldWidget.refreshTick != widget.refreshTick &&
        _inFlight.isEmpty &&
        !_checking) {
      _refresh();
    } else if (oldWidget.ownership != widget.ownership ||
        oldWidget.ownershipStatusKnown != widget.ownershipStatusKnown) {
      _syncPendingRecheck();
    }
  }

  @override
  void dispose() {
    _pendingRecheckTimer?.cancel();
    super.dispose();
  }

  Future<void> _check() async {
    _activeChecks++;
    final foreignId = widget.foreignId;
    final generation = ++_checkGeneration;
    try {
      final detail = await widget.service.checkBookStatusDetail(
        foreignId,
        instanceId: widget.instanceId,
        title: widget.title,
        searchTerm: widget.searchTerm,
      );
      if (!mounted ||
          generation != _checkGeneration ||
          foreignId != widget.foreignId) {
        return;
      }
      setState(() {
        _refreshFailed = !detail.isKnown;
        if (detail.isKnown ||
            !_hasLiveDetail ||
            detail.effectiveUnknownReason !=
                BookStatusUnknownReason.transient) {
          _serverDetail = detail;
          _hasLiveDetail = true;
        }
        _loading = false;
      });
      _syncPendingRecheck();
      final canonical = detail.canonicalForeignId ?? '';
      if (detail.isKnown ||
          detail.effectiveUnknownReason ==
              BookStatusUnknownReason.identityNeedsAttention) {
        final nextId =
            !detail.isKnown || canonical.isEmpty ? widget.foreignId : canonical;
        if (nextId != (_reportedCanonicalId ?? widget.foreignId)) {
          _reportedCanonicalId = nextId;
          widget.onCanonicalForeignId?.call(nextId);
        }
      }
    } finally {
      _activeChecks--;
    }
  }

  void _refresh() {
    unawaited(_checkSaved());
    unawaited(_check());
  }

  Future<void> _checkSaved() async {
    final generation = ++_savedGeneration;
    final data = await widget.service
        .bookDeliveryStatus(widget.foreignId, instanceId: widget.instanceId);
    if (!mounted || generation != _savedGeneration || data == null) return;
    setState(() {
      _delivery = ((data['delivery'] as List?) ?? [])
          .map((d) => Map<String, dynamic>.from(d as Map))
          .toList();
      _loading = !_hasLiveDetail;
    });
    _syncPendingRecheck();
  }

  Future<void> _deliveryAction(int id, String action, {String? format}) async {
    if (!_actions.add(id)) return;
    setState(() {});
    try {
      final data =
          await widget.service.bookDeliveryAction(id, action, format: format);
      if (!mounted) return;
      _savedGeneration++;
      setState(() {
        final updated = ((data['delivery'] as List?) ?? [])
            .map((d) => Map<String, dynamic>.from(d as Map))
            .toList();
        _delivery = [
          ..._delivery.where((d) => d['request_id'] != id),
          ...updated
        ];
        if (action == 'cancel') {
          for (final d in updated) {
            final format =
                BookRequestFormat.tryFromValue(d['format'] as String? ?? '');
            if (format != null && d['state'] == 'cancelled') {
              _submitted.remove(format);
            }
          }
        }
      });
      _announce(action == 'cancel'
          ? 'Request cancelled.'
          : 'Your request is saved for another delivery attempt.');
      _refresh();
    } on RequestSubmissionException catch (e) {
      if (mounted) _announce(e.message);
    } finally {
      if (mounted) setState(() => _actions.remove(id));
    }
  }

  void _syncPendingRecheck() {
    // A wait polls on the same timer as an approval: both end without the
    // requester doing anything, and a waiting row is the only notice the owner
    // gets that the library caught up — a system completion deliberately sends
    // them no push, because nobody decided anything.
    final hasPending = _detail.hasFormatWait ||
        [
          BookRequestFormat.ebook,
          BookRequestFormat.audiobook,
        ].any((format) => _detail.statusFor(format) == RequestStatus.pending);
    if (!hasPending) {
      _pendingRecheckTimer?.cancel();
      _pendingRecheckTimer = null;
      return;
    }
    _pendingRecheckTimer ??= Timer.periodic(
      const Duration(seconds: 30),
      (_) {
        if (mounted && _inFlight.isEmpty && !_checking) _refresh();
      },
    );
  }

  /// Submits exactly the tapped [format]. Every outcome is named back to the
  /// requester: a tap that changed nothing visible would leave them guessing
  /// whether it registered at all.
  Future<void> _request(BookRequestFormat format) async {
    final selected = format == BookRequestFormat.both
        ? [BookRequestFormat.ebook, BookRequestFormat.audiobook]
        : [format];
    if (selected.any(_inFlight.contains) || !_canRequest(_detail, format)) {
      return;
    }
    _savedGeneration++; // ignore reads started before this submission
    _checkGeneration++;
    setState(() => _inFlight.addAll(selected));
    try {
      BookRequestSubmission? submission;
      String? failureMessage;
      var definitiveFailure = false;
      try {
        submission = await widget.service.requestBook(
          foreignId: widget.foreignId,
          title: widget.title,
          format: format,
          instanceId: widget.instanceId,
          searchTerm: widget.searchTerm,
        );
      } on RequestSubmissionException catch (e) {
        failureMessage = e.message;
        definitiveFailure = e.definitive;
      }
      if (!mounted) return;
      if (submission == null) {
        await _refreshAfterSubmission();
        if (!mounted) return;
        // The refresh just told us whether the tapped format landed despite the
        // failed call (a server error can postdate a durable add). When it did,
        // announce that truth — the rows already show it, and a failure toast
        // beside a Requested row would contradict the screen.
        final landed = _detail.statusFor(format);
        final covered = landed == RequestStatus.requested ||
            landed == RequestStatus.downloading ||
            landed == RequestStatus.available ||
            landed == RequestStatus.pending;
        if (covered) {
          _announce(_formatOutcome(format, landed));
          return;
        }
        _announce(definitiveFailure && failureMessage != null
            ? failureMessage
            : 'The request outcome couldn’t be confirmed. The book status was '
                'refreshed.');
        return;
      }
      if (!submission.isKnown) {
        await _refreshAfterSubmission();
        if (!mounted) return;
        _announce(
          'The request was sent, but its result could not be confirmed. The '
          'book status was refreshed.',
        );
        return;
      }
      final resolved = submission.formats[format] ?? submission.status;
      // A server explanation outranks the generic status line: "pending
      // approval" would read as a policy hold when the real reason is that the
      // book couldn't be matched and is waiting on an admin to add it.
      final outcome = submission.message.isNotEmpty
          ? submission.message
          : _formatOutcome(format, resolved);
      for (final selectedFormat in selected) {
        _rememberSubmission(
            selectedFormat,
            submission.formats[selectedFormat] ?? resolved,
            submission.formatWaits[selectedFormat]);
      }
      setState(() {
        final ids = submission!.delivery.map((d) => d['request_id']).toSet();
        _delivery = [
          ..._delivery.where((d) => !ids.contains(d['request_id'])),
          ...submission.delivery
        ];
      });
      _announce(outcome);
      _syncPendingRecheck();
      _refresh();
      unawaited(Future<void>.sync(() async {
        await widget.onRequestCompleted?.call();
      }));
      if (submission.succeeded(format)) {
        unawaited(maybeShowPhoneAppsPrompt(context));
      }
    } finally {
      if (mounted) {
        setState(() => _inFlight.removeAll(selected));
      } else {
        _inFlight.removeAll(selected);
      }
    }
  }

  Future<void> _refreshAfterSubmission() async {
    // Re-pull per-format truth before re-enabling the rows so a fast second tap
    // cannot submit the same format against stale pre-request state.
    await _check();
    if (!mounted) return;
    // Parent ownership/live-record invalidation can change [refreshTick]. The
    // submitting format stays in [_inFlight] while it runs; didUpdateWidget
    // suppresses a redundant status check until this accepted refresh has
    // fully completed.
    await widget.onRequestCompleted?.call();
  }

  /// Records a submission the server confirmed. An outcome that did not land —
  /// a denial, or a format the server refused — is never recorded, so it stays
  /// retryable.
  void _rememberSubmission(
    BookRequestFormat format,
    RequestStatus? status,
    BookFormatWait? wait,
  ) {
    if (status == null ||
        status == RequestStatus.unavailable ||
        status == RequestStatus.denied) {
      return;
    }
    setState(() {
      _submitted[format] = (
        status:
            status == RequestStatus.partial ? RequestStatus.requested : status,
        wait: wait,
      );
    });
  }

  void _announce(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message)),
    );
  }

  String _formatOutcome(BookRequestFormat format, RequestStatus? status) =>
      switch (status) {
        RequestStatus.available => '${format.label} is available.',
        RequestStatus.downloading => '${format.label} is downloading.',
        RequestStatus.requested ||
        RequestStatus.partial =>
          '${format.label} requested.',
        RequestStatus.pending => '${format.label} is pending approval.',
        RequestStatus.denied => '${format.label} was not approved.',
        RequestStatus.unavailable ||
        null =>
          '${format.label} could not be requested. Try again.',
      };

  bool _canRequest(BookRequestStatusDetail detail, BookRequestFormat format) {
    if (format != BookRequestFormat.both) return detail.isRequestable(format);
    // One owned or already requested format does not block requesting the pair.
    // The worker leaves that format alone and delivers the missing one.
    const formats = [BookRequestFormat.ebook, BookRequestFormat.audiobook];
    return formats.any(detail.isRequestable) &&
        formats.every((f) =>
            detail.isRequestable(f) ||
            {
              RequestStatus.available,
              RequestStatus.downloading,
              RequestStatus.requested,
              RequestStatus.pending
            }.contains(detail.statusFor(f)));
  }

  @override
  Widget build(BuildContext context) {
    final detail = _detail;
    final cancellable = <int>{
      for (final d in _delivery)
        if (!{'complete', 'cancelled'}.contains(d['state']) &&
            d['can_cancel'] != false)
          d['request_id'] as int,
    };
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // A Material (not a decorated box) so a row tap paints its ripple on
        // top of the card instead of behind its opaque surface colour.
        Material(
          color: AppTheme.surface,
          clipBehavior: Clip.antiAlias,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
            side: const BorderSide(color: AppTheme.border),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 14, 16, 10),
                child: Text('Formats',
                    style: Theme.of(context).textTheme.titleSmall),
              ),
              const Divider(height: 1, color: AppTheme.border),
              _row(
                detail,
                format: BookRequestFormat.ebook,
                icon: Icons.menu_book,
                download: widget.ebookDownload,
              ),
              const Divider(height: 1, indent: 52, color: AppTheme.border),
              _row(
                detail,
                format: BookRequestFormat.audiobook,
                icon: Icons.headphones,
                download: widget.audiobookDownload,
              ),
              const Divider(height: 1, color: AppTheme.border),
              Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    key: const ValueKey('book-request-both'),
                    onPressed: !_loading &&
                            _inFlight.isEmpty &&
                            _canRequest(detail, BookRequestFormat.both)
                        ? () => _request(BookRequestFormat.both)
                        : null,
                    icon: const Icon(Icons.add),
                    label: const Text('Request both'),
                  )),
              for (final d in _delivery)
                if (!{'complete', 'cancelled'}.contains(d['state']))
                  Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      child: Wrap(spacing: 12, children: [
                        if ({'attention', 'retry'}.contains(d['state']) &&
                            d['can_manage'] != false &&
                            d['code'] != 'catalog_retired')
                          TextButton(
                              onPressed: _actions.contains(d['request_id'])
                                  ? null
                                  : () => _deliveryAction(
                                      d['request_id'] as int, 'retry',
                                      format: d['format'] as String),
                              child: Text(
                                  'Retry ${d['format'] == 'ebook' ? 'eBook' : 'audiobook'}')),
                      ])),
              for (final id in cancellable)
                Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 16),
                    child: Align(
                        alignment: Alignment.centerLeft,
                        child: TextButton(
                            onPressed: _actions.contains(id)
                                ? null
                                : () => _deliveryAction(id, 'cancel'),
                            child: const Text('Cancel request')))),
            ],
          ),
        ),
        // Stated in full, every time, not as a one-shot toast at submission.
        // The toast is what the requester in the original report saw and could
        // not get back; "Requested" was all the app said from then on.
        if (_waitExplanation(detail) case final explanation?)
          Padding(
            padding: const EdgeInsets.only(top: 10, left: 4, right: 4),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Icon(Icons.schedule_rounded,
                    size: 18, color: AppTheme.requested),
                const SizedBox(width: 6),
                Expanded(
                  child: Text(
                    explanation,
                    style: const TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                      height: 1.35,
                    ),
                  ),
                ),
              ],
            ),
          ),
        if (detail.effectiveUnknownReason ==
                BookStatusUnknownReason.formatNeedsAttention ||
            detail.effectiveUnknownReason ==
                BookStatusUnknownReason.identityNeedsAttention)
          Padding(
            padding: const EdgeInsets.only(top: 10, left: 4, right: 4),
            child: Row(
              children: [
                const Icon(Icons.warning_amber_rounded,
                    size: 18, color: AppTheme.requested),
                const SizedBox(width: 6),
                Flexible(
                  child: Text(
                    detail.effectiveUnknownReason ==
                            BookStatusUnknownReason.identityNeedsAttention
                        ? 'Ask an admin to check this book’s library match'
                        : 'Ask an admin to check this book’s format',
                    style: const TextStyle(
                      color: AppTheme.requested,
                      fontSize: 12,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ],
            ),
          )
        else if (_refreshFailed ||
            detail.effectiveUnknownReason == BookStatusUnknownReason.transient)
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              onPressed: _inFlight.isNotEmpty || _checking ? null : _refresh,
              icon: const Icon(Icons.refresh_rounded, size: 18),
              label: const Text('Couldn’t check · Retry'),
            ),
          ),
      ],
    );
  }

  /// One explanation for the panel, not one per row: both formats of a book
  /// wait on the same author import, and saying it twice reads as two problems.
  /// Two genuinely different reasons are named separately.
  String? _waitExplanation(BookRequestStatusDetail detail) {
    final reasons = <BookWaitReason, String>{};
    for (final format in [
      BookRequestFormat.ebook,
      BookRequestFormat.audiobook,
    ]) {
      final wait = detail.waitFor(format);
      if (wait != null) reasons[wait.reason] = wait.explanation;
    }
    if (reasons.isEmpty) return null;
    return reasons.values.join('\n\n');
  }

  Widget _row(
    BookRequestStatusDetail detail, {
    required BookRequestFormat format,
    required IconData icon,
    required Widget? download,
  }) {
    // A waiting format is covered, so isRequestable already refuses it: the
    // request is saved and re-requesting would only make a second row for the
    // same wait.
    final requestable = !_loading && detail.isRequestable(format);
    // A denied format keeps its verdict visible; any other requestable row is
    // fully described by its action, so it carries no status pill as well.
    final denied = detail.statusFor(format) == RequestStatus.denied;
    return _FormatRow(
      rowKey: ValueKey('book-format-row:${format.value}'),
      icon: icon,
      label: format.label,
      state: requestable && !denied
          ? null
          : _formatState(detail, format, !_loading),
      action: _inFlight.contains(format)
          ? const _SubmittingIndicator()
          : requestable
              ? _RequestAction(denied ? 'Request again' : 'Request')
              : null,
      download: download,
      onTap: requestable && !_inFlight.contains(format)
          ? () => _request(format)
          : null,
    );
  }
}

class _FormatRow extends StatelessWidget {
  final Key rowKey;
  final IconData icon;
  final String label;
  final ({String label, Color color})? state;
  final Widget? action;
  final Widget? download;
  final VoidCallback? onTap;

  const _FormatRow({
    required this.rowKey,
    required this.icon,
    required this.label,
    required this.state,
    required this.action,
    required this.download,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final status = state;
    // Long status text, narrow screens, and large accessibility text all lose
    // the single-line layout; stack the state under the format name instead of
    // squeezing both onto one line.
    final stack = MediaQuery.textScalerOf(context).scale(1) > 1.3 ||
        MediaQuery.sizeOf(context).width < 360 ||
        (status != null && status.label.length > 18);
    final pill = status == null
        ? null
        : StatusPill(text: status.label, color: status.color);
    final heading = Row(
      children: [
        Icon(icon, color: AppTheme.accent, size: 20),
        const SizedBox(width: 12),
        Expanded(
          child: Text(label, style: Theme.of(context).textTheme.titleSmall),
        ),
      ],
    );
    return InkWell(
      key: rowKey,
      onTap: onTap,
      child: MergeSemantics(
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          child: stack
              ? Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    heading,
                    const SizedBox(height: 8),
                    Padding(
                      padding: const EdgeInsets.only(left: 28),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          ConstrainedBox(
                            constraints: const BoxConstraints(minHeight: 48),
                            child: Row(
                              children: [
                                // Bounded so a long state wraps instead of
                                // running off a narrow or scaled-up row.
                                if (pill != null)
                                  Flexible(child: pill)
                                else if (action != null)
                                  Flexible(child: action!),
                                // Reserve width too: otherwise a late button
                                // can wrap the status onto a new line.
                                const SizedBox(width: 4),
                                SizedBox(
                                    width: 48, height: 48, child: download),
                              ],
                            ),
                          ),
                          if (pill != null && action != null) ...[
                            const SizedBox(height: 8),
                            action!,
                          ],
                        ],
                      ),
                    ),
                  ],
                )
              : ConstrainedBox(
                  // Reserve the download button's touch target before its
                  // file lookup finishes, keeping both format rows steady.
                  constraints: const BoxConstraints(minHeight: 48),
                  child: Row(children: [
                    Expanded(child: heading),
                    if (pill != null) pill,
                    if (action != null) ...[
                      if (pill != null) const SizedBox(width: 8),
                      action!,
                    ],
                    if (download != null) ...[
                      const SizedBox(width: 4),
                      download!,
                    ],
                  ]),
                ),
        ),
      ),
    );
  }
}

/// The call to action a still-open format row carries. It is painted, not
/// tapped: the whole row is the target, so the label and the gesture agree.
class _RequestAction extends StatelessWidget {
  final String label;

  const _RequestAction(this.label);

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.fromLTRB(8, 5, 10, 5),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(999),
        border: Border.all(color: AppTheme.accent.withValues(alpha: 0.45)),
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.add_rounded, size: 15, color: AppTheme.accent),
          const SizedBox(width: 4),
          Flexible(
            child: Text(
              label,
              style: const TextStyle(
                color: AppTheme.accent,
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _SubmittingIndicator extends StatelessWidget {
  const _SubmittingIndicator();

  @override
  Widget build(BuildContext context) {
    return const Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        SizedBox(
          width: 14,
          height: 14,
          child:
              CircularProgressIndicator(strokeWidth: 2, color: AppTheme.accent),
        ),
        SizedBox(width: 8),
        Flexible(
          child: Text(
            'Requesting…',
            style: TextStyle(
              color: AppTheme.accent,
              fontSize: 12,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    );
  }
}

({String label, Color color}) _formatState(
  BookRequestStatusDetail detail,
  BookRequestFormat format,
  bool statusLoaded,
) {
  final ownership = detail.ownership;
  final owned = switch (format) {
    BookRequestFormat.ebook => ownership?.ebook,
    BookRequestFormat.audiobook => ownership?.audiobook,
    BookRequestFormat.both => null,
  };
  if (owned?.downloaded ?? false) {
    return (label: 'Available', color: AppTheme.available);
  }

  if (owned?.monitored ?? false) {
    return (label: 'Requested', color: AppTheme.requested);
  }

  // Checked before the plain status words below, because the server hands this
  // state over as "requested" for the benefit of clients that know no other
  // word. Requested claims a monitored library record; this row has none yet.
  final wait = detail.waitFor(format);
  if (wait != null) {
    return (label: wait.label, color: AppTheme.requested);
  }

  final status = detail.statusFor(format);
  if (status != null && status != RequestStatus.unavailable) {
    return switch (status) {
      RequestStatus.available => (
          label: 'Available',
          color: AppTheme.available
        ),
      RequestStatus.pending => (
          label: 'Pending Approval',
          color: AppTheme.requested
        ),
      RequestStatus.requested => (
          label: 'Requested',
          color: AppTheme.requested
        ),
      RequestStatus.downloading => (
          label: 'Downloading',
          color: AppTheme.downloading
        ),
      RequestStatus.partial => (label: 'Requested', color: AppTheme.requested),
      RequestStatus.denied => (label: 'Request Denied', color: AppTheme.error),
      RequestStatus.unavailable => throw StateError('unreachable'),
    };
  }

  // Until request history resolves, an empty ownership row does not prove the
  // format was never requested. Keep the neutral loading state instead of
  // briefly claiming "Not requested" for a pending or denied request.
  if (!statusLoaded) {
    return (label: 'Checking…', color: AppTheme.textSecondary);
  }

  if (!detail.isKnown) {
    if (detail.effectiveUnknownReason ==
        BookStatusUnknownReason.identityNeedsAttention) {
      return (
        label: 'Library match needs attention',
        color: AppTheme.requested
      );
    }
    return detail.effectiveUnknownReason ==
            BookStatusUnknownReason.formatNeedsAttention
        ? (label: 'Format needs attention', color: AppTheme.requested)
        : (label: 'Couldn’t check', color: AppTheme.error);
  }
  if (status == RequestStatus.unavailable) {
    return (label: 'Not requested', color: AppTheme.textSecondary);
  }
  return (label: 'Couldn’t check', color: AppTheme.error);
}
