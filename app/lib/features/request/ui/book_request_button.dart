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
  final RequestService service;
  final BookOwnership? ownership;
  final ValueChanged<BookRequestStatusDetail>? onDetailChanged;
  final VoidCallback? onRequestCompleted;

  const BookRequestButton({
    super.key,
    required this.foreignId,
    required this.title,
    required this.service,
    this.ownership,
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
  RequestStatus _status = RequestStatus.unavailable;
  bool _loading = true;
  bool _busy = false;
  int _checkGeneration = 0;

  BookRequestStatusDetail get _detail =>
      _serverDetail.withOwnership(widget.ownership);

  @override
  void initState() {
    super.initState();
    _check();
  }

  @override
  void didUpdateWidget(covariant BookRequestButton oldWidget) {
    super.didUpdateWidget(oldWidget);
    // If this row got reused for a different book, re-fetch its request state.
    if (oldWidget.foreignId != widget.foreignId) {
      _loading = true;
      _serverDetail = const BookRequestStatusDetail();
      _status = RequestStatus.unavailable;
      _check();
    } else if (oldWidget.ownership != widget.ownership) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) widget.onDetailChanged?.call(_detail);
      });
    }
  }

  Future<void> _check() async {
    final foreignId = widget.foreignId;
    final generation = ++_checkGeneration;
    final detail = await widget.service.checkBookStatusDetail(foreignId);
    if (!mounted ||
        generation != _checkGeneration ||
        foreignId != widget.foreignId) {
      return;
    }
    setState(() {
      _serverDetail = detail;
      _status = detail.status;
      _loading = false;
    });
    widget.onDetailChanged?.call(_detail);
  }

  Future<void> _request() async {
    if (_busy) return;
    final selected = await showModalBottomSheet<BookRequestFormat>(
      context: context,
      backgroundColor: Colors.transparent,
      builder: (_) => _BookFormatSheet(title: widget.title, detail: _detail),
    );
    if (selected == null) return;
    if (!mounted) return;
    setState(() => _busy = true);
    RequestStatus? s;
    String? failureMessage;
    try {
      s = await widget.service.requestBook(
        foreignId: widget.foreignId,
        title: widget.title,
        format: selected,
      );
    } on RequestSubmissionException catch (e) {
      failureMessage = e.message;
    }
    if (!mounted) return;
    setState(() => _busy = false);
    if (s == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
          content: Text(failureMessage ?? 'Request failed. Please try again.'),
        ),
      );
      return;
    }
    // Re-pull per-format coverage so the button reflects the still-open format.
    await _check();
    if (mounted) widget.onRequestCompleted?.call();
  }

  bool _isCovered(BookRequestFormat f) => _detail.isCovered(f);

  /// Requestable while at least one of ebook/audiobook hasn't been requested.
  bool get _requestable => !(_isCovered(BookRequestFormat.ebook) &&
      _isCovered(BookRequestFormat.audiobook));

  String get _buttonText {
    if (!_requestable) {
      // Both formats covered. Books don't use the movie/TV available labels
      // ("Available"): a fulfilled request is simply downloaded, and a
      // partially fulfilled one is still on its way.
      return switch (_status) {
        RequestStatus.available => 'Downloaded',
        RequestStatus.partial => 'Requested',
        _ => _status.buttonLabel,
      };
    }
    final anyCovered = _isCovered(BookRequestFormat.ebook) ||
        _isCovered(BookRequestFormat.audiobook);
    return anyCovered ? 'Request more' : _status.buttonLabel;
  }

  Color get _color {
    if (_requestable) return AppTheme.accent;
    return switch (_status) {
      RequestStatus.pending ||
      RequestStatus.requested ||
      RequestStatus.partial =>
        AppTheme.requested,
      RequestStatus.downloading => AppTheme.downloading,
      RequestStatus.available => AppTheme.available,
      _ => AppTheme.accent,
    };
  }

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
    return TextButton(
      onPressed: _requestable && !_busy ? _request : null,
      style: TextButton.styleFrom(foregroundColor: _color),
      child: _busy
          ? const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : Text(_buttonText),
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
    final exactlyOneCovered = eb != ab;
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
              // Hide "both" when exactly one format is already requested — only
              // the remaining single format is worth offering.
              if (!(choice == BookRequestFormat.both && exactlyOneCovered))
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
    return ListTile(
      enabled: !covered,
      contentPadding: const EdgeInsets.symmetric(horizontal: 12),
      leading:
          Icon(icon, color: covered ? AppTheme.textSecondary : AppTheme.accent),
      title: Text(
        choice.label,
        style: TextStyle(
          color: covered ? AppTheme.textSecondary : AppTheme.textPrimary,
          fontWeight: FontWeight.w600,
        ),
      ),
      subtitle: covered && statusLabel != null
          ? Text(statusLabel!,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12))
          : null,
      trailing: covered
          ? const Icon(Icons.check, color: AppTheme.available, size: 18)
          : null,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(8),
        side: const BorderSide(color: AppTheme.border),
      ),
      onTap: covered ? null : () => Navigator.of(context).pop(choice),
    );
  }
}
