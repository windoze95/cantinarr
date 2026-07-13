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
      expect(IssueStatus.observing.isTerminal, isFalse);
      expect(IssueStatus.observing.label, 'Watching the download');
      expect(IssueStatus.observing.isActive, isFalse);
      expect(IssueStatus.observing.isTracking, isTrue);
      expect(IssueStatus.observing.needsAttention, isFalse);
      expect(IssueStatus.recovering.isTracking, isTrue);
      expect(IssueStatus.recovering.label, 'Download recovery in progress');
      expect(IssueStatus.recovering.needsAttention, isFalse);
      expect(IssueStatus.awaitingApproval.needsAttention, isTrue);
    });

    test('shared status labels use requester vocabulary', () {
      final forbidden = RegExp(
        r'radarr|sonarr|agent|proposal|admin',
        caseSensitive: false,
      );
      for (final status in IssueStatus.values) {
        expect(status.label, isNot(matches(forbidden)));
      }
      for (final kind in IssueResolutionKind.values) {
        expect(kind.label, isNot(matches(forbidden)));
      }
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
        'instance_id': 'sonarr-main',
        'tmdb_id': 1399,
        'media_type': 'tv',
        'title': 'Show',
        'season_number': 2,
        'episode_number': 4,
        'detail': 'wrong language',
        'occurrences': 1,
        'resolution': '',
        'resolution_kind': '',
      });
      expect(tv.id, 7);
      expect(tv.category, IssueCategory.wrongAudio);
      expect(tv.instanceId, 'sonarr-main');
      expect(tv.scopeLabel, 'S2·E4');

      final special = Issue.fromJson({
        'id': 8,
        'status': 'observing',
        'media_type': 'tv',
        'tmdb_id': 1399,
        'title': 'Special',
        'season_number': 0,
        'episode_number': 1,
      });
      expect(special.scopeLabel, 'S0·E1');
      expect(tv.read, isTrue); // absent 'read' defaults true (older server)

      final unread = Issue.fromJson({
        'id': 11,
        'media_type': 'movie',
        'status': 'open',
        'tmdb_id': 1,
        'read': false,
      });
      expect(unread.read, isFalse); // explicit false parses through

      final movie = Issue.fromJson({
        'id': 8,
        'media_type': 'movie',
        'status': 'resolved',
        'category': null,
        'tmdb_id': 27205,
        'resolution': 'The queue cleared before a fix was approved.',
        'resolution_kind': 'arr_state_cleared',
        'closed_at': '2026-07-10T12:00:00Z',
      });
      expect(movie.category, isNull); // auto / no category
      expect(movie.scopeLabel, 'Movie');
      expect(movie.status, IssueStatus.resolved);
      expect(movie.resolutionKind, IssueResolutionKind.arrStateCleared);
      expect(movie.resolution, contains('before a fix'));
      expect(movie.closedAt, isNotNull);

      final adminCompleted = Issue.fromJson({
        'id': 13,
        'media_type': 'movie',
        'status': 'resolved',
        'resolution': 'Verified playback manually.',
        'resolution_kind': 'admin_completed',
      });
      expect(adminCompleted.resolutionKind, IssueResolutionKind.adminCompleted);
      expect(adminCompleted.resolutionKind.label, 'Completed after review');
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

    test('translates the reporter-timeout sentinel into requester copy', () {
      final issue = Issue.fromJson({
        'id': 12,
        'media_type': 'tv',
        'status': 'wont_fix',
        'resolution': 'user_unresponsive',
        'resolution_kind': 'reporter_timeout',
      });
      expect(issue.resolutionLabel, contains('No reply was received'));
      expect(issue.resolutionLabel, isNot(contains('user_unresponsive')));
    });
  });

  group('RemediationSettings', () {
    test('round-trips legacy provider and model compatibility fields', () {
      const s = RemediationSettings(
        enabled: true,
        autoDispatch: false,
        allowReporting: true,
        markResolvedAsRead: false,
        mode: RemediationMode.supervised,
        provider: 'openai',
        model: 'gpt-5',
        maxSteps: 12,
        maxTurnTokens: 4096,
        maxWallClockSecs: 300,
        dailyRunCap: 50,
        circuitBreakerGiveups: 5,
        maxUserWaitHours: 48,
        observationMinMinutes: 12,
        observationQuietMinutes: 7,
        observationSettleMinutes: 3,
      );
      final back = RemediationSettings.fromJson(s.toJson());
      expect(back.provider, 'openai');
      expect(back.model, 'gpt-5');
      expect(back.mode, RemediationMode.supervised);
      expect(back.maxUserWaitHours, 48);
      expect(back.observationMinMinutes, 12);
      expect(back.observationQuietMinutes, 7);
      expect(back.observationSettleMinutes, 3);
      expect(back.markResolvedAsRead, isFalse); // explicit false round-trips
      expect(back.toJson(), isNot(contains('max_cost_micros')));
      expect(back.toJson(), isNot(contains('daily_cost_ceiling_micros')));
    });

    test('blank provider follows shared selection and model inherits', () {
      final s = RemediationSettings.fromJson({});
      expect(s.provider, '');
      expect(s.model, '');
      expect(s.mode, RemediationMode.supervised); // tolerant safe default
      expect(s.maxUserWaitHours, 72);
      expect(s.observationMinMinutes, 10);
      expect(s.observationQuietMinutes, 5);
      expect(s.observationSettleMinutes, 2);
      expect(s.markResolvedAsRead, isTrue); // defaults on when absent
    });

    test('ignores legacy cost controls instead of sending them back', () {
      final s = RemediationSettings.fromJson({
        'max_cost_micros': 500000,
        'daily_cost_ceiling_micros': 5000000,
      });

      expect(s.toJson(), isNot(contains('max_cost_micros')));
      expect(s.toJson(), isNot(contains('daily_cost_ceiling_micros')));
    });
  });
}
