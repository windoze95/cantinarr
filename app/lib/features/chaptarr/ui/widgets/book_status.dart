import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/chaptarr_models.dart';

/// (text, colour) pair for a book's one-line availability status.
typedef BookStatus = ({String text, Color color});

/// Computes one format record's availability line. A monitored record without
/// a file is an active request in Chaptarr, not an unrequested/missing title.
BookStatus bookFileStatusLine(ChaptarrBook book, {AppLocalizations? strings}) {
  if (book.hasFile) {
    final size = book.statistics?.sizeFormatted;
    final formats = book.formats;
    final label = formats.length == 1
        ? _formatLabel(formats.first, strings: strings)
        : (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminDownloaded;
    final suffix = (size != null && size != '0 B') ? ' — $size' : '';
    return (text: '$label$suffix', color: AppTheme.available);
  }
  if (book.monitored) {
    return _isReleased(book.releaseDate)
        ? (
            text: (strings ?? lookupAppLocalizations(const Locale('en')))
                .adminRequestedNotDownloadedYet,
            color: AppTheme.requested
          )
        : (
            text: (strings ?? lookupAppLocalizations(const Locale('en')))
                .adminRequestedNotReleasedYet,
            color: AppTheme.requested
          );
  }
  return _isReleased(book.releaseDate)
      ? (
          text: (strings ?? lookupAppLocalizations(const Locale('en')))
              .adminNotRequestedNoFile,
          color: AppTheme.unavailable
        )
      : (
          text: (strings ?? lookupAppLocalizations(const Locale('en')))
              .adminNotRequestedNotReleasedYet,
          color: AppTheme.unavailable
        );
}

/// Computes a title summary from every ebook/audiobook record rather than
/// letting whichever record happened to arrive first define the whole title.
BookStatus groupedBookStatusLine(Iterable<ChaptarrBook> records,
    {AppLocalizations? strings}) {
  final books = records.toList();
  if (books.isEmpty) {
    return (
      text: (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminStatusUnavailable,
      color: AppTheme.unavailable
    );
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
        final formatStatus =
            bookFormatStatusLine(books, format, strings: strings);
        hasNotRequested = true;
        parts.add(
            '${_formatLabel(format, strings: strings)}: ${formatStatus.text}');
      }
      continue;
    }
    final formatStatus = bookFormatStatusLine(books, format, strings: strings);
    final available = matching.any((book) => book.hasFile);
    final requested = !available && matching.any((book) => book.monitored);
    final notRequested = !available && !requested;
    hasFile |= available;
    hasRequested |= requested;
    hasNotRequested |= notRequested;
    parts
        .add('${_formatLabel(format, strings: strings)}: ${formatStatus.text}');
  }

  if (hasUnknownFormat) {
    parts.insert(
        0,
        (strings ?? lookupAppLocalizations(const Locale('en')))
            .adminBookFormatNeedsAttention);
  }

  if (parts.isEmpty) {
    final fallback = bookFileStatusLine(books.first, strings: strings);
    return (
      text: (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminBook2((_shortState(fallback.text)).toString()),
      color: fallback.color
    );
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
    Iterable<ChaptarrBook> records, BookFormat format,
    {AppLocalizations? strings}) {
  final matching = records.where((book) => book.format == format).toList();
  if (matching.isEmpty) {
    return (
      text: (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminNotRequested,
      color: AppTheme.unavailable
    );
  }
  if (matching.any((book) => book.hasFile)) {
    return (
      text: (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminAvailable,
      color: AppTheme.available
    );
  }
  if (matching.any((book) => book.monitored)) {
    return (
      text: (strings ?? lookupAppLocalizations(const Locale('en')))
          .adminRequested,
      color: AppTheme.requested
    );
  }
  return (
    text: (strings ?? lookupAppLocalizations(const Locale('en')))
        .adminNotRequested,
    color: AppTheme.unavailable
  );
}

String _shortState(String text) => text.split(' — ').first;

/// A book is "released" once its release date has passed (a null date is
/// treated as released, matching Sonarr's "aired with no date" handling).
bool _isReleased(DateTime? releaseDate) {
  if (releaseDate == null) return true;
  return !releaseDate.isAfter(DateTime.now());
}

String _formatLabel(BookFormat format, {AppLocalizations? strings}) =>
    switch (format) {
      BookFormat.audiobook =>
        (strings ?? lookupAppLocalizations(const Locale('en'))).adminAudiobook,
      BookFormat.ebook =>
        (strings ?? lookupAppLocalizations(const Locale('en'))).adminEBook,
      BookFormat.unknown =>
        (strings ?? lookupAppLocalizations(const Locale('en'))).adminDownloaded,
    };
