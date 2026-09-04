import 'package:cantinarr/features/notifications/notification_prefs.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('NotificationPrefs', () {
    test('toJson carries every category the server knows', () {
      // The server's PUT replaces the full preference row and treats missing
      // keys as false, so omitting any category here would silently disable
      // it whenever the user saves an unrelated toggle.
      final json = const NotificationPrefs(
        requestDecision: true,
        requestPending: true,
        newMovie: true,
        newEpisode: true,
      ).toJson();
      expect(
        json.keys,
        containsAll(const [
          'request_decision',
          'request_pending',
          'new_movie',
          'new_episode',
          'new_book',
          'new_music',
          'issue_created',
          'agent_action_pending',
          'plex_access_request',
          'plex_invite_sent',
          'content_upgraded',
        ]),
      );
    });

    test('new_book defaults on when an older server omits the key', () {
      final prefs = NotificationPrefs.fromJson(const {
        'request_decision': false,
        'request_pending': true,
        'new_movie': true,
        'new_episode': true,
      });
      expect(prefs.newBook, isTrue);
    });

    test('new_book round-trips through json and copyWith', () {
      final prefs = NotificationPrefs.fromJson(const {'new_book': false});
      expect(prefs.newBook, isFalse);
      expect(prefs.toJson()['new_book'], isFalse);
      expect(prefs.copyWith(newBook: true).newBook, isTrue);
      // copyWith without the flag preserves the current value.
      expect(prefs.copyWith(newMovie: true).newBook, isFalse);
    });

    test('new_music defaults on when an older server omits the key', () {
      final prefs = NotificationPrefs.fromJson(const {
        'request_decision': false,
        'request_pending': true,
        'new_movie': true,
        'new_episode': true,
      });
      expect(prefs.newMusic, isTrue);
    });

    test('new_music round-trips through json and copyWith', () {
      final prefs = NotificationPrefs.fromJson(const {'new_music': false});
      expect(prefs.newMusic, isFalse);
      expect(prefs.toJson()['new_music'], isFalse);
      expect(prefs.copyWith(newMusic: true).newMusic, isTrue);
      // copyWith without the flag preserves the current value.
      expect(prefs.copyWith(newMovie: true).newMusic, isFalse);
    });

    test('content_upgraded defaults OFF when the key is absent', () {
      // Unlike the other admin categories, the server default is off —
      // mirroring on here would silently opt an admin in the first time they
      // saved any unrelated toggle against an older server.
      final prefs = NotificationPrefs.fromJson(const {
        'request_decision': false,
        'request_pending': true,
        'new_movie': true,
        'new_episode': true,
      });
      expect(prefs.contentUpgraded, isFalse);
    });

    test('content_upgraded round-trips through json and copyWith', () {
      final prefs =
          NotificationPrefs.fromJson(const {'content_upgraded': true});
      expect(prefs.contentUpgraded, isTrue);
      expect(prefs.toJson()['content_upgraded'], isTrue);
      expect(prefs.copyWith(contentUpgraded: false).contentUpgraded, isFalse);
      // copyWith without the flag preserves the current value.
      expect(prefs.copyWith(newMovie: true).contentUpgraded, isTrue);
    });
  });
}
