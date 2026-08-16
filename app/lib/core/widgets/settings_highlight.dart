import 'dart:async';

import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// Scrolls its child into view and flashes a fading accent ring when a
/// settings-search deep link (`?highlight=<anchorId>`) targets it.
///
/// Wrap an anchorable control (a `SwitchListTile`, `ListTile`, or section
/// widget that is a direct child of the screen's `ListView`) and pass the
/// screen's active `highlightId`. When the two match, the wrapper waits for
/// its first frame, calls [Scrollable.ensureVisible] on itself, and plays a
/// one-shot highlight that decays over ~1.6s. With animations disabled it
/// jumps instead and shows the ring statically for two seconds.
///
/// When inactive the wrapper paints nothing and adds no layout — a wrapped
/// child is pixel-identical to a bare one.
///
/// The trigger runs when this widget mounts, so on screens that load content
/// asynchronously it fires whenever the body actually appears. Anchors must
/// only be placed below content that is present at body-mount; a section that
/// streams in later would shift an already-performed scroll.
///
/// A `ListView` inflates children lazily, so an anchor far below the fold
/// would never mount and never fire. Screens make the anchored child real on
/// arrival by widening the list's cache extent while a highlight is
/// requested:
///
/// ```dart
/// ListView(
///   cacheExtent: SettingsHighlight.cacheExtentFor(widget.highlightId),
///   ...
/// )
/// ```
class SettingsHighlight extends StatefulWidget {
  /// Stable anchor id for the wrapped control (see `SettingsAnchors`).
  final String anchorId;

  /// The screen's active deep-link target, usually from `?highlight=`.
  /// `null` (or any other id) leaves this wrapper inert.
  final String? highlightId;

  final Widget child;

  const SettingsHighlight({
    super.key,
    required this.anchorId,
    required this.highlightId,
    required this.child,
  });

  /// Far beyond any settings list, but finite: an infinite cache extent
  /// produces non-finite semantics geometry.
  static const double _buildAllCacheExtent = 100000;

  /// Cache extent for a settings list that hosts anchors: wide enough to
  /// build every child while [highlightId] is active (so the anchored
  /// control mounts and can fire), the framework default otherwise.
  static double? cacheExtentFor(String? highlightId) =>
      highlightId != null ? _buildAllCacheExtent : null;

  @override
  State<SettingsHighlight> createState() => _SettingsHighlightState();
}

class _SettingsHighlightState extends State<SettingsHighlight>
    with SingleTickerProviderStateMixin {
  // At rest the controller sits fully faded out (strength 0), so the first
  // build paints nothing without any status juggling.
  late final AnimationController _fade = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 1600),
    value: 1.0,
  );

  /// One-shot latch: in-page rebuilds never re-trigger. A genuine new target
  /// (didUpdateWidget with a different highlightId) re-arms it.
  bool _fired = false;
  Timer? _clearTimer;

  @override
  void initState() {
    super.initState();
    _armIfMatched();
  }

  @override
  void didUpdateWidget(SettingsHighlight oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.highlightId != widget.highlightId) {
      _fired = false;
      _armIfMatched();
    }
  }

  void _armIfMatched() {
    if (_fired ||
        widget.highlightId == null ||
        widget.highlightId != widget.anchorId) {
      return;
    }
    _fired = true;
    WidgetsBinding.instance.addPostFrameCallback((_) => _trigger());
  }

  void _trigger() {
    if (!mounted) return;
    final reduceMotion = MediaQuery.disableAnimationsOf(context);
    Scrollable.ensureVisible(
      context,
      duration: reduceMotion ? Duration.zero : AppTheme.motionSlow,
      curve: Curves.easeInOut,
      alignment: 0.1,
    );
    if (reduceMotion) {
      // The ring is informative, not decorative: show it statically and
      // clear it after a beat instead of animating.
      _fade.value = 0.0;
      _clearTimer?.cancel();
      _clearTimer = Timer(const Duration(seconds: 2), () {
        if (mounted) _fade.value = 1.0;
      });
    } else {
      _fade.forward(from: 0.0);
    }
  }

  @override
  void dispose() {
    _clearTimer?.cancel();
    _fade.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AnimatedBuilder(
      animation: _fade,
      child: widget.child,
      builder: (context, child) {
        // Bright arrival, gentle decay: full strength the instant the flash
        // starts, easing out to nothing.
        final strength =
            (1.0 - Curves.easeOutCubic.transform(_fade.value)).clamp(0.0, 1.0);
        return DecoratedBox(
          position: DecorationPosition.foreground,
          decoration: strength == 0
              ? const BoxDecoration()
              : BoxDecoration(
                  color: AppTheme.accent.withValues(alpha: 0.08 * strength),
                  borderRadius: BorderRadius.circular(AppTheme.radiusMd),
                  border: Border.all(
                    color: AppTheme.accent.withValues(alpha: 0.9 * strength),
                    width: 1.5,
                  ),
                ),
          child: child!,
        );
      },
    );
  }
}
