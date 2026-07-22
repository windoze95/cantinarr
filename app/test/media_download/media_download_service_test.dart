import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/features/media_download/data/media_download_models.dart';
import 'package:cantinarr/features/media_download/data/media_download_service.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('creates a ticket and resolves its relative same-origin URL', () async {
    final adapter = _TicketAdapter(response: {
      'url': '/api/media-files/download/one-time-token',
      'filename': 'Movie.mkv',
      'size_bytes': 1234,
      'expires_at': '2026-07-22T18:00:00Z',
    });
    final service = MediaDownloadService(backendDio: _dio(adapter));

    final ticket = await service.createTicket(
      instanceId: 'radarr-main',
      fileId: 42,
    );

    expect(adapter.method, 'POST');
    expect(adapter.path, '/api/media-files/tickets');
    expect(adapter.data, {
      'instance_id': 'radarr-main',
      'file_id': 42,
    });
    expect(
      ticket.url,
      Uri.parse(
        'https://cantinarr.example/api/media-files/download/one-time-token',
      ),
    );
    expect(ticket.filename, 'Movie.mkv');
    expect(ticket.sizeBytes, 1234);
    expect(ticket.expiresAt, DateTime.utc(2026, 7, 22, 18));
  });

  test('accepts an absolute URL only when it has the backend origin', () async {
    final service = MediaDownloadService(
      backendDio: _dio(_TicketAdapter(response: {
        'url': 'https://cantinarr.example:443/download/token',
        'filename': 'Episode.mkv',
        'size_bytes': 20,
        'expires_at': '2026-07-22T18:00:00Z',
      })),
    );

    final ticket = await service.createTicket(
      instanceId: 'sonarr-main',
      fileId: 9,
    );

    expect(ticket.url.host, 'cantinarr.example');
    expect(ticket.url.path, '/download/token');
  });

  test('rejects a cross-origin ticket URL', () async {
    final service = MediaDownloadService(
      backendDio: _dio(_TicketAdapter(response: {
        'url': 'https://files.example/secret',
        'filename': 'Book.epub',
        'size_bytes': 20,
        'expires_at': '2026-07-22T18:00:00Z',
      })),
    );

    expect(
      () => service.createTicket(instanceId: 'books', fileId: 3),
      throwsA(
        isA<MediaDownloadException>().having(
          (error) => error.message,
          'message',
          'Could not prepare the download. Try again.',
        ),
      ),
    );
  });

  test('maps missing and expired files to a concise safe error', () async {
    final service = MediaDownloadService(
      backendDio: _dio(_TicketAdapter(response: const {}, statusCode: 410)),
    );

    expect(
      () => service.createTicket(instanceId: 'books', fileId: 3),
      throwsA(
        isA<MediaDownloadException>().having(
          (error) => error.message,
          'message',
          'This file is no longer available.',
        ),
      ),
    );
  });
}

Dio _dio(HttpClientAdapter adapter) => Dio(
      BaseOptions(baseUrl: 'https://cantinarr.example'),
    )..httpClientAdapter = adapter;

class _TicketAdapter implements HttpClientAdapter {
  final Map<String, dynamic> response;
  final int statusCode;
  String? method;
  String? path;
  dynamic data;

  _TicketAdapter({required this.response, this.statusCode = 200});

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? requestStream,
    Future<void>? cancelFuture,
  ) async {
    method = options.method;
    path = options.path;
    data = options.data;
    return ResponseBody.fromString(
      jsonEncode(response),
      statusCode,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
