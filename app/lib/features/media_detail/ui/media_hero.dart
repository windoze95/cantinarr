import 'dart:math' as math;
import 'dart:ui' as ui show ImageFilter;

import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart' show OverScrollHeaderStretchConfiguration;

import '../../../core/automation/web_semantics.dart';
import '../../../core/config/app_config.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/app_image_cache.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';

/// Scroll-choreographed cinematic hero for the media detail screen.
///
/// A pinned [SliverPersistentHeaderDelegate] so the scroll position drives
/// every ramp directly through `shrinkOffset` — no scroll listeners, no
/// setState, and only the header subtree rebuilds while the content slivers
/// below never re-render during a collapse.
///
/// Layer choreography (t = collapse progress 0→1):
/// - backdrop translates up at 0.5x scroll speed (its bottom edge can never
///   reveal the layers beneath: bottom = maxExtent − 0.5s ≥ crop line
///   maxExtent − s) and zooms up to 1.18x under iOS overscroll stretch;
/// - a dim wash and the poster/title fade through the middle of the collapse;
/// - past t ≈ 0.6 a marquee bar surfaces with the title climbing into it.
///
/// The backdrop is never blurred — legibility comes from gradient scrims.
class MediaHeroDelegate extends SliverPersistentHeaderDelegate {
  final String title;
  final String? posterPath;
  final String? backdropPath;
  final double expandedExtent;
  final double collapsedExtent;
  final double topPadding;
  final bool disableAnimations;
  final VoidCallback onBack;

  const MediaHeroDelegate({
    required this.title,
    required this.posterPath,
    required this.backdropPath,
    required this.expandedExtent,
    required this.collapsedExtent,
    required this.topPadding,
    required this.disableAnimations,
    required this.onBack,
  });

  /// Expanded hero height. Decided from whether a backdrop *path* exists —
  /// never from image load state — so the extent can't jump when the image
  /// lands.
  static double expandedExtentFor({
    required double viewportHeight,
    required double viewportWidth,
    required bool hasBackdrop,
  }) {
    final desktop = viewportWidth >= AppBreakpoints.desktopMinWidth;
    if (hasBackdrop) {
      return desktop
          ? (0.52 * viewportHeight).clamp(430.0, 640.0)
          : (0.58 * viewportHeight).clamp(400.0, 520.0);
    }
    return desktop ? 400.0 : (0.42 * viewportHeight).clamp(300.0, 380.0);
  }

  /// Collapsed (pinned marquee bar) height.
  static double collapsedExtentFor({required double topPadding}) =>
      topPadding + 64;

  @override
  double get maxExtent => math.max(expandedExtent, collapsedExtent);

  @override
  double get minExtent => collapsedExtent;

  /// Lets iOS overscroll grow the header past [maxExtent]; the body reads the
  /// stretched height off its LayoutBuilder and zooms the backdrop with it.
  @override
  OverScrollHeaderStretchConfiguration get stretchConfiguration =>
      OverScrollHeaderStretchConfiguration();

  @override
  Widget build(
      BuildContext context, double shrinkOffset, bool overlapsContent) {
    final range = math.max(1.0, maxExtent - minExtent);
    final t = (shrinkOffset / range).clamp(0.0, 1.0);
    return LayoutBuilder(
      builder: (context, constraints) => _MediaHeroBody(
        title: title,
        posterPath: posterPath,
        backdropPath: backdropPath,
        expandedExtent: maxExtent,
        topPadding: topPadding,
        disableAnimations: disableAnimations,
        onBack: onBack,
        t: t,
        shrinkOffset: shrinkOffset,
        // During overscroll stretch shrinkOffset stays 0 while the box is
        // laid out taller than maxExtent — this is the only place that
        // stretched height is observable.
        height: constraints.maxHeight,
        width: constraints.maxWidth,
      ),
    );
  }

  @override
  bool shouldRebuild(MediaHeroDelegate oldDelegate) =>
      title != oldDelegate.title ||
      posterPath != oldDelegate.posterPath ||
      backdropPath != oldDelegate.backdropPath ||
      expandedExtent != oldDelegate.expandedExtent ||
      collapsedExtent != oldDelegate.collapsedExtent ||
      topPadding != oldDelegate.topPadding ||
      disableAnimations != oldDelegate.disableAnimations;
}

/// Linear ramp of [t] across [a]..[b], clamped to 0..1.
double _ramp(double t, double a, double b) =>
    ((t - a) / (b - a)).clamp(0.0, 1.0);

/// Stateful so the pointer-parallax target survives the per-frame delegate
/// rebuilds during a scroll.
class _MediaHeroBody extends StatefulWidget {
  final String title;
  final String? posterPath;
  final String? backdropPath;
  final double expandedExtent;
  final double topPadding;
  final bool disableAnimations;
  final VoidCallback onBack;
  final double t;
  final double shrinkOffset;
  final double height;
  final double width;

  const _MediaHeroBody({
    required this.title,
    required this.posterPath,
    required this.backdropPath,
    required this.expandedExtent,
    required this.topPadding,
    required this.disableAnimations,
    required this.onBack,
    required this.t,
    required this.shrinkOffset,
    required this.height,
    required this.width,
  });

  @override
  State<_MediaHeroBody> createState() => _MediaHeroBodyState();
}

class _MediaHeroBodyState extends State<_MediaHeroBody> {
  /// Desktop-only pointer parallax target, in logical px. Updated without
  /// setState — only the backdrop plane listens.
  final ValueNotifier<Offset> _pointerTarget = ValueNotifier(Offset.zero);

  @override
  void dispose() {
    _pointerTarget.dispose();
    super.dispose();
  }

  bool get _pointerParallaxEnabled =>
      widget.backdropPath != null &&
      !widget.disableAnimations &&
      widget.width >= AppBreakpoints.desktopMinWidth;

  void _onHover(PointerEvent event) {
    if (!_pointerParallaxEnabled) return;
    final dx = (event.localPosition.dx / widget.width - 0.5) * 2;
    final dy = (event.localPosition.dy / widget.height - 0.5) * 2;
    _pointerTarget.value = Offset(dx.clamp(-1.0, 1.0) * 8, dy.clamp(-1.0, 1.0) * 5);
  }

  @override
  Widget build(BuildContext context) {
    final t = widget.t;
    final expandedLayout = widget.width >= 720;
    final hasBackdrop = widget.backdropPath != null;
    final pad = AppBreakpoints.centeredContentPadding(
      widget.width,
      maxContentWidth: 1180,
      minPadding: expandedLayout ? 24 : 16,
    );

    // Scroll ramps (see class doc). All pure arithmetic on t / shrinkOffset.
    // The bar choreography completes by t ≈ 0.9 rather than exactly 1.0: a
    // page whose content is barely taller than the viewport exhausts its
    // scroll extent before the header fully collapses, and its resting state
    // must still read as a finished marquee bar.
    final dimAlpha = 0.35 * _ramp(t, 0.30, 0.85);
    final foregroundOpacity = 1 - _ramp(t, 0.45, 0.72);
    final seamOpacity = 1 - _ramp(t, 0.35, 0.60);
    final barSurface = _ramp(t, 0.62, 0.88);
    final barTitleIn = Curves.easeOutCubic.transform(_ramp(t, 0.70, 0.92));
    final barHairline = _ramp(t, 0.82, 0.96);
    final backPillAlpha = 0.35 * (1 - _ramp(t, 0.62, 0.88));

    // Overscroll stretch: zoom the backdrop with the growing box, capped so
    // the crop never gets mushy.
    final stretchScale = widget.disableAnimations
        ? 1.0
        : (widget.height / widget.expandedExtent).clamp(1.0, 1.18);

    final hero = ClipRect(
      child: Stack(
        clipBehavior: Clip.hardEdge,
        fit: StackFit.expand,
        children: [
          // (1) Ambient stage: pre-load state and permanent no-backdrop art.
          _AmbientStage(raisedGlow: !hasBackdrop),

          // (2) Backdrop plane: parallax translate outside the repaint
          // boundary so a collapse is a layer transform, never a re-raster.
          if (hasBackdrop)
            Positioned(
              top: 0,
              left: 0,
              right: 0,
              height: math.max(widget.expandedExtent, widget.height),
              child: Transform.translate(
                offset: Offset(0, -0.5 * widget.shrinkOffset),
                child: Transform.scale(
                  scale: stretchScale,
                  alignment: Alignment.topCenter,
                  child: _BackdropPlane(
                    backdropPath: widget.backdropPath!,
                    width: widget.width,
                    desktop: widget.width >= AppBreakpoints.desktopMinWidth,
                    disableAnimations: widget.disableAnimations,
                    pointerTarget: _pointerTarget,
                    pointerParallax: _pointerParallaxEnabled,
                  ),
                ),
              ),
            )
          else
            const SizedBox.shrink(),

          // (3) Vertical legibility scrim: upper 38% of the image untouched,
          // bottom ramps fully into the page background so content emerges
          // from the sheet. Never a blur.
          const DecoratedBox(
            decoration: BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.topCenter,
                end: Alignment.bottomCenter,
                stops: [0, 0.38, 0.58, 0.80, 0.93, 1.0],
                colors: [
                  Color(0x000C0805),
                  Color(0x000C0805),
                  Color(0x380C0805),
                  Color(0xB80C0805),
                  Color(0xF50C0805),
                  AppTheme.background,
                ],
              ),
            ),
          ),

          // (4) Corner-anchor scrim: a dark wedge under the title block that
          // leaves the backdrop's focal area unveiled.
          DecoratedBox(
            decoration: BoxDecoration(
              gradient: LinearGradient(
                begin: Alignment.bottomLeft,
                end: const Alignment(0.15, 0.35),
                colors: [
                  AppTheme.background
                      .withValues(alpha: expandedLayout ? 0.55 : 0.42),
                  Colors.transparent,
                ],
              ),
            ),
          ),

          // (5) Top scrim: keeps the back affordance readable on any art.
          Positioned(
            top: 0,
            left: 0,
            right: 0,
            height: 96 + widget.topPadding,
            child: const DecoratedBox(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [Color(0x4D000000), Colors.transparent],
                ),
              ),
            ),
          ),

          // (6) Scroll dim: alpha baked into the color — no saveLayer.
          if (dimAlpha > 0)
            ColoredBox(color: AppTheme.background.withValues(alpha: dimAlpha))
          else
            const SizedBox.shrink(),

          // (7) Poster + title block.
          Positioned(
            left: pad,
            right: pad,
            bottom: 20,
            child: _HeroForeground(
              title: widget.title,
              posterPath: widget.posterPath,
              hasBackdrop: hasBackdrop,
              expandedLayout: expandedLayout,
              contentWidth: widget.width - 2 * pad,
              opacity: foregroundOpacity,
              shift: -0.15 * widget.shrinkOffset,
            ),
          ),

          // (8) Seam rule: hairline + amber tick marking where the hero hands
          // off to the content sheet. Stays mounted through the collapse so
          // its one-shot draw animation really runs only once.
          Positioned(
            left: pad,
            right: pad,
            bottom: 0,
            child: Opacity(
              opacity: seamOpacity,
              child: _SeamRule(disableAnimations: widget.disableAnimations),
            ),
          ),

          // (9) Marquee bar + back affordance, topmost.
          Positioned(
            top: 0,
            left: 0,
            right: 0,
            height: widget.topPadding + 64,
            child: _MarqueeBar(
              title: widget.title,
              topPadding: widget.topPadding,
              pad: pad,
              surfaceIn: barSurface,
              titleIn: barTitleIn,
              hairlineIn: barHairline,
              backPillAlpha: backPillAlpha,
              onBack: widget.onBack,
            ),
          ),
        ],
      ),
    );

    // Always mounted (handlers just switch off) so a breakpoint or
    // reduced-motion flip can't tear down the hero subtree's state.
    return MouseRegion(
      opaque: false,
      onHover: _pointerParallaxEnabled ? _onHover : null,
      onExit: _pointerParallaxEnabled
          ? (_) => _pointerTarget.value = Offset.zero
          : null,
      child: hero,
    );
  }
}

/// Warm radial glow on the page surface — simultaneously the pre-image
/// loading state and the permanent art when a title has no backdrop, so the
/// first frame is always a finished composition.
class _AmbientStage extends StatelessWidget {
  /// Raise the accent glow when the stage is the permanent art.
  final bool raisedGlow;

  const _AmbientStage({required this.raisedGlow});

  @override
  Widget build(BuildContext context) {
    return DecoratedBox(
      decoration: const BoxDecoration(color: AppTheme.surface),
      child: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            center: Alignment(-0.6, 0.9),
            radius: 1.3,
            colors: [Color(0x59573916), Color(0x00573916)],
          ),
        ),
        child: DecoratedBox(
          decoration: BoxDecoration(
            gradient: RadialGradient(
              center: const Alignment(0.5, 0.30),
              radius: 0.9,
              colors: [
                raisedGlow ? const Color(0x1AF2AC2D) : const Color(0x0FF2AC2D),
                const Color(0x00F2AC2D),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// The crisp backdrop image with its entrance settle (scale 1.06→1 as it
/// fades in) and the desktop pointer parallax, both skipped under reduced
/// motion.
class _BackdropPlane extends StatefulWidget {
  final String backdropPath;
  final double width;
  final bool desktop;
  final bool disableAnimations;
  final ValueNotifier<Offset> pointerTarget;
  final bool pointerParallax;

  const _BackdropPlane({
    required this.backdropPath,
    required this.width,
    required this.desktop,
    required this.disableAnimations,
    required this.pointerTarget,
    required this.pointerParallax,
  });

  @override
  State<_BackdropPlane> createState() => _BackdropPlaneState();
}

class _BackdropPlaneState extends State<_BackdropPlane>
    with SingleTickerProviderStateMixin {
  /// Created on first use so the reduced-motion path never allocates a
  /// ticker (dispose must therefore not force the lazy init).
  AnimationController? _settleController;
  AnimationController get _settle => _settleController ??= AnimationController(
        vsync: this,
        duration: Duration(milliseconds: widget.desktop ? 900 : 600),
      );
  bool _settleKicked = false;

  @override
  void dispose() {
    _settleController?.dispose();
    super.dispose();
  }

  /// Start the settle the frame the image first becomes available, so it
  /// arrives already moving. Called from frameBuilder — i.e. during build —
  /// so the controller may only be touched from the deferred callback.
  void _kickSettle() {
    if (_settleKicked || widget.disableAnimations) return;
    _settleKicked = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _settle.forward();
    });
  }

  /// Slight overscan when pointer parallax is live so the ±8px translate can
  /// never drag the image edge inside the plane.
  double get _baseScale => widget.pointerParallax ? 1.02 : 1.0;

  /// TMDB size ladder from physical pixels: sharp at every width without
  /// paying `original` bytes below genuinely wide displays.
  String _imageUrl(double physicalWidth) {
    if (physicalWidth <= 900) {
      return AppConfig.tmdbBackdrop(widget.backdropPath, width: 780);
    }
    if (physicalWidth <= 2560) {
      return AppConfig.tmdbBackdrop(widget.backdropPath, width: 1280);
    }
    return AppConfig.tmdbBackdropOriginal(widget.backdropPath);
  }

  @override
  Widget build(BuildContext context) {
    final physicalWidth =
        widget.width * MediaQuery.devicePixelRatioOf(context);
    // Decode capped near the display size, quantized so window resizes reuse
    // the cached decode instead of re-decoding per pixel of width change.
    final cacheWidth = (physicalWidth / 320).ceil() * 320;
    // A plain Image over the shared cache: cached_network_image's imageBuilder
    // path would resolve the un-resized provider a second time, so the resize
    // wrapping happens here instead.
    final provider = ResizeImage.resizeIfNeeded(
      cacheWidth,
      null,
      CachedNetworkImageProvider(
        _imageUrl(physicalWidth),
        cacheManager: appImageCache,
      ),
    );

    Widget image = RepaintBoundary(
      child: Image(
        image: provider,
        fit: BoxFit.cover,
        alignment: Alignment.center,
        filterQuality: FilterQuality.medium,
        gaplessPlayback: true,
        // The ambient stage shows through while loading and stays if the
        // image errors — never a broken-image glyph.
        errorBuilder: (_, __, ___) => const SizedBox.shrink(),
        frameBuilder: (context, child, frame, wasSynchronouslyLoaded) {
          if (frame != null) _kickSettle();
          if (wasSynchronouslyLoaded || widget.disableAnimations) return child;
          return AnimatedOpacity(
            opacity: frame == null ? 0 : 1,
            duration: const Duration(milliseconds: 200),
            curve: Curves.easeOut,
            child: child,
          );
        },
      ),
    );

    // Entrance settle: a layer transform around the repaint boundary. Under
    // reduced motion the controller is never started or read.
    if (!widget.disableAnimations) {
      final settleCurve =
          widget.desktop ? Curves.easeOutQuint : Curves.easeOutCubic;
      final settleAmp = widget.desktop ? 0.08 : 0.06;
      image = AnimatedBuilder(
        animation: _settle,
        builder: (context, child) {
          final scale = _baseScale +
              settleAmp * (1 - settleCurve.transform(_settle.value));
          if (scale == 1) return child!;
          return Transform.scale(scale: scale, child: child);
        },
        child: image,
      );
    }

    // Always mounted (idle at zero when parallax is off) so pointer-parallax
    // availability flips can't re-inflate the image subtree.
    return ValueListenableBuilder<Offset>(
      valueListenable: widget.pointerTarget,
      builder: (context, target, child) => TweenAnimationBuilder<Offset>(
        tween: Tween(end: target),
        duration: const Duration(milliseconds: 120),
        builder: (context, offset, child) => offset == Offset.zero
            ? child!
            : Transform.translate(offset: offset, child: child),
        child: child,
      ),
      child: image,
    );
  }
}

/// Poster card + display title, fading and counter-rising through the middle
/// of the collapse.
class _HeroForeground extends StatelessWidget {
  final String title;
  final String? posterPath;
  final bool hasBackdrop;
  final bool expandedLayout;
  final double contentWidth;
  final double opacity;
  final double shift;

  const _HeroForeground({
    required this.title,
    required this.posterPath,
    required this.hasBackdrop,
    required this.expandedLayout,
    required this.contentWidth,
    required this.opacity,
    required this.shift,
  });

  @override
  Widget build(BuildContext context) {
    // Without a backdrop the poster steps up a size and becomes the
    // composition's object.
    final posterWidth = hasBackdrop
        ? (expandedLayout ? 156.0 : 108.0)
        : (expandedLayout ? 172.0 : 128.0);
    final posterHeight = posterWidth * 1.5;

    final textScale = MediaQuery.textScalerOf(context).scale(100) / 100;
    final mobileFontSize = title.length > 28 ? 28.0 : 34.0;

    Widget block = Row(
      crossAxisAlignment: CrossAxisAlignment.end,
      children: [
        Container(
          width: posterWidth,
          height: posterHeight,
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
            border: Border.all(
              color: AppTheme.textPrimary.withValues(alpha: 0.15),
            ),
            boxShadow: [
              // Ambient + contact shadows read as physical card lighting.
              BoxShadow(
                color: Colors.black.withValues(alpha: 0.45),
                blurRadius: 32,
                offset: const Offset(0, 18),
              ),
              BoxShadow(
                color: Colors.black.withValues(alpha: 0.35),
                blurRadius: 8,
                offset: const Offset(0, 4),
              ),
            ],
          ),
          // Top-edge sheen: printed-card light, not a blur.
          foregroundDecoration: BoxDecoration(
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
            gradient: LinearGradient(
              begin: Alignment.topCenter,
              end: Alignment.bottomCenter,
              stops: const [0, 0.12],
              colors: [
                Colors.white.withValues(alpha: 0.16),
                Colors.white.withValues(alpha: 0),
              ],
            ),
          ),
          child: ClipRRect(
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge - 1),
            child: CachedImage(
              url: posterPath == null ? null : AppConfig.tmdbPoster(posterPath),
              fit: BoxFit.cover,
              icon: Icons.movie_outlined,
              iconSize: 40,
            ),
          ),
        ),
        SizedBox(width: expandedLayout ? 24 : 15),
        Expanded(
          child: Padding(
            padding: EdgeInsets.only(bottom: expandedLayout ? 12 : 5),
            child: Align(
              alignment: Alignment.bottomLeft,
              child: ConstrainedBox(
                // On desktop the display title holds to a poster-adjacent
                // measure instead of stretching across the whole plate.
                constraints: BoxConstraints(
                  maxWidth:
                      expandedLayout ? contentWidth * 0.6 : double.infinity,
                ),
                // The hero title is display type inside a fixed-extent
                // header; clamp its scaling and give body text below the
                // full range instead.
                child: MediaQuery.withClampedTextScaling(
                  maxScaleFactor: 1.3,
                  child: Semantics(
                    identifier: 'media-detail-title',
                    label: e2eWebSemanticsEnabled ? title : null,
                    header: true,
                    excludeSemantics: e2eWebSemanticsEnabled,
                    child: Text(
                      title,
                      style:
                          Theme.of(context).textTheme.displaySmall?.copyWith(
                        color: AppTheme.textPrimary,
                        fontSize: expandedLayout ? 56 : mobileFontSize,
                        height: 1.02,
                        fontWeight: FontWeight.w800,
                        letterSpacing: expandedLayout ? -1.5 : -1.0,
                        shadows: const [
                          Shadow(color: Colors.black, blurRadius: 16),
                        ],
                      ),
                      maxLines:
                          expandedLayout ? 2 : (textScale > 1.3 ? 4 : 3),
                      overflow: TextOverflow.ellipsis,
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      ],
    );

    block = Transform.translate(offset: Offset(0, shift), child: block);
    // Permanently mounted so the poster/title subtree keeps its element state
    // across the fade threshold (Opacity at 1.0 allocates no layer), with the
    // semantics node alive through the fade so screen readers and the e2e
    // layer always see exactly one title header.
    return IgnorePointer(
      ignoring: opacity < 1,
      child: Opacity(
        opacity: opacity,
        alwaysIncludeSemantics: true,
        child: block,
      ),
    );
  }
}

/// Hairline + amber tick marking the hero/content seam. The tick draws in
/// once on first load.
class _SeamRule extends StatefulWidget {
  final bool disableAnimations;

  const _SeamRule({required this.disableAnimations});

  @override
  State<_SeamRule> createState() => _SeamRuleState();
}

class _SeamRuleState extends State<_SeamRule>
    with SingleTickerProviderStateMixin {
  late final AnimationController _draw = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 420),
  );

  @override
  void initState() {
    super.initState();
    if (widget.disableAnimations) {
      _draw.value = 1;
    } else {
      _draw.forward();
    }
  }

  @override
  void dispose() {
    _draw.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 2,
      child: Stack(
        children: [
          const Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: SizedBox(
              height: 1,
              child: ColoredBox(color: AppTheme.border),
            ),
          ),
          Positioned(
            left: 0,
            bottom: 0,
            child: AnimatedBuilder(
              animation: _draw,
              builder: (context, _) => Transform.scale(
                scaleX: Curves.easeOutCubic.transform(_draw.value),
                alignment: Alignment.centerLeft,
                child: const SizedBox(
                  width: 48,
                  height: 2,
                  child: ColoredBox(color: AppTheme.accent),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// The pinned toolbar strip: a bare back chip over the open hero that
/// surfaces into a frosted marquee bar as the header collapses.
class _MarqueeBar extends StatelessWidget {
  final String title;
  final double topPadding;
  final double pad;
  final double surfaceIn;
  final double titleIn;
  final double hairlineIn;
  final double backPillAlpha;
  final VoidCallback onBack;

  const _MarqueeBar({
    required this.title,
    required this.topPadding,
    required this.pad,
    required this.surfaceIn,
    required this.titleIn,
    required this.hairlineIn,
    required this.backPillAlpha,
    required this.onBack,
  });

  @override
  Widget build(BuildContext context) {
    // The open hero never pays for the bar: surface, blur, and title are only
    // mounted once the collapse is well underway.
    final mounted = surfaceIn > 0;

    Widget? surface;
    if (mounted) {
      final fill = ColoredBox(
        color: AppTheme.surface.withValues(
          alpha: (kIsWeb ? 0.92 : 0.72) * surfaceIn,
        ),
      );
      // BackdropFilter is expensive on the web renderer — the opaque fill
      // covers it there; native gets the frosted glass.
      surface = kIsWeb
          ? fill
          : RepaintBoundary(
              child: ClipRect(
                child: BackdropFilter(
                  filter: ui.ImageFilter.blur(sigmaX: 18, sigmaY: 18),
                  child: fill,
                ),
              ),
            );
    }

    return Stack(
      fit: StackFit.expand,
      children: [
        if (surface != null) IgnorePointer(child: surface),
        // Bottom hairline quoting the seam rule — brand continuity between
        // the expanded and collapsed states.
        if (hairlineIn > 0)
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: IgnorePointer(
              child: Opacity(
                opacity: hairlineIn,
                child: SizedBox(
                  height: 2,
                  child: Stack(
                    children: [
                      const Positioned(
                        left: 0,
                        right: 0,
                        bottom: 0,
                        child: SizedBox(
                          height: 1,
                          child: ColoredBox(color: AppTheme.border),
                        ),
                      ),
                      Positioned(
                        left: pad,
                        bottom: 0,
                        child: const SizedBox(
                          width: 48,
                          height: 2,
                          child: ColoredBox(color: AppTheme.accent),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        Padding(
          padding: EdgeInsets.only(top: topPadding, left: pad, right: pad),
          child: Row(
            children: [
              DecoratedBox(
                decoration: BoxDecoration(
                  color: Colors.black.withValues(alpha: backPillAlpha),
                  shape: BoxShape.circle,
                ),
                child: IconButton(
                  icon: const Icon(Icons.arrow_back),
                  color: AppTheme.textPrimary,
                  tooltip: 'Back',
                  onPressed: onBack,
                ),
              ),
              const SizedBox(width: 12),
              if (mounted && titleIn > 0)
                Expanded(
                  child: Opacity(
                    opacity: titleIn,
                    child: Transform.translate(
                      offset: Offset(0, 8 * (1 - titleIn)),
                      // The hero title owns the semantics; the bar copy is
                      // purely visual. Clamped like the hero title — the bar
                      // is a fixed 64px strip.
                      child: ExcludeSemantics(
                        child: MediaQuery.withClampedTextScaling(
                          maxScaleFactor: 1.3,
                          child: Text(
                            title,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(
                              color: AppTheme.textPrimary,
                              fontSize: 19,
                              height: 1.2,
                              fontWeight: FontWeight.w600,
                              letterSpacing: -0.2,
                            ),
                          ),
                        ),
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
      ],
    );
  }
}
