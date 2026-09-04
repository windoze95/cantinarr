List<dynamic> _asList(dynamic value) => value is List ? value : const [];

Map<String, dynamic>? _asMap(dynamic value) {
  if (value is Map<String, dynamic>) return value;
  if (value is Map) {
    return value.map((key, val) => MapEntry(key.toString(), val));
  }
  return null;
}

List<T> _modelList<T>(
  dynamic value,
  T Function(Map<String, dynamic>) fromJson,
) =>
    _asList(value)
        .map(_asMap)
        .whereType<Map<String, dynamic>>()
        .map(fromJson)
        .toList();

List<String> _stringList(dynamic value) {
  if (value is List) {
    return value
        .map((item) => item.toString())
        .where((item) => item.isNotEmpty)
        .toList();
  }
  if (value is String && value.trim().isNotEmpty) return [value.trim()];
  return const [];
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

/// A cover/poster reference on a Lidarr artist or album.
class LidarrImage {
  final String coverType;
  final String? url;
  final String? remoteUrl;

  const LidarrImage({required this.coverType, this.url, this.remoteUrl});

  factory LidarrImage.fromJson(Map<String, dynamic> json) => LidarrImage(
        coverType: json['coverType'] as String? ?? '',
        url: json['url'] as String?,
        remoteUrl: json['remoteUrl'] as String?,
      );
}

/// Picks a cover URL from a list of images, preferring `cover`/`poster` art
/// and falling back to the first image with a URL. Returns the raw `url`
/// field — relative (`/MediaCover/...`) for library records, which the UI
/// resolves through the backend proxy via `lidarrImageSource`.
String? _pickCoverUrl(List<LidarrImage> images) {
  bool hasUrl(LidarrImage i) => i.url != null && i.url!.isNotEmpty;
  for (final type in ['cover', 'poster']) {
    final match = images.where((i) => i.coverType == type && hasUrl(i));
    if (match.isNotEmpty) return match.first.url;
  }
  final withUrl = images.where(hasUrl);
  return withUrl.isNotEmpty ? withUrl.first.url : null;
}

/// Per-artist rollup Lidarr returns on an artist record.
class LidarrArtistStatistics {
  final int albumCount;
  final int trackFileCount;
  final int trackCount;
  final int totalTrackCount;
  final int sizeOnDisk;
  final double percentOfTracks;

  const LidarrArtistStatistics({
    this.albumCount = 0,
    this.trackFileCount = 0,
    this.trackCount = 0,
    this.totalTrackCount = 0,
    this.sizeOnDisk = 0,
    this.percentOfTracks = 0,
  });

  factory LidarrArtistStatistics.fromJson(Map<String, dynamic> json) =>
      LidarrArtistStatistics(
        albumCount: json['albumCount'] as int? ?? 0,
        trackFileCount: json['trackFileCount'] as int? ?? 0,
        trackCount: json['trackCount'] as int? ?? 0,
        totalTrackCount: json['totalTrackCount'] as int? ?? 0,
        sizeOnDisk: (json['sizeOnDisk'] as num?)?.toInt() ?? 0,
        percentOfTracks: (json['percentOfTracks'] as num?)?.toDouble() ?? 0,
      );

  String get sizeFormatted => _formatBytes(sizeOnDisk);
}

/// Per-album track rollup Lidarr returns on an album record.
class LidarrAlbumStatistics {
  final int trackFileCount;
  final int trackCount;
  final int totalTrackCount;
  final int sizeOnDisk;
  final double percentOfTracks;

  const LidarrAlbumStatistics({
    this.trackFileCount = 0,
    this.trackCount = 0,
    this.totalTrackCount = 0,
    this.sizeOnDisk = 0,
    this.percentOfTracks = 0,
  });

  factory LidarrAlbumStatistics.fromJson(Map<String, dynamic> json) =>
      LidarrAlbumStatistics(
        trackFileCount: json['trackFileCount'] as int? ?? 0,
        trackCount: json['trackCount'] as int? ?? 0,
        totalTrackCount: json['totalTrackCount'] as int? ?? 0,
        sizeOnDisk: (json['sizeOnDisk'] as num?)?.toInt() ?? 0,
        percentOfTracks: (json['percentOfTracks'] as num?)?.toDouble() ?? 0,
      );

  String get sizeFormatted => _formatBytes(sizeOnDisk);
}

/// An artist managed by Lidarr. `foreignArtistId` is the MusicBrainz artist
/// id — the identity requester surfaces address an artist by.
class LidarrArtist {
  final int id;
  final String artistName;
  final String? foreignArtistId;
  final String? overview;
  final String? artistType;
  final String? status;
  final bool monitored;
  final String? path;
  final int qualityProfileId;
  final int metadataProfileId;
  final DateTime? added;
  final String? remotePoster;
  final LidarrArtistStatistics? statistics;
  final List<LidarrImage> images;
  final List<String> genres;

  const LidarrArtist({
    required this.id,
    required this.artistName,
    this.foreignArtistId,
    this.overview,
    this.artistType,
    this.status,
    this.monitored = true,
    this.path,
    this.qualityProfileId = 0,
    this.metadataProfileId = 0,
    this.added,
    this.remotePoster,
    this.statistics,
    this.images = const [],
    this.genres = const [],
  });

  factory LidarrArtist.fromJson(Map<String, dynamic> json) => LidarrArtist(
        id: json['id'] as int? ?? 0,
        artistName: json['artistName'] as String? ?? 'Unknown Artist',
        foreignArtistId: json['foreignArtistId'] as String?,
        overview: json['overview'] as String?,
        artistType: json['artistType'] as String?,
        status: json['status'] as String?,
        monitored: json['monitored'] as bool? ?? true,
        path: json['path'] as String?,
        qualityProfileId: json['qualityProfileId'] as int? ?? 0,
        metadataProfileId: json['metadataProfileId'] as int? ?? 0,
        added: DateTime.tryParse(json['added'] as String? ?? ''),
        remotePoster: json['remotePoster'] as String?,
        statistics: json['statistics'] != null
            ? LidarrArtistStatistics.fromJson(
                json['statistics'] as Map<String, dynamic>)
            : null,
        images: _modelList(json['images'], LidarrImage.fromJson),
        genres: _stringList(json['genres']),
      );

  /// The artist's own image URL as Lidarr reports it (library records carry a
  /// relative `/MediaCover/...` path; lookup results a remote poster).
  String? get imageUrl => remotePoster ?? _pickCoverUrl(images);

  /// The artist's portrait for a search result or browse card: the absolute
  /// metadata-CDN copy when present (loads directly), else the relative
  /// library path a proxy resolver handles.
  String? get portraitUrl {
    if (remotePoster != null && remotePoster!.trim().isNotEmpty) {
      return remotePoster;
    }
    final poster = images.where((i) => i.coverType == 'poster');
    for (final image in [...poster, ...images]) {
      final remote = image.remoteUrl?.trim();
      if (remote != null && remote.isNotEmpty) return remote;
      final local = image.url?.trim();
      if (local != null && local.isNotEmpty) return local;
    }
    return null;
  }

  double get percentComplete {
    final s = statistics;
    if (s == null || s.trackCount == 0) return 0;
    return s.trackFileCount / s.trackCount;
  }

  /// e.g. "8 / 12 albums".
  String get albumCountLabel {
    final s = statistics;
    if (s == null) return '0 albums';
    return '${s.albumCount} album${s.albumCount == 1 ? '' : 's'}';
  }

  /// e.g. "142 / 160 tracks".
  String get trackCountLabel {
    final s = statistics;
    if (s == null) return '';
    return '${s.trackFileCount} / ${s.trackCount} tracks';
  }
}

/// An album managed by Lidarr (or a lookup result). `foreignAlbumId` is the
/// MusicBrainz release-group id — the identity requests are keyed by.
class LidarrAlbum {
  final int id;
  final String title;
  final int artistId;
  final String? foreignAlbumId;
  final String? overview;
  final String? disambiguation;
  final DateTime? releaseDate;
  final bool monitored;
  final String? albumType;
  final List<String> secondaryTypes;
  final String? remoteCover;
  final LidarrArtist? artist;
  final LidarrAlbumStatistics? statistics;
  final List<LidarrImage> images;
  final List<String> genres;

  const LidarrAlbum({
    required this.id,
    required this.title,
    this.artistId = 0,
    this.foreignAlbumId,
    this.overview,
    this.disambiguation,
    this.releaseDate,
    this.monitored = true,
    this.albumType,
    this.secondaryTypes = const [],
    this.remoteCover,
    this.artist,
    this.statistics,
    this.images = const [],
    this.genres = const [],
  });

  factory LidarrAlbum.fromJson(Map<String, dynamic> json) => LidarrAlbum(
        id: json['id'] as int? ?? 0,
        title: json['title'] as String? ?? 'Unknown Album',
        artistId: json['artistId'] as int? ?? 0,
        foreignAlbumId: json['foreignAlbumId'] as String?,
        overview: json['overview'] as String?,
        disambiguation: json['disambiguation'] as String?,
        releaseDate: DateTime.tryParse(json['releaseDate'] as String? ?? ''),
        monitored: json['monitored'] as bool? ?? true,
        albumType: json['albumType'] as String?,
        secondaryTypes: _stringList(json['secondaryTypes']),
        remoteCover: json['remoteCover'] as String?,
        artist: json['artist'] != null
            ? LidarrArtist.fromJson(json['artist'] as Map<String, dynamic>)
            : null,
        statistics: json['statistics'] != null
            ? LidarrAlbumStatistics.fromJson(
                json['statistics'] as Map<String, dynamic>)
            : null,
        images: _modelList(json['images'], LidarrImage.fromJson),
        genres: _stringList(json['genres']),
      );

  String get artistName => artist?.artistName ?? '';

  int get year => releaseDate?.year ?? 0;

  /// Whether every monitored track is on disk. An album with files but no
  /// track count makes no completeness claim, so any file counts as complete
  /// rather than stranding it in partial forever (mirrors the server rule).
  bool get isComplete {
    final stats = statistics;
    if (stats == null || stats.trackFileCount <= 0) return false;
    if (stats.trackCount <= 0) return true;
    return stats.trackFileCount >= stats.trackCount;
  }

  bool get hasFiles => (statistics?.trackFileCount ?? 0) > 0;

  /// The album's cover URL as Lidarr reports it (relative for library
  /// records, remote for lookups).
  String? get coverUrl => remoteCover ?? _pickCoverUrl(images);
}

class LidarrQualityProfile {
  final int id;
  final String name;

  const LidarrQualityProfile({required this.id, required this.name});

  factory LidarrQualityProfile.fromJson(Map<String, dynamic> json) =>
      LidarrQualityProfile(
        id: json['id'] as int? ?? 0,
        name: json['name'] as String? ?? '',
      );
}

class LidarrMetadataProfile {
  final int id;
  final String name;

  const LidarrMetadataProfile({required this.id, required this.name});

  factory LidarrMetadataProfile.fromJson(Map<String, dynamic> json) =>
      LidarrMetadataProfile(
        id: json['id'] as int? ?? 0,
        name: json['name'] as String? ?? '',
      );
}

class LidarrSystemStatus {
  final String version;
  final String? instanceName;

  const LidarrSystemStatus({this.version = '', this.instanceName});

  factory LidarrSystemStatus.fromJson(Map<String, dynamic> json) =>
      LidarrSystemStatus(
        version: json['version'] as String? ?? '',
        instanceName: json['instanceName'] as String?,
      );
}

/// One grouped warning/error Lidarr attaches to a queue item.
class LidarrStatusMessage {
  final String title;
  final List<String> messages;

  const LidarrStatusMessage({this.title = '', this.messages = const []});

  factory LidarrStatusMessage.fromJson(Map<String, dynamic> json) =>
      LidarrStatusMessage(
        title: json['title'] as String? ?? '',
        messages: _stringList(json['messages']),
      );
}

class LidarrQueueItem {
  final int id;
  final int? artistId;
  final int? albumId;
  final String title;
  final String? artistTitle;
  final String? albumTitle;
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
  final List<LidarrStatusMessage> statusMessageGroups;
  final String? downloadId;
  final String? quality;
  final int trackFileCount;
  final int trackHasFileCount;

  const LidarrQueueItem({
    required this.id,
    this.artistId,
    this.albumId,
    required this.title,
    this.artistTitle,
    this.albumTitle,
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
    this.downloadId,
    this.quality,
    this.trackFileCount = 0,
    this.trackHasFileCount = 0,
  });

  factory LidarrQueueItem.fromJson(Map<String, dynamic> json) {
    final messages = <String>[];
    final groups = <LidarrStatusMessage>[];
    for (final entry in _asList(json['statusMessages'])) {
      final map = _asMap(entry);
      if (map == null) continue;
      groups.add(LidarrStatusMessage.fromJson(map));
      final groupMessages = _stringList(map['messages']);
      for (final msg in groupMessages) {
        if (msg.isNotEmpty) messages.add(msg);
      }
      if (map['messages'] == null || groupMessages.isEmpty) {
        final title = map['title'] as String?;
        if (title != null && title.isNotEmpty) messages.add(title);
      }
    }
    final album = json['album'] as Map<String, dynamic>?;
    return LidarrQueueItem(
      id: json['id'] as int? ?? 0,
      artistId: json['artistId'] as int?,
      albumId: album?['id'] as int? ?? json['albumId'] as int?,
      title: json['title'] as String? ?? 'Unknown',
      artistTitle:
          (json['artist'] as Map<String, dynamic>?)?['artistName'] as String?,
      albumTitle: album?['title'] as String?,
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
      downloadId: json['downloadId'] as String?,
      quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
          as String?,
      trackFileCount: json['trackFileCount'] as int? ?? 0,
      trackHasFileCount: json['trackHasFileCount'] as int? ?? 0,
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

  /// e.g. "Artist Name • Album title", or null when context is missing.
  String? get albumLabel {
    final hasAlbum = albumTitle != null && albumTitle!.isNotEmpty;
    final hasArtist = artistTitle != null && artistTitle!.isNotEmpty;
    if (hasArtist && hasAlbum) return '$artistTitle • $albumTitle';
    if (hasAlbum) return albumTitle;
    if (hasArtist) return artistTitle;
    return null;
  }
}

/// A single Lidarr history event.
class LidarrHistoryRecord {
  final int id;
  final String sourceTitle;
  final String eventType;
  final DateTime? date;
  final String? quality;
  final int? artistId;
  final int? albumId;
  final Map<String, String> data;
  final String? downloadId;

  const LidarrHistoryRecord({
    required this.id,
    this.sourceTitle = '',
    this.eventType = '',
    this.date,
    this.quality,
    this.artistId,
    this.albumId,
    this.data = const {},
    this.downloadId,
  });

  factory LidarrHistoryRecord.fromJson(Map<String, dynamic> json) =>
      LidarrHistoryRecord(
        id: json['id'] as int? ?? 0,
        sourceTitle: json['sourceTitle'] as String? ?? '',
        eventType: json['eventType'] as String? ?? '',
        date: DateTime.tryParse(json['date'] as String? ?? ''),
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
        artistId: json['artistId'] as int?,
        albumId: json['albumId'] as int?,
        data: ((json['data'] as Map<String, dynamic>?) ?? {})
            .map((k, v) => MapEntry(k, v?.toString() ?? '')),
        downloadId: json['downloadId'] as String?,
      );

  /// Indexer the release was grabbed from, e.g. "NZBgeek (Prowlarr)".
  String? get indexer => data['indexer'];

  /// Release group parsed from the grab, when present.
  String? get releaseGroup => data['releaseGroup'];
}

/// Paged envelope for Lidarr history.
class LidarrHistoryPage {
  final List<LidarrHistoryRecord> records;
  final int totalRecords;

  const LidarrHistoryPage({this.records = const [], this.totalRecords = 0});

  factory LidarrHistoryPage.fromJson(Map<String, dynamic> json) =>
      LidarrHistoryPage(
        records: _modelList(json['records'], LidarrHistoryRecord.fromJson),
        totalRecords: json['totalRecords'] as int? ?? 0,
      );
}

/// Paged envelope for Lidarr wanted albums (missing / cutoff unmet). Records
/// are full album resources with artist context.
class LidarrWantedPage {
  final List<LidarrAlbum> records;
  final int totalRecords;

  const LidarrWantedPage({this.records = const [], this.totalRecords = 0});

  factory LidarrWantedPage.fromJson(Map<String, dynamic> json) =>
      LidarrWantedPage(
        records: _modelList(json['records'], LidarrAlbum.fromJson),
        totalRecords: json['totalRecords'] as int? ?? 0,
      );
}

/// One rejection reason attached to a manual-import candidate.
class LidarrImportRejection {
  final String reason;
  final String type;

  const LidarrImportRejection({this.reason = '', this.type = ''});

  factory LidarrImportRejection.fromJson(Map<String, dynamic> json) =>
      LidarrImportRejection(
        reason: json['reason'] as String? ?? '',
        type: json['type'] as String? ?? '',
      );

  bool get isPermanent => type.toLowerCase() == 'permanent';
}

/// One importable file returned by GET /manualimport for a download. Mirrors
/// [ChaptarrManualImportCandidate] but keyed by `artistId` + `albumId` +
/// matched track ids. The `quality` blob is kept VERBATIM and round-tripped
/// back into the ManualImport command unchanged (re-modelling it loses fields
/// Lidarr needs).
class LidarrManualImportCandidate {
  final int id;
  final String path;
  final String? folderName;
  final String name;
  final int size;
  final int? artistId;
  final int? albumId;
  final int? albumReleaseId;
  final List<int> trackIds;
  final Map<String, dynamic>? quality;
  final String? releaseGroup;
  final String? downloadId;
  final List<LidarrImportRejection> rejections;

  const LidarrManualImportCandidate({
    required this.id,
    required this.path,
    this.folderName,
    this.name = '',
    this.size = 0,
    this.artistId,
    this.albumId,
    this.albumReleaseId,
    this.trackIds = const [],
    this.quality,
    this.releaseGroup,
    this.downloadId,
    this.rejections = const [],
  });

  factory LidarrManualImportCandidate.fromJson(Map<String, dynamic> json) =>
      LidarrManualImportCandidate(
        id: json['id'] as int? ?? 0,
        path: json['path'] as String? ?? '',
        folderName: json['folderName'] as String?,
        name: json['name'] as String? ??
            (json['relativePath'] as String?) ??
            (json['path'] as String? ?? ''),
        size: (json['size'] as num?)?.toInt() ?? 0,
        artistId: (json['artist'] as Map<String, dynamic>?)?['id'] as int?,
        albumId: (json['album'] as Map<String, dynamic>?)?['id'] as int?,
        albumReleaseId: (json['albumReleaseId'] as num?)?.toInt(),
        trackIds: (json['tracks'] as List<dynamic>? ?? const [])
            .whereType<Map<String, dynamic>>()
            .map((t) => (t['id'] as num?)?.toInt() ?? 0)
            .where((id) => id > 0)
            .toList(),
        quality: json['quality'] as Map<String, dynamic>?,
        releaseGroup: json['releaseGroup'] as String?,
        downloadId: json['downloadId'] as String?,
        rejections:
            _modelList(json['rejections'], LidarrImportRejection.fromJson),
      );

  bool get hasPermanentRejection => rejections.any((r) => r.isPermanent);

  /// A candidate is importable once Lidarr has matched it to an album's
  /// tracks — an album guess with no matched track still cannot import.
  bool get isMapped => albumId != null && trackIds.isNotEmpty;
  String get sizeFormatted => _formatBytes(size);

  /// The file entry for the ManualImport command, with quality sent back
  /// exactly as received.
  Map<String, dynamic> toImportFile() => {
        'path': path,
        if (folderName != null) 'folderName': folderName,
        if (artistId != null) 'artistId': artistId,
        if (albumId != null) 'albumId': albumId,
        if (albumReleaseId != null) 'albumReleaseId': albumReleaseId,
        if (trackIds.isNotEmpty) 'trackIds': trackIds,
        if (quality != null) 'quality': quality,
        if (releaseGroup != null) 'releaseGroup': releaseGroup,
        if (downloadId != null) 'downloadId': downloadId,
      };
}

/// One release row from Lidarr's interactive search for an album.
class LidarrRelease {
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

  const LidarrRelease({
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

  factory LidarrRelease.fromJson(Map<String, dynamic> json) => LidarrRelease(
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
        rejections: _asList(json['rejections'])
            .map((r) => r is Map<String, dynamic>
                ? (r['reason']?.toString() ?? '')
                : r.toString())
            .where((r) => r.isNotEmpty)
            .toList(),
      );

  bool get isTorrent => protocol == 'torrent';
  String get sizeFormatted => _formatBytes(size);
  String get ageFormatted {
    if (age >= 1) return '${age}d';
    if (ageHours >= 1) return '${ageHours.round()}h';
    return '<1h';
  }
}
/// One completed music file on disk, as Lidarr reports it. The id is what the
/// media-download ticket API takes; [path] is the arr-reported path the
/// coverage check maps against the server's roots.
class LidarrTrackFile {
  final int id;
  final int albumId;
  final String path;
  final int size;
  final String? qualityName;

  const LidarrTrackFile({
    required this.id,
    this.albumId = 0,
    this.path = '',
    this.size = 0,
    this.qualityName,
  });

  factory LidarrTrackFile.fromJson(Map<String, dynamic> json) =>
      LidarrTrackFile(
        id: json['id'] as int? ?? 0,
        albumId: json['albumId'] as int? ?? 0,
        path: json['path'] as String? ?? '',
        size: (json['size'] as num?)?.toInt() ?? 0,
        qualityName: (json['quality'] as Map<String, dynamic>?)?['quality']
            ?['name'] as String?,
      );

  String get sizeFormatted => _formatBytes(size);

  /// The file's basename, the label fallback when no track record names it.
  String get fileName {
    final segments = path.split(RegExp(r'[\\/]'));
    return segments.isEmpty ? path : segments.last;
  }
}

/// One track of an album — the naming join for downloads: a track knows its
/// number, title, and which file (if any) holds it.
class LidarrTrack {
  final int id;
  final int trackFileId;
  final int absoluteTrackNumber;
  final int mediumNumber;
  final String title;

  const LidarrTrack({
    required this.id,
    this.trackFileId = 0,
    this.absoluteTrackNumber = 0,
    this.mediumNumber = 0,
    this.title = '',
  });

  factory LidarrTrack.fromJson(Map<String, dynamic> json) => LidarrTrack(
        id: json['id'] as int? ?? 0,
        trackFileId: json['trackFileId'] as int? ?? 0,
        absoluteTrackNumber: (json['absoluteTrackNumber'] as num?)?.toInt() ??
            int.tryParse(json['trackNumber']?.toString() ?? '') ??
            0,
        mediumNumber: (json['mediumNumber'] as num?)?.toInt() ?? 0,
        title: json['title'] as String? ?? '',
      );
}
