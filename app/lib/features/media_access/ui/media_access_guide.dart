import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/link.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/media_access_service.dart';
import '../logic/media_app_launcher.dart';
import '../data/video_apps.dart';
import '../logic/video_apps_provider.dart';
import '../logic/media_access_guide_provider.dart';
import 'media_server_email_sheet.dart';
import 'media_server_password_sheet.dart';
import 'media_server_sign_in_sheet.dart';
import 'plex_sign_in_sheet.dart';
import '../logic/listen_links_provider.dart';

/// Requester-focused guide for the media servers (Plex, Jellyfin, Emby, Audiobookshelf)
/// shared with this account: create the account with a password only they
/// know, or link one they already have by signing in with it; on Plex, sign
/// in with their own Plex account or share the email their invite goes to;
/// see where to sign in and start watching or listening. Everything here is
/// re-read from the server on every open and on pull-to-refresh: the rows
/// behind it are an action log and the media server is the truth, which is
/// also why an unconfirmed account is said to be unconfirmed rather than
/// shown as fact. A user with no Plex grant on a server that has Plex sees
/// one card to ask for it: signing in with Plex or sharing their email tells
/// the admin where to send the invite.
class MediaAccessGuide extends ConsumerStatefulWidget {
  const MediaAccessGuide({super.key});

  @override
  ConsumerState<MediaAccessGuide> createState() => _MediaAccessGuideState();
}

class _MediaAccessGuideState extends ConsumerState<MediaAccessGuide>
    with WidgetsBindingObserver {
  List<MediaServerAccess>? _servers;
  bool _failed = false;
  int _loadGeneration = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _load();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _load();
  }

  Future<void> _load() async {
    final generation = ++_loadGeneration;
    try {
      final servers = await ref.read(mediaAccessServiceProvider).listMine();
      if (!mounted || generation != _loadGeneration) return;
      ref.read(mediaAccessRevisionProvider.notifier).state++;
      setState(() {
        _servers = servers;
        _failed = false;
      });
    } catch (_) {
      if (!mounted || generation != _loadGeneration) return;
      if (_servers == null) {
        setState(() => _failed = true);
        return;
      }
      // Retain the known servers, but do not keep presenting old account
      // verification or actions as current after a failed read.
      setState(() => _failed = true);
    }
  }

  void _retry() {
    if (_servers == null) setState(() => _failed = false);
    _load();
  }

  Future<void> _createAccount(MediaServerAccess server, String username) async {
    final outcome = await showMediaServerPasswordSheet(
      context,
      server: server,
      username: username,
    );
    if (!mounted || outcome == null) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(switch (outcome) {
        MediaServerPasswordSheetOutcome.created =>
          context.l10n.mediaAccountCreatedSignInWithYourNewPassword,
        MediaServerPasswordSheetOutcome.accountExists =>
          context.l10n.mediaYouAlreadyHaveAnAccountHere,
      }),
    ));
    await _load();
  }

  Future<void> _linkOwnAccount(
      MediaServerAccess server, String username) async {
    final outcome = await showMediaServerSignInSheet(
      context,
      server: server,
      username: username,
    );
    if (!mounted || outcome == null) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(switch (outcome) {
        MediaServerSignInSheetOutcome.linked =>
          context.l10n.mediaAccountLinkedSignInWithYourUsualPassword,
        MediaServerSignInSheetOutcome.accountExists =>
          context.l10n.mediaYouAlreadyHaveAnAccountHere,
      }),
    ));
    await _load();
  }

  /// Signs in with the person's own Plex account. The server remembers the
  /// verified email and runs the same share pass a shared email does, so
  /// the snackbar says what that led to.
  Future<void> _signInWithPlex() async {
    final state = await showPlexSignInSheet(context);
    if (!mounted || state == null) return;
    final who = state.username.isNotEmpty ? state.username : state.email;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text((switch (state.inviteState) {
            'sent' => context.l10n
                .mediaSignedInAsValue0InviteSentCheckYourEmail(
                    (who).toString()),
            'adopted' => context.l10n
                .mediaSignedInAsValue0YourAccessIsSetUp((who).toString()),
            'unlinked' => context.l10n
                .mediaSignedInAsValue0YourServerConnectionStaysUnlinkedUseLinkMyPlexAccountToReconnectIt(
                    (who).toString()),
            'failed' => context.l10n
                .mediaSignedInAsValue0ButTheInviteCouldnTBeSentYetItWillBeRetried(
                    (who).toString()),
            'claimed' => context.l10n
                .mediaSignedInAsValue0ButThatPlexAccountIsAlreadyLinkedToAnotherCantinarrUserHereAskYourAdmin(
                    (who).toString()),
            _ => context.l10n.mediaSignedInAsValue0YourAdminHasBeenNotified(
                (who).toString()),
          }) +
          (state.identityError.isEmpty
              ? ''
              : context.l10n.mediaPlexSignInWasNotLinkedValue0(
                  (state.identityError).toString()))),
    ));
    // The profile's email changed; the ask card reads it from there, and a
    // grant that auto-approve added shows up in the config.
    final auth = ref.read(authProvider.notifier);
    auth.refreshUser();
    auth.refreshConfig();
    await _load();
  }

  Future<void> _requestInvite(MediaServerAccess server, String email) async {
    final outcome = await showMediaServerEmailSheet(
      context,
      server: server,
      initialEmail: email,
    );
    if (!mounted || outcome == null) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(switch (outcome) {
        MediaServerEmailSheetOutcome.requested =>
          context.l10n.mediaInviteSentCheckYourEmailThenAcceptIt,
        MediaServerEmailSheetOutcome.accountExists =>
          context.l10n.mediaYouAlreadyHaveAccessHere,
      }),
    ));
    // The profile's email changed too; the ask card reads it from there.
    ref.read(authProvider.notifier).refreshUser();
    await _load();
  }

  /// Shares the email an admin should send the Plex invite to, for a user
  /// who holds no Plex grant yet. The server tells the admins; with
  /// auto-approve on it grants and invites right away.
  Future<void> _askForPlexAccess(String current) async {
    final controller = TextEditingController(text: current);
    String? errorText;
    var saving = false;
    await showDialog<void>(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) {
          Future<void> submit() async {
            final email = controller.text.trim();
            if (!looksLikeEmail(email)) {
              setDialogState(
                  () => errorText = context.l10n.mediaEnterAValidEmailAddress);
              return;
            }
            setDialogState(() {
              saving = true;
              errorText = null;
            });
            try {
              await ref.read(authProvider.notifier).setPlexEmail(email);
              if (dialogContext.mounted) Navigator.of(dialogContext).pop();
              if (!mounted) return;
              ScaffoldMessenger.of(this.context).showSnackBar(SnackBar(
                content: Text(context.l10n.mediaThanksYourAdminHasBeenNotified),
              ));
              // With auto-approve the grant and invite land within seconds:
              // re-read so the card flips to the invite.
              Future.delayed(const Duration(seconds: 3), () {
                if (!mounted) return;
                ref.read(authProvider.notifier).refreshConfig();
                _load();
              });
            } catch (_) {
              setDialogState(() {
                saving = false;
                errorText = context.l10n.mediaCouldnTSendCheckYourConnection;
              });
            }
          }

          return AlertDialog(
            title: Text(context.l10n.mediaYourPlexEmail),
            content: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  context.l10n
                      .mediaEnterTheEmailOfYourPlexAccountYourAdminSendsTheInviteThere,
                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: controller,
                  enabled: !saving,
                  autofocus: true,
                  keyboardType: TextInputType.emailAddress,
                  autocorrect: false,
                  textInputAction: TextInputAction.done,
                  decoration: InputDecoration(
                    labelText: context.l10n.mediaEmail,
                    hintText: 'you@example.com',
                    prefixIcon: const Icon(Icons.mail_outline),
                    errorText: errorText,
                  ),
                  onSubmitted: (_) => submit(),
                ),
              ],
            ),
            actions: [
              TextButton(
                onPressed:
                    saving ? null : () => Navigator.of(dialogContext).pop(),
                child: Text(context.l10n.adminCancel),
              ),
              ElevatedButton(
                onPressed: saving ? null : submit,
                child: saving
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : Text(context.l10n.mediaSend),
              ),
            ],
          );
        },
      ),
    );
    // The dialog's field still references the controller while the close
    // animation runs, so it is left to the garbage collector, as the text
    // fields in ad-hoc dialogs elsewhere are.
  }

  Future<void> _copy(String text, String confirmation) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(confirmation)),
    );
  }

  String _openLabel(MediaServerAccess server) => VideoApps.serviceTypes
              .contains(server.serviceType) &&
          ref.read(mediaAppLauncherProvider).videoAppFor(server.videoApps) ==
              VideoApp.infuse
      ? context.l10n.mediaOpenInInfuse
      : context.l10n.mediaOpenValue0(
          (mediaServerTypeLabel(server.serviceType)).toString());

  Future<void> _open(MediaServerAccess server) async {
    final launcher = ref.read(mediaAppLauncherProvider);
    final opened = server.serviceType == 'audiobookshelf'
        ? await launcher.openAudiobook(
            webUrl: server.publicAddress, apps: server.listeningApps)
        : await launcher.open(
            serviceType: server.serviceType,
            apps: server.videoApps,
            webUrl: server.publicAddress,
          );
    if (opened || !mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(context.l10n.adminCouldnTOpen((server.name).toString())),
    ));
  }

  Future<void> _setHidden(bool hidden) async {
    try {
      await ref.read(mediaAccessGuideHiddenProvider.notifier).setHidden(hidden);
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(context.l10n.mediaCouldnTSaveThisPreferenceOnThisDevice),
      ));
    }
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authProvider).valueOrNull;
    ref.listen(videoAppRevisionProvider, (_, __) => _load());
    ref.listen(authProvider, (previous, next) {
      if (previous?.valueOrNull?.connection == null) return;
      if (previous?.valueOrNull?.user?.id != next.valueOrNull?.user?.id ||
          previous?.valueOrNull?.connection?.serverUrl !=
              next.valueOrNull?.connection?.serverUrl) {
        setState(() {
          _servers = null;
          _failed = false;
        });
      }
      _load();
    });
    final user = auth?.user;
    final servers = _servers;
    // A Plex server the user is not granted yet still gets a card (ask for
    // access), so it counts toward the title and the guide's product set.
    final plexRequestable = auth?.connection?.plexAccessRequestable ?? false;
    // Until the live list arrives, the granted set from /api/config names
    // the same servers, so the title never flashes a placeholder.
    final types = {
      ...(servers != null
          ? servers.map((server) => server.serviceType)
          : (auth?.connection?.mediaServerInstances ?? const [])
              .map((instance) => instance.serviceType)),
      if (plexRequestable) 'plex',
    };
    final askForPlex = plexRequestable &&
        servers != null &&
        !servers.any((server) => server.serviceType == 'plex');

    return Scaffold(
      appBar: AppBar(title: Text(mediaServerGuideTitle(types))),
      body: CenteredContent(
        child: Column(children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
            child: _card(
              Material(
                color: Colors.transparent,
                child: SwitchListTile.adaptive(
                  contentPadding: EdgeInsets.zero,
                  title: Text(context.l10n.mediaHideFromMainNavigation),
                  subtitle: Text(
                    context.l10n
                        .mediaYouCanAlwaysOpenThisGuideFromSettingsGuidesMediaServerAccess,
                  ),
                  value: ref.watch(mediaAccessGuideHiddenProvider),
                  onChanged: _setHidden,
                ),
              ),
            ),
          ),
          Expanded(
              child: servers == null
                  ? (_failed ? _buildLoadFailure() : _buildLoading())
                  : RefreshIndicator(
                      onRefresh: _load,
                      child: ListView(
                        padding: const EdgeInsets.all(16),
                        children: _failed && servers.isEmpty
                            ? [_buildLoadFailure()]
                            : servers.isEmpty && !askForPlex
                                ? [_buildEmpty(isAdmin: user?.isAdmin == true)]
                                : _buildGuide(
                                    servers,
                                    username: user?.username ?? '',
                                    plexEmail: user?.plexEmail ?? '',
                                    types: types,
                                    askForPlex: askForPlex,
                                  ),
                      ),
                    )),
        ]),
      ),
    );
  }

  Widget _buildLoading() => const Center(child: CircularProgressIndicator());

  Widget _buildLoadFailure() {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Text(
            context.l10n.mediaCouldnTLoadYourMediaServers,
            style: TextStyle(color: AppTheme.textSecondary),
          ),
          const SizedBox(height: 16),
          ElevatedButton(
              onPressed: _retry, child: Text(context.l10n.adminRetry)),
        ],
      ),
    );
  }

  /// Nothing granted. An admin can fix that themselves, so they are told
  /// where; a requester can only ask.
  Widget _buildEmpty({required bool isAdmin}) {
    return Padding(
      padding: const EdgeInsets.only(top: 48),
      child: Column(
        children: [
          const Icon(Icons.live_tv_outlined,
              size: 40, color: AppTheme.textMuted),
          const SizedBox(height: 12),
          Text(
            isAdmin
                ? context.l10n
                    .mediaNoMediaServerIsSharedWithYourAccountYetOpenTheInstanceUnderSettingsAndAddYourselfUnderUser
                : context.l10n
                    .mediaNoMediaServerIsSharedWithYouYetAskYourAdminForAccess,
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.5,
            ),
            textAlign: TextAlign.center,
          ),
        ],
      ),
    );
  }

  List<Widget> _buildGuide(
    List<MediaServerAccess> servers, {
    required String username,
    required String plexEmail,
    required Set<String> types,
    required bool askForPlex,
  }) {
    final orderedTypes = ['plex', 'jellyfin', 'emby', 'audiobookshelf']
        .where(types.contains)
        .toList();
    final onlyPlex = types.length == 1 && types.contains('plex');
    final activity = types.contains('audiobookshelf')
        ? types.length == 1
            ? 'listen'
            : context.l10n.mediaWatchOrListen
        : 'watch';
    return [
      Text(
        context.l10n
            .mediaEachServerHasItsOwnAccountOrInvitationUseTheCredentialsForThatServerEvenWhenTwoServersUseT(
                (types.contains('plex')
                        ? ' For Plex, use the Plex account that accepted each invitation.'
                        : '')
                    .toString()),
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 14,
          height: 1.5,
        ),
      ),
      const SizedBox(height: 24),
      if (_failed) ...[
        Text(
          context.l10n
              .mediaCouldNotRefreshYourMediaServersTheNamesAndAddressesBelowAreFromTheLastSuccessfulCheck,
          style: TextStyle(color: AppTheme.warning),
        ),
        TextButton.icon(
          onPressed: _retry,
          icon: const Icon(Icons.refresh),
          label: Text(context.l10n.adminRetry),
        ),
        const SizedBox(height: 12),
      ],
      _SectionHeader(
        number: 1,
        title: onlyPlex
            ? context.l10n.mediaYourInvite
            : servers.length > 1
                ? context.l10n.mediaYourAccounts
                : context.l10n.mediaYourAccount,
      ),
      const SizedBox(height: 12),
      for (final server in servers)
        Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: _failed
              ? _buildUnavailableCard(server)
              : server.isInvite
                  ? _buildInviteCard(server, plexEmail)
                  : _buildAccountCard(server, username),
        ),
      if (askForPlex && !_failed)
        Padding(
          padding: const EdgeInsets.only(bottom: 12),
          child: _buildAskForPlexCard(plexEmail),
        ),
      for (var i = 0; i < orderedTypes.length; i++) ...[
        const SizedBox(height: 24),
        _serviceInstructions(orderedTypes[i], number: i + 2),
      ],
      if (types.any(VideoApps.serviceTypes.contains)) ...[
        const SizedBox(height: 24),
        _TipCard(
          title: context.l10n.mediaChooseYourVideoApp,
          message: context.l10n
              .mediaOnIPhoneAndIPadChooseYourMediaServerSAppInfuseOrBrowserInSettingsAccountVideoAppsOrFollowY,
        ),
        _guideLink(context.l10n.mediaGetInfuse, 'https://firecore.com/infuse'),
      ],
      const SizedBox(height: 24),
      _TipCard(
        title: context.l10n.mediaRequestHereValue0There((activity).toString()),
        message: context.l10n
            .mediaAvailableMeansTheDownloadedFilesHaveArrivedYourMediaServerStillNeedsToScanThemBeforeTheyAp(
                (types.any((type) => type != 'audiobookshelf')
                        ? 'On phones and tablets, a media app may open its home screen even from a verified Watch link. Choose the server and find the title there. '
                        : '')
                    .toString()),
      ),
    ];
  }

  Widget _guideLink(String label, String url) => Link(
        uri: Uri.parse(url),
        target: LinkTarget.blank,
        builder: (context, followLink) => Align(
          alignment: Alignment.centerLeft,
          child: TextButton.icon(
              onPressed: followLink,
              icon: const Icon(Icons.open_in_new, size: 16),
              label: Text(label)),
        ),
      );

  Widget _buildUnavailableCard(MediaServerAccess server) => _card(Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('${mediaServerTypeLabel(server.serviceType)} · ${server.name}',
              style: const TextStyle(
                  color: AppTheme.textPrimary, fontWeight: FontWeight.w600)),
          const SizedBox(height: 6),
          Text(context.l10n.mediaAccessCouldNotBeVerified,
              style: TextStyle(color: AppTheme.warning)),
          const SizedBox(height: 8),
          if (server.account != null)
            Text(
                context.l10n.mediaLastKnownAccountValue0(
                    (server.account!.username).toString()),
                style: const TextStyle(color: AppTheme.textSecondary)),
          Text(
            context.l10n
                .mediaRetryToCheckYourAccountAndAvailableActionsOpeningTheServerDoesNotConfirmYouStillHaveAccess,
            style: TextStyle(color: AppTheme.textSecondary),
          ),
          if (server.publicAddress.isNotEmpty) ...[
            const SizedBox(height: 8),
            Text(server.publicAddress),
            Wrap(spacing: 8, children: [
              TextButton.icon(
                onPressed: () => _copy(
                    server.publicAddress, context.l10n.mediaAddressCopied),
                icon: const Icon(Icons.copy, size: 16),
                label: Text(context.l10n.mediaCopyAddress),
              ),
              TextButton.icon(
                onPressed: () => _open(server),
                icon: const Icon(Icons.open_in_new, size: 16),
                label: Text(_openLabel(server)),
              ),
            ]),
          ],
        ],
      ));

  Widget _serviceInstructions(String type, {required int number}) {
    final label = mediaServerTypeLabel(type);
    return _GuideSection(
      key: ValueKey('media-guide-instructions-$type'),
      number: number,
      title: label,
      steps: switch (type) {
        'plex' => [
            context.l10n
                .mediaInstallThePlexAppOnYourPhoneTVOrComputerOrOpenAppPlexTvInYourBrowser,
            context.l10n
                .mediaYourPlexInviteArrivesByEmailFromPlexOpenItAndAcceptPendingInvitesAreAlsoUnderTheBellIconAt,
            context.l10n
                .mediaSignInToPlexWithTheSamePlexAccountThenChooseTheSharedServerAndItsLibraries,
            context.l10n
                .mediaRemoteVideoPlaybackInSupportedAppsRequiresTheServerOwnerToHavePlexPassOrYourAccountToHaveP,
            ..._videoPlaybackSteps(label),
          ],
        'jellyfin' || 'emby' => [
            context.l10n
                .mediaInstallTheValue0AppForYourPhoneTVOrComputerTheDownloadPageListsTheSupportedDevices(
                    (label).toString()),
            context.l10n
                .mediaInTheAppEnterTheSignInAddressFromTheValue0ServerSCardAboveYouCanAlsoOpenThatAddressInYourB(
                    (label).toString()),
            context.l10n
                .mediaSignInWithThatServerSUsernameAndThePasswordYouChoseWhenCreatingTheAccountOrYourUsualPasswo,
            if (type == 'emby')
              context.l10n
                  .mediaSomeEmbyAppsNeedAOneTimeUnlockOrEmbyPremiereForFullPlaybackCheckTheRequirementsForYourDevi,
            ..._videoPlaybackSteps(label),
          ],
        _ => [
            context.l10n
                .mediaOpenAudiobookshelfUsingTheSignInAddressInItsCardAboveInYourBrowserOrInstallAnAudiobookshel,
            context.l10n
                .mediaInTheAppEnterThatServerSSignInAddressUseTheUsernameAndPasswordYouCreatedOrLinkedForThatAud,
            context.l10n
                .mediaYouAlsoNeedSeparateChaptarrAccessToSeeAndRequestBooksInCantinarrAudiobookshelfAccessDoesNo,
            context.l10n
                .mediaAudiobookshelfNeedsToScanTheAudiobookLibraryAfterADownloadFinishes,
            context.l10n
                .mediaChooseYourListeningAppInSettingsAccountListeningAppsOrFollowYourAdminSDefaultForThisServer,
            context.l10n
                .mediaOnAnAvailableAudiobookListenOpensAVerifiedCopyInYourBrowserATitleSearchInShelfPlayerOrTheH,
            context.l10n
                .mediaOpenIsAGeneralShortcutWhenAMatchingCopyCannotBeVerifiedItDoesNotMeanTheBookIsInTheLibraryI,
          ],
      },
      links: switch (type) {
        'plex' => [
            (
              label: context.l10n.mediaDownloadPlexApps,
              url: 'https://www.plex.tv/media-server-downloads/#plex-app'
            ),
            (
              label: context.l10n.mediaPlexRemotePlaybackRequirements,
              url:
                  'https://support.plex.tv/articles/requirements-for-remote-playback-of-personal-media/'
            ),
          ],
        'jellyfin' => [
            (
              label: context.l10n.mediaDownloadJellyfinApps,
              url: 'https://jellyfin.org/downloads/clients/'
            ),
          ],
        'emby' => [
            (
              label: context.l10n.mediaDownloadEmbyApps,
              url: 'https://emby.media/download.html#emby-apps-and-devices'
            ),
            (
              label: context.l10n.mediaEmbyAppUnlocksAndPremiere,
              url: 'https://emby.media/premiere.html'
            ),
          ],
        _ => [
            (
              label: context.l10n.mediaDownloadAudiobookshelfApps,
              url: 'https://github.com/advplyr/audiobookshelf-app#readme'
            ),
            (
              label: context.l10n.mediaCompatibleAudiobookshelfApps,
              url:
                  'https://audiobookshelf.org/docs/documentation/community/community-apps/'
            ),
          ],
      },
    );
  }

  List<String> _videoPlaybackSteps(String label) => [
        context.l10n
            .mediaAfterTheLibraryScanCantinarrOffersWatchOnValue0WhenItVerifiesAMatchingMovieOrShowForYourAc(
                (label).toString()),
        context.l10n
            .mediaAnOpenShortcutOpensTheAppOrSignInAddressItDoesNotConfirmThatAParticularTitleIsAvailableOnV(
                (label).toString()),
      ];

  /// A Plex server's share state: nothing yet (share your email), an invite
  /// waiting to be accepted, or an accepted share (where to sign in).
  Widget _buildInviteCard(MediaServerAccess server, String plexEmail) {
    final account = server.account;
    final Widget body;
    if (account == null) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            server.autoLinkSuppressed
                ? context.l10n
                    .mediaYourAccountWasUnlinkedFromValue0LinkItAgainExplicitlyToReconnectThisServer(
                        (server.name).toString())
                : context.l10n
                    .mediaYourAdminHasOfferedAccessToValue0SignInWithPlexToLinkYourAccountOrShareTheEmailOfYourPlexA(
                        (server.name).toString()),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 12),
          ElevatedButton.icon(
            onPressed: server.autoLinkSuppressed
                ? () => _requestInvite(server, plexEmail)
                : _signInWithPlex,
            icon: const Icon(Icons.login, size: 18),
            label: Text(context.l10n.mediaLinkMyPlexAccount),
          ),
          if (!server.autoLinkSuppressed)
            TextButton.icon(
              onPressed: () => _requestInvite(server, plexEmail),
              icon: const Icon(Icons.mail_outline, size: 18),
              label: Text(context.l10n.mediaShareMyPlexEmail),
            ),
        ],
      );
    } else if (account.pending) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Padding(
                padding: EdgeInsets.only(top: 1),
                child: Icon(Icons.mark_email_unread_outlined,
                    color: AppTheme.requested, size: 18),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  context.l10n
                      .mediaInviteSentToValue0AcceptItFromTheEmailPlexSentYouOrUnderTheBellIconAtAppPlexTvAndValue1App(
                          (account.username).toString(),
                          (server.name).toString()),
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 14,
                    height: 1.4,
                  ),
                ),
              ),
            ],
          ),
          TextButton(
            onPressed: () => _requestInvite(server, plexEmail),
            style: TextButton.styleFrom(
              padding: EdgeInsets.zero,
              minimumSize: const Size(0, 32),
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
            child: Text(context.l10n.mediaWrongEmail),
          ),
          if (!account.verified) _buildUnconfirmed(),
        ],
      );
    } else {
      // The owner of the server is an accepted share of a kind: nothing to
      // accept, nothing Cantinarr will ever change.
      final owner = account.administrator;
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(account.verified ? Icons.check_circle : Icons.info_outline,
                  color:
                      account.verified ? AppTheme.available : AppTheme.warning,
                  size: 18),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  owner
                      ? context.l10n.mediaYouOwnValue0((server.name).toString())
                      : context.l10n.mediaValue0IsSharedWithValue1(
                          (server.name).toString(),
                          (account.username).toString()),
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
            ],
          ),
          if (owner) ...[
            const SizedBox(height: 4),
            Text(
              context.l10n.mediaSignedInAsValue0((account.username).toString()),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
          ],
          const SizedBox(height: 8),
          if (server.publicAddress.isNotEmpty) ...[
            Text(
              context.l10n
                  .mediaSignInAtValue0((server.publicAddress).toString()),
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
            Wrap(
              spacing: 8,
              children: [
                TextButton.icon(
                  onPressed: () => _copy(
                      server.publicAddress, context.l10n.mediaAddressCopied),
                  icon: const Icon(Icons.copy, size: 16),
                  label: Text(context.l10n.mediaCopyAddress),
                ),
                TextButton.icon(
                  onPressed: () => _open(server),
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: Text(_openLabel(server)),
                ),
              ],
            ),
          ],
          if (!account.verified) _buildUnconfirmed(),
        ],
      );
    }
    return _accountCardWithManagement(server, body);
  }

  /// A Plex server exists but this user holds no grant on it: signing in
  /// with Plex or sharing the email tells the admin where to send the invite
  /// (and, with auto-approve on, sends it at once).
  Widget _buildAskForPlexCard(String plexEmail) {
    final Widget body = plexEmail.isEmpty
        ? Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                context.l10n
                    .mediaThisServerHasPlexSignInWithPlexToAskForAccessOrShareTheEmailOfYourPlexAccountYourAdminGets,
                style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 14,
                  height: 1.4,
                ),
              ),
              const SizedBox(height: 12),
              ElevatedButton.icon(
                onPressed: _signInWithPlex,
                icon: const Icon(Icons.login, size: 18),
                label: Text(context.l10n.mediaSignInWithPlex),
              ),
              TextButton.icon(
                onPressed: () => _askForPlexAccess(plexEmail),
                icon: const Icon(Icons.mail_outline, size: 18),
                label: Text(context.l10n.mediaShareMyPlexEmail),
              ),
            ],
          )
        : Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  const Icon(Icons.hourglass_top,
                      color: AppTheme.requested, size: 18),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      plexEmail,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 8),
              Text(
                context.l10n
                    .mediaYourAdminHasBeenNotifiedOnceTheyGrantYouPlexTheInviteGoesToThisAddress,
                style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                  height: 1.4,
                ),
              ),
              Wrap(
                spacing: 16,
                children: [
                  TextButton(
                    onPressed: _signInWithPlex,
                    style: TextButton.styleFrom(
                      padding: EdgeInsets.zero,
                      minimumSize: const Size(0, 32),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: Text(context.l10n.mediaSignInWithPlex),
                  ),
                  TextButton(
                    onPressed: () => _askForPlexAccess(plexEmail),
                    style: TextButton.styleFrom(
                      padding: EdgeInsets.zero,
                      minimumSize: const Size(0, 32),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: Text(context.l10n.mediaChangeEmail),
                  ),
                ],
              ),
            ],
          );
    return _card(Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(context.l10n.mediaPlexRequestAccess,
            style: TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w600)),
        const SizedBox(height: 12),
        body,
      ],
    ));
  }

  Widget _buildUnconfirmed() {
    return Padding(
      padding: EdgeInsets.only(top: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.only(top: 1),
            child: Icon(Icons.info_outline, color: AppTheme.warning, size: 16),
          ),
          SizedBox(width: 6),
          Expanded(
            child: Text(
              context.l10n
                  .mediaWeCouldnTConfirmYourServerAccessJustNowTryAgainOrCheckWithYourAdmin,
              style: TextStyle(
                color: AppTheme.warning,
                fontSize: 12,
                height: 1.4,
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _accountCardWithManagement(MediaServerAccess server, Widget body) {
    final account = server.account;
    final supported = ref
            .watch(authProvider)
            .valueOrNull
            ?.connection
            ?.mediaAccountManagement ??
        false;
    return _card(
        Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text('${mediaServerTypeLabel(server.serviceType)} · ${server.name}',
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w600)),
      const SizedBox(height: 6),
      Text(
        account != null && !account.verified
            ? context.l10n.mediaAccessCouldNotBeVerified
            : account == null
                ? (server.autoLinkSuppressed
                    ? context.l10n.mediaAccountIntentionallyUnlinked
                    : context.l10n.mediaAccountNotLinked)
                : account.pending
                    ? context.l10n.mediaAwaitingInvitationAcceptance
                    : account.disabled
                        ? context.l10n.mediaAccessDisabled
                        : context.l10n.mediaAccessActive,
        style: TextStyle(
          color: account != null && !account.verified
              ? AppTheme.warning
              : AppTheme.textSecondary,
          fontSize: 13,
        ),
      ),
      const SizedBox(height: 12),
      body,
      const SizedBox(height: 12),
      Text(
        server.serviceType == 'plex'
            ? context.l10n
                .mediaUseYourPlexAccountInPlexLinkingLibraryAccessHereDoesNotChangeHowYouSignInToCantinarr
            : context.l10n
                .mediaUseYourValue0UsernameAndPasswordOnValue1TheseCredentialsAreSeparateFromSigningInToCantinar(
                    (mediaServerTypeLabel(server.serviceType)).toString(),
                    (server.name).toString()),
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
      ),
      if (supported && account != null) ...[
        const SizedBox(height: 10),
        Text(
            account.accessSyncPending
                ? context
                    .l10n.mediaYourServerAccessChangeIsPendingCantinarrWillRetry
                : account.administrator
                    ? context.l10n.mediaProtectedAdministratorAccount
                    : account.manageAccess
                        ? context.l10n
                            .mediaYourAdminManagesThisAccountSAccessThroughCantinarr
                        : context.l10n
                            .mediaLinkedOnlyYourAccountSAccessIsManagedOnValue0(
                                (server.name).toString()),
            style: TextStyle(
                color: account.accessSyncPending
                    ? AppTheme.warning
                    : AppTheme.textMuted,
                fontSize: 12)),
        if (!account.verified && account.disabled) _buildUnconfirmed(),
      ],
    ]));
  }

  Widget _card(Widget body) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.accent.withValues(alpha: 0.2)),
      ),
      child: body,
    );
  }

  /// One server's account state: nothing yet (create it, or link one the
  /// person already has; when the server already holds an account with
  /// their name, signing in with it comes first, since creating would only
  /// collide), turned off (ask the admin), or active (username, where to
  /// sign in, whether the server confirmed the account just now, and
  /// whether it is an administrator account Cantinarr never changes).
  Widget _buildAccountCard(MediaServerAccess server, String username) {
    final account = server.account;
    final Widget body;
    if (account == null && server.existingAccount) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n
                .mediaThereSAlreadyAnAccountNamedValue0OnValue1IfItSYoursSignInWithItsPasswordToLinkIt(
                    (username).toString(), (server.name).toString()),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 12),
          ElevatedButton.icon(
            onPressed: () => _linkOwnAccount(server, username),
            icon: const Icon(Icons.login, size: 18),
            label: Text(context.l10n.mediaLinkMyValue0Account(
                (mediaServerTypeLabel(server.serviceType)).toString())),
          ),
          const SizedBox(height: 8),
          Text(
            context.l10n.mediaNotYoursAskYourAdmin,
            style: TextStyle(color: AppTheme.textMuted, fontSize: 12),
          ),
        ],
      );
    } else if (account == null) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            context.l10n
                .mediaYourAdminHasOfferedAccessToValue0CreateOrLinkYourAccountToGetStarted(
                    (server.name).toString()),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 12),
          ElevatedButton.icon(
            onPressed: () => _createAccount(server, username),
            icon: const Icon(Icons.person_add_alt_1_outlined, size: 18),
            label: Text(context.l10n.mediaCreateMyValue0Account(
                (mediaServerTypeLabel(server.serviceType)).toString())),
          ),
          TextButton.icon(
            onPressed: () => _linkOwnAccount(server, username),
            icon: const Icon(Icons.login, size: 18),
            label: Text(context.l10n.mediaLinkMyValue0Account(
                (mediaServerTypeLabel(server.serviceType)).toString())),
          ),
        ],
      );
    } else if (account.disabled) {
      body = Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Padding(
            padding: EdgeInsets.only(top: 1),
            child: Icon(Icons.block, color: AppTheme.unavailable, size: 18),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              account.verified
                  ? context.l10n
                      .mediaYourAccessToValue0IsTurnedOffAskYourAdminIfYouThinkThatSAMistake(
                          (server.name).toString())
                  : context.l10n
                      .mediaYourLastKnownAccessToValue0WasTurnedOffRefreshToCheckAgainOrAskYourAdmin(
                          (server.name).toString()),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 14,
                height: 1.4,
              ),
            ),
          ),
        ],
      );
    } else {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(account.verified ? Icons.check_circle : Icons.info_outline,
                  color:
                      account.verified ? AppTheme.available : AppTheme.warning,
                  size: 18),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  server.name,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 10),
          Row(
            children: [
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      context.l10n.mediaUsername,
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    ),
                    Text(
                      account.username,
                      style: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ],
                ),
              ),
              IconButton(
                tooltip: context.l10n.mediaCopyUsername,
                icon: const Icon(Icons.copy, size: 18),
                onPressed: () =>
                    _copy(account.username, context.l10n.mediaUsernameCopied),
              ),
            ],
          ),
          if (account.administrator) ...[
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Padding(
                  padding: EdgeInsets.only(top: 1),
                  child: Icon(Icons.admin_panel_settings_outlined,
                      color: AppTheme.textSecondary, size: 16),
                ),
                SizedBox(width: 6),
                Expanded(
                  child: Text(
                    context
                        .l10n.mediaAdministratorAccountCantinarrNeverChangesIt,
                    style: TextStyle(
                      color: AppTheme.textSecondary,
                      fontSize: 12,
                      height: 1.4,
                    ),
                  ),
                ),
              ],
            ),
          ],
          const SizedBox(height: 8),
          if (server.publicAddress.isNotEmpty) ...[
            Text(
              context.l10n
                  .mediaSignInAtValue0((server.publicAddress).toString()),
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
            Wrap(
              spacing: 8,
              children: [
                TextButton.icon(
                  onPressed: () => _copy(
                      server.publicAddress, context.l10n.mediaAddressCopied),
                  icon: const Icon(Icons.copy, size: 16),
                  label: Text(context.l10n.mediaCopyAddress),
                ),
                TextButton.icon(
                  onPressed: () => _open(server),
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: Text(_openLabel(server)),
                ),
              ],
            ),
          ] else
            Text(
              context.l10n
                  .mediaYourAdminHasnTSharedTheSignInAddressYetAskThemWhereToSignIn,
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
          if (!account.verified) ...[
            const SizedBox(height: 8),
            Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Padding(
                  padding: EdgeInsets.only(top: 1),
                  child: Icon(Icons.info_outline,
                      color: AppTheme.warning, size: 16),
                ),
                SizedBox(width: 6),
                Expanded(
                  child: Text(
                    context.l10n
                        .mediaWeCouldnTConfirmYourServerAccessJustNowTryAgainOrCheckWithYourAdmin,
                    style: TextStyle(
                      color: AppTheme.warning,
                      fontSize: 12,
                      height: 1.4,
                    ),
                  ),
                ),
              ],
            ),
          ],
        ],
      );
    }
    return _accountCardWithManagement(server, body);
  }
}

/// Numbered section header: the accent number bubble plus the section title.
class _SectionHeader extends StatelessWidget {
  final int number;
  final String title;

  const _SectionHeader({required this.number, required this.title});

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        Container(
          width: 32,
          height: 32,
          decoration: BoxDecoration(
            color: AppTheme.accent.withValues(alpha: 0.15),
            shape: BoxShape.circle,
          ),
          child: Center(
            child: Text(
              '$number',
              style: const TextStyle(
                color: AppTheme.accent,
                fontWeight: FontWeight.bold,
              ),
            ),
          ),
        ),
        const SizedBox(width: 12),
        Expanded(
          child: Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 18,
              fontWeight: FontWeight.w600,
            ),
          ),
        ),
      ],
    );
  }
}

class _GuideSection extends StatelessWidget {
  final int number;
  final String title;
  final List<String> steps;
  final List<({String label, String url})> links;

  const _GuideSection({
    super.key,
    required this.number,
    required this.title,
    required this.steps,
    required this.links,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionHeader(number: number, title: title),
        const SizedBox(height: 12),
        ...steps.map((step) => Padding(
              padding: const EdgeInsets.only(left: 12, bottom: 8),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('• ',
                      style: TextStyle(color: AppTheme.textSecondary)),
                  Expanded(
                    child: Text(
                      step,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 14,
                        height: 1.4,
                      ),
                    ),
                  ),
                ],
              ),
            )),
        Wrap(
          spacing: 8,
          children: [
            for (final link in links)
              Link(
                uri: Uri.parse(link.url),
                target: LinkTarget.blank,
                builder: (context, followLink) => TextButton.icon(
                  onPressed: followLink,
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: Text(link.label),
                ),
              ),
          ],
        ),
      ],
    );
  }
}

class _TipCard extends StatelessWidget {
  final String title;
  final String message;

  const _TipCard({required this.title, required this.message});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.accent.withValues(alpha: 0.2)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.lightbulb_outline, color: AppTheme.accent, size: 20),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: const TextStyle(
                        color: AppTheme.accent, fontWeight: FontWeight.w600)),
                const SizedBox(height: 4),
                Text(message,
                    style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                        height: 1.4)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
