import 'package:flutter/material.dart';

import '../theme/app_theme.dart';
import 'app_panel.dart';

class LibraryStat {
  final String label;
  final int value;
  final Color color;

  const LibraryStat({
    required this.label,
    required this.value,
    required this.color,
  });
}

/// Shared command header for the three admin library workbenches.
///
/// It gives every library a visible identity, separates local filtering from
/// global discovery search, and keeps operational counts readable at all
/// widths.
class LibraryCommandHeader extends StatelessWidget {
  final String title;
  final String subtitle;
  final List<LibraryStat> stats;
  final TextEditingController searchController;
  final ValueChanged<String> onSearch;
  final String searchHint;
  final Widget filter;

  const LibraryCommandHeader({
    super.key,
    required this.title,
    required this.subtitle,
    required this.stats,
    required this.searchController,
    required this.onSearch,
    required this.searchHint,
    required this.filter,
  });

  @override
  Widget build(BuildContext context) {
    return AppPanel(
      margin: const EdgeInsets.fromLTRB(12, 10, 12, 8),
      padding: const EdgeInsets.all(16),
      accentColor: AppTheme.signal,
      child: LayoutBuilder(
        builder: (context, constraints) {
          final wide = constraints.maxWidth >= 620;
          final identity = Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                title,
                style: Theme.of(context).textTheme.headlineSmall?.copyWith(
                      color: AppTheme.textPrimary,
                      fontWeight: FontWeight.w800,
                      letterSpacing: -0.4,
                    ),
              ),
              const SizedBox(height: 3),
              Text(
                subtitle.toUpperCase(),
                style: Theme.of(context).textTheme.labelSmall?.copyWith(
                      color: AppTheme.signal,
                      fontWeight: FontWeight.w800,
                      letterSpacing: 1.1,
                    ),
              ),
            ],
          );
          final metrics = Row(
            mainAxisSize: wide ? MainAxisSize.min : MainAxisSize.max,
            mainAxisAlignment: MainAxisAlignment.spaceBetween,
            children: [
              for (final stat in stats) _Metric(stat: stat),
            ],
          );

          return Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (wide)
                Row(
                  crossAxisAlignment: CrossAxisAlignment.center,
                  children: [
                    Expanded(child: identity),
                    metrics,
                  ],
                )
              else ...[
                identity,
                const SizedBox(height: 15),
                metrics,
              ],
              const SizedBox(height: 15),
              Row(
                children: [
                  Expanded(
                    child: ListenableBuilder(
                      listenable: searchController,
                      builder: (context, _) => TextField(
                        controller: searchController,
                        onChanged: onSearch,
                        textInputAction: TextInputAction.search,
                        decoration: InputDecoration(
                          hintText: searchHint,
                          prefixIcon: const Icon(Icons.filter_alt_outlined),
                          suffixIcon: searchController.text.isEmpty
                              ? null
                              : IconButton(
                                  tooltip: 'Clear library filter',
                                  icon: const Icon(Icons.close_rounded),
                                  onPressed: () {
                                    searchController.clear();
                                    onSearch('');
                                  },
                                ),
                        ),
                      ),
                    ),
                  ),
                  const SizedBox(width: 8),
                  Container(
                    width: 52,
                    height: 52,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceRaised,
                      borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
                      border: Border.all(color: AppTheme.border),
                    ),
                    child: filter,
                  ),
                ],
              ),
            ],
          );
        },
      ),
    );
  }
}

class _Metric extends StatelessWidget {
  final LibraryStat stat;

  const _Metric({required this.stat});

  @override
  Widget build(BuildContext context) {
    return Container(
      constraints: const BoxConstraints(minWidth: 76),
      margin: const EdgeInsets.only(left: 7),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
      decoration: BoxDecoration(
        color: stat.color.withValues(alpha: 0.075),
        borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
        border: Border.all(color: stat.color.withValues(alpha: 0.16)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            '${stat.value}',
            style: TextStyle(
              color: stat.color,
              fontSize: 20,
              height: 1,
              fontWeight: FontWeight.w800,
              fontFeatures: const [FontFeature.tabularFigures()],
            ),
          ),
          const SizedBox(height: 4),
          Text(
            stat.label.toUpperCase(),
            style: const TextStyle(
              color: AppTheme.textMuted,
              fontSize: 9.5,
              fontWeight: FontWeight.w700,
              letterSpacing: 0.55,
            ),
          ),
        ],
      ),
    );
  }
}
