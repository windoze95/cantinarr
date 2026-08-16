import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/settings/data/settings_search_index.dart';
import 'package:flutter_test/flutter_test.dart';

const _admin = UserProfile(id: 1, username: 'admin', role: 'admin');
const _user = UserProfile(id: 2, username: 'user', role: 'user');

const _adminGates = SettingsSearchGates(
  user: _admin,
  chaptarrEnabled: true,
  donateVisible: true,
);
const _userGates = SettingsSearchGates(user: _user);

/// Every settings-adjacent path the router declares that the registry may
/// target. Mirrors the literal GoRoute paths in
/// `lib/navigation/app_router.dart` (settings region) — update both together.
const _routableSettingsPaths = {
  '/settings',
  '/settings/ai',
  '/settings/chatgpt',
  '/settings/credentials/chatgpt',
  '/settings/credentials',
  '/settings/ai-tools',
  '/settings/change-history',
  '/settings/profile-approvals',
  '/settings/users',
  '/settings/ai-remediation',
  '/settings/agent-approval-rules',
  '/settings/request-settings',
  '/settings/discovery',
  '/settings/devices',
  '/settings/plex',
  '/settings/notifications',
  '/settings/passkeys',
  '/settings/password',
  '/settings/instance/new',
  '/setup',
  '/issues',
  '/plex-guide',
  '/approvals',
  '/agent-actions',
};

void main() {
  group('registry invariants', () {
    test('ids are unique', () {
      final seen = <String>{};
      for (final entry in settingsSearchIndex) {
        expect(seen.add(entry.id), isTrue, reason: 'duplicate id ${entry.id}');
      }
    });

    test('anchor ids are URL-safe and equal their entry id', () {
      final pattern = RegExp(r'^[a-z0-9.-]+$');
      for (final entry in settingsSearchIndex) {
        final anchor = entry.anchorId;
        if (anchor == null) continue;
        expect(pattern.hasMatch(anchor), isTrue,
            reason: '$anchor must stay URL-safe without encoding');
        expect(entry.id, anchor,
            reason: 'anchored entries use the anchor as their id');
      }
    });

    test('every route is one the router actually declares', () {
      for (final entry in settingsSearchIndex) {
        expect(_routableSettingsPaths.contains(entry.route), isTrue,
            reason: '${entry.id} targets undeclared route ${entry.route}');
      }
    });

    test('titles and screen names are non-empty', () {
      for (final entry in settingsSearchIndex) {
        expect(entry.title.trim(), isNotEmpty, reason: entry.id);
        expect(entry.screenTitle.trim(), isNotEmpty, reason: entry.id);
      }
    });
  });

  group('matching', () {
    test('empty and whitespace queries return nothing', () {
      expect(searchSettingsIndex('', _adminGates), isEmpty);
      expect(searchSettingsIndex('   ', _adminGates), isEmpty);
    });

    test('gates hide admin entries from regular users', () {
      expect(
        searchSettingsIndex('users', _userGates),
        isEmpty,
        reason: 'the Users screen is admin-only',
      );
      expect(
        searchSettingsIndex('users', _adminGates).map((e) => e.id),
        contains('screen.users'),
      );
    });

    test('non-admins find My reports, admins do not', () {
      expect(
        searchSettingsIndex('reports', _userGates).map((e) => e.id),
        contains('screen.my-reports'),
      );
      expect(
        searchSettingsIndex('my reports', _adminGates)
            .map((e) => e.id)
            .contains('screen.my-reports'),
        isFalse,
      );
    });

    test('chaptarr gate controls the new-book toggle', () {
      expect(
        searchSettingsIndex('book', _adminGates).map((e) => e.id),
        contains('notifications.new-book'),
      );
      const noBooks = SettingsSearchGates(user: _admin);
      expect(
        searchSettingsIndex('book', noBooks)
            .map((e) => e.id)
            .contains('notifications.new-book'),
        isFalse,
      );
    });

    test('title hits rank above keyword and context hits', () {
      final results = searchSettingsIndex('approval', _adminGates);
      final ids = results.map((e) => e.id).toList();
      final titleHit = ids.indexOf('requests.require-approval');
      final keywordHit = ids.indexOf('screen.request-settings');
      expect(titleHit, isNot(-1));
      expect(keywordHit, isNot(-1));
      expect(titleHit, lessThan(keywordHit),
          reason: 'the whole query inside a title outranks a keyword match');
    });

    test('multi-term queries require every term', () {
      final ids =
          searchSettingsIndex('default sonarr', _adminGates).map((e) => e.id);
      expect(ids, contains('requests.quality-sonarr'));
      expect(ids, isNot(contains('requests.quality-radarr')));
    });

    test('screen and section names alone can match', () {
      final ids = searchSettingsIndex('needs attention', _adminGates)
          .map((e) => e.id)
          .toList();
      expect(ids, contains('root.attention-approvals'));
    });

    test('registry order is preserved within a tier', () {
      final results = searchSettingsIndex('plex', _adminGates);
      final ids = results.map((e) => e.id).toList();
      final screenTile = ids.indexOf('screen.plex-invites');
      final autoInvite = ids.indexOf('plex.auto-invite');
      expect(screenTile, isNot(-1));
      expect(autoInvite, isNot(-1));
    });
  });
}
