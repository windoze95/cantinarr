import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';

/// How much of a season Sonarr is actually watching.
enum MonitorFill {
  /// Neither the season nor any episode in it is monitored — nothing in it is
  /// being searched for.
  none,

  /// Part of the season is monitored and part is not: episodes left out of a
  /// monitored season, or episodes monitored on their own inside a season that
  /// is not monitored.
  partial,

  /// The season and every episode in it are monitored.
  full,
}

/// The monitoring bookmark on a season card. A hollow grey bookmark is a
/// season nobody is watching and a solid accent one is a season watched whole;
/// [MonitorFill.partial] fills the bottom half of that same bookmark, so a
/// season that is only partly watched — either way round — reads as "some of
/// this, but not all of it" at a glance instead of looking identical to a
/// whole one or to an empty one.
class MonitorBookmark extends StatelessWidget {
  final MonitorFill fill;
  final bool enabled;

  const MonitorBookmark(this.fill, {super.key, this.enabled = true});

  @override
  Widget build(BuildContext context) {
    final color = enabled ? AppTheme.accent : AppTheme.textSecondary;
    switch (fill) {
      case MonitorFill.none:
        return const Icon(Icons.bookmark_border, color: AppTheme.textSecondary);
      case MonitorFill.full:
        return Icon(Icons.bookmark, color: color);
      case MonitorFill.partial:
        // The filled and outlined bookmarks are the same glyph, so clipping
        // one over the other fills the outline exactly.
        return Stack(
          alignment: Alignment.center,
          children: [
            Icon(Icons.bookmark_border, color: color),
            ClipRect(
              clipper: const _BottomHalf(),
              child: Icon(Icons.bookmark, color: color),
            ),
          ],
        );
    }
  }
}

class _BottomHalf extends CustomClipper<Rect> {
  const _BottomHalf();

  @override
  Rect getClip(Size size) =>
      Rect.fromLTRB(0, size.height / 2, size.width, size.height);

  @override
  bool shouldReclip(covariant CustomClipper<Rect> oldClipper) => false;
}
