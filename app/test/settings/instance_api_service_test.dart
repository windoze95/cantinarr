import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/settings/data/instance_api_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

class _RecordingAdapter implements HttpClientAdapter {
  _RecordingAdapter({
    this.mediaRootsStatus = 200,
    this.rootFolderBody,
    this.rootFolderContentType = 'application/json',
  });

  final int mediaRootsStatus;
  final String? rootFolderBody;
  final String rootFolderContentType;
  final List<({String method, String path, dynamic body})> requests = [];

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    dynamic body;
    if (requestStream != null) {
      final bytes = await requestStream.expand((chunk) => chunk).toList();
      if (bytes.isNotEmpty) body = jsonDecode(utf8.decode(bytes));
    }
    final path = options.uri.path;
    requests.add((method: options.method, path: path, body: body));

    if (path.endsWith('/rootfolder')) {
      return ResponseBody.fromString(
        rootFolderBody ??
            jsonEncode([
              {'id': 1, 'path': '/media-server/media/ebooks'},
              {'id': 2, 'path': '  '},
              {'id': 3},
            ]),
        200,
        headers: {
          'content-type': [rootFolderContentType],
        },
      );
    }

    if (path == '/api/instances/media-roots') {
      return ResponseBody.fromString(
        mediaRootsStatus == 200
            ? jsonEncode(['/media/movies', '/media/books'])
            : jsonEncode({'error': 'not found'}),
        mediaRootsStatus,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }

    final request = body as Map<String, dynamic>;
    final id = path == '/api/instances'
        ? '${request['service_type']}-new'
        : path.split('/').last;
    final serviceType = request['service_type'] as String? ?? 'radarr';
    return ResponseBody.fromString(
      jsonEncode({
        'id': id,
        'service_type': serviceType,
        'name': request['name'],
        'is_default': request['is_default'],
      }),
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}

InstanceApiService _service(_RecordingAdapter adapter) {
  final dio = Dio(BaseOptions(baseUrl: 'http://localhost'))
    ..httpClientAdapter = adapter;
  return InstanceApiService(backendDio: dio);
}

void main() {
  test('MediaPathMapping uses the instance API JSON shape', () {
    const mapping = MediaPathMapping(
      arrPath: '/ebooks',
      cantinarrPath: '/media/books',
    );

    expect(mapping.toJson(), {
      'arr_path': '/ebooks',
      'cantinarr_path': '/media/books',
    });
    final decoded = MediaPathMapping.fromJson(mapping.toJson());
    expect(decoded.arrPath, '/ebooks');
    expect(decoded.cantinarrPath, '/media/books');
  });

  test('listMediaRoots returns the admin-configured absolute roots', () async {
    final adapter = _RecordingAdapter();
    final roots = await _service(adapter).listMediaRoots();

    expect(roots, ['/media/movies', '/media/books']);
    expect(adapter.requests.single.method, 'GET');
    expect(adapter.requests.single.path, '/api/instances/media-roots');
  });

  test('listMediaRoots propagates an unsupported endpoint response', () async {
    final adapter = _RecordingAdapter(mediaRootsStatus: 404);

    await expectLater(
      _service(adapter).listMediaRoots(),
      throwsA(isA<DioException>()),
    );
  });

  test('listArrRootFolders speaks v1 to Chaptarr and keeps only real paths',
      () async {
    final adapter = _RecordingAdapter();
    final folders = await _service(adapter).listArrRootFolders(
      instanceId: 'chaptarr-a',
      serviceType: 'chaptarr',
    );

    expect(folders, ['/media-server/media/ebooks']);
    expect(adapter.requests.single.method, 'GET');
    expect(
      adapter.requests.single.path,
      '/api/instances/chaptarr-a/api/v1/rootfolder',
    );
  });

  test('listArrRootFolders speaks v3 to Radarr and Sonarr', () async {
    final adapter = _RecordingAdapter();
    final service = _service(adapter);
    await service.listArrRootFolders(
      instanceId: 'radarr-a',
      serviceType: 'radarr',
    );
    await service.listArrRootFolders(
      instanceId: 'sonarr-a',
      serviceType: 'sonarr',
    );

    expect(adapter.requests[0].path, '/api/instances/radarr-a/api/v3/rootfolder');
    expect(adapter.requests[1].path, '/api/instances/sonarr-a/api/v3/rootfolder');
  });

  test('listArrRootFolders decodes a body served without a JSON content type',
      () async {
    final adapter = _RecordingAdapter(
      rootFolderBody: jsonEncode([
        {'id': 7, 'path': '/books'},
      ]),
      rootFolderContentType: 'text/plain; charset=utf-8',
    );

    final folders = await _service(adapter).listArrRootFolders(
      instanceId: 'chaptarr-a',
      serviceType: 'chaptarr',
    );
    expect(folders, ['/books']);
  });

  test('listArrRootFolders yields nothing for an unexpected shape', () async {
    final adapter = _RecordingAdapter(
      rootFolderBody: jsonEncode({'error': 'shape'}),
    );

    final folders = await _service(adapter).listArrRootFolders(
      instanceId: 'radarr-a',
      serviceType: 'radarr',
    );
    expect(folders, isEmpty);
  });

  test('create conditionally includes ordered media path mappings', () async {
    final adapter = _RecordingAdapter();
    final service = _service(adapter);

    await service.createInstance(
      serviceType: 'chaptarr',
      name: 'Books',
      url: 'http://chaptarr:8787',
      apiKey: 'key',
      mediaPathMappings: const [
        MediaPathMapping(
          arrPath: '/ebooks',
          cantinarrPath: '/media/ebooks',
        ),
        MediaPathMapping(
          arrPath: '/audiobooks',
          cantinarrPath: '/media/audiobooks',
        ),
      ],
    );
    await service.createInstance(
      serviceType: 'radarr',
      name: 'Movies',
      url: 'http://radarr:7878',
      apiKey: 'key',
    );

    final mapped = adapter.requests[0].body as Map<String, dynamic>;
    expect(mapped['media_path_mappings'], [
      {
        'arr_path': '/ebooks',
        'cantinarr_path': '/media/ebooks',
      },
      {
        'arr_path': '/audiobooks',
        'cantinarr_path': '/media/audiobooks',
      },
    ]);
    final legacy = adapter.requests[1].body as Map<String, dynamic>;
    expect(legacy.containsKey('media_path_mappings'), isFalse);
  });

  test('update distinguishes preserved, cleared, and replaced mappings',
      () async {
    final adapter = _RecordingAdapter();
    final service = _service(adapter);

    await service.updateInstance(
      id: 'radarr-main',
      name: 'Movies',
      url: 'http://radarr:7878',
    );
    await service.updateInstance(
      id: 'radarr-main',
      name: 'Movies',
      url: 'http://radarr:7878',
      mediaPathMappings: const [],
    );
    await service.updateInstance(
      id: 'radarr-main',
      name: 'Movies',
      url: 'http://radarr:7878',
      mediaPathMappings: const [
        MediaPathMapping(
          arrPath: '/movies',
          cantinarrPath: '/media/movies',
        ),
      ],
    );

    final preserved = adapter.requests[0].body as Map<String, dynamic>;
    expect(preserved.containsKey('media_path_mappings'), isFalse);
    final cleared = adapter.requests[1].body as Map<String, dynamic>;
    expect(cleared['media_path_mappings'], isEmpty);
    final replaced = adapter.requests[2].body as Map<String, dynamic>;
    expect(replaced['media_path_mappings'], [
      {'arr_path': '/movies', 'cantinarr_path': '/media/movies'},
    ]);
  });
}
