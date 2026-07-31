import 'dart:convert';

import 'package:dio/dio.dart';
import 'chaptarr_models.dart';

/// Coerces a Dio response body into a JSON list. Chaptarr's lookup/metadata
/// endpoints don't reliably send an `application/json` content-type, so Dio can
/// hand back the raw String instead of a decoded List — decode it here rather
/// than blindly casting (which threw "String is not a subtype of List").
List<dynamic> _jsonList(dynamic data) {
  if (data is List) return data;
  if (data is String && data.trim().isNotEmpty) {
    final decoded = jsonDecode(data);
    if (decoded is List) return decoded;
  }
  return const [];
}

/// Extracts the releases array from an interactive-search response. This
/// Chaptarr fork wraps results as `{"releases": [...]}` (alongside
/// `hiddenReleases`/`filterSummary`), whereas stock Servarr returns a bare
/// array. A String body (no `application/json` content-type) is decoded first.
/// Anything unexpected yields an empty list, so a stray object can no longer
/// throw the `_Map is not a subtype of List` cast error a bare cast did.
List<dynamic> _releaseList(dynamic data) {
  dynamic decoded = data;
  if (decoded is String && decoded.trim().isNotEmpty) {
    decoded = jsonDecode(decoded);
  }
  if (decoded is List) return decoded;
  if (decoded is Map) {
    final releases = decoded['releases'];
    if (releases is List) return releases;
  }
  return const [];
}

/// Networking layer for Chaptarr (a Readarr-family books service), proxied
/// through the Cantinarr backend. Note the Readarr API is v1 (not v3).
class ChaptarrApiService {
  final Dio _dio;
  final String _instanceId;

  ChaptarrApiService({required Dio backendDio, required String instanceId})
      : _dio = backendDio,
        _instanceId = instanceId;

  /// Returns the base path prefix for API calls.
  String get _basePath => '/api/instances/$_instanceId/api/v1';

  Future<ChaptarrSystemStatus> getSystemStatus() async {
    final resp = await _dio.get('$_basePath/system/status');
    return ChaptarrSystemStatus.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<ChaptarrAuthor>> getAuthors() async {
    final resp = await _dio.get('$_basePath/author');
    return (resp.data as List<dynamic>)
        .map((a) => ChaptarrAuthor.fromJson(a as Map<String, dynamic>))
        .toList();
  }

  Future<ChaptarrAuthor> getAuthorById(int id) async {
    final resp = await _dio.get('$_basePath/author/$id');
    return ChaptarrAuthor.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<ChaptarrAuthor>> lookupAuthor(String term) async {
    final resp = await _dio
        .get('$_basePath/author/lookup', queryParameters: {'term': term});
    return _jsonList(resp.data)
        .map((a) => ChaptarrAuthor.fromJson(a as Map<String, dynamic>))
        .toList();
  }

  /// Lists books, optionally narrowed to one author. Each book carries its
  /// editions inline — drives the per-book status line.
  Future<List<ChaptarrBook>> getBooks({int? authorId}) async {
    final resp = await _dio.get('$_basePath/book', queryParameters: {
      if (authorId != null) 'authorId': authorId,
    });
    return (resp.data as List<dynamic>)
        .map((b) => ChaptarrBook.fromJson(b as Map<String, dynamic>))
        .toList();
  }

  Future<ChaptarrBook> getBookById(int id) async {
    final resp = await _dio.get('$_basePath/book/$id');
    return ChaptarrBook.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<List<ChaptarrBook>> lookupBook(String term) async {
    final resp = await _dio
        .get('$_basePath/book/lookup', queryParameters: {'term': term});
    return _jsonList(resp.data)
        .map((b) => ChaptarrBook.fromJson(b as Map<String, dynamic>))
        .toList();
  }

  /// Lists downloaded book files, optionally narrowed to an author or book.
  Future<List<ChaptarrBookFile>> getBookFiles({
    int? authorId,
    int? bookId,
  }) async {
    final resp = await _dio.get('$_basePath/bookfile', queryParameters: {
      if (authorId != null) 'authorId': authorId,
      if (bookId != null) 'bookId': bookId,
    });
    return (resp.data as List<dynamic>)
        .map((f) => ChaptarrBookFile.fromJson(f as Map<String, dynamic>))
        .toList();
  }

  Future<List<ChaptarrQualityProfile>> getQualityProfiles() async {
    final resp = await _dio.get('$_basePath/qualityprofile');
    return (resp.data as List<dynamic>)
        .map((p) => ChaptarrQualityProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<ChaptarrMetadataProfile>> getMetadataProfiles() async {
    final resp = await _dio.get('$_basePath/metadataprofile');
    return (resp.data as List<dynamic>)
        .map((p) => ChaptarrMetadataProfile.fromJson(p as Map<String, dynamic>))
        .toList();
  }

  Future<List<ChaptarrRootFolder>> getRootFolders() async {
    final resp = await _dio.get('$_basePath/rootfolder');
    return (resp.data as List<dynamic>)
        .map((f) => ChaptarrRootFolder.fromJson(f as Map<String, dynamic>))
        .toList();
  }

  Future<ChaptarrAuthor> addAuthor(Map<String, dynamic> body) async {
    final resp = await _dio.post('$_basePath/author', data: body);
    return ChaptarrAuthor.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<ChaptarrBook> addBook(Map<String, dynamic> body) async {
    final resp = await _dio.post('$_basePath/book', data: body);
    return ChaptarrBook.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<ChaptarrBook> updateBook(Map<String, dynamic> body) async {
    final id = body['id'];
    final resp = await _dio.put('$_basePath/book/$id', data: body);
    return ChaptarrBook.fromJson(resp.data as Map<String, dynamic>);
  }

  Future<void> deleteAuthor(int id, {bool deleteFiles = false}) async {
    await _dio.delete('$_basePath/author/$id',
        queryParameters: {'deleteFiles': deleteFiles});
  }

  Future<void> deleteBook(int id, {bool deleteFiles = false}) async {
    await _dio.delete('$_basePath/book/$id',
        queryParameters: {'deleteFiles': deleteFiles});
  }

  /// Toggles monitoring for a set of books in one call. Chaptarr exposes a
  /// dedicated bulk endpoint, so unlike Sonarr's per-season GET-flip-PUT this
  /// is a single POST. Admin only (proxy requires instances:manage).
  Future<void> setBookMonitored(List<int> bookIds, bool monitored) async {
    await _dio.put('$_basePath/book/monitor',
        data: {'bookIds': bookIds, 'monitored': monitored});
  }

  /// Triggers an indexer search for every monitored book of an author.
  Future<void> searchAuthor(int authorId) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'AuthorSearch',
      'authorId': authorId,
    });
  }

  /// Triggers an automatic indexer search for the given books.
  Future<void> searchBook(List<int> bookIds) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'BookSearch',
      'bookIds': bookIds,
    });
  }

  /// Triggers a search for all monitored books that are missing a file.
  Future<void> searchMissing() async {
    await _dio.post('$_basePath/command', data: {'name': 'MissingBookSearch'});
  }

  Future<List<Map<String, dynamic>>> getQueue() async {
    final resp = await _dio.get('$_basePath/queue', queryParameters: {
      'includeAuthor': true,
      'includeBook': true,
      'pageSize': 50,
    });
    final records =
        (resp.data as Map<String, dynamic>)['records'] as List<dynamic>?;
    return records?.cast<Map<String, dynamic>>() ?? [];
  }

  /// Fetches the queue with full author + book details, typed.
  Future<List<ChaptarrQueueItem>> getQueueDetailed({
    int page = 1,
    int pageSize = 100,
  }) async {
    final resp = await _dio.get('$_basePath/queue', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'includeAuthor': true,
      'includeBook': true,
    });
    final records =
        (resp.data as Map<String, dynamic>)['records'] as List<dynamic>? ?? [];
    return records
        .map((r) => ChaptarrQueueItem.fromJson(r as Map<String, dynamic>))
        .toList();
  }

  /// Removes a queue item, optionally from the download client / blocklist.
  /// [changeCategory] hands the download to the post-import category instead of
  /// deleting it (e.g. for Unpackerr); [skipRedownload] suppresses the
  /// automatic re-grab on a blocklist removal.
  Future<void> deleteQueueItem(
    int id, {
    bool removeFromClient = true,
    bool blocklist = false,
    bool skipRedownload = false,
    bool changeCategory = false,
  }) async {
    await _dio.delete('$_basePath/queue/$id', queryParameters: {
      'removeFromClient': removeFromClient,
      'blocklist': blocklist,
      'skipRedownload': skipRedownload,
      'changeCategory': changeCategory,
    });
  }

  /// Fetches a page of history events, newest first.
  Future<ChaptarrHistoryPage> getHistory({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/history', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'date',
      'sortDirection': 'descending',
    });
    return ChaptarrHistoryPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// History for an author, newest first. Uses the non-paged /history/author
  /// endpoint.
  Future<List<ChaptarrHistoryRecord>> getAuthorHistory(int authorId) async {
    final resp = await _dio.get('$_basePath/history/author',
        queryParameters: {'authorId': authorId});
    final records = (resp.data as List<dynamic>)
        .map((r) => ChaptarrHistoryRecord.fromJson(r as Map<String, dynamic>))
        .toList();
    records.sort(
        (a, b) => (b.date ?? DateTime(0)).compareTo(a.date ?? DateTime(0)));
    return records;
  }

  /// History for a single book, newest first. Uses the paged /history endpoint
  /// filtered by bookId: the author-scoped /history/author requires an authorId
  /// and 404s ("Author with ID 0 does not exist") when called with only a
  /// bookId on this Chaptarr build, which left the book sheet stuck on
  /// "Loading…".
  Future<List<ChaptarrHistoryRecord>> getBookHistory(int bookId) async {
    final resp = await _dio.get('$_basePath/history', queryParameters: {
      'bookId': bookId,
      'pageSize': 50,
      'sortKey': 'date',
      'sortDirection': 'descending',
    });
    var data = resp.data;
    // Decode a raw String body first (some Chaptarr endpoints omit the JSON
    // content-type), mirroring _jsonList/_releaseList.
    if (data is String && data.trim().isNotEmpty) data = jsonDecode(data);
    final raw = data is Map ? data['records'] : data;
    final records = (raw is List ? raw : const [])
        .whereType<Map<String, dynamic>>()
        .map(ChaptarrHistoryRecord.fromJson)
        .toList();
    records.sort(
        (a, b) => (b.date ?? DateTime(0)).compareTo(a.date ?? DateTime(0)));
    return records;
  }

  /// Fetches a page of monitored books that are missing a file, newest release
  /// date first. Records include author context.
  Future<ChaptarrWantedPage> getWantedMissing({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/missing', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'releaseDate',
      'sortDirection': 'descending',
      'monitored': true,
      'includeAuthor': true,
    });
    return ChaptarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Fetches a page of monitored books whose file is below the quality profile
  /// cutoff, newest release date first. Records include author context.
  Future<ChaptarrWantedPage> getWantedCutoff({
    int page = 1,
    int pageSize = 50,
  }) async {
    final resp = await _dio.get('$_basePath/wanted/cutoff', queryParameters: {
      'page': page,
      'pageSize': pageSize,
      'sortKey': 'releaseDate',
      'sortDirection': 'descending',
      'monitored': true,
      'includeAuthor': true,
    });
    return ChaptarrWantedPage.fromJson(resp.data as Map<String, dynamic>);
  }

  /// Interactive release search for one book.
  /// Slow (10-60s): indexers are queried live.
  Future<List<ChaptarrRelease>> getReleases(int bookId) async {
    final resp = await _dio.get(
      '$_basePath/release',
      queryParameters: {'bookId': bookId},
      options: Options(receiveTimeout: const Duration(seconds: 120)),
    );
    return _releaseList(resp.data)
        .whereType<Map<String, dynamic>>()
        .map(ChaptarrRelease.fromJson)
        .toList();
  }

  /// Sends a release from interactive search to the download client.
  Future<void> grabRelease(String guid, int indexerId) async {
    await _dio.post(
      '$_basePath/release',
      data: {'guid': guid, 'indexerId': indexerId},
      options: Options(receiveTimeout: const Duration(seconds: 60)),
    );
  }

  // --- Import Doctor (admin; proxy requires instances:manage) ---

  /// Lists the importable files Chaptarr found for a finished download, with any
  /// rejection reasons. Backs the manual-import recovery flow.
  Future<List<ChaptarrManualImportCandidate>> getManualImportCandidates(
    String downloadId,
  ) async {
    final resp = await _dio.get(
      '$_basePath/manualimport',
      queryParameters: {
        'downloadId': downloadId,
        'filterExistingFiles': false,
      },
      options: Options(receiveTimeout: const Duration(seconds: 60)),
    );
    return (resp.data as List<dynamic>)
        .map((c) =>
            ChaptarrManualImportCandidate.fromJson(c as Map<String, dynamic>))
        .toList();
  }

  /// Imports the given candidate files. [importMode] must be lowercase
  /// (`move`/`copy`/`auto`); `copy` preserves seeding for torrents.
  Future<void> executeManualImport(
    List<Map<String, dynamic>> files, {
    String importMode = 'move',
  }) async {
    await _dio.post('$_basePath/command', data: {
      'name': 'ManualImport',
      'importMode': importMode,
      'files': files,
    });
  }

  /// Nudges Chaptarr to run its completed-download import pass now (clears items
  /// stuck "waiting to import").
  Future<void> processMonitoredDownloads() async {
    await _dio.post('$_basePath/command',
        data: {'name': 'ProcessMonitoredDownloads'});
  }

  /// Rescans an author's files on disk (retries imports blocked by a transient
  /// path/permissions problem).
  Future<void> rescanAuthor(int authorId) async {
    await _dio.post('$_basePath/command',
        data: {'name': 'RescanAuthor', 'authorId': authorId});
  }
}
