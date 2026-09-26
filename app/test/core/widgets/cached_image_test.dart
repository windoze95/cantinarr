import 'dart:ui' as ui;

import 'package:cached_network_image/cached_network_image.dart';
import 'package:cached_network_image_platform_interface/cached_network_image_platform_interface.dart'
    show ImageRenderMethodForWeb;
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:cantinarr/core/widgets/artwork_resize_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('web visible and prefetched artwork use the standard provider with headers', () {
    const source = (url: 'https://cantina.example/art.jpg',
        headers: {'Authorization': 'Bearer test'});
    final provider = cachedImageProvider(source, isWeb: true) as NetworkImage;
    expect(provider.headers, source.headers);
    expect(provider.webHtmlElementStrategy, WebHtmlElementStrategy.never);
    expect(provider, cachedImageProvider(source, isWeb: true));
  });

  test('headered cache keys survive token rotation but isolate access contexts', () async {
    ImageProvider provider(String token, String scope) => cachedImageProvider(
        (url: 'https://cantina.example/art.jpg', headers: {'Authorization': 'Bearer $token'}),
        isWeb: true, cacheScope: scope);
    final before = provider('old', 'account-one') as SessionNetworkImage;
    final rotated = provider('new', 'account-one') as SessionNetworkImage;
    expect(before, isNot(rotated), reason: 'retry failed reads with fresh headers');
    final oldKey = await before.obtainKey(ImageConfiguration.empty);
    final newKey = await rotated.obtainKey(ImageConfiguration.empty);
    expect(oldKey, newKey);
    expect(oldKey.hashCode, newKey.hashCode);
    expect(rotated.networkImage.headers, {'Authorization': 'Bearer new'});
    expect(newKey, isNot(await provider('new', 'account-two').obtainKey(ImageConfiguration.empty)));
  });

  for (final resized in [false, true]) {
  testWidgets('token rotation reuses a decoded image and retries a failed old-token read (resized: $resized)', (tester) async {
    Widget frame(_NetworkFixture network, String scope) => MaterialApp(home: Image(
      image: resized ? ArtworkResizeImage.forDisplay(SessionNetworkImage(network, scope),
          const Size(124, 186), 2, BoxFit.cover) : SessionNetworkImage(network, scope),
      errorBuilder: (_, __, ___) => const Text('Unavailable'),
    ));
    final image = (await tester.runAsync(() => createTestImage(width: 4, height: 4)))!;
    addTearDown(image.dispose);
    final first = _NetworkFixture('old', image);
    await tester.pumpWidget(frame(first, 'account-one'));
    await tester.pumpAndSettle();
    final decoded = tester.widget<RawImage>(find.byType(RawImage)).image;
    expect(decoded, isNotNull);
    final rotated = _NetworkFixture('new', image);
    await tester.pumpWidget(frame(rotated, 'account-one'));
    await tester.pump();
    expect(rotated.reads, 0);
    expect(tester.widget<RawImage>(find.byType(RawImage)).image, same(decoded));

    final denied = _NetworkFixture('expired', image, fail: true);
    await tester.pumpWidget(frame(denied, 'account-two'));
    await tester.pumpAndSettle();
    expect(find.text('Unavailable'), findsOneWidget);
    final refreshed = _NetworkFixture('fresh', image);
    await tester.pumpWidget(frame(refreshed, 'account-two'));
    await tester.pumpAndSettle();
    expect(refreshed.reads, 1);
    expect(find.text('Unavailable'), findsNothing);
    expect(tester.widget<RawImage>(find.byType(RawImage)).image, isNotNull);
    await tester.pumpWidget(const SizedBox());
    PaintingBinding.instance.imageCache.clear();
  });
  }

  group('Hardcover web artwork', () {
    const source = (
      url: 'https://assets.hardcover.app/edition/1/cover.jpg',
      headers: null,
    );

    test('public Hardcover covers use browser image elements only on web', () {
      expect(usesHtmlImageElement(source, isWeb: true), isTrue);
      expect(usesHtmlImageElement(source, isWeb: false), isFalse);
    });

    test('authenticated images never lose their headers to an HTML element',
        () {
      expect(
        usesHtmlImageElement(
          (url: source.url, headers: const {'Authorization': 'Bearer token'}),
          isWeb: true,
        ),
        isFalse,
      );
    });

    test('other image hosts and insecure URLs keep their existing transport',
        () {
      for (final url in [
        'http://assets.hardcover.app/edition/1/cover.jpg',
        'https://assets.hardcover.app.example.com/cover.jpg',
        'https://image.tmdb.org/t/p/w342/abc.jpg',
        'https://cantina.example/api/instances/books/MediaCover/1.jpg',
      ]) {
        expect(usesHtmlImageElement((url: url, headers: null), isWeb: true),
            isFalse);
      }
    });
  });

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

    // A Hardcover cover fetched straight from the CDN can only render as a DOM
    // image element, and Flutter web paints platform views above the canvas —
    // which hid the availability badge and rating on every card past the first
    // handful. The relay keeps covers on the canvas path so they layer right.
    test('web rewrites Hardcover covers to the backend relay with the bearer',
        () {
      for (final path in [
        // Hardcover serves covers under both spellings.
        '/edition/31601422/3a01ea07.jpeg',
        '/editions/3274049/8741341047797682-91mYu67RfUL._SL1500_.jpg',
      ]) {
        final source = resolveImageSource(
          url: 'https://assets.hardcover.app$path',
          serverUrl: 'https://cantina.example',
          accessToken: 'tok',
          isWeb: true,
        );
        expect(source.url, 'https://cantina.example/api/discover/books/images$path');
        expect(source.headers, {'Authorization': 'Bearer tok'});
      }
    });

    test('native fetches Hardcover covers straight from the CDN', () {
      const cover = 'https://assets.hardcover.app/edition/1/cover.jpg';
      final source = resolveImageSource(
        url: cover,
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: false,
      );
      expect(source.url, cover);
      expect(source.headers, isNull);
    });

    test('web never relays a Hardcover lookalike host', () {
      const lookalike = 'https://assets.hardcover.app.evil.com/edition/1/a.jpg';
      final source = resolveImageSource(
        url: lookalike,
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: true,
      );
      expect(source.url, lookalike);
      expect(source.headers, isNull);
    });

    test('a relayed Hardcover cover no longer wants a DOM image element', () {
      final source = resolveImageSource(
        url: 'https://assets.hardcover.app/edition/1/cover.jpg',
        serverUrl: 'https://cantina.example',
        accessToken: 'tok',
        isWeb: true,
      );
      expect(usesHtmlImageElement(source, isWeb: true), isFalse);
    });

    test('web without a session still falls back to the CDN url', () {
      // No server URL to build a relay path from: showing the cover through a
      // DOM image element beats showing no cover at all.
      const cover = 'https://assets.hardcover.app/edition/1/cover.jpg';
      final source = resolveImageSource(url: cover, isWeb: true);
      expect(source.url, cover);
      expect(usesHtmlImageElement(source, isWeb: true), isTrue);
    });
  });

  group('CachedImage render method', () {
    testWidgets('native headered artwork retains its cached transport',
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

    testWidgets('native Hardcover covers keep the shared cached image path',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: CachedImage(
              url: 'https://assets.hardcover.app/edition/1/cover.jpg'),
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

class _NetworkFixture extends ImageProvider<NetworkImage> implements NetworkImage {
  _NetworkFixture(String token, this.image, {this.fail = false})
      : headers = {'Authorization': 'Bearer $token'};
  final bool fail;
  final ui.Image image;
  int reads = 0;
  @override
  final Map<String, String> headers;
  @override
  String get url => 'https://fixture.invalid/poster.png';
  @override
  double get scale => 1;
  @override
  WebHtmlElementStrategy get webHtmlElementStrategy => WebHtmlElementStrategy.never;
  @override
  Future<NetworkImage> obtainKey(ImageConfiguration configuration) async => this;
  @override
  ImageStreamCompleter loadImage(NetworkImage key, ImageDecoderCallback decode) =>
      OneFrameImageStreamCompleter(() async {
        reads++;
        if (fail) throw StateError('expired image credentials');
        return ImageInfo(image: image.clone());
      }());
  @override
  ImageStreamCompleter loadBuffer(NetworkImage key, DecoderBufferCallback decode) =>
      throw UnimplementedError();
}
