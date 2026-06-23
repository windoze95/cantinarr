import 'package:cantinarr/features/request/data/request_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('SeasonScope.isExplicitList', () {
    test('coarse scopes are not explicit lists', () {
      expect(SeasonScope.isExplicitList('all'), isFalse);
      expect(SeasonScope.isExplicitList('first'), isFalse);
      expect(SeasonScope.isExplicitList(''), isFalse);
    });

    test('a JSON array is an explicit list', () {
      expect(SeasonScope.isExplicitList('[3,5]'), isTrue);
      expect(SeasonScope.isExplicitList('[4]'), isTrue);
    });
  });

  group('SeasonScope.describe', () {
    test('coarse scopes map to their choice label', () {
      expect(SeasonScope.describe('all'), 'Entire series');
      expect(SeasonScope.describe('first'), 'First season');
      expect(SeasonScope.describe('latest'), 'Most recent season');
      expect(SeasonScope.describe('pilot'), 'Pilot only');
    });

    test('a single-season list reads "Season N"', () {
      expect(SeasonScope.describe('[4]'), 'Season 4');
    });

    test('a multi-season list reads "Seasons a, b" sorted', () {
      expect(SeasonScope.describe('[3,5]'), 'Seasons 3, 5');
      expect(SeasonScope.describe('[5,3]'), 'Seasons 3, 5');
    });

    test('malformed JSON falls back to the raw value', () {
      expect(SeasonScope.describe('[not json'), '[not json');
    });
  });
}
