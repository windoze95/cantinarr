import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/media_access_service.dart';
import 'media_server_email_sheet.dart';
import 'media_server_password_sheet.dart';
import 'media_server_sign_in_sheet.dart';
import 'plex_sign_in_sheet.dart';

/// Requester-focused guide for the media servers (Plex, Jellyfin, Emby)
/// shared with this account: create the account with a password only they
/// know, or link one they already have by signing in with it; on Plex, sign
/// in with their own Plex account or share the email their invite goes to;
/// see where to sign in, install the app, start watching. Everything here is
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

class _MediaAccessGuideState extends ConsumerState<MediaAccessGuide> {
  List<MediaServerAccess>? _servers;
  bool _failed = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    try {
      final servers = await ref.read(mediaAccessServiceProvider).listMine();
      if (!mounted) return;
      setState(() {
        _servers = servers;
        _failed = false;
      });
    } catch (_) {
      if (!mounted) return;
      if (_servers == null) {
        setState(() => _failed = true);
        return;
      }
      // A refresh that failed keeps what was shown; say so rather than
      // swapping a working screen for an error.
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
        content: Text("Couldn't refresh your media servers."),
      ));
    }
  }

  void _retry() {
    setState(() => _failed = false);
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
          'Account created. Sign in with your new password.',
        MediaServerPasswordSheetOutcome.accountExists =>
          'You already have an account here.',
      }),
    ));
    await _load();
  }

  Future<void> _linkOwnAccount(MediaServerAccess server, String username) async {
    final outcome = await showMediaServerSignInSheet(
      context,
      server: server,
      username: username,
    );
    if (!mounted || outcome == null) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(
      content: Text(switch (outcome) {
        MediaServerSignInSheetOutcome.linked =>
          'Account linked. Sign in with your usual password.',
        MediaServerSignInSheetOutcome.accountExists =>
          'You already have an account here.',
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
      content: Text(switch (state.inviteState) {
        'sent' => 'Signed in as $who. Invite sent. Check your email.',
        'adopted' => 'Signed in as $who. Your access is set up.',
        'failed' => "Signed in as $who, but the invite couldn't be sent yet. "
            'It will be retried.',
        'claimed' => 'Signed in as $who, but that Plex account is already '
            'linked to another Cantinarr user here. Ask your admin.',
        _ => 'Signed in as $who. Your admin has been notified.',
      }),
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
          'Invite sent. Check your email, then accept it.',
        MediaServerEmailSheetOutcome.accountExists =>
          'You already have access here.',
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
              setDialogState(() => errorText = 'Enter a valid email address');
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
              ScaffoldMessenger.of(this.context).showSnackBar(const SnackBar(
                content: Text('Thanks! Your admin has been notified.'),
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
                errorText = "Couldn't send. Check your connection";
              });
            }
          }

          return AlertDialog(
            title: const Text('Your Plex email'),
            content: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Text(
                  'Enter the email of your Plex account. Your admin sends the '
                  'invite there.',
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
                    labelText: 'Email',
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
                child: const Text('Cancel'),
              ),
              ElevatedButton(
                onPressed: saving ? null : submit,
                child: saving
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Send'),
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

  Future<void> _open(String address) async {
    final uri = Uri.tryParse(address);
    if (uri == null) return;
    await launchUrl(uri, mode: LaunchMode.externalApplication);
  }

  @override
  Widget build(BuildContext context) {
    final auth = ref.watch(authProvider).valueOrNull;
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
        child: servers == null
            ? (_failed ? _buildLoadFailure() : _buildLoading())
            : RefreshIndicator(
                onRefresh: _load,
                child: ListView(
                  padding: const EdgeInsets.all(24),
                  children: servers.isEmpty && !askForPlex
                      ? [_buildEmpty(isAdmin: user?.isAdmin == true)]
                      : _buildGuide(
                          servers,
                          username: user?.username ?? '',
                          plexEmail: user?.plexEmail ?? '',
                          types: types,
                          askForPlex: askForPlex,
                        ),
                ),
              ),
      ),
    );
  }

  Widget _buildLoading() => const Center(child: CircularProgressIndicator());

  Widget _buildLoadFailure() {
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Text(
            "Couldn't load your media servers.",
            style: TextStyle(color: AppTheme.textSecondary),
          ),
          const SizedBox(height: 16),
          ElevatedButton(onPressed: _retry, child: const Text('Retry')),
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
                ? 'No media server is shared with your account yet. Open the '
                    'instance under Settings and add yourself under User '
                    'Access.'
                : 'No media server is shared with you yet. Ask your admin '
                    'for access.',
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
    final labels = mediaServerTypeLabels(types);
    final single = labels.length == 1;
    // "Plex", "Jellyfin", or "Plex or Jellyfin": the granted set decides.
    final names = mediaServerNamesPhrase(types);
    final where = single ? labels.single : 'your media server';
    final whereOpening = single ? labels.single : 'Your media server';
    final includesEmby = types.contains('emby');
    final includesPlex = types.contains('plex');
    final onlyPlex = single && includesPlex;
    final hasAccountServer = types.any((type) => type != 'plex');
    return [
      Text(
        onlyPlex
            ? 'Cantinarr is where you request. Plex is where you watch. '
                'Share the email of your Plex account once, accept the '
                'invite, then sign in on any device.'
            : includesPlex
                ? 'Cantinarr is where you request. $whereOpening is where '
                    'you watch. Set up your access once, then sign in on '
                    'any device.'
                : 'Cantinarr is where you request. $whereOpening is where '
                    'you watch. Create your account once, then sign in on '
                    'any device.',
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 14,
          height: 1.5,
        ),
      ),
      const SizedBox(height: 24),
      _SectionHeader(number: 1, title: onlyPlex ? 'Your invite' : 'Your account'),
      const SizedBox(height: 12),
      for (final server in servers)
        Padding(
          padding: const EdgeInsets.only(left: 44, bottom: 12),
          child: server.isInvite
              ? _buildInviteCard(server, plexEmail)
              : _buildAccountCard(server, username),
        ),
      if (askForPlex)
        Padding(
          padding: const EdgeInsets.only(left: 44, bottom: 12),
          child: _buildAskForPlexCard(plexEmail),
        ),
      const SizedBox(height: 12),
      _GuideSection(
        number: 2,
        title: 'Install the $names app',
        steps: [
          // Jellyfin's and Plex's apps are free; Emby's are free to install
          // but ask for an unlock or Premiere to play video on phones and
          // tablets, so "free" is said only when Emby is not in the set.
          if (!includesEmby)
            'Download the free $names app from the App Store or Google Play'
          else
            'Download the $names app from the App Store or Google Play',
          if (single)
            '$names is also on Apple TV, Android TV, Roku, Fire TV, and most '
                'smart TVs'
          else
            '${labels.length == 2 ? 'Both' : 'All of them'} are also on '
                'Apple TV, Android TV, Roku, Fire TV, and most smart TVs',
          if (includesEmby)
            'On a phone or tablet, Emby may ask for a one-time unlock or Emby '
                'Premiere before it plays video.',
          if (onlyPlex)
            'On a computer there is nothing to install: open app.plex.tv in '
                'your browser'
          else
            'On a computer there is nothing to install: open the sign-in '
                'address in your browser',
        ],
      ),
      const SizedBox(height: 24),
      _GuideSection(
        number: 3,
        title: includesPlex && !hasAccountServer
            ? 'Accept your invite and sign in'
            : 'Sign in',
        steps: [
          if (includesPlex) ...[
            'Your Plex invite arrives by email from Plex: open it and accept. '
                'Pending invites are also under the bell icon at app.plex.tv',
            'Sign in to the Plex app with the same Plex account, and the '
                'shared libraries appear',
          ],
          if (hasAccountServer) ...[
            'Open the app and enter the sign-in address from your account '
                'card above',
            'Sign in with your username and the password you chose when you '
                'created the account, or your usual password if you linked '
                'one you already had',
            'Forgot the password? Your admin can reset it on the server',
          ],
        ],
      ),
      const SizedBox(height: 24),
      _GuideSection(
        number: 4,
        title: 'Start watching',
        steps: [
          'Everything you request in Cantinarr shows up in $where once '
              'it is Available',
          if (hasAccountServer)
            "An available title's page in Cantinarr has a Watch on "
                '${mediaServerNamesPhrase({
                  for (final type in types)
                    if (type != 'plex') type,
                })} button that opens it on your server',
          'Missing something? Ask your admin',
        ],
      ),
      const SizedBox(height: 24),
      const _TipCard(
        title: 'Request here, watch there',
        message: 'When a request shows as Available in Cantinarr, it is '
            'ready to play on your server.',
      ),
    ];
  }

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
            'You have access to ${server.name}. Sign in with Plex to link '
            'your account, or share the email of your Plex account.',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 12),
          ElevatedButton.icon(
            onPressed: _signInWithPlex,
            icon: const Icon(Icons.login, size: 18),
            label: const Text('Sign in with Plex'),
          ),
          TextButton.icon(
            onPressed: () => _requestInvite(server, plexEmail),
            icon: const Icon(Icons.mail_outline, size: 18),
            label: const Text('Share my Plex email'),
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
                  'Invite sent to ${account.username}. Accept it from the '
                  'email Plex sent you, or under the bell icon at '
                  'app.plex.tv, and ${server.name} appears in Plex.',
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
            child: const Text('Wrong email?'),
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
              const Icon(Icons.check_circle,
                  color: AppTheme.available, size: 18),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  owner
                      ? 'You own ${server.name}.'
                      : '${server.name} is shared with ${account.username}',
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
              'Signed in as ${account.username}',
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
              'Sign in at ${server.publicAddress}',
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
                  onPressed: () =>
                      _copy(server.publicAddress, 'Address copied'),
                  icon: const Icon(Icons.copy, size: 16),
                  label: const Text('Copy address'),
                ),
                TextButton.icon(
                  onPressed: () => _open(server.publicAddress),
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: const Text('Open'),
                ),
              ],
            ),
          ],
          if (!account.verified) _buildUnconfirmed(),
        ],
      );
    }
    return _card(body);
  }

  /// A Plex server exists but this user holds no grant on it: signing in
  /// with Plex or sharing the email tells the admin where to send the invite
  /// (and, with auto-approve on, sends it at once).
  Widget _buildAskForPlexCard(String plexEmail) {
    final Widget body = plexEmail.isEmpty
        ? Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                'This server has Plex. Sign in with Plex to ask for access, '
                'or share the email of your Plex account; your admin gets a '
                'notification either way.',
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
                label: const Text('Sign in with Plex'),
              ),
              TextButton.icon(
                onPressed: () => _askForPlexAccess(plexEmail),
                icon: const Icon(Icons.mail_outline, size: 18),
                label: const Text('Share my Plex email'),
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
              const Text(
                'Your admin has been notified. Once they grant you Plex, '
                'the invite goes to this address.',
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
                    child: const Text('Sign in with Plex'),
                  ),
                  TextButton(
                    onPressed: () => _askForPlexAccess(plexEmail),
                    style: TextButton.styleFrom(
                      padding: EdgeInsets.zero,
                      minimumSize: const Size(0, 32),
                      tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                    ),
                    child: const Text('Change email'),
                  ),
                ],
              ),
            ],
          );
    return _card(body);
  }

  Widget _buildUnconfirmed() {
    return const Padding(
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
              "We couldn't confirm this with the server just now. It should "
              'still work.',
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
            "There's already an account named $username on ${server.name}. "
            "If it's yours, sign in with its password to link it.",
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
            label: const Text('Sign in to link it'),
          ),
          const SizedBox(height: 8),
          const Text(
            'Not yours? Ask your admin.',
            style: TextStyle(color: AppTheme.textMuted, fontSize: 12),
          ),
        ],
      );
    } else if (account == null) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            'You have access to ${server.name}. Create your account to '
            'start watching.',
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
            label: const Text('Create my account'),
          ),
          TextButton.icon(
            onPressed: () => _linkOwnAccount(server, username),
            icon: const Icon(Icons.login, size: 18),
            label: const Text('I already have an account'),
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
              'Your access to ${server.name} is turned off. Ask your admin '
              "if you think that's a mistake.",
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
              const Icon(Icons.check_circle,
                  color: AppTheme.available, size: 18),
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
                    const Text(
                      'Username',
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
                tooltip: 'Copy username',
                icon: const Icon(Icons.copy, size: 18),
                onPressed: () => _copy(account.username, 'Username copied'),
              ),
            ],
          ),
          if (account.administrator) ...[
            const Row(
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
                    'Administrator account. Cantinarr never changes it.',
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
              'Sign in at ${server.publicAddress}',
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
                  onPressed: () =>
                      _copy(server.publicAddress, 'Address copied'),
                  icon: const Icon(Icons.copy, size: 16),
                  label: const Text('Copy address'),
                ),
                TextButton.icon(
                  onPressed: () => _open(server.publicAddress),
                  icon: const Icon(Icons.open_in_new, size: 16),
                  label: const Text('Open'),
                ),
              ],
            ),
          ] else
            const Text(
              "Your admin hasn't shared the sign-in address yet. Ask them "
              'where to sign in.',
              style: TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
          if (!account.verified) ...[
            const SizedBox(height: 8),
            const Row(
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
                    "We couldn't confirm this account with the server just "
                    'now. Signing in should still work.',
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

  const _GuideSection({
    required this.number,
    required this.title,
    required this.steps,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        _SectionHeader(number: number, title: title),
        const SizedBox(height: 12),
        ...steps.map((step) => Padding(
              padding: const EdgeInsets.only(left: 44, bottom: 8),
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
