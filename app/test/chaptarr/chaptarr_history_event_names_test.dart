import 'package:cantinarr/features/chaptarr/data/chaptarr_models.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_book_detail_sheet.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_history_screen.dart';
import 'package:flutter_test/flutter_test.dart';

// The event names Chaptarr's history enum actually serializes on the wire
// (EntityHistoryEventType, camelCase). The two screens used to match invented
// names — 'bookImported', 'downloadFolderImported', 'authorFolderImported' —
// so real events fell through to the raw fallback. This pins every wire name
// to a mapped label so the vocabulary cannot drift again.
const _wireEventNames = [
  'grabbed',
  'bookFileImported',
  'downloadImported',
  'bookImportIncomplete',
  'downloadFailed',
  'bookFileDeleted',
  'bookFileRenamed',
  'bookFileRetagged',
  'downloadIgnored',
  'bookFileConverted',
  'bookFileConversionFailed',
];

ChaptarrHistoryRecord _record(String eventType) =>
    ChaptarrHistoryRecord(id: 1, eventType: eventType);

void main() {
  test('history screen styles every wire event name', () {
    for (final name in _wireEventNames) {
      final style = chaptarrHistoryEventStyle(name);
      expect(style.label, isNot(name),
          reason: "'$name' fell through to the raw fallback");
    }
  });

  test('book detail sheet labels every wire event name', () {
    for (final name in _wireEventNames) {
      final label = chaptarrBookHistoryEventLabel(_record(name));
      expect(label, isNot(name),
          reason: "'$name' fell through to the raw fallback");
    }
  });

  test('import events read as Imported in both screens', () {
    for (final name in ['bookFileImported', 'downloadImported']) {
      expect(chaptarrHistoryEventStyle(name).label, 'Imported');
      expect(chaptarrBookHistoryEventLabel(_record(name)), 'Imported');
    }
  });

  test('the invented names stay unmapped so nobody re-adds them', () {
    for (final name in [
      'bookImported',
      'downloadFolderImported',
      'authorFolderImported',
    ]) {
      // Falling through to the fallback IS the assertion: these names never
      // occur on the wire, and a case for one of them would be dead code
      // masquerading as coverage.
      expect(chaptarrHistoryEventStyle(name).label, name);
      expect(chaptarrBookHistoryEventLabel(_record(name)), name);
    }
  });
}
