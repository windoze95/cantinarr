import 'package:dio/dio.dart';

import '../../request/data/request_service.dart';

class RequesterTagReceipt {
  final String username;
  final String format;
  final RequesterTagStatus tagging;

  RequesterTagReceipt.fromJson(Map<String, dynamic> json)
      : username = (json['username'] as String? ?? '').trim(),
        format = json['format'] as String? ?? '',
        tagging = RequesterTagStatus.fromJson(json);

  String get label => '${username.isEmpty ? 'Unknown requester' : username} • '
      '${format == 'ebook' ? 'eBook' : format == 'audiobook' ? 'Audiobook' : 'Request'}';
}

class RequesterTagStatus {
  final String status;
  final String message;
  final bool canRetry;
  final String tagLabel;
  final DateTime? appliedAt;
  final List<RequesterTagReceipt> recipients;

  RequesterTagStatus.fromJson(Map<String, dynamic> json)
      : status = json['status'] as String? ?? '',
        message = json['message'] as String? ?? '',
        canRetry = json['can_retry'] as bool? ?? false,
        tagLabel = json['tag_label'] as String? ?? '',
        appliedAt = DateTime.tryParse(json['applied_at'] as String? ?? '')?.toLocal(),
        recipients = (json['recipients'] as List? ?? [])
            .map((r) => RequesterTagReceipt.fromJson(r as Map<String, dynamic>))
            .toList();

  String get label => switch (status) {
    'partial' => 'Tag results differ by requester or format',
    'waiting' => 'Tag waiting for request',
    'pending' => 'Tag pending',
    'retrying' => 'Tag retry scheduled',
    'applied' => 'Tag applied',
    'failed' => 'Tag failed',
    'cancelled' => 'Tag cancelled',
    _ => 'Tag status unavailable',
  };
}

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
  final RequesterTagStatus? requesterTagging;

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
            .toList(),
        requesterTagging = json['requester_tagging'] is Map<String, dynamic>
            ? RequesterTagStatus.fromJson(json['requester_tagging'] as Map<String, dynamic>)
            : null;

  RequestHistoryItem.withTagging(RequestHistoryItem item, RequesterTagStatus tagging)
      : id = item.id,
        tmdbId = item.tmdbId,
        foreignId = item.foreignId,
        catalogProvider = item.catalogProvider,
        mediaType = item.mediaType,
        title = item.title,
        posterPath = item.posterPath,
        instanceId = item.instanceId,
        instanceName = item.instanceName,
        seasonScope = item.seasonScope,
        bookFormat = item.bookFormat,
        decision = item.decision,
        decidedBy = item.decidedBy,
        denyReason = item.denyReason,
        requestedAt = item.requestedAt,
        decidedAt = item.decidedAt,
        requesters = item.requesters,
        requesterTagging = tagging;

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

  Future<RequesterTagStatus> retryTag(int requestId) async {
    final response = await _dio.post<Map<String, dynamic>>(
      '/api/admin/requests/$requestId/tags/retry');
    return RequesterTagStatus.fromJson(response.data!);
  }

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
