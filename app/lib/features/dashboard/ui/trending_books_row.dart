import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/trending_books_service.dart';
import '../../request/data/book_ownership.dart';
import '../data/book_library_service.dart';
import '../logic/recent_book_ownership_status.dart';

/// The Books tab's "Trending Books" row: what is trending on Hardcover right
/// now, read with the Hardcover token an admin connected to this Chaptarr
/// instance.
///
/// Each card carries the same Available / Partial / Requested pill the
/// Recently Added row uses, decided by exact identity-key match against the
/// owned-books digest (`hc-book:<id>` and `isbn:<13>`) — never by title.
///
/// When the instance has no Hardcover token, an admin sees the way to connect
/// one; a requester sees no row, since only an admin can fix it. An older
/// server that does not answer the route hides the row too.
class TrendingBooksRow extends ConsumerWidget {
  const TrendingBooksRow({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instance = ref.watch(instanceProvider).activeChaptarrInstance;
    if (instance == null) return const SizedBox.shrink();

    final trending = ref.watch(trendingBooksProvider);
    final owned = ref.watch(ownedBooksProvider);
    final isAdmin =
        ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false;
    final viewportWidth = MediaQuery.sizeOf(context).width;
    final horizontalPadding = viewportWidth >= 900 ? 24.0 : 16.0;

    // Blindness is not absence: a feed that could not be read renders
    // nothing rather than a row that looks like a quiet day.
    if (trending.hasError) return const SizedBox.shrink();
    final feed = trending.valueOrNull;
    if (feed == null && !trending.isLoading) return const SizedBox.shrink();

    if (feed != null && feed.connected == false) {
      if (!isAdmin) return const SizedBox.shrink();
      return Padding(
        padding: EdgeInsets.fromLTRB(horizontalPadding, 20, horizontalPadding, 0),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const SectionHeader(title: 'Trending Books'),
            const SizedBox(height: 8),
            const Text(
              'Connect a Hardcover account to this Chaptarr instance to see '
              "what's trending with readers right now.",
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
            const SizedBox(height: 8),
            OutlinedButton.icon(
              icon: const Icon(Icons.add_link),
              label: const Text('Connect Hardcover in Chaptarr settings'),
              onPressed: () => context.push(
                '/settings/instance/${instance.id}',
                extra: {
                  'service_type': instance.serviceType,
                  'name': instance.name,
                  'is_default': instance.isDefault,
                },
              ),
            ),
          ],
        ),
      );
    }

    // An older server said nothing about the connection: no row.
    if (feed != null && feed.connected == null) return const SizedBox.shrink();

    final books = feed?.books ?? const <TrendingBook>[];
    if (books.isEmpty && !trending.isLoading) return const SizedBox.shrink();

    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    // Exact typed-key join against the digest; an unreadable digest simply
    // shows no pills (the list itself is still worth seeing).
    final byIdentityKey = <String, OwnedTitle>{};
    for (final title in owned.valueOrNull ?? const <OwnedTitle>[]) {
      for (final key in title.identityKeys) {
        byIdentityKey.putIfAbsent(key, () => title);
      }
      final foreign = title.foreignBookId.trim();
      if (foreign.isNotEmpty) {
        byIdentityKey.putIfAbsent('native-book:$foreign', () => title);
      }
    }
    OwnedTitle? ownedFor(TrendingBook book) {
      for (final key in book.identityKeys) {
        final match = byIdentityKey[key];
        if (match != null) return match;
      }
      return byIdentityKey['native-book:${book.foreignId}'];
    }

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(horizontal: horizontalPadding),
            child: const SectionHeader(title: 'Trending Books'),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<TrendingBook>(
            items: books,
            isLoading: trending.isLoading,
            height: cardWidth * 1.5 + 68,
            itemBuilder: (book) {
              final status = buildRecentBookStatus(ownedFor(book));
              return MediaCard(
                id: book.hardcoverId,
                title: book.title,
                posterPath: book.imageUrl,
                placeholderIcon: Icons.menu_book,
                subtitle: status?.subtitle ??
                    (book.author.isNotEmpty ? book.author : null),
                statusLabel: status?.label,
                statusColor: status?.color,
                rating: book.rating,
                width: cardWidth,
                onTap: () => context.push(
                  '/detail/book/${Uri.encodeComponent(book.foreignId)}'
                  '?source=chaptarr&title=${Uri.encodeQueryComponent(book.title)}'
                  '&instance_id=${Uri.encodeQueryComponent(instance.id)}',
                ),
              );
            },
          ),
        ],
      ),
    );
  }
}
