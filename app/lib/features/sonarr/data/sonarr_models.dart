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
        percentOfEpisodes:
            (json['percentOfEpisodes'] as num?)?.toDouble() ?? 0,
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

  const SonarrRootFolder({required this.id, required this.path, this.freeSpace});

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
