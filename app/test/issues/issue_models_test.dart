import 'package:cantinarr/features/issues/data/issue_models.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('IssueCategory', () {
    test('maps wire values and labels', () {
      expect(IssueCategory.fromValue('wrong_audio'), IssueCategory.wrongAudio);
      expect(IssueCategory.wrongContent.value, 'wrong_content');
    });
    test('unknown value falls back to other (never crashes)', () {
      expect(IssueCategory.fromValue('something_new'), IssueCategory.other);
      expect(IssueCategory.fromValue(null), IssueCategory.other);
    });
    test('only "other" requires a reason', () {
      expect(IssueCategory.other.requiresReason, isTrue);
      expect(IssueCategory.badCopy.requiresReason, isFalse);
    });
  });

  group('IssueStatus', () {
    test('unknown server status is tolerated', () {
      expect(IssueStatus.fromValue('a_future_status'), IssueStatus.unknown);
    });
    test('terminal vs active', () {
      expect(IssueStatus.resolved.isTerminal, isTrue);
      expect(IssueStatus.investigating.isActive, isTrue);
      expect(IssueStatus.investigating.isTerminal, isFalse);
    });
  });

  group('Issue.fromJson', () {
    test('parses fields and derives a scope label', () {
      final tv = Issue.fromJson({
        'id': 7,
        'source': 'user',
        'status': 'open',
        'category': 'wrong_audio',
        'reporter_id': 3,
        'reporter_name': 'alice',
        'tmdb_id': 1399,
        'media_type': 'tv',
        'title': 'Show',
        'season_number': 2,
        'episode_number': 4,
        'detail': 'wrong language',
        'occurrences': 1,
      });
      expect(tv.id, 7);
      expect(tv.category, IssueCategory.wrongAudio);
      expect(tv.scopeLabel, 'S2·E4');

      final movie = Issue.fromJson({
        'id': 8,
        'media_type': 'movie',
        'status': 'resolved',
        'category': null,
        'tmdb_id': 27205,
      });
      expect(movie.category, isNull); // auto / no category
      expect(movie.scopeLabel, 'Movie');
      expect(movie.status, IssueStatus.resolved);
    });

    test('never claims "Movie" for a non-movie media_type', () {
      // An older server's auto issues stored the *arr service type here.
      final offContract = Issue.fromJson({
        'id': 9,
        'media_type': 'sonarr',
        'status': 'open',
        'tmdb_id': 0,
      });
      expect(offContract.scopeLabel, 'Sonarr');

      final book = Issue.fromJson({
        'id': 10,
        'media_type': 'book',
        'status': 'open',
        'tmdb_id': 0,
      });
      expect(book.scopeLabel, 'Book');
    });
  });

  group('RemediationSettings', () {
    test('round-trips, including the provider/model override fields', () {
      const s = RemediationSettings(
        enabled: true,
        autoDispatch: false,
        allowReporting: true,
        autonomy: RemediationAutonomy.propose,
        provider: 'openai',
        model: 'gpt-5',
        maxSteps: 12,
        maxTurnTokens: 4096,
        maxWallClockSecs: 300,
        maxCostMicros: 500000,
        dailyRunCap: 50,
        dailyCostCeilingMicros: 5000000,
        circuitBreakerGiveups: 5,
      );
      final back = RemediationSettings.fromJson(s.toJson());
      expect(back.provider, 'openai');
      expect(back.model, 'gpt-5');
      expect(back.autonomy, RemediationAutonomy.propose);
      expect(back.maxCostMicros, 500000);
    });

    test('blank provider/model means "inherit server default"', () {
      final s = RemediationSettings.fromJson({});
      expect(s.provider, '');
      expect(s.model, '');
      expect(s.autonomy, RemediationAutonomy.propose); // tolerant default
    });
  });
}
