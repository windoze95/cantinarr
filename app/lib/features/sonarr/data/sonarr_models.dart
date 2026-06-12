/// A TV series managed by Sonarr.
class SonarrSeries {
  final int id;
  final String title;
  final int? tvdbId;
  final int? tmdbId;
  final int? year;
  final String? overview;
  final String? titleSlug;
  final bool monitored;
  final String? path;
  final String seriesType;
  final List<SonarrImage> images;
  final SonarrStatistics? statistics;
  final String? status;
  final int qualityProfileId;
  final List<SonarrSeason> seasons;

  const SonarrSeries({
    required this.id,
    required this.title,
    this.tvdbId,
    this.tmdbId,
    this.year,
    this.overview,
    this.titleSlug,
    this.monitored = true,
    this.path,
    this.seriesType = 'standard',
    this.images = const [],
    this.statistics,
    this.status,
    this.qualityProfileId = 0,
    this.seasons = const [],
  });

  factory SonarrSeries.fromJson(Map<String, dynamic> json) => SonarrSeries(
        id: json['id'] as int? ?? 0,
        title: json['title'] as String? ?? 'Untitled',
        tvdbId: json['tvdbId'] as int?,
        tmdbId: json['tmdbId'] as int?,
        year: json['year'] as int?,
        overview: json['overview'] as String?,
        titleSlug: json['titleSlug'] as String?,
        monitored: json['monitored'] as bool? ?? true,
        path: json['path'] as String?,
        seriesType: json['seriesType'] as String? ?? 'standard',
        images: (json['images'] as List<dynamic>?)
                ?.map((i) => SonarrImage.fromJson(i as Map<String, dynamic>))
                .toList() ??
            [],
        statistics: json['statistics'] != null
            ? SonarrStatistics.fromJson(
                json['statistics'] as Map<String, dynamic>)
            : null,
        status: json['status'] as String?,
        qualityProfileId: json['qualityProfileId'] as int? ?? 0,
        seasons: (json['seasons'] as List<dynamic>?)
                ?.map((s) => SonarrSeason.fromJson(s as Map<String, dynamic>))
                .toList() ??
            [],
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'title': title,
        'tvdbId': tvdbId,
        'tmdbId': tmdbId,
        'year': year,
        'overview': overview,
        'titleSlug': titleSlug,
        'monitored': monitored,
        'path': path,
        'seriesType': seriesType,
        'images': images.map((i) => i.toJson()).toList(),
        'qualityProfileId': qualityProfileId,
        'seasons': seasons.map((s) => s.toJson()).toList(),
      };

  String? get posterUrl {
    final poster = images.where((i) => i.coverType == 'poster');
    return poster.isNotEmpty ? poster.first.remoteUrl : null;
  }

  String? get fanartUrl {
    final fanart = images.where((i) => i.coverType == 'fanart');
    return fanart.isNotEmpty ? fanart.first.remoteUrl : null;
  }

  double get percentComplete {
    if (statistics == null || statistics!.episodeCount == 0) return 0;
    return statistics!.episodeFileCount / statistics!.episodeCount;
  }
}

class SonarrImage {
  final String coverType;
  final String? url;
  final String? remoteUrl;

  const SonarrImage({required this.coverType, this.url, this.remoteUrl});

  factory SonarrImage.fromJson(Map<String, dynamic> json) => SonarrImage(
        coverType: json['coverType'] as String? ?? '',
        url: json['url'] as String?,
        remoteUrl: json['remoteUrl'] as String?,
      );

  Map<String, dynamic> toJson() => {
        'coverType': coverType,
        'url': url,
        'remoteUrl': remoteUrl,
      };
}

class SonarrStatistics {
  final int seasonCount;
  final int episodeFileCount;
  final int episodeCount;
  final int totalEpisodeCount;
  final int sizeOnDisk;
  final double percentOfEpisodes;

  const SonarrStatistics({
    this.seasonCount = 0,
    this.episodeFileCount = 0,
    this.episodeCount = 0,
    this.totalEpisodeCount = 0,
    this.sizeOnDisk = 0,
    this.percentOfEpisodes = 0,
  });

  factory SonarrStatistics.fromJson(Map<String, dynamic> json) =>
      SonarrStatistics(
        seasonCount: json['seasonCount'] as int? ?? 0,
        episodeFileCount: json['episodeFileCount'] as int? ?? 0,
        episodeCount: json['episodeCount'] as int? ?? 0,
        totalEpisodeCount: json['totalEpisodeCount'] as int? ?? 0,
        sizeOnDisk: json['sizeOnDisk'] as int? ?? 0,
        percentOfEpisodes: (json['percentOfEpisodes'] as num?)?.toDouble() ?? 0,
      );

  String get sizeFormatted {
    final gb = sizeOnDisk / (1024 * 1024 * 1024);
    return '${gb.toStringAsFixed(1)} GB';
  }
}

class SonarrSeason {
  final int seasonNumber;
  final bool monitored;
  final SonarrStatistics? statistics;

  const SonarrSeason({
    required this.seasonNumber,
    this.monitored = true,
    this.statistics,
  });

  factory SonarrSeason.fromJson(Map<String, dynamic> json) => SonarrSeason(
        seasonNumber: json['seasonNumber'] as int,
        monitored: json['monitored'] as bool? ?? true,
        statistics: json['statistics'] != null
            ? SonarrStatistics.fromJson(
                json['statistics'] as Map<String, dynamic>)
            : null,
      );

  Map<String, dynamic> toJson() => {
        'seasonNumber': seasonNumber,
        'monitored': monitored,
      };
}

class SonarrEpisode {
  final int id;
  final int seriesId;
  final int seasonNumber;
  final int episodeNumber;
  final String? title;
  final String? overview;
  final bool hasFile;
  final bool monitored;
  final String? airDate;

  const SonarrEpisode({
    required this.id,
    required this.seriesId,
    required this.seasonNumber,
    required this.episodeNumber,
    this.title,
    this.overview,
    this.hasFile = false,
    this.monitored = true,
    this.airDate,
  });

  factory SonarrEpisode.fromJson(Map<String, dynamic> json) => SonarrEpisode(
        id: json['id'] as int,
        seriesId: json['seriesId'] as int,
        seasonNumber: json['seasonNumber'] as int,
        episodeNumber: json['episodeNumber'] as int,
        title: json['title'] as String?,
        overview: json['overview'] as String?,
        hasFile: json['hasFile'] as bool? ?? false,
        monitored: json['monitored'] as bool? ?? true,
        airDate: json['airDate'] as String?,
      );
}

class SonarrQualityProfile {
  final int id;
  final String name;

  const SonarrQualityProfile({required this.id, required this.name});

  factory SonarrQualityProfile.fromJson(Map<String, dynamic> json) =>
      SonarrQualityProfile(
        id: json['id'] as int,
        name: json['name'] as String,
      );
}

class SonarrRootFolder {
  final int id;
  final String path;
  final int? freeSpace;

  const SonarrRootFolder(
      {required this.id, required this.path, this.freeSpace});

  factory SonarrRootFolder.fromJson(Map<String, dynamic> json) =>
      SonarrRootFolder(
        id: json['id'] as int,
        path: json['path'] as String,
        freeSpace: json['freeSpace'] as int?,
      );
}

class SonarrSystemStatus {
  final String version;

  const SonarrSystemStatus({required this.version});

  factory SonarrSystemStatus.fromJson(Map<String, dynamic> json) =>
      SonarrSystemStatus(version: json['version'] as String? ?? 'Unknown');
}

String _formatBytes(num bytes) {
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

/// One item in the Sonarr download queue (fetched with series + episode
/// details).
class SonarrQueueItem {
  final int id;
  final int? seriesId;
  final String title;
  final String? seriesTitle;
  final int? seasonNumber;
  final int? episodeNumber;
  final String? episodeTitle;
  final String status;
  final String? trackedDownloadState;
  final String? trackedDownloadStatus;
  final String protocol;
  final String? indexer;
  final String? downloadClient;
  final double size;
  final double sizeleft;
  final String? timeleft;
  final String? errorMessage;
  final List<String> statusMessages;
  final String? quality;

  const SonarrQueueItem({
    required this.id,
    this.seriesId,
    required this.title,
    this.seriesTitle,
    this.seasonNumber,
    this.episodeNumber,
    this.episodeTitle,
    this.status = '',
    this.trackedDownloadState,
    this.trackedDownloadStatus,
    this.protocol = 'unknown',
    this.indexer,
    this.downloadClient,
    this.size = 0,
    this.sizeleft = 0,
    this.timeleft,
    this.errorMessage,
    this.statusMessages = const [],
    this.quality,
  });

  factory SonarrQueueItem.fromJson(Map<String, dynamic> json) {
    final messages = <String>[];
    for (final entry in (json['statusMessages'] as List<dynamic>? ?? [])) {
      final map = entry as Map<String, dynamic>;
      for (final msg in (map['messages'] as List<dynamic>? ?? [])) {
        final text = msg.toString();
        if (text.isNotEmpty) messages.add(text);
      }
      if (map['messages'] == null || (map['messages'] as List).isEmpty) {
        final title = map['title'] as String?;
        if (title != null && title.isNotEmpty) messages.add(title);
      }
    }
    final episode = json['episode'] as Map<String, dynamic>?;
    return SonarrQueueItem(
      id: json['id'] as int? ?? 0,
      seriesId: json['seriesId'] as int?,
      title: json['title'] as String? ?? 'Unknown',
      seriesTitle:
          (json['series'] as Map<String, dynamic>?)?['title'] as String?,
      seasonNumber:
          episode?['seasonNumber'] as int? ?? json['seasonNumber'] as int?,
      episodeNumber: episode?['episodeNumber'] as int?,
      episodeTitle: episode?['title'] as String?,
      status: json['status'] as String? ?? '',
      trackedDownloadState: json['trackedDownloadState'] as String?,
      trackedDownloadStatus: json['trackedDownloadStatus'] as String?,
      protocol: json['protocol'] as String? ?? 'unknown',
      indexer: json['indexer'] as String?,
      downloadClient: json['downloadClient'] as String?,
      size: (json['size'] as num?)?.toDouble() ?? 0,
      sizeleft: (json['sizeleft'] as num?)?.toDouble() ?? 0,
      timeleft: json['timeleft'] as String?,
      errorMessage: json['errorMessage'] as String?,
      statusMessages: messages,
      quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
          as String?,
    );
  }

  double get progress =>
      size > 0 ? ((size - sizeleft) / size).clamp(0.0, 1.0) : 0;
  String get sizeFormatted => _formatBytes(size);
  String get downloadedFormatted => _formatBytes(size - sizeleft);
  bool get hasIssues =>
      (errorMessage?.isNotEmpty ?? false) ||
      statusMessages.isNotEmpty ||
      trackedDownloadStatus == 'warning' ||
      trackedDownloadStatus == 'error';

  /// e.g. "S01E05 • Episode title", or null when episode info is missing.
  String? get episodeLabel {
    if (seasonNumber == null || episodeNumber == null) return null;
    final se = 'S${seasonNumber!.toString().padLeft(2, '0')}'
        'E${episodeNumber!.toString().padLeft(2, '0')}';
    return (episodeTitle != null && episodeTitle!.isNotEmpty)
        ? '$se • $episodeTitle'
        : se;
  }
}

/// A single Sonarr history event.
class SonarrHistoryRecord {
  final int id;
  final String sourceTitle;
  final String eventType;
  final DateTime? date;
  final String? quality;
  final int? seriesId;
  final int? episodeId;

  const SonarrHistoryRecord({
    required this.id,
    this.sourceTitle = '',
    this.eventType = '',
    this.date,
    this.quality,
    this.seriesId,
    this.episodeId,
  });

  factory SonarrHistoryRecord.fromJson(Map<String, dynamic> json) =>
      SonarrHistoryRecord(
        id: json['id'] as int? ?? 0,
        sourceTitle: json['sourceTitle'] as String? ?? '',
        eventType: json['eventType'] as String? ?? '',
        date: DateTime.tryParse(json['date'] as String? ?? ''),
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
        seriesId: json['seriesId'] as int?,
        episodeId: json['episodeId'] as int?,
      );
}

/// Paged envelope for Sonarr history.
class SonarrHistoryPage {
  final List<SonarrHistoryRecord> records;
  final int totalRecords;

  const SonarrHistoryPage({this.records = const [], this.totalRecords = 0});

  factory SonarrHistoryPage.fromJson(Map<String, dynamic> json) =>
      SonarrHistoryPage(
        records: (json['records'] as List<dynamic>?)
                ?.map((r) =>
                    SonarrHistoryRecord.fromJson(r as Map<String, dynamic>))
                .toList() ??
            [],
        totalRecords: json['totalRecords'] as int? ?? 0,
      );
}

/// A release returned by Sonarr's interactive search.
class SonarrRelease {
  final String guid;
  final int indexerId;
  final String title;
  final String? quality;
  final int size;
  final int age;
  final double ageHours;
  final String? indexer;
  final String protocol;
  final int? seeders;
  final int? leechers;
  final bool rejected;
  final List<String> rejections;

  const SonarrRelease({
    required this.guid,
    required this.indexerId,
    required this.title,
    this.quality,
    this.size = 0,
    this.age = 0,
    this.ageHours = 0,
    this.indexer,
    this.protocol = 'unknown',
    this.seeders,
    this.leechers,
    this.rejected = false,
    this.rejections = const [],
  });

  factory SonarrRelease.fromJson(Map<String, dynamic> json) => SonarrRelease(
        guid: json['guid'] as String? ?? '',
        indexerId: json['indexerId'] as int? ?? 0,
        title: json['title'] as String? ?? 'Unknown',
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
        size: (json['size'] as num?)?.toInt() ?? 0,
        age: json['age'] as int? ?? 0,
        ageHours: (json['ageHours'] as num?)?.toDouble() ?? 0,
        indexer: json['indexer'] as String?,
        protocol: json['protocol'] as String? ?? 'unknown',
        seeders: json['seeders'] as int?,
        leechers: json['leechers'] as int?,
        rejected: json['rejected'] as bool? ?? false,
        rejections: (json['rejections'] as List<dynamic>?)
                ?.map((r) => r is Map<String, dynamic>
                    ? (r['reason']?.toString() ?? '')
                    : r.toString())
                .where((r) => r.isNotEmpty)
                .toList() ??
            [],
      );

  bool get isTorrent => protocol == 'torrent';
  String get sizeFormatted => _formatBytes(size);
  String get ageFormatted {
    if (age >= 1) return '${age}d';
    if (ageHours >= 1) return '${ageHours.round()}h';
    return '<1h';
  }
}
