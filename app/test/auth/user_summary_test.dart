import 'package:cantinarr/features/auth/data/auth_service.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('UserSummary reads the child flag and defaults it off', () {
    final kid = UserSummary.fromJson(const {
      'id': 7,
      'username': 'kid',
      'role': 'user',
      'permissions': ['media:discover'],
      'created_at': '2026-09-03T00:00:00Z',
      'device_count': 1,
      'has_password': false,
      'password_enabled': false,
      'passkey_enabled': false,
      'has_pending_invite': false,
      'child': true,
    });
    expect(kid.child, isTrue);

    final legacy = UserSummary.fromJson(const {
      'id': 8,
      'username': 'adult',
      'role': 'user',
    });
    expect(legacy.child, isFalse);
  });
}
