import 'package:flutter/material.dart';

import '../theme/app_theme.dart';

/// Consistent section heading for discovery shelves and operational groups.
///
/// The small signal mark gives long scrolling pages a stable visual rhythm
/// without turning every section into another card.
class SectionHeader extends StatelessWidget {
  final String title;
  final String? eyebrow;
  final Widget? trailing;

  const SectionHeader({
    super.key,
    required this.title,
    this.eyebrow,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    final textTheme = Theme.of(context).textTheme;

    return Row(
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        Container(
          width: 4,
          height: eyebrow == null ? 24 : 38,
          decoration: BoxDecoration(
            gradient: const LinearGradient(
              begin: Alignment.topCenter,
              end: Alignment.bottomCenter,
              colors: [AppTheme.accent, AppTheme.signal],
            ),
            borderRadius: BorderRadius.circular(99),
            boxShadow: [
              BoxShadow(
                color: AppTheme.accent.withValues(alpha: 0.18),
                blurRadius: 10,
              ),
            ],
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              if (eyebrow != null)
                Text(
                  eyebrow!.toUpperCase(),
                  style: textTheme.labelSmall?.copyWith(
                    color: AppTheme.textMuted,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 1.25,
                  ),
                ),
              Text(
                title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: textTheme.headlineSmall?.copyWith(
                  color: AppTheme.textPrimary,
                  fontWeight: FontWeight.w700,
                  letterSpacing: -0.45,
                ),
              ),
            ],
          ),
        ),
        if (trailing != null) ...[
          const SizedBox(width: 12),
          trailing!,
        ],
      ],
    );
  }
}
