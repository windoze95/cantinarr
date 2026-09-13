import 'package:dio/dio.dart';

class RequestAllowance {
  final String mediaType;
  final String bookFormat;
  final int? count;
  final int windowDays;
  final String source;
  final int used;
  final int? remaining;
  final int requestedUnits;
  final DateTime? nextReplenishesAt;
  final DateTime? fullyReplenishesAt;

  const RequestAllowance({required this.mediaType, this.bookFormat = '',
    this.count, this.windowDays = 7, this.source = 'default', this.used = 0,
    this.remaining, this.requestedUnits = 0, this.nextReplenishesAt,
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
    remaining: json['remaining'] as int?, requestedUnits: json['requested_units'] as int? ?? 0,
    nextReplenishesAt: DateTime.tryParse(json['next_replenishes_at'] as String? ?? '')?.toLocal(),
    fullyReplenishesAt: DateTime.tryParse(json['fully_replenishes_at'] as String? ?? '')?.toLocal(),
  );
}

class RequestQuotaView {
  final List<RequestAllowance> allowances;
  final bool exempt;
  final bool fits;
  final bool reduceSelection;
  final DateTime? earliestFitsAt;
  final List<int> seasons;
  final DateTime? nextChangeAt;
  final DateTime? asOf;

  const RequestQuotaView({required this.allowances, this.exempt = false,
    this.fits = true, this.reduceSelection = false, this.earliestFitsAt,
    this.seasons = const [], this.nextChangeAt, this.asOf});

  factory RequestQuotaView.fromJson(Map<String, dynamic> json) => RequestQuotaView(
    allowances: ((json['allowances'] as List?) ?? []).map((item) =>
        RequestAllowance.fromJson(Map<String, dynamic>.from(item as Map))).toList(),
    exempt: json['exempt'] == true, fits: json['fits'] != false,
    reduceSelection: json['reduce_selection'] == true,
    earliestFitsAt: DateTime.tryParse(json['earliest_fits_at'] as String? ?? '')?.toLocal(),
    seasons: ((json['seasons'] as List?) ?? []).cast<int>(),
    nextChangeAt: DateTime.tryParse(json['next_change_at'] as String? ?? '')?.toLocal(),
    asOf: DateTime.tryParse(json['as_of'] as String? ?? '')?.toLocal(),
  );

  DateTime? get nextChange {
    if (nextChangeAt != null) return nextChangeAt;
    final times = allowances.map((a) => a.nextReplenishesAt).whereType<DateTime>().toList()..sort();
    return times.isEmpty ? null : times.first;
  }
}

String? requestQuotaError(Object error) {
  if (error is! DioException) return null;
  final data = error.response?.data;
  if (data is! Map || data['code'] != 'request_quota_exceeded') return null;
  return data['error'] as String? ?? 'This selection exceeds your request allowance.';
}

class RequestQuotaService {
  final Dio dio;
  RequestQuotaService(this.dio);

  Future<RequestQuotaView> read(String path) async => RequestQuotaView.fromJson(
      Map<String, dynamic>.from((await dio.get(path)).data as Map));

  Future<RequestQuotaView> preview(Map<String, dynamic> selection) async =>
      RequestQuotaView.fromJson(Map<String, dynamic>.from(
          (await dio.post('/api/requests/preview', data: selection)).data as Map));

  Future<void> save(String path, Map<String, dynamic> rule) async {
    await dio.put(path, data: {'allowances': [rule]});
  }

  Future<void> reset(int userId, List<RequestAllowance> selected) async {
    await dio.post('/api/admin/users/$userId/request-quotas/reset',
        data: {'allowances': selected.map((a) => a.keyJson).toList()});
  }
}
