import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../request/data/book_ownership.dart';
import '../data/book_library_service.dart';
import '../data/recent_books_service.dart';
import '../logic/recent_book_ownership_status.dart';

/// The Books tab's "Recently Added" row: what actually landed in the library,
/// newest first.
///
/// Books need this more than movies or TV do — a small ebook can finish
/// downloading between two queue polls, so this row is often the first place a
/// user sees that it arrived.
///
/// The row hides itself entirely when there is nothing to show, when the user
/// has no Chaptarr access, or when the library cannot be read. None of those is
/// an error worth interrupting a search for.
class RecentlyAddedBooksRow extends ConsumerWidget {
  const RecentlyAddedBooksRow({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceId = ref.watch(instanceProvider).activeChaptarrInstance?.id;
    if (instanceId == null) return const SizedBox.shrink();

    final recent = ref.watch(recentBooksProvider);
    final owned = ref.watch(ownedBooksProvider);
    final books = recent.valueOrNull ?? const <RecentBook>[];
    // Nothing to show, no access, an unreachable library, or an unreadable
    // ownership digest all look the same: no row. hasError also covers a
    // failed refresh that retained stale books. D-03: rendering the cards
    // with no pills would be honest per D-02 but pointless, since this row's
    // whole job after this phase is to carry availability — so an
    // unreadable digest hides the row instead of degrading it, the same
    // treatment `recent.hasError` already gets. `ownedBooksProvider` keeps
    // failures as `AsyncError` rather than an empty list precisely so this
    // check can tell an unreachable library from a genuinely empty one. Do
    // not gate on `owned.isLoading` — that would make the row flicker in and
    // out on every refresh.
    if (recent.hasError || owned.hasError || (books.isEmpty && !recent.isLoading)) {
      return const SizedBox.shrink();
    }

    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    // D-01: both `RecentBook.foreignBookId` and `OwnedTitle.foreignBookId`
    // come from Chaptarr's own `book.ForeignBookID` on the same library
    // record, so exact string equality is the correct join — the fuzzy
    // title/author matcher in this feature's `logic/` folder is the wrong
    // tool here; it exists only for search lookup results whose
    // foreignBookId does not line up with the digest. Trimming and
    // excluding empty keys means a recent record with an empty
    // foreignBookId can never collide with a digest row that also has one.
    final byForeignBookId = <String, OwnedTitle>{
      for (final title in owned.valueOrNull ?? const <OwnedTitle>[])
        if (title.foreignBookId.trim().isNotEmpty)
          title.foreignBookId.trim(): title,
    };

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: viewportWidth >= 900 ? 24 : 16,
            ),
            child: const SectionHeader(title: 'Recently Added'),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<RecentBook>(
            items: books,
            isLoading: recent.isLoading,
            height: cardWidth * 1.5 + 68,
            itemBuilder: (book) {
              final cover = chaptarrImageSource(ref, book.cover, instanceId);
              final canOpen = book.foreignBookId.trim().isNotEmpty;
              final status =
                  buildRecentBookStatus(byForeignBookId[book.foreignBookId.trim()]);
              return MediaCard(
                id: book.bookId,
                title: book.title,
                posterPath: cover?.url,
                posterHeaders: cover?.headers,
                placeholderIcon: Icons.menu_book,
                subtitle: status?.subtitle ?? book.formatLabel,
                // D-02: a null status here is the correct rendering for an
                // undetermined ownership state — MediaCard already skips its
                // badge entirely when the label is null, and substituting
                // any literal label would reintroduce the exact defect
                // BOOK-01 removes.
                statusLabel: status?.label,
                statusColor: status?.color,
                width: cardWidth,
                onTap: canOpen
                    ? () => context.push(
                          '/detail/book/${Uri.encodeComponent(book.foreignBookId)}'
                          '?title=${Uri.encodeQueryComponent(book.title)}'
                          '&instance_id=${Uri.encodeQueryComponent(instanceId)}',
                        )
                    : null,
              );
            },
          ),
        ],
      ),
    );
  }
}
