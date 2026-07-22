import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/chaptarr_models.dart';

/// (text, colour) pair for a book's one-line availability status.
typedef BookStatus = ({String text, Color color});

/// Computes one format record's availability line. A monitored record without
/// a file is an active request in Chaptarr, not an unrequested/missing title.
BookStatus bookFileStatusLine(ChaptarrBook book) {
  if (book.hasFile) {
    final size = book.statistics?.sizeFormatted;
    final formats = book.formats;
    final label =
        formats.length == 1 ? _formatLabel(formats.first) : 'Downloaded';
    final suffix = (size != null && size != '0 B') ? ' — $size' : '';
    return (text: '$label$suffix', color: AppTheme.available);
  }
  if (book.monitored) {
    return _isReleased(book.releaseDate)
        ? (text: 'Requested — Not downloaded yet', color: AppTheme.requested)
        : (text: 'Requested — Not released yet', color: AppTheme.requested);
  }
  return _isReleased(book.releaseDate)
      ? (text: 'Not requested — No file', color: AppTheme.unavailable)
      : (text: 'Not requested — Not released yet', color: AppTheme.unavailable);
}

/// Computes a title summary from every ebook/audiobook record rather than
/// letting whichever record happened to arrive first define the whole title.
BookStatus groupedBookStatusLine(Iterable<ChaptarrBook> records) {
  final books = records.toList();
  if (books.isEmpty) {
    return (text: 'Status unavailable', color: AppTheme.unavailable);
  }

  final parts = <String>[];
  var hasFile = false;
  var hasRequested = false;
  var hasNotRequested = false;
  final hasUnknownFormat =
      books.any((book) => book.format == BookFormat.unknown);
  final hasKnownFormat = books.any((book) => book.format != BookFormat.unknown);
  for (final format in [BookFormat.ebook, BookFormat.audiobook]) {
    final matching = books.where((book) => book.format == format).toList();
    if (matching.isEmpty) {
      // An unknown sibling may actually be this format. Keep the known record's
      // state visible, but do not also claim the missing slot is requestable.
      if (hasKnownFormat && !hasUnknownFormat) {
        final formatStatus = bookFormatStatusLine(books, format);
        hasNotRequested = true;
        parts.add('${_formatLabel(format)}: ${formatStatus.text}');
      }
      continue;
    }
    final formatStatus = bookFormatStatusLine(books, format);
    final available = formatStatus.text == 'Available';
    final requested = formatStatus.text == 'Requested';
    final notRequested = formatStatus.text == 'Not requested';
    hasFile |= available;
    hasRequested |= requested;
    hasNotRequested |= notRequested;
    parts.add('${_formatLabel(format)}: ${formatStatus.text}');
  }

  if (hasUnknownFormat) {
    parts.insert(0, 'Book format: Needs attention');
  }

  if (parts.isEmpty) {
    final fallback = bookFileStatusLine(books.first);
    return (text: 'Book: ${_shortState(fallback.text)}', color: fallback.color);
  }
  final color = hasUnknownFormat || hasRequested || (hasFile && hasNotRequested)
      ? AppTheme.requested
      : hasFile
          ? AppTheme.available
          : AppTheme.unavailable;
  return (text: parts.join(' • '), color: color);
}

/// Reduces all records for one format. Duplicate records cannot hide a file or
/// an active request merely because an older record appeared first.
BookStatus bookFormatStatusLine(
  Iterable<ChaptarrBook> records,
  BookFormat format,
) {
  final matching = records.where((book) => book.format == format).toList();
  if (matching.isEmpty) {
    return (text: 'Not requested', color: AppTheme.unavailable);
  }
  if (matching.any((book) => book.hasFile)) {
    return (text: 'Available', color: AppTheme.available);
  }
  if (matching.any((book) => book.monitored)) {
    return (text: 'Requested', color: AppTheme.requested);
  }
  return (text: 'Not requested', color: AppTheme.unavailable);
}

String _shortState(String text) => text.split(' — ').first;

/// A book is "released" once its release date has passed (a null date is
/// treated as released, matching Sonarr's "aired with no date" handling).
bool _isReleased(DateTime? releaseDate) {
  if (releaseDate == null) return true;
  return !releaseDate.isAfter(DateTime.now());
}

String _formatLabel(BookFormat format) => switch (format) {
      BookFormat.audiobook => 'Audiobook',
      BookFormat.ebook => 'eBook',
      BookFormat.unknown => 'Downloaded',
    };
