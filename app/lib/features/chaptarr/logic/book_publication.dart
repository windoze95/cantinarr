import '../data/chaptarr_models.dart';

/// Distant placeholder dates stay visible as source metadata without claiming
/// a confirmed publication date. The server uses the same five-year window.
String bookPublicationYearLabel(int year, {int? currentYear}) {
  if (year <= 0) return '';
  return year > (currentYear ?? DateTime.now().year) + 5
      ? 'Date unconfirmed ($year)'
      : '$year';
}

/// Metadata from one selected catalog/library record, including its leading
/// edition. A missing page count never borrows from a different library record.
class BookPublication {
  final int year;
  final int pages;
  final String publisher;
  final String format;
  const BookPublication(
      {this.year = 0, this.pages = 0, this.publisher = '', this.format = ''});

  factory BookPublication.fromBook(ChaptarrBook book, {int fallbackYear = 0}) {
    final editions = [...book.editions]..sort((a, b) {
        if (a.monitored != b.monitored) return a.monitored ? -1 : 1;
        if (a.manualAdd != b.manualAdd) return a.manualAdd ? -1 : 1;
        return a.id.compareTo(b.id);
      });
    final edition = editions
            .where((e) =>
                book.foreignEditionId?.isNotEmpty == true &&
                e.foreignEditionId == book.foreignEditionId)
            .firstOrNull ??
        editions.firstOrNull;
    return BookPublication(
      year:
          book.releaseDate?.year ?? edition?.releaseDate?.year ?? fallbackYear,
      pages: book.pageCount > 0 ? book.pageCount : edition?.pageCount ?? 0,
      publisher: edition?.publisher?.trim() ?? '',
      format: edition?.format?.trim() ?? '',
    );
  }

  String get summary => [
        if (year > 0) bookPublicationYearLabel(year),
        if (pages > 0) '$pages pages',
      ].join(' · ');
  String get editionLabel => [
        if (publisher.isNotEmpty) publisher,
        if (format.isNotEmpty) format
      ].join(' · ');
}
