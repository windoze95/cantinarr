import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

/// Loaded automatically by `flutter test` for every test under `test/`.
///
/// Swaps the default golden-file comparator for a tolerant one so goldens
/// generated on macOS still pass on the Linux CI renderer, whose glyph and
/// shape anti-aliasing drifts by a handful of pixels. Text keeps the default
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

  /// Maximum fraction of pixels allowed to differ (1%).
  ///
  /// Cross-platform anti-aliasing drift measures well below this on the
  /// flat-colour scenes we golden; a layout shift of even a few pixels
  /// measures well above it.
  static const double _maxDiffFraction = 0.01;

  @override
  Future<bool> compare(Uint8List imageBytes, Uri golden) async {
    final ComparisonResult result = await GoldenFileComparator.compareLists(
      imageBytes,
      await getGoldenBytes(golden),
    );

    if (result.passed || result.diffPercent <= _maxDiffFraction) {
      result.dispose();
      return true;
    }

    final String error = await generateFailureOutput(result, golden, basedir);
    result.dispose();
    throw FlutterError(error);
  }
}
