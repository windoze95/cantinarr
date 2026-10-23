import 'package:flutter/widgets.dart';

import 'l10n.dart';
import '../features/config_changes/data/config_change_models.dart';

/// App-authored operation statuses and labels; external descriptions stay intact.
String localizeOperationsLabel(BuildContext context, String label) {
  final season = RegExp(r'^Season (\d+)$').firstMatch(label);
  if (season != null) return context.l10n.opsSeasonNumber(season.group(1)!);
  return switch (label) {
    'Previous settings restored.' => context.l10n.opsPreviousSettingsRestored,
    'Could not confirm whether settings were restored. The current state was refreshed.' =>
      context.l10n.opsCouldNotConfirmWhetherSettingsWereRestored,
    'Could not load this change.' => context.l10n.opsCouldNotLoadThisChange,
    'Change details' => context.l10n.opsChangeDetails,
    'Change unavailable' => context.l10n.opsChangeUnavailable,
    'Couldn\'t refresh this change. Current settings may be out of date.' =>
      context.l10n.opsCouldnTRefreshThisChangeCurrentSettings,
    'Comparison' => context.l10n.opsComparison,
    'Restore previous settings' => context.l10n.opsRestorePreviousSettings,
    'Previous settings restored' =>
      context.l10n.opsPreviousSettingsRestoredMessage,
    'Why restore is unavailable' => context.l10n.opsWhyRestoreIsUnavailable,
    'Unavailable' => context.l10n.opsUnavailable,
    'Different' => context.l10n.opsDifferent,
    'Could not verify' => context.l10n.opsCouldNotVerify,
    'Not verified' => context.l10n.opsNotVerified,
    'Before' => context.l10n.opsBefore,
    'Current' => context.l10n.opsCurrent,
    'Change record' => context.l10n.opsChangeRecord,
    'Initiated by' => context.l10n.opsInitiatedBy,
    'Recorded' => context.l10n.opsRecorded,
    'Change ID' => context.l10n.opsChangeID,
    'Restores change' => context.l10n.opsRestoresChange,
    'No field comparison was recorded for this change.' =>
      context.l10n.opsNoFieldComparisonWasRecordedForThis,
    'Could not confirm whether settings were restored. Review the current state.' =>
      context.l10n.opsCouldNotConfirmWhetherSettingsWereRestoredMessage,
    'Connected-app settings' => context.l10n.opsConnectedAppSettings,
    'Review change' => context.l10n.opsReviewChange,
    'Unnamed instance' => context.l10n.opsUnnamedInstance,
    'Current settings match' => context.l10n.opsCurrentSettingsMatch,
    'Current settings changed' => context.l10n.opsCurrentSettingsChanged,
    'Current settings unavailable' =>
      context.l10n.opsCurrentSettingsUnavailable,
    'Current status unknown' => context.l10n.opsCurrentStatusUnknown,
    'Current status not checked' => context.l10n.opsCurrentStatusNotChecked,
    'Restore previous settings?' =>
      context.l10n.opsRestorePreviousSettingsMessage,
    'Restore unavailable' => context.l10n.opsRestoreUnavailable,
    'Settings to restore' => context.l10n.opsSettingsToRestore,
    'Cancel' => context.l10n.opsCancel,
    'Done' => context.l10n.opsDone,
    'This change cannot be restored safely.' =>
      context.l10n.opsThisChangeCannotBeRestoredSafely,
    'Could not load older changes.' => context.l10n.opsCouldNotLoadOlderChanges,
    'Could not load change history.' =>
      context.l10n.opsCouldNotLoadChangeHistory,
    'Configuration history' => context.l10n.opsConfigurationHistory,
    'Configuration history unavailable' =>
      context.l10n.opsConfigurationHistoryUnavailable,
    'Connected-app changes' => context.l10n.opsConnectedAppChanges,
    'AI and MCP quality-profile and custom-format writes are recorded here for review, live comparison, and troubleshooting. Applied profile updates can also be safely restored once while nothing has drifted.' =>
      context.l10n.opsAIAndMCPQualityProfileAndCustom,
    'Couldn\'t refresh change history. Showing the last update.' =>
      context.l10n.opsCouldnTRefreshChangeHistoryShowingThe,
    'Load older changes' => context.l10n.opsLoadOlderChanges,
    'TODAY' => context.l10n.opsTODAY,
    'YESTERDAY' => context.l10n.opsYESTERDAY,
    'No connected-app changes yet' => context.l10n.opsNoConnectedAppChangesYet,
    'Changes made by the AI Assistant and other Cantinarr tools will appear here.' =>
      context.l10n.opsChangesMadeByTheAIAssistantAnd,
    'Something went wrong.' => context.l10n.opsSomethingWentWrong,
    'Proposal rejected. Nothing was changed.' =>
      context.l10n.opsProposalRejectedNothingWasChanged,
    'Apply this profile change?' => context.l10n.opsApplyThisProfileChange,
    'Approve' => context.l10n.opsApprove,
    'Reject this proposal?' => context.l10n.opsRejectThisProposal,
    'Reason (optional)' => context.l10n.opsReasonOptional,
    'Reject' => context.l10n.opsReject,
    'Profile Change Approvals' => context.l10n.opsProfileChangeApprovals,
    'Approvals unavailable' => context.l10n.opsApprovalsUnavailable,
    'Could not load profile change proposals.' =>
      context.l10n.opsCouldNotLoadProfileChangeProposals,
    'Recent' => context.l10n.opsRecent,
    'Nothing awaiting approval' => context.l10n.opsNothingAwaitingApproval,
    'Quality profile' => context.l10n.opsQualityProfile,
    'Apple TVs' => context.l10n.opsAppleTVs,
    'Apple TV setup is not available for this account and server.' =>
      context.l10n.opsAppleTVSetupIsNotAvailableFor,
    'Couldn’t load Apple TVs · Retry' =>
      context.l10n.opsCouldnTLoadAppleTVsRetry,
    'Add Apple TV' => context.l10n.opsAddAppleTV,
    'No Apple TVs are paired with this server.' =>
      context.l10n.opsNoAppleTVsArePairedWithThis,
    'TV address (optional)' => context.l10n.opsTVAddressOptional,
    'Searching…' => context.l10n.opsSearching,
    'Search for TVs' => context.l10n.opsSearchForTVs,
    'Pair' => context.l10n.opsPair,
    'Enter the four-digit PIN shown on the TV.' =>
      context.l10n.opsEnterTheFourDigitPINShownOn,
    'Pairing did not finish. Start again when the TV is ready.' =>
      context.l10n.opsPairingDidNotFinishStartAgainWhen,
    'Connecting to the TV…' => context.l10n.opsConnectingToTheTV,
    'Enter the four-digit PIN shown on your TV. Pairing expires after five minutes.' =>
      context.l10n.opsEnterTheFourDigitPINShownOnMessage,
    'TV PIN' => context.l10n.opsTVPIN,
    'Start again' => context.l10n.opsStartAgain,
    'Pairing…' => context.l10n.opsPairing,
    'Pair TV' => context.l10n.opsPairTV,
    'Connected. Infuse is installed.' =>
      context.l10n.opsConnectedInfuseIsInstalled,
    'Paired again.' => context.l10n.opsPairedAgain,
    'Forget' => context.l10n.opsForget,
    'Name' => context.l10n.opsName,
    'TV address' => context.l10n.opsTVAddress,
    'Save address changes before checking the connection.' =>
      context.l10n.opsSaveAddressChangesBeforeCheckingTheConnection,
    'Who can use this TV' => context.l10n.opsWhoCanUseThisTV,
    'All admins have access. Choose other adults below.' =>
      context.l10n.opsAllAdminsHaveAccessChooseOtherAdults,
    'Retry loading people' => context.l10n.opsRetryLoadingPeople,
    'Save' => context.l10n.opsSave,
    'Check connection' => context.l10n.opsCheckConnection,
    'Pair again' => context.l10n.opsPairAgain,
    'Forget TV' => context.l10n.opsForgetTV,
    'Open on Apple TV' => context.l10n.opsOpenOnAppleTV,
    'Sending to TV…' => context.l10n.opsSendingToTV,
    'Infuse uses the libraries connected on that TV.' =>
      context.l10n.opsInfuseUsesTheLibrariesConnectedOnThat,
    'Confirming…' => context.l10n.opsConfirming,
    'Confirmation expired' => context.l10n.opsConfirmationExpired,
    'Confirm Open' => context.l10n.opsConfirmOpen,
    'Retry' => context.l10n.opsRetry,
    'Something went wrong' => context.l10n.opsSomethingWentWrongMessage,
    'Enter a custom model ID' => context.l10n.opsEnterACustomModelID,
    'Saved' => context.l10n.opsSaved,
    'AI Remediation' => context.l10n.opsAIRemediation,
    'Standing auto-approvals' => context.l10n.opsStandingAutoApprovals,
    'The fixes approved without paging you' =>
      context.l10n.opsTheFixesApprovedWithoutPagingYou,
    'Agent fixes' => context.l10n.opsAgentFixes,
    'Awaiting review, and everything already done' =>
      context.l10n.opsAwaitingReviewAndEverythingAlreadyDone,
    'General' => context.l10n.opsGeneral,
    'Enabled' => context.l10n.opsEnabled,
    'Master switch for the remediation assistant.' =>
      context.l10n.opsMasterSwitchForTheRemediationAssistant,
    'Auto-dispatch on detected problems' =>
      context.l10n.opsAutoDispatchOnDetectedProblems,
    'Allow problem reporting' => context.l10n.opsAllowProblemReporting,
    'Show a "Report a problem" button to users on media screens.' =>
      context.l10n.opsShowAReportAProblemButtonTo,
    'Mark resolved issues as read' => context.l10n.opsMarkResolvedIssuesAsRead,
    'Mode' => context.l10n.opsMode,
    'Whether the assistant may prepare a fix for an admin to review.' =>
      context.l10n.opsWhetherTheAssistantMayPrepareAFix,
    'Automatic recovery' => context.l10n.opsAutomaticRecovery,
    'Minimum watch time (minutes)' => context.l10n.opsMinimumWatchTimeMinutes,
    'Quiet time after arr activity (minutes)' =>
      context.l10n.opsQuietTimeAfterArrActivityMinutes,
    'Recovery settle time (minutes)' =>
      context.l10n.opsRecoverySettleTimeMinutes,
    'Shared AI' => context.l10n.opsSharedAI,
    'Set up the shared provider' => context.l10n.opsSetUpTheSharedProvider,
    'Admin > Providers & Credentials' =>
      context.l10n.opsAdminProvidersCredentials,
    'Remediation model' => context.l10n.opsRemediationModel,
    'Custom model ID' => context.l10n.opsCustomModelID,
    'Saving runs a small response test before activation.' =>
      context.l10n.opsSavingRunsASmallResponseTestBefore,
    'Limits' => context.l10n.opsLimits,
    'Max steps per run' => context.l10n.opsMaxStepsPerRun,
    'Max output tokens per turn' => context.l10n.opsMaxOutputTokensPerTurn,
    'Max wall-clock (seconds)' => context.l10n.opsMaxWallClockSeconds,
    'Daily run cap' => context.l10n.opsDailyRunCap,
    'Wait for a user reply (hours)' => context.l10n.opsWaitForAUserReplyHours,
    'Failed auto investigations before pausing' =>
      context.l10n.opsFailedAutoInvestigationsBeforePausing,
    'Agent activity' => context.l10n.opsAgentActivity,
    'Could not load run' => context.l10n.opsCouldNotLoadRun,
    'Couldn\'t refresh agent activity. Showing the last update.' =>
      context.l10n.opsCouldnTRefreshAgentActivityShowingThe,
    'No steps recorded.' => context.l10n.opsNoStepsRecorded,
    'Error' => context.l10n.opsError,
    'Agent' => context.l10n.opsAgent,
    'Tool call' => context.l10n.opsToolCall,
    'Result' => context.l10n.opsResult,
    'Gave up' => context.l10n.opsGaveUp,
    'System' => context.l10n.opsSystem,
    'Activity' => context.l10n.opsActivity,
    'Delete this rule?' => context.l10n.opsDeleteThisRule,
    'Delete' => context.l10n.opsDelete,
    'Agent Auto-Approvals' => context.l10n.opsAgentAutoApprovals,
    'No standing rules yet.' => context.l10n.opsNoStandingRulesYet,
    'Auto-approval rule' => context.l10n.opsAutoApprovalRule,
    'Paused' => context.l10n.opsPaused,
    'Active' => context.l10n.opsActive,
    'See the issue that paused it →' => context.l10n.opsSeeTheIssueThatPausedIt,
    'Resume' => context.l10n.opsResume,
    'Pause' => context.l10n.opsPause,
    'Ready to automate' => context.l10n.opsReadyToAutomate,
    'Arm' => context.l10n.opsArm,
    'Report a problem' => context.l10n.opsReportAProblem,
    'Wrong book/title' => context.l10n.opsWrongBookTitle,
    'Wrong album/release' => context.l10n.opsWrongAlbumRelease,
    'Please describe the problem.' => context.l10n.opsPleaseDescribeTheProblem,
    'Could not send your report.' => context.l10n.opsCouldNotSendYourReport,
    'Thanks — we\'re looking into it' =>
      context.l10n.opsThanksWeReLookingIntoIt,
    'What\'s wrong?' => context.l10n.opsWhatSWrong,
    'Details' => context.l10n.opsDetails,
    'Details (optional)' => context.l10n.opsDetailsOptional,
    'Tell us what happened' => context.l10n.opsTellUsWhatHappened,
    'Add anything that helps (optional)' =>
      context.l10n.opsAddAnythingThatHelpsOptional,
    'Submit' => context.l10n.opsSubmit,
    'on its own' => context.l10n.opsOnItsOwn,
    'all on their own' => context.l10n.opsAllOnTheirOwn,
    'Last 7 days:' => context.l10n.opsLast7Days,
    'Right now:' => context.l10n.opsRightNow,
    'Issues' => context.l10n.opsIssues,
    'My reports' => context.l10n.opsMyReports,
    'Needs attention' => context.l10n.opsNeedsAttention,
    'Tracking' => context.l10n.opsTracking,
    'Closed' => context.l10n.opsClosed,
    'Couldn\'t refresh issues. Showing the last update.' =>
      context.l10n.opsCouldnTRefreshIssuesShowingTheLast,
    'No issues need attention.' => context.l10n.opsNoIssuesNeedAttention,
    'Nothing is being tracked.' => context.l10n.opsNothingIsBeingTracked,
    'No closed issues yet.' => context.l10n.opsNoClosedIssuesYet,
    'Close this as fixed?' => context.l10n.opsCloseThisAsFixed,
    'Not yet' => context.l10n.opsNotYet,
    'Yes, it’s fixed' => context.l10n.opsYesItSFixed,
    'Thanks for checking. This is closed.' =>
      context.l10n.opsThanksForCheckingThisIsClosed,
    'Dismiss this notice?' => context.l10n.opsDismissThisNotice,
    'Dismiss this issue?' => context.l10n.opsDismissThisIssue,
    'Dismissing silences this notice for about 6 months. It only returns if the problem re-forms from newer incidents.' =>
      context.l10n.opsDismissingSilencesThisNoticeForAbout6,
    'This closes the issue without applying any pending fixes. Pending fixes will no longer be available for approval.' =>
      context.l10n.opsThisClosesTheIssueWithoutApplyingAny,
    'Dismiss issue' => context.l10n.opsDismissIssue,
    'Issue dismissed.' => context.l10n.opsIssueDismissed,
    'Issue marked resolved.' => context.l10n.opsIssueMarkedResolved,
    'Issue closed without a fix.' => context.l10n.opsIssueClosedWithoutAFix,
    'The issue changed before completion. Showing its current state.' =>
      context.l10n.opsTheIssueChangedBeforeCompletionShowingIts,
    'Issue reopened for review.' => context.l10n.opsIssueReopenedForReview,
    'Open issue' => context.l10n.opsOpenIssue,
    'Issue' => context.l10n.opsIssue,
    'Could not load issue' => context.l10n.opsCouldNotLoadIssue,
    'Couldn\'t refresh this issue. Showing the last update.' =>
      context.l10n.opsCouldnTRefreshThisIssueShowingThe,
    'Agent activity couldn\'t be refreshed. History may be incomplete.' =>
      context.l10n.opsAgentActivityCouldnTBeRefreshedHistory,
    'Agent investigations' => context.l10n.opsAgentInvestigations,
    'Reopen issue' => context.l10n.opsReopenIssue,
    'Answer the question…' => context.l10n.opsAnswerTheQuestion,
    'Add a reply…' => context.l10n.opsAddAReply,
    'A fix is ready and waiting for an administrator\'s approval. Nothing is needed from you right now.' =>
      context.l10n.opsAFixIsReadyAndWaitingFor,
    'An administrator is taking a closer look at this.' =>
      context.l10n.opsAnAdministratorIsTakingACloserLook,
    'Is it right now?' => context.l10n.opsIsItRightNow,
    'This is fixed' => context.l10n.opsThisIsFixed,
    'Complete after admin review' => context.l10n.opsCompleteAfterAdminReview,
    'Complete this issue' => context.l10n.opsCompleteThisIssue,
    'Verify the current arr state, then record the honest outcome. You can add a note about what you checked.' =>
      context.l10n.opsVerifyTheCurrentArrStateThenRecord,
    'Record a final human judgment, with an optional note for context. This is separate from dismissing the report.' =>
      context.l10n.opsRecordAFinalHumanJudgmentWithAn,
    'Mark resolved' => context.l10n.opsMarkResolved,
    'Close without fix' => context.l10n.opsCloseWithoutFix,
    'Mark this issue resolved?' => context.l10n.opsMarkThisIssueResolved,
    'Close without a fix?' => context.l10n.opsCloseWithoutAFix,
    'Confirm only after checking that the reported problem is no longer present.' =>
      context.l10n.opsConfirmOnlyAfterCheckingThatTheReported,
    'Use this when you reviewed the issue but cannot or should not apply a fix.' =>
      context.l10n.opsUseThisWhenYouReviewedTheIssue,
    'Closing a notice this way silences it for about 2 months; it returns only if the problem re-forms from newer incidents.' =>
      context.l10n.opsClosingANoticeThisWaySilencesIt,
    'Closing without a fix tells Cantinarr you are not going to change this — the notice stays silent for 12 months unless the problem re-forms.' =>
      context.l10n.opsClosingWithoutAFixTellsCantinarrYou,
    'An uncertain or partial agent outcome must be verified manually before completion.' =>
      context.l10n.opsAnUncertainOrPartialAgentOutcomeMust,
    'Completion note (optional)' => context.l10n.opsCompletionNoteOptional,
    'What did you verify, or why is no fix appropriate?' =>
      context.l10n.opsWhatDidYouVerifyOrWhyIs,
    'Review provider settings' => context.l10n.opsReviewProviderSettings,
    'Open AI Access for your own account' =>
      context.l10n.opsOpenAIAccessForYourOwnAccount,
    'Where to look' => context.l10n.opsWhereToLook,
    'What to change' => context.l10n.opsWhatToChange,
    'Needs a closer look' => context.l10n.opsNeedsACloserLook,
    'Working on it…' => context.l10n.opsWorkingOnIt,
    'This issue is closed.' => context.l10n.opsThisIssueIsClosed,
    'Approve all' => context.l10n.opsApproveAll,
    'Could not confirm the results. Refresh to see what ran.' =>
      context.l10n.opsCouldNotConfirmTheResultsRefreshTo,
    'No fixes were applied.' => context.l10n.opsNoFixesWereApplied,
    'Awaiting review' => context.l10n.opsAwaitingReview,
    'History' => context.l10n.opsHistory,
    'Couldn\'t refresh agent fixes. Showing the last update.' =>
      context.l10n.opsCouldnTRefreshAgentFixesShowingThe,
    'No fixes awaiting review.' => context.l10n.opsNoFixesAwaitingReview,
    'No agent-fix history yet.' => context.l10n.opsNoAgentFixHistoryYet,
    'Today' => context.l10n.opsToday,
    'Yesterday' => context.l10n.opsYesterday,
    'Cantinarr will restore only the settings recorded below. The restore will be added to change history.' =>
      context.l10n.opsCantinarrWillRestoreOnlyTheSettingsRecorded,
    'This record is the completed restore. It stays in change history and cannot be restored again.' =>
      context.l10n.opsThisRecordIsTheCompletedRestoreIt,
    'These settings changed after Cantinarr applied them. Review the live values before deciding what to keep.' =>
      context.l10n.opsTheseSettingsChangedAfterCantinarrAppliedThem,
    'Cantinarr could not read the connected app, so it cannot safely restore the recorded values.' =>
      context.l10n.opsCantinarrCouldNotReadTheConnectedApp,
    'When an external assistant proposes a quality-profile change, it appears here for your review before anything is written.' =>
      context.l10n.opsWhenAnExternalAssistantProposesAQuality,
    'Open movies and shows in Infuse on your TV from any Cantinarr app. The server must be able to reach the TV on your home network.' =>
      context.l10n.opsOpenMoviesAndShowsInInfuseOn,
    'Admins can use every paired TV. You can give other adults access to individual TVs. Kids accounts cannot control TVs.' =>
      context.l10n.opsAdminsCanUseEveryPairedTVYou,
    'The Apple TV helper is missing. Official Cantinarr container images include it; manual server installs need the helper installed first.' =>
      context.l10n.opsTheAppleTVHelperIsMissingOfficial,
    'Turn on the TV and have Infuse installed. Search the server’s local network, or enter the TV’s IP address or hostname to narrow the search. For separate subnets, enable mDNS forwarding between the server and TV networks.' =>
      context.l10n.opsTurnOnTheTVAndHaveInfuse,
    'No Apple TVs answered local discovery. A container or separate network can hide discovery results; this does not mean no TVs are present. Check mDNS forwarding if the server and TV are on different subnets.' =>
      context.l10n.opsNoAppleTVsAnsweredLocalDiscoveryA,
    'No Apple TV answered at that address. Check that the TV is awake and mDNS discovery can reach it. Entering an address does not bypass discovery.' =>
      context.l10n.opsNoAppleTVAnsweredAtThatAddress,
    'Removes its saved pairing and everyone’s access in Cantinarr. Infuse and its libraries stay on the TV.' =>
      context.l10n.opsRemovesItsSavedPairingAndEveryoneS,
    'Check your TV. If it asks “Open in Infuse”, use Confirm Open. If the title is already open, tap Done.' =>
      context.l10n.opsCheckYourTVIfItAsksOpen,
    'Let the assistant investigate reported problems and propose fixes for your approval. Read-only investigation never changes anything; every mutation waits for an admin. This server-owned agent always uses the admin shared provider, so runs count against its API quota or shared OpenAI OAuth usage meter. Personal providers and per-user AI access switches are never used.' =>
      context.l10n.opsLetTheAssistantInvestigateReportedProblemsAnd,
    'Track detected problems quietly while Radarr or Sonarr retries, then investigate only after the observation window.' =>
      context.l10n.opsTrackDetectedProblemsQuietlyWhileRadarrOr,
    'Clear the unread dot when an issue resolves, instead of re-flagging it for another look.' =>
      context.l10n.opsClearTheUnreadDotWhenAnIssue,
    'Detected download problems stay silent while the arr may still recover on its own. These timers decide when recovery has had enough time and the issue should become actionable.' =>
      context.l10n.opsDetectedDownloadProblemsStaySilentWhileThe,
    'Wait at least this long before any agent work, proposal, or admin alert can begin.' =>
      context.l10n.opsWaitAtLeastThisLongBeforeAny,
    'Keep waiting while Radarr or Sonarr is retrying. Escalation starts only after no new recovery activity for this long.' =>
      context.l10n.opsKeepWaitingWhileRadarrOrSonarrIs,
    'When the failed queue item clears, allow imports and library state this long to settle before deciding the outcome.' =>
      context.l10n.opsWhenTheFailedQueueItemClearsAllow,
    'No shared AI provider is set up yet. Remediation always runs on the shared provider, so detected problems will wait instead of being investigated until one is configured.' =>
      context.l10n.opsNoSharedAIProviderIsSetUp,
    'If the shared provider changes later, Cantinarr falls back to its shared model until a remediation override is tested for the new provider.' =>
      context.l10n.opsIfTheSharedProviderChangesLaterCantinarr,
    'API-key providers receive this as a request cap. For shared OpenAI OAuth, Cantinarr watches Codex usage reports and interrupts the turn when reported output reaches the limit. Reports are best-effort rather than a request-side hard cap and can arrive after the model has already exceeded the boundary.' =>
      context.l10n.opsAPIKeyProvidersReceiveThisAsA,
    'Future matching fixes will wait for manual approval. Decided fixes keep their audit history.' =>
      context.l10n.opsFutureMatchingFixesWillWaitForManual,
    'Approve an agent fix and check "Always approve this fix for this problem" to create one. Rules approve matching fixes without paging you and pause themselves if a fix fails.' =>
      context.l10n.opsApproveAnAgentFixAndCheckAlways,
    'Fixes you\'ve already approved by hand. Arming one approves future matches automatically; it pauses itself if a fix fails.' =>
      context.l10n.opsFixesYouVeAlreadyApprovedByHand,
    'Thanks — we’re tracking this quietly and will step in if it still needs help.' =>
      context.l10n.opsThanksWeReTrackingThisQuietlyAnd,
    'This closes the conversation. You won’t be able to reply while it is closed. If the problem continues, ask an administrator to take another look.' =>
      context.l10n.opsThisClosesTheConversationYouWonT,
    'We’re tracking this quietly and will step in if it still needs help.' =>
      context.l10n.opsWeReTrackingThisQuietlyAndWill,
    'You’re the one who can tell — have a look, and if the content is what you expected, close this out. Still not right? Reply below instead and we’ll keep looking.' =>
      context.l10n.opsYouReTheOneWhoCanTell,
    'This fix changed while you were reviewing it. Refresh first.' =>
      context.l10n.opsThisFixChangedWhileYouWereReviewing,
    'Could not confirm whether the fix ran. Refresh before deciding again.' =>
      context.l10n.opsCouldNotConfirmWhetherTheFixRan,
    'Approve this change?' => context.l10n.opsApproveThisChange,
    'Always approve this fix for this problem' =>
      context.l10n.opsAlwaysApproveThisFixForThisProblem,
    'Approve and apply' => context.l10n.opsApproveAndApply,
    'Fix applied.' => context.l10n.opsFixApplied,
    'The fix was approved, but it failed.' =>
      context.l10n.opsTheFixWasApprovedButItFailed,
    'The fix did not run because the issue or proposal changed.' =>
      context.l10n.opsTheFixDidNotRunBecauseThe,
    'Approval was recorded, but the result could not be confirmed.' =>
      context.l10n.opsApprovalWasRecordedButTheResultCould,
    'Fix approved and applying.' => context.l10n.opsFixApprovedAndApplying,
    'This fix was already denied.' => context.l10n.opsThisFixWasAlreadyDenied,
    'The fix was not approved.' => context.l10n.opsTheFixWasNotApproved,
    'Deny this fix' => context.l10n.opsDenyThisFix,
    'Deny' => context.l10n.opsDeny,
    'Fix denied. The agent can investigate another option.' =>
      context.l10n.opsFixDeniedTheAgentCanInvestigateAnother,
    'The agent proposed a fix' => context.l10n.opsTheAgentProposedAFix,
    'Episodes to delete (uncheck to spare)' =>
      context.l10n.opsEpisodesToDeleteUncheckToSpare,
    'Values used after approval' => context.l10n.opsValuesUsedAfterApproval,
    'Agent\'s reasoning' => context.l10n.opsAgentSReasoning,
    'Deny note' => context.l10n.opsDenyNote,
    'View agent activity' => context.l10n.opsViewAgentActivity,
    'This fix could not be refreshed. Retry before deciding.' =>
      context.l10n.opsThisFixCouldNotBeRefreshedRetry,
    'Waiting on an admin to approve a fix.' =>
      context.l10n.opsWaitingOnAnAdminToApproveA,
    'Already tried — and it came back' =>
      context.l10n.opsAlreadyTriedAndItCameBack,
    'Already tried on this problem' =>
      context.l10n.opsAlreadyTriedOnThisProblem,
    'Target instance' => context.l10n.opsTargetInstance,
    'Instance ID unavailable' => context.l10n.opsInstanceIDUnavailable,
    'Grab a different release and remove the current one' =>
      context.l10n.opsGrabADifferentReleaseAndRemoveThe,
    'Grab a specific release' => context.l10n.opsGrabASpecificRelease,
    'Remove the stuck item from the download queue' =>
      context.l10n.opsRemoveTheStuckItemFromTheDownload,
    'Blocklist the current release and let the service replace it' =>
      context.l10n.opsBlocklistTheCurrentReleaseAndLetThe,
    'Remove and blocklist this release without searching for a replacement' =>
      context.l10n.opsRemoveAndBlocklistThisReleaseWithoutSearching,
    'Change the download category to unblock the import' =>
      context.l10n.opsChangeTheDownloadCategoryToUnblockThe,
    'Fix the stuck download-queue item' =>
      context.l10n.opsFixTheStuckDownloadQueueItem,
    'Force-import the downloaded files (overrides safety checks)' =>
      context.l10n.opsForceImportTheDownloadedFilesOverridesSafety,
    'Manually import the downloaded files' =>
      context.l10n.opsManuallyImportTheDownloadedFiles,
    'Start an automatic search for this title' =>
      context.l10n.opsStartAnAutomaticSearchForThisTitle,
    'Rescan the files on disk and re-run the import' =>
      context.l10n.opsRescanTheFilesOnDiskAndRe,
    'Apply a fix' => context.l10n.opsApplyAFix,
    'Cantinarr will download a different release and remove the current queue item. This changes your connected media service immediately.' =>
      context.l10n.opsCantinarrWillDownloadADifferentReleaseAnd,
    'Cantinarr will download a release in your connected media service immediately.' =>
      context.l10n.opsCantinarrWillDownloadAReleaseInYour,
    'Cantinarr will remove an item from the download queue. This cannot be undone from Cantinarr.' =>
      context.l10n.opsCantinarrWillRemoveAnItemFromThe,
    'Cantinarr will blocklist the current release and remove it from the queue. Replacement searches follow your service\'s failed-download settings, but are suppressed for unaired episodes.' =>
      context.l10n.opsCantinarrWillBlocklistTheCurrentReleaseAnd,
    'Cantinarr will blocklist the stuck release and remove it from the queue, without searching for a replacement. Episode monitoring and existing library files are unchanged.' =>
      context.l10n.opsCantinarrWillBlocklistTheStuckReleaseAnd,
    'Cantinarr will change a download category in your connected services.' =>
      context.l10n.opsCantinarrWillChangeADownloadCategoryIn,
    'Cantinarr will change the download queue in your connected media service.' =>
      context.l10n.opsCantinarrWillChangeTheDownloadQueueIn,
    'Cantinarr will force-import downloaded files and override normal import safety checks.' =>
      context.l10n.opsCantinarrWillForceImportDownloadedFilesAnd,
    'Cantinarr will import downloaded files into your media library.' =>
      context.l10n.opsCantinarrWillImportDownloadedFilesIntoYour,
    'Cantinarr will start a search and may add a download to the queue.' =>
      context.l10n.opsCantinarrWillStartASearchAndMay,
    'Cantinarr will rescan files and run the import process in your connected media service.' =>
      context.l10n.opsCantinarrWillRescanFilesAndRunThe,
    'Cantinarr will make a change in your connected media service.' =>
      context.l10n.opsCantinarrWillMakeAChangeInYour,
    'every file this book holds in your library' =>
      context.l10n.opsEveryFileThisBookHoldsInYour,
    'every track file this album holds in your library' =>
      context.l10n.opsEveryTrackFileThisAlbumHoldsIn,
    'the episode files listed on this fix' =>
      context.l10n.opsTheEpisodeFilesListedOnThisFix,
    'the movie file in your library' =>
      context.l10n.opsTheMovieFileInYourLibrary,
    'TV' => context.l10n.opsTV,
    'Book' => context.l10n.opsBook,
    'Music' => context.l10n.opsMusic,
    'Movie' => context.l10n.opsMovie,
    'Type' => context.l10n.opsType,
    'Release' => context.l10n.opsRelease,
    'Quality' => context.l10n.opsQuality,
    'Size' => context.l10n.opsSize,
    'Protocol' => context.l10n.opsProtocol,
    'Indexer' => context.l10n.opsIndexer,
    'Rejected by the connected media service' =>
      context.l10n.opsRejectedByTheConnectedMediaService,
    'Warning' => context.l10n.opsWarning,
    'Safe reference' => context.l10n.opsSafeReference,
    'Replaces queue item' => context.l10n.opsReplacesQueueItem,
    'Queue item' => context.l10n.opsQueueItem,
    'Action' => context.l10n.opsAction,
    'Force' => context.l10n.opsForce,
    'TMDB id' => context.l10n.opsTMDBId,
    'Season' => context.l10n.opsSeason,
    'Episode' => context.l10n.opsEpisode,
    'Author id' => context.l10n.opsAuthorId,
    'Book id' => context.l10n.opsBookId,
    'Artist id' => context.l10n.opsArtistId,
    'Album id' => context.l10n.opsAlbumId,
    'Episodes' => context.l10n.opsEpisodes,
    'Files to delete' => context.l10n.opsFilesToDelete,
    'Block the release' => context.l10n.opsBlockTheRelease,
    'Approved' => context.l10n.opsApproved,
    'Approved automatically, but the fix failed' =>
      context.l10n.opsApprovedAutomaticallyButTheFixFailed,
    'Approved, but the fix failed' => context.l10n.opsApprovedButTheFixFailed,
    'Not run · the issue or proposal changed' =>
      context.l10n.opsNotRunTheIssueOrProposalChanged,
    'Approved automatically, but the outcome could not be confirmed' =>
      context.l10n.opsApprovedAutomaticallyButTheOutcomeCouldNot,
    'Approved, but the outcome could not be confirmed' =>
      context.l10n.opsApprovedButTheOutcomeCouldNotBe,
    'just now' => context.l10n.opsJustNow,
    'Delete the wrong file already in your library and look for a replacement' =>
      context.l10n.opsDeleteWrongFileSearch,
    'Delete the wrong files already in your library and look for replacements' =>
      context.l10n.opsDeleteWrongFilesSearch,
    'Delete the wrong file, block that release from coming back, and look for a replacement' =>
      context.l10n.opsDeleteWrongFileBlockSearch,
    'Delete the wrong files, block those releases from coming back, and look for replacements' =>
      context.l10n.opsDeleteWrongFilesBlockSearch,
    ' It also blocks the release that file came from, so your media service will not download that same release again.' =>
      context.l10n.opsItAlsoBlocksTheReleaseThatFile,
    ' It also blocks the releases those files came from, so your media service will not download those same releases again.' =>
      context.l10n.opsItAlsoBlocksTheReleasesThoseFiles,
    ' The release that file came from is not blocked, so your media service could download the same one again.' =>
      context.l10n.opsTheReleaseThatFileCameFromIs,
    ' The releases those files came from are not blocked, so your media service could download the same ones again.' =>
      context.l10n.opsTheReleasesThoseFilesCameFromAre,
    ' Cantinarr then looks for replacements, but only for the episodes of this season that have already aired. The rest of the season is left alone — your media service will grab each episode as it comes out.' =>
      context.l10n.opsCantinarrThenLooksForReplacementsButOnly,
    ' Cantinarr then looks for a replacement.' =>
      context.l10n.opsCantinarrThenLooksForAReplacement,
    ' If blocking that release already sent your media service looking for a replacement itself, Cantinarr leaves that to it.' =>
      context.l10n.opsIfBlockingThatReleaseAlreadySentYour,
    ' If blocking those releases already sent your media service looking for replacements itself, Cantinarr leaves that to it.' =>
      context.l10n.opsIfBlockingThoseReleasesAlreadySentYour,
    'Awaiting approval' => context.l10n.opsLabelAwaitingApproval,
    'Applying' => context.l10n.opsLabelApplying,
    'Applied' => context.l10n.opsLabelApplied,
    'Rejected' => context.l10n.opsLabelRejected,
    'Replaced by a newer proposal' =>
      context.l10n.opsLabelReplacedByANewerProposal,
    'Expired' => context.l10n.opsLabelExpired,
    'Not applied' => context.l10n.opsLabelNotApplied,
    'Wrong episode/movie' => context.l10n.opsLabelWrongEpisodeMovie,
    'Bad / corrupt copy' => context.l10n.opsLabelBadCorruptCopy,
    'Wrong or missing audio language' =>
      context.l10n.opsLabelWrongOrMissingAudioLanguage,
    'Something else' => context.l10n.opsLabelSomethingElse,
    'Reported' => context.l10n.opsLabelReported,
    'Watching the download' => context.l10n.opsLabelWatchingTheDownload,
    'Download recovery in progress' =>
      context.l10n.opsLabelDownloadRecoveryInProgress,
    'Waiting and retrying' => context.l10n.opsLabelWaitingAndRetrying,
    'Checking the problem' => context.l10n.opsLabelCheckingTheProblem,
    'Needs your reply' => context.l10n.opsLabelNeedsYourReply,
    'Fix ready for review' => context.l10n.opsLabelFixReadyForReview,
    'Did the fix work?' => context.l10n.opsLabelDidTheFixWork,
    'Resolved' => context.l10n.opsLabelResolved,
    'Closed without a fix' => context.l10n.opsLabelClosedWithoutAFix,
    'Could not resolve' => context.l10n.opsLabelCouldNotResolve,
    'Dismissed' => context.l10n.opsLabelDismissed,
    'Unknown' => context.l10n.opsLabelUnknown,
    'Review completed' => context.l10n.opsLabelReviewCompleted,
    'Media became available' => context.l10n.opsLabelMediaBecameAvailable,
    'Closed after no reply' => context.l10n.opsLabelClosedAfterNoReply,
    'Confirmed fixed' => context.l10n.opsLabelConfirmedFixed,
    'Closed after review' => context.l10n.opsLabelClosedAfterReview,
    'Completed after review' => context.l10n.opsLabelCompletedAfterReview,
    'Shared AI recovered' => context.l10n.opsLabelSharedAIRecovered,
    'Notifications recovered' => context.l10n.opsLabelNotificationsRecovered,
    'Book imports cleared' => context.l10n.opsLabelBookImportsCleared,
    'Dead download removed; no copy available yet' =>
      context.l10n.opsLabelDeadDownloadRemovedNoCopyAvailableYet,
    'Bad download removed; episode has not aired yet' =>
      context.l10n.opsLabelBadDownloadRemovedEpisodeHasNotAiredYet,
    'How it closed is unknown' => context.l10n.opsLabelHowItClosedIsUnknown,
    'No reply was received before the waiting period ended.' =>
      context.l10n.opsLabelNoReplyWasReceivedBeforeTheWaitingPeriod,
    'Prevention' => context.l10n.opsLabelPrevention,
    'Series' => context.l10n.opsLabelSeries,
    'Investigate only' => context.l10n.opsLabelInvestigateOnly,
    'Propose fixes for review' => context.l10n.opsLabelProposeFixesForReview,
    'Denied' => context.l10n.opsLabelDenied,
    'Failed' => context.l10n.opsLabelFailed,
    'Superseded' => context.l10n.opsLabelSuperseded,
    'Outcome unknown' => context.l10n.opsLabelOutcomeUnknown,
    'Investigation in progress' => context.l10n.opsLabelInvestigationInProgress,
    'Investigation completed' => context.l10n.opsLabelInvestigationCompleted,
    'Investigation failed' => context.l10n.opsLabelInvestigationFailed,
    'Investigation stopped without a fix' =>
      context.l10n.opsLabelInvestigationStoppedWithoutAFix,
    'Waiting for a reply' => context.l10n.opsLabelWaitingForAReply,
    'Waiting for fix review' => context.l10n.opsLabelWaitingForFixReview,
    'Ready to continue after a reply or decision' =>
      context.l10n.opsLabelReadyToContinueAfterAReplyOrDecision,
    'Investigation stopped' => context.l10n.opsLabelInvestigationStopped,
    'Investigation status unknown' =>
      context.l10n.opsLabelInvestigationStatusUnknown,
    'Resolution verified' => context.l10n.opsLabelResolutionVerified,
    'Reached the investigation step limit' =>
      context.l10n.opsLabelReachedTheInvestigationStepLimit,
    'Reached the investigation time limit' =>
      context.l10n.opsLabelReachedTheInvestigationTimeLimit,
    'Reached the investigation limit' =>
      context.l10n.opsLabelReachedTheInvestigationLimit,
    'The AI provider returned an error' =>
      context.l10n.opsLabelTheAIProviderReturnedAnError,
    'The investigation service returned an error' =>
      context.l10n.opsLabelTheInvestigationServiceReturnedAnError,
    'No reliable diagnosis was found' =>
      context.l10n.opsLabelNoReliableDiagnosisWasFound,
    'The proposed resolution could not be verified' =>
      context.l10n.opsLabelTheProposedResolutionCouldNotBeVerified,
    'Waiting for an admin to review a fix' =>
      context.l10n.opsLabelWaitingForAnAdminToReviewAFix,
    'Waiting for the reporter to reply' =>
      context.l10n.opsLabelWaitingForTheReporterToReply,
    'The issue changed outside this run' =>
      context.l10n.opsLabelTheIssueChangedOutsideThisRun,
    'Dismissed by an admin' => context.l10n.opsLabelDismissedByAnAdmin,
    'Reopened for administrator review' =>
      context.l10n.opsLabelReopenedForAdministratorReview,
    'Completed after admin review' =>
      context.l10n.opsLabelCompletedAfterAdminReview,
    'Stopped because the issue closed' =>
      context.l10n.opsLabelStoppedBecauseTheIssueClosed,
    'Interrupted by a server restart' =>
      context.l10n.opsLabelInterruptedByAServerRestart,
    'Stopped because an approved action needs manual verification' => context
        .l10n.opsLabelStoppedBecauseAnApprovedActionNeedsManualVerification,
    'Stopped because the media service resumed download recovery' =>
      context.l10n.opsLabelStoppedBecauseTheMediaServiceResumedDownloadRecovery,
    'Stopped because the live media state changed' =>
      context.l10n.opsLabelStoppedBecauseTheLiveMediaStateChanged,
    'Stopped because the latest media state could not be verified' =>
      context.l10n.opsLabelStoppedBecauseTheLatestMediaStateCouldNot,
    'Stopped because the saved investigation could not be resumed' =>
      context.l10n.opsLabelStoppedBecauseTheSavedInvestigationCouldNotBe,
    'Stopped because a legacy fix lacked verified release details' =>
      context.l10n.opsLabelStoppedBecauseALegacyFixLackedVerifiedRelease,
    'Stopped for an unrecognized reason' =>
      context.l10n.opsLabelStoppedForAnUnrecognizedReason,
    'Connected app' => context.l10n.opsLabelConnectedApp,
    'AI Assistant' => context.l10n.opsLabelAIAssistant,
    'Admin restore' => context.l10n.opsLabelAdminRestore,
    'External MCP' => context.l10n.opsLabelExternalMCP,
    'Setup' => context.l10n.opsLabelSetup,
    'Admin UI' => context.l10n.opsLabelAdminUI,
    'Restored' => context.l10n.opsLabelRestored,
    'Attempted' => context.l10n.opsLabelAttempted,
    'Intended' => context.l10n.opsLabelIntended,
    'Not set' => context.l10n.opsLabelNotSet,
    'Disabled' => context.l10n.opsLabelDisabled,
    'Empty' => context.l10n.opsLabelEmpty,
    'Matches applied' => context.l10n.opsLabelMatchesApplied,
    'Matches attempted' => context.l10n.opsLabelMatchesAttempted,
    'Matches intended' => context.l10n.opsLabelMatchesIntended,
    'Matches before' => context.l10n.opsLabelMatchesBefore,
    'Before, applied, and current' =>
      context.l10n.opsLabelBeforeAppliedAndCurrent,
    'Before, attempted, and current' =>
      context.l10n.opsLabelBeforeAttemptedAndCurrent,
    'Before, intended, and current' =>
      context.l10n.opsLabelBeforeIntendedAndCurrent,
    'This fix is no longer awaiting a decision.' =>
      context.l10n.opsValidationThisFixIsNoLongerAwaitingA,
    'The proposed fix has an invalid identifier.' =>
      context.l10n.opsValidationTheProposedFixHasAnInvalidIdentifier,
    'The issue is already closed, so this fix will not run.' =>
      context.l10n.opsValidationTheIssueIsAlreadyClosedSoThis,
    'The issue state could not be verified. Refresh before deciding.' =>
      context.l10n.opsValidationTheIssueStateCouldNotBeVerified,
    'The issue is no longer waiting for this fix to be reviewed.' =>
      context.l10n.opsValidationTheIssueIsNoLongerWaitingFor,
    'This app does not recognize the proposed fix type.' =>
      context.l10n.opsValidationThisAppDoesNotRecognizeTheProposed,
    'The proposed fix data is malformed.' =>
      context.l10n.opsValidationTheProposedFixDataIsMalformed,
    'The target instance could not be verified. Refresh before deciding.' =>
      context.l10n.opsValidationTheTargetInstanceCouldNotBeVerified,
    'The target service does not match this issue. Refresh before deciding.' =>
      context.l10n.opsValidationTheTargetServiceDoesNotMatchThis,
    'The server says this fix can no longer be approved or denied.' =>
      context.l10n.opsValidationTheServerSaysThisFixCanNo,
    'Unknown service' => context.l10n.opsValidationUnknownService,
    'The proposed fix contains fields this app does not recognize.' =>
      context.l10n.opsValidationTheProposedFixContainsFieldsThisApp,
    'The proposed fix has an invalid media type.' =>
      context.l10n.opsValidationTheProposedFixHasAnInvalidMedia,
    'The release details needed to apply this fix are missing.' =>
      context.l10n.opsValidationTheReleaseDetailsNeededToApplyThis,
    'The server-observed release details are missing or malformed.' =>
      context.l10n.opsValidationTheServerObservedReleaseDetailsAreMissing,
    'The proposed queue item is invalid.' =>
      context.l10n.opsValidationTheProposedQueueItemIsInvalid,
    'The proposed queue change is not recognized.' =>
      context.l10n.opsValidationTheProposedQueueChangeIsNotRecognized,
    'The proposed import options are malformed.' =>
      context.l10n.opsValidationTheProposedImportOptionsAreMalformed,
    'The proposed search details are malformed.' =>
      context.l10n.opsValidationTheProposedSearchDetailsAreMalformed,
    'The book or author needed for this search is missing.' =>
      context.l10n.opsValidationTheBookOrAuthorNeededForThis,
    'The proposed book search contains invalid media details.' =>
      context.l10n.opsValidationTheProposedBookSearchContainsInvalidMedia,
    'The album or artist needed for this search is missing.' =>
      context.l10n.opsValidationTheAlbumOrArtistNeededForThis,
    'The proposed music search contains invalid media details.' =>
      context.l10n.opsValidationTheProposedMusicSearchContainsInvalidMedia,
    'The proposed search contains music details.' =>
      context.l10n.opsValidationTheProposedSearchContainsMusicDetails,
    'The title needed for this search is missing.' =>
      context.l10n.opsValidationTheTitleNeededForThisSearchIs,
    'The proposed movie search contains TV episode details.' =>
      context.l10n.opsValidationTheProposedMovieSearchContainsTVEpisode,
    'The proposed TV season is invalid.' =>
      context.l10n.opsValidationTheProposedTVSeasonIsInvalid,
    'The proposed episode search is missing its season or episode.' =>
      context.l10n.opsValidationTheProposedEpisodeSearchIsMissingIts,
    'The proposed rescan details are malformed.' =>
      context.l10n.opsValidationTheProposedRescanDetailsAreMalformed,
    'The author needed for this rescan is missing.' =>
      context.l10n.opsValidationTheAuthorNeededForThisRescanIs,
    'The artist needed for this rescan is missing.' =>
      context.l10n.opsValidationTheArtistNeededForThisRescanIs,
    'The title needed for this rescan is missing.' =>
      context.l10n.opsValidationTheTitleNeededForThisRescanIs,
    'The proposed deletion options are malformed.' =>
      context.l10n.opsValidationTheProposedDeletionOptionsAreMalformed,
    'The book whose files would be deleted is missing.' =>
      context.l10n.opsValidationTheBookWhoseFilesWouldBeDeleted,
    'The proposed book deletion contains invalid media details.' =>
      context.l10n.opsValidationTheProposedBookDeletionContainsInvalidMedia,
    'The album whose files would be deleted is missing.' =>
      context.l10n.opsValidationTheAlbumWhoseFilesWouldBeDeleted,
    'The proposed music deletion contains invalid media details.' =>
      context.l10n.opsValidationTheProposedMusicDeletionContainsInvalidMedia,
    'The proposed deletion contains book details.' =>
      context.l10n.opsValidationTheProposedDeletionContainsBookDetails,
    'The proposed deletion contains music details.' =>
      context.l10n.opsValidationTheProposedDeletionContainsMusicDetails,
    'The title whose files would be deleted is missing.' =>
      context.l10n.opsValidationTheTitleWhoseFilesWouldBeDeleted,
    'The proposed movie deletion contains TV episode details.' =>
      context.l10n.opsValidationTheProposedMovieDeletionContainsTVEpisode,
    'The list of episodes to delete is malformed.' =>
      context.l10n.opsValidationTheListOfEpisodesToDeleteIs,
    'The episodes whose files would be deleted are missing.' =>
      context.l10n.opsValidationTheEpisodesWhoseFilesWouldBeDeleted,
    'The proposed episode numbers are invalid.' =>
      context.l10n.opsValidationTheProposedEpisodeNumbersAreInvalid,
    'The proposed deletion covers too many episodes to review.' =>
      context.l10n.opsValidationTheProposedDeletionCoversTooManyEpisodes,
    'Apple TV control is not installed on this server.' =>
      context.l10n.opsTvErrorAppleTVControlIsNotInstalledOn,
    'Check the TV details and access choices, then try again.' =>
      context.l10n.opsTvErrorCheckTheTVDetailsAndAccessChoices,
    'Pairing ended. Start again with a new PIN.' =>
      context.l10n.opsTvErrorPairingEndedStartAgainWithANew,
    'Pair this Apple TV again.' => context.l10n.opsTvErrorPairThisAppleTVAgain,
    'A different device answered. Check the TV address.' =>
      context.l10n.opsTvErrorADifferentDeviceAnsweredCheckTheTV,
    'Install Infuse on this Apple TV, then try again.' =>
      context.l10n.opsTvErrorInstallInfuseOnThisAppleTVThen,
    'The server could not reach the TV. Check its address and network.' =>
      context.l10n.opsTvErrorTheServerCouldNotReachTheTV,
    'The TV did not answer in time. Check the TV before trying again.' =>
      context.l10n.opsTvErrorTheTVDidNotAnswerInTime,
    'Another action is in progress on this TV. Try again shortly.' =>
      context.l10n.opsTvErrorAnotherActionIsInProgressOnThis,
    'This Apple TV is no longer available to your account.' =>
      context.l10n.opsTvErrorThisAppleTVIsNoLongerAvailable,
    'This title is not available through your media servers.' =>
      context.l10n.opsTvErrorThisTitleIsNotAvailableThroughYour,
    'Could not verify your access to this title. Try again.' =>
      context.l10n.opsTvErrorCouldNotVerifyYourAccessToThis,
    'The TV rejected the request. Check the TV before trying again.' =>
      context.l10n.opsTvErrorTheTVRejectedTheRequestCheckThe,
    'Confirmation expired. Open the title again if needed.' =>
      context.l10n.opsTvErrorConfirmationExpiredOpenTheTitleAgainIf,
    'Could not finish the request. Check the TV before trying again.' =>
      context.l10n.opsTvErrorCouldNotFinishTheRequestCheckThe,
    _ => label,
  };
}

/// Only translates placeholder values emitted by the client formatter.
String localizedConfigValue(BuildContext context, Object? value) {
  final text = formatConfigValue(value);
  return value == null || value is bool || (value is String && value.isEmpty)
      ? localizeOperationsLabel(context, text)
      : text;
}
