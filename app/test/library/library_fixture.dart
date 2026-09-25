// Deterministic library payloads shared by widget tests and the browser preview.
import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/models/backend_connection.dart';
import 'package:cantinarr/core/models/user_profile.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:dio/dio.dart';

const libraryModules = ['radarr', 'sonarr', 'chaptarr', 'lidarr'];

class LibraryFixtureAuth extends AuthNotifier {
  LibraryFixtureAuth({this.serverUrl = 'http://localhost'});
  final String serverUrl;

  @override
  Future<AuthState> build() async => AuthState(
    user: const UserProfile(id: 1, username: 'library-fixture', role: 'admin'),
    connection: BackendConnection(
      serverUrl: serverUrl,
      accessToken: 'library-fixture', refreshToken: 'library-fixture',
      services: const AvailableServices(
          radarr: true, sonarr: true, chaptarr: true, lidarr: true),
      instances: [for (final module in libraryModules) ...[
        ServiceInstance(id: '$module-one', serviceType: module,
            name: 'Main library', isDefault: true),
        ServiceInstance(id: '$module-two', serviceType: module, name: 'Second library'),
      ]],
    ),
  );
}

Map<String, dynamic> libraryRecord(int id, {String? name, bool monitored = true}) => {
  'id': id, 'title': name ?? 'Example $id', 'year': 2020,
  'authorName': name ?? 'Example $id', 'artistName': name ?? 'Example $id',
  'monitored': monitored, 'hasFile': id.isEven, 'status': 'continuing',
  'statistics': {'episodeCount': 10, 'episodeFileCount': 5,
    'bookCount': 10, 'bookFileCount': 5, 'albumCount': 3,
    'trackCount': 10, 'trackFileCount': 5},
};

class LibraryFixtureAdapter implements HttpClientAdapter {
  LibraryFixtureAdapter({this.managementFixtures = false});
  final bool managementFixtures;
  List<Map<String, dynamic>> records = [
    libraryRecord(1, name: 'Example one'),
    libraryRecord(2, name: 'Example two', monitored: false),
    libraryRecord(3, name: 'Another title'),
  ];
  final requests = <String>[];
  int status = 200;
  Future<void>? waitFor;

  @override
  Future<ResponseBody> fetch(RequestOptions options,
      Stream<Uint8List>? requestStream, Future<void>? cancelFuture) async {
    requests.add('${options.method} ${options.path}');
    if (waitFor != null) await waitFor;
    final path = options.path;
    Object body = <String, dynamic>{};
    if (RegExp(r'/(movie|series|author|artist)$').hasMatch(path)) {
      body = records;
    }
    if (RegExp(r'/(movie|series|author|artist)/\d+$').hasMatch(path)) {
      body = records.firstWhere((r) => '${r['id']}' == path.split('/').last);
      if (managementFixtures) {
        body = {
        ...body as Map<String, dynamic>,
        'qualityProfileId': 1, 'metadataProfileId': 1, 'tags': [1],
        'ebookMonitored': true, 'audiobookMonitored': false,
        'ebookMonitorNewItems': 'none', 'audiobookMonitorNewItems': 'all',
        'ebookQualityProfileId': 1, 'audiobookQualityProfileId': 2,
        'ebookMetadataProfileId': 1, 'audiobookMetadataProfileId': 2,
        'ebookTags': [1], 'audiobookTags': [], 'monitorNewItems': 'all',
        };
      }
    }
    if (path.endsWith('/queue') || path.contains('/history')) {
      body = {'records': []};
    }
    if (path.endsWith('/book') || path.endsWith('/album') ||
        path.endsWith('/qualityprofile') || path.endsWith('/tag')) {
      body = [];
    }
    if (managementFixtures) {
      if (path.endsWith('/qualityprofile') || path.endsWith('/metadataprofile')) {
        body = [
          {'id': 1, 'name': 'Standard', 'profileType': 'ebook'},
          {'id': 2, 'name': 'Audiobooks', 'profileType': 'audiobook'},
        ];
      }
      if (path.endsWith('/tag')) body = [{'id': 1, 'label': 'Favorites'}];
    }
    return ResponseBody.fromString(jsonEncode(body), status,
        headers: {'content-type': ['application/json']});
  }

  @override
  void close({bool force = false}) {}
}
