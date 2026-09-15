import 'package:cantinarr/features/settings/data/setup_status_service.dart';
import 'package:flutter_test/flutter_test.dart';

Map<String, dynamic> _item(String key, bool configured,
        {bool optional = true, bool skipped = false}) =>
    {
      'key': key,
      'title': key,
      'description': 'about $key',
      'configured': configured,
      'optional': optional,
      if (skipped) 'skipped': true,
    };

SetupStatus _status(List<Map<String, dynamic>> items) => SetupStatus.fromJson({
      'items': items,
      'configured': items.where((i) => i['configured'] == true).length,
      'total': items.length,
    });

void main() {
  group('skipped items', () {
    test('a skipped row leaves the progress math entirely', () {
      final status = _status([
        _item('radarr', true),
        _item('tmdb', true),
        _item('books', false),
        _item('music', false, skipped: true),
      ]);

      // Denominator and remaining both shed the skip, so "2 of 3 configured"
      // stays a true sentence and nothing nags for the acknowledged row.
      expect(status.skippedCount, 1);
      expect(status.effectiveTotal, 3);
      expect(status.remaining, 1);
    });

    test('a skipped row that later becomes configured counts as configured',
        () {
      final status = _status([
        _item('radarr', true),
        _item('tmdb', true),
        _item('music', true, skipped: true),
      ]);

      expect(status.skippedCount, 0);
      expect(status.effectiveTotal, 3);
      expect(status.remaining, 0);
    });

    test('every item can be skipped without counting it configured', () {
      final status = _status([
        for (final key in ['radarr', 'sonarr', 'tmdb', 'push'])
          _item(key, false, skipped: true),
      ]);
      expect(status.configured, 0);
      expect(status.skippedCount, 4);
      expect(status.effectiveTotal, 0);
      expect(status.remaining, 0);
      expect(status.progress, 1);
      expect(status.summary, 'Nothing left to set up');
    });

    test('restoring an item adds it back to the remaining count', () {
      final status = _status([
        _item('radarr', false),
        _item('sonarr', false, skipped: true),
      ]);
      expect(status.remaining, 1);
      expect(status.progress, 0);
      expect(status.summary, '0 of 1 features configured');
    });

    test('a configured skipped feature uses its skip again after removal', () {
      expect(_status([_item('radarr', true, skipped: true)]).configured, 1);
      final removed = _status([_item('radarr', false, skipped: true)]);
      expect(removed.configured, 0);
      expect(removed.remaining, 0);
      expect(removed.isComplete, isTrue);
    });
  });
}
