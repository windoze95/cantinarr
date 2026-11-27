import 'dart:convert';
import 'dart:typed_data';

import 'package:cantinarr/core/network/backend_client.dart';
import 'package:cantinarr/features/radarr/ui/radarr_releases_screen.dart';
import 'package:cantinarr/features/sonarr/ui/sonarr_releases_screen.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  for (final isSonarr in [false, true]) {
    testWidgets(
        '${isSonarr ? 'Sonarr' : 'Radarr'} asks for a fresh search after a release reference expires',
        (
      tester,
    ) async {
      final adapter = _ReleaseAdapter();
      final requests = adapter.requests;
      final endpoint =
          '/api/instances/${isSonarr ? 'tv' : 'movies'}/api/v3/release';
      final dio = Dio(BaseOptions(baseUrl: 'http://fixture.invalid'))
        ..httpClientAdapter = adapter;
      await tester.pumpWidget(
        ProviderScope(
          overrides: [backendClientProvider.overrideWithValue(dio)],
          child: MaterialApp(
            home: Builder(
              builder: (context) => Scaffold(
                body: TextButton(
                  onPressed: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => isSonarr
                          ? const SonarrReleasesScreen(
                              instanceId: 'tv',
                              seriesId: 42,
                              seasonNumber: 3,
                              seriesTitle: 'Show',
                            )
                          : const RadarrReleasesScreen(
                              instanceId: 'movies',
                              movieId: 42,
                              movieTitle: 'Movie',
                            ),
                    ),
                  ),
                  child: const Text('Open interactive search'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('Open interactive search'));
      await tester.pumpAndSettle();
      expect(adapter.searches, 1);
      expect(requests.single.uri.path, endpoint);
      expect(requests.single.queryParameters,
          isSonarr ? {'seriesId': 42, 'seasonNumber': 3} : {'movieId': 42});
      await tester.tap(find.text('Fixture 1'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Grab'));
      await tester.pumpAndSettle();
      expect(
        find.text(
          'This release selection is no longer available. Search again and choose a release.',
        ),
        findsOneWidget,
      );
      expect(requests.last.data, {
        'guid': 'cantinarr:release:fixture-1',
        'indexerId': 7,
      });
      expect(requests.map((r) => r.method), ['GET', 'POST'],
          reason: 'An expired reference must not trigger a retry or search.');
      expect(find.text('Fixture 1'), findsOneWidget);
      await tester.tap(find.byTooltip('Search again'));
      await tester.pumpAndSettle();
      expect(requests.map((r) => r.method), ['GET', 'POST', 'GET']);
      expect(requests[2].queryParameters, requests[0].queryParameters);
      expect(find.text('Fixture 1'), findsNothing);
      await tester.tap(find.text('Fixture 2'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Grab'));
      await tester.pumpAndSettle();
      expect(requests.map((r) => r.method), ['GET', 'POST', 'GET', 'POST']);
      expect(requests.map((r) => r.uri.path), everyElement(endpoint));
      expect(requests.last.data, {
        'guid': 'cantinarr:release:fixture-2',
        'indexerId': 7,
      });
      expect(find.text('Open interactive search'), findsOneWidget);
    });
  }
}

class _ReleaseAdapter implements HttpClientAdapter {
  final requests = <RequestOptions>[];
  int searches = 0;
  int grabs = 0;

  @override
  Future<ResponseBody> fetch(
    RequestOptions options,
    Stream<Uint8List>? stream,
    Future<void>? cancelFuture,
  ) async {
    requests.add(options);
    if (options.method == 'GET') {
      searches++;
      return ResponseBody.fromString(
        jsonEncode([
          {
            'guid': 'cantinarr:release:fixture-$searches',
            'title': 'Fixture $searches',
            'indexerId': 7,
            'protocol': 'usenet',
          },
        ]),
        200,
        headers: {
          'content-type': ['application/json'],
        },
      );
    }
    if (options.method != 'POST') {
      throw StateError('Unexpected release request method: ${options.method}');
    }
    grabs++;
    if (grabs == 1) {
      return ResponseBody.fromString(
        '{"error":"This release selection is no longer available. Search again and choose a release."}\n',
        410,
        headers: {
          'content-type': ['text/plain; charset=utf-8'],
        },
      );
    }
    return ResponseBody.fromString(
      '{}',
      200,
      headers: {
        'content-type': ['application/json'],
      },
    );
  }

  @override
  void close({bool force = false}) {}
}
