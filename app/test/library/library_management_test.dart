import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/core/network/library_settings_service.dart';
import 'package:cantinarr/core/widgets/library_settings_screen.dart';
import 'package:cantinarr/features/auth/logic/auth_provider.dart';
import 'package:cantinarr/features/chaptarr/data/chaptarr_api_service.dart';
import 'package:cantinarr/features/chaptarr/ui/chaptarr_home_screen.dart';
import 'package:cantinarr/features/lidarr/data/lidarr_api_service.dart';
import 'package:cantinarr/features/lidarr/ui/lidarr_home_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'library_fixture.dart';

class _Adapter implements HttpClientAdapter {
  Map<String, dynamic> record = {
    ...libraryRecord(7, name: 'Example'),
    'qualityProfileId': 1, 'metadataProfileId': 1, 'tags': [4],
    'ebookMonitored': true, 'audiobookMonitored': false,
    'ebookMonitorNewItems': 'none', 'audiobookMonitorNewItems': 'all',
    'ebookQualityProfileId': 1, 'audiobookQualityProfileId': 2,
    'ebookMetadataProfileId': 1, 'audiobookMetadataProfileId': 2,
    'ebookTags': [4], 'audiobookTags': [5],
    'path': '/library/Example', 'unknownFutureField': {'keep': true},
  };
  final requests = <RequestOptions>[];
  bool failWrite = false;
  bool failRead = false;
  bool failTags = false;

  @override
  Future<ResponseBody> fetch(RequestOptions options, Stream<Uint8List>? stream,
      Future<void>? cancelFuture) async {
    requests.add(options);
    final path = options.path;
    Object body = <String, dynamic>{};
    var status = 200;
    if (RegExp(r'/(author|artist)$').hasMatch(path)) body = [record];
    if (RegExp(r'/(author|artist)/7$').hasMatch(path)) {
      body = record;
      if (options.method == 'GET' && failRead) status = 503;
    }
    if (path.endsWith('/qualityprofile') || path.endsWith('/metadataprofile')) {
      body = [
        {'id': 1, 'name': 'eBook profile', 'profileType': 2},
        {'id': 2, 'name': 'Audio profile', 'profileType': 'audiobook'},
        {'id': 3, 'name': 'Alternate eBook', 'profileType': 'ebook'},
      ];
    }
    if (path.endsWith('/tag')) {
      body = [{'id': 4, 'label': 'Books'}, {'id': 5, 'label': 'Audio'}];
      if (failTags) status = 503;
    }
    if (path.endsWith('/book') || path.endsWith('/album')) {
      body = [
        {'id': 21, 'title': 'Exact title', 'authorId': 7, 'artistId': 7,
          'mediaType': 'ebook'},
        {'id': 22, 'title': 'Exact title', 'authorId': 7, 'artistId': 7,
          'mediaType': 'audiobook'},
        {'id': 99, 'title': 'Different parent', 'authorId': 999, 'artistId': 999},
      ];
    }
    if (path.endsWith('/release')) body = [];
    if (options.method != 'GET' && failWrite) status = 503;
    return ResponseBody.fromString(jsonEncode(body), status,
        headers: {'content-type': ['application/json']});
  }
  @override
  void close({bool force = false}) {}
}

void main() {
  late _Adapter adapter;
  late Dio dio;
  setUp(() {
    SharedPreferences.setMockInitialValues({});
    adapter = _Adapter();
    dio = Dio(BaseOptions(baseUrl: 'http://localhost'))..httpClientAdapter = adapter;
  });

  LibrarySettingsService settings(LibrarySettingsKind kind) => LibrarySettingsService(
    dio: dio, instanceId: 'selected', kind: kind, id: 7);

  for (final kind in LibrarySettingsKind.values) {
    test('${kind.name} update re-reads and preserves unknown and unrelated settings', () async {
      final service = settings(kind);
      await service.read();
      adapter.record['path'] = '/changed/by/another/admin';
      await service.update({kind == LibrarySettingsKind.author ? 'ebookMonitored' : 'monitored': false});
      final put = adapter.requests.last;
      expect(put.method, 'PUT');
      expect(put.path, '/api/instances/selected/api/v1/${kind.name}/7');
      final body = put.data as Map;
      expect(body['path'], '/changed/by/another/admin');
      expect(body['audiobookMonitored'], false);
      expect(body['ebookMonitorNewItems'], 'none');
      expect(body['audiobookTags'], [5]);
      expect(body['unknownFutureField'], {'keep': true});
    });

    test('${kind.name} unreadable or different record never writes', () async {
      adapter.failRead = true;
      await expectLater(settings(kind).update({'monitored': false}), throwsA(isA<DioException>()));
      adapter.failRead = false;
      adapter.record['id'] = 88;
      await expectLater(settings(kind).update({'monitored': false}), throwsStateError);
      expect(adapter.requests.where((r) => r.method == 'PUT'), isEmpty);
    });
  }

  test('rescans use singleton provider arrays, never the whole library', () async {
    await ChaptarrApiService(backendDio: dio, instanceId: 'books').rescanAuthor(7);
    await LidarrApiService(backendDio: dio, instanceId: 'music').rescanArtist(8);
    expect(adapter.requests[0].path, '/api/instances/books/api/v1/command');
    expect(adapter.requests[0].data, {'name': 'RescanFolders', 'authorIds': [7]});
    expect(adapter.requests[1].path, '/api/instances/music/api/v1/command');
    expect(adapter.requests[1].data, {'name': 'RescanFolders', 'artistIds': [8]});
  });

  Future<void> editor(WidgetTester tester, {bool monitoringOnly = false}) async {
    await tester.pumpWidget(MaterialApp(home: LibrarySettingsScreen(
      service: settings(LibrarySettingsKind.author), title: 'Example', monitoringOnly: monitoringOnly)));
    await tester.pumpAndSettle();
  }

  testWidgets('author monitoring changes one format, preserving the other and new-title choices', (tester) async {
    await editor(tester, monitoringOnly: true);
    await tester.tap(find.byKey(const ValueKey('ebookMonitored')));
    await tester.pump();
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    final put = adapter.requests.singleWhere((r) => r.method == 'PUT');
    expect(put.data['ebookMonitored'], false);
    expect(put.data['audiobookMonitored'], false);
    expect(put.data['ebookMonitorNewItems'], 'none');
    expect(put.data['audiobookMonitorNewItems'], 'all');
  });

  testWidgets('profiles stay format-specific and a failed save retains edits for retry', (tester) async {
    adapter.failTags = true;
    await editor(tester);
    await tester.tap(find.byKey(const ValueKey('ebookQualityProfileId')));
    await tester.pumpAndSettle();
    expect(find.text('Audio profile'), findsNothing);
    await tester.tap(find.text('Alternate eBook'));
    await tester.pumpAndSettle();
    adapter.failWrite = true;
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    expect(find.textContaining('Could not save settings:'), findsOneWidget);
    expect(find.text('Alternate eBook'), findsOneWidget);
    adapter.failWrite = false;
    adapter.record['audiobookQualityProfileId'] = 45;
    await tester.tap(find.text('Save'));
    await tester.pumpAndSettle();
    final put = adapter.requests.last;
    expect(put.data['ebookQualityProfileId'], 3);
    expect(put.data['audiobookQualityProfileId'], 45);
    expect(put.data['ebookTags'], [4]);
  });

  testWidgets('older author responses explain missing format gates and cannot write a global toggle', (tester) async {
    adapter.record.remove('ebookMonitored');
    adapter.record.remove('audiobookMonitored');
    await editor(tester, monitoringOnly: true);
    expect(find.byType(SwitchListTile), findsNothing);
    expect(find.text('Monitoring settings unavailable'), findsNWidgets(2));
    expect(tester.widget<TextButton>(find.widgetWithText(TextButton, 'Save')).onPressed, isNull);
  });

  for (final module in ['chaptarr', 'lidarr']) {
    Future<void> home(WidgetTester tester) async {
      await tester.pumpWidget(ProviderScope(overrides: [
        authProvider.overrideWith(LibraryFixtureAuth.new),
        backendClientProvider.overrideWithValue(dio),
      ], child: MaterialApp(home: Scaffold(body: module == 'chaptarr'
          ? const ChaptarrHomeScreen() : const LidarrHomeScreen()))));
      await tester.pumpAndSettle();
    }
    Future<void> choose(WidgetTester tester, String label) async {
      await tester.tap(find.byTooltip('Actions for Example'));
      await tester.pumpAndSettle();
      await tester.tap(find.text(label));
      await tester.pumpAndSettle();
    }

    testWidgets('$module home runs search, refresh, rescan and guarded removal on selected instance', (tester) async {
      await home(tester);
      for (final label in ['Automatic search', 'Refresh metadata', 'Rescan files']) {
        await choose(tester, label);
      }
      final commands = adapter.requests.where((r) => r.method == 'POST').toList();
      expect(commands, hasLength(3));
      expect(commands.every((r) => r.path == '/api/instances/$module-one/api/v1/command'), isTrue);
      expect(commands.last.data, {'name': 'RescanFolders', '${module == 'chaptarr' ? 'author' : 'artist'}Ids': [7]});
      await choose(tester, 'Remove…');
      expect(tester.widget<CheckboxListTile>(find.byType(CheckboxListTile)).value, isFalse);
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
      expect(adapter.requests.where((r) => r.method == 'DELETE'), isEmpty);
      await choose(tester, 'Remove…');
      await tester.tap(find.text('Remove'));
      await tester.pumpAndSettle();
      final deletion = adapter.requests.singleWhere((r) => r.method == 'DELETE');
      expect(deletion.path, '/api/instances/$module-one/api/v1/${module == 'chaptarr' ? 'author' : 'artist'}/7');
      expect(deletion.queryParameters['deleteFiles'], false);
    });

    testWidgets('$module interactive picker retains exact records and excludes another parent', (tester) async {
      await home(tester);
      await choose(tester, 'Interactive search');
      expect(find.text('Different parent'), findsNothing);
      if (module == 'chaptarr') {
        expect(find.text('Exact title · eBook'), findsOneWidget);
        await tester.tap(find.text('Exact title · Audiobook'));
      } else {
        await tester.tap(find.text('Exact title').last);
      }
      await tester.pumpAndSettle();
      final release = adapter.requests.singleWhere((r) => r.path.endsWith('/release'));
      expect(release.path, '/api/instances/$module-one/api/v1/release');
      expect(release.queryParameters[module == 'chaptarr' ? 'bookId' : 'albumId'], 22);
    });
  }
}
