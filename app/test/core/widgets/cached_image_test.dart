import 'package:cached_network_image/cached_network_image.dart';
import 'package:cached_network_image_platform_interface/cached_network_image_platform_interface.dart'
    show ImageRenderMethodForWeb;
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('resolveImageSource', () {
    const walterUrl =
        'https://walter-r2.trakt.tv/images/movies/000/337/posters/thumb/faaa819377.jpg.webp';

    test('native fetches every host directly, Trakt CDN included', () {
      final source = resolveImageSource(
        url: walterUrl,
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: false,
      );
      expect(source.url, walterUrl);
      expect(source.headers, isNull);
    });

    test('web rewrites Trakt CDN urls to the backend relay with the bearer',
        () {
      final source = resolveImageSource(
        url: walterUrl,
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: true,
      );
      expect(
        source.url,
        'https://cantina.example/api/trakt/images/walter-r2.trakt.tv'
        '/images/movies/000/337/posters/thumb/faaa819377.jpg.webp',
      );
      expect(source.headers, {'Authorization': 'Bearer tok'});
    });

    test('web rewrites whichever trakt.tv CDN host the payload carries', () {
      // media.trakt.tv is what Trakt serves today; walter*.trakt.tv is what
      // it served before July 2026. The rewrite follows the domain so a CDN
      // migration on Trakt's side cannot silently blank the posters again.
      for (final host in [
        'media.trakt.tv',
        'walter.trakt.tv',
        'walter-r2.trakt.tv',
      ]) {
        final source = resolveImageSource(
          url: 'https://$host/images/shows/000/001/posters/a.jpg',
          serverUrl: 'https://cantina.example',
          accessToken: 'tok',
          isWeb: true,
        );
        expect(
          source.url,
          'https://cantina.example/api/trakt/images/$host'
          '/images/shows/000/001/posters/a.jpg',
        );
      }
    });

    test('web never relays lookalike or apex trakt hosts', () {
      for (final url in [
        'https://notrakt.tv/images/a.jpg',
        'https://media.trakt.tv.evil.com/images/a.jpg',
        'https://trakt.tv/images/a.jpg',
      ]) {
        final source = resolveImageSource(
          url: url,
          serverUrl: 'https://cantina.example',
          accessToken: 'tok',
          isWeb: true,
        );
        expect(source.url, url);
        expect(source.headers, isNull);
      }
    });

    test('a trailing-slash server url never doubles the slash', () {
      final source = resolveImageSource(
        url: walterUrl,
        serverUrl: 'https://cantina.example/',
        accessToken: 'tok',
        isWeb: true,
      );
      expect(source.url, startsWith('https://cantina.example/api/'));
    });

    test('web leaves CORS-friendly hosts alone', () {
      const tmdb = 'https://image.tmdb.org/t/p/w342/abc.jpg';
      final source = resolveImageSource(
        url: tmdb,
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: true,
      );
      expect(source.url, tmdb);
      expect(source.headers, isNull);
    });

    test('web leaves backend proxy urls and their headers alone', () {
      const proxied =
          'https://cantina.example/api/instances/abc/api/v1/MediaCover/1/cover.jpg';
      final source = resolveImageSource(
        url: proxied,
        headers: const {'Authorization': 'Bearer tok'},
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: true,
      );
      expect(source.url, proxied);
      expect(source.headers, const {'Authorization': 'Bearer tok'});
    });

    test('web without a session passes through rather than half-rewriting',
        () {
      final source = resolveImageSource(url: walterUrl, isWeb: true);
      expect(source.url, walterUrl);
      expect(source.headers, isNull);
    });

    test('web refuses to relay a walter path outside images/', () {
      const odd = 'https://walter-r2.trakt.tv/other/thing.jpg';
      final source = resolveImageSource(
        url: odd,
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: true,
      );
      expect(source.url, odd);
    });
  });

  group('CachedImage render method', () {
    testWidgets('headered requests use the HTTP fetch path on web',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: CachedImage(
            url: 'https://cantina.example/api/instances/x/api/v1/MediaCover/1.jpg',
            headers: {'Authorization': 'Bearer tok'},
          ),
        ),
      );
      final provider = tester.widget<Image>(find.byType(Image)).image
          as CachedNetworkImageProvider;
      expect(provider.imageRenderMethodForWeb, ImageRenderMethodForWeb.HttpGet);
      // Let the (failing, test-environment) fetch resolve into the error
      // fallback so nothing is pending when the test ends.
      await tester.pump(const Duration(seconds: 1));
    });

    testWidgets('header-free requests keep the browser-native path',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: CachedImage(url: 'https://image.tmdb.org/t/p/w342/abc.jpg'),
        ),
      );
      final provider = tester.widget<Image>(find.byType(Image)).image
          as CachedNetworkImageProvider;
      expect(
          provider.imageRenderMethodForWeb, ImageRenderMethodForWeb.HtmlImage);
      await tester.pump(const Duration(seconds: 1));
    });
  });
}
