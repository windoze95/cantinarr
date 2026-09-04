/// Models and formatters for the Monitoring module, matching the backend's
/// normalized watch-history payloads. Tautulli (Plex) and Tracearr (Plex,
/// Jellyfin, Emby) answer with the same shapes; the server, server-type and
/// media-type fields are empty when a provider does not know them.
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

String _serverTypeLabel(String serverType) => switch (serverType) {
      'plex' => 'Plex',
      'jellyfin' => 'Jellyfin',
      'emby' => 'Emby',
      _ => serverType,
    };

/// The server a stream or play happened on: its name when the provider
/// gave one, else its kind, else nothing.
String _serverLabel(String server, String serverType) =>
    server.isNotEmpty ? server : _serverTypeLabel(serverType);

/// A badge for anything that is not plain video: movies and episodes are the
/// default and get no label.
String _mediaTypeLabel(String mediaType) => switch (mediaType) {
      'track' => 'Music',
      'live' => 'Live TV',
      'photo' => 'Photo',
      'trailer' => 'Trailer',
      _ => '',
    };

/// What an answer was computed from, so an empty list reads as "nothing was
/// recorded" rather than "this could not be seen".
class Coverage {
  final String note;
  final bool truncated;

  const Coverage({this.note = '', this.truncated = false});

  factory Coverage.fromJson(Map<String, dynamic>? json) => Coverage(
        note: json?['note'] as String? ?? '',
        truncated: json?['truncated'] as bool? ?? false,
      );
}

/// Current activity: stream count, total bandwidth and per-stream info.
class ActivitySnapshot {
  final int streamCount;
  final int totalBandwidthKbps;
  final List<ActiveStream> streams;

  const ActivitySnapshot({
    this.streamCount = 0,
    this.totalBandwidthKbps = 0,
    this.streams = const [],
  });

  factory ActivitySnapshot.fromJson(Map<String, dynamic> json) =>
      ActivitySnapshot(
        streamCount: (json['stream_count'] as num?)?.toInt() ?? 0,
        totalBandwidthKbps:
            (json['total_bandwidth_kbps'] as num?)?.toInt() ?? 0,
        streams: (json['streams'] as List<dynamic>? ?? [])
            .map((s) => ActiveStream.fromJson(s as Map<String, dynamic>))
            .toList(),
      );

  String get totalBandwidthFormatted => formatBandwidthKbps(totalBandwidthKbps);
}

/// One active stream.
class ActiveStream {
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
  final String mediaType;
  final String server;
  final String serverType;

  const ActiveStream({
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
    this.mediaType = '',
    this.server = '',
    this.serverType = '',
  });

  factory ActiveStream.fromJson(Map<String, dynamic> json) => ActiveStream(
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
        mediaType: json['media_type'] as String? ?? '',
        server: json['server'] as String? ?? '',
        serverType: json['server_type'] as String? ?? '',
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

  String get serverLabel => _serverLabel(server, serverType);
  String get mediaTypeLabel => _mediaTypeLabel(mediaType);
}

/// One watch-history entry.
class HistoryItem {
  final String user;
  final String fullTitle;
  final DateTime? date;
  final int durationSeconds;
  final int percentComplete;
  final String player;
  final String platform;
  final String mediaType;
  final String server;
  final String serverType;

  const HistoryItem({
    this.user = '',
    this.fullTitle = '',
    this.date,
    this.durationSeconds = 0,
    this.percentComplete = 0,
    this.player = '',
    this.platform = '',
    this.mediaType = '',
    this.server = '',
    this.serverType = '',
  });

  factory HistoryItem.fromJson(Map<String, dynamic> json) {
    final dateRaw = json['date'] as String? ?? '';
    return HistoryItem(
      user: json['user'] as String? ?? '',
      fullTitle: json['full_title'] as String? ?? '',
      date: dateRaw.isEmpty ? null : DateTime.tryParse(dateRaw),
      durationSeconds: (json['duration_seconds'] as num?)?.toInt() ?? 0,
      percentComplete: (json['percent_complete'] as num?)?.toInt() ?? 0,
      player: json['player'] as String? ?? '',
      platform: json['platform'] as String? ?? '',
      mediaType: json['media_type'] as String? ?? '',
      server: json['server'] as String? ?? '',
      serverType: json['server_type'] as String? ?? '',
    );
  }

  String get serverLabel => _serverLabel(server, serverType);
}

/// Recent plays, newest first, with what the list was computed from.
class HistorySnapshot {
  final List<HistoryItem> items;
  final Coverage coverage;

  const HistorySnapshot({this.items = const [], this.coverage = const Coverage()});

  factory HistorySnapshot.fromJson(Map<String, dynamic> json) =>
      HistorySnapshot(
        items: (json['items'] as List<dynamic>? ?? [])
            .map((i) => HistoryItem.fromJson(i as Map<String, dynamic>))
            .toList(),
        coverage:
            Coverage.fromJson(json['coverage'] as Map<String, dynamic>?),
      );
}

/// A ranked play-count entry (movie, show or user).
class StatEntry {
  final String label;
  final int plays;

  const StatEntry({required this.label, this.plays = 0});

  factory StatEntry.fromJson(Map<String, dynamic> json) => StatEntry(
        label: json['title'] as String? ?? json['user'] as String? ?? '',
        plays: (json['plays'] as num?)?.toInt() ?? 0,
      );
}

/// Watch statistics over a period: top movies, shows and users.
class StatsSnapshot {
  final List<StatEntry> topMovies;
  final List<StatEntry> topShows;
  final List<StatEntry> topUsers;
  final Coverage coverage;

  const StatsSnapshot({
    this.topMovies = const [],
    this.topShows = const [],
    this.topUsers = const [],
    this.coverage = const Coverage(),
  });

  factory StatsSnapshot.fromJson(Map<String, dynamic> json) => StatsSnapshot(
        topMovies: (json['top_movies'] as List<dynamic>? ?? [])
            .map((e) => StatEntry.fromJson(e as Map<String, dynamic>))
            .toList(),
        topShows: (json['top_shows'] as List<dynamic>? ?? [])
            .map((e) => StatEntry.fromJson(e as Map<String, dynamic>))
            .toList(),
        topUsers: (json['top_users'] as List<dynamic>? ?? [])
            .map((e) => StatEntry.fromJson(e as Map<String, dynamic>))
            .toList(),
        coverage:
            Coverage.fromJson(json['coverage'] as Map<String, dynamic>?),
      );
}
