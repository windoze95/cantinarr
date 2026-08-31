import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/section_header.dart';
import '../../../core/widgets/section_sort_menu.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../data/book_authors_service.dart';

/// The Books tab's "Authors" row: whose books this library actually holds,
/// most-collected first.
///
/// Books discovery is search-first because Chaptarr has no popular feed, which
/// leaves a requester who does not already have a title in mind with nothing to
/// do. Authors are the one axis a book library can be browsed along, so this row
/// gives that user somewhere to start.
///
/// Like the Recently Added row it hides itself entirely when there is nothing to
/// show, when the user has no Chaptarr access, or when the library cannot be
/// read — none of those is an error worth interrupting a search for.
class LibraryAuthorsRow extends ConsumerWidget {
  const LibraryAuthorsRow({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    if (instanceId == null) return const SizedBox.shrink();

    final authorsAsync = ref.watch(bookAuthorsProvider);
    final page = authorsAsync.valueOrNull;
    final authors = page?.authors ?? const <LibraryAuthor>[];
    // hasError also covers a failed refresh that retained stale authors: an
    // unreadable library must not keep claiming it holds these authors.
    //
    // Unlike the Recently Added row above it, this one shows nothing at all
    // while it loads rather than a shelf of placeholders. Two reasons: the
    // shared placeholder is poster-shaped and these cards are circular, and a
    // row that must vanish when the library has no authors should not first
    // promise a row that is about to disappear.
    if (authorsAsync.hasError || authors.isEmpty) {
      return const SizedBox.shrink();
    }

    final viewportWidth = MediaQuery.sizeOf(context).width;
    final avatarSize =
        viewportWidth >= 900 ? 104.0 : (viewportWidth >= 600 ? 96.0 : 88.0);

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
              title: 'Authors',
              trailing: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SectionTruncationNote(
                    shown: authors.length,
                    total: page?.total ?? authors.length,
                  ),
                  SectionSortMenu<AuthorSort>(
                    tooltip: 'Sort authors',
                    options: AuthorSort.values,
                    selected: ref.watch(bookAuthorsSortProvider),
                    labelOf: (option) => option.label,
                    onSelected: (next) =>
                        ref.read(bookAuthorsSortProvider.notifier).state = next,
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<LibraryAuthor>(
            items: authors,
            isLoading: false,
            // Pinned statically rather than measured: a row whose height
            // depends on its longest name resizes as cards scroll in, which
            // shifts everything below it.
            height: authorAvatarRowHeight(avatarSize),
            itemBuilder: (author) => AuthorAvatarCard(
              author: author,
              size: avatarSize,
              image: chaptarrImageSource(ref, author.image, instanceId),
              onTap: author.foreignAuthorId.isEmpty
                  ? null
                  : () => context.push(
                        '/detail/author/${Uri.encodeComponent(author.foreignAuthorId)}'
                        '?name=${Uri.encodeQueryComponent(author.name)}'
                        '&instance_id=${Uri.encodeQueryComponent(instanceId)}',
                      ),
            ),
          ),
        ],
      ),
    );
  }
}

/// The row's fixed height for a given avatar size: the circle, plus room for a
/// two-line name and a two-line count beneath it.
///
/// The count gets two lines because it is the line that must not be cut off:
/// "2 of 4 books available" says what the number counted, and an ellipsis
/// turns it back into a bare number that reads as the whole library.
double authorAvatarRowHeight(double avatarSize) => avatarSize + 76;

/// One author in the browse row: a circular portrait, the name, and what the
/// library holds by them.
class AuthorAvatarCard extends StatelessWidget {
  final LibraryAuthor author;
  final double size;
  final ChaptarrImageSource? image;
  final VoidCallback? onTap;

  const AuthorAvatarCard({
    super.key,
    required this.author,
    required this.size,
    this.image,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final count = author.countLabel;
    return SizedBox(
      width: size,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            ClipOval(
              child: CachedImage(
                url: image?.url,
                headers: image?.headers,
                width: size,
                height: size,
                icon: Icons.person,
                iconSize: size / 3,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              author.name,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              textAlign: TextAlign.center,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 12.5,
                fontWeight: FontWeight.w600,
                height: 1.2,
              ),
            ),
            if (count.isNotEmpty) ...[
              const SizedBox(height: 2),
              Text(
                count,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 11,
                  height: 1.25,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
