import 'package:flutter/widgets.dart';
import 'package:cantinarr/l10n/l10n.dart';

import '../data/ai_tools_service.dart';
import '../data/discord_notifications_service.dart';
import '../data/request_history_service.dart';
import '../data/request_settings_service.dart';
import '../data/setup_status_service.dart';

/// Display-only labels from the existing model API. Protocol values and saved
/// records retain their original identity; unknown labels remain visible.
String settingsModelLabel(BuildContext context, String label) =>
    switch (label) {
      'Off' => context.l10n.settingsOff,
      'Request awaiting approval' =>
        context.l10n.settingsRequestAwaitingApproval,
      'Automatically approved request' =>
        context.l10n.settingsAutomaticallyApprovedRequest,
      'Request approved' => context.l10n.settingsRequestApproved,
      'Request denied' => context.l10n.settingsRequestDenied,
      'Requested content available' =>
        context.l10n.settingsRequestedContentAvailable,
      'Request needs attention' => context.l10n.settingsRequestNeedsAttention,
      'Problem reported' => context.l10n.settingsProblemReported,
      'Reply to a report' => context.l10n.settingsReplyToAReport,
      'Problem resolved' => context.l10n.notificationProblemResolved,
      'Problem report reopened' => context.l10n.settingsProblemReportReopened,
      'Delivery could not be confirmed.' =>
        context.l10n.settingsDeliveryCouldNotBeConfirmed,
      'Delivered' => context.l10n.settingsDelivered,
      'Waiting to send' => context.l10n.settingsWaitingToSend,
      'Sending' => context.l10n.settingsSending,
      'Failed' => context.l10n.settingsFailed,
      'Cancelled' => context.l10n.settingsCancelled,
      'Grouped deliveries' => context.l10n.settingsGroupedDeliveries,
      'Delivery unconfirmed' => context.l10n.settingsDeliveryUnconfirmed,
      'Trending this week (TMDB)' => context.l10n.settingsTrendingThisWeekTmdb,
      'What TMDB users are engaging with over the past week.' =>
        context.l10n.settingsWhatTmdbUsersAreEngagingWithOverThe,
      'Trending now (Trakt)' => context.l10n.settingsTrendingNowTrakt,
      'Ranked by how many people are watching right now.' =>
        context.l10n.settingsRankedByHowManyPeopleAreWatchingRight,
      'All-time popular (TMDB)' => context.l10n.settingsAllTimePopularTmdb,
      'TMDB\'s lifetime popularity score. Long-running catalogue shows and nightly talk shows dominate it.' =>
        context.l10n.settingsTmdbSLifetimePopularityScoreLongRunningCatalogue,
      'Tag results differ by requester or format' =>
        context.l10n.settingsTagResultsDifferByRequesterOrFormat,
      'Tag waiting for request' => context.l10n.settingsTagWaitingForRequest,
      'Tag pending' => context.l10n.settingsTagPending,
      'Tag retry scheduled' => context.l10n.settingsTagRetryScheduled,
      'Tag applied' => context.l10n.settingsTagApplied,
      'Tag failed' => context.l10n.settingsTagFailed,
      'Tag cancelled' => context.l10n.settingsTagCancelled,
      'Tag status unavailable' => context.l10n.settingsTagStatusUnavailable,
      'Unknown requester' => context.l10n.settingsUnknownRequester,
      'Unknown decision' => context.l10n.settingsUnknownDecision,
      'Media' => context.l10n.settingsMedia,
      'Library not recorded or removed' =>
        context.l10n.settingsLibraryNotRecordedOrRemoved,
      'Format not recorded' => context.l10n.settingsFormatNotRecorded,
      'music' => context.l10n.settingsMusic2,
      'Movie' => context.l10n.settingsMovie,
      'TV' => context.l10n.settingsTv,
      'Book' => context.l10n.settingsBook,
      'Album' => context.l10n.settingsAlbum,
      'Needs approval' => context.l10n.settingsNeedsApproval,
      'Approved' => context.l10n.settingsApproved,
      'Denied' => context.l10n.notificationDenied,
      'The library is still importing this author' =>
        context.l10n.settingsTheLibraryIsStillImportingThisAuthor,
      'The library is not ready for this book yet' =>
        context.l10n.settingsTheLibraryIsNotReadyForThisBook,
      'The library couldn’t match this book' =>
        context.l10n.settingsTheLibraryCouldnTMatchThisBook,
      'Approving retries the same add. Add it in the library first, then approve.' =>
        context.l10n.settingsApprovingRetriesTheSameAddAddItIn,
      'The automatic add failed while waiting for the author import' =>
        context.l10n.settingsTheAutomaticAddFailedWhileWaitingForThe,
      'Try again to resume waiting, or deny to close the request.' =>
        context.l10n.settingsTryAgainToResumeWaitingOrDenyTo,
      'The library gave up importing this author' =>
        context.l10n.settingsTheLibraryGaveUpImportingThisAuthor,
      'Its metadata service reported the import failed. Try again to reopen it and keep waiting, or deny to close the request.' =>
        context.l10n.settingsItsMetadataServiceReportedTheImportFailedTry,
      'The author import was cancelled in the library' =>
        context.l10n.settingsTheAuthorImportWasCancelledInTheLibrary,
      'Try again to queue it once more and keep waiting, or deny to close the request.' =>
        context.l10n.settingsTryAgainToQueueItOnceMoreAnd,
      'The automatic add already failed' =>
        context.l10n.settingsTheAutomaticAddAlreadyFailed,
      'Approving retries it. Check the library first.' =>
        context.l10n.settingsApprovingRetriesItCheckTheLibraryFirst,
      'a user' => context.l10n.settingsAUser,
      'Nothing left to set up' => context.l10n.settingsNothingLeftToSetUp,
      _ => label,
    };

String settingsDebugRemainingLabel(BuildContext context, AiDebugStatus status) {
  if (!status.enabled || status.remainingSeconds <= 0)
    return context.l10n.settingsOff;
  final hours = status.remainingSeconds ~/ 3600;
  final minutes = (status.remainingSeconds % 3600) ~/ 60;
  return hours > 0
      ? context.l10n.settingsMessageValue1HValue2MRemaining(
          hours.toString(), minutes.toString())
      : context.l10n
          .settingsMessageValue1MRemaining(minutes.clamp(1, 59).toString());
}

String settingsSetupSummary(BuildContext context, SetupStatus status) =>
    status.isComplete
        ? context.l10n.settingsNothingLeftToSetUp
        : context.l10n.settingsMessageValue1OfValue2FeaturesConfigured(
            status.configured.toString(), status.effectiveTotal.toString());

String settingsHistoryRequester(BuildContext context, RequestHistoryItem item) {
  if (item.requesters.isEmpty) return context.l10n.settingsUnknownRequester;
  final name = item.requesters.first.name.isEmpty
      ? context.l10n.settingsUnknownRequester
      : item.requesters.first.name;
  if (item.requesters.length == 1) return name;
  final others = item.requesters.length - 1;
  return others == 1
      ? context.l10n
          .settingsMessageValue1AndValue2Other(name, others.toString())
      : context.l10n
          .settingsMessageValue1AndValue2Others(name, others.toString());
}

String settingsPendingRequestedBy(
    BuildContext context, PendingRequestItem item) {
  final name = item.username.trim().isEmpty
      ? context.l10n.settingsAUser
      : item.username.trim();
  final others = item.requesterCount - 1;
  if (others <= 0) return context.l10n.settingsMessageRequestedByValue1(name);
  return others == 1
      ? context.l10n.settingsMessageRequestedByValue1AndValue2Other(
          name, others.toString())
      : context.l10n.settingsMessageRequestedByValue1AndValue2Others(
          name, others.toString());
}

String settingsDiscordEventLabel(BuildContext context, String event) =>
    settingsModelLabel(context, discordEventLabels[event] ?? event);

String settingsDiscordSubject(BuildContext context, DiscordDelivery delivery) {
  if (delivery.event == null)
    return context.l10n
        .settingsMessageRequestValue1(delivery.requestId.toString());
  final label = settingsDiscordEventLabel(context, delivery.event!);
  return (delivery.issueId ?? 0) > 0 ? '$label #${delivery.issueId}' : label;
}
