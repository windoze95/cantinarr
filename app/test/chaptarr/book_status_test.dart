import 'package:cantinarr/core/theme/app_theme.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/widgets/book_status.dart';
import 'package:flutter_test/flutter_test.dart';

ChaptarrBook _book({
  required int id,
  required String mediaType,
  bool monitored = true,
  bool hasFile = false,
  DateTime? releaseDate,
}) =>
    ChaptarrBook(
      id: id,
      title: 'Flock',
      foreignBookId: 'flock',
      mediaType: mediaType,
      monitored: monitored,
      releaseDate: releaseDate,
      statistics: ChaptarrBookStatistics(
        bookCount: 1,
        bookFileCount: hasFile ? 1 : 0,
        sizeOnDisk: hasFile ? 1024 : 0,
      ),
    );

void main() {
  test('monitored missing format communicates an active request', () {
    final status = bookFileStatusLine(
      _book(id: 1, mediaType: 'audiobook'),
    );

    expect(status.text, 'Requested — Not downloaded yet');
    expect(status.color, AppTheme.requested);
  });

  test('monitored format with a file remains available, not requested', () {
    final status = bookFileStatusLine(
      _book(id: 1, mediaType: 'audiobook', hasFile: true),
    );

    expect(status.text, isNot(contains('Requested')));
    expect(status.color, AppTheme.available);
    expect(
      bookFormatStatusLine([
        _book(id: 1, mediaType: 'audiobook', hasFile: true),
      ], BookFormat.audiobook).text,
      'Available',
    );
  });

  test('unmonitored missing format does not look requested', () {
    final status = bookFileStatusLine(
      _book(id: 1, mediaType: 'ebook', monitored: false),
    );

    expect(status.text, 'Not requested — No file');
    expect(status.color, AppTheme.unavailable);
  });

  test('grouped status derives each format instead of the first record', () {
    final status = groupedBookStatusLine([
      _book(id: 1, mediaType: 'ebook', hasFile: true),
      _book(id: 2, mediaType: 'audiobook'),
    ]);

    expect(status.text, 'eBook: Available • Audiobook: Requested');
    expect(status.color, AppTheme.requested);
  });

  test('grouped status is stable when record order changes', () {
    final audiobook =
        _book(id: 2, mediaType: 'audiobook', monitored: false);
    final ebook = _book(id: 1, mediaType: 'ebook');

    expect(
      groupedBookStatusLine([audiobook, ebook]).text,
      groupedBookStatusLine([ebook, audiobook]).text,
    );
    expect(
      groupedBookStatusLine([ebook, audiobook]).text,
      'eBook: Requested • Audiobook: Not requested',
    );
  });

  test('grouped status names an absent counterpart format', () {
    final status = groupedBookStatusLine([
      _book(id: 2, mediaType: 'audiobook'),
    ]);

    expect(status.text, 'eBook: Not requested • Audiobook: Requested');
  });

  test('available plus not requested uses attention color, not all-green', () {
    final status = groupedBookStatusLine([
      _book(id: 1, mediaType: 'ebook', hasFile: true),
      _book(id: 2, mediaType: 'audiobook', monitored: false),
    ]);

    expect(status.text, 'eBook: Available • Audiobook: Not requested');
    expect(status.color, AppTheme.requested);
  });

  test('a later duplicate cannot hide an available format', () {
    final formatStatus = bookFormatStatusLine([
      _book(id: 1, mediaType: 'ebook', hasFile: true),
      _book(id: 2, mediaType: 'ebook', monitored: false),
    ], BookFormat.ebook);

    expect(formatStatus.text, 'Available');
    expect(formatStatus.color, AppTheme.available);
  });

  test('an unknown record makes grouped format truth need attention', () {
    final status = groupedBookStatusLine([
      _book(id: 1, mediaType: 'future-format'),
    ]);

    expect(status.text, 'Book format: Needs attention');
    expect(status.color, AppTheme.requested);
  });

  test('an unknown sibling does not make an absent format look requestable', () {
    final status = groupedBookStatusLine([
      _book(id: 1, mediaType: 'ebook'),
      _book(id: 2, mediaType: 'future-format'),
    ]);

    expect(status.text, 'Book format: Needs attention • eBook: Requested');
    expect(status.text, isNot(contains('Audiobook: Not requested')));
    expect(status.color, AppTheme.requested);
  });
}
