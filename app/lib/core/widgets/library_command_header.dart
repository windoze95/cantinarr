import 'package:flutter/material.dart';

import '../theme/app_theme.dart';
import '../storage/library_view_preferences.dart';
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

/// Shared command header for the four admin libraries.
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
  final LibraryViewMode viewMode;
  final ValueChanged<LibraryViewMode> onViewModeChanged;

  const LibraryCommandHeader({
    super.key,
    required this.title,
    required this.subtitle,
    required this.stats,
    required this.searchController,
    required this.onSearch,
    required this.searchHint,
    required this.filter,
    required this.viewMode,
    required this.onViewModeChanged,
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
          final viewControl = SegmentedButton<LibraryViewMode>(
            showSelectedIcon: false,
            style: wide ? null : SegmentedButton.styleFrom(
              padding: EdgeInsets.zero,
            ),
            segments: [
              ButtonSegment(value: LibraryViewMode.list,
                  icon: const Icon(Icons.view_list_rounded),
                  label: wide ? const Text('List') : null,
                  tooltip: 'List view'),
              ButtonSegment(value: LibraryViewMode.grid,
                  icon: const Icon(Icons.grid_view_rounded),
                  label: wide ? const Text('Grid') : null,
                  tooltip: 'Grid view'),
            ],
            selected: {viewMode},
            onSelectionChanged: (selection) =>
                onViewModeChanged(selection.single),
          );
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
          final metrics = Wrap(
            spacing: 7,
            runSpacing: 7,
            alignment: wide ? WrapAlignment.end : WrapAlignment.spaceBetween,
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
                        autocorrect: false,
                        textInputAction: TextInputAction.search,
                        decoration: InputDecoration(
                          hintText: searchHint,
                          prefixIcon: wide
                              ? const Icon(Icons.filter_alt_outlined) : null,
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
                  SizedBox(width: wide ? 8 : 6),
                  Container(
                    width: wide ? 52 : 48,
                    height: wide ? 52 : 48,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceRaised,
                      borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
                      border: Border.all(color: AppTheme.border),
                    ),
                    child: filter,
                  ),
                  SizedBox(width: wide ? 12 : 6),
                  SizedBox(width: wide ? null : 96, child: viewControl),
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
