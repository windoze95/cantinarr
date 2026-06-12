/// Models and formatters for the Tautulli module, matching the backend's
/// normalized /api/tautulli payloads.
library;

/// Human-readable bandwidth from a kbps figure.
String formatBandwidthKbps(num kbps) {
  if (kbps <= 0) return '0 kbps';
  if (kbps >= 1000) {
    final mbps = kbps / 1000;
    final decimals = mbps >= 100 ? 0 : 1;
    return '${mbps.toStringAsFixed(decimals)} Mbps';
  }
  return '${kbps.round()} kbps';
}

/// Current Plex activity: stream count, total bandwidth and per-stream info.
class TautulliActivity {
  final int streamCount;
  final int totalBandwidthKbps;
  final List<TautulliStream> streams;

  const TautulliActivity({
    this.streamCount = 0,
    this.totalBandwidthKbps = 0,
    this.streams = const [],
  });

  factory TautulliActivity.fromJson(Map<String, dynamic> json) =>
      TautulliActivity(
        streamCount: (json['stream_count'] as num?)?.toInt() ?? 0,
        totalBandwidthKbps:
            (json['total_bandwidth_kbps'] as num?)?.toInt() ?? 0,
        streams: (json['streams'] as List<dynamic>? ?? [])
            .map((s) => TautulliStream.fromJson(s as Map<String, dynamic>))
            .toList(),
      );

  String get totalBandwidthFormatted => formatBandwidthKbps(totalBandwidthKbps);
}

/// One active stream.
class TautulliStream {
  final String user;
  final String title;
  final String fullTitle;
  final String player;
  final String product;
  final String state;
  final int progressPercent;
  final String quality;
  final String streamType;
  final int bandwidthKbps;

  const TautulliStream({
    this.user = '',
    this.title = '',
    this.fullTitle = '',
    this.player = '',
    this.product = '',
    this.state = '',
    this.progressPercent = 0,
    this.quality = '',
    this.streamType = '',
    this.bandwidthKbps = 0,
  });

  factory TautulliStream.fromJson(Map<String, dynamic> json) => TautulliStream(
        user: json['user'] as String? ?? '',
        title: json['title'] as String? ?? '',
        fullTitle: json['full_title'] as String? ?? '',
        player: json['player'] as String? ?? '',
        product: json['product'] as String? ?? '',
        state: json['state'] as String? ?? '',
        progressPercent: (json['progress_percent'] as num?)?.toInt() ?? 0,
        quality: json['quality'] as String? ?? '',
        streamType: json['stream_type'] as String? ?? '',
        bandwidthKbps: (json['bandwidth_kbps'] as num?)?.toInt() ?? 0,
      );

  /// Title for display: prefer the full title (includes show/episode).
  String get displayTitle => fullTitle.isNotEmpty ? fullTitle : title;

  double get progressFraction => (progressPercent / 100).clamp(0.0, 1.0);

  String get bandwidthFormatted => formatBandwidthKbps(bandwidthKbps);

  bool get isPaused => state.toLowerCase() == 'paused';
  bool get isBuffering => state.toLowerCase() == 'buffering';

  bool get isTranscode => streamType.toLowerCase().contains('transcode');

  /// Badge label for the stream decision.
  String get streamTypeLabel {
    final s = streamType.toLowerCase();
    if (s.contains('transcode')) return 'Transcode';
    if (s.contains('copy')) return 'Direct Stream';
    return 'Direct Play';
  }
}

/// One watch-history entry.
class TautulliHistoryItem {
  final String user;
  final String fullTitle;
  final DateTime? date;
  final int durationSeconds;
  final int percentComplete;
  final String player;
  final String platform;

  const TautulliHistoryItem({
    this.user = '',
    this.fullTitle = '',
    this.date,
    this.durationSeconds = 0,
    this.percentComplete = 0,
    this.player = '',
    this.platform = '',
  });

  factory TautulliHistoryItem.fromJson(Map<String, dynamic> json) {
    final dateRaw = json['date'] as String? ?? '';
    return TautulliHistoryItem(
      user: json['user'] as String? ?? '',
      fullTitle: json['full_title'] as String? ?? '',
      date: dateRaw.isEmpty ? null : DateTime.tryParse(dateRaw),
      durationSeconds: (json['duration_seconds'] as num?)?.toInt() ?? 0,
      percentComplete: (json['percent_complete'] as num?)?.toInt() ?? 0,
      player: json['player'] as String? ?? '',
      platform: json['platform'] as String? ?? '',
    );
  }
}

/// A ranked play-count entry (movie, show or user).
class TautulliStatEntry {
  final String label;
  final int plays;

  const TautulliStatEntry({required this.label, this.plays = 0});

  factory TautulliStatEntry.fromJson(Map<String, dynamic> json) =>
      TautulliStatEntry(
        label: json['title'] as String? ?? json['user'] as String? ?? '',
        plays: (json['plays'] as num?)?.toInt() ?? 0,
      );
}

/// Watch statistics over a period: top movies, shows and users.
class TautulliStats {
  final List<TautulliStatEntry> topMovies;
  final List<TautulliStatEntry> topShows;
  final List<TautulliStatEntry> topUsers;

  const TautulliStats({
    this.topMovies = const [],
    this.topShows = const [],
    this.topUsers = const [],
  });

  factory TautulliStats.fromJson(Map<String, dynamic> json) => TautulliStats(
        topMovies: (json['top_movies'] as List<dynamic>? ?? [])
            .map((e) => TautulliStatEntry.fromJson(e as Map<String, dynamic>))
            .toList(),
        topShows: (json['top_shows'] as List<dynamic>? ?? [])
            .map((e) => TautulliStatEntry.fromJson(e as Map<String, dynamic>))
            .toList(),
        topUsers: (json['top_users'] as List<dynamic>? ?? [])
            .map((e) => TautulliStatEntry.fromJson(e as Map<String, dynamic>))
            .toList(),
      );
}
