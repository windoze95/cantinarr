import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';
import '../../data/chaptarr_models.dart';

/// (text, colour) pair for a book's one-line availability status.
typedef BookStatus = ({String text, Color color});

/// Computes a book's availability line, mirroring [episodeStatusLine]'s
/// priority order adapted to books (which have no live queue lookup here):
/// 1. has a file              -> "{format} — {size}" / "Downloaded"  (green)
/// 2. no file, already out    -> "Missing"     (red)
/// 3. no file, not yet out    -> "Unreleased"  (blue)
BookStatus bookFileStatusLine(ChaptarrBook book) {
  if (book.hasFile) {
    final size = book.statistics?.sizeFormatted;
    final formats = book.formats;
    final label =
        formats.length == 1 ? _formatLabel(formats.first) : 'Downloaded';
    final suffix = (size != null && size != '0 B') ? ' — $size' : '';
    return (text: '$label$suffix', color: AppTheme.available);
  }
  return _isReleased(book.releaseDate)
      ? (text: 'Missing', color: AppTheme.error)
      : (text: 'Unreleased', color: AppTheme.downloading);
}

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
