import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/status_pill.dart';
import '../../chaptarr/data/chaptarr_image.dart';
import '../../request/data/book_ownership.dart';
import '../../request/data/request_service.dart';
import '../../request/ui/book_request_button.dart';
import '../data/book_library_service.dart';

/// Requester-facing detail for one book, addressed by its Chaptarr/Readarr
/// foreignBookId — the identity request rows store and request-decision push
/// payloads carry as `foreign_id`. It reuses the Books tab's presentation
/// pieces (owned cover, ownership chip, per-format request button); books have
/// no TMDB-style metadata endpoint, so title/author/cover resolve from the
/// owned-books digest, with the push payload's title as a fallback for a book
/// that never landed in the library (e.g. a denied request). When neither
/// source can name the book, a graceful not-found state points back to the
/// Books tab instead of presenting an unnamed record.
class RequesterBookDetailScreen extends ConsumerWidget {
  /// The Chaptarr/Readarr foreignBookId this screen presents.
  final String foreignId;

  /// Display title carried on the deep link (`?title=`), used when the
  /// owned-books digest can't resolve [foreignId].
  final String? titleHint;

  const RequesterBookDetailScreen({
    super.key,
    required this.foreignId,
    this.titleHint,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final digest = ref.watch(ownedBooksProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Book details')),
      body: digest.when(
        loading: () => const Center(
          child: CircularProgressIndicator(color: AppTheme.accent),
        ),
        // The digest provider itself degrades to an empty list on failure, so
        // this branch is defensive: fall through to the hint/not-found flow.
        error: (_, __) => _resolved(context, ref, const []),
        data: (titles) => _resolved(context, ref, titles),
      ),
    );
  }

  Widget _resolved(BuildContext context, WidgetRef ref, List<OwnedTitle> titles) {
    OwnedTitle? owned;
    for (final t in titles) {
      if (t.foreignBookId.isNotEmpty && t.foreignBookId == foreignId) {
        owned = t;
        break;
      }
    }
    final hint = titleHint?.trim() ?? '';
    final title = owned?.title.isNotEmpty ?? false ? owned!.title : hint;
    if (title.isEmpty) return _notFound(context);
    return _detail(context, ref, title, owned);
  }

  Widget _detail(
      BuildContext context, WidgetRef ref, String title, OwnedTitle? owned) {
    final subtitle = <String>[
      if (owned != null && owned.author.isNotEmpty) owned.author,
      if (owned != null && owned.year > 0) '${owned.year}',
    ].join(' · ');
    final instanceId = ref.read(instanceProvider).activeChaptarrInstance?.id;
    // Only an owned record carries a loadable cover (its cached /MediaCover);
    // see the Books tab for why lookup covers aren't attempted.
    final cover = (owned != null && owned.cover.isNotEmpty && instanceId != null)
        ? chaptarrImageSource(ref, owned.cover, instanceId)
        : null;
    final ownership = owned?.ownership;
    final bothDownloaded = ownership != null &&
        ownership.ebook.downloaded &&
        ownership.audiobook.downloaded;
    return CenteredContent(
      child: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          Center(
            child: ClipRRect(
              borderRadius: BorderRadius.circular(8),
              child: CachedImage(
                url: cover?.url,
                headers: cover?.headers,
                width: 132,
                height: 198,
                icon: Icons.menu_book,
              ),
            ),
          ),
          const SizedBox(height: 20),
          Text(
            title,
            textAlign: TextAlign.center,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          if (subtitle.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              subtitle,
              textAlign: TextAlign.center,
              style: const TextStyle(color: AppTheme.textSecondary),
            ),
          ],
          const SizedBox(height: 16),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              if (ownership != null && ownership.anyOwned) ...[
                StatusPill(
                  text: ownership.anyDownloaded ? 'Downloaded' : 'In Library',
                  color: ownership.anyDownloaded
                      ? AppTheme.available
                      : AppTheme.requested,
                ),
                const SizedBox(width: 12),
              ],
              // Both files present → the chip says it all; otherwise the same
              // per-format request affordance as the Books tab (it loads the
              // user's request state itself and gates each format).
              if (!bothDownloaded)
                BookRequestButton(
                  foreignId: foreignId,
                  title: title,
                  service: RequestService(
                      backendDio: ref.read(backendClientProvider)),
                  ownership: ownership,
                ),
            ],
          ),
        ],
      ),
    );
  }

  /// Shown when neither the owned-books digest nor the deep link can name the
  /// book (removed from Chaptarr, or a hand-typed id). Requesting needs a
  /// title, so the honest affordance is the Books tab's search.
  Widget _notFound(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.menu_book, size: 48, color: AppTheme.textSecondary),
            const SizedBox(height: 12),
            const Text(
              'This book could not be found. It may have been removed from '
              'the library.',
              textAlign: TextAlign.center,
              style: TextStyle(color: AppTheme.textSecondary),
            ),
            const SizedBox(height: 16),
            OutlinedButton(
              onPressed: () => context.go('/dashboard/books'),
              child: const Text('Browse Books'),
            ),
          ],
        ),
      ),
    );
  }
}
