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
import '../data/book_series_service.dart';
import '../logic/author_book_status.dart';

/// Requester-facing page for one series, addressed by its name.
///
/// It lists every title the library knows about in the series, in reading
/// order, including the ones nothing has been requested for — the gap is the
/// reason to open the page, and the row's own "6 of 61" label is what raises
/// the question.
class RequesterSeriesDetailScreen extends ConsumerWidget {
  final String seriesName;

  /// The library this page is pinned to, so it cannot read another library's
  /// answer if the drawer switches.
  final String? instanceId;

  const RequesterSeriesDetailScreen({
    super.key,
    required this.seriesName,
    this.instanceId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final pinned =
        instanceId ?? ref.watch(instanceProvider).activeChaptarrInstance?.id;
    final target = (instanceId: pinned, name: seriesName);
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) ref.invalidate(bookSeriesDetailProvider(target));
    });

    final detail = ref.watch(bookSeriesDetailProvider(target));
    return Scaffold(
      appBar: AppBar(title: Text(seriesName)),
      body: RefreshIndicator(
        color: AppTheme.accent,
        onRefresh: () async {
          ref.invalidate(bookSeriesDetailProvider(target));
          await ref.read(bookSeriesDetailProvider(target).future);
        },
        child: detail.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppTheme.accent),
          ),
          error: (error, _) => _SeriesError(message: _seriesErrorMessage(error)),
          data: (data) => _SeriesBody(detail: data, instanceId: pinned),
        ),
      ),
    );
  }
}

/// Says which failure this was. "This library has no such series" and "this
/// library could not be read" are different answers and must not look alike.
String _seriesErrorMessage(Object error) {
  final status = error is DioException ? error.response?.statusCode : null;
  switch (status) {
    case 404:
      return 'This series is not in your book library.\n'
          'Search for one of its books to add it.';
    case 401:
    case 403:
      return 'You do not have access to this book library.';
    default:
      return 'This series could not be loaded. '
          'Check the connection and try again.';
  }
}

class _SeriesError extends StatelessWidget {
  final String message;

  const _SeriesError({required this.message});

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
                  const Icon(Icons.auto_stories_outlined,
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

class _SeriesBody extends StatelessWidget {
  final BookSeriesDetail detail;
  final String? instanceId;

  const _SeriesBody({required this.detail, required this.instanceId});

  @override
  Widget build(BuildContext context) {
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
            return _SeriesHeader(series: detail.series, instanceId: instanceId);
          }
          return _SeriesBookTile(
            entry: titles[index - 1],
            instanceId: instanceId,
          );
        },
      );
    });
  }
}

class _SeriesHeader extends ConsumerWidget {
  final LibrarySeries series;
  final String? instanceId;

  const _SeriesHeader({required this.series, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cover = instanceId == null || series.covers.isEmpty
        ? null
        : chaptarrImageSource(ref, series.covers.first, instanceId!);
    final count = series.countLabel;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 20),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(6),
            child: CachedImage(
              url: cover?.url,
              headers: cover?.headers,
              width: 64,
              height: 96,
              icon: Icons.auto_stories,
              iconSize: 24,
            ),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  series.name,
                  maxLines: 3,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (count.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(count,
                      style: const TextStyle(color: AppTheme.textSecondary)),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// One title in the series, led by its position so the run reads in order.
class _SeriesBookTile extends ConsumerWidget {
  final SeriesTitle entry;
  final String? instanceId;

  const _SeriesBookTile({required this.entry, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final title = entry.title;
    final cover = instanceId == null
        ? null
        : chaptarrImageSource(ref, title.cover, instanceId!);
    final status = buildAuthorBookStatus(title);
    final fid = title.foreignBookId.trim();
    final subtitle = <String>[
      if (title.author.isNotEmpty) title.author,
      if (title.year > 0) '${title.year}',
      if (status?.subtitle != null) status!.subtitle!,
    ].join(' · ');

    return ListTile(
      key: ValueKey('series-book:${entry.position}:$fid'),
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: SizedBox(
        width: 74,
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            SizedBox(
              width: 26,
              child: Text(
                // The library's own label, not a renumbering: "2A" and
                // "1.5, 1.6, 1.7" are what a reader recognises.
                entry.position.isEmpty ? '—' : entry.position,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                  color: AppTheme.textMuted,
                  fontSize: 12,
                  fontWeight: FontWeight.w700,
                ),
              ),
            ),
            const SizedBox(width: 4),
            ClipRRect(
              borderRadius: BorderRadius.circular(4),
              child: CachedImage(
                url: cover?.url,
                headers: cover?.headers,
                width: 44,
                height: 66,
                icon: Icons.menu_book,
              ),
            ),
          ],
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
