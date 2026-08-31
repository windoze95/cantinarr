import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../shell/logic/library_author_index.dart';
import '../data/book_library_service.dart';
import '../data/book_authors_service.dart';
import '../data/book_series_service.dart';
import '../data/recent_books_service.dart';
import 'library_authors_row.dart';
import 'library_series_row.dart';
import 'recently_added_books_row.dart';

/// Dashboard Books tab: the browse rows only (Recently Added, Authors,
/// Series). Chaptarr book/author search now lives in the shell toolbar
/// (`shellBookSearchProvider` / `BookSearchResultsView`, see
/// `app/lib/features/shell/logic/shell_book_search_provider.dart` and
/// `app/lib/features/discover/ui/book_search_results_view.dart`) — the shell
/// overlay covers this tab's body while a search is active, so these rows no
/// longer hide themselves during one.
class DashboardBooksTab extends ConsumerStatefulWidget {
  const DashboardBooksTab({super.key});

  @override
  ConsumerState<DashboardBooksTab> createState() => _DashboardBooksTabState();
}

class _DashboardBooksTabState extends ConsumerState<DashboardBooksTab>
    with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    // The libraries may have changed while the app was backgrounded (downloads
    // finishing, an admin working directly in the arrs) — re-pull the browse
    // rows' truth.
    if (state == AppLifecycleState.resumed) _refreshBookTruth();
  }

  void _refreshBookTruth() {
    final instanceId = ref.read(instanceProvider).activeChaptarrInstance?.id;
    ref.invalidate(ownedBooksForInstanceProvider(instanceId));
    ref.invalidate(ownedBooksProvider);
    ref.invalidate(recentBooksForInstanceProvider(instanceId));
    ref.invalidate(recentBooksProvider);
    ref.invalidate(bookAuthorsProvider);
    ref.invalidate(bookSeriesProvider);
    // The search overlay's "in your library" verdicts come from this index,
    // which is kept alive for the session. Without invalidating it here, a book
    // requested from an author the library did not previously hold shows up in
    // the Authors row above while the overlay keeps calling that author
    // metadata-only until a restart — and an index whose first fetch failed
    // stays empty for the rest of the session with no way to retry.
    ref.invalidate(libraryAuthorIndexProvider);
    ref.read(libraryRefreshTickProvider.notifier).state++;
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) _refreshBookTruth();
    });
    ref.listen(
      instanceProvider.select((state) => state.activeChaptarrInstance?.id),
      (previous, next) {
        if (previous == next) return;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (!mounted) return;
          ref.invalidate(ownedBooksProvider);
        });
      },
    );
    return const SingleChildScrollView(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          RecentlyAddedBooksRow(),
          LibraryAuthorsRow(),
          LibrarySeriesRow(),
        ],
      ),
    );
  }
}
