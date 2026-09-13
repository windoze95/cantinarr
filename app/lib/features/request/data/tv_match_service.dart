import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../auth/logic/auth_provider.dart';

final tvMatchesAllowedProvider = Provider<bool>((ref) {
  final auth = ref.watch(authProvider).valueOrNull;
  return auth?.user?.isAdmin == true && auth?.connection?.tvMatchCorrections == true;
});

final tvMatchServiceProvider = Provider<TVMatchService>((ref) =>
    TVMatchService(ref.watch(backendClientProvider)));

class TVMatch {
  final int tmdbId;
  final int tvdbId;
  final int seriesId;
  final String title;
  final String targetTitle;
  final String provenance;
  final String revision;
  final String state;
  final String? message;
  final Map<int, int> seasonMap;
  const TVMatch({required this.tmdbId, required this.tvdbId, required this.title,
    required this.targetTitle, required this.provenance, required this.revision,
    required this.state, required this.seasonMap, this.seriesId = 0, this.message});
  bool get isResolved => state == 'resolved';
  String get originLabel => provenance == 'bundled' ? 'Bundled correction'
      : provenance == 'custom' ? 'Local correction' : 'Automatic match';
  factory TVMatch.fromJson(Map<String, dynamic> json) => TVMatch(
    tmdbId: (json['tmdb_id'] as num?)?.toInt() ?? 0,
    tvdbId: (json['tvdb_id'] as num?)?.toInt() ?? 0,
    seriesId: (json['series_id'] as num?)?.toInt() ?? 0,
    title: json['title'] as String? ?? '', targetTitle: json['target_title'] as String? ?? '',
    provenance: json['provenance'] as String? ?? 'default',
    revision: json['revision'] as String? ?? '', state: json['state'] as String? ?? 'unresolved',
    message: json['message'] as String?,
    seasonMap: {for (final e in ((json['season_map'] as Map?) ?? {}).entries)
      if (int.tryParse(e.key.toString()) != null && e.value is num)
        int.parse(e.key.toString()): (e.value as num).toInt()},
  );
}

String tvSeasonMappingLabel(Map<int, int> seasons) {
  final keys = seasons.keys.toList()..sort();
  return keys.map((n) => 'Season $n → Sonarr season ${seasons[n]}').join('\n');
}

class TVMatchCandidate {
  final int tvdbId;
  final String title;
  final int year;
  final List<int> seasons;
  const TVMatchCandidate({required this.tvdbId, required this.title, required this.year, required this.seasons});
  factory TVMatchCandidate.fromJson(Map<String, dynamic> json) => TVMatchCandidate(
    tvdbId: (json['tvdbId'] as num).toInt(), title: json['title'] as String? ?? '',
    year: (json['year'] as num?)?.toInt() ?? 0,
    seasons: [for (final s in (json['seasons'] as List?) ?? [])
      if ((s['seasonNumber'] as num).toInt() > 0) (s['seasonNumber'] as num).toInt()],
  );
}

class TVMatchView {
  final TVMatch match;
  final Map<int, String> sourceSeasons;
  final List<int> targetSeasons;
  final String? instanceId;
  const TVMatchView({required this.match, required this.sourceSeasons, required this.targetSeasons, this.instanceId});
  factory TVMatchView.fromJson(Map<String, dynamic> json) => TVMatchView(
    match: TVMatch.fromJson(json['match'] as Map<String, dynamic>),
    sourceSeasons: {for (final s in (json['source_seasons'] as List?) ?? [])
      (s['season_number'] as num).toInt(): s['name'] as String? ?? 'Season ${s['season_number']}'},
    targetSeasons: [for (final s in (json['target_seasons'] as List?) ?? [])
      if ((s['seasonNumber'] as num).toInt() > 0) (s['seasonNumber'] as num).toInt()],
    instanceId: json['instance_id'] as String?,
  );
}

class TVRepairPreview {
  final int requestId;
  final String title;
  final String status;
  final String instanceName;
  final String message;
  final String revision;
  final bool canRepair;
  final bool requiresApproval;
  final bool recordedTargetKnown;
  final int recordedTvdbId;
  final int? repairRequestId;
  final Map<String, dynamic>? recordedTarget;
  final Map<String, dynamic>? intendedTarget;
  TVRepairPreview.fromJson(Map<String, dynamic> json)
      : requestId = (json['request_id'] as num).toInt(), title = json['title'] as String? ?? '',
        status = json['status'] as String? ?? '', instanceName = json['instance_name'] as String? ?? '',
        message = json['message'] as String? ?? '', revision = json['revision'] as String? ?? '',
        canRepair = json['can_repair'] == true, requiresApproval = json['requires_approval'] == true,
        recordedTargetKnown = json['recorded_target_known'] == true,
        recordedTvdbId = (json['recorded_tvdb_id'] as num?)?.toInt() ?? 0,
        repairRequestId = (json['repair_request_id'] as num?)?.toInt(),
        recordedTarget = json['recorded_target'] as Map<String, dynamic>?,
        intendedTarget = json['intended_target'] as Map<String, dynamic>?;
  static String describeTarget(Map<String, dynamic>? target) {
    if (target == null) return 'Not verified';
    final match = TVMatch.fromJson(target['match'] as Map<String, dynamic>);
    final scope = (target['target_seasons'] as List?)?.join(', ') ?? '';
    return '${match.targetTitle} · TVDB ${match.tvdbId}\n'
        '${target['pilot'] == true ? 'Episode 1 of season' : 'Seasons'} $scope';
  }
}

String tvMatchError(Object error) {
  if (error is DioException && error.response?.data is Map) {
    final message = (error.response!.data as Map)['error'];
    if (message is String) return message;
  }
  return 'Couldn’t load TV matching. Check the connection and retry.';
}

class TVMatchService {
  final Dio dio;
  TVMatchService(this.dio);
  Future<List<TVMatch>> list() async => ((await dio.get('/api/admin/tv-matches')).data as List)
      .map((e) => TVMatch.fromJson(e as Map<String, dynamic>)).toList();
  Future<TVMatchView> read(int id, String? instanceId) async => TVMatchView.fromJson(
      (await dio.get('/api/admin/tv-matches/$id', queryParameters: {if (instanceId != null) 'instance_id': instanceId})).data as Map<String, dynamic>);
  Future<List<TVMatchCandidate>> search(String query, String? instanceId) async =>
      ((await dio.get('/api/admin/tv-matches/candidates', queryParameters: {'q': query, if (instanceId != null) 'instance_id': instanceId})).data as List)
          .map((e) => TVMatchCandidate.fromJson(e as Map<String, dynamic>)).toList();
  Future<TVMatchView> save(int id, String revision, String mode, String? instanceId,
      {int? tvdbId, Map<int, int> seasons = const {}}) async => TVMatchView.fromJson(
      (await dio.put('/api/admin/tv-matches/$id', data: {'revision': revision, 'mode': mode,
        if (instanceId != null) 'instance_id': instanceId, if (tvdbId != null) 'tvdb_id': tvdbId,
        'season_map': {for (final e in seasons.entries) '${e.key}': e.value}})).data as Map<String, dynamic>);
  Future<List<TVRepairPreview>> repairs(int id) async =>
      ((await dio.get('/api/admin/tv-matches/$id/repairs')).data as List)
          .map((e) => TVRepairPreview.fromJson(e as Map<String, dynamic>)).toList();
  Future<void> repair(TVRepairPreview preview) async {
    await dio.post('/api/admin/requests/${preview.requestId}/repair-tv-match', data: {'revision': preview.revision});
  }
}
