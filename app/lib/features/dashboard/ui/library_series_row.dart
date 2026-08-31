import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/section_header.dart';
import '../../../core/widgets/section_sort_menu.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../data/book_series_service.dart';

/// The Books tab's "Series" row: which series the library actually holds books
/// of, and how complete each one is.
///
/// It answers the question the Authors row raises but cannot: an author card
/// says you hold 6 of their 61 books, this one says which run those 6 belong to
/// and how much of it is missing.
///
/// Cards are poster-shaped rather than circular like the Authors row — a series
/// is recognised by its first book's cover, not by a face.
class LibrarySeriesRow extends ConsumerWidget {
  const LibrarySeriesRow({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    if (instanceId == null) return const SizedBox.shrink();

    final seriesAsync = ref.watch(bookSeriesProvider);
    final page = seriesAsync.valueOrNull;
    final series = page?.series ?? const <LibrarySeries>[];
    // Same rule as the other book rows: nothing to show, no access, or an
    // unreadable library all look the same — no row. Shows nothing while it
    // loads rather than a shelf it may be about to withdraw.
    if (seriesAsync.hasError || series.isEmpty) {
      return const SizedBox.shrink();
    }

    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: viewportWidth >= 900 ? 24 : 16,
            ),
            child: SectionHeader(
              title: 'Series',
              trailing: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SectionTruncationNote(
                    shown: series.length,
                    total: page?.total ?? series.length,
                  ),
                  SectionSortMenu<SeriesSort>(
                    tooltip: 'Sort series',
                    options: SeriesSort.values,
                    selected: ref.watch(bookSeriesSortProvider),
                    labelOf: (option) => option.label,
                    onSelected: (next) =>
                        ref.read(bookSeriesSortProvider.notifier).state = next,
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<LibrarySeries>(
            items: series,
            isLoading: false,
            // Taller than the Recently Added row by one text line: the count
            // is allowed to wrap, because "9 of 41 books available" cut to
            // "9 of 41 books avail…" is the half that carried the meaning.
            height: cardWidth * 1.5 + 82,
            itemBuilder: (entry) => SeriesStackCard(
              series: entry,
              width: cardWidth,
              covers: [
                for (final path in entry.covers)
                  chaptarrImageSource(ref, path, instanceId),
              ].whereType<ChaptarrImageSource>().toList(),
              onTap: () => context.push(
                '/detail/series/${Uri.encodeComponent(entry.name)}'
                '?instance_id=${Uri.encodeQueryComponent(instanceId)}',
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// One series in the browse row: its earliest covers stacked, the series name,
/// and how complete the run is.
///
/// The stack is the point — a single cover is indistinguishable from a book
/// card, and this row sits directly beneath one that shows exactly that. Covers
/// step to the right and shrink slightly, so the leftmost (earliest) book stays
/// fully readable and the ones behind read as the rest of the run.
class SeriesStackCard extends StatelessWidget {
  final LibrarySeries series;
  final double width;
  final List<ChaptarrImageSource> covers;
  final VoidCallback? onTap;

  const SeriesStackCard({
    super.key,
    required this.series,
    required this.width,
    required this.covers,
    this.onTap,
  });

  /// How far each cover behind the front one steps right, as a fraction of the
  /// front cover's width.
  static const _step = 0.16;

  /// How much shorter each cover behind the front one is. Small on purpose:
  /// most of each is hidden, and the visible sliver should read as another
  /// book of the same size, not a shrinking echo.
  static const _shrink = 0.06;

  /// The stack's depth, and therefore the width it reserves, is fixed rather
  /// than following each series' cover count — otherwise a one-cover series
  /// would sit in a narrower card and the row would come out ragged.
  static const _depth = 3;

  /// The full width a card occupies for a given cover width. The front cover
  /// keeps its full size and the stack grows to the right, so a series' art is
  /// exactly as large as a book's in the row above.
  static double totalWidth(double coverWidth) =>
      coverWidth * (1 + (_step - _shrink) * (_depth - 1));

  @override
  Widget build(BuildContext context) {
    final count = series.countLabel;
    final coverHeight = width * 1.5;
    return SizedBox(
      width: totalWidth(width),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: totalWidth(width),
              height: coverHeight,
              child: _stack(coverHeight),
            ),
            const SizedBox(height: 9),
            Text(
              series.name,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 12.5,
                height: 1.22,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 2),
            if (count.isNotEmpty)
              Text(
                count,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                  color: AppTheme.textMuted,
                  fontSize: 11,
                  height: 1.25,
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _stack(double coverHeight) {
    // A series the library has no art for still draws one frame, so the row's
    // cards stay the same size whatever their covers.
    final drawn = covers.isEmpty ? 1 : covers.length.clamp(1, _depth);
    final step = width * _step;

    return Stack(
      clipBehavior: Clip.none,
      children: [
        // Back to front, so the earliest book ends up on top.
        for (var i = drawn - 1; i >= 0; i--)
          _layer(i, step, coverHeight),
      ],
    );
  }

  Widget _layer(int i, double step, double coverHeight) {
    // Each cover keeps a book's proportions — deriving the height from the
    // width, rather than fixing both, is what stops the front cover being
    // cropped down its sides.
    final coverWidth = width * (1 - _shrink * i);
    final height = coverWidth * 1.5;
    return Positioned(
      left: step * i,
      top: (coverHeight - height) / 2,
      width: coverWidth,
      height: height,
      child: Opacity(
        // Barely dimmed, and deliberately so. Heavier dimming was tried first
        // and made the stack vanish: what is visible of each book behind is a
        // ~12px sliver, and a dimmed sliver against this theme's near-black
        // background reads as nothing at all. Real cover art in that sliver is
        // what makes the card read as a run of books.
        opacity: i == 0 ? 1 : (i == 1 ? 0.88 : 0.76),
        child: DecoratedBox(
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
            boxShadow: const [
              BoxShadow(color: Color(0x99000000), blurRadius: 5),
            ],
          ),
          child: ClipRRect(
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
            child: CachedImage(
              url: i < covers.length ? covers[i].url : null,
              headers: i < covers.length ? covers[i].headers : null,
              icon: Icons.auto_stories,
              iconSize: 22,
            ),
          ),
        ),
      ),
    );
  }
}
