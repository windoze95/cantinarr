import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/data/discover_api_service.dart';
import '../../issues/ui/report_problem_sheet.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/ui/radarr_movie_detail_screen.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/request_provider.dart';
import '../../request/ui/request_button.dart';
import '../../request/ui/request_options_sheet.dart';
import '../../request/ui/request_status_sheet.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/ui/sonarr_series_detail_screen.dart';
import '../logic/arr_deep_link.dart';
import '../logic/media_detail_provider.dart';
import 'media_hero.dart';
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

  /// The request option set the server allows this user (season/quality
  /// choice). Loaded once for TV so the season picker can hide its request
  /// affordances when the user may not choose seasons — the server ignores an
  /// explicit season list from such users, so offering the picker would be a
  /// silent no-op.
  RequestOptions? _requestOptions;

  /// For admins, a resolved deep link into the backing *arr (Radarr for movies,
  /// Sonarr for TV) when this title actually exists there. Null while loading,
  /// for non-admins, or when the title has no destination in the arr yet — the
  /// "Open in …" affordance is shown only when this is non-null.
  ArrDeepLink? _arrLink;

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

    // Resolve the arr deep link once the TMDB detail is in — Sonarr matching
    // needs the show's TVDB id, which only lands after the detail loads.
    _detailNotifier.load().then((_) {
      if (mounted) _resolveArrLink();
    });
    _requestNotifier.checkStatus();
    if (widget.mediaType == MediaType.tv) {
      _requestNotifier.fetchOptions().then((opts) {
        if (mounted && opts != null) setState(() => _requestOptions = opts);
      });
    }
  }

  /// Whether the user may pick specific seasons. Defaults to true (the
  /// server's out-of-the-box global setting) until the options load.
  bool get _canChooseSeasons => _requestOptions?.canChooseSeason ?? true;

  @override
  Widget build(BuildContext context) {
    // Reporting binds to the currently active arr, so an instance switch must
    // rebuild the affordance and capture the new concrete instance id.
    ref.watch(instanceProvider);
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
    // A request just added the title to the arr (main or per-season request
    // both bump this tick) — re-check so the admin "Open in …" link appears.
    ref.listen(libraryRefreshTickProvider, (_, __) => _resolveArrLink());
    // Resolve (or re-resolve) the admin link once auth settles — covers auth
    // landing after the initial detail load (e.g. an optimistic reconnect).
    ref.listen(authProvider, (_, __) => _resolveArrLink());
    return ListenableBuilder(
      listenable: _detailNotifier,
      builder: (context, _) {
        final state = _detailNotifier.state;

        if (state.isLoading &&
            state.movieDetail == null &&
            state.tvDetail == null) {
          return const Scaffold(
            body: Center(
                child: CircularProgressIndicator(color: AppTheme.accent)),
          );
        }

        if (state.error != null &&
            state.movieDetail == null &&
            state.tvDetail == null) {
          return Scaffold(
            appBar: AppBar(),
            body: Center(
                child: Text(state.error!,
                    style: const TextStyle(color: AppTheme.error))),
          );
        }

        final size = MediaQuery.sizeOf(context);
        final topPadding = MediaQuery.paddingOf(context).top;
        return Scaffold(
          body: CustomScrollView(
            slivers: [
              // Cinematic hero: pinned, scroll-choreographed backdrop +
              // poster + title that collapses into a marquee bar owning the
              // back affordance (full-bleed; the detail content below it
              // reads as a centered column on desktop).
              SliverPersistentHeader(
                pinned: true,
                delegate: MediaHeroDelegate(
                  title: state.title,
                  posterPath: state.posterPath,
                  backdropPath: state.backdropPath,
                  expandedExtent: MediaHeroDelegate.expandedExtentFor(
                    viewportHeight: size.height,
                    viewportWidth: size.width,
                    hasBackdrop: state.backdropPath != null,
                  ),
                  collapsedExtent: MediaHeroDelegate.collapsedExtentFor(
                    topPadding: topPadding,
                  ),
                  topPadding: topPadding,
                  disableAnimations: MediaQuery.disableAnimationsOf(context),
                  onBack: () => context.pop(),
                ),
              ),

              SliverToBoxAdapter(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const SizedBox(height: 16),
                    CenteredContent(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          // One coherent request/status dock. The previous
                          // tiny status text was easy to miss and not keyboard
                          // accessible; every secondary action is now a real
                          // button in the same decision surface.
                          Padding(
                            padding: const EdgeInsets.symmetric(horizontal: 16),
                            child: AppPanel(
                              accentColor: AppTheme.accent,
                              child: ListenableBuilder(
                                listenable: _requestNotifier,
                                builder: (_, __) => Column(
                                  mainAxisSize: MainAxisSize.min,
                                  children: [
                                    RequestButton(
                                      status: _requestNotifier.state.status,
                                      isRequesting:
                                          _requestNotifier.state.isRequesting,
                                      error: _requestNotifier.state.error,
                                      onRequest: () => _onRequest(),
                                    ),
                                    const SizedBox(height: 10),
                                    Wrap(
                                      alignment: WrapAlignment.center,
                                      crossAxisAlignment:
                                          WrapCrossAlignment.center,
                                      spacing: 6,
                                      runSpacing: 4,
                                      children: [
                                        TextButton.icon(
                                          onPressed: () => _showStatusSheet(
                                            context,
                                            state.title,
                                            _requestNotifier.state.status,
                                          ),
                                          icon: const Icon(
                                            Icons.info_outline_rounded,
                                            size: 17,
                                          ),
                                          label: Text(
                                            _requestNotifier.state.status.label,
                                          ),
                                        ),
                                        if (_canReport(
                                          _requestNotifier.state.status,
                                        ))
                                          TextButton.icon(
                                            onPressed: () =>
                                                _onReportProblem(state),
                                            icon: const Icon(
                                              Icons.flag_outlined,
                                              size: 17,
                                            ),
                                            label: const Text(
                                              'Report a problem',
                                            ),
                                          ),
                                        if (_arrLink != null)
                                          TextButton.icon(
                                            onPressed: _openInArr,
                                            icon: const Icon(
                                              Icons.open_in_new_rounded,
                                              size: 17,
                                            ),
                                            label: Text(
                                              'Open in ${_arrLink!.moduleLabel}',
                                            ),
                                          ),
                                      ],
                                    ),
                                  ],
                                ),
                              ),
                            ),
                          ),
                          const SizedBox(height: 16),

                          // Genres
                          if (state.genres.isNotEmpty)
                            Padding(
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
                              child: Wrap(
                                spacing: 6,
                                runSpacing: 6,
                                children: state.genres
                                    .map((g) => Chip(
                                          label: Text(g.name,
                                              style: const TextStyle(
                                                  fontSize: 12)),
                                          backgroundColor:
                                              AppTheme.surfaceVariant,
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
                          if (state.voteAverage != null &&
                              state.voteAverage! > 0) ...[
                            const SizedBox(height: 12),
                            Padding(
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
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
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
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
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
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
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
                              child: OutlinedButton.icon(
                                onPressed: () =>
                                    _openTrailer(state.trailerKey!),
                                icon: const Icon(Icons.play_arrow),
                                label: const Text('Watch Trailer'),
                                style: OutlinedButton.styleFrom(
                                  foregroundColor: AppTheme.textPrimary,
                                  side:
                                      const BorderSide(color: AppTheme.border),
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
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
                              child: const SectionHeader(title: 'Seasons'),
                            ),
                            const SizedBox(height: 12),
                            SeasonTable(
                              seasons: state.seasons,
                              notifier: _requestNotifier,
                              title: state.title,
                              tvdbId: state.tvDetail?.externalIds?.tvdbId,
                              canRequest: _canChooseSeasons,
                              onRequested: _bumpLibraryRefresh,
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

    final options = await _requestNotifier.fetchOptions();
    if (options != null && mounted) {
      setState(() => _requestOptions = options);
    }

    // A partially-available show: "Request More" drops the user into the
    // per-season picker below rather than the coarse season-scope sheet, so
    // they can choose exactly which missing seasons to add. Users who may not
    // choose seasons fall through to the coarse flow instead (the server
    // applies their default season scope to the missing seasons).
    if (widget.mediaType == MediaType.tv &&
        _requestNotifier.state.status == RequestStatus.partial &&
        s.seasons.isNotEmpty &&
        (options?.canChooseSeason ?? true)) {
      _scrollToSeasons();
      return;
    }

    final title = s.title;
    final tvdbId = s.tvDetail?.externalIds?.tvdbId;

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
    if (mounted && _requestNotifier.state.error == null) _bumpLibraryRefresh();
  }

  /// Tell the shell its search-chip library snapshot just went stale (the arr
  /// library changed under it), so the requested title reads "Requested" on
  /// the next search.
  void _bumpLibraryRefresh() {
    ref.read(libraryRefreshTickProvider.notifier).state++;
  }

  /// For admins, resolve whether this title already exists in the backing arr
  /// (Radarr for movies, Sonarr for TV) and, when it does, capture the matched
  /// library object + instance id so the "Open in …" affordance can push the
  /// arr detail screen. Runs on load and again after a request (via
  /// [libraryRefreshTickProvider]). Non-admins never resolve a link, and any
  /// fetch error leaves the current link untouched (quietly hidden) rather than
  /// surfacing — the affordance appears only when there's a real destination.
  Future<void> _resolveArrLink() async {
    final isAdmin = ref.read(authProvider).valueOrNull?.user?.isAdmin ?? false;
    if (!isAdmin) return;

    final backendDio = ref.read(backendClientProvider);
    final instances = ref.read(instanceProvider);
    final connection = ref.read(authProvider).valueOrNull?.connection;

    try {
      if (widget.mediaType == MediaType.movie) {
        final instanceId = instances.activeRadarrInstance?.id ??
            connection?.defaultRadarrInstance?.id;
        if (instanceId == null) return;
        final movies = await RadarrApiService(
          backendDio: backendDio,
          instanceId: instanceId,
        ).getMovies();
        if (!mounted) return;
        final match = matchRadarrMovie(movies, widget.id);
        setState(() => _arrLink = match == null
            ? null
            : ArrDeepLink(instanceId: instanceId, movie: match));
      } else {
        final instanceId = instances.activeSonarrInstance?.id ??
            connection?.defaultSonarrInstance?.id;
        if (instanceId == null) return;
        final series = await SonarrApiService(
          backendDio: backendDio,
          instanceId: instanceId,
        ).getSeries();
        if (!mounted) return;
        final match = matchSonarrSeries(
          series,
          tvdbId: _detailNotifier.state.tvDetail?.externalIds?.tvdbId,
          title: _detailNotifier.state.title,
        );
        setState(() => _arrLink = match == null
            ? null
            : ArrDeepLink(instanceId: instanceId, series: match));
      }
    } catch (_) {
      // Leave any existing link as-is; a transient fetch failure shouldn't
      // yank a working affordance.
    }
  }

  /// Pushes the matched arr detail screen (movie → Radarr, TV → Sonarr) over
  /// the root navigator, mirroring how the arr home screens open an item.
  void _openInArr() {
    final link = _arrLink;
    if (link == null) return;
    final movie = link.movie;
    final series = link.series;
    final Widget screen;
    if (movie != null) {
      screen =
          RadarrMovieDetailScreen(instanceId: link.instanceId, movie: movie);
    } else if (series != null) {
      screen =
          SonarrSeriesDetailScreen(instanceId: link.instanceId, series: series);
    } else {
      return;
    }
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(builder: (_) => screen),
    );
  }

  /// Reporting is offered only once the title is at least partially in the
  /// library (so there's a download to complain about) and the server allows
  /// it.
  bool _canReport(RequestStatus status) {
    final allow =
        ref.watch(authProvider).valueOrNull?.connection?.allowReporting ??
            false;
    if (!allow || _reportInstanceId == null) return false;
    return status == RequestStatus.available ||
        status == RequestStatus.partial ||
        status == RequestStatus.downloading;
  }

  /// Opens the report flow. For a movie, scopes directly to the movie. For TV,
  /// lets the reporter narrow to a season/episode (reusing the loaded seasons)
  /// or report the whole series.
  Future<void> _onReportProblem(MediaDetailState state) async {
    final instanceId = _reportInstanceId;
    if (instanceId == null) return;
    final title = state.title;
    if (widget.mediaType == MediaType.movie) {
      await showReportProblemSheet(
        context,
        scope: ReportScope.movie(
          instanceId: instanceId,
          tmdbId: widget.id,
          title: title,
        ),
      );
      return;
    }

    final tvdbId = state.tvDetail?.externalIds?.tvdbId;
    final scope = await _pickTvScope(state, title, tvdbId, instanceId);
    if (scope == null) return; // cancelled
    if (!mounted) return;
    await showReportProblemSheet(context, scope: scope);
  }

  /// Presents a small picker for which part of a show the report is about.
  /// Returns null if cancelled.
  Future<ReportScope?> _pickTvScope(
      MediaDetailState state, String title, int? tvdbId, String instanceId) {
    // Real seasons only (drop a season 0 / specials placeholder when empty).
    final seasons = state.seasons.where((s) => s.seasonNumber > 0).toList();
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
                leading: const Icon(Icons.tv_outlined,
                    color: AppTheme.textSecondary),
                title: const Text('The whole series',
                    style: TextStyle(color: AppTheme.textPrimary)),
                onTap: () => Navigator.of(sheetContext).pop(
                  ReportScope.series(
                    instanceId: instanceId,
                    tmdbId: widget.id,
                    tvdbId: tvdbId,
                    title: title,
                  ),
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
                              instanceId: instanceId,
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

  /// The concrete arr currently backing this media surface. An already
  /// resolved detail link is authoritative; regular requester screens fall
  /// back to the active/default instance exposed for that media type.
  String? get _reportInstanceId {
    final linked = _arrLink?.instanceId;
    if (linked != null && linked.isNotEmpty) return linked;

    final instances = ref.read(instanceProvider);
    final connection = ref.read(authProvider).valueOrNull?.connection;
    if (widget.mediaType == MediaType.movie) {
      return instances.activeRadarrInstance?.id ??
          connection?.defaultRadarrInstance?.id;
    }
    return instances.activeSonarrInstance?.id ??
        connection?.defaultSonarrInstance?.id;
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
          child: SectionHeader(title: title),
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
