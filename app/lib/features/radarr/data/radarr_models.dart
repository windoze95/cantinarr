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

  const RadarrMovieFile({
    required this.id,
    this.relativePath,
    this.size,
    this.quality,
  });

  factory RadarrMovieFile.fromJson(Map<String, dynamic> json) =>
      RadarrMovieFile(
        id: json['id'] as int,
        relativePath: json['relativePath'] as String?,
        size: json['size'] as int?,
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
      );

  String get sizeFormatted {
    if (size == null) return 'Unknown';
    final gb = size! / (1024 * 1024 * 1024);
    return '${gb.toStringAsFixed(1)} GB';
  }
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
  final List<String> statusMessages;
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
    this.quality,
  });

  factory RadarrQueueItem.fromJson(Map<String, dynamic> json) {
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

  const RadarrHistoryRecord({
    required this.id,
    this.sourceTitle = '',
    this.eventType = '',
    this.date,
    this.quality,
    this.movieId,
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
      );
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
