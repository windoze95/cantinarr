import 'package:dio/dio.dart';

/// A feed the headline discovery rows can be pointed at.
class DiscoverySource {
  final String value;
  final String label;
  final String description;

  /// Whether the picker should mark this as the option we suggest. Trakt's
  /// feed is ranked by what people are watching right now rather than by
  /// metadata engagement, so it is the upgrade — and the server already
  /// defaults to it once a Trakt client ID exists. The tag says out loud what
  /// the default is doing quietly, and stays on the locked Trakt row as the
  /// reason to go configure the credential.
  final bool recommended;

  const DiscoverySource({
    required this.value,
    required this.label,
    required this.description,
    this.recommended = false,
  });

  /// Trakt is optional, so it can be listed but unselectable.
  bool get requiresTrakt => value == 'trakt_trending';
}

/// Copy for every source the server can offer, keyed by its stored value. The
/// server owns the list; this only decides how each option reads.
const _sourceCopy = <String, DiscoverySource>{
  'tmdb_trending': DiscoverySource(
    value: 'tmdb_trending',
    label: 'Trending this week (TMDB)',
    description: 'What TMDB users are engaging with over the past week.',
  ),
  'trakt_trending': DiscoverySource(
    value: 'trakt_trending',
    label: 'Trending now (Trakt)',
    description: 'Ranked by how many people are watching right now.',
    recommended: true,
  ),
  'tmdb_popular': DiscoverySource(
    value: 'tmdb_popular',
    label: 'All-time popular (TMDB)',
    description:
        'TMDB\'s lifetime popularity score. Long-running catalogue shows and '
        'nightly talk shows dominate it.',
  ),
};

/// The admin's discovery preferences plus the choices the server offers.
class DiscoverySettings {
  final String source;
  final bool englishOnly;
  final List<DiscoverySource> sources;
  final bool traktConfigured;

  const DiscoverySettings({
    required this.source,
    required this.englishOnly,
    required this.sources,
    required this.traktConfigured,
  });

  factory DiscoverySettings.fromJson(Map<String, dynamic> json) {
    final values = ((json['sources'] as List?) ?? const [])
        .map((e) => e.toString())
        .toList();
    return DiscoverySettings(
      source: json['source'] as String? ?? '',
      englishOnly: json['english_only'] as bool? ?? false,
      // An unrecognized value still gets an entry so the picker can show it
      // rather than silently dropping a source this build does not know.
      sources: [
        for (final value in values)
          _sourceCopy[value] ??
              DiscoverySource(
                value: value,
                label: value,
                description: '',
              ),
      ],
      traktConfigured: json['trakt_configured'] as bool? ?? false,
    );
  }

  /// Whether the given source can be selected right now.
  bool isSelectable(DiscoverySource source) =>
      !source.requiresTrakt || traktConfigured;

  DiscoverySettings copyWith({String? source, bool? englishOnly}) =>
      DiscoverySettings(
        source: source ?? this.source,
        englishOnly: englishOnly ?? this.englishOnly,
        sources: sources,
        traktConfigured: traktConfigured,
      );
}

/// Admin API client for the discovery row preferences.
class DiscoverySettingsService {
  final Dio _dio;

  DiscoverySettingsService({required Dio backendDio}) : _dio = backendDio;

  Future<DiscoverySettings> get() async {
    final resp = await _dio.get('/api/admin/discovery-settings');
    return DiscoverySettings.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<DiscoverySettings> update(DiscoverySettings settings) async {
    final resp = await _dio.put(
      '/api/admin/discovery-settings',
      data: {
        'source': settings.source,
        'english_only': settings.englishOnly,
      },
    );
    return DiscoverySettings.fromJson(resp.data as Map<String, dynamic>);
  }
}
