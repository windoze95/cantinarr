import 'package:cantinarr/core/models/user_profile.dart';
import 'package:flutter_test/flutter_test.dart';

/// The kids-account fields ride the profile through the persisted session:
/// toJson then fromJson must keep them, and an older server that sends
/// neither must read as an unrestricted account.
void main() {
  test('kids account fields round-trip through JSON', () {
    final profile = UserProfile.fromJson(const {
      'id': 7,
      'username': 'kid',
      'role': 'user',
      'child': true,
      'content_limits': {
        'max_movie_rating': 'PG',
        'max_tv_rating': 'TV-PG',
        'rating_region': 'US',
      },
    });
    expect(profile.child, isTrue);
    expect(profile.contentLimits?.maxMovieRating, 'PG');
    expect(profile.contentLimits?.maxTvRating, 'TV-PG');
    expect(profile.contentLimits?.ratingRegion, 'US');

    final restored = UserProfile.fromJson(profile.toJson());
    expect(restored.child, isTrue);
    expect(restored.contentLimits?.maxMovieRating, 'PG');
    expect(restored.contentLimits?.ratingRegion, 'US');

    final kept = profile.copyWith(hasPassword: true);
    expect(kept.child, isTrue);
    expect(kept.contentLimits?.maxTvRating, 'TV-PG');
  });

  test('absent or null kids fields read as an unrestricted account', () {
    final legacy = UserProfile.fromJson(const {
      'id': 1,
      'username': 'adult',
      'role': 'user',
    });
    expect(legacy.child, isFalse);
    expect(legacy.contentLimits, isNull);
    expect(legacy.toJson()['child'], isFalse);
    expect(legacy.toJson().containsKey('content_limits'), isFalse);

    final explicit = UserProfile.fromJson(const {
      'id': 1,
      'username': 'adult',
      'role': 'user',
      'child': false,
      'content_limits': null,
    });
    expect(explicit.child, isFalse);
    expect(explicit.contentLimits, isNull);
  });
}
