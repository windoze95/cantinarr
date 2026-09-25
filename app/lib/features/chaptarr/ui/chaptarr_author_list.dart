import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/library_collection.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/chaptarr_models.dart';

/// List or artwork grid of Chaptarr authors
/// with explicit actions and progress indicators.
/// Mirrors [SonarrSeriesList] adapted to the author-centric book library.
class ChaptarrAuthorList extends StatelessWidget {
  final List<ChaptarrAuthor> authors;
  final void Function(ChaptarrAuthor) onTap;
  final void Function(ChaptarrAuthor)? onSearch;
  final void Function(ChaptarrAuthor, {bool deleteFiles})? onDelete;
  final bool embedded;
  final LibraryViewMode viewMode;
  final String scrollKey;
  final ImageSource? Function(ChaptarrAuthor)? imageSourceFor;

  const ChaptarrAuthorList({
    super.key,
    required this.authors,
    required this.onTap,
    this.onSearch,
    this.onDelete,
    this.embedded = false,
    this.viewMode = LibraryViewMode.list,
    this.scrollKey = 'chaptarr-library',
    this.imageSourceFor,
  });

  @override
  Widget build(BuildContext context) {
    return LibraryCollection(
      viewMode: viewMode,
      scrollKey: scrollKey,
      embedded: embedded,
      emptyMessage: 'No authors found',
      itemCount: authors.length,
      itemBuilder: (context, index) {
        final author = authors[index];
        return _AuthorTile(
          key: ValueKey(author.id),
          grid: viewMode == LibraryViewMode.grid,
          image: imageSourceFor == null
              ? (url: author.coverUrl ?? '', headers: null)
              : imageSourceFor!(author),
          author: author,
          onTap: () => onTap(author),
          onSearch: onSearch != null ? () => onSearch!(author) : null,
          onDelete: onDelete == null
              ? null
              : () async {
                  final deleteFiles =
                      await _confirmDelete(context, author.authorName);
                  if (deleteFiles == null) return;
                  onDelete!(author, deleteFiles: deleteFiles);
                },
        );
      },
    );
  }

  /// Delete confirmation with an opt-in "also delete files" choice.
  /// Resolves to the delete-files flag, or null when cancelled.
  Future<bool?> _confirmDelete(BuildContext context, String name) {
    var deleteFiles = false;
    return showDialog<bool>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) => AlertDialog(
          backgroundColor: AppTheme.surface,
          title: const Text('Delete Author'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Remove "$name" from Chaptarr?'),
              const SizedBox(height: 8),
              CheckboxListTile(
                value: deleteFiles,
                onChanged: (v) => setState(() => deleteFiles = v ?? false),
                title: const Text('Also delete files from disk',
                    style: TextStyle(fontSize: 14)),
                contentPadding: EdgeInsets.zero,
                controlAffinity: ListTileControlAffinity.leading,
                activeColor: AppTheme.error,
              ),
            ],
          ),
          actions: [
            TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('Cancel')),
            TextButton(
              onPressed: () => Navigator.pop(ctx, deleteFiles),
              style: TextButton.styleFrom(foregroundColor: AppTheme.error),
              child: const Text('Delete'),
            ),
          ],
        ),
      ),
    );
  }
}

class _AuthorTile extends StatelessWidget {
  final bool grid;
  final ImageSource? image;
  final ChaptarrAuthor author;
  final VoidCallback onTap;
  final VoidCallback? onSearch;
  final VoidCallback? onDelete;

  const _AuthorTile({
    super.key,
    required this.grid,
    this.image,
    required this.author,
    required this.onTap,
    this.onSearch,
    this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    final stats = author.statistics;
    final percent = author.percentComplete;

    return LibraryItem(
      grid: grid,
      name: author.authorName,
      onTap: onTap,
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
              Container(
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
          if (stats != null && stats.bookCount > 0) ...[
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
      actions: onSearch != null || onDelete != null
          ? PopupMenuButton<String>(
              icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
              color: AppTheme.surfaceVariant,
              tooltip: 'Actions for ${author.authorName}',
              onSelected: (value) {
                switch (value) {
                  case 'search':
                    onSearch?.call();
                  case 'remove':
                    onDelete?.call();
                }
              },
              itemBuilder: (_) => [
                if (onSearch != null)
                  const PopupMenuItem(
                    value: 'search',
                    child: Row(
                      children: [
                        Icon(Icons.search,
                            size: 18, color: AppTheme.textSecondary),
                        SizedBox(width: 10),
                        Flexible(child: Text('Find books automatically')),
                      ],
                    ),
                  ),
                if (onSearch != null && onDelete != null)
                  const PopupMenuDivider(),
                if (onDelete != null)
                  const PopupMenuItem(
                    value: 'remove',
                    child: Row(
                      children: [
                        Icon(Icons.delete_outline,
                            size: 18, color: AppTheme.error),
                        SizedBox(width: 10),
                        Text('Remove…',
                            style: TextStyle(color: AppTheme.error)),
                      ],
                    ),
                  ),
              ],
            )
          : null,
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
