/// Models and formatters for the Downloads module (SABnzbd / qBittorrent),
/// matching the backend's normalized /api/downloads payloads.
library;

String formatBytes(num bytes) {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var value = bytes.toDouble();
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  final decimals = value >= 100 || unit == 0 ? 0 : 1;
  return '${value.toStringAsFixed(decimals)} ${units[unit]}';
}

String formatSpeed(num bytesPerSecond) {
  if (bytesPerSecond <= 0) return '0 B/s';
  return '${formatBytes(bytesPerSecond)}/s';
}

/// qBittorrent reports 8640000 seconds when the ETA is unknown.
const int _maxEtaSeconds = 8640000;

/// Human-readable ETA; empty string when unknown/not applicable.
String formatEta(int seconds) {
  if (seconds <= 0 || seconds >= _maxEtaSeconds) return '';
  final d = Duration(seconds: seconds);
  if (d.inDays >= 1) return '${d.inDays}d ${d.inHours % 24}h';
  if (d.inHours >= 1) return '${d.inHours}h ${d.inMinutes % 60}m';
  if (d.inMinutes >= 1) return '${d.inMinutes}m ${d.inSeconds % 60}s';
  return '${d.inSeconds}s';
}

/// The download client queue: global state plus per-item entries.
class DownloadsQueue {
  final bool paused;
  final int speedBps;
  final List<DownloadQueueItem> items;

  const DownloadsQueue({
    this.paused = false,
    this.speedBps = 0,
    this.items = const [],
  });

  factory DownloadsQueue.fromJson(Map<String, dynamic> json) => DownloadsQueue(
        paused: json['paused'] as bool? ?? false,
        speedBps: (json['speed_bps'] as num?)?.toInt() ?? 0,
        items: (json['items'] as List<dynamic>? ?? [])
            .map((i) => DownloadQueueItem.fromJson(i as Map<String, dynamic>))
            .toList(),
      );

  String get speedFormatted => formatSpeed(speedBps);
}

/// One item in the download client queue.
class DownloadQueueItem {
  final String id;
  final String name;
  final int sizeBytes;
  final int sizeLeftBytes;

  /// Percentage 0-100 as reported by the backend.
  final double progress;
  final int speedBps;
  final int etaSeconds;
  final String status;
  final String category;

  const DownloadQueueItem({
    required this.id,
    required this.name,
    this.sizeBytes = 0,
    this.sizeLeftBytes = 0,
    this.progress = 0,
    this.speedBps = 0,
    this.etaSeconds = 0,
    this.status = '',
    this.category = '',
  });

  factory DownloadQueueItem.fromJson(Map<String, dynamic> json) =>
      DownloadQueueItem(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? 'Unknown',
        sizeBytes: (json['size_bytes'] as num?)?.toInt() ?? 0,
        sizeLeftBytes: (json['size_left_bytes'] as num?)?.toInt() ?? 0,
        progress: (json['progress'] as num?)?.toDouble() ?? 0,
        speedBps: (json['speed_bps'] as num?)?.toInt() ?? 0,
        etaSeconds: (json['eta_seconds'] as num?)?.toInt() ?? 0,
        status: json['status'] as String? ?? '',
        category: json['category'] as String? ?? '',
      );

  /// Progress as a 0.0-1.0 fraction for progress bars.
  double get progressFraction => (progress / 100).clamp(0.0, 1.0);

  String get sizeFormatted => formatBytes(sizeBytes);
  String get downloadedFormatted =>
      formatBytes((sizeBytes - sizeLeftBytes).clamp(0, sizeBytes));
  String get speedFormatted => formatSpeed(speedBps);
  String get etaFormatted => formatEta(etaSeconds);

  bool get isPaused {
    final s = status.toLowerCase();
    return s.contains('paused') || s.contains('stopped');
  }
}

/// One completed/failed entry in the download client history.
class DownloadHistoryItem {
  final String name;
  final String status;
  final int sizeBytes;
  final DateTime? completedAt;
  final String category;
  final String error;

  const DownloadHistoryItem({
    required this.name,
    this.status = '',
    this.sizeBytes = 0,
    this.completedAt,
    this.category = '',
    this.error = '',
  });

  factory DownloadHistoryItem.fromJson(Map<String, dynamic> json) {
    final completedRaw = json['completed_at'] as String? ?? '';
    return DownloadHistoryItem(
      name: json['name'] as String? ?? 'Unknown',
      status: json['status'] as String? ?? '',
      sizeBytes: (json['size_bytes'] as num?)?.toInt() ?? 0,
      completedAt:
          completedRaw.isEmpty ? null : DateTime.tryParse(completedRaw),
      category: json['category'] as String? ?? '',
      error: json['error'] as String? ?? '',
    );
  }

  String get sizeFormatted => formatBytes(sizeBytes);

  // SABnzbd reports "Completed"/"Failed"; qBittorrent passes raw torrent
  // states through ("uploading", "stalledUP", "pausedUP", "error",
  // "missingFiles") and populates `error` for the failure states.
  bool get isFailed {
    final s = status.toLowerCase();
    return error.isNotEmpty ||
        s.contains('fail') ||
        s == 'error' ||
        s == 'missingfiles';
  }

  bool get isCompleted => !isFailed;
}
