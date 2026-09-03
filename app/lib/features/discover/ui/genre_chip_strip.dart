import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/section_header.dart';
import '../data/tmdb_models.dart';
import '../logic/browse_query.dart';

/// A row of genre chips on a discovery tab, each opening the Browse page
/// filtered to that genre. Renders nothing until the genres are known.
class GenreChipStrip extends StatelessWidget {
  const GenreChipStrip({
    super.key,
    required this.genres,
    required this.mediaType,
  });

  final List<Genre> genres;
  final MediaType mediaType;

  @override
  Widget build(BuildContext context) {
    if (genres.isEmpty) return const SizedBox.shrink();
    final horizontalPadding = MediaQuery.sizeOf(context).width >= 900 ? 24.0 : 16.0;
    final everything = mediaType == MediaType.tv ? 'All shows' : 'All movies';

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Padding(
            padding: EdgeInsets.symmetric(horizontal: horizontalPadding),
            child: const SectionHeader(title: 'Browse by genre'),
          ),
          const SizedBox(height: 12),
          SizedBox(
            height: 36,
            child: ListView(
              scrollDirection: Axis.horizontal,
              padding: EdgeInsets.symmetric(horizontal: horizontalPadding),
              children: [
                _chip(context, everything, const BrowseFilters()),
                for (final genre in genres) ...[
                  const SizedBox(width: 8),
                  _chip(
                    context,
                    genre.name,
                    BrowseFilters(genreIds: [genre.id]),
                    title: genre.name,
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _chip(
    BuildContext context,
    String label,
    BrowseFilters filters, {
    String? title,
  }) {
    return ActionChip(
      label: Text(label, style: const TextStyle(fontSize: 12)),
      labelStyle: const TextStyle(color: AppTheme.textPrimary),
      backgroundColor: AppTheme.surfaceVariant,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(20),
        side: const BorderSide(color: AppTheme.border),
      ),
      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
      visualDensity: VisualDensity.compact,
      onPressed: () => context.push(
        BrowseQuery(
          type: mediaType,
          feed: BrowseFeed.discover,
          title: title,
          filters: filters,
        ).toLocation(),
      ),
    );
  }
}
