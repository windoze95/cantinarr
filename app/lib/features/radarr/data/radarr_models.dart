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
        ratings: (json['ratings'] as Map<String, dynamic>?)?['value']
            as double?,
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
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']
            ?['name'] as String?,
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
