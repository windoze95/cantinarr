import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/chaptarr_models.dart';

/// List of Chaptarr authors with swipe-to-delete and progress indicators.
/// Mirrors [SonarrSeriesList] adapted to the author-centric book library.
class ChaptarrAuthorList extends StatelessWidget {
  final List<ChaptarrAuthor> authors;
  final void Function(ChaptarrAuthor) onTap;
  final void Function(ChaptarrAuthor)? onSearch;
  final void Function(ChaptarrAuthor)? onDelete;
  final bool embedded;

  const ChaptarrAuthorList({
    super.key,
    required this.authors,
    required this.onTap,
    this.onSearch,
    this.onDelete,
    this.embedded = false,
  });

  @override
  Widget build(BuildContext context) {
    if (authors.isEmpty) {
      return const Center(
        child: Text('No authors found',
            style: TextStyle(color: AppTheme.textSecondary)),
      );
    }

    return ListView.separated(
      shrinkWrap: embedded,
      physics: embedded ? const NeverScrollableScrollPhysics() : null,
      itemCount: authors.length,
      separatorBuilder: (_, __) =>
          const Divider(color: AppTheme.border, height: 1),
      itemBuilder: (context, index) {
        final author = authors[index];
        final tile = _AuthorTile(
          author: author,
          onTap: () => onTap(author),
          onSearch: onSearch != null ? () => onSearch!(author) : null,
        );
        if (onDelete == null) return tile;
        return Dismissible(
          key: ValueKey(author.id),
          direction: DismissDirection.endToStart,
          background: Container(
            color: AppTheme.error,
            alignment: Alignment.centerRight,
            padding: const EdgeInsets.only(right: 20),
            child: const Icon(Icons.delete, color: Colors.white),
          ),
          confirmDismiss: (_) => _confirmDelete(context, author.authorName),
          onDismissed: (_) => onDelete!(author),
          child: tile,
        );
      },
    );
  }

  Future<bool?> _confirmDelete(BuildContext context, String name) {
    return showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Delete Author'),
        content: Text('Remove "$name" from Chaptarr?'),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            style: TextButton.styleFrom(foregroundColor: AppTheme.error),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
  }
}

class _AuthorTile extends StatelessWidget {
  final ChaptarrAuthor author;
  final VoidCallback onTap;
  final VoidCallback? onSearch;

  const _AuthorTile({
    required this.author,
    required this.onTap,
    this.onSearch,
  });

  @override
  Widget build(BuildContext context) {
    final stats = author.statistics;
    final percent = author.percentComplete;

    return ListTile(
      onTap: onTap,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: author.coverUrl,
            fit: BoxFit.cover,
            icon: Icons.person,
          ),
        ),
      ),
      title: Text(
        author.authorName,
        style: const TextStyle(
            color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Row(
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
                const SizedBox(width: 6),
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
                valueColor: AlwaysStoppedAnimation(
                  percent >= 1.0 ? AppTheme.available : AppTheme.accent,
                ),
                minHeight: 4,
              ),
            ),
          ],
        ],
      ),
      trailing: onSearch != null
          ? PopupMenuButton<String>(
              icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
              color: AppTheme.surfaceVariant,
              tooltip: 'Actions',
              onSelected: (value) {
                if (value == 'search') onSearch!();
              },
              itemBuilder: (_) => [
                const PopupMenuItem(
                  value: 'search',
                  child: Row(
                    children: [
                      Icon(Icons.search,
                          size: 18, color: AppTheme.textSecondary),
                      SizedBox(width: 10),
                      Text('Automatic search'),
                    ],
                  ),
                ),
              ],
            )
          : null,
    );
  }

  Color get _statusColor {
    if (author.percentComplete >= 1.0) return AppTheme.available;
    if (author.status == 'continuing') return AppTheme.downloading;
    if (author.status == 'ended') return AppTheme.textSecondary;
    return AppTheme.requested;
  }

  String get _statusText {
    if (author.percentComplete >= 1.0) return 'Complete';
    if (author.status == 'continuing') return 'Continuing';
    if (author.status == 'ended') return 'Ended';
    return 'Partial';
  }
}
