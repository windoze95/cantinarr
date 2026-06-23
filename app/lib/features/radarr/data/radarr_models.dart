import '../../sonarr/data/sonarr_models.dart' show SonarrStatusMessage;

/// A movie managed by Radarr.
class RadarrMovie {
  final int id;
  final String title;
  final int year;
  final int? tmdbId;
  final String? overview;
  final String? titleSlug;
  final bool monitored;
  final bool hasFile;
  final String? path;
  final int runtime;
  final String minimumAvailability;
  final List<RadarrImage> images;
  final RadarrMovieFile? movieFile;
  final DateTime? added;
  final String? status;
  final double? ratings;
  final int qualityProfileId;
  final DateTime? inCinemas;
  final DateTime? physicalRelease;
  final DateTime? digitalRelease;
  final int sizeOnDisk;

  /// True when the movie has reached its minimum availability (i.e. Radarr will
  /// actively search for it). Drives the "Available"/"Not yet available" line.
  final bool isAvailable;

  const RadarrMovie({
    required this.id,
    required this.title,
    required this.year,
    this.tmdbId,
    this.overview,
    this.titleSlug,
    this.monitored = true,
    this.hasFile = false,
    this.path,
    this.runtime = 0,
    this.minimumAvailability = 'announced',
    this.images = const [],
    this.movieFile,
    this.added,
    this.status,
    this.ratings,
    this.qualityProfileId = 0,
    this.inCinemas,
    this.physicalRelease,
    this.digitalRelease,
    this.sizeOnDisk = 0,
    this.isAvailable = false,
  });

  factory RadarrMovie.fromJson(Map<String, dynamic> json) => RadarrMovie(
        id: json['id'] as int? ?? 0,
        title: json['title'] as String? ?? 'Untitled',
        year: json['year'] as int? ?? 0,
        tmdbId: json['tmdbId'] as int?,
        overview: json['overview'] as String?,
        titleSlug: json['titleSlug'] as String?,
        monitored: json['monitored'] as bool? ?? true,
        hasFile: json['hasFile'] as bool? ?? false,
        path: json['path'] as String?,
        runtime: json['runtime'] as int? ?? 0,
        minimumAvailability:
            json['minimumAvailability'] as String? ?? 'announced',
        images: (json['images'] as List<dynamic>?)
                ?.map((i) => RadarrImage.fromJson(i as Map<String, dynamic>))
                .toList() ??
            [],
        movieFile: json['movieFile'] != null
            ? RadarrMovieFile.fromJson(
                json['movieFile'] as Map<String, dynamic>)
            : null,
        added: json['added'] != null
            ? DateTime.tryParse(json['added'] as String)
            : null,
        status: json['status'] as String?,
        ratings:
            (json['ratings'] as Map<String, dynamic>?)?['value'] as double?,
        qualityProfileId: json['qualityProfileId'] as int? ?? 0,
        inCinemas: DateTime.tryParse(json['inCinemas'] as String? ?? ''),
        physicalRelease:
            DateTime.tryParse(json['physicalRelease'] as String? ?? ''),
        digitalRelease:
            DateTime.tryParse(json['digitalRelease'] as String? ?? ''),
        sizeOnDisk: (json['sizeOnDisk'] as num?)?.toInt() ?? 0,
        isAvailable: json['isAvailable'] as bool? ?? false,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'title': title,
        'year': year,
        'tmdbId': tmdbId,
        'overview': overview,
        'titleSlug': titleSlug,
        'monitored': monitored,
        'hasFile': hasFile,
        'path': path,
        'runtime': runtime,
        'minimumAvailability': minimumAvailability,
        'images': images.map((i) => i.toJson()).toList(),
        'qualityProfileId': qualityProfileId,
      };

  /// Gets the poster URL from Radarr images.
  String? get posterUrl {
    final poster = images.where((i) => i.coverType == 'poster');
    return poster.isNotEmpty ? poster.first.remoteUrl : null;
  }

  String? get fanartUrl {
    final fanart = images.where((i) => i.coverType == 'fanart');
    return fanart.isNotEmpty ? fanart.first.remoteUrl : null;
  }

  /// Size on disk, preferring the top-level field but falling back to the
  /// movie file's size (the list endpoint omits one or the other at times).
  String get sizeOnDiskFormatted =>
      _formatBytes(sizeOnDisk > 0 ? sizeOnDisk : (movieFile?.size ?? 0));
}

class RadarrImage {
  final String coverType;
  final String? url;
  final String? remoteUrl;

  const RadarrImage({
    required this.coverType,
    this.url,
    this.remoteUrl,
  });

  factory RadarrImage.fromJson(Map<String, dynamic> json) => RadarrImage(
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

class RadarrMovieFile {
  final int id;
  final String? relativePath;
  final int? size;
  final String? quality;
  final bool qualityCutoffNotMet;
  final String? releaseGroup;

  const RadarrMovieFile({
    required this.id,
    this.relativePath,
    this.size,
    this.quality,
    this.qualityCutoffNotMet = false,
    this.releaseGroup,
  });

  factory RadarrMovieFile.fromJson(Map<String, dynamic> json) =>
      RadarrMovieFile(
        id: json['id'] as int,
        relativePath: json['relativePath'] as String?,
        size: (json['size'] as num?)?.toInt(),
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
        qualityCutoffNotMet: json['qualityCutoffNotMet'] as bool? ?? false,
        releaseGroup: json['releaseGroup'] as String?,
      );

  String get sizeFormatted =>
      (size == null || size! <= 0) ? 'Unknown' : _formatBytes(size!);
}

class RadarrQualityProfile {
  final int id;
  final String name;

  const RadarrQualityProfile({required this.id, required this.name});

  factory RadarrQualityProfile.fromJson(Map<String, dynamic> json) =>
      RadarrQualityProfile(
        id: json['id'] as int,
        name: json['name'] as String,
      );
}

class RadarrRootFolder {
  final int id;
  final String path;
  final int? freeSpace;

  const RadarrRootFolder({
    required this.id,
    required this.path,
    this.freeSpace,
  });

  factory RadarrRootFolder.fromJson(Map<String, dynamic> json) =>
      RadarrRootFolder(
        id: json['id'] as int,
        path: json['path'] as String,
        freeSpace: json['freeSpace'] as int?,
      );

  String get freeSpaceFormatted {
    if (freeSpace == null) return 'Unknown';
    final gb = freeSpace! / (1024 * 1024 * 1024);
    return '${gb.toStringAsFixed(0)} GB free';
  }
}

class RadarrSystemStatus {
  final String version;
  final String? startupPath;
  final String? appData;

  const RadarrSystemStatus({
    required this.version,
    this.startupPath,
    this.appData,
  });

  factory RadarrSystemStatus.fromJson(Map<String, dynamic> json) =>
      RadarrSystemStatus(
        version: json['version'] as String? ?? 'Unknown',
        startupPath: json['startupPath'] as String?,
        appData: json['appData'] as String?,
      );
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

/// One item in the Radarr download queue (fetched with movie details).
class RadarrQueueItem {
  final int id;
  final int? movieId;
  final String title;
  final String? movieTitle;
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

  /// Flat list of message strings — kept for existing consumers (the queue
  /// card's inline issues box).
  final List<String> statusMessages;

  /// Grouped status messages ({title, messages[]}) — the data the Import Doctor
  /// classifies, mirroring [SonarrQueueItem.statusMessageGroups].
  final List<SonarrStatusMessage> statusMessageGroups;
  final String? outputPath;
  final String? downloadId;
  final String? quality;

  const RadarrQueueItem({
    required this.id,
    this.movieId,
    required this.title,
    this.movieTitle,
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

  factory RadarrQueueItem.fromJson(Map<String, dynamic> json) {
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
    return RadarrQueueItem(
      id: json['id'] as int? ?? 0,
      movieId: json['movieId'] as int?,
      title: json['title'] as String? ?? 'Unknown',
      movieTitle: (json['movie'] as Map<String, dynamic>?)?['title'] as String?,
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
}

/// A single Radarr history event.
class RadarrHistoryRecord {
  final int id;
  final String sourceTitle;
  final String eventType;
  final DateTime? date;
  final String? quality;
  final int? movieId;
  final Map<String, String> data;
  final String? downloadId;

  const RadarrHistoryRecord({
    required this.id,
    this.sourceTitle = '',
    this.eventType = '',
    this.date,
    this.quality,
    this.movieId,
    this.data = const {},
    this.downloadId,
  });

  factory RadarrHistoryRecord.fromJson(Map<String, dynamic> json) =>
      RadarrHistoryRecord(
        id: json['id'] as int? ?? 0,
        sourceTitle: json['sourceTitle'] as String? ?? '',
        eventType: json['eventType'] as String? ?? '',
        date: DateTime.tryParse(json['date'] as String? ?? ''),
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
        movieId: json['movieId'] as int?,
        data: ((json['data'] as Map<String, dynamic>?) ?? {})
            .map((k, v) => MapEntry(k, v?.toString() ?? '')),
        downloadId: json['downloadId'] as String?,
      );

  /// Indexer the release was grabbed from, e.g. "NZBgeek (Prowlarr)".
  String? get indexer => data['indexer'];

  /// Release group parsed from the grab, when present.
  String? get releaseGroup => data['releaseGroup'];
}

/// Paged envelope for Radarr history.
class RadarrHistoryPage {
  final List<RadarrHistoryRecord> records;
  final int totalRecords;

  const RadarrHistoryPage({this.records = const [], this.totalRecords = 0});

  factory RadarrHistoryPage.fromJson(Map<String, dynamic> json) =>
      RadarrHistoryPage(
        records: (json['records'] as List<dynamic>?)
                ?.map((r) =>
                    RadarrHistoryRecord.fromJson(r as Map<String, dynamic>))
                .toList() ??
            [],
        totalRecords: json['totalRecords'] as int? ?? 0,
      );
}

/// Paged envelope for Radarr wanted movies (missing / cutoff unmet).
class RadarrWantedPage {
  final List<RadarrMovie> records;
  final int totalRecords;

  const RadarrWantedPage({this.records = const [], this.totalRecords = 0});

  factory RadarrWantedPage.fromJson(Map<String, dynamic> json) =>
      RadarrWantedPage(
        records: (json['records'] as List<dynamic>?)
                ?.map((r) => RadarrMovie.fromJson(r as Map<String, dynamic>))
                .toList() ??
            [],
        totalRecords: json['totalRecords'] as int? ?? 0,
      );
}

/// A release returned by Radarr's interactive search.
class RadarrRelease {
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

  const RadarrRelease({
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

  factory RadarrRelease.fromJson(Map<String, dynamic> json) => RadarrRelease(
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
class RadarrImportRejection {
  final String reason;
  final String type;

  const RadarrImportRejection({this.reason = '', this.type = ''});

  factory RadarrImportRejection.fromJson(Map<String, dynamic> json) =>
      RadarrImportRejection(
        reason: json['reason'] as String? ?? '',
        type: json['type'] as String? ?? '',
      );

  bool get isPermanent => type.toLowerCase() == 'permanent';
}

/// One importable file returned by GET /manualimport for a movie. Mirrors
/// [SonarrManualImportCandidate] but keyed by `movieId` (movies are single
/// items — no episodes). The `quality` and `languages` blobs are kept VERBATIM
/// and round-tripped back into the ManualImport command unchanged (re-modelling
/// them loses fields Radarr needs).
class RadarrManualImportCandidate {
  final int id;
  final String path;
  final String? folderName;
  final String name;
  final int size;
  final int? movieId;
  final Map<String, dynamic>? quality;
  final List<dynamic> languages;
  final String? releaseGroup;
  final String? downloadId;
  final int? indexerFlags;
  final List<RadarrImportRejection> rejections;

  const RadarrManualImportCandidate({
    required this.id,
    required this.path,
    this.folderName,
    this.name = '',
    this.size = 0,
    this.movieId,
    this.quality,
    this.languages = const [],
    this.releaseGroup,
    this.downloadId,
    this.indexerFlags,
    this.rejections = const [],
  });

  factory RadarrManualImportCandidate.fromJson(Map<String, dynamic> json) =>
      RadarrManualImportCandidate(
        id: json['id'] as int? ?? 0,
        path: json['path'] as String? ?? '',
        folderName: json['folderName'] as String?,
        name: json['name'] as String? ??
            (json['relativePath'] as String?) ??
            (json['path'] as String? ?? ''),
        size: (json['size'] as num?)?.toInt() ?? 0,
        movieId: (json['movie'] as Map<String, dynamic>?)?['id'] as int?,
        quality: json['quality'] as Map<String, dynamic>?,
        languages: (json['languages'] as List<dynamic>?) ?? const [],
        releaseGroup: json['releaseGroup'] as String?,
        downloadId: json['downloadId'] as String?,
        indexerFlags: json['indexerFlags'] as int?,
        rejections: ((json['rejections'] as List<dynamic>?) ?? [])
            .map((r) => RadarrImportRejection.fromJson(r as Map<String, dynamic>))
            .toList(),
      );

  bool get hasPermanentRejection => rejections.any((r) => r.isPermanent);

  /// A candidate is importable once Radarr has matched it to a movie.
  bool get isMapped => movieId != null;
  String get sizeFormatted => _formatBytes(size);

  /// The file entry for the ManualImport command, with quality/languages sent
  /// back exactly as received.
  Map<String, dynamic> toImportFile() => {
        'path': path,
        if (folderName != null) 'folderName': folderName,
        if (movieId != null) 'movieId': movieId,
        if (quality != null) 'quality': quality,
        'languages': languages,
        if (releaseGroup != null) 'releaseGroup': releaseGroup,
        if (downloadId != null) 'downloadId': downloadId,
        if (indexerFlags != null) 'indexerFlags': indexerFlags,
      };
}
