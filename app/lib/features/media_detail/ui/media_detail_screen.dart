import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/media_header.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/data/discover_api_service.dart';
import '../../issues/ui/report_problem_sheet.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/request_provider.dart';
import '../../request/ui/request_button.dart';
import '../../request/ui/request_options_sheet.dart';
import '../../request/ui/request_status_sheet.dart';
import '../logic/media_detail_provider.dart';
import 'season_table.dart';

/// Full detail screen for a movie or TV show.
class MediaDetailScreen extends ConsumerStatefulWidget {
  final int id;
  final MediaType mediaType;

  const MediaDetailScreen({
    super.key,
    required this.id,
    required this.mediaType,
  });

  @override
  ConsumerState<MediaDetailScreen> createState() => _MediaDetailScreenState();
}

class _MediaDetailScreenState extends ConsumerState<MediaDetailScreen> {
  late final MediaDetailNotifier _detailNotifier;
  late final RequestNotifier _requestNotifier;

  /// Anchors the "Seasons" section so "Request More" can scroll the user to the
  /// per-season picker.
  final GlobalKey _seasonsKey = GlobalKey();

  @override
  void initState() {
    super.initState();
    final api = ref.read(discoverServiceProvider);
    _detailNotifier = MediaDetailNotifier(
      api: api,
      id: widget.id,
      mediaType: widget.mediaType,
    );
    final backendDio = ref.read(backendClientProvider);
    _requestNotifier = RequestNotifier(
      service: RequestService(backendDio: backendDio),
      tmdbId: widget.id,
      mediaType: widget.mediaType,
    );

    _detailNotifier.load();
    _requestNotifier.checkStatus();
  }

  @override
  Widget build(BuildContext context) {
    // Live-update the request button when an approval decision for THIS title
    // arrives over the socket (complements the global toast).
    ref.listen(requestDecisionEventsProvider, (_, next) {
      final event = next.valueOrNull;
      if (event == null) return;
      final tmdb = (event.data['tmdb_id'] as num?)?.toInt();
      if (tmdb == widget.id &&
          event.data['media_type'] == widget.mediaType.name) {
        _requestNotifier.checkStatus();
      }
    });
    return ListenableBuilder(
      listenable: _detailNotifier,
      builder: (context, _) {
        final state = _detailNotifier.state;

        if (state.isLoading && state.movieDetail == null && state.tvDetail == null) {
          return const Scaffold(
            body: Center(child: CircularProgressIndicator(color: AppTheme.accent)),
          );
        }

        if (state.error != null && state.movieDetail == null && state.tvDetail == null) {
          return Scaffold(
            appBar: AppBar(),
            body: Center(child: Text(state.error!, style: const TextStyle(color: AppTheme.error))),
          );
        }

        return Scaffold(
          body: CustomScrollView(
            slivers: [
              // Back button
              SliverAppBar(
                backgroundColor: Colors.transparent,
                leading: IconButton(
                  icon: const Icon(Icons.arrow_back),
                  onPressed: () => context.pop(),
                ),
                pinned: false,
                floating: true,
              ),

              SliverToBoxAdapter(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    // Header with backdrop + poster
                    MediaHeader(
                      posterPath: state.posterPath,
                      backdropPath: state.backdropPath,
                      title: state.title,
                    ),
                    const SizedBox(height: 16),

                    // Request button
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      child: ListenableBuilder(
                        listenable: _requestNotifier,
                        builder: (_, __) => RequestButton(
                          status: _requestNotifier.state.status,
                          isRequesting: _requestNotifier.state.isRequesting,
                          error: _requestNotifier.state.error,
                          onRequest: () => _onRequest(),
                        ),
                      ),
                    ),
                    const SizedBox(height: 8),

                    // Status info tap target
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 16),
                      child: ListenableBuilder(
                        listenable: _requestNotifier,
                        builder: (_, __) => GestureDetector(
                          onTap: () => _showStatusSheet(context, state.title,
                              _requestNotifier.state.status),
                          child: Row(
                            mainAxisAlignment: MainAxisAlignment.center,
                            children: [
                              Icon(Icons.info_outline,
                                  size: 14, color: AppTheme.textSecondary),
                              const SizedBox(width: 4),
                              Text(
                                _requestNotifier.state.status.label,
                                style: const TextStyle(
                                    color: AppTheme.textSecondary,
                                    fontSize: 13),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ),

                    // Quiet "Report a problem" affordance — only once the
                    // title is at least partially in the library and the
                    // server allows reporting.
                    ListenableBuilder(
                      listenable: _requestNotifier,
                      builder: (_, __) {
                        if (!_canReport(_requestNotifier.state.status)) {
                          return const SizedBox.shrink();
                        }
                        return Center(
                          child: TextButton.icon(
                            onPressed: () => _onReportProblem(state),
                            icon: const Icon(Icons.flag_outlined, size: 14),
                            label: const Text('Report a problem'),
                            style: TextButton.styleFrom(
                              foregroundColor: AppTheme.textSecondary,
                              textStyle: const TextStyle(fontSize: 13),
                            ),
                          ),
                        );
                      },
                    ),
                    const SizedBox(height: 16),

                    // Genres
                    if (state.genres.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: Wrap(
                          spacing: 6,
                          runSpacing: 6,
                          children: state.genres
                              .map((g) => Chip(
                                    label: Text(g.name,
                                        style: const TextStyle(fontSize: 12)),
                                    backgroundColor: AppTheme.surfaceVariant,
                                    side: const BorderSide(
                                        color: AppTheme.border),
                                    materialTapTargetSize:
                                        MaterialTapTargetSize.shrinkWrap,
                                    visualDensity: VisualDensity.compact,
                                  ))
                              .toList(),
                        ),
                      ),

                    // Rating
                    if (state.voteAverage != null && state.voteAverage! > 0) ...[
                      const SizedBox(height: 12),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: Row(
                          children: [
                            const Icon(Icons.star,
                                color: AppTheme.accent, size: 18),
                            const SizedBox(width: 4),
                            Text(
                              state.voteAverage!.toStringAsFixed(1),
                              style: const TextStyle(
                                color: AppTheme.textPrimary,
                                fontSize: 16,
                                fontWeight: FontWeight.w600,
                              ),
                            ),
                            const Text(' / 10',
                                style: TextStyle(
                                    color: AppTheme.textSecondary,
                                    fontSize: 14)),
                          ],
                        ),
                      ),
                    ],

                    // Tagline
                    if (state.tagline.isNotEmpty) ...[
                      const SizedBox(height: 16),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: Text(
                          '"${state.tagline}"',
                          style: const TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 15,
                            fontStyle: FontStyle.italic,
                          ),
                        ),
                      ),
                    ],

                    // Overview
                    if (state.overview.isNotEmpty) ...[
                      const SizedBox(height: 12),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: Text(
                          state.overview,
                          style: const TextStyle(
                              color: AppTheme.textPrimary,
                              fontSize: 15,
                              height: 1.5),
                        ),
                      ),
                    ],

                    // Watch Trailer button
                    if (state.trailerKey != null) ...[
                      const SizedBox(height: 16),
                      Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: OutlinedButton.icon(
                          onPressed: () => _openTrailer(state.trailerKey!),
                          icon: const Icon(Icons.play_arrow),
                          label: const Text('Watch Trailer'),
                          style: OutlinedButton.styleFrom(
                            foregroundColor: AppTheme.textPrimary,
                            side: const BorderSide(color: AppTheme.border),
                            shape: RoundedRectangleBorder(
                              borderRadius: BorderRadius.circular(12),
                            ),
                          ),
                        ),
                      ),
                    ],

                    // Seasons (TV only): interactive per-season request table
                    // fed by live availability from the request notifier.
                    if (state.seasons.isNotEmpty) ...[
                      const SizedBox(height: 24),
                      Padding(
                        key: _seasonsKey,
                        padding: const EdgeInsets.symmetric(horizontal: 16),
                        child: const Text('Seasons',
                            style: TextStyle(
                                fontSize: 20,
                                fontWeight: FontWeight.bold,
                                color: AppTheme.textPrimary)),
                      ),
                      const SizedBox(height: 12),
                      SeasonTable(
                        seasons: state.seasons,
                        notifier: _requestNotifier,
                        title: state.title,
                        tvdbId: state.tvDetail?.externalIds?.tvdbId,
                      ),
                    ],

                    // Recommendations
                    if (state.recommendations.isNotEmpty) ...[
                      const SizedBox(height: 24),
                      _SectionRow(
                        title: 'Recommended',
                        items: state.recommendations,
                      ),
                    ],

                    // Similar
                    if (state.similar.isNotEmpty) ...[
                      const SizedBox(height: 24),
                      _SectionRow(
                        title: 'Similar',
                        items: state.similar,
                      ),
                    ],

                    const SizedBox(height: 40),
                  ],
                ),
              ),
            ],
          ),
        );
      },
    );
  }

  void _scrollToSeasons() {
    final ctx = _seasonsKey.currentContext;
    if (ctx == null) return;
    Scrollable.ensureVisible(
      ctx,
      duration: const Duration(milliseconds: 400),
      curve: Curves.easeInOut,
      alignment: 0.05,
    );
  }

  void _openTrailer(String key) {
    final url = Uri.parse('https://www.youtube.com/watch?v=$key');
    launchUrl(url, mode: LaunchMode.externalApplication);
  }

  /// Handle a request tap: if the user may choose options (season scope /
  /// quality), present the picker first; otherwise submit immediately to keep
  /// the one-tap experience.
  Future<void> _onRequest() async {
    final s = _detailNotifier.state;

    // A partially-available show: "Request More" drops the user into the
    // per-season picker below rather than the coarse season-scope sheet, so
    // they can choose exactly which missing seasons to add.
    if (widget.mediaType == MediaType.tv &&
        _requestNotifier.state.status == RequestStatus.partial &&
        s.seasons.isNotEmpty) {
      _scrollToSeasons();
      return;
    }

    final title = s.title;
    final tvdbId = s.tvDetail?.externalIds?.tvdbId;

    final options = await _requestNotifier.fetchOptions();
    String? seasonScope;
    int? qualityProfileId;
    if (options != null && options.hasChoices) {
      if (!mounted) return;
      final result = await showModalBottomSheet<RequestOptionsResult>(
        context: context,
        backgroundColor: Colors.transparent,
        isScrollControlled: true,
        builder: (_) => RequestOptionsSheet(options: options),
      );
      if (result == null) return; // cancelled
      seasonScope = result.seasonScope;
      qualityProfileId = result.qualityProfileId;
    }

    await _requestNotifier.request(
      title: title,
      tvdbId: tvdbId,
      seasonScope: seasonScope,
      qualityProfileId: qualityProfileId,
    );
  }

  /// Reporting is offered only once the title is at least partially in the
  /// library (so there's a download to complain about) and the server allows
  /// it.
  bool _canReport(RequestStatus status) {
    final allow =
        ref.watch(authProvider).valueOrNull?.connection?.allowReporting ??
            false;
    if (!allow) return false;
    return status == RequestStatus.available ||
        status == RequestStatus.partial ||
        status == RequestStatus.downloading;
  }

  /// Opens the report flow. For a movie, scopes directly to the movie. For TV,
  /// lets the reporter narrow to a season/episode (reusing the loaded seasons)
  /// or report the whole series.
  Future<void> _onReportProblem(MediaDetailState state) async {
    final title = state.title;
    if (widget.mediaType == MediaType.movie) {
      await showReportProblemSheet(
        context,
        scope: ReportScope.movie(tmdbId: widget.id, title: title),
      );
      return;
    }

    final tvdbId = state.tvDetail?.externalIds?.tvdbId;
    final scope = await _pickTvScope(state, title, tvdbId);
    if (scope == null) return; // cancelled
    if (!mounted) return;
    await showReportProblemSheet(context, scope: scope);
  }

  /// Presents a small picker for which part of a show the report is about.
  /// Returns null if cancelled.
  Future<ReportScope?> _pickTvScope(
      MediaDetailState state, String title, int? tvdbId) {
    // Real seasons only (drop a season 0 / specials placeholder when empty).
    final seasons =
        state.seasons.where((s) => s.seasonNumber > 0).toList();
    return showModalBottomSheet<ReportScope>(
      context: context,
      backgroundColor: AppTheme.surface,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      builder: (sheetContext) {
        return SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Padding(
                padding: EdgeInsets.fromLTRB(20, 16, 20, 8),
                child: Text(
                  "What's the problem with?",
                  style: TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 16,
                      fontWeight: FontWeight.bold),
                ),
              ),
              ListTile(
                leading:
                    const Icon(Icons.tv_outlined, color: AppTheme.textSecondary),
                title: const Text('The whole series',
                    style: TextStyle(color: AppTheme.textPrimary)),
                onTap: () => Navigator.of(sheetContext).pop(
                  ReportScope.series(
                      tmdbId: widget.id, tvdbId: tvdbId, title: title),
                ),
              ),
              if (seasons.isNotEmpty)
                Flexible(
                  child: ListView(
                    shrinkWrap: true,
                    children: [
                      for (final s in seasons)
                        ListTile(
                          leading: const Icon(Icons.video_library_outlined,
                              color: AppTheme.textSecondary),
                          title: Text('Season ${s.seasonNumber}',
                              style:
                                  const TextStyle(color: AppTheme.textPrimary)),
                          onTap: () => Navigator.of(sheetContext).pop(
                            ReportScope.series(
                              tmdbId: widget.id,
                              tvdbId: tvdbId,
                              seasonNumber: s.seasonNumber,
                              title: title,
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  void _showStatusSheet(
      BuildContext context, String title, RequestStatus status) {
    showModalBottomSheet(
      context: context,
      backgroundColor: Colors.transparent,
      builder: (_) => RequestStatusSheet(
        title: title,
        status: status,
        seasons: _requestNotifier.state.seasons,
      ),
    );
  }
}

class _SectionRow extends StatelessWidget {
  final String title;
  final List<MediaItem> items;

  const _SectionRow({
    required this.title,
    required this.items,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: Text(title,
              style: const TextStyle(
                  fontSize: 20,
                  fontWeight: FontWeight.bold,
                  color: AppTheme.textPrimary)),
        ),
        const SizedBox(height: 12),
        HorizontalItemRow<MediaItem>(
          items: items,
          isLoading: false,
          itemBuilder: (item) => MediaCard(
            id: item.id,
            title: item.title,
            posterPath: item.posterPath,
            width: 100,
            onTap: () => context.push(
              '/detail/${item.mediaType.name}/${item.id}',
            ),
          ),
        ),
      ],
    );
  }
}
