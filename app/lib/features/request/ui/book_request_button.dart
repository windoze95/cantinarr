import 'dart:async';

import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../data/book_ownership.dart';
import '../data/request_service.dart';

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
  });

  @override
  State<BookRequestButton> createState() => _BookRequestButtonState();
}

class _BookRequestButtonState extends State<BookRequestButton> {
  // The async-loaded request state (no ownership). Ownership is layered on in
  // [_detail] on every read, so the button reflects the owned-books digest even
  // when it loads AFTER this button was first built (the chip already does) —
  // otherwise an owned-but-unrequested format reads as "Request", not
  // "Request more".
  BookRequestStatusDetail _serverDetail = const BookRequestStatusDetail();
  bool _loading = true;
  bool _busy = false;
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

  Future<void> _check() async {
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
        return;
      }
      setState(() {
        _serverDetail = detail;
        _loading = false;
      });
      _syncPendingRecheck();
      widget.onDetailChanged?.call(_detail);
    } finally {
      _activeChecks--;
    }
  }

  void _syncPendingRecheck() {
    final hasPending = [
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
        if (mounted && !_busy && !_checking) _check();
      },
    );
  }

  Future<void> _chooseAndRequest() async {
    if (_busy) return;
    final requestable = _requestableFormats;
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
    if (selected == null) return;
    if (!mounted) return;
    setState(() => _busy = true);
    try {
      BookRequestSubmission? submission;
      String? failureMessage;
      var definitiveFailure = false;
      try {
        submission = await widget.service.requestBook(
          foreignId: widget.foreignId,
          title: widget.title,
          format: selected,
          instanceId: widget.instanceId,
        );
      } on RequestSubmissionException catch (e) {
        failureMessage = e.message;
        definitiveFailure = e.definitive;
      }
      if (!mounted) return;
      if (submission == null) {
        await _refreshAfterSubmission();
        if (!mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(definitiveFailure && failureMessage != null
                ? failureMessage
                : 'The request outcome couldn’t be confirmed. The book status was refreshed.'),
          ),
        );
        return;
      }
      if (!submission.isKnown) {
        await _refreshAfterSubmission();
        if (!mounted) return;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text(
              'The request was sent, but its result could not be confirmed. The book status was refreshed.',
            ),
          ),
        );
        return;
      }
      String? partialMessage;
      if (submission.status == RequestStatus.partial) {
        final requestedFormats = selected == BookRequestFormat.both
            ? [BookRequestFormat.ebook, BookRequestFormat.audiobook]
            : [selected];
        partialMessage = requestedFormats
            .map((format) =>
                _formatOutcome(format, submission!.formats[format]))
            .join(' ');
      }
      await _refreshAfterSubmission();
      if (!mounted) return;
      if (partialMessage != null) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(partialMessage)),
        );
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _refreshAfterSubmission() async {
    // Re-pull per-format truth before re-enabling the CTA so a fast second tap
    // cannot submit the same format against stale pre-request state.
    await _check();
    if (!mounted) return;
    // Parent ownership/live-record invalidation can change [refreshTick]. Keep
    // [_busy] set while it runs; didUpdateWidget suppresses a redundant status
    // check until this accepted refresh has fully completed.
    await widget.onRequestCompleted?.call();
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
        label: const Text('Couldn’t check · Retry'),
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
      child: _busy
          ? const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
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
