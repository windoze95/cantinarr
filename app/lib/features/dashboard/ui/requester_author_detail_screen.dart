import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../request/data/book_ownership.dart';
import '../data/book_authors_service.dart';
import '../logic/author_book_status.dart';

/// Requester-facing page for one author, addressed by their foreignAuthorId.
///
/// It answers the question the Books tab's authors row raises — "what does this
/// library have by them?" — with the same per-format ownership vocabulary the
/// search results and Recently Added row use, so a title reads the same wherever
/// the requester meets it.
class RequesterAuthorDetailScreen extends ConsumerWidget {
  final String foreignAuthorId;

  /// The name the row already displayed, so the app bar is right before the
  /// first byte arrives.
  final String? nameHint;

  /// The library this page is pinned to. A pinned id can never read another
  /// library's answer for the same author, even if the drawer switches.
  final String? instanceId;

  const RequesterAuthorDetailScreen({
    super.key,
    required this.foreignAuthorId,
    this.nameHint,
    this.instanceId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final pinned =
        instanceId ?? ref.watch(instanceProvider).activeChaptarrInstance?.id;
    final target = (
      instanceId: pinned,
      foreignAuthorId: foreignAuthorId,
    );
    // A library change while the page is open (an import lands, a request is
    // approved) makes this page's ownership stale, and it is uncached precisely
    // so a refetch tells the truth.
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) ref.invalidate(bookAuthorDetailProvider(target));
    });

    final detail = ref.watch(bookAuthorDetailProvider(target));
    final name = detail.valueOrNull?.author.name.trim();
    return Scaffold(
      appBar: AppBar(
        title: Text(
          (name != null && name.isNotEmpty ? name : nameHint?.trim()) ?? 'Author',
        ),
      ),
      body: RefreshIndicator(
        color: AppTheme.accent,
        onRefresh: () async {
          ref.invalidate(bookAuthorDetailProvider(target));
          await ref.read(bookAuthorDetailProvider(target).future);
        },
        child: detail.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppTheme.accent),
          ),
          error: (error, _) => _AuthorError(message: _authorErrorMessage(error)),
          data: (data) => _AuthorBody(detail: data, instanceId: pinned),
        ),
      ),
    );
  }
}

/// Says which failure this was in requester vocabulary. The distinction that
/// matters is "this library has no such author" versus "this library could not
/// be read at all" — rendered identically, a reader stops looking.
String _authorErrorMessage(Object error) {
  final status = error is DioException ? error.response?.statusCode : null;
  switch (status) {
    case 404:
      return 'This author is not in your book library.\n'
          'Search for one of their books to add them.';
    case 401:
    case 403:
      return 'You do not have access to this book library.';
    default:
      return 'This author could not be loaded. '
          'Check the connection and try again.';
  }
}

class _AuthorError extends StatelessWidget {
  final String message;

  const _AuthorError({required this.message});

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) => SingleChildScrollView(
        physics: const AlwaysScrollableScrollPhysics(),
        child: ConstrainedBox(
          constraints: BoxConstraints(minHeight: constraints.maxHeight),
          child: Center(
            child: Padding(
              padding: const EdgeInsets.all(32),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.person_off_outlined,
                      size: 48, color: AppTheme.textSecondary),
                  const SizedBox(height: 12),
                  Text(
                    message,
                    textAlign: TextAlign.center,
                    style: const TextStyle(color: AppTheme.textSecondary),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _AuthorBody extends ConsumerWidget {
  final BookAuthorDetail detail;
  final String? instanceId;

  const _AuthorBody({required this.detail, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final titles = detail.titles;
    return LayoutBuilder(builder: (context, constraints) {
      final hPad = AppBreakpoints.centeredContentPadding(
        constraints.maxWidth,
        minPadding: 0,
      );
      return ListView.separated(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: EdgeInsets.fromLTRB(hPad, 0, hPad, 16),
        itemCount: titles.length + 1,
        separatorBuilder: (_, index) => index == 0
            ? const SizedBox.shrink()
            : const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (context, index) {
          if (index == 0) {
            return _AuthorHeader(author: detail.author, instanceId: instanceId);
          }
          final title = titles[index - 1];
          return _AuthorBookTile(title: title, instanceId: instanceId);
        },
      );
    });
  }
}

class _AuthorHeader extends ConsumerWidget {
  final LibraryAuthor author;
  final String? instanceId;

  const _AuthorHeader({required this.author, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final image = instanceId == null
        ? null
        : chaptarrImageSource(ref, author.image, instanceId!);
    final count = author.countLabel;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 20),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          ClipOval(
            child: CachedImage(
              url: image?.url,
              headers: image?.headers,
              width: 84,
              height: 84,
              icon: Icons.person,
              iconSize: 28,
            ),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  author.name,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (count.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    count,
                    style: const TextStyle(color: AppTheme.textSecondary),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// One title on the author page. A title nobody has requested says so, an
/// undetermined state renders no pill at all rather than a guessed one, and
/// everything else carries the same Available/Partial/Requested vocabulary the
/// rest of the books surfaces use.
class _AuthorBookTile extends ConsumerWidget {
  final OwnedTitle title;
  final String? instanceId;

  const _AuthorBookTile({required this.title, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cover = instanceId == null
        ? null
        : chaptarrImageSource(ref, title.cover, instanceId!);
    final status = buildAuthorBookStatus(title);
    final fid = title.foreignBookId.trim();
    final year = title.year > 0 ? '${title.year}' : null;
    final subtitle = <String>[
      if (year != null) year,
      if (status?.subtitle != null) status!.subtitle!,
    ].join(' · ');

    return ListTile(
      key: ValueKey('author-book:$fid'),
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(4),
        child: CachedImage(
          url: cover?.url,
          headers: cover?.headers,
          width: 44,
          height: 66,
          icon: Icons.menu_book,
        ),
      ),
      title: Text(
        title.title,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.w600,
        ),
      ),
      subtitle: (subtitle.isEmpty && status == null)
          ? null
          : Padding(
              padding: const EdgeInsets.only(top: 3),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  if (subtitle.isNotEmpty)
                    Text(
                      subtitle,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(color: AppTheme.textSecondary),
                    ),
                  if (status != null) ...[
                    if (subtitle.isNotEmpty) const SizedBox(height: 4),
                    _StatusPill(label: status.label, color: status.color),
                  ],
                ],
              ),
            ),
      // Requests belong on the book's own page, which already owns the format
      // panel and every guard around it.
      trailing: fid.isEmpty
          ? null
          : const Icon(Icons.chevron_right, color: AppTheme.textSecondary),
      onTap: fid.isEmpty
          ? null
          : () => context.push(
                '/detail/book/${Uri.encodeComponent(fid)}'
                '?title=${Uri.encodeQueryComponent(title.title)}'
                '${instanceId == null ? '' : '&instance_id=${Uri.encodeQueryComponent(instanceId!)}'}',
              ),
    );
  }
}

class _StatusPill extends StatelessWidget {
  final String label;
  final Color color;

  const _StatusPill({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 10.5,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
