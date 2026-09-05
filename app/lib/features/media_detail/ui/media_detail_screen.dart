import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/config/app_config.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_panel.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/phone_apps_sheet.dart';
import '../../../core/widgets/section_header.dart';
import '../../../core/widgets/see_all_button.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/logic/browse_query.dart';
import '../../issues/logic/issues_provider.dart';
import '../../issues/ui/report_problem_sheet.dart';
import '../../media_access/data/media_access_service.dart';
import '../../media_download/data/media_download_models.dart';
import '../../media_download/ui/media_download_button.dart';
import '../../person/ui/person_detail_sheet.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../radarr/data/radarr_models.dart';
import '../../radarr/ui/radarr_movie_detail_screen.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/request_provider.dart';
import '../../request/ui/request_button.dart';
import '../../request/ui/request_options_sheet.dart';
import '../../request/ui/request_status_sheet.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../../sonarr/data/sonarr_models.dart';
import '../../sonarr/ui/sonarr_series_detail_screen.dart';
import '../logic/arr_deep_link.dart';
import '../logic/media_detail_provider.dart';
import '../logic/title_links.dart';
import '../logic/release_schedule.dart';
import '../logic/release_window.dart';
import '../logic/title_facts.dart';
import 'cast_crew_sheet.dart';
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
  /// Opens a browse grid anchored on this title or on one of its genres.
  void _browse(
    BrowseFeed feed, {
    required String title,
    BrowseFilters filters = BrowseFilters.none,
  }) =>
      context.push(
        BrowseQuery(
          type: widget.mediaType,
          feed: feed,
          id: feed.needsId ? widget.id : null,
          title: title,
          filters: filters,
        ).toLocation(),
      );

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

  /// Exact live files exposed as requester downloads. These are resolved from
  /// the active arr instead of inferred from request status, since admins may
  /// change or replace files directly in Radarr/Sonarr at any time.
  RadarrMovieFile? _downloadMovieFile;
  List<SonarrEpisode> _downloadEpisodes = const [];
  String? _downloadInstanceId;
  int _arrResolveGeneration = 0;

  /// Where this title can be watched on the user's media servers, as they
  /// answered just now; empty until the title is there to watch (available
  /// or partial) and a server has answered.
  List<WatchLink> _watchLinks = const [];
  int _watchGeneration = 0;
  RequestStatus? _watchedStatus;

  /// The library this screen currently reads and requests against; null means
  /// the user's default. Only ever set when the connection exposes more than
  /// one library for this media type (multi-grant users, or admins).
  String? _selectedLibraryId;

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
    // needs the show's TVDB id, which only lands after the detail loads. The
    // watch links need the same detail (year, title, TVDB id) and the request
    // status, so they resolve on whichever lands last.
    _detailNotifier.load().then((_) {
      if (!mounted) return;
      _resolveArrLink();
      _resolveWatchLinks();
    });
    _requestNotifier.addListener(_onRequestStateChanged);
    _requestNotifier.checkStatus();
    _loadMyOpenReport();
    if (widget.mediaType == MediaType.tv) {
      _requestNotifier.fetchOptions().then((opts) {
        if (mounted && opts != null) setState(() => _requestOptions = opts);
      });
    }
  }

  @override
  void dispose() {
    _requestNotifier.removeListener(_onRequestStateChanged);
    super.dispose();
  }

  /// Re-asks the media servers when the request status moves, and only then:
  /// the notifier also fires for option loads and request writes, which
  /// change nothing about where the title can be watched.
  void _onRequestStateChanged() {
    if (_requestNotifier.state.status != _watchedStatus) _resolveWatchLinks();
  }

  /// Whether the user may pick specific seasons. Defaults to true (the
  /// server's out-of-the-box global setting) until the options load.
  bool get _canChooseSeasons => _requestOptions?.canChooseSeason ?? true;

  /// The libraries this user may aim requests at for this media type, from
  /// the per-user filtered connection (granted set for requesters, every
  /// instance for admins). Names are the admin-chosen instance names.
  List<LibraryChoice> get _libraryChoices {
    final connection = ref.read(authProvider).valueOrNull?.connection;
    if (connection == null) return const [];
    final instances = widget.mediaType == MediaType.movie
        ? connection.radarrInstances
        : connection.sonarrInstances;
    return instances
        .map((i) => LibraryChoice(id: i.id, name: i.name))
        .toList();
  }

  /// The connection's default library for this media type.
  String? get _defaultLibraryId {
    final connection = ref.read(authProvider).valueOrNull?.connection;
    return widget.mediaType == MediaType.movie
        ? connection?.defaultRadarrInstance?.id
        : connection?.defaultSonarrInstance?.id;
  }

  /// The library id the screen is effectively reading: the explicit selection,
  /// else the connection's default for this media type.
  String? get _effectiveLibraryId => _selectedLibraryId ?? _defaultLibraryId;

  /// Switches every read and write on this screen to [libraryId] and
  /// refreshes what depends on it.
  void _selectLibrary(String? libraryId) {
    if (_selectedLibraryId == libraryId) return;
    setState(() => _selectedLibraryId = libraryId);
    _requestNotifier.instanceId = libraryId;
    _requestNotifier.checkStatus();
    _resolveArrLink();
  }

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
    // both bump this tick) — re-check so the admin "Open in …" link appears,
    // and ask the media servers again.
    ref.listen(libraryRefreshTickProvider, (_, __) {
      _resolveArrLink();
      _resolveWatchLinks();
    });
    // Resolve (or re-resolve) the admin link once auth settles — covers auth
    // landing after the initial detail load (e.g. an optimistic reconnect).
    ref.listen(authProvider, (_, __) => _resolveArrLink());
    ref.listen(
      instanceProvider.select((s) => s.activeRadarrInstanceId),
      (_, __) => _resolveArrLink(),
    );
    ref.listen(
      instanceProvider.select((s) => s.activeSonarrInstanceId),
      (_, __) => _resolveArrLink(),
    );
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

        // A title the server keeps from this account is an answer, not a
        // failure: a plain line rather than the red error.
        if (state.titleUnavailable &&
            state.movieDetail == null &&
            state.tvDetail == null) {
          return Scaffold(
            appBar: AppBar(),
            body: const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text(
                  "This title isn't available on this account.",
                  textAlign: TextAlign.center,
                  style: TextStyle(color: AppTheme.textSecondary),
                ),
              ),
            ),
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
                  year: tmdbPremiereYear(state.movieDetail?.releaseDate ??
                      state.tvDetail?.firstAirDate),
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
                              // Secondary actions land async (request status,
                              // admin arr-link resolution), so the dock height
                              // morphs instead of snapping when one appears.
                              child: AnimatedSize(
                                duration: const Duration(milliseconds: 220),
                                curve: Curves.easeOutCubic,
                                alignment: Alignment.topCenter,
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
                                      // Sits with the status, not down in the
                                      // facts: its whole job is to explain a
                                      // badge that would otherwise read as a
                                      // stalled download. Empty for TV, for
                                      // titles not in the library, and once
                                      // every date has passed.
                                      _PendingReleaseLine(
                                        releases:
                                            _requestNotifier.state.releases,
                                        status: _requestNotifier.state.status,
                                      ),
                                      // One chip per granted library when the
                                      // user holds more than one (HD vs 4K):
                                      // each carries that library's own
                                      // status, and tapping one retargets the
                                      // whole screen — status, request,
                                      // downloads — at that library.
                                      _LibraryStatusChips(
                                        statuses: _requestNotifier
                                            .state.instanceStatuses,
                                        libraries: _libraryChoices,
                                        selectedId: _effectiveLibraryId,
                                        onSelect: (id) => _selectLibrary(
                                            id == _defaultLibraryId
                                                ? null
                                                : id),
                                      ),
                                      const SizedBox(height: 10),
                                      Wrap(
                                        alignment: WrapAlignment.center,
                                        crossAxisAlignment:
                                            WrapCrossAlignment.center,
                                        spacing: 6,
                                        runSpacing: 4,
                                        children: [
                                          if (_requestNotifier.state.status !=
                                              RequestStatus.available)
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
                                                _requestNotifier
                                                    .state.status.label,
                                              ),
                                            ),
                                          if (_myOpenReportId != null)
                                            TextButton.icon(
                                              onPressed: () => context.push(
                                                  '/issues/$_myOpenReportId'),
                                              icon: const Icon(
                                                Icons.flag,
                                                size: 17,
                                              ),
                                              label: const Text(
                                                'View your report',
                                              ),
                                            )
                                          else if (_canReport(
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
                                          if (widget.mediaType ==
                                                  MediaType.movie &&
                                              _downloadInstanceId != null &&
                                              _downloadMovieFile != null)
                                            MediaDownloadButton(
                                              instanceId:
                                                  _downloadInstanceId!,
                                              fileId:
                                                  _downloadMovieFile!.id,
                                              label: 'Download movie',
                                              reportedPath:
                                                  _downloadMovieFile!.path,
                                            ),
                                          for (final link in _watchLinks)
                                            if (link.state ==
                                                WatchLinkState.found)
                                              TextButton.icon(
                                                onPressed: () =>
                                                    _openWatchLink(link),
                                                icon: const Icon(
                                                  Icons.play_circle_outline,
                                                  size: 17,
                                                ),
                                                label: Text(
                                                  'Watch on ${_watchLabel(link)}',
                                                ),
                                              )
                                            else if (link.fallbackUrl.isNotEmpty)
                                              TextButton.icon(
                                                onPressed: () => _openWatchLink(
                                                  link,
                                                  fallback: true,
                                                ),
                                                icon: const Icon(
                                                  Icons.open_in_new_rounded,
                                                  size: 17,
                                                ),
                                                label: Text(
                                                  'Open ${_watchLabel(link)}',
                                                ),
                                              )
                                            else if (link.state ==
                                                WatchLinkState.missing)
                                              TextButton.icon(
                                                onPressed: null,
                                                icon: const Icon(
                                                  Icons.hourglass_empty,
                                                  size: 17,
                                                ),
                                                label: Text(
                                                  'Not on ${_watchLabel(link)} yet',
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
                          ),
                          const SizedBox(height: 16),

                          // Genres: each opens the Browse grid for that genre.
                          if (state.genres.isNotEmpty)
                            Padding(
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
                              child: Wrap(
                                spacing: 6,
                                runSpacing: 6,
                                children: state.genres
                                    .map((g) => ActionChip(
                                          label: Text(g.name,
                                              style: const TextStyle(
                                                  fontSize: 12)),
                                          tooltip: 'Browse ${g.name}',
                                          backgroundColor:
                                              AppTheme.surfaceVariant,
                                          side: const BorderSide(
                                              color: AppTheme.border),
                                          materialTapTargetSize:
                                              MaterialTapTargetSize.shrinkWrap,
                                          visualDensity: VisualDensity.compact,
                                          onPressed: () => _browse(
                                            BrowseFeed.discover,
                                            title: g.name,
                                            filters: BrowseFilters(
                                                genreIds: [g.id]),
                                          ),
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

                          // Cast: billing order, a person sheet per tap,
                          // See all for everyone credited.
                          if (state.credits.cast.isNotEmpty) ...[
                            const SizedBox(height: 24),
                            _CastRow(
                              cast: state.credits.cast,
                              onSeeAll: () => showCastCrewSheet(
                                context,
                                title: state.title,
                                cast: state.credits.cast,
                                crew: state.crew,
                              ),
                            ),
                          ],

                          // Details: the few lines worth reading before
                          // deciding, never a spec sheet. Only known lines
                          // render; the Links line closes it with the
                          // title's own pages elsewhere (TMDB's is always
                          // known, so the section is there once loaded).
                          if (state.facts.isNotEmpty ||
                              state.studios.isNotEmpty ||
                              state.links.isNotEmpty) ...[
                            const SizedBox(height: 24),
                            Padding(
                              padding:
                                  const EdgeInsets.symmetric(horizontal: 16),
                              child: _DetailsSection(
                                facts: state.facts,
                                studios: state.studios,
                                onStudio: (studio) => _browse(
                                  BrowseFeed.discover,
                                  title: studio.name!,
                                  filters:
                                      BrowseFilters(companies: [studio]),
                                ),
                                links: state.links,
                                onLink: _openTitleLink,
                              ),
                            ),
                          ],

                          // Release dates: TMDB-backed cinema/digital/disc
                          // schedule for any movie, library or not. See D-02
                          // in the plan — this is deliberately separate from
                          // _PendingReleaseLine in the request dock above.
                          // The region is the device's, read the way the
                          // browse screen reads it: a widget-level locale
                          // here is always en_US.
                          if (state.movieDetail != null)
                            Padding(
                              padding: const EdgeInsets.symmetric(
                                  horizontal: 16),
                              child: _ReleaseDatesSection(
                                regions: state.movieDetail!.releaseDates,
                                deviceRegion: watchRegionFor(WidgetsBinding
                                    .instance
                                    .platformDispatcher
                                    .locale
                                    .countryCode),
                              ),
                            ),

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
                              onRequested: _onRequestSucceeded,
                              downloadInstanceId: _downloadInstanceId,
                              downloadChoicesBySeason:
                                  _episodeDownloadChoicesBySeason,
                            ),
                          ],

                          // Recommendations
                          if (state.recommendations.isNotEmpty) ...[
                            const SizedBox(height: 24),
                            _SectionRow(
                              title: 'Recommended',
                              items: state.recommendations,
                              onSeeAll: () => _browse(
                                BrowseFeed.recommendations,
                                title: 'Recommended',
                              ),
                            ),
                          ],

                          // Similar
                          if (state.similar.isNotEmpty) ...[
                            const SizedBox(height: 24),
                            _SectionRow(
                              title: 'Similar',
                              items: state.similar,
                              onSeeAll: () =>
                                  _browse(BrowseFeed.similar, title: 'Similar'),
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

    // A multi-library user gets the sheet even with season/quality choice
    // off: the library IS a choice.
    final libraries = _libraryChoices;
    final hasChoices =
        (options != null && options.hasChoices) || libraries.length > 1;

    String? seasonScope;
    int? qualityProfileId;
    if (hasChoices) {
      if (!mounted) return;
      final result = await showAppSheet<RequestOptionsResult>(
        context,
        builder: (_) => RequestOptionsSheet(
          options: options ??
              const RequestOptions(
                canChooseSeason: false,
                canChooseQuality: false,
                defaultSeasonScope: SeasonScope.all,
                qualityProfiles: [],
              ),
          libraries: libraries,
          selectedLibraryId: _effectiveLibraryId,
          onLibraryOptions: (libraryId) =>
              _requestNotifier.fetchOptions(libraryId: libraryId),
        ),
      );
      if (result == null) return; // cancelled
      seasonScope = result.seasonScope;
      qualityProfileId = result.qualityProfileId;
      if (result.instanceId != null &&
          result.instanceId != _selectedLibraryId) {
        // Adopt the selection without an immediate status refetch — the
        // submit below produces the authoritative status, and a racing
        // pre-request read could land after it and overwrite it.
        setState(() => _selectedLibraryId = result.instanceId);
        _requestNotifier.instanceId = result.instanceId;
        _resolveArrLink();
      }
    }

    await _requestNotifier.request(
      title: title,
      tvdbId: tvdbId,
      seasonScope: seasonScope,
      qualityProfileId: qualityProfileId,
    );
    if (mounted && _requestNotifier.state.error == null) _onRequestSucceeded();
  }

  /// Every successful submission (coarse request or per-season table) lands
  /// here: refresh the library snapshot, then offer the phone apps — a no-op
  /// everywhere but the first success on a build that shows them.
  void _onRequestSucceeded() {
    _bumpLibraryRefresh();
    unawaited(maybeShowPhoneAppsPrompt(context));
  }

  /// Tell the shell its search-chip library snapshot just went stale (the arr
  /// library changed under it), so the requested title reads "Requested" on
  /// the next search.
  void _bumpLibraryRefresh() {
    ref.read(libraryRefreshTickProvider.notifier).state++;
  }

  /// Resolves this title against the active arr. Admins get a deep link into
  /// the matched record; any user gets exact live file downloads when the
  /// server advertises that capability. Runs again after requests, auth
  /// changes, and instance switches so stale files are never offered.
  Future<void> _resolveArrLink() async {
    final generation = ++_arrResolveGeneration;
    final auth = ref.read(authProvider).valueOrNull;
    final isAdmin = auth?.user?.isAdmin ?? false;
    final backendDio = ref.read(backendClientProvider);
    final instances = ref.read(instanceProvider);
    final connection = auth?.connection;

    if (mounted) {
      setState(() {
        _arrLink = null;
        _downloadMovieFile = null;
        _downloadEpisodes = const [];
        _downloadInstanceId = null;
      });
    }
    try {
      if (widget.mediaType == MediaType.movie) {
        final instanceId = _selectedLibraryId ??
            instances.activeRadarrInstance?.id ??
            connection?.defaultRadarrInstance?.id;
        if (instanceId == null) return;
        final downloadsEnabled =
            connection?.mediaDownloadsEnabledFor(instanceId) ?? false;
        if (!isAdmin && !downloadsEnabled) return;
        final service = RadarrApiService(
          backendDio: backendDio,
          instanceId: instanceId,
        );
        final movies = await service.getMovies();
        var match = matchRadarrMovie(movies, widget.id);
        if (downloadsEnabled && match != null && match.id > 0) {
          try {
            final detail = await service.getMovieById(match.id);
            if (detail.id == match.id) match = detail;
          } catch (_) {
            // The live list may already carry movieFile. Keep that exact
            // response as a fallback when Radarr's detail endpoint is down.
          }
        }
        if (!mounted || generation != _arrResolveGeneration) return;
        final file = match?.movieFile;
        setState(() {
          _arrLink = isAdmin && match != null
              ? ArrDeepLink(instanceId: instanceId, movie: match)
              : null;
          if (downloadsEnabled && file != null && file.id > 0) {
            _downloadMovieFile = file;
            _downloadInstanceId = instanceId;
          }
        });
      } else {
        final instanceId = _selectedLibraryId ??
            instances.activeSonarrInstance?.id ??
            connection?.defaultSonarrInstance?.id;
        if (instanceId == null) return;
        final downloadsEnabled =
            connection?.mediaDownloadsEnabledFor(instanceId) ?? false;
        if (!isAdmin && !downloadsEnabled) return;
        final service = SonarrApiService(
          backendDio: backendDio,
          instanceId: instanceId,
        );
        final series = await service.getSeries();
        final match = matchSonarrSeries(
          series,
          tvdbId: _detailNotifier.state.tvDetail?.externalIds?.tvdbId,
          tmdbId: widget.id,
          title: _detailNotifier.state.title,
          year: tmdbPremiereYear(_detailNotifier.state.tvDetail?.firstAirDate),
        );
        var episodes = const <SonarrEpisode>[];
        if (downloadsEnabled && match != null && match.id > 0) {
          episodes = (await service.getEpisodes(
            match.id,
            includeEpisodeFile: true,
          ))
              .where((episode) =>
                  episode.hasFile && episode.episodeFileId > 0)
              .toList()
            ..sort((left, right) {
              final season =
                  left.seasonNumber.compareTo(right.seasonNumber);
              return season != 0
                  ? season
                  : left.episodeNumber.compareTo(right.episodeNumber);
            });
        }
        if (!mounted || generation != _arrResolveGeneration) return;
        setState(() {
          _arrLink = isAdmin && match != null
              ? ArrDeepLink(instanceId: instanceId, series: match)
              : null;
          if (downloadsEnabled && episodes.isNotEmpty) {
            _downloadEpisodes = episodes;
            _downloadInstanceId = instanceId;
          }
        });
      }
    } catch (_) {
      // File/link discovery is an optional affordance. Keep the safely-cleared
      // state when the active arr is unavailable instead of exposing stale IDs.
    }
  }

  Map<int, List<MediaDownloadChoice>> get
      _episodeDownloadChoicesBySeason {
    final result = <int, List<MediaDownloadChoice>>{};
    for (final episode in _downloadEpisodes) {
      final title = episode.title?.trim();
      final file = episode.episodeFile;
      final details = <String>[
        if (file?.quality?.trim().isNotEmpty ?? false) file!.quality!.trim(),
        if (file != null && file.size > 0) file.sizeFormatted,
      ];
      (result[episode.seasonNumber] ??= []).add(MediaDownloadChoice(
        fileId: episode.episodeFileId,
        label: title == null || title.isEmpty
            ? episode.seasonEpisodeLabel
            : '${episode.seasonEpisodeLabel} · $title',
        subtitle: details.isEmpty ? null : details.join(' · '),
        reportedPath: file?.path,
      ));
    }
    return result;
  }

  /// Asks the media servers the user can watch on where this title is, once
  /// the request status says it is there to watch (available or partial) and
  /// the TMDB detail is in (the year, the title, and a show's TVDB id narrow
  /// the lookup; the match is by provider id). Best-effort like the arr
  /// link: a failed read clears the links rather than showing stale ones.
  /// Nothing is asked when the user has no media server at all.
  Future<void> _resolveWatchLinks() async {
    final generation = ++_watchGeneration;
    final status = _requestNotifier.state.status;
    _watchedStatus = status;
    final connection = ref.read(authProvider).valueOrNull?.connection;
    final watchable = status == RequestStatus.available ||
        status == RequestStatus.partial;
    final askable = connection?.mediaServerInstances.isNotEmpty ?? false;
    final detail = _detailNotifier.state;
    if (!watchable || !askable) {
      if (_watchLinks.isNotEmpty) setState(() => _watchLinks = const []);
      return;
    }
    if (detail.movieDetail == null && detail.tvDetail == null) return;
    try {
      final links = await ref.read(mediaAccessServiceProvider).watchLinks(
            mediaType: widget.mediaType,
            tmdbId: widget.id,
            tvdbId: detail.tvDetail?.externalIds?.tvdbId,
            year: widget.mediaType == MediaType.movie
                ? tmdbPremiereYear(detail.movieDetail?.releaseDate)
                : tmdbPremiereYear(detail.tvDetail?.firstAirDate),
            title: detail.title,
          );
      if (!mounted || generation != _watchGeneration) return;
      setState(() => _watchLinks = links);
    } catch (_) {
      if (!mounted || generation != _watchGeneration) return;
      setState(() => _watchLinks = const []);
    }
  }

  /// "Jellyfin" when one server of that type answered, the instance's own
  /// name when two of the same type did.
  String _watchLabel(WatchLink link) {
    final sameType = _watchLinks
        .where((other) => other.serviceType == link.serviceType)
        .length;
    return sameType > 1 ? link.name : mediaServerTypeLabel(link.serviceType);
  }

  /// Opens the title's page on the media server, in the browser or the
  /// server's app when it claims the address.
  Future<void> _openWatchLink(WatchLink link, {bool fallback = false}) async {
    final uri = Uri.tryParse(fallback ? link.fallbackUrl : link.url);
    var opened = false;
    if (uri != null) {
      try {
        opened = await launchUrl(uri, mode: LaunchMode.externalApplication);
      } catch (_) {
        opened = false;
      }
    }
    if (opened || !mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text("Couldn't open ${_watchLabel(link)}."),
    ));
  }

  /// Opens the title's own page on an outside site (IMDb, TMDB, Trakt) in
  /// the browser, or the site's app when it claims the address, and says so
  /// when nothing could open it.
  Future<void> _openTitleLink(TitleLink link) async {
    var opened = false;
    try {
      opened = await launchUrl(Uri.parse(link.url),
          mode: LaunchMode.externalApplication);
    } catch (_) {
      opened = false;
    }
    if (opened || !mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text("Couldn't open ${link.label}."),
    ));
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

  /// The caller's own OPEN report for this exact title, when one exists — the
  /// detail page then offers the thread instead of a duplicate report. Loaded
  /// once per screen from the self-scoped inbox; best-effort (a failed read
  /// just leaves the plain Report button).
  int? _myOpenReportId;

  Future<void> _loadMyOpenReport() async {
    try {
      final mine = await ref.read(issuesServiceProvider).listMyIssues();
      if (!mounted) return;
      final wantType = widget.mediaType == MediaType.movie ? 'movie' : 'tv';
      for (final issue in mine) {
        if (issue.closedAt == null &&
            issue.mediaType == wantType &&
            issue.tmdbId == widget.id) {
          setState(() => _myOpenReportId = issue.id);
          return;
        }
      }
    } catch (_) {
      // The chip is a convenience; the report flow works without it.
    }
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
    return showAppSheet<ReportScope>(
      context,
      builder: (sheetContext) {
        return AppSheet(
          padding: EdgeInsets.zero,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Padding(
                padding: EdgeInsets.fromLTRB(20, 0, 20, 8),
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
              for (final s in seasons)
                ListTile(
                  leading: const Icon(Icons.video_library_outlined,
                      color: AppTheme.textSecondary),
                  title: Text('Season ${s.seasonNumber}',
                      style: const TextStyle(color: AppTheme.textPrimary)),
                  trailing: (s.episodeCount ?? 0) > 0
                      ? const Icon(Icons.chevron_right,
                          color: AppTheme.textSecondary, size: 18)
                      : null,
                  onTap: () async {
                    // A season with a known episode list narrows once more —
                    // "wrong episode" deserves an episode-shaped report, and a
                    // tighter scope is a cheaper diagnosis. No count, no
                    // second step.
                    final episodes = s.episodeCount ?? 0;
                    if (episodes <= 0) {
                      Navigator.of(sheetContext).pop(
                        ReportScope.series(
                          instanceId: instanceId,
                          tmdbId: widget.id,
                          tvdbId: tvdbId,
                          seasonNumber: s.seasonNumber,
                          title: title,
                        ),
                      );
                      return;
                    }
                    final scope = await _pickSeasonScope(
                        sheetContext, s.seasonNumber, episodes,
                        title: title, tvdbId: tvdbId, instanceId: instanceId);
                    if (scope != null && sheetContext.mounted) {
                      Navigator.of(sheetContext).pop(scope);
                    }
                  },
                ),
              const SizedBox(height: 8),
            ],
          ),
        );
      },
    );
  }

  /// The second picker step for one season: the whole season, or one episode.
  Future<ReportScope?> _pickSeasonScope(
      BuildContext parentContext, int seasonNumber, int episodeCount,
      {required String title, int? tvdbId, required String instanceId}) {
    return showAppSheet<ReportScope>(
      parentContext,
      builder: (sheetContext) {
        return AppSheet(
          padding: EdgeInsets.zero,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding: const EdgeInsets.fromLTRB(20, 0, 20, 8),
                child: Text(
                  'Season $seasonNumber — which part?',
                  style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 16,
                      fontWeight: FontWeight.bold),
                ),
              ),
              ListTile(
                leading: const Icon(Icons.video_library_outlined,
                    color: AppTheme.textSecondary),
                title: Text('All of Season $seasonNumber',
                    style: const TextStyle(color: AppTheme.textPrimary)),
                onTap: () => Navigator.of(sheetContext).pop(
                  ReportScope.series(
                    instanceId: instanceId,
                    tmdbId: widget.id,
                    tvdbId: tvdbId,
                    seasonNumber: seasonNumber,
                    title: title,
                  ),
                ),
              ),
              Flexible(
                child: SingleChildScrollView(
                  child: Padding(
                    padding: const EdgeInsets.fromLTRB(20, 4, 20, 8),
                    child: Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: [
                        for (var e = 1; e <= episodeCount; e++)
                          ActionChip(
                            label: Text('E$e'),
                            onPressed: () => Navigator.of(sheetContext).pop(
                              ReportScope.episode(
                                instanceId: instanceId,
                                tmdbId: widget.id,
                                tvdbId: tvdbId,
                                seasonNumber: seasonNumber,
                                episodeNumber: e,
                                title: title,
                              ),
                            ),
                          ),
                      ],
                    ),
                  ),
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
    if (_selectedLibraryId != null) return _selectedLibraryId;

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
    showAppSheet(
      context,
      builder: (_) => RequestStatusSheet(
        title: title,
        status: status,
        seasons: _requestNotifier.state.seasons,
      ),
    );
  }
}

/// The full release schedule for a movie: cinema, digital and disc dates
/// TMDB knows for the resolved region, for any movie whether or not it is in
/// a library. Renders nothing at all — not even a header — when
/// [resolveReleaseSchedule] finds no shown milestone anywhere in the
/// payload, matching the shrink-to-nothing discipline `_PendingReleaseLine`
/// already uses below: the page never asserts a schedule it does not have.
class _ReleaseDatesSection extends StatelessWidget {
  final List<TmdbReleaseDateRegion> regions;

  /// The device's country, tried first; `resolveReleaseSchedule` falls
  /// back from it the same way whatever it is.
  final String deviceRegion;

  const _ReleaseDatesSection({
    required this.regions,
    required this.deviceRegion,
  });

  @override
  Widget build(BuildContext context) {
    final schedule = resolveReleaseSchedule(
      regions,
      preferredRegion: deviceRegion,
    );
    if (schedule == null) return const SizedBox.shrink();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // The leading gap lives inside the Column, past the early return
        // above, so a movie with no resolvable schedule leaves no dead space
        // between the trailer button and whatever follows.
        const SizedBox(height: 24),
        SectionHeader(
          title: 'Release dates',
          trailing: Text(
            schedule.regionCode,
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
            ),
          ),
        ),
        const SizedBox(height: 12),
        ...schedule.milestones.map((m) => Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Row(
                children: [
                  Icon(_iconFor(m.type), size: 18, color: AppTheme.textSecondary),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(
                      m.label,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 14,
                      ),
                    ),
                  ),
                  Text(
                    formatReleaseDate(m.date),
                    style: TextStyle(
                      color: m.isUpcoming
                          ? AppTheme.accent
                          : AppTheme.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ],
              ),
            )),
      ],
    );
  }

  /// Keyed on the TMDB release type, not the display label — rewording a
  /// label must never silently drop a row back to the generic icon.
  IconData _iconFor(int type) {
    switch (type) {
      case 2:
      case 3:
        return Icons.theaters_outlined;
      case 4:
        return Icons.play_circle_outline;
      case 5:
        return Icons.album_outlined;
      default:
        return Icons.event_outlined;
    }
  }
}

/// The release milestones a movie hasn't reached yet, e.g.
/// "In cinemas Jun 12 • Digital Sep 4".
///
/// Renders nothing at all — not even padding — when there is nothing still
/// ahead, so the request dock keeps its current shape for the overwhelmingly
/// common case of an already-released title.
class _PendingReleaseLine extends StatelessWidget {
  final MovieReleaseDates releases;
  final RequestStatus status;

  const _PendingReleaseLine({required this.releases, required this.status});

  @override
  Widget build(BuildContext context) {
    final pending = pendingReleases(releases, status: status);
    if (pending.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(top: 8),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          const Icon(Icons.event_outlined,
              size: 15, color: AppTheme.textSecondary),
          const SizedBox(width: 6),
          Flexible(
            child: Text(
              formatPendingReleases(pending),
              textAlign: TextAlign.center,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

/// One chip per granted library, each carrying that library's own status
/// ("Movies · Available", "4K Movies · Not Available"). Renders nothing —
/// not even padding — unless the server reported statuses for more than one
/// library, so single-library users keep today's dock exactly.
class _LibraryStatusChips extends StatelessWidget {
  final Map<String, RequestStatus> statuses;
  final List<LibraryChoice> libraries;
  final String? selectedId;
  final ValueChanged<String> onSelect;

  const _LibraryStatusChips({
    required this.statuses,
    required this.libraries,
    required this.selectedId,
    required this.onSelect,
  });

  @override
  Widget build(BuildContext context) {
    if (statuses.length < 2) return const SizedBox.shrink();
    // Connection order (the admin's sort order); ignore libraries the server
    // reported no status for.
    final chips = libraries
        .where((library) => statuses.containsKey(library.id))
        .toList();
    if (chips.length < 2) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.only(top: 10),
      child: Wrap(
        alignment: WrapAlignment.center,
        spacing: 8,
        runSpacing: 8,
        children: chips.map((library) {
          final selected = library.id == selectedId;
          return ChoiceChip(
            label: Text('${library.name} · ${statuses[library.id]!.label}'),
            selected: selected,
            onSelected: (_) => onSelect(library.id),
            showCheckmark: false,
            selectedColor: AppTheme.accent,
            backgroundColor: AppTheme.surfaceVariant,
            labelStyle: TextStyle(
              color: selected ? AppTheme.onAccent : AppTheme.textPrimary,
              fontSize: 12,
            ),
            side: const BorderSide(color: AppTheme.border),
          );
        }).toList(),
      ),
    );
  }
}

class _SectionRow extends StatelessWidget {
  final String title;
  final List<MediaItem> items;

  /// Opens the row's feed as a full grid that pages past this first page.
  final VoidCallback? onSeeAll;

  const _SectionRow({
    required this.title,
    required this.items,
    this.onSeeAll,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: SectionHeader(
            title: title,
            trailing: onSeeAll == null
                ? null
                : SeeAllButton(rowTitle: title, onPressed: onSeeAll!),
          ),
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

/// The Cast row: billing order, a person sheet per tap, See all for the
/// whole cast and crew. Same footprint as [_SectionRow] so the page keeps
/// one rhythm.
class _CastRow extends StatelessWidget {
  final List<CastMember> cast;
  final VoidCallback onSeeAll;

  /// How much of the billing the row itself carries; the sheet has the rest.
  static const shown = 20;

  const _CastRow({required this.cast, required this.onSeeAll});

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: SectionHeader(
            title: 'Cast',
            trailing: SeeAllButton(rowTitle: 'Cast', onPressed: onSeeAll),
          ),
        ),
        const SizedBox(height: 12),
        HorizontalItemRow<CastMember>(
          items: cast.take(shown).toList(),
          isLoading: false,
          itemBuilder: (member) => _CastCard(
            member: member,
            onTap: () => showPersonDetailSheet(
              context,
              personId: member.id,
              personName: member.name,
              profilePath: member.profilePath,
            ),
          ),
        ),
      ],
    );
  }
}

/// One person in the Cast row: a 2:3 headshot (TMDB profiles share the
/// poster ratio), their name, and who they played.
class _CastCard extends StatelessWidget {
  final CastMember member;
  final VoidCallback onTap;

  const _CastCard({required this.member, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final path = member.profilePath;
    return SizedBox(
      width: 100,
      child: InkWell(
        borderRadius: BorderRadius.circular(12),
        onTap: onTap,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(12),
              child: SizedBox(
                width: 100,
                height: 150,
                child: CachedImage(
                  url: path == null
                      ? null
                      : AppConfig.tmdbPoster(path, width: 185),
                  fit: BoxFit.cover,
                  icon: Icons.person,
                  iconSize: 28,
                ),
              ),
            ),
            const SizedBox(height: 6),
            Text(
              member.name,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                fontWeight: FontWeight.w600,
              ),
            ),
            if (member.character case final character?)
              Text(
                character,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 12,
                ),
              ),
          ],
        ),
      ),
    );
  }
}

/// The Details section: one label/value row per known fact, in the
/// release-dates row style, plus the studios as chips that open the browse
/// grid for that studio.
class _DetailsSection extends StatelessWidget {
  final List<TitleFact> facts;
  final List<TaggedId> studios;
  final ValueChanged<TaggedId> onStudio;
  final List<TitleLink> links;
  final ValueChanged<TitleLink> onLink;

  const _DetailsSection({
    required this.facts,
    required this.studios,
    required this.onStudio,
    required this.links,
    required this.onLink,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const SectionHeader(title: 'Details'),
        const SizedBox(height: 12),
        for (final fact in facts)
          _row(
            fact.label,
            Text(
              fact.value,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 14,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
        if (studios.isNotEmpty)
          _row(
            'Studio',
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final studio in studios)
                  ActionChip(
                    label: Text(studio.name!,
                        style: const TextStyle(fontSize: 12)),
                    tooltip: 'Browse ${studio.name}',
                    backgroundColor: AppTheme.surfaceVariant,
                    side: const BorderSide(color: AppTheme.border),
                    materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    visualDensity: VisualDensity.compact,
                    onPressed: () => onStudio(studio),
                  ),
              ],
            ),
          ),
        // Outbound, unlike the studio chips, and marked as such.
        if (links.isNotEmpty)
          _row(
            'Links',
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                for (final link in links)
                  ActionChip(
                    avatar: const Icon(Icons.open_in_new,
                        size: 14, color: AppTheme.textSecondary),
                    label: Text(link.label,
                        style: const TextStyle(fontSize: 12)),
                    tooltip: 'Open on ${link.label}',
                    backgroundColor: AppTheme.surfaceVariant,
                    side: const BorderSide(color: AppTheme.border),
                    materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    visualDensity: VisualDensity.compact,
                    onPressed: () => onLink(link),
                  ),
              ],
            ),
          ),
      ],
    );
  }

  Widget _row(String label, Widget value) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 96,
              child: Text(
                label,
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 14,
                ),
              ),
            ),
            Expanded(child: value),
          ],
        ),
      );
}
