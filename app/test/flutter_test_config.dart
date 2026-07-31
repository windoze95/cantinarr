import 'dart:async';
import 'dart:ui' as ui;

import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

/// Loaded automatically by `flutter test` for every test under `test/`.
///
/// Swaps the default golden-file comparator for a tolerant one so goldens
/// generated on macOS still pass on the Linux CI renderer, whose glyph and
/// shape anti-aliasing (and engine version) drifts slightly. The default
/// comparator counts a pixel as different on any 1-bit channel delta; ours
/// ignores low-magnitude rasterisation noise and only fails when enough
/// pixels differ by a humanly meaningful amount. Text keeps the default
/// deterministic Ahem test font (solid boxes) — never load real fonts in
/// goldens, that determinism is what keeps them portable. Keep golden scenes
/// flat-colour and text-light so genuine regressions land far above the
/// threshold. Regenerate goldens with `flutter test --update-goldens`.
Future<void> testExecutable(FutureOr<void> Function() testMain) async {
  final comparator = goldenFileComparator;
  if (comparator is LocalFileComparator) {
    goldenFileComparator = _TolerantGoldenFileComparator(comparator.basedir);
  }
  await testMain();
}

class _TolerantGoldenFileComparator extends LocalFileComparator {
  /// [LocalFileComparator] derives its golden lookup directory from the test
  /// file's URI, so hand it a synthetic file inside the original basedir.
  _TolerantGoldenFileComparator(Uri basedir)
      : super(basedir.resolve('config.dart'));

  /// Per-channel delta (out of 255) at or below which a pixel difference is
  /// treated as anti-aliasing noise rather than a real change. Comparable to
  /// pixelmatch's default perceptual threshold.
  static const int _channelTolerance = 24;

  /// Maximum fraction of pixels (1%) allowed to differ beyond
  /// [_channelTolerance]. Cross-platform drift lands on scattered AA edge
  /// pixels; a real regression (recoloured, moved, or missing element)
  /// changes a contiguous region far above this.
  static const double _maxDiffFraction = 0.01;

  @override
  Future<bool> compare(Uint8List imageBytes, Uri golden) async {
    final goldenBytes = Uint8List.fromList(await getGoldenBytes(golden));
    final result =
        await GoldenFileComparator.compareLists(imageBytes, goldenBytes);
    if (result.passed) {
      result.dispose();
      return true;
    }

    final diff = await _significantDiff(imageBytes, goldenBytes);
    if (diff != null && diff.fraction <= _maxDiffFraction) {
      result.dispose();
      return true;
    }

    final error = await generateFailureOutput(result, golden, basedir);
    result.dispose();
    throw FlutterError(
      diff == null
          ? error
          : '$error\n'
              'Tolerant comparison: '
              '${(diff.fraction * 100).toStringAsFixed(2)}% of pixels differ '
              'by more than $_channelTolerance/255 on a channel (budget '
              '${(_maxDiffFraction * 100).toStringAsFixed(2)}%). '
              'Pixels by max channel delta: ${diff.histogram}',
    );
  }

  /// Fraction of pixels whose max per-channel delta exceeds
  /// [_channelTolerance], plus a delta histogram for tuning. Returns null
  /// when the images cannot be compared pixelwise (e.g. size mismatch).
  static Future<({double fraction, String histogram})?> _significantDiff(
    Uint8List testPng,
    Uint8List goldenPng,
  ) async {
    final ui.Image testImage = await _decode(testPng);
    final ui.Image masterImage = await _decode(goldenPng);
    try {
      if (testImage.width != masterImage.width ||
          testImage.height != masterImage.height) {
        return null;
      }
      final ByteData? testData =
          await testImage.toByteData(format: ui.ImageByteFormat.rawStraightRgba);
      final ByteData? masterData = await masterImage.toByteData(
          format: ui.ImageByteFormat.rawStraightRgba);
      if (testData == null || masterData == null) {
        return null;
      }
      final Uint8List a = testData.buffer.asUint8List();
      final Uint8List b = masterData.buffer.asUint8List();
      const bucketEdges = [8, 16, 24, 64, 255];
      final buckets = List<int>.filled(bucketEdges.length, 0);
      var significant = 0;
      for (var i = 0; i < a.length; i += 4) {
        var maxDelta = 0;
        for (var c = 0; c < 4; c++) {
          final delta = (a[i + c] - b[i + c]).abs();
          if (delta > maxDelta) {
            maxDelta = delta;
          }
        }
        if (maxDelta == 0) {
          continue;
        }
        for (var bucket = 0; bucket < bucketEdges.length; bucket++) {
          if (maxDelta <= bucketEdges[bucket]) {
            buckets[bucket]++;
            break;
          }
        }
        if (maxDelta > _channelTolerance) {
          significant++;
        }
      }
      final histogram = [
        for (var bucket = 0; bucket < bucketEdges.length; bucket++)
          '<=${bucketEdges[bucket]}: ${buckets[bucket]}',
      ].join(', ');
      return (fraction: significant / (a.length ~/ 4), histogram: histogram);
    } finally {
      testImage.dispose();
      masterImage.dispose();
    }
  }

  static Future<ui.Image> _decode(Uint8List png) async {
    final ui.Codec codec = await ui.instantiateImageCodec(png);
    try {
      return (await codec.getNextFrame()).image;
    } finally {
      codec.dispose();
    }
  }
}
