import 'package:dio/dio.dart';

class RequestAllowance {
  final String mediaType;
  final String bookFormat;
  final int? count;
  final int windowDays;
  final String source;
  final int used;
  final int? remaining;
  final DateTime? nextReplenishesAt;
  final DateTime? fullyReplenishesAt;

  const RequestAllowance({required this.mediaType, this.bookFormat = '',
    this.count, this.windowDays = 7, this.source = 'default', this.used = 0,
    this.remaining, this.nextReplenishesAt,
    this.fullyReplenishesAt});

  String get id => '$mediaType:$bookFormat';
  String get label => switch (mediaType) {
    'movie' => 'Movies', 'tv' => 'TV seasons', 'music' => 'Albums',
    'book' => bookFormat == 'ebook' ? 'eBooks' : 'Audiobooks',
    _ => 'Requests',
  };
  String get ruleLabel => count == null ? 'Unlimited' :
      '$count per $windowDays ${windowDays == 1 ? 'day' : 'days'}';
  Map<String, dynamic> get keyJson => {'media_type': mediaType,
    if (bookFormat.isNotEmpty) 'book_format': bookFormat};

  factory RequestAllowance.fromJson(Map<String, dynamic> json) => RequestAllowance(
    mediaType: json['media_type'] as String,
    bookFormat: json['book_format'] as String? ?? '',
    count: json['count'] as int?, windowDays: json['window_days'] as int? ?? 7,
    source: json['source'] as String? ?? 'default', used: json['used'] as int? ?? 0,
    remaining: json['remaining'] as int?,
    nextReplenishesAt: DateTime.tryParse(json['next_replenishes_at'] as String? ?? '')?.toLocal(),
    fullyReplenishesAt: DateTime.tryParse(json['fully_replenishes_at'] as String? ?? '')?.toLocal(),
  );
}

class RequestQuotaView {
  final List<RequestAllowance> allowances;
  final bool exempt;
  final DateTime? nextChangeAt;
  final DateTime? asOf;

  const RequestQuotaView({required this.allowances, this.exempt = false,
    this.nextChangeAt, this.asOf});

  factory RequestQuotaView.fromJson(Map<String, dynamic> json) => RequestQuotaView(
    allowances: ((json['allowances'] as List?) ?? []).map((item) =>
        RequestAllowance.fromJson(Map<String, dynamic>.from(item as Map))).toList(),
    exempt: json['exempt'] == true,
    nextChangeAt: DateTime.tryParse(json['next_change_at'] as String? ?? '')?.toLocal(),
    asOf: DateTime.tryParse(json['as_of'] as String? ?? '')?.toLocal(),
  );

  DateTime? get nextChange {
    if (nextChangeAt != null) return nextChangeAt;
    final times = allowances.map((a) => a.nextReplenishesAt).whereType<DateTime>().toList()..sort();
    return times.isEmpty ? null : times.first;
  }
}

const requestQuotaExceededMessage = 'Request limit reached.';

String? requestQuotaError(Object error) {
  if (error is! DioException) return null;
  final data = error.response?.data;
  if (data is! Map || data['code'] != 'request_quota_exceeded') return null;
  return requestQuotaExceededMessage;
}

class RequestQuotaService {
  final Dio dio;
  RequestQuotaService(this.dio);

  Future<RequestQuotaView> read(String path) async => RequestQuotaView.fromJson(
      Map<String, dynamic>.from((await dio.get(path)).data as Map));

  Future<void> save(String path, Map<String, dynamic> rule) async {
    await dio.put(path, data: {'allowances': [rule]});
  }

  Future<void> reset(int userId, List<RequestAllowance> selected) async {
    await dio.post('/api/admin/users/$userId/request-quotas/reset',
        data: {'allowances': selected.map((a) => a.keyJson).toList()});
  }
}
