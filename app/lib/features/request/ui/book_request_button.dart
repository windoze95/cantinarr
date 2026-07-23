import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../data/book_ownership.dart';
import '../data/request_service.dart';

/// The exact book and format chosen by an optional request picker.
class BookRequestTarget {
  final String foreignId;
  final String title;
  final BookRequestFormat format;
  final BookRequestSelection? selection;

  const BookRequestTarget({
    required this.foreignId,
    required this.title,
    required this.format,
    this.selection,
  });
}

/// Current request truth supplied to a custom picker. The dashboard uses this
/// to add a catalog-match step without duplicating submission or reconciliation.
class BookRequestPickerContext {
  final String foreignId;
  final String title;
  final String? instanceId;
  final BookRequestStatusDetail detail;
  final List<BookRequestFormat> requestableFormats;

  const BookRequestPickerContext({
    required this.foreignId,
    required this.title,
    required this.instanceId,
    required this.detail,
    required this.requestableFormats,
  });
}

typedef BookRequestTargetPicker = Future<BookRequestTarget?> Function(
  BuildContext context,
  BookRequestPickerContext request,
);

/// Per-book request affordance shared by the Books search tab and the
/// requester book detail screen: loads the user's request state on build, and
/// on tap submits a request (which may land as pending when approval is
/// required).
class BookRequestButton extends StatefulWidget {
  final String foreignId;
  final String title;
  final String? instanceId;
  final RequestService service;
  final BookOwnership? ownership;
  final bool ownershipStatusKnown;
  final int refreshTick;
  final bool showCoveredStatus;
  final ValueChanged<BookRequestStatusDetail>? onDetailChanged;
  final FutureOr<void> Function()? onRequestCompleted;
  final BookRequestTargetPicker? requestTargetPicker;

  const BookRequestButton({
    super.key,
    required this.foreignId,
    required this.title,
    this.instanceId,
    required this.service,
    this.ownership,
    this.ownershipStatusKnown = true,
    this.refreshTick = 0,
    this.showCoveredStatus = false,
    this.onDetailChanged,
    this.onRequestCompleted,
    this.requestTargetPicker,
  });

  @override
  State<BookRequestButton> createState() => _BookRequestButtonState();
}

class _BookRequestButtonState extends State<BookRequestButton> {
  static const _outcomeRecheckDelays = [
    Duration.zero,
    Duration(milliseconds: 500),
    Duration(milliseconds: 1500),
  ];

  // The async-loaded request state (no ownership). Ownership is layered on in
  // [_detail] on every read, so the button reflects the owned-books digest even
  // when it loads AFTER this button was first built (the chip already does) —
  // otherwise an owned-but-unrequested format reads as "Request", not
  // "Request more".
  BookRequestStatusDetail _serverDetail = const BookRequestStatusDetail();
  bool _loading = true;
  bool _busy = false;
  bool _submitting = false;
  int _activeChecks = 0;
  int _checkGeneration = 0;
  Timer? _pendingRecheckTimer;

  bool get _checking => _activeChecks > 0;

  BookRequestStatusDetail get _detail =>
      _serverDetail.withOwnership(
        widget.ownership,
        ownershipStatusKnown: widget.ownershipStatusKnown,
      );

  @override
  void initState() {
    super.initState();
    _check();
  }

  @override
  void didUpdateWidget(covariant BookRequestButton oldWidget) {
    super.didUpdateWidget(oldWidget);
    // If this row got reused for a different book, re-fetch its request state.
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.instanceId != widget.instanceId) {
      _loading = true;
      _serverDetail = const BookRequestStatusDetail();
      _check();
    } else if (oldWidget.refreshTick != widget.refreshTick && !_busy) {
      _check();
    } else if (oldWidget.ownership != widget.ownership ||
        oldWidget.ownershipStatusKnown != widget.ownershipStatusKnown) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) {
          _syncPendingRecheck();
          widget.onDetailChanged?.call(_detail);
        }
      });
    }
  }

  @override
  void dispose() {
    _pendingRecheckTimer?.cancel();
    super.dispose();
  }

  Future<BookRequestStatusDetail?> _check() async {
    _activeChecks++;
    final foreignId = widget.foreignId;
    final generation = ++_checkGeneration;
    try {
      final detail = await widget.service.checkBookStatusDetail(
        foreignId,
        instanceId: widget.instanceId,
      );
      if (!mounted ||
          generation != _checkGeneration ||
          foreignId != widget.foreignId) {
        return null;
      }
      setState(() {
        _serverDetail = detail;
        _loading = false;
      });
      _syncPendingRecheck();
      widget.onDetailChanged?.call(_detail);
      return detail;
    } finally {
      _activeChecks--;
    }
  }

  void _syncPendingRecheck() {
    final hasPending = [
      BookRequestFormat.ebook,
      BookRequestFormat.audiobook,
    ].any((format) => _detail.statusFor(format) == RequestStatus.pending);
    final outcomePending = !_detail.isKnown &&
        _detail.effectiveUnknownReason ==
            BookStatusUnknownReason.outcomePending;
    if (!hasPending && !outcomePending) {
      _pendingRecheckTimer?.cancel();
      _pendingRecheckTimer = null;
      return;
    }
    _pendingRecheckTimer ??= Timer.periodic(
      const Duration(seconds: 30),
      (_) {
        if (mounted && !_busy && !_checking) _check();
      },
    );
  }

  Future<void> _chooseAndRequest() async {
    if (_busy) return;
    setState(() => _busy = true);
    // Search results are commonly requested while the search field still owns
    // focus. Clear it before opening the bottom sheet so the keyboard cannot
    // cover the format or publication choices on a phone.
    FocusScope.of(context).unfocus();
    try {
      final requestable = _requestableFormats;
      final BookRequestTarget? target;
      if (widget.requestTargetPicker != null) {
        target = await widget.requestTargetPicker!(
          context,
          BookRequestPickerContext(
            foreignId: widget.foreignId,
            title: widget.title,
            instanceId: widget.instanceId,
            detail: _detail,
            requestableFormats: List.unmodifiable(requestable),
          ),
        );
      } else {
        final selected = requestable.length == 1
            ? requestable.first
            : await showModalBottomSheet<BookRequestFormat>(
                context: context,
                backgroundColor: Colors.transparent,
                builder: (_) => _BookFormatSheet(
                  title: widget.title,
                  detail: _detail,
                ),
              );
        target = selected == null
            ? null
            : BookRequestTarget(
                foreignId: widget.foreignId,
                title: widget.title,
                format: selected,
              );
      }
      if (target == null || target.foreignId.trim().isEmpty) return;
      if (!mounted) return;
      setState(() => _submitting = true);
      BookRequestSubmission? submission;
      String? failureMessage;
      String? failureCode;
      var definitiveFailure = false;
      try {
        submission = await widget.service.requestBook(
          foreignId: target.foreignId,
          title: target.title,
          format: target.format,
          instanceId: widget.instanceId,
          selection: target.selection,
        );
      } on RequestSubmissionException catch (e) {
        failureMessage = e.message;
        failureCode = e.code;
        definitiveFailure = e.definitive;
      }
      if (!mounted) return;
      if (submission == null) {
        BookRequestStatusDetail? reconciled;
        if (!definitiveFailure) {
          reconciled = await _reconcileUnknownOutcome(target);
          if (!mounted) return;
          if (_hasReconciledOutcome(reconciled, target.format)) {
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                content: Text(_reconciledOutcome(target.format, reconciled!)),
              ),
            );
            return;
          }
        } else {
          await _refreshAfterSubmission(target.foreignId);
        }
        if (!mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(definitiveFailure && failureMessage != null
                ? failureMessage
                : _unconfirmedOutcomeMessage(
                    reconciled,
                    requestedFormat: target.format,
                    failureCode: failureCode,
                    failureMessage: failureMessage,
                  )),
          ),
        );
        return;
      }
      if (!submission.isKnown) {
        final reconciled = await _reconcileUnknownOutcome(target);
        if (!mounted) return;
        if (_hasReconciledOutcome(reconciled, target.format)) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
              content: Text(_reconciledOutcome(target.format, reconciled!)),
            ),
          );
          return;
        }
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(_unconfirmedOutcomeMessage(
            reconciled,
            requestedFormat: target.format,
          )),
        ));
        return;
      }
      String? partialMessage;
      if (submission.status == RequestStatus.partial) {
        final requestedFormats = target.format == BookRequestFormat.both
            ? [BookRequestFormat.ebook, BookRequestFormat.audiobook]
            : [target.format];
        partialMessage = requestedFormats
            .map((format) =>
                _formatOutcome(format, submission!.formats[format]))
            .join(' ');
      }
      await _refreshAfterSubmission(target.foreignId);
      if (!mounted) return;
      if (partialMessage != null) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(partialMessage)),
        );
      }
    } finally {
      if (mounted) {
        setState(() {
          _busy = false;
          _submitting = false;
        });
      }
    }
  }

  Future<BookRequestStatusDetail?> _refreshSelectedStatus(
    String selectedForeignId,
  ) async {
    // Re-pull per-format truth before re-enabling the CTA so a fast second tap
    // cannot submit the same format against stale pre-request state.
    if (selectedForeignId == widget.foreignId) {
      return _check();
    }
    return widget.service.checkBookStatusDetail(
      selectedForeignId,
      instanceId: widget.instanceId,
    );
  }

  Future<BookRequestStatusDetail?> _refreshAfterSubmission(
    String selectedForeignId,
  ) async {
    final detail = await _refreshSelectedStatus(selectedForeignId);
    if (!mounted) return null;
    // Parent ownership/live-record invalidation can change [refreshTick]. Keep
    // [_busy] set while it runs; didUpdateWidget suppresses a redundant status
    // check until this accepted refresh has fully completed.
    await widget.onRequestCompleted?.call();
    return detail;
  }

  Future<BookRequestStatusDetail?> _reconcileUnknownOutcome(
    BookRequestTarget target,
  ) async {
    BookRequestStatusDetail? lastDetail;
    for (final delay in _outcomeRecheckDelays) {
      if (delay != Duration.zero) await Future<void>.delayed(delay);
      if (!mounted) return null;
      final detail = await _refreshSelectedStatus(target.foreignId);
      lastDetail = detail;
      if (_hasReconciledOutcome(detail, target.format)) {
        if (mounted) await widget.onRequestCompleted?.call();
        return detail;
      }
      if (detail != null &&
          !detail.isKnown &&
          detail.effectiveUnknownReason ==
              BookStatusUnknownReason.requestFailed) {
        if (mounted) await widget.onRequestCompleted?.call();
        return detail;
      }
    }
    if (mounted) await widget.onRequestCompleted?.call();
    return lastDetail;
  }

  String _unconfirmedOutcomeMessage(
    BookRequestStatusDetail? detail, {
    required BookRequestFormat requestedFormat,
    String? failureCode,
    String? failureMessage,
  }) {
    final stillChecking = detail != null &&
        !detail.isKnown &&
        detail.effectiveUnknownReason ==
            BookStatusUnknownReason.outcomePending;
    if (stillChecking) {
      return 'The book library is still confirming this request. Cantinarr will keep checking it.';
    }
    final terminalFailure = detail != null &&
        !detail.isKnown &&
        detail.effectiveUnknownReason == BookStatusUnknownReason.requestFailed;
    if (terminalFailure) {
      return bookRequestFailureMessage(
            detail.failureCode ?? failureCode,
            requestedFormat,
          ) ??
          failureMessage ??
          'Cantinarr could not add this book. Try again, or ask an admin to check the book library.';
    }
    if (failureCode == 'book_outcome_pending' && failureMessage != null) {
      return failureMessage;
    }
    return 'Cantinarr couldn’t confirm whether this request reached the server. Nothing was sent again; refresh before retrying.';
  }

  bool _hasReconciledOutcome(
    BookRequestStatusDetail? detail,
    BookRequestFormat requestedFormat,
  ) {
    if (detail == null || !detail.isKnown) return false;
    final formats = requestedFormat == BookRequestFormat.both
        ? [BookRequestFormat.ebook, BookRequestFormat.audiobook]
        : [requestedFormat];
    final states = formats.map((format) => detail.formats[format]).toList();
    if (states.any((status) => status == null)) return false;
    return states.any((status) => switch (status) {
          RequestStatus.available ||
          RequestStatus.downloading ||
          RequestStatus.requested ||
          RequestStatus.pending ||
          RequestStatus.partial => true,
          RequestStatus.denied || RequestStatus.unavailable || null => false,
        });
  }

  String _reconciledOutcome(
    BookRequestFormat requestedFormat,
    BookRequestStatusDetail detail,
  ) {
    final formats = requestedFormat == BookRequestFormat.both
        ? [BookRequestFormat.ebook, BookRequestFormat.audiobook]
        : [requestedFormat];
    return formats
        .map((format) => _formatOutcome(format, detail.formats[format]))
        .join(' ');
  }

  List<BookRequestFormat> get _requestableFormats => [
        if (_detail.isRequestable(BookRequestFormat.ebook))
          BookRequestFormat.ebook,
        if (_detail.isRequestable(BookRequestFormat.audiobook))
          BookRequestFormat.audiobook,
      ];

  String _formatOutcome(BookRequestFormat format, RequestStatus? status) =>
      switch (status) {
        RequestStatus.available => '${format.label} is available.',
        RequestStatus.downloading => '${format.label} is downloading.',
        RequestStatus.requested || RequestStatus.partial =>
          '${format.label} requested.',
        RequestStatus.pending => '${format.label} is pending approval.',
        RequestStatus.denied => '${format.label} was not approved.',
        RequestStatus.unavailable || null =>
          '${format.label} could not be requested. Try again.',
      };

  @override
  Widget build(BuildContext context) {
    if (_loading) {
      return const SizedBox(
        width: 96,
        child: Center(
          child: SizedBox(
            width: 16,
            height: 16,
            child: CircularProgressIndicator(
                strokeWidth: 2, color: AppTheme.accent),
          ),
        ),
      );
    }
    if (!_detail.isKnown) {
      if (_detail.effectiveUnknownReason ==
          BookStatusUnknownReason.requestFailed) {
        final needsAdmin = _detail.failureCode ==
                'book_configuration_invalid' ||
            _detail.failureCode == 'book_connection_invalid' ||
            _detail.failureCode == 'book_search_rejected';
        return TextButton.icon(
          onPressed: _busy ? null : _chooseAndRequest,
          icon: const Icon(Icons.error_outline_rounded, size: 18),
          label: Text(
            needsAdmin
                ? 'Ask an admin, then retry'
                : 'Couldn’t add · Try again',
          ),
        );
      }
      if (_detail.effectiveUnknownReason ==
          BookStatusUnknownReason.formatNeedsAttention) {
        return const Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.warning_amber_rounded,
                size: 18, color: AppTheme.requested),
            SizedBox(width: 6),
            Flexible(
              child: Text(
                'Ask an admin to check this book’s format',
                style: TextStyle(
                  color: AppTheme.requested,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ],
        );
      }
      return TextButton.icon(
        onPressed: _busy ? null : _check,
        icon: const Icon(Icons.refresh_rounded, size: 18),
        label: Text(_detail.effectiveUnknownReason ==
                BookStatusUnknownReason.outcomePending
            ? 'Still checking · Refresh'
            : 'Couldn’t check · Retry'),
      );
    }
    final requestable = _requestableFormats;
    if (requestable.isEmpty) {
      if (!widget.showCoveredStatus) return const SizedBox.shrink();
      final ebook = _detail.statusFor(BookRequestFormat.ebook);
      final audiobook = _detail.statusFor(BookRequestFormat.audiobook);
      final label = ebook == audiobook
          ? ebook?.label ?? 'Couldn’t check'
          : '${ebook?.label ?? 'Unknown'} + '
              '${audiobook?.label ?? 'Unknown'}';
      return Text(
        label,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      );
    }
    final buttonText = requestable.length == 1
        ? 'Request ${requestable.first.label}'
        : 'Choose format';
    return TextButton(
      onPressed: !_busy ? _chooseAndRequest : null,
      style: TextButton.styleFrom(foregroundColor: AppTheme.accent),
      child: _submitting
          ? Semantics(
              label: 'Preparing request',
              child: const SizedBox(
                width: 16,
                height: 16,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            )
          : Text(buttonText),
    );
  }
}

class _BookFormatSheet extends StatelessWidget {
  final String title;
  final BookRequestStatusDetail detail;

  const _BookFormatSheet({required this.title, required this.detail});

  bool _coveredFor(BookRequestFormat choice) {
    final eb = detail.isCovered(BookRequestFormat.ebook);
    final ab = detail.isCovered(BookRequestFormat.audiobook);
    return switch (choice) {
      BookRequestFormat.ebook => eb,
      BookRequestFormat.audiobook => ab,
      BookRequestFormat.both => eb && ab,
    };
  }

  String? _statusLabelFor(BookRequestFormat choice) =>
      detail.coverageLabel(choice);

  @override
  Widget build(BuildContext context) {
    final eb = detail.isCovered(BookRequestFormat.ebook);
    final ab = detail.isCovered(BookRequestFormat.audiobook);
    return SafeArea(
      child: Container(
        padding: const EdgeInsets.fromLTRB(20, 12, 20, 20),
        decoration: const BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Center(
              child: Container(
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: AppTheme.textSecondary,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),
            const SizedBox(height: 18),
            Text(
              title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 18,
                fontWeight: FontWeight.bold,
              ),
            ),
            const SizedBox(height: 14),
            for (final choice in BookRequestFormat.values)
              if ((choice == BookRequestFormat.ebook && !eb) ||
                  (choice == BookRequestFormat.audiobook && !ab) ||
                  (choice == BookRequestFormat.both && !eb && !ab))
                Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: _FormatChoiceTile(
                    choice: choice,
                    covered: _coveredFor(choice),
                    statusLabel: _statusLabelFor(choice),
                  ),
                ),
          ],
        ),
      ),
    );
  }
}

class _FormatChoiceTile extends StatelessWidget {
  final BookRequestFormat choice;
  final bool covered;
  final String? statusLabel;

  const _FormatChoiceTile({
    required this.choice,
    this.covered = false,
    this.statusLabel,
  });

  @override
  Widget build(BuildContext context) {
    final icon = switch (choice) {
      BookRequestFormat.ebook => Icons.menu_book,
      BookRequestFormat.audiobook => Icons.headphones,
      BookRequestFormat.both => Icons.library_books,
    };
    // Give the tile its own ink surface. The sheet's rounded background is a
    // DecoratedBox, which otherwise sits between ListTile and the modal's
    // Material and can hide taps/splashes (and trips Flutter's debug check).
    return Material(
      color: Colors.transparent,
      child: ListTile(
        enabled: !covered,
        contentPadding: const EdgeInsets.symmetric(horizontal: 12),
        leading: Icon(icon,
            color: covered ? AppTheme.textSecondary : AppTheme.accent),
        title: Text(
          choice.label,
          style: TextStyle(
            color: covered ? AppTheme.textSecondary : AppTheme.textPrimary,
            fontWeight: FontWeight.w600,
          ),
        ),
        subtitle: covered && statusLabel != null
            ? Text(statusLabel!,
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 12))
            : null,
        trailing: covered
            ? const Icon(Icons.check, color: AppTheme.available, size: 18)
            : null,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(8),
          side: const BorderSide(color: AppTheme.border),
        ),
        onTap: covered ? null : () => Navigator.of(context).pop(choice),
      ),
    );
  }
}
