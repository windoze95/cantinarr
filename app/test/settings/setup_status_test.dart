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
  group('missingCoreCapability', () {
    test('a movies-only server is not broken', () {
      final status = _status([
        _item('radarr', true, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
      ]);

      // Sonarr is an unconfigured essential, and that is fine: the admin does
      // not do TV. Grading the rows rather than the capability would paint a
      // working server red forever.
      expect(status.remaining, 1);
      expect(status.missingCoreCapability, isFalse);
    });

    test('a books-only server is not broken', () {
      final status = _status([
        _item('radarr', false, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
        _item('books', true),
      ]);

      expect(status.missingCoreCapability, isFalse);
    });

    test('no library at all is broken', () {
      final status = _status([
        _item('radarr', false, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
        _item('books', false),
      ]);

      expect(status.missingCoreCapability, isTrue);
    });

    test('no metadata is broken even with a library', () {
      final status = _status([
        _item('radarr', true, optional: false),
        _item('tmdb', false, optional: false),
      ]);

      expect(status.missingCoreCapability, isTrue);
    });

    test('a failed load is never called broken', () {
      // No items means the request failed or the server is older, not that the
      // admin configured nothing.
      expect(SetupStatus.fromJson(const {}).missingCoreCapability, isFalse);
    });
  });

  group('isUrgent', () {
    SetupItem itemNamed(SetupStatus status, String key) =>
        status.items.firstWhere((i) => i.key == key);

    test('an empty arr is not urgent once some library exists', () {
      final status = _status([
        _item('radarr', true, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
      ]);

      // The movies-only server again: the row is unfinished, but nothing is
      // broken, and the Settings tile calls this server merely unfinished too.
      expect(status.isUrgent(itemNamed(status, 'sonarr')), isFalse);
      expect(status.missingCoreCapability, isFalse);
    });

    test('every empty arr is urgent while there is no library at all', () {
      final status = _status([
        _item('radarr', false, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
      ]);

      expect(status.isUrgent(itemNamed(status, 'radarr')), isTrue);
      expect(status.isUrgent(itemNamed(status, 'sonarr')), isTrue);
    });

    test('metadata is urgent on its own', () {
      final status = _status([
        _item('radarr', true, optional: false),
        _item('tmdb', false, optional: false),
      ]);

      expect(status.isUrgent(itemNamed(status, 'tmdb')), isTrue);
    });

    test('an optional row is never urgent, library or not', () {
      final status = _status([
        _item('radarr', false, optional: false),
        _item('sonarr', false, optional: false),
        _item('tmdb', false, optional: false),
        _item('books', false),
        _item('trakt', false),
      ]);

      // Chaptarr can satisfy the library capability, but a books module nobody
      // asked for is not what is wrong with this server.
      expect(status.isUrgent(itemNamed(status, 'books')), isFalse);
      expect(status.isUrgent(itemNamed(status, 'trakt')), isFalse);
    });

    test('a configured row is never urgent', () {
      final status = _status([
        _item('tmdb', true, optional: false),
        _item('radarr', true, optional: false),
      ]);

      expect(status.isUrgent(itemNamed(status, 'tmdb')), isFalse);
      expect(status.isUrgent(itemNamed(status, 'radarr')), isFalse);
    });

    test('an essential this build has never heard of is urgent', () {
      // A newer server can add essentials. Without a capability rule to check
      // them against, an empty one is taken at its word.
      final status = _status([_item('from_a_newer_build', false, optional: false)]);

      expect(status.isUrgent(itemNamed(status, 'from_a_newer_build')), isTrue);
    });
  });

  group('skipped items', () {
    test('a skipped row leaves the progress math entirely', () {
      final status = _status([
        _item('radarr', true, optional: false),
        _item('tmdb', true, optional: false),
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
        _item('radarr', true, optional: false),
        _item('tmdb', true, optional: false),
        _item('music', true, skipped: true),
      ]);

      expect(status.skippedCount, 0);
      expect(status.effectiveTotal, 3);
      expect(status.remaining, 0);
    });

    test('skipping never repairs a missing core capability', () {
      // Capability is about what the server can do, not about tidiness — and
      // the server never stamps skipped onto an essential anyway; this pins
      // the client honoring the same rule if a malformed payload tried.
      final status = _status([
        _item('radarr', false, optional: false, skipped: true),
        _item('sonarr', false, optional: false),
        _item('tmdb', true, optional: false),
      ]);

      expect(status.missingCoreCapability, isTrue);
    });
  });
}
