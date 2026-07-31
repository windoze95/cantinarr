import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../data/recent_books_service.dart';

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
    final books = recent.valueOrNull ?? const <RecentBook>[];
    // Nothing to show, no access, or an unreachable library all look the same:
    // no row. hasError also covers a failed refresh that retained stale books.
    if (recent.hasError || (books.isEmpty && !recent.isLoading)) {
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
              return MediaCard(
                id: book.bookId,
                title: book.title,
                posterPath: cover?.url,
                posterHeaders: cover?.headers,
                placeholderIcon: Icons.menu_book,
                subtitle: book.formatLabel,
                statusLabel: 'Available',
                statusColor: AppTheme.available,
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
