import 'package:cantinarr/features/settings/data/discovery_settings_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('DiscoverySettings.fromJson', () {
    test('parses the server payload and labels every source', () {
      final settings = DiscoverySettings.fromJson(const {
        'source': 'trakt_trending',
        'english_only': true,
        'sources': ['tmdb_trending', 'trakt_trending', 'tmdb_popular'],
        'trakt_configured': true,
      });

      expect(settings.source, 'trakt_trending');
      expect(settings.englishOnly, isTrue);
      expect(settings.traktConfigured, isTrue);
      expect(settings.sources.map((s) => s.value),
          ['tmdb_trending', 'trakt_trending', 'tmdb_popular']);
      // Every known source reads as prose, not as its stored key.
      for (final source in settings.sources) {
        expect(source.label, isNot(source.value));
        expect(source.description, isNotEmpty);
      }
      // Exactly one option is tagged as the upgrade, and it is the one the
      // server also adopts by default once its credential exists.
      expect(
        settings.sources.where((s) => s.recommended).map((s) => s.value),
        ['trakt_trending'],
      );
    });

    test('shows a source this build does not know rather than dropping it', () {
      final settings = DiscoverySettings.fromJson(const {
        'sources': ['tmdb_trending', 'from_a_newer_build'],
      });

      expect(settings.sources.map((s) => s.value),
          ['tmdb_trending', 'from_a_newer_build']);
      expect(settings.sources.last.label, 'from_a_newer_build');
      expect(settings.sources.last.recommended, isFalse,
          reason: 'this build cannot vouch for a source it has no copy for');
    });

    test('defaults missing fields', () {
      final settings = DiscoverySettings.fromJson(const {});
      expect(settings.source, '');
      expect(settings.englishOnly, isFalse);
      expect(settings.sources, isEmpty);
      expect(settings.traktConfigured, isFalse);
    });
  });

  group('isSelectable', () {
    test('locks the Trakt source until Trakt is configured', () {
      const trakt = DiscoverySource(
          value: 'trakt_trending', label: 'Trending now', description: '');
      const tmdb = DiscoverySource(
          value: 'tmdb_trending', label: 'Trending this week', description: '');

      final without = DiscoverySettings.fromJson(const {
        'sources': ['tmdb_trending', 'trakt_trending'],
        'trakt_configured': false,
      });
      expect(without.isSelectable(trakt), isFalse);
      expect(without.isSelectable(tmdb), isTrue,
          reason: 'only the Trakt source depends on the Trakt credential');

      final with_ = DiscoverySettings.fromJson(const {
        'sources': ['tmdb_trending', 'trakt_trending'],
        'trakt_configured': true,
      });
      expect(with_.isSelectable(trakt), isTrue);
    });
  });
}
