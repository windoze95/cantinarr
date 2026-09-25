import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/library_collection.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/library_actions.dart';
import '../data/chaptarr_models.dart';

/// Library tiles share their popup and long-press actions.
class ChaptarrAuthorList extends StatelessWidget {
  final List<ChaptarrAuthor> authors;
  final void Function(ChaptarrAuthor) onTap;
  final void Function(ChaptarrAuthor, LibraryAction)? onAction;
  final bool embedded;
  final LibraryViewMode viewMode;
  final String scrollKey;
  final ImageSource? Function(ChaptarrAuthor)? imageSourceFor;

  const ChaptarrAuthorList({
    super.key,
    required this.authors,
    required this.onTap,
    this.onAction,
    this.embedded = false,
    this.viewMode = LibraryViewMode.list,
    this.scrollKey = 'chaptarr-library',
    this.imageSourceFor,
  });

  @override
  Widget build(BuildContext context) => LibraryCollection(
    viewMode: viewMode, scrollKey: scrollKey, embedded: embedded,
    emptyMessage: 'No authors found', itemCount: authors.length,
    itemBuilder: (context, index) {
      final author = authors[index];
      return _AuthorTile(
        key: ValueKey(author.id), grid: viewMode == LibraryViewMode.grid,
        author: author,
        onTap: () => onTap(author),
        onAction: onAction == null ? null : (action) => onAction!(author, action),
        image: imageSourceFor == null
            ? (url: author.coverUrl ?? '', headers: null) : imageSourceFor!(author),
      );
    },
  );
}

class _AuthorTile extends StatelessWidget {
  final bool grid;
  final ChaptarrAuthor author;
  final VoidCallback onTap;
  final ValueChanged<LibraryAction>? onAction;
  final ImageSource? image;
  const _AuthorTile({super.key, required this.grid, required this.author,
    required this.onTap, this.onAction, this.image});

  @override
  Widget build(BuildContext context) {
    final stats = author.statistics;
    final percent = author.percentComplete;

    return LibraryItem(
      grid: grid,
      name: author.authorName,
      onTap: onTap,
      onLongPress: onAction == null ? null : () => showLibraryActionMenu(
        context, title: author.authorName, actions: libraryActions('author'), onSelected: onAction!),
      artwork: CachedImage(
        url: image?.url,
        headers: image?.headers,
        fit: BoxFit.cover,
        icon: Icons.person,
      ),
      details: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Wrap(
            spacing: 6,
            runSpacing: 4,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              if (!grid) Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: _statusColor.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  _statusText,
                  style: TextStyle(
                    color: _statusColor,
                    fontSize: 11,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
              if (stats != null) ...[
                Text(
                  author.bookCountLabel,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 11),
                ),
              ],
            ],
          ),
          if (!grid && stats != null && stats.bookCount > 0) ...[
            const SizedBox(height: 6),
            ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: percent,
                backgroundColor: AppTheme.surfaceVariant,
                valueColor: AlwaysStoppedAnimation(_progressColor),
                minHeight: 4,
              ),
            ),
          ],
        ],
      ),
      actions: onAction == null ? null : LibraryActionMenu(
        title: author.authorName, actions: libraryActions('author'), onSelected: onAction!),
    );
  }

  Color get _statusColor => switch (author.status) {
        'continuing' => AppTheme.downloading,
        'ended' => AppTheme.textSecondary,
        _ => AppTheme.requested,
      };

  String get _statusText => switch (author.status) {
        'continuing' => 'Continuing',
        'ended' => 'Ended',
        _ => 'Unknown',
      };

  /// Mirrors the Sonarr tile's progress grammar: green only when an ended
  /// author's monitored books are all on disk, info/ember when merely caught up,
  /// red/amber for monitored/unmonitored gaps.
  Color get _progressColor {
    if (author.percentComplete >= 1.0) {
      return author.status == 'ended'
          ? AppTheme.available
          : AppTheme.downloading;
    }
    return author.monitored ? AppTheme.error : AppTheme.requested;
  }
}
