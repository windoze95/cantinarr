import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';

/// A kids account's content limits, as the admin edits them. The server
/// enforces them on every title surface; the app only edits and displays.
class ContentPolicy {
  final String maxMovieRating;
  final String maxTvRating;
  final String ratingRegion;
  final bool blockUnrated;
  final List<int> blockedMovieGenres;
  final List<int> blockedTvGenres;

  const ContentPolicy({
    required this.maxMovieRating,
    required this.maxTvRating,
    required this.ratingRegion,
    this.blockUnrated = true,
    this.blockedMovieGenres = const [],
    this.blockedTvGenres = const [],
  });

  factory ContentPolicy.fromJson(Map<String, dynamic> json) => ContentPolicy(
        maxMovieRating: json['max_movie_rating'] as String? ?? '',
        maxTvRating: json['max_tv_rating'] as String? ?? '',
        ratingRegion: json['rating_region'] as String? ?? 'US',
        blockUnrated: json['block_unrated'] as bool? ?? true,
        blockedMovieGenres: _ids(json['blocked_movie_genres']),
        blockedTvGenres: _ids(json['blocked_tv_genres']),
      );

  static List<int> _ids(dynamic raw) => (raw as List<dynamic>?)
          ?.map((e) => (e as num).toInt())
          .toList() ??
      const [];

  /// Every key is always sent: the PUT creates or replaces the whole row.
  Map<String, dynamic> toJson() => {
        'max_movie_rating': maxMovieRating,
        'max_tv_rating': maxTvRating,
        'rating_region': ratingRegion,
        'block_unrated': blockUnrated,
        'blocked_movie_genres': blockedMovieGenres,
        'blocked_tv_genres': blockedTvGenres,
      };

  @override
  bool operator ==(Object other) =>
      other is ContentPolicy &&
      other.maxMovieRating == maxMovieRating &&
      other.maxTvRating == maxTvRating &&
      other.ratingRegion == ratingRegion &&
      other.blockUnrated == blockUnrated &&
      listEquals(other.blockedMovieGenres, blockedMovieGenres) &&
      listEquals(other.blockedTvGenres, blockedTvGenres);

  @override
  int get hashCode => Object.hash(
        maxMovieRating,
        maxTvRating,
        ratingRegion,
        blockUnrated,
        Object.hashAll(blockedMovieGenres),
        Object.hashAll(blockedTvGenres),
      );
}

/// One entry of a region's rating scheme, as TMDB lists it.
class CertificationOption {
  final String certification;
  final int order;
  final String meaning;

  /// The server's suggested starting point for a new kids account.
  final bool isDefault;

  const CertificationOption({
    required this.certification,
    required this.order,
    this.meaning = '',
    this.isDefault = false,
  });

  factory CertificationOption.fromJson(Map<String, dynamic> json) =>
      CertificationOption(
        certification: json['certification'] as String? ?? '',
        order: (json['order'] as num?)?.toInt() ?? 0,
        meaning: json['meaning'] as String? ?? '',
        isDefault: json['default'] as bool? ?? false,
      );
}

/// Every region's movie and TV schemes, the way the editor offers them.
/// Order-0 entries (TMDB's unrated placeholder) are never a cap and are
/// dropped on the way in.
class CertificationCatalog {
  final Map<String, List<CertificationOption>> movie;
  final Map<String, List<CertificationOption>> tv;

  const CertificationCatalog({required this.movie, required this.tv});

  factory CertificationCatalog.fromJson(Map<String, dynamic> json) =>
      CertificationCatalog(
        movie: _schemes(json['movie']),
        tv: _schemes(json['tv']),
      );

  static Map<String, List<CertificationOption>> _schemes(dynamic raw) {
    final out = <String, List<CertificationOption>>{};
    if (raw is! Map) return out;
    for (final entry in raw.entries) {
      final options = (entry.value as List<dynamic>?)
              ?.whereType<Map<String, dynamic>>()
              .map(CertificationOption.fromJson)
              .where((o) => o.order > 0 && o.certification.isNotEmpty)
              .toList() ??
          const <CertificationOption>[];
      options.sort((a, b) => a.order.compareTo(b.order));
      if (options.isNotEmpty) out[entry.key as String] = options;
    }
    return out;
  }

  /// The regions both schemes know: one region serves both caps.
  List<String> get regions =>
      movie.keys.where(tv.containsKey).toList()..sort();

  List<CertificationOption> movieFor(String region) =>
      movie[region] ?? const [];

  List<CertificationOption> tvFor(String region) => tv[region] ?? const [];

  /// Where a fresh kids account starts in one scheme: the server's
  /// suggestion when it marked one, else the second-lowest entry (the
  /// mildest rating that is not "for the very youngest").
  static CertificationOption? defaultFor(List<CertificationOption> options) {
    for (final option in options) {
      if (option.isDefault) return option;
    }
    if (options.length >= 2) return options[1];
    return options.isEmpty ? null : options.first;
  }
}

/// Admin API client for kids accounts.
class ContentPolicyService {
  final Dio _dio;

  ContentPolicyService({required Dio backendDio}) : _dio = backendDio;

  /// The rating schemes. Null means the server is too old to have kids
  /// accounts at all (the route 404s), which hides the section; every other
  /// failure throws so the editor can say the list could not be read.
  Future<CertificationCatalog?> certifications() async {
    try {
      final resp = await _dio.get('/api/admin/certifications');
      final data = resp.data;
      if (data is! Map<String, dynamic>) return null;
      return CertificationCatalog.fromJson(data);
    } on DioException catch (e) {
      if (e.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  /// The user's policy, or null when the account is not a kids account.
  Future<ContentPolicy?> getUserPolicy(int userId) async {
    try {
      final resp = await _dio.get('/api/admin/users/$userId/content-policy');
      return ContentPolicy.fromJson(resp.data as Map<String, dynamic>);
    } on DioException catch (e) {
      if (e.response?.statusCode == 404) return null;
      rethrow;
    }
  }

  Future<ContentPolicy> updateUserPolicy(
      int userId, ContentPolicy policy) async {
    final resp = await _dio.put('/api/admin/users/$userId/content-policy',
        data: policy.toJson());
    return ContentPolicy.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteUserPolicy(int userId) async {
    await _dio.delete('/api/admin/users/$userId/content-policy');
  }
}
