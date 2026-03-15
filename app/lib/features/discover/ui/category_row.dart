import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../data/tmdb_models.dart';

/// A titled horizontal row of media items (e.g. "Trending Now").
class CategoryRow extends StatelessWidget {
  final String title;
  final List<MediaItem> items;
  final bool isLoading;
  final void Function(MediaItem)? onLoadMore;

  const CategoryRow({
    super.key,
    required this.title,
    required this.items,
    required this.isLoading,
    this.onLoadMore,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Text(
              title,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 20,
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<MediaItem>(
            items: items,
            isLoading: isLoading,
            onItemAppear: onLoadMore,
            itemBuilder: (item) => MediaCard(
              id: item.id,
              title: item.title,
              posterPath: item.posterPath,
              width: 100,
              onTap: () => context.push(
                '/detail/${item.mediaType.name}/${item.id}',
              ),
            ),
          ),
        ],
      ),
    );
  }
}
