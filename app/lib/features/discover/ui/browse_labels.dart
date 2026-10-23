import '../../chaptarr/logic/book_publication.dart';
import 'package:intl/intl.dart';
import '../logic/browse_query.dart';
import '../data/tmdb_models.dart';
import '../../media_detail/logic/season_label.dart';
import '../../media_detail/logic/release_window.dart';
import 'package:flutter/widgets.dart';
import 'package:cantinarr/l10n/l10n.dart';

/// Localizes legacy model display labels at the widget boundary. Wire values,
/// catalog metadata, saved filters, and provider queries remain unchanged.
String browseLabel(BuildContext context, String label) {
  final l = context.l10n;
  final translated = switch (label) {
    'ALL' => l.browseALL,
    'About this album' => l.browseAboutThisAlbum,
    'About this book' => l.browseAboutThisBook,
    'Acting' => l.browseActing,
    'Add a keyword' => l.browseAddAKeyword,
    'Add a studio' => l.browseAddAStudio,
    'Admins have unlimited requests. These rules apply if the account becomes a User.' =>
      l.browseAdminsHaveUnlimitedRequestsTheseRulesApplyIfTheAccountBecomesAUser,
    'Affected requests' => l.browseAffectedRequests,
    'After setup, it appears for users assigned to its service.' =>
      l.browseAfterSetupItAppearsForUsersAssignedToItsService,
    'Airing' => l.browseAiring,
    'Airing Next' => l.browseAiringNext,
    'Airing This Week' => l.browseAiringThisWeek,
    'Album' => l.browseAlbum,
    'Album details' => l.browseAlbumDetails,
    'Album details could not be loaded.' =>
      l.browseAlbumDetailsCouldNotBeLoaded,
    'Albums' => l.browseAlbums,
    'Albums and EPs · MusicBrainz matching order' =>
      l.browseAlbumsAndEPsMusicBrainzMatchingOrder,
    'All' => l.browseAll,
    'All credits' => l.browseAllCredits,
    'All movies' => l.browseAllMovies,
    'All shows' => l.browseAllShows,
    'An administrator declined this request. You can request it again.' =>
      l.browseAnAdministratorDeclinedThisRequestYouCanRequestItAgain,
    'Any' => l.browseAny,
    'Any language' => l.browseAnyLanguage,
    'Any year' => l.browseAnyYear,
    'Apply' => l.browseApply,
    'Art' => l.browseArt,
    'Artist' => l.browseArtist,
    'Artist details could not be checked. Retry' =>
      l.browseArtistDetailsCouldNotBeCheckedRetry,
    'Artists' => l.browseArtists,
    'Artists could not be searched.' => l.browseArtistsCouldNotBeSearched,
    'Ask an admin to check this author’s library record' =>
      l.browseAskAnAdminToCheckThisAuthorSLibraryRecord,
    'Ask an admin to check this book’s format' =>
      l.browseAskAnAdminToCheckThisBookSFormat,
    'Ask an admin to check this book’s library match' =>
      l.browseAskAnAdminToCheckThisBookSLibraryMatch,
    'Ask an admin to check this book’s library record' =>
      l.browseAskAnAdminToCheckThisBookSLibraryRecord,
    'Assign a library to your account in Users to show its Discover page.' =>
      l.browseAssignALibraryToYourAccountInUsersToShowItsDiscoverPage,
    'Audiobook' => l.browseAudiobook,
    'Audiobook available' => l.browseAudiobookAvailable,
    'Audiobook requested' => l.browseAudiobookRequested,
    'Audiobooks' => l.browseAudiobooks,
    'Author' => l.browseAuthor,
    'Author · in your library' => l.browseAuthorInYourLibrary,
    'Author · not in your library' => l.browseAuthorNotInYourLibrary,
    'Authors' => l.browseAuthors,
    'Authors could not be searched.' => l.browseAuthorsCouldNotBeSearched,
    'Automatic match' => l.browseAutomaticMatch,
    'Available' => l.browseAvailable,
    'Back' => l.browseBack,
    'Biography' => l.browseBiography,
    'Birthday' => l.browseBirthday,
    'Book details' => l.browseBookDetails,
    'Books' => l.browseBooks,
    'Books could not be searched. Check the connection and try again.' =>
      l.browseBooksCouldNotBeSearchedCheckTheConnectionAndTryAgain,
    'Browse Books' => l.browseBrowseBooks,
    'Browse Movies' => l.browseBrowseMovies,
    'Browse Music' => l.browseBrowseMusic,
    'Browse TV Shows' => l.browseBrowseTVShows,
    'Browse by genre' => l.browseBrowseByGenre,
    'Budget' => l.browseBudget,
    'Bundled correction' => l.browseBundledCorrection,
    'Calendar' => l.browseCalendar,
    'Camera' => l.browseCamera,
    'Cancel' => l.browseCancel,
    'Cancel request' => l.browseCancelRequest,
    'Canceled' => l.browseCanceled,
    'Cast' => l.browseCast,
    'Cast & crew' => l.browseCastCrew,
    'Check the connection and try again.' =>
      l.browseCheckTheConnectionAndTryAgain,
    'Check your connection and try again.' =>
      l.browseCheckYourConnectionAndTryAgain,
    'Checking TV match…' => l.browseCheckingTVMatch,
    'Checking library and saved requests…' =>
      l.browseCheckingLibraryAndSavedRequests,
    'Checking request progress…' => l.browseCheckingRequestProgress,
    'Checking request…' => l.browseCheckingRequest,
    'Checking…' => l.browseChecking,
    'Choose one target series, then select a Sonarr season for every source season. Saving a correction changes no library monitoring.' =>
      l.browseChooseOneTargetSeriesThenSelectASonarrSeasonForEverySourceSeasonSavingACorrectionChangesNoLibraryMon,
    'Choose the usage to clear. This reset is recorded in the administrator audit.' =>
      l.browseChooseTheUsageToClearThisResetIsRecordedInTheAdministratorAudit,
    'Classical' => l.browseClassical,
    'Clear' => l.browseClear,
    'Clear filters' => l.browseClearFilters,
    'Coming Soon' => l.browseComingSoon,
    'Connect Chaptarr to request books' =>
      l.browseConnectChaptarrToRequestBooks,
    'Connect Hardcover in Chaptarr settings' =>
      l.browseConnectHardcoverInChaptarrSettings,
    'Connect Lidarr to request music' => l.browseConnectLidarrToRequestMusic,
    'Connect Radarr to request movies' => l.browseConnectRadarrToRequestMovies,
    'Connect Sonarr to request TV shows' =>
      l.browseConnectSonarrToRequestTVShows,
    'Connect a Hardcover account to this Chaptarr instance to see ' =>
      l.browseConnectAHardcoverAccountToThisChaptarrInstanceToSee,
    'Connect a Hardcover account to this Chaptarr instance to see what\'s trending with readers right now.' =>
      l.browseConnectAHardcoverAccountToThisChaptarrInstanceToSeeBrowseWhatSTrendingWithReadersRightNow,
    'Connect a Radarr, Sonarr, or Lidarr service to see when ' =>
      l.browseConnectARadarrSonarrOrLidarrServiceToSeeWhen,
    'Connect a Radarr, Sonarr, or Lidarr service to see when your movies, shows, and albums drop.' =>
      l.browseConnectARadarrSonarrOrLidarrServiceToSeeWhenBrowseYourMoviesShowsAndAlbumsDrop,
    'Correct TV match' => l.browseCorrectTVMatch,
    'Correct a TV match' => l.browseCorrectATVMatch,
    'Costume & Make-Up' => l.browseCostumeMakeUp,
    'Could not check request progress. Try again.' =>
      l.browseCouldNotCheckRequestProgressTryAgain,
    'Could not hide this tab. Try again.' =>
      l.browseCouldNotHideThisTabTryAgain,
    'Could not load music genres.' => l.browseCouldNotLoadMusicGenres,
    'Could not load this series. Retry, or ask an admin to check TV Matches.' =>
      l.browseCouldNotLoadThisSeriesRetryOrAskAnAdminToCheckTVMatches,
    'Could not refresh these titles. Showing the last loaded results.' =>
      l.browseCouldNotRefreshTheseTitlesShowingTheLastLoadedResults,
    'Could not save the allowance. Please retry.' =>
      l.browseCouldNotSaveTheAllowancePleaseRetry,
    'Could not update this request. Please retry.' =>
      l.browseCouldNotUpdateThisRequestPleaseRetry,
    'Could not verify this season. Retry before requesting.' =>
      l.browseCouldNotVerifyThisSeasonRetryBeforeRequesting,
    'Couldn\'t load releases' => l.browseCouldnTLoadReleases,
    'Couldn’t check' => l.browseCouldnTCheck,
    'Couldn’t check · Retry' => l.browseCouldnTCheckRetry,
    'Country' => l.browseCountry,
    'Create corrective request' => l.browseCreateCorrectiveRequest,
    'Created by' => l.browseCreatedBy,
    'Creators' => l.browseCreators,
    'Crew' => l.browseCrew,
    'Date' => l.browseDate,
    'Date added' => l.browseDateAdded,
    'Default' => l.browseDefault,
    'Defaults for User and kids accounts. Admins are exempt. Each allowance is shared across libraries.' =>
      l.browseDefaultsForUserAndKidsAccountsAdminsAreExemptEachAllowanceIsSharedAcrossLibraries,
    'Details' => l.browseDetails,
    'Died' => l.browseDied,
    'Digital' => l.browseDigital,
    'Directed by' => l.browseDirectedBy,
    'Directing' => l.browseDirecting,
    'Director' => l.browseDirector,
    'Disc' => l.browseDisc,
    'Discover settings' => l.browseDiscoverSettings,
    'Download a track' => l.browseDownloadATrack,
    'Download audiobook' => l.browseDownloadAudiobook,
    'Download eBook' => l.browseDownloadEBook,
    'Download movie' => l.browseDownloadMovie,
    'Download track' => l.browseDownloadTrack,
    'Download tracks' => l.browseDownloadTracks,
    'Downloaded' => l.browseDownloaded,
    'Downloading' => l.browseDownloading,
    'Downloading Soon' => l.browseDownloadingSoon,
    'Edit current match' => l.browseEditCurrentMatch,
    'Editing' => l.browseEditing,
    'Electronic' => l.browseElectronic,
    'Ended' => l.browseEnded,
    'Enter a valid TMDB TV ID.' => l.browseEnterAValidTMDBTVID,
    'Enter a whole number from 0 to 1000000.' =>
      l.browseEnterAWholeNumberFrom0To1000000,
    'Episode' => l.browseEpisode,
    'Executive Producer' => l.browseExecutiveProducer,
    'Filters' => l.browseFilters,
    'Find series' => l.browseFindSeries,
    'First' => l.browseFirst,
    'First aired' => l.browseFirstAired,
    'Format needs attention' => l.browseFormatNeedsAttention,
    'Formats' => l.browseFormats,
    'Fully replenished' => l.browseFullyReplenished,
    'Genres' => l.browseGenres,
    'Great news! This title is currently downloading and should be available shortly.' =>
      l.browseGreatNewsThisTitleIsCurrentlyDownloadingAndShouldBeAvailableShortly,
    'Hide this tab' => l.browseHideThisTab,
    'Hiding this tab affects everyone on this server. ' =>
      l.browseHidingThisTabAffectsEveryoneOnThisServer,
    'Hiding this tab affects everyone on this server. After setup, it appears for users assigned to its service.' =>
      l.browseHidingThisTabAffectsEveryoneOnThisServerBrowseAfterSetupItAppearsForUsersAssignedToItsService,
    'Hiding…' => l.browseHiding,
    'In Production' => l.browseInProduction,
    'In Theaters' => l.browseInTheaters,
    'In cinemas' => l.browseInCinemas,
    'Inherit User default' => l.browseInheritUserDefault,
    'Keywords' => l.browseKeywords,
    'Keywords could not be searched.' => l.browseKeywordsCouldNotBeSearched,
    'Known for' => l.browseKnownFor,
    'Language' => l.browseLanguage,
    'Latest' => l.browseLatest,
    'Library' => l.browseLibrary,
    'Library and saved requests checked.' =>
      l.browseLibraryAndSavedRequestsChecked,
    'Library artists could not be checked.' =>
      l.browseLibraryArtistsCouldNotBeChecked,
    'Library availability could not be checked.' =>
      l.browseLibraryAvailabilityCouldNotBeChecked,
    'Library availability could not be checked. Retry' =>
      l.browseLibraryAvailabilityCouldNotBeCheckedRetry,
    'Library match needs attention' => l.browseLibraryMatchNeedsAttention,
    'Library unavailable' => l.browseLibraryUnavailable,
    'Lighting' => l.browseLighting,
    'Links' => l.browseLinks,
    'List' => l.browseList,
    'Load more' => l.browseLoadMore,
    'Local correction' => l.browseLocalCorrection,
    'MOVIES' => l.browseMOVIES,
    'Manage book' => l.browseManageBook,
    'Manage users' => l.browseManageUsers,
    'Match a TMDB title to a Sonarr series and its seasons. Bundled corrections apply automatically; local corrections take precedence.' =>
      l.browseMatchATMDBTitleToASonarrSeriesAndItsSeasonsBundledCorrectionsApplyAutomaticallyLocalCorrectionsTakeP,
    'Minimum rating' => l.browseMinimumRating,
    'More albums' => l.browseMoreAlbums,
    'More artists' => l.browseMoreArtists,
    'More releases' => l.browseMoreReleases,
    'Most Anticipated' => l.browseMostAnticipated,
    'Most albums' => l.browseMostAlbums,
    'Most books' => l.browseMostBooks,
    'Most popular' => l.browseMostPopular,
    'Most releases' => l.browseMostReleases,
    'Movie' => l.browseMovie,
    'Movie spotlight' => l.browseMovieSpotlight,
    'Movies' => l.browseMovies,
    'Music' => l.browseMusic,
    'Music is not available for this account.' =>
      l.browseMusicIsNotAvailableForThisAccount,
    'Name' => l.browseName,
    'Needs attention' => l.browseNeedsAttention,
    'Network' => l.browseNetwork,
    'New Releases' => l.browseNewReleases,
    'Newest' => l.browseNewest,
    'Newest first' => l.browseNewestFirst,
    'Next month' => l.browseNextMonth,
    'No Chaptarr instance is available.' =>
      l.browseNoChaptarrInstanceIsAvailable,
    'No Discover tabs are available for your account.' =>
      l.browseNoDiscoverTabsAreAvailableForYourAccount,
    'No Discover tabs to show' => l.browseNoDiscoverTabsToShow,
    'No affected accepted requests were found in the latest 200 requests for this title.' =>
      l.browseNoAffectedAcceptedRequestsWereFoundInTheLatest200RequestsForThisTitle,
    'No albums, EPs, or singles matched this catalog page.' =>
      l.browseNoAlbumsEPsOrSinglesMatchedThisCatalogPage,
    'No albums, EPs, or singles on this catalog page.' =>
      l.browseNoAlbumsEPsOrSinglesOnThisCatalogPage,
    'No artists matched this catalog page.' =>
      l.browseNoArtistsMatchedThisCatalogPage,
    'No biography available.' => l.browseNoBiographyAvailable,
    'No books found, and authors could not be searched. ' =>
      l.browseNoBooksFoundAndAuthorsCouldNotBeSearched,
    'No books found, and authors could not be searched. Try a different search.' =>
      l.browseNoBooksFoundAndAuthorsCouldNotBeSearchedBrowseTryADifferentSearch,
    'No books or authors found. Try a different search.' =>
      l.browseNoBooksOrAuthorsFoundTryADifferentSearch,
    'No overview available.' => l.browseNoOverviewAvailable,
    'No request library is assigned. An administrator can update assignments in Settings > Users.' =>
      l.browseNoRequestLibraryIsAssignedAnAdministratorCanUpdateAssignmentsInSettingsUsers,
    'No seasons can currently be requested.' =>
      l.browseNoSeasonsCanCurrentlyBeRequested,
    'No titles came back for this browse.' =>
      l.browseNoTitlesCameBackForThisBrowse,
    'No upcoming releases' => l.browseNoUpcomingReleases,
    'Not Available' => l.browseNotAvailable,
    'Not added' => l.browseNotAdded,
    'Not requested' => l.browseNotRequested,
    'Nothing matched' => l.browseNothingMatched,
    'Nothing to schedule yet' => l.browseNothingToScheduleYet,
    'Oldest' => l.browseOldest,
    'Oldest first' => l.browseOldestFirst,
    'Open Library book discovery has retired. Search your Chaptarr library and select the book to request.' =>
      l.browseOpenLibraryBookDiscoveryHasRetiredSearchYourChaptarrLibraryAndSelectTheBookToRequest,
    'Open in …' => l.browseOpenIn,
    'Original Music Composer' => l.browseOriginalMusicComposer,
    'Original language' => l.browseOriginalLanguage,
    'Overview' => l.browseOverview,
    'Partial' => l.browsePartial,
    'Past 30 days · ListenBrainz' => l.browsePast30DaysListenBrainz,
    'Pause matching' => l.browsePauseMatching,
    'Pending' => l.browsePending,
    'Pending Approval' => l.browsePendingApproval,
    'Person' => l.browsePerson,
    'Physical' => l.browsePhysical,
    'Pick another day to see what drops.' =>
      l.browsePickAnotherDayToSeeWhatDrops,
    'Pilot' => l.browsePilot,
    'Place of birth' => l.browsePlaceOfBirth,
    'Planned' => l.browsePlanned,
    'Popular' => l.browsePopular,
    'Popular Albums' => l.browsePopularAlbums,
    'Popular Movies' => l.browsePopularMovies,
    'Popular TV Shows' => l.browsePopularTVShows,
    'Popularity' => l.browsePopularity,
    'Post Production' => l.browsePostProduction,
    'Premiere' => l.browsePremiere,
    'Previous month' => l.browsePreviousMonth,
    'Problem reporting is disabled. You can still correct this TV match.' =>
      l.browseProblemReportingIsDisabledYouCanStillCorrectThisTVMatch,
    'Producer' => l.browseProducer,
    'Production' => l.browseProduction,
    'Quality' => l.browseQuality,
    'Rating' => l.browseRating,
    'Recently Added' => l.browseRecentlyAdded,
    'Recently Downloaded' => l.browseRecentlyDownloaded,
    'Recently added' => l.browseRecentlyAddedAlternate,
    'Recommended' => l.browseRecommended,
    'Refresh and retry' => l.browseRefreshAndRetry,
    'Refresh failed. Your previous genres are still shown.' =>
      l.browseRefreshFailedYourPreviousGenresAreStillShown,
    'Release date' => l.browseReleaseDate,
    'Release dates' => l.browseReleaseDates,
    'Released' => l.browseReleased,
    'Releases' => l.browseReleases,
    'Repair TV request?' => l.browseRepairTVRequest,
    'Report a problem' => l.browseReportAProblem,
    'Request' => l.browseRequest,
    'Request Denied' => l.browseRequestDenied,
    'Request More' => l.browseRequestMoreAlternate,
    'Request again' => l.browseRequestAgain,
    'Request album' => l.browseRequestAlbum,
    'Request allowance' => l.browseRequestAllowance,
    'Request allowances' => l.browseRequestAllowances,
    'Request allowances could not be loaded.' =>
      l.browseRequestAllowancesCouldNotBeLoaded,
    'Request audiobook' => l.browseRequestAudiobook,
    'Request both' => l.browseRequestBoth,
    'Request cancelled.' => l.browseRequestCancelled,
    'Request eBook' => l.browseRequestEBook,
    'Request more' => l.browseRequestMore,
    'Request options' => l.browseRequestOptions,
    'Request saved' => l.browseRequestSaved,
    'Requested' => l.browseRequested,
    'Requesting...' => l.browseRequestingAlternate,
    'Requesting…' => l.browseRequesting,
    'Requests' => l.browseRequests,
    'Reset selected' => l.browseResetSelected,
    'Reset selected allowances' => l.browseResetSelectedAllowances,
    'Restore bundled / default' => l.browseRestoreBundledDefault,
    'Retry' => l.browseRetry,
    'Retry TV match' => l.browseRetryTVMatch,
    'Retry album details' => l.browseRetryAlbumDetails,
    'Returning Series' => l.browseReturningSeries,
    'Revenue' => l.browseRevenue,
    'Review repair' => l.browseReviewRepair,
    'Rolling window' => l.browseRollingWindow,
    'Rolling windows count accepted requests, including requests awaiting approval. TV counts seasons; books count each format separately.' =>
      l.browseRollingWindowsCountAcceptedRequestsIncludingRequestsAwaitingApprovalTVCountsSeasonsBooksCountEachFor,
    'Rule' => l.browseRule,
    'Runtime' => l.browseRuntime,
    'Save allowance' => l.browseSaveAllowance,
    'Save local correction' => l.browseSaveLocalCorrection,
    'Saved request updates could not be checked.' =>
      l.browseSavedRequestUpdatesCouldNotBeChecked,
    'Saved requests could not be checked.' =>
      l.browseSavedRequestsCouldNotBeChecked,
    'Saved, but could not refresh tabs. Try again.' =>
      l.browseSavedButCouldNotRefreshTabsTryAgain,
    'Saving…' => l.browseSaving,
    'Screenplay' => l.browseScreenplay,
    'Search Sonarr or enter a TVDB ID' => l.browseSearchSonarrOrEnterATVDBID,
    'Search books' => l.browseSearchBooks,
    'Search for one of its books to add it.' =>
      l.browseSearchForOneOfItsBooksToAddIt,
    'Search for one of their books to add them.' =>
      l.browseSearchForOneOfTheirBooksToAddThem,
    'Searching authors…' => l.browseSearchingAuthors,
    'Season' => l.browseSeason,
    'Season requests are unavailable. See the notes beside each season.' =>
      l.browseSeasonRequestsAreUnavailableSeeTheNotesBesideEachSeason,
    'Season selection is unavailable for this library. Use the main Request button.' =>
      l.browseSeasonSelectionIsUnavailableForThisLibraryUseTheMainRequestButton,
    'Seasons' => l.browseSeasons,
    'See all' => l.browseSeeAll,
    'Select seasons to request' => l.browseSelectSeasonsToRequest,
    'Selected allowances reset' => l.browseSelectedAllowancesReset,
    'Selected library' => l.browseSelectedLibrary,
    'Series' => l.browseSeries,
    'Series spotlight' => l.browseSeriesSpotlight,
    'Set allowance' => l.browseSetAllowance,
    'Show a tab in Discover settings or connect a service to start browsing.' =>
      l.browseShowATabInDiscoverSettingsOrConnectAServiceToStartBrowsing,
    'Show less' => l.browseShowLess,
    'Show more' => l.browseShowMore,
    'Similar' => l.browseSimilar,
    'Some episodes are available. You can request the remaining ones.' =>
      l.browseSomeEpisodesAreAvailableYouCanRequestTheRemainingOnes,
    'Some seasons need attention. You can request the other seasons below.' =>
      l.browseSomeSeasonsNeedAttentionYouCanRequestTheOtherSeasonsBelow,
    'Some titles could not be loaded. Retry to check for updates.' =>
      l.browseSomeTitlesCouldNotBeLoadedRetryToCheckForUpdates,
    'Sonarr library for validation' => l.browseSonarrLibraryForValidation,
    'Sonarr returned no candidates for this search.' =>
      l.browseSonarrReturnedNoCandidatesForThisSearch,
    'Sort artists' => l.browseSortArtists,
    'Sort authors' => l.browseSortAuthors,
    'Sort series' => l.browseSortSeries,
    'Sort titles' => l.browseSortTitles,
    'Sound' => l.browseSound,
    'Status' => l.browseStatus,
    'Status unknown' => l.browseStatusUnknown,
    'Story' => l.browseStory,
    'Streaming on' => l.browseStreamingOn,
    'Streaming services could not be loaded.' =>
      l.browseStreamingServicesCouldNotBeLoaded,
    'Studio' => l.browseStudio,
    'Studios' => l.browseStudios,
    'Studios could not be searched.' => l.browseStudiosCouldNotBeSearched,
    'TMDB TV ID' => l.browseTMDBTVID,
    'TV' => l.browseTV,
    'TV SHOWS' => l.browseTVSHOWS,
    'TV Show' => l.browseTVShowOther,
    'TV Shows' => l.browseTVShows,
    'TV corrections are not available for this account and server.' =>
      l.browseTVCorrectionsAreNotAvailableForThisAccountAndServer,
    'TV match saved. Existing requests can be reviewed below.' =>
      l.browseTVMatchSavedExistingRequestsCanBeReviewedBelow,
    'TV matches' => l.browseTVMatches,
    'TV seasons' => l.browseTVSeasons,
    'TV show' => l.browseTVShow,
    'Tap to see their books' => l.browseTapToSeeTheirBooks,
    'The album catalog could not be searched. Library matches remain usable.' =>
      l.browseTheAlbumCatalogCouldNotBeSearchedLibraryMatchesRemainUsable,
    'The corrective request will wait for approval.' =>
      l.browseTheCorrectiveRequestWillWaitForApproval,
    'The request outcome could not be confirmed. Try refreshing its saved status.' =>
      l.browseTheRequestOutcomeCouldNotBeConfirmedTryRefreshingItsSavedStatus,
    'The whole series' => l.browseTheWholeSeries,
    'These seasons belong to this library. Open the show in Discover to request another library.' =>
      l.browseTheseSeasonsBelongToThisLibraryOpenTheShowInDiscoverToRequestAnotherLibrary,
    'These titles could not be loaded.' => l.browseTheseTitlesCouldNotBeLoaded,
    'This artist’s catalog releases could not be loaded.' =>
      l.browseThisArtistSCatalogReleasesCouldNotBeLoaded,
    'This author could not be loaded. ' => l.browseThisAuthorCouldNotBeLoaded,
    'This author could not be loaded. Check the connection and try again.' =>
      l.browseThisAuthorCouldNotBeLoadedBrowseCheckTheConnectionAndTryAgain,
    'This book could not be found. It may have been removed from ' =>
      l.browseThisBookCouldNotBeFoundItMayHaveBeenRemovedFrom,
    'This book could not be found. It may have been removed from the library.' =>
      l.browseThisBookCouldNotBeFoundItMayHaveBeenRemovedFromBrowseTheLibrary,
    'This feed has no titles right now.' => l.browseThisFeedHasNoTitlesRightNow,
    'This library was removed or you no longer have access to it.' =>
      l.browseThisLibraryWasRemovedOrYouNoLongerHaveAccessToIt,
    'This month' => l.browseThisMonth,
    'This month · ListenBrainz' => l.browseThisMonthListenBrainz,
    'This music library is not available to you.' =>
      l.browseThisMusicLibraryIsNotAvailableToYou,
    'This saved request needs attention. Search your Chaptarr library for the book.' =>
      l.browseThisSavedRequestNeedsAttentionSearchYourChaptarrLibraryForTheBook,
    'This series could not be loaded. ' => l.browseThisSeriesCouldNotBeLoaded,
    'This series could not be loaded. Check the connection and try again.' =>
      l.browseThisSeriesCouldNotBeLoadedBrowseCheckTheConnectionAndTryAgain,
    'This title is already covered by your library.' =>
      l.browseThisTitleIsAlreadyCoveredByYourLibrary,
    'This title is not available.' => l.browseThisTitleIsNotAvailable,
    'This title is not yet on the server. Tap Request to add it!' =>
      l.browseThisTitleIsNotYetOnTheServerTapRequestToAddIt,
    'This title is ready to watch! Open Plex or Infuse to start streaming.' =>
      l.browseThisTitleIsReadyToWatchOpenPlexOrInfuseToStartStreaming,
    'This title isn\'t available on this account.' =>
      l.browseThisTitleIsnTAvailableOnThisAccount,
    'This week' => l.browseThisWeek,
    'This week · ListenBrainz' => l.browseThisWeekListenBrainz,
    'This year' => l.browseThisYear,
    'This year · ListenBrainz' => l.browseThisYearListenBrainz,
    'Title' => l.browseTitle,
    'Title A to Z' => l.browseTitleAToZ,
    'Top Rated' => l.browseTopRated,
    'Top rated' => l.browseTopRatedOther,
    'Trailer' => l.browseTrailer,
    'Trending Books' => l.browseTrendingBooks,
    'Trending Movies' => l.browseTrendingMovies,
    'Trending Now' => l.browseTrendingNow,
    'Trending TV Shows' => l.browseTrendingTVShows,
    'Try a different search term' => l.browseTryADifferentSearchTerm,
    'Try a different search.' => l.browseTryADifferentSearch,
    'Try again' => l.browseTryAgain,
    'Unconfirmed request' => l.browseUnconfirmedRequest,
    'Units' => l.browseUnits,
    'Unknown' => l.browseUnknown,
    'Unlimited' => l.browseUnlimited,
    'Upcoming movies and episodes in your library will appear here.' =>
      l.browseUpcomingMoviesAndEpisodesInYourLibraryWillAppearHere,
    'Upgrading or saving a match does not re-request existing content. Review each proposed correction before creating it.' =>
      l.browseUpgradingOrSavingAMatchDoesNotReRequestExistingContentReviewEachProposedCorrectionBeforeCreatingIt,
    'View your report' => l.browseViewYourReport,
    'Visual Effects' => l.browseVisualEffects,
    'Waiting for approval' => l.browseWaitingForApproval,
    'Waiting for library' => l.browseWaitingForLibrary,
    'Watch Trailer' => l.browseWatchTrailer,
    'Watch on' => l.browseWatchOn,
    'What\'s the problem with?' => l.browseWhatSTheProblemWith,
    'Which format is the problem with?' => l.browseWhichFormatIsTheProblemWith,
    'Writer' => l.browseWriter,
    'Writing' => l.browseWriting,
    'Written by' => l.browseWrittenBy,
    'You can request the other seasons below.' =>
      l.browseYouCanRequestTheOtherSeasonsBelow,
    'You do not have access to search this book library.' =>
      l.browseYouDoNotHaveAccessToSearchThisBookLibrary,
    'You do not have access to this book library.' =>
      l.browseYouDoNotHaveAccessToThisBookLibrary,
    'Your allowance' => l.browseYourAllowance,
    'Your request has been received! The server is searching for this title and will start downloading it soon.' =>
      l.browseYourRequestHasBeenReceivedTheServerIsSearchingForThisTitleAndWillStartDownloadingItSoon,
    'Your request is awaiting approval from an administrator. You\'ll be notified once it\'s reviewed.' =>
      l.browseYourRequestIsAwaitingApprovalFromAnAdministratorYouLlBeNotifiedOnceItSReviewed,
    'Your request is saved and needs attention before delivery can continue.' =>
      l.browseYourRequestIsSavedAndNeedsAttentionBeforeDeliveryCanContinue,
    'Your request is saved for another delivery attempt.' =>
      l.browseYourRequestIsSavedForAnotherDeliveryAttempt,
    'Your request is saved.' => l.browseYourRequestIsSaved,
    'Your request is saved. Delivery to the library continues in the background.' =>
      l.browseYourRequestIsSavedDeliveryToTheLibraryContinuesInTheBackground,
    'Your request is saved. The library is still adding this author. Cantinarr keeps retrying automatically — no action is needed.' =>
      l.browseYourRequestIsSavedTheLibraryIsStillAddingThisAuthorCantinarrKeepsRetryingAutomaticallyNoActionIsNeed,
    'Your request is saved. The library isn’t ready for this book yet. Cantinarr keeps retrying automatically — no action is needed.' =>
      l.browseYourRequestIsSavedTheLibraryIsnTReadyForThisBookYetCantinarrKeepsRetryingAutomaticallyNoActionIsNeed,
    'Your session changed. Reopen this title.' =>
      l.browseYourSessionChangedReopenThisTitle,
    'Zero allows no new units.' => l.browseZeroAllowsNoNewUnits,
    'conflict' => l.browseConflict,
    'eBook' => l.browseEBook,
    'eBook + Audiobook' => l.browseEBookAudiobook,
    'eBook available' => l.browseEBookAvailable,
    'eBook requested' => l.browseEBookRequested,
    'eBooks' => l.browseEBooks,
    'paused' => l.browsePaused,
    'resolved' => l.browseResolved,
    'the library.' => l.browseTheLibrary,
    'unresolved' => l.browseUnresolved,
    'what\'s trending with readers right now.' =>
      l.browseWhatSTrendingWithReadersRightNow,
    'your movies, shows, and albums drop.' =>
      l.browseYourMoviesShowsAndAlbumsDrop,
    'Couldn’t load TV matching. Check the connection and retry.' =>
      l.browseTVMatchingFailed,
    'Pilot only' => l.browsePilotOnly,
    'First season' => l.browseFirstSeason,
    'Most recent season' => l.browseMostRecentSeason,
    'Entire series' => l.browseEntireSeries,
    'TV season' => l.browseTVSeason,
    'No library folder is available for music. Ask an admin to check the Lidarr settings.' =>
      l.browseNoLibraryFolderIsAvailableForMusicAskAnAdminToCheckTheLidarrSettings,
    'Ask an admin to check the Lidarr settings.' =>
      l.browseAskAnAdminToCheckTheLidarrSettings,
    'This album could not be matched in the library. Search for it again and retry.' =>
      l.browseThisAlbumCouldNotBeMatchedInTheLibrarySearchForItAgainAndRetry,
    'The library could not complete this request. Try again later.' =>
      l.browseTheLibraryCouldNotCompleteThisRequestTryAgainLater,
    'This album could not be requested. Check the connection and try again.' =>
      l.browseThisAlbumCouldNotBeRequestedCheckTheConnectionAndTryAgain,
    'No audiobook edition is available for this book.' =>
      l.browseNoAudiobookEditionIsAvailableForThisBook,
    'No eBook edition is available for this book.' =>
      l.browseNoEBookEditionIsAvailableForThisBook,
    'No library folder is available for this book format. Ask an admin to check the book settings.' =>
      l.browseNoLibraryFolderIsAvailableForThisBookFormatAskAnAdminToCheckTheBookSettings,
    'Ask an admin to check the book settings.' =>
      l.browseAskAnAdminToCheckTheBookSettings,
    'This book could not be matched in the library. Search for it again and retry.' =>
      l.browseThisBookCouldNotBeMatchedInTheLibrarySearchForItAgainAndRetry,
    'This book could not be requested. Check the connection and try again.' =>
      l.browseThisBookCouldNotBeRequestedCheckTheConnectionAndTryAgain,
    'Music is no longer available for this account or library.' =>
      l.browseMusicIsNoLongerAvailableForThisAccountOrLibrary,
    'Refresh failed. Your previous results are still shown.' =>
      l.browseRefreshFailedYourPreviousResultsAreStillShown,
    'Could not load music. Please retry.' =>
      l.browseCouldNotLoadMusicPleaseRetry,
    'Music discovery requires a server update. You can still browse your library and search.' =>
      l.browseMusicDiscoveryRequiresAServerUpdateYouCanStillBrowseYourLibraryAndSearch,
    'Could not check status' => l.browseCouldNotCheckStatus,
    'Request failed. Please try again.' => l.browseRequestFailedPleaseTryAgain,
    'Request limit reached.' => l.browseRequestLimitReached,
    'Update your Cantinarr server to hide Discover tabs.' =>
      l.browseUpdateYourCantinarrServerToHideDiscoverTabs,
    'Update your Cantinarr server to browse this catalog before connecting a service.' =>
      l.browseUpdateYourCantinarrServerToBrowseThisCatalogBeforeConnectingAService,
    _ => null,
  };
  if (translated != null) return translated;
  final partial =
      RegExp(r'^(\d+) of (\d+) (books|albums) available$').firstMatch(label);
  if (partial != null) {
    final available = int.parse(partial[1]!);
    final count = int.parse(partial[2]!);
    return partial[3] == 'books'
        ? l.browseBooksSomeAvailable(available, count)
        : l.browseAlbumsSomeAvailable(available, count);
  }
  final count =
      RegExp(r'^(\d+) (books?|albums?)( · all available)?$').firstMatch(label);
  if (count != null) {
    final total = int.parse(count[1]!);
    final books = count[2]!.startsWith('book');
    return count[3] == null
        ? (books ? l.browseBooksCount(total) : l.browseAlbumsCount(total))
        : (books
            ? l.browseBooksAllAvailable(total)
            : l.browseAlbumsAllAvailable(total));
  }
  final episodes = RegExp(r'^(\d+/\d+) eps$').firstMatch(label);
  if (episodes != null) return l.detailEpisodeRatio(episodes[1]!);
  if (label.contains(' + '))
    return label
        .split(' + ')
        .map((part) => browseLabel(context, part))
        .join(' + ');
  if (label.contains('\n\n'))
    return label
        .split('\n\n')
        .map((part) => browseLabel(context, part))
        .join('\n\n');
  final season = RegExp(r'^Season (\d+)$').firstMatch(label);
  if (season != null) return l.detailSeasonNumber(int.parse(season[1]!));
  final seasons = RegExp(r'^Seasons ([\d, -]+)$').firstMatch(label);
  if (seasons != null) return l.browseSeasonList(seasons[1]!);
  final quota = RegExp(r'^(.+) request limit reached\.$').firstMatch(label);
  if (quota != null)
    return l.requestCategoryLimit(browseLabel(context, quota[1]!));
  if (label.startsWith('Request limits reached: ') && label.endsWith('.')) {
    return l.requestCategoryLimits(label
        .substring(24, label.length - 1)
        .split(', ')
        .map((part) => browseLabel(context, part))
        .join(', '));
  }
  if (label.startsWith('Failed to load details: '))
    return l.detailFailedWithError(label.substring(24));
  if (label.startsWith('Failed to load person: '))
    return l.personFailedWithError(label.substring(23));
  final unconfirmedYear =
      RegExp(r'^Date unconfirmed \((\d+)\)$').firstMatch(label);
  if (unconfirmedYear != null)
    return l.browsePublicationDateUnconfirmed(int.parse(unconfirmedYear[1]!));
  return label;
}

String? browseNullableLabel(BuildContext context, String? label) =>
    label == null ? null : browseLabel(context, label);

String localizedAllowanceRule(BuildContext context, int? count, int days) =>
    count == null
        ? context.l10n.browseUnlimited
        : context.l10n.requestRuleWindow(count, days);

String localizedSeasonRowLabel(BuildContext context, Season season) {
  final raw = seasonRowLabel(season);
  final match = RegExp(r'^Season (\d+)( · \d{4})?$').firstMatch(raw);
  if (match == null) return raw;
  return '${context.l10n.detailSeasonNumber(int.parse(match[1]!))}${match[2] ?? ''}';
}

String localizedPendingReleases(
    BuildContext context, List<PendingRelease> pending,
    {required DateTime now}) {
  final today = DateTime(now.year, now.month, now.day);
  return pending.map((release) {
    final date = release.date;
    final dateLabel = DateTime(date.year, date.month, date.day) == today
        ? context.l10n.browseToday
        : date.year == now.year
            ? DateFormat.MMMd(context.l10n.dateLocale).format(date)
            : DateFormat.yMMMd(context.l10n.dateLocale).format(date);
    return '${browseLabel(context, release.label)} $dateLabel';
  }).join(' • ');
}

String localizedBrowseFilters(
    BuildContext context, BrowseFilters filters, Map<int, String> genres,
    {required Map<String, String> languages,
    required Map<int, String> providers}) {
  final l = context.l10n;
  final parts = <String>[];
  if (filters.genreIds.isNotEmpty)
    parts.add(filters.genreIds
        .map((id) => genres[id] ?? l.browseFilterGenreId(id))
        .join(', '));
  final from = filters.yearFrom;
  final to = filters.yearTo;
  if (from != null && to != null) {
    parts.add(from == to ? '$from' : l.browseYearRange(from, to));
  } else if (from != null) {
    parts.add(l.browseFilterYearOnward(from));
  } else if (to != null) {
    parts.add(l.browseFilterYearUntil(to));
  }
  if (filters.minRating != null)
    parts.add(l.browseFilterRating(filters.minRating!));
  if (filters.language != null)
    parts.add(l.browseFilterLanguage(
        languages[filters.language] ?? filters.language!));
  if (filters.providerIds.isNotEmpty)
    parts.add(l.browseFilterProvider(filters.providerIds
        .map((id) => providers[id] ?? l.browseFilterServiceId(id))
        .join(', ')));
  if (filters.keywords.isNotEmpty)
    parts.add(l.browseFilterKeyword(filters.keywords
        .map((tag) => tag.name ?? l.browseFilterKeywordId(tag.id))
        .join(', ')));
  if (filters.companies.isNotEmpty)
    parts.add(l.browseFilterStudio(filters.companies
        .map((tag) => tag.name ?? l.browseFilterStudioId(tag.id))
        .join(', ')));
  return parts.join(' · ');
}

String localizedBookPublicationSummary(
        BuildContext context, BookPublication publication) =>
    [
      if (publication.year > 0)
        browseLabel(context, bookPublicationYearLabel(publication.year)),
      if (publication.pages > 0) '${publication.pages} pages',
    ].join(' · ');
