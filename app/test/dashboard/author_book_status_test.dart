import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/dashboard/logic/author_book_status.dart';
import 'package:cantinarr/features/request/data/book_ownership.dart';
import 'package:flutter_test/flutter_test.dart';

OwnedTitle _title({
  bool ebookMonitored = false,
  bool ebookDownloaded = false,
  bool audiobookMonitored = false,
  bool audiobookDownloaded = false,
  bool statusKnown = true,
}) =>
    OwnedTitle(
      title: 'Ahsoka',
      author: 'E. K. Johnston',
      statusKnown: statusKnown,
      ownership: BookOwnership(
        ebook: FormatOwnership(
          monitored: ebookMonitored,
          downloaded: ebookDownloaded,
        ),
        audiobook: FormatOwnership(
          monitored: audiobookMonitored,
          downloaded: audiobookDownloaded,
        ),
      ),
    );

void main() {
  test('a title nobody has requested says so instead of going blank', () {
    final status = buildAuthorBookStatus(_title());

    // This is the whole reason the author page does not reuse the Recently
    // Added verdict: an un-requested book is the normal state here, and it must
    // not render identically to a state that could not be read.
    expect(status, isNotNull);
    expect(status!.label, 'Not requested');
    expect(status.color, AppTheme.textSecondary);
    expect(status.subtitle, isNull);
  });

  test('an unreadable format state renders no pill at all', () {
    expect(buildAuthorBookStatus(_title(statusKnown: false)), isNull);
    // Even with formats owned: an old server that cannot classify a record has
    // told us nothing, and a guessed label is worse than none.
    expect(
      buildAuthorBookStatus(
        _title(ebookDownloaded: true, statusKnown: false),
      ),
      isNull,
    );
  });

  test('both formats on disk read Available', () {
    final status = buildAuthorBookStatus(_title(
      ebookMonitored: true,
      ebookDownloaded: true,
      audiobookMonitored: true,
      audiobookDownloaded: true,
    ));

    expect(status!.label, 'Available');
    expect(status.color, AppTheme.available);
    expect(status.subtitle, 'eBook + Audiobook');
  });

  test('a format still being fetched reads Requested', () {
    final status = buildAuthorBookStatus(_title(
      ebookDownloaded: true,
      audiobookMonitored: true,
    ));

    expect(status!.label, 'Requested');
    expect(status.subtitle, 'eBook + Audiobook requested');
  });

  test('on disk with nothing pending reads Partial', () {
    final status = buildAuthorBookStatus(_title(ebookDownloaded: true));

    expect(status!.label, 'Partial');
    expect(status.subtitle, 'eBook');
  });
}
