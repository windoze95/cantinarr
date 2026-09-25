import 'package:dio/dio.dart';

import '../../request/data/request_service.dart';

class HistoryRequester {
  final int id;
  final String name;
  final String bookFormat;

  HistoryRequester.fromJson(Map<String, dynamic> json)
      : id = json['user_id'] as int? ?? 0,
        name = (json['username'] as String? ?? '').trim(),
        bookFormat = json['book_format'] as String? ?? '';

  String get label => name.isEmpty ? 'Unknown requester' : name;
}

class RequestHistoryItem {
  final int id;
  final int tmdbId;
  final String foreignId;
  final String catalogProvider;
  final String mediaType;
  final String title;
  final String posterPath;
  final String instanceId;
  final String instanceName;
  final String seasonScope;
  final String bookFormat;
  final String decision;
  final String decidedBy;
  final String denyReason;
  final DateTime? requestedAt;
  final DateTime? decidedAt;
  final List<HistoryRequester> requesters;

  RequestHistoryItem.fromJson(Map<String, dynamic> json)
      : id = json['id'] as int,
        tmdbId = json['tmdb_id'] as int? ?? 0,
        foreignId = json['foreign_id'] as String? ?? '',
        catalogProvider = json['catalog_provider'] as String? ?? '',
        mediaType = json['media_type'] as String? ?? '',
        title = json['title'] as String? ?? '',
        posterPath = json['poster_path'] as String? ?? '',
        instanceId = json['instance_id'] as String? ?? '',
        instanceName = json['instance_name'] as String? ?? '',
        seasonScope = json['season_scope'] as String? ?? '',
        bookFormat = json['book_format'] as String? ?? '',
        decision = json['decision'] as String? ?? 'unknown',
        decidedBy = (json['decided_by'] as String? ?? '').trim(),
        denyReason = json['deny_reason'] as String? ?? '',
        requestedAt = DateTime.tryParse(json['requested_at'] as String? ?? '')?.toLocal(),
        decidedAt = DateTime.tryParse(json['decided_at'] as String? ?? '')?.toLocal(),
        requesters = (json['requesters'] as List? ?? [])
            .map((u) => HistoryRequester.fromJson(u as Map<String, dynamic>))
            .toList();

  String get decisionLabel => historyDecisions[decision] ?? 'Unknown decision';
  String get mediaLabel => historyMediaTypes[mediaType] ?? 'Media';
  String get requesterLabel => requesters.isEmpty
      ? 'Unknown requester'
      : requesters.length == 1
          ? requesters.first.label
          : '${requesters.first.label} and ${requesters.length - 1} ${requesters.length == 2 ? 'other' : 'others'}';
  String get libraryLabel => instanceName.isEmpty ? 'Library not recorded or removed' : instanceName;
  String get scopeLabel => mediaType == 'tv' && seasonScope.isNotEmpty
      ? SeasonScope.describe(seasonScope)
      : mediaType == 'book'
          ? BookRequestFormat.tryFromValue(bookFormat)?.label ?? 'Format not recorded'
          : '';

  String? get detailRoute {
    String type;
    String identity;
    final query = <String, String>{};
    if (instanceId.isNotEmpty) query['instance_id'] = instanceId;
    if (mediaType == 'movie' || mediaType == 'tv') {
      if (tmdbId <= 0) return null;
      type = mediaType;
      identity = '$tmdbId';
    } else if (mediaType == 'book' || mediaType == 'music') {
      // An unscoped historical record cannot safely pick today's default.
      if (foreignId.isEmpty || instanceId.isEmpty) return null;
      if (mediaType == 'book' && catalogProvider == 'openlibrary') return null;
      type = mediaType == 'book' ? 'book' : 'album';
      identity = foreignId;
      query['title'] = title;
      if (mediaType == 'book') query['source'] = 'chaptarr';
    } else {
      return null;
    }
    final suffix = query.isEmpty ? '' : '?${Uri(queryParameters: query).query}';
    return '/detail/$type/${Uri.encodeComponent(identity)}$suffix';
  }
}

const historyMediaTypes = {
  'movie': 'Movie',
  'tv': 'TV',
  'book': 'Book',
  'music': 'Album',
};

const historyDecisions = {
  'pending': 'Needs approval',
  'approved': 'Approved',
  'denied': 'Denied',
  'cancelled': 'Cancelled',
  'unknown': 'Unknown decision',
};

class RequestHistoryPage {
  final List<RequestHistoryItem> requests;
  final List<HistoryRequester> requesters;
  final int? nextBefore;

  RequestHistoryPage.fromJson(Map<String, dynamic> json)
      : requests = (json['requests'] as List)
            .map((r) => RequestHistoryItem.fromJson(r as Map<String, dynamic>))
            .toList(),
        requesters = (json['requesters'] as List)
            .map((u) => HistoryRequester.fromJson(u as Map<String, dynamic>))
            .toList(),
        nextBefore = json['next_before'] as int?;
}

class RequestHistoryService {
  final Dio _dio;
  RequestHistoryService(this._dio);

  Future<RequestHistoryPage> list({
    String query = '',
    String mediaType = '',
    String decision = '',
    int? userId,
    int? before,
    CancelToken? cancelToken,
  }) async {
    final response = await _dio.get<Map<String, dynamic>>(
      '/api/admin/requests/history',
      queryParameters: {
        if (query.trim().isNotEmpty) 'q': query.trim(),
        if (mediaType.isNotEmpty) 'media_type': mediaType,
        if (decision.isNotEmpty) 'decision': decision,
        if (userId != null) 'user_id': userId,
        if (before != null) 'before': before,
        'limit': 50,
      },
      cancelToken: cancelToken,
    );
    return RequestHistoryPage.fromJson(response.data!);
  }
}
