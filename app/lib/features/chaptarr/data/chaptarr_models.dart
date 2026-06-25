/// The medium a book file is stored in. Mirrors the Go `FormatOf` helper:
/// ebook formats (EPUB/MOBI/…) vs audiobook formats (MP3/M4B/…).
enum BookFormat { ebook, audiobook, unknown }

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

List<String> _stringList(dynamic value, {bool splitCommaString = false}) {
  if (value is List) {
    return value
        .map((item) => item.toString())
        .where((item) => item.isNotEmpty)
        .toList();
  }
  if (value is String) {
    final trimmed = value.trim();
    if (trimmed.isEmpty) return const [];
    if (splitCommaString) {
      return trimmed
          .split(',')
          .map((item) => item.trim())
          .where((item) => item.isNotEmpty)
          .toList();
    }
    return [trimmed];
  }
  return const [];
}

/// Classifies a Chaptarr/Readarr quality name into a [BookFormat]. Matches the
/// server's `FormatOf` helper: a case-insensitive substring check against the
/// known ebook and audiobook quality names. Unknown/empty names fall through to
/// [BookFormat.unknown].
BookFormat bookFormatFromQuality(String? qualityName) {
  if (qualityName == null || qualityName.isEmpty) return BookFormat.unknown;
  final q = qualityName.toUpperCase();
  const ebookTokens = [
    'EPUB',
    'MOBI',
    'AZW3',
    'AZW',
    'PDF',
    'CBZ',
    'CBR',
    'KEPUB',
    'EBOOK',
    'E-BOOK',
    'KINDLE',
    'NOOK',
    'KOBO',
    'DIGITAL'
  ];
  for (final t in ebookTokens) {
    if (q.contains(t)) return BookFormat.ebook;
  }
  const audioTokens = [
    'MP3',
    'M4B',
    'M4A',
    'FLAC',
    'AAC',
    'OGG',
    'OPUS',
    'AUDIOBOOK',
    'AUDIO BOOK',
    'AUDIBLE',
    'AUDIO CD',
    'MP3 CD',
    'AUDIO'
  ];
  for (final t in audioTokens) {
    if (q.contains(t)) return BookFormat.audiobook;
  }
  return BookFormat.unknown;
}

/// An author managed by Chaptarr.
class ChaptarrAuthor {
  final int id;
  final String authorName;
  final String? foreignAuthorId;
  final String? titleSlug;
  final String? overview;
  final String? status;
  final bool monitored;
  final String? path;
  final int qualityProfileId;
  final int metadataProfileId;
  final ChaptarrAuthorStatistics? statistics;
  final List<ChaptarrImage> images;
  final List<String> genres;

  const ChaptarrAuthor({
    required this.id,
    required this.authorName,
    this.foreignAuthorId,
    this.titleSlug,
    this.overview,
    this.status,
    this.monitored = true,
    this.path,
    this.qualityProfileId = 0,
    this.metadataProfileId = 0,
    this.statistics,
    this.images = const [],
    this.genres = const [],
  });

  factory ChaptarrAuthor.fromJson(Map<String, dynamic> json) => ChaptarrAuthor(
        id: json['id'] as int? ?? 0,
        authorName: json['authorName'] as String? ?? 'Unknown Author',
        foreignAuthorId: json['foreignAuthorId'] as String?,
        titleSlug: json['titleSlug'] as String?,
        overview: json['overview'] as String?,
        status: json['status'] as String?,
        monitored: json['monitored'] as bool? ?? true,
        path: json['path'] as String?,
        qualityProfileId: json['qualityProfileId'] as int? ?? 0,
        metadataProfileId: json['metadataProfileId'] as int? ?? 0,
        statistics: json['statistics'] != null
            ? ChaptarrAuthorStatistics.fromJson(
                json['statistics'] as Map<String, dynamic>)
            : null,
        images: _modelList(json['images'], ChaptarrImage.fromJson),
        genres: _stringList(json['genres'], splitCommaString: true),
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'authorName': authorName,
        'foreignAuthorId': foreignAuthorId,
        'titleSlug': titleSlug,
        'overview': overview,
        'status': status,
        'monitored': monitored,
        'path': path,
        'qualityProfileId': qualityProfileId,
        'metadataProfileId': metadataProfileId,
        'images': images.map((i) => i.toJson()).toList(),
        'genres': genres,
      };

  String? get coverUrl => _pickCoverUrl(images);

  double get percentComplete {
    if (statistics == null || statistics!.bookCount == 0) return 0;
    return statistics!.bookFileCount / statistics!.bookCount;
  }

  /// e.g. "12 / 15 books".
  String get bookCountLabel {
    final s = statistics;
    if (s == null) return '0 books';
    return '${s.bookFileCount} / ${s.bookCount} books';
  }
}

/// An artwork reference (cover/poster/fanart) on an author or book.
class ChaptarrImage {
  final String coverType;
  final String? url;
  final String? remoteUrl;

  const ChaptarrImage({required this.coverType, this.url, this.remoteUrl});

  factory ChaptarrImage.fromJson(Map<String, dynamic> json) => ChaptarrImage(
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

/// Picks a remote cover URL from a list of images, preferring `cover`/`poster`
/// art and falling back to the first image with a URL. Returns the raw `url`
/// field (this fork populates `url`, not `remoteUrl`) — absolute for author art,
/// relative (`/MediaCover/...`) for book covers, which the UI resolves through
/// the backend proxy via `chaptarrImageSource`.
String? _pickCoverUrl(List<ChaptarrImage> images) {
  bool hasUrl(ChaptarrImage i) => i.url != null && i.url!.isNotEmpty;
  for (final type in ['cover', 'poster']) {
    final match = images.where((i) => i.coverType == type && hasUrl(i));
    if (match.isNotEmpty) return match.first.url;
  }
  final withUrl = images.where(hasUrl);
  return withUrl.isNotEmpty ? withUrl.first.url : null;
}

class ChaptarrAuthorStatistics {
  final int bookCount;
  final int bookFileCount;
  final int availableBookCount;
  final int totalBookCount;
  final int sizeOnDisk;
  final double percentOfBooks;

  const ChaptarrAuthorStatistics({
    this.bookCount = 0,
    this.bookFileCount = 0,
    this.availableBookCount = 0,
    this.totalBookCount = 0,
    this.sizeOnDisk = 0,
    this.percentOfBooks = 0,
  });

  factory ChaptarrAuthorStatistics.fromJson(Map<String, dynamic> json) =>
      ChaptarrAuthorStatistics(
        bookCount: json['bookCount'] as int? ?? 0,
        bookFileCount: json['bookFileCount'] as int? ?? 0,
        availableBookCount: json['availableBookCount'] as int? ?? 0,
        totalBookCount: json['totalBookCount'] as int? ?? 0,
        sizeOnDisk: (json['sizeOnDisk'] as num?)?.toInt() ?? 0,
        percentOfBooks: (json['percentOfBooks'] as num?)?.toDouble() ?? 0,
      );

  String get sizeFormatted => _formatBytes(sizeOnDisk);
}

class ChaptarrBookStatistics {
  final int bookFileCount;
  final int bookCount;
  final int sizeOnDisk;
  final double percentOfBooks;

  const ChaptarrBookStatistics({
    this.bookFileCount = 0,
    this.bookCount = 0,
    this.sizeOnDisk = 0,
    this.percentOfBooks = 0,
  });

  factory ChaptarrBookStatistics.fromJson(Map<String, dynamic> json) =>
      ChaptarrBookStatistics(
        bookFileCount: json['bookFileCount'] as int? ?? 0,
        bookCount: json['bookCount'] as int? ?? 0,
        sizeOnDisk: (json['sizeOnDisk'] as num?)?.toInt() ?? 0,
        percentOfBooks: (json['percentOfBooks'] as num?)?.toDouble() ?? 0,
      );

  String get sizeFormatted => _formatBytes(sizeOnDisk);
}

/// One edition of a book (a specific publication: ebook/audiobook, publisher,
/// ISBN). Mirrors Sonarr's per-season granularity for a book.
class ChaptarrEdition {
  final int id;
  final int bookId;
  final String? title;
  final String? format;
  final String? asin;
  final String? isbn13;
  final String? overview;
  final String? publisher;
  final int pageCount;
  final bool monitored;
  final bool manualAdd;
  final bool? isEbook;
  final List<ChaptarrImage> images;

  const ChaptarrEdition({
    required this.id,
    this.bookId = 0,
    this.title,
    this.format,
    this.asin,
    this.isbn13,
    this.overview,
    this.publisher,
    this.pageCount = 0,
    this.monitored = true,
    this.manualAdd = false,
    this.isEbook,
    this.images = const [],
  });

  factory ChaptarrEdition.fromJson(Map<String, dynamic> json) =>
      ChaptarrEdition(
        id: json['id'] as int? ?? 0,
        bookId: json['bookId'] as int? ?? 0,
        title: json['title'] as String?,
        format: json['format'] as String?,
        asin: json['asin'] as String?,
        isbn13: json['isbn13'] as String?,
        overview: json['overview'] as String?,
        publisher: json['publisher'] as String?,
        pageCount: json['pageCount'] as int? ?? 0,
        monitored: json['monitored'] as bool? ?? true,
        manualAdd: json['manualAdd'] as bool? ?? false,
        isEbook: json.containsKey('isEbook') ? json['isEbook'] as bool? : null,
        images: _modelList(json['images'], ChaptarrImage.fromJson),
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'bookId': bookId,
        'title': title,
        'format': format,
        'asin': asin,
        'isbn13': isbn13,
        'overview': overview,
        'publisher': publisher,
        'pageCount': pageCount,
        'monitored': monitored,
        'manualAdd': manualAdd,
        'isEbook': isEbook,
        'images': images.map((i) => i.toJson()).toList(),
      };

  /// The medium this edition represents: audiobook when Chaptarr flags it as
  /// non-ebook, ebook otherwise (also inferring from the `format` string).
  BookFormat get bookFormat {
    final fromFormat = bookFormatFromQuality(format);
    if (fromFormat != BookFormat.unknown) return fromFormat;
    final fromTitle = bookFormatFromQuality(title);
    if (fromTitle != BookFormat.unknown) return fromTitle;
    if (isEbook == true) return BookFormat.ebook;
    if (isEbook == false) return BookFormat.audiobook;
    return BookFormat.unknown;
  }
}

/// A book managed by Chaptarr. Carries its editions (and, when fetched with
/// author context, the owning author) plus statistics for the status line.
class ChaptarrBook {
  final int id;
  final String title;
  final int authorId;
  final String? foreignBookId;
  final String? titleSlug;
  final String? overview;
  final DateTime? releaseDate;
  final bool monitored;

  /// Chaptarr (this fork) tracks a title's ebook and audiobook as separate book
  /// records distinguished by mediaType ("ebook"/"audiobook").
  final String? mediaType;
  final bool anyEditionOk;
  final int pageCount;
  final ChaptarrAuthorContext? author;
  final ChaptarrBookStatistics? statistics;
  final List<ChaptarrEdition> editions;
  final List<ChaptarrImage> images;
  final List<String> genres;

  const ChaptarrBook({
    required this.id,
    required this.title,
    this.authorId = 0,
    this.foreignBookId,
    this.titleSlug,
    this.overview,
    this.releaseDate,
    this.monitored = true,
    this.mediaType,
    this.anyEditionOk = true,
    this.pageCount = 0,
    this.author,
    this.statistics,
    this.editions = const [],
    this.images = const [],
    this.genres = const [],
  });

  factory ChaptarrBook.fromJson(Map<String, dynamic> json) => ChaptarrBook(
        id: json['id'] as int? ?? 0,
        title: json['title'] as String? ?? 'Untitled',
        authorId: json['authorId'] as int? ?? 0,
        foreignBookId: json['foreignBookId'] as String?,
        titleSlug: json['titleSlug'] as String?,
        overview: json['overview'] as String?,
        releaseDate: DateTime.tryParse(json['releaseDate'] as String? ?? ''),
        monitored: json['monitored'] as bool? ?? true,
        mediaType: json['mediaType'] as String?,
        anyEditionOk: json['anyEditionOk'] as bool? ?? true,
        pageCount: json['pageCount'] as int? ?? 0,
        author: json['author'] != null
            ? ChaptarrAuthorContext.fromJson(
                json['author'] as Map<String, dynamic>)
            : null,
        statistics: json['statistics'] != null
            ? ChaptarrBookStatistics.fromJson(
                json['statistics'] as Map<String, dynamic>)
            : null,
        editions: _modelList(json['editions'], ChaptarrEdition.fromJson),
        images: _modelList(json['images'], ChaptarrImage.fromJson),
        genres: _stringList(json['genres'], splitCommaString: true),
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'title': title,
        'authorId': authorId,
        'foreignBookId': foreignBookId,
        'titleSlug': titleSlug,
        'overview': overview,
        'releaseDate': releaseDate?.toIso8601String(),
        'monitored': monitored,
        'mediaType': mediaType,
        'anyEditionOk': anyEditionOk,
        'pageCount': pageCount,
        'editions': editions.map((e) => e.toJson()).toList(),
        'images': images.map((i) => i.toJson()).toList(),
        'genres': genres,
      };

  String? get coverUrl => _pickCoverUrl(images);

  double get percentComplete {
    if (statistics == null || statistics!.bookCount == 0) return 0;
    return statistics!.bookFileCount / statistics!.bookCount;
  }

  bool get hasFile => (statistics?.bookFileCount ?? 0) > 0;

  /// The set of book formats represented across this book's editions.
  Set<BookFormat> get formats {
    final set = <BookFormat>{};
    for (final e in editions) {
      final f = e.bookFormat;
      if (f != BookFormat.unknown) set.add(f);
    }
    return set;
  }

  /// The single format this record represents. Chaptarr stores a title's ebook
  /// and audiobook as separate records distinguished by [mediaType]; fall back
  /// to a lone edition's format, else unknown.
  BookFormat get format {
    switch (mediaType) {
      case 'ebook':
        return BookFormat.ebook;
      case 'audiobook':
        return BookFormat.audiobook;
    }
    final fs = formats;
    return fs.length == 1 ? fs.first : BookFormat.unknown;
  }

  /// Groups the ebook and audiobook records of one title (they share a
  /// foreignBookId). Falls back to the unique id so records without a
  /// foreignBookId never merge into one another.
  String get groupKey => (foreignBookId != null && foreignBookId!.isNotEmpty)
      ? foreignBookId!
      : 'id:$id';
}

/// A downloaded book file: drives the "EPUB — 4.6 MB" status line. The raw
/// `quality` map is kept for verbatim round-tripping, like Sonarr's
/// episodeFile.
class ChaptarrBookFile {
  final int id;
  final int authorId;
  final int bookId;
  final int editionId;
  final String? path;
  final int size;
  final DateTime? dateAdded;

  /// The raw `quality` blob exactly as returned (kept for verbatim round-trip);
  /// use [qualityName] for the display string.
  final Map<String, dynamic>? quality;

  const ChaptarrBookFile({
    required this.id,
    this.authorId = 0,
    this.bookId = 0,
    this.editionId = 0,
    this.path,
    this.size = 0,
    this.dateAdded,
    this.quality,
  });

  factory ChaptarrBookFile.fromJson(Map<String, dynamic> json) =>
      ChaptarrBookFile(
        id: json['id'] as int? ?? 0,
        authorId: json['authorId'] as int? ?? 0,
        bookId: json['bookId'] as int? ?? 0,
        editionId: json['editionId'] as int? ?? 0,
        path: json['path'] as String?,
        size: (json['size'] as num?)?.toInt() ?? 0,
        dateAdded: DateTime.tryParse(json['dateAdded'] as String? ?? ''),
        quality: json['quality'] as Map<String, dynamic>?,
      );

  String? get qualityName =>
      (quality?['quality'] as Map<String, dynamic>?)?['name'] as String?;

  BookFormat get format => bookFormatFromQuality(qualityName);

  String get sizeFormatted => _formatBytes(size);
}

/// Lightweight author reference embedded on a book (when fetched with
/// includeAuthor).
class ChaptarrAuthorContext {
  final int id;
  final String authorName;
  final String? foreignAuthorId;

  const ChaptarrAuthorContext({
    required this.id,
    this.authorName = '',
    this.foreignAuthorId,
  });

  factory ChaptarrAuthorContext.fromJson(Map<String, dynamic> json) =>
      ChaptarrAuthorContext(
        id: json['id'] as int? ?? 0,
        authorName: json['authorName'] as String? ?? '',
        foreignAuthorId: json['foreignAuthorId'] as String?,
      );
}

/// Lightweight book reference embedded on a queue/import item (when fetched
/// with includeBook).
class ChaptarrBookContext {
  final int id;
  final String title;
  final DateTime? releaseDate;

  const ChaptarrBookContext({
    required this.id,
    this.title = '',
    this.releaseDate,
  });

  factory ChaptarrBookContext.fromJson(Map<String, dynamic> json) =>
      ChaptarrBookContext(
        id: json['id'] as int? ?? 0,
        title: json['title'] as String? ?? '',
        releaseDate: DateTime.tryParse(json['releaseDate'] as String? ?? ''),
      );
}

class ChaptarrQualityProfile {
  final int id;
  final String name;

  const ChaptarrQualityProfile({required this.id, required this.name});

  factory ChaptarrQualityProfile.fromJson(Map<String, dynamic> json) =>
      ChaptarrQualityProfile(
        id: json['id'] as int,
        name: json['name'] as String,
      );
}

class ChaptarrMetadataProfile {
  final int id;
  final String name;

  const ChaptarrMetadataProfile({required this.id, required this.name});

  factory ChaptarrMetadataProfile.fromJson(Map<String, dynamic> json) =>
      ChaptarrMetadataProfile(
        id: json['id'] as int,
        name: json['name'] as String,
      );
}

class ChaptarrRootFolder {
  final int id;
  final String path;
  final int? freeSpace;

  const ChaptarrRootFolder(
      {required this.id, required this.path, this.freeSpace});

  factory ChaptarrRootFolder.fromJson(Map<String, dynamic> json) =>
      ChaptarrRootFolder(
        id: json['id'] as int,
        path: json['path'] as String,
        freeSpace: json['freeSpace'] as int?,
      );
}

class ChaptarrSystemStatus {
  final String version;

  const ChaptarrSystemStatus({required this.version});

  factory ChaptarrSystemStatus.fromJson(Map<String, dynamic> json) =>
      ChaptarrSystemStatus(version: json['version'] as String? ?? 'Unknown');
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
class ChaptarrStatusMessage {
  final String title;
  final List<String> messages;

  const ChaptarrStatusMessage({this.title = '', this.messages = const []});

  factory ChaptarrStatusMessage.fromJson(Map<String, dynamic> json) =>
      ChaptarrStatusMessage(
        title: json['title'] as String? ?? '',
        messages: _stringList(json['messages']),
      );
}

/// One item in the Chaptarr download queue (fetched with author + book
/// details).
class ChaptarrQueueItem {
  final int id;
  final int? authorId;
  final int? bookId;
  final String title;
  final String? authorTitle;
  final String? bookTitle;
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
  final List<ChaptarrStatusMessage> statusMessageGroups;
  final String? downloadId;
  final String? quality;

  const ChaptarrQueueItem({
    required this.id,
    this.authorId,
    this.bookId,
    required this.title,
    this.authorTitle,
    this.bookTitle,
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
  });

  factory ChaptarrQueueItem.fromJson(Map<String, dynamic> json) {
    final messages = <String>[];
    final groups = <ChaptarrStatusMessage>[];
    for (final entry in _asList(json['statusMessages'])) {
      final map = _asMap(entry);
      if (map == null) continue;
      groups.add(ChaptarrStatusMessage.fromJson(map));
      final groupMessages = _stringList(map['messages']);
      for (final msg in groupMessages) {
        final text = msg.toString();
        if (text.isNotEmpty) messages.add(text);
      }
      if (map['messages'] == null || groupMessages.isEmpty) {
        final title = map['title'] as String?;
        if (title != null && title.isNotEmpty) messages.add(title);
      }
    }
    final book = json['book'] as Map<String, dynamic>?;
    return ChaptarrQueueItem(
      id: json['id'] as int? ?? 0,
      authorId: json['authorId'] as int?,
      bookId: book?['id'] as int? ?? json['bookId'] as int?,
      title: json['title'] as String? ?? 'Unknown',
      authorTitle:
          (json['author'] as Map<String, dynamic>?)?['authorName'] as String?,
      bookTitle: book?['title'] as String?,
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

  /// e.g. "Author Name • Book title", or null when context is missing.
  String? get bookLabel {
    final hasBook = bookTitle != null && bookTitle!.isNotEmpty;
    final hasAuthor = authorTitle != null && authorTitle!.isNotEmpty;
    if (hasAuthor && hasBook) return '$authorTitle • $bookTitle';
    if (hasBook) return bookTitle;
    if (hasAuthor) return authorTitle;
    return null;
  }
}

/// A single Chaptarr history event.
class ChaptarrHistoryRecord {
  final int id;
  final String sourceTitle;
  final String eventType;
  final DateTime? date;
  final String? quality;
  final int? authorId;
  final int? bookId;
  final Map<String, String> data;
  final String? downloadId;

  const ChaptarrHistoryRecord({
    required this.id,
    this.sourceTitle = '',
    this.eventType = '',
    this.date,
    this.quality,
    this.authorId,
    this.bookId,
    this.data = const {},
    this.downloadId,
  });

  factory ChaptarrHistoryRecord.fromJson(Map<String, dynamic> json) =>
      ChaptarrHistoryRecord(
        id: json['id'] as int? ?? 0,
        sourceTitle: json['sourceTitle'] as String? ?? '',
        eventType: json['eventType'] as String? ?? '',
        date: DateTime.tryParse(json['date'] as String? ?? ''),
        quality: (json['quality'] as Map<String, dynamic>?)?['quality']?['name']
            as String?,
        authorId: json['authorId'] as int?,
        bookId: json['bookId'] as int?,
        data: ((json['data'] as Map<String, dynamic>?) ?? {})
            .map((k, v) => MapEntry(k, v?.toString() ?? '')),
        downloadId: json['downloadId'] as String?,
      );

  /// Indexer the release was grabbed from, e.g. "NZBgeek (Prowlarr)".
  String? get indexer => data['indexer'];

  /// Release group parsed from the grab, when present.
  String? get releaseGroup => data['releaseGroup'];
}

/// Paged envelope for Chaptarr history.
class ChaptarrHistoryPage {
  final List<ChaptarrHistoryRecord> records;
  final int totalRecords;

  const ChaptarrHistoryPage({this.records = const [], this.totalRecords = 0});

  factory ChaptarrHistoryPage.fromJson(Map<String, dynamic> json) =>
      ChaptarrHistoryPage(
        records: _modelList(json['records'], ChaptarrHistoryRecord.fromJson),
        totalRecords: json['totalRecords'] as int? ?? 0,
      );
}

/// One wanted book (missing or cutoff unmet) with author context.
class ChaptarrWantedRecord {
  final int id;
  final int authorId;
  final int bookId;
  final String? title;
  final DateTime? releaseDate;
  final bool monitored;
  final String? authorTitle;

  const ChaptarrWantedRecord({
    required this.id,
    this.authorId = 0,
    this.bookId = 0,
    this.title,
    this.releaseDate,
    this.monitored = true,
    this.authorTitle,
  });

  factory ChaptarrWantedRecord.fromJson(Map<String, dynamic> json) =>
      ChaptarrWantedRecord(
        id: json['id'] as int? ?? 0,
        authorId: json['authorId'] as int? ?? 0,
        bookId: json['id'] as int? ?? 0,
        title: json['title'] as String?,
        releaseDate: DateTime.tryParse(json['releaseDate'] as String? ?? ''),
        monitored: json['monitored'] as bool? ?? true,
        authorTitle:
            (json['author'] as Map<String, dynamic>?)?['authorName'] as String?,
      );
}

/// Paged envelope for Chaptarr wanted books (missing / cutoff unmet).
class ChaptarrWantedPage {
  final List<ChaptarrWantedRecord> records;
  final int totalRecords;

  const ChaptarrWantedPage({this.records = const [], this.totalRecords = 0});

  factory ChaptarrWantedPage.fromJson(Map<String, dynamic> json) =>
      ChaptarrWantedPage(
        records: _modelList(json['records'], ChaptarrWantedRecord.fromJson),
        totalRecords: json['totalRecords'] as int? ?? 0,
      );
}

/// A release returned by Chaptarr's interactive search.
class ChaptarrRelease {
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

  const ChaptarrRelease({
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

  factory ChaptarrRelease.fromJson(Map<String, dynamic> json) =>
      ChaptarrRelease(
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

/// Why a manual-import candidate was rejected. `type` is "permanent" or
/// "temporary"; permanent rejections only import with force.
class ChaptarrImportRejection {
  final String reason;
  final String type;

  const ChaptarrImportRejection({this.reason = '', this.type = ''});

  factory ChaptarrImportRejection.fromJson(Map<String, dynamic> json) =>
      ChaptarrImportRejection(
        reason: json['reason'] as String? ?? '',
        type: json['type'] as String? ?? '',
      );

  bool get isPermanent => type.toLowerCase() == 'permanent';
}

/// One importable file returned by GET /manualimport for a book. Mirrors
/// [SonarrManualImportCandidate] but keyed by `authorId` + `bookId`. The
/// `quality` blob is kept VERBATIM and round-tripped back into the ManualImport
/// command unchanged (re-modelling it loses fields Chaptarr needs).
class ChaptarrManualImportCandidate {
  final int id;
  final String path;
  final String? folderName;
  final String name;
  final int size;
  final int? authorId;
  final int? bookId;
  final Map<String, dynamic>? quality;
  final String? releaseGroup;
  final String? downloadId;
  final List<ChaptarrImportRejection> rejections;

  const ChaptarrManualImportCandidate({
    required this.id,
    required this.path,
    this.folderName,
    this.name = '',
    this.size = 0,
    this.authorId,
    this.bookId,
    this.quality,
    this.releaseGroup,
    this.downloadId,
    this.rejections = const [],
  });

  factory ChaptarrManualImportCandidate.fromJson(Map<String, dynamic> json) =>
      ChaptarrManualImportCandidate(
        id: json['id'] as int? ?? 0,
        path: json['path'] as String? ?? '',
        folderName: json['folderName'] as String?,
        name: json['name'] as String? ??
            (json['relativePath'] as String?) ??
            (json['path'] as String? ?? ''),
        size: (json['size'] as num?)?.toInt() ?? 0,
        authorId: (json['author'] as Map<String, dynamic>?)?['id'] as int?,
        bookId: (json['book'] as Map<String, dynamic>?)?['id'] as int?,
        quality: json['quality'] as Map<String, dynamic>?,
        releaseGroup: json['releaseGroup'] as String?,
        downloadId: json['downloadId'] as String?,
        rejections:
            _modelList(json['rejections'], ChaptarrImportRejection.fromJson),
      );

  bool get hasPermanentRejection => rejections.any((r) => r.isPermanent);

  /// A candidate is importable once Chaptarr has matched it to a book.
  bool get isMapped => bookId != null;
  String get sizeFormatted => _formatBytes(size);

  /// The file entry for the ManualImport command, with quality sent back
  /// exactly as received.
  Map<String, dynamic> toImportFile() => {
        'path': path,
        if (folderName != null) 'folderName': folderName,
        if (authorId != null) 'authorId': authorId,
        if (bookId != null) 'bookId': bookId,
        if (quality != null) 'quality': quality,
        if (releaseGroup != null) 'releaseGroup': releaseGroup,
        if (downloadId != null) 'downloadId': downloadId,
      };
}
