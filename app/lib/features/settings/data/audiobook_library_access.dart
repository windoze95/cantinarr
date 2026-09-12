import 'package:dio/dio.dart';

class AudiobookLibraryPolicy {
  final String mode;
  final List<String> libraryIds;
  final bool syncPending;
  final bool managesLibraries;
  const AudiobookLibraryPolicy(
      {this.mode = 'default',
      this.libraryIds = const [],
      this.syncPending = false,
      this.managesLibraries = false});
  factory AudiobookLibraryPolicy.fromJson(Map<String, dynamic> json) =>
      AudiobookLibraryPolicy(
          mode: json['mode'] as String,
          libraryIds: (json['library_ids'] as List).cast<String>(),
          syncPending: json['sync_pending'] == true,
          managesLibraries: json['manages_libraries'] == true);
  Map<String, dynamic> toJson() => {'mode': mode, 'library_ids': libraryIds};
}

class AudiobookLibraryAccess {
  final Set<int> userIds;
  final List<String> defaultLibraryIds;
  final Map<int, AudiobookLibraryPolicy> policies;
  const AudiobookLibraryAccess(
      {required this.userIds,
      required this.defaultLibraryIds,
      required this.policies});
  factory AudiobookLibraryAccess.fromJson(Map<String, dynamic> json) =>
      AudiobookLibraryAccess(
          userIds: (json['user_ids'] as List).cast<int>().toSet(),
          defaultLibraryIds:
              (json['default_library_ids'] as List).cast<String>(),
          policies: (json['policies'] as Map<String, dynamic>).map(
              (id, value) => MapEntry(
                  int.parse(id),
                  AudiobookLibraryPolicy.fromJson(
                      value as Map<String, dynamic>))));
  bool get syncPending => policies.values.any((p) => p.syncPending);
}

class AudiobookLibraryAccessService {
  final Dio dio;
  AudiobookLibraryAccessService(this.dio);
  Future<AudiobookLibraryAccess> get(String instanceId) async {
    final response =
        await dio.get('/api/admin/instances/$instanceId/media-access');
    return AudiobookLibraryAccess.fromJson(
        response.data as Map<String, dynamic>);
  }

  Future<AudiobookLibraryAccess> save(String instanceId,
      {required Set<int> userIds,
      required Set<String> defaultLibraryIds,
      required Map<int, AudiobookLibraryPolicy> policies}) async {
    final response =
        await dio.put('/api/admin/instances/$instanceId/media-access', data: {
      'default_library_ids': defaultLibraryIds.toList()..sort(),
      'user_ids': userIds.toList()..sort(),
      'policies': policies.map((id, p) => MapEntry('$id', p.toJson())),
    });
    return AudiobookLibraryAccess.fromJson(
        response.data as Map<String, dynamic>);
  }
}
