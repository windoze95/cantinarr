/// A TV series managed by Sonarr.
class SonarrSeries {
  final int id;
  final String title;
  final int? tvdbId;
  final int? tmdbId;
  final String? imdbId;
  final int? year;
  final String? overview;
  final String? titleSlug;
  final bool monitored;
  final bool seasonFolder;
  final String? path;
  final String seriesType;
  final List<SonarrImage> images;
  final SonarrStatistics? statistics;
  final String? status;
  final int qualityProfileId;
  final List<int> tags;
  final List<SonarrSeason> seasons;

  const SonarrSeries({
    required this.id,
    required this.title,
    this.tvdbId,
    this.tmdbId,
    this.imdbId,
    this.year,
    this.overview,
    this.titleSlug,
    this.monitored = true,
    this.seasonFolder = true,
    this.path,
    this.seriesType = 'standard',
    this.images = const [],
    this.statistics,
    this.status,
    this.qualityProfileId = 0,
    this.tags = const [],
    this.seasons = const [],
  });

  factory SonarrSeries.fromJson(Map<String, dynamic> json) => SonarrSeries(
        id: json['id'] as int? ?? 0,
        title: json['title'] as String? ?? 'Untitled',
        tvdbId: json['tvdbId'] as int?,
        tmdbId: json['tmdbId'] as int?,
        imdbId: json['imdbId'] as String?,
        year: json['year'] as int?,
        overview: json['overview'] as String?,
        titleSlug: json['titleSlug'] as String?,
        monitored: json['monitored'] as bool? ?? true,
        seasonFolder: json['seasonFolder'] as bool? ?? true,
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
        tags: (json['tags'] as List<dynamic>?)?.cast<int>() ?? const [],
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
        'imdbId': imdbId,
        'year': year,
        'overview': overview,
        'titleSlug': titleSlug,
        'monitored': monitored,
        'seasonFolder': seasonFolder,
        'path': path,
        'seriesType': seriesType,
        'images': images.map((i) => i.toJson()).toList(),
        'qualityProfileId': qualityProfileId,
        'tags': tags,
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

/// A Sonarr tag (id + label), used by the Edit Series tag picker.
class SonarrTag {
  final int id;
  final String label;

  const SonarrTag({required this.id, required this.label});

  factory SonarrTag.fromJson(Map<String, dynamic> json) => SonarrTag(
        id: json['id'] as int? ?? 0,
        label: json['label'] as String? ?? '',
      );
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

/// A downloaded episode file: drives the "WEBDL-1080p — 564.60 MB" status line.
class SonarrEpisodeFile {
  final int id;
  final int seriesId;
  final int seasonNumber;
  final int size;
  final String? relativePath;
  final String? path;
  final String? releaseGroup;
  final String? quality;
  final bool qualityCutoffNotMet;

  const SonarrEpisodeFile({
    required this.id,
    this.seriesId = 0,
    this.seasonNumber = 0,
    this.size = 0,
    this.relativePath,
    this.path,
    this.releaseGroup,
    this.quality,
    this.qualityCutoffNotMet = false,
  });

  factory SonarrEpisodeFile.fromJson(Map<String, dynamic> json) =>
      SonarrEpisodeFile(
        id: json['id'] as int? ?? 0,
        seriesId: json['seriesId'] as int? ?? 0,
        seasonNumber: json['seasonNumber'] as int? ?? 0,
        size: (json['size'] as num?)?.toInt() ?? 0,
        relativePath: json['relativePath'] as String?,
        path: json['path'] as String?,
        releaseGroup: json['releaseGroup'] as String?,
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
        qualityCutoffNotMet: json['qualityCutoffNotMet'] as bool? ?? false,
      );

  String get sizeFormatted => _formatBytes(size);
}

class SonarrEpisode {
  final int id;
  final int seriesId;
  final int seasonNumber;
  final int episodeNumber;
  final int? absoluteEpisodeNumber;
  final String? title;
  final String? overview;
  final bool hasFile;
  final bool monitored;
  final int episodeFileId;
  final String? airDate;
  final DateTime? airDateUtc;
  final SonarrEpisodeFile? episodeFile;

  const SonarrEpisode({
    required this.id,
    required this.seriesId,
    required this.seasonNumber,
    required this.episodeNumber,
    this.absoluteEpisodeNumber,
    this.title,
    this.overview,
    this.hasFile = false,
    this.monitored = true,
    this.episodeFileId = 0,
    this.airDate,
    this.airDateUtc,
    this.episodeFile,
  });

  factory SonarrEpisode.fromJson(Map<String, dynamic> json) => SonarrEpisode(
        id: json['id'] as int,
        seriesId: json['seriesId'] as int,
        seasonNumber: json['seasonNumber'] as int,
        episodeNumber: json['episodeNumber'] as int,
        absoluteEpisodeNumber: json['absoluteEpisodeNumber'] as int?,
        title: json['title'] as String?,
        overview: json['overview'] as String?,
        hasFile: json['hasFile'] as bool? ?? false,
        monitored: json['monitored'] as bool? ?? true,
        episodeFileId: json['episodeFileId'] as int? ?? 0,
        airDate: json['airDate'] as String?,
        airDateUtc: DateTime.tryParse(json['airDateUtc'] as String? ?? ''),
        episodeFile: json['episodeFile'] != null
            ? SonarrEpisodeFile.fromJson(
                json['episodeFile'] as Map<String, dynamic>)
            : null,
      );

  /// e.g. "S01E05".
  String get seasonEpisodeLabel =>
      'S${seasonNumber.toString().padLeft(2, '0')}'
      'E${episodeNumber.toString().padLeft(2, '0')}';

  /// True once the episode has aired (air date is in the past).
  bool get hasAired =>
      airDateUtc != null && airDateUtc!.isBefore(DateTime.now().toUtc());
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

/// One grouped status message on a queue item (a title plus its messages) —
/// the data behind LunaSea's "Messages" surface and our Import Doctor.
class SonarrStatusMessage {
  final String title;
  final List<String> messages;

  const SonarrStatusMessage({this.title = '', this.messages = const []});

  factory SonarrStatusMessage.fromJson(Map<String, dynamic> json) =>
      SonarrStatusMessage(
        title: json['title'] as String? ?? '',
        messages: ((json['messages'] as List<dynamic>?) ?? [])
            .map((m) => m.toString())
            .where((m) => m.isNotEmpty)
            .toList(),
      );
}

/// One item in the Sonarr download queue (fetched with series + episode
/// details).
class SonarrQueueItem {
  final int id;
  final int? seriesId;
  final int? episodeId;
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
  final List<SonarrStatusMessage> statusMessageGroups;
  final String? outputPath;
  final String? downloadId;
  final String? quality;

  const SonarrQueueItem({
    required this.id,
    this.seriesId,
    this.episodeId,
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
    this.statusMessageGroups = const [],
    this.outputPath,
    this.downloadId,
    this.quality,
  });

  factory SonarrQueueItem.fromJson(Map<String, dynamic> json) {
    final messages = <String>[];
    final groups = <SonarrStatusMessage>[];
    for (final entry in (json['statusMessages'] as List<dynamic>? ?? [])) {
      final map = entry as Map<String, dynamic>;
      groups.add(SonarrStatusMessage.fromJson(map));
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
      episodeId: episode?['id'] as int? ?? json['episodeId'] as int?,
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
      statusMessageGroups: groups,
      outputPath: json['outputPath'] as String?,
      downloadId: json['downloadId'] as String?,
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
  final Map<String, String> data;
  final String? downloadId;

  const SonarrHistoryRecord({
    required this.id,
    this.sourceTitle = '',
    this.eventType = '',
    this.date,
    this.quality,
    this.seriesId,
    this.episodeId,
    this.data = const {},
    this.downloadId,
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
        data: ((json['data'] as Map<String, dynamic>?) ?? {})
            .map((k, v) => MapEntry(k, v?.toString() ?? '')),
        downloadId: json['downloadId'] as String?,
      );

  /// Indexer the release was grabbed from, e.g. "NZBgeek (Prowlarr)".
  String? get indexer => data['indexer'];

  /// Release group parsed from the grab, when present.
  String? get releaseGroup => data['releaseGroup'];
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

/// One wanted episode (missing or cutoff unmet) with series context.
class SonarrWantedRecord {
  final int id;
  final int seriesId;
  final int seasonNumber;
  final int episodeNumber;
  final String? title;
  final DateTime? airDateUtc;
  final bool monitored;
  final String? seriesTitle;

  /// Current file quality; only present on cutoff-unmet records fetched
  /// with includeEpisodeFile.
  final String? quality;

  const SonarrWantedRecord({
    required this.id,
    this.seriesId = 0,
    this.seasonNumber = 0,
    this.episodeNumber = 0,
    this.title,
    this.airDateUtc,
    this.monitored = true,
    this.seriesTitle,
    this.quality,
  });

  factory SonarrWantedRecord.fromJson(Map<String, dynamic> json) =>
      SonarrWantedRecord(
        id: json['id'] as int? ?? 0,
        seriesId: json['seriesId'] as int? ?? 0,
        seasonNumber: json['seasonNumber'] as int? ?? 0,
        episodeNumber: json['episodeNumber'] as int? ?? 0,
        title: json['title'] as String?,
        airDateUtc: DateTime.tryParse(json['airDateUtc'] as String? ?? ''),
        monitored: json['monitored'] as bool? ?? true,
        seriesTitle:
            (json['series'] as Map<String, dynamic>?)?['title'] as String?,
        quality: (json['episodeFile'] as Map<String, dynamic>?)?['quality']
            ?['quality']?['name'] as String?,
      );

  /// e.g. "S01E05".
  String get seasonEpisodeLabel => 'S${seasonNumber.toString().padLeft(2, '0')}'
      'E${episodeNumber.toString().padLeft(2, '0')}';
}

/// Paged envelope for Sonarr wanted episodes (missing / cutoff unmet).
class SonarrWantedPage {
  final List<SonarrWantedRecord> records;
  final int totalRecords;

  const SonarrWantedPage({this.records = const [], this.totalRecords = 0});

  factory SonarrWantedPage.fromJson(Map<String, dynamic> json) =>
      SonarrWantedPage(
        records: (json['records'] as List<dynamic>?)
                ?.map((r) =>
                    SonarrWantedRecord.fromJson(r as Map<String, dynamic>))
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

/// Why a manual-import candidate was rejected. `type` is "permanent" or
/// "temporary"; permanent rejections only import with force.
class SonarrImportRejection {
  final String reason;
  final String type;

  const SonarrImportRejection({this.reason = '', this.type = ''});

  factory SonarrImportRejection.fromJson(Map<String, dynamic> json) =>
      SonarrImportRejection(
        reason: json['reason'] as String? ?? '',
        type: json['type'] as String? ?? '',
      );

  bool get isPermanent => type.toLowerCase() == 'permanent';
}

/// One importable file returned by GET /manualimport. The `quality` and
/// `languages` blobs are kept VERBATIM and round-tripped back into the
/// ManualImport command unchanged (re-modelling them loses fields Sonarr needs).
class SonarrManualImportCandidate {
  final int id;
  final String path;
  final String? folderName;
  final String name;
  final int size;
  final int? seriesId;
  final int? seasonNumber;
  final List<int> episodeIds;
  final List<String> episodeLabels;
  final Map<String, dynamic>? quality;
  final List<dynamic> languages;
  final String? releaseGroup;
  final String? downloadId;
  final int? indexerFlags;
  final String? releaseType;
  final List<SonarrImportRejection> rejections;

  const SonarrManualImportCandidate({
    required this.id,
    required this.path,
    this.folderName,
    this.name = '',
    this.size = 0,
    this.seriesId,
    this.seasonNumber,
    this.episodeIds = const [],
    this.episodeLabels = const [],
    this.quality,
    this.languages = const [],
    this.releaseGroup,
    this.downloadId,
    this.indexerFlags,
    this.releaseType,
    this.rejections = const [],
  });

  factory SonarrManualImportCandidate.fromJson(Map<String, dynamic> json) {
    final episodes = (json['episodes'] as List<dynamic>?) ?? [];
    final ids = <int>[];
    final labels = <String>[];
    for (final e in episodes) {
      final map = e as Map<String, dynamic>;
      final id = map['id'] as int?;
      if (id != null) ids.add(id);
      final s = map['seasonNumber'] as int?;
      final n = map['episodeNumber'] as int?;
      if (s != null && n != null) {
        labels.add('S${s.toString().padLeft(2, '0')}'
            'E${n.toString().padLeft(2, '0')}');
      }
    }
    return SonarrManualImportCandidate(
      id: json['id'] as int? ?? 0,
      path: json['path'] as String? ?? '',
      folderName: json['folderName'] as String?,
      name: json['name'] as String? ??
          (json['relativePath'] as String?) ??
          (json['path'] as String? ?? ''),
      size: (json['size'] as num?)?.toInt() ?? 0,
      seriesId: (json['series'] as Map<String, dynamic>?)?['id'] as int?,
      seasonNumber: json['seasonNumber'] as int?,
      episodeIds: ids,
      episodeLabels: labels,
      quality: json['quality'] as Map<String, dynamic>?,
      languages: (json['languages'] as List<dynamic>?) ?? const [],
      releaseGroup: json['releaseGroup'] as String?,
      downloadId: json['downloadId'] as String?,
      indexerFlags: json['indexerFlags'] as int?,
      releaseType: json['releaseType'] as String?,
      rejections: ((json['rejections'] as List<dynamic>?) ?? [])
          .map((r) => SonarrImportRejection.fromJson(r as Map<String, dynamic>))
          .toList(),
    );
  }

  bool get hasPermanentRejection => rejections.any((r) => r.isPermanent);
  bool get isMapped => episodeIds.isNotEmpty;
  String get sizeFormatted => _formatBytes(size);

  /// The file entry for the ManualImport command, with quality/languages sent
  /// back exactly as received.
  Map<String, dynamic> toImportFile() => {
        'path': path,
        if (folderName != null) 'folderName': folderName,
        if (seriesId != null) 'seriesId': seriesId,
        'episodeIds': episodeIds,
        if (quality != null) 'quality': quality,
        'languages': languages,
        if (releaseGroup != null) 'releaseGroup': releaseGroup,
        if (downloadId != null) 'downloadId': downloadId,
        if (indexerFlags != null) 'indexerFlags': indexerFlags,
        if (releaseType != null) 'releaseType': releaseType,
      };
}
