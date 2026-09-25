import 'package:dio/dio.dart';

enum LibrarySettingsKind { author, artist }

/// Raw resource updates preserve provider fields the app does not model.
class LibrarySettingsService {
  LibrarySettingsService({required this.dio, required this.instanceId,
    required this.kind, required this.id});
  final Dio dio;
  final String instanceId;
  final LibrarySettingsKind kind;
  final int id;
  String get base => '/api/instances/$instanceId/api/v1';

  Future<Map<String, dynamic>> read() async {
    final response = await dio.get('$base/${kind.name}/$id');
    final record = Map<String, dynamic>.from(response.data as Map);
    if (record['id'] != id) throw StateError('Library record changed; reopen its settings.');
    return record;
  }

  Future<List<Map<String, dynamic>>> options(String resource) async {
    final response = await dio.get('$base/$resource');
    return (response.data as List).map((row) =>
        Map<String, dynamic>.from(row as Map)).toList();
  }

  Future<void> update(Map<String, dynamic> changes) async {
    if (changes.isEmpty) return;
    final record = await read();
    record.addAll(changes);
    await dio.put('$base/${kind.name}/$id', data: record);
  }
}
