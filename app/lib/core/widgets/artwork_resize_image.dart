import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' as ui;

import 'package:flutter/painting.dart';

typedef ArtworkResizeKey = ({Object source, int? width, int? height, BoxFit fit});

/// Reduce large web artwork near its physical display size before painting it.
/// Bicubic painting alone aliases heavily reduced originals, and CanvasKit's
/// mipmapped painting can change quality when a GPU texture is recreated.
/// The reduction is cached with the decoded frame, outside hover repaints.
class ArtworkResizeImage extends ImageProvider<ArtworkResizeKey> {
  final ImageProvider source;
  final int? width;
  final int? height;
  final BoxFit fit;

  const ArtworkResizeImage(this.source, {
    required this.width, required this.height, required this.fit,
  }) : assert(width != null || height != null),
       assert(width == null || width > 0), assert(height == null || height > 0);

  static ImageProvider forDisplay(ImageProvider source, Size size,
      double devicePixelRatio, BoxFit fit) {
    // Small layout changes share a decoded frame. Leave room for card hover
    // enlargement without decoding again on every animation frame.
    int? pixels(double extent) => extent.isFinite && extent > 0
        ? (extent * devicePixelRatio * 1.025 / 32).ceil() * 32 : null;
    final width = pixels(size.width);
    final height = pixels(size.height);
    if ((width == null && height == null) || fit == BoxFit.none) return source;
    return ArtworkResizeImage(source, width: width, height: height, fit: fit);
  }

  @override
  Future<ArtworkResizeKey> obtainKey(ImageConfiguration configuration) =>
      source.obtainKey(configuration).then((key) =>
          (source: key, width: width, height: height, fit: fit));

  @override
  ImageStreamCompleter loadImage(ArtworkResizeKey key, ImageDecoderCallback decode) {
    final completer = source.loadImage(key.source, (buffer, {getTargetSize}) async =>
        _ArtworkCodec(await decode(buffer, getTargetSize: getTargetSize), key));
    completer.addEphemeralErrorListener((_, __) {
      scheduleMicrotask(() => PaintingBinding.instance.imageCache.evict(key));
    });
    return completer;
  }

  @override
  bool operator ==(Object other) => other is ArtworkResizeImage &&
      other.source == source && other.width == width &&
      other.height == height && other.fit == fit;

  @override
  int get hashCode => Object.hash(source, width, height, fit);
}

class _ArtworkCodec implements ui.Codec {
  final ui.Codec source;
  final ArtworkResizeKey size;
  _ArtworkCodec(this.source, this.size);

  @override
  int get frameCount => source.frameCount;
  @override
  int get repetitionCount => source.repetitionCount;
  @override
  void dispose() => source.dispose();

  @override
  Future<ui.FrameInfo> getNextFrame() async {
    final frame = await source.getNextFrame();
    var image = frame.image;
    final intrinsic = Size(image.width.toDouble(), image.height.toDouble());
    final target = Size(
      size.width?.toDouble() ?? image.width * size.height! / image.height,
      size.height?.toDouble() ?? image.height * size.width! / image.width,
    );
    final fitted = applyBoxFit(size.fit, intrinsic, target);
    // Preserve source aspect ratio, including cover crops. Fitting the whole
    // source inside a square would under-size a portrait's crop. Never upscale.
    final scale = math.min(1.0, math.max(
      fitted.destination.width / fitted.source.width,
      fitted.destination.height / fitted.source.height,
    ));
    final width = math.max(1, (image.width * scale).ceil());
    final height = math.max(1, (image.height * scale).ceil());
    try {
      // Flutter web's decode-size hint uses a single canvas resize, which can
      // alias too. Halve progressively so every source pixel contributes,
      // without relying on CanvasKit's transient mipmap cache (issue #653).
      while (image.width > width || image.height > height) {
        final w = math.max(width, (image.width / 2).ceil());
        final h = math.max(height, (image.height / 2).ceil());
        final recorder = ui.PictureRecorder();
        ui.Canvas(recorder).drawImageRect(image,
          Rect.fromLTWH(0, 0, image.width.toDouble(), image.height.toDouble()),
          Rect.fromLTWH(0, 0, w.toDouble(), h.toDouble()),
          ui.Paint()..filterQuality = ui.FilterQuality.low);
        final picture = recorder.endRecording();
        final ui.Image reduced;
        try {
          reduced = await picture.toImage(w, h);
        } finally {
          picture.dispose();
        }
        image.dispose();
        image = reduced;
      }
      return _ArtworkFrame(image, frame.duration);
    } catch (_) {
      image.dispose();
      rethrow;
    }
  }
}

class _ArtworkFrame implements ui.FrameInfo {
  @override
  final ui.Image image;
  @override
  final Duration duration;
  _ArtworkFrame(this.image, this.duration);
}
