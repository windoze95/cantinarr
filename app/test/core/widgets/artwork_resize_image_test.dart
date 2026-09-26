import 'dart:async';
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:cantinarr/core/widgets/artwork_resize_image.dart';
import 'package:cantinarr/core/widgets/cached_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  Future<ui.Image> load(WidgetTester tester, ImageProvider provider) async {
    await tester.pumpWidget(const MaterialApp(home: SizedBox()));
    await tester.runAsync(() => precacheImage(provider, tester.element(find.byType(SizedBox))));
    await tester.pumpWidget(MaterialApp(home: Image(image: provider)));
    await tester.pumpAndSettle();
    return tester.widget<RawImage>(find.byType(RawImage)).image!;
  }

  Future<Uint8List> pixels(WidgetTester tester, int width, int height) async =>
      (await tester.runAsync(() async {
        final image = await createTestImage(width: width, height: height);
        final bytes = await image.toByteData(format: ui.ImageByteFormat.png);
        image.dispose();
        return bytes!.buffer.asUint8List();
      }))!;

  testWidgets('decode near display resolution, retaining enough pixels for cover crops', (tester) async {
    final source = MemoryImage(await pixels(tester, 2000, 1000));
    for (final sample in [
      (fit: BoxFit.cover, size: const Size(100, 100), dpr: 1.0, decoded: const Size(256, 128)),
      (fit: BoxFit.contain, size: const Size(100, 100), dpr: 1.0, decoded: const Size(128, 64)),
      (fit: BoxFit.cover, size: const Size(124, 186), dpr: 1.0, decoded: const Size(384, 192)),
      (fit: BoxFit.cover, size: const Size(124, 186), dpr: 2.0, decoded: const Size(768, 384)),
      (fit: BoxFit.cover, size: const Size(124, double.infinity), dpr: 1.0, decoded: const Size(128, 64)),
    ]) {
      final provider = ArtworkResizeImage.forDisplay(source, sample.size, sample.dpr, sample.fit);
      final image = await load(tester, provider);
      expect(Size(image.width.toDouble(), image.height.toDouble()), sample.decoded,
          reason: '${sample.fit}, ${sample.size}, DPR ${sample.dpr}');
    }
  });

  testWidgets('small originals are not upscaled during decode', (tester) async {
    final provider = ArtworkResizeImage.forDisplay(
        MemoryImage(await pixels(tester, 20, 30)), const Size(124, 186), 2, BoxFit.cover);
    final image = await load(tester, provider);
    expect((image.width, image.height), (20, 30));
  });

  testWidgets('fine source detail averages instead of aliasing into a grainy pattern', (tester) async {
    final bytes = (await tester.runAsync(() async {
      const width = 600, height = 900;
      final rgba = Uint8List(width * height * 4);
      for (var y = 0; y < height; y++) {
        for (var x = 0; x < width; x++) {
          final i = (y * width + x) * 4;
          final value = (x + y).isEven ? 0 : 255;
          rgba[i] = rgba[i + 1] = rgba[i + 2] = value;
          rgba[i + 3] = 255;
        }
      }
      final ready = Completer<ui.Image>();
      ui.decodeImageFromPixels(rgba, width, height, ui.PixelFormat.rgba8888, ready.complete);
      final source = await ready.future;
      final png = await source.toByteData(format: ui.ImageByteFormat.png);
      source.dispose();
      return png!.buffer.asUint8List();
    }))!;
    final image = await load(tester, ArtworkResizeImage.forDisplay(
        MemoryImage(bytes), const Size(124, 186), 1, BoxFit.cover));
    final reduced = (await tester.runAsync(() => image.toByteData()))!;
    // A one-pixel checkerboard is below this thumbnail's resolution. Its
    // interior should be neutral grey, with no invented light/dark pattern.
    for (var y = 8; y < image.height - 8; y++) {
      for (var x = 8; x < image.width - 8; x++) {
        expect(reduced.getUint8((y * image.width + x) * 4), inInclusiveRange(125, 130));
      }
    }
  });

  test('prefetch and display share size buckets and retain authorization-scoped keys', () async {
    ImageProvider provider(String token, String scope, Size size) => cachedImageProvider(
      (url: 'https://cantina.example/poster.jpg', headers: {'Authorization': 'Bearer $token'}),
      isWeb: true, cacheScope: scope, displaySize: size, devicePixelRatio: 2,
    );
    final before = provider('old', 'account-one', const Size(124, 186));
    final rotated = provider('new', 'account-one', const Size(123, 185));
    expect(before, isNot(rotated), reason: 'a failed token must still retry');
    final key = await before.obtainKey(ImageConfiguration.empty);
    expect(key, await rotated.obtainKey(ImageConfiguration.empty));
    expect(key, isNot(await provider('new', 'account-two', const Size(124, 186))
        .obtainKey(ImageConfiguration.empty)));
    expect(key, isNot(await provider('new', 'account-one', const Size(300, 450))
        .obtainKey(ImageConfiguration.empty)));
  });

  test('unbounded and unscaled images keep their source provider', () {
    const source = NetworkImage('https://cantina.example/poster.jpg');
    expect(ArtworkResizeImage.forDisplay(source, Size.infinite, 2, BoxFit.cover), same(source));
    expect(ArtworkResizeImage.forDisplay(source, const Size(124, 186), 2, BoxFit.none), same(source));
  });
}
