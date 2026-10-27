import 'settings_labels.dart';
import 'package:cantinarr/core/storage/locale_preferences.dart';
import 'package:cantinarr/l10n/l10n.dart';
import '../../request/logic/request_quota_provider.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/models/user_profile.dart';
import '../../../core/network/api_error_message.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../../core/widgets/attention_menu_visibility_switch.dart';
import '../../../core/widgets/phone_apps_sheet.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../ai_assistant/data/ai_settings_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../../media_access/data/video_apps.dart';
import '../data/outbound_proxy_service.dart';
import '../data/settings_search_index.dart';
import '../data/setup_status_service.dart';
import '../logic/app_version_provider.dart';
import '../logic/external_address_provider.dart';
import '../logic/outbound_proxy_provider.dart';
import '../logic/setup_status_provider.dart';
import '../logic/update_status_provider.dart';
import '../settings_anchors.dart';
import 'about_sheet.dart';
import 'app_icon_sheet.dart';
import '../data/app_icon_service.dart';

/// Simplified settings screen for backend-connected architecture.
class SettingsScreen extends ConsumerStatefulWidget {
  /// Settings-search anchor to scroll to and flash on arrival.
  final String? highlightId;

  const SettingsScreen({super.key, this.highlightId});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  final _searchController = TextEditingController();
  String _query = '';

  /// Anchor of a root row picked from search results: search dismisses and
  /// the browse list reveals the row in place (scroll + flash).
  String? _pendingRootHighlight;

  /// The root highlight currently in effect — an in-place reveal from search
  /// wins over the route's deep-link param.
  String? get _activeHighlight => _pendingRootHighlight ?? widget.highlightId;

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  @override
  void initState() {
    super.initState();
    // Learn whether the account has a password so the Account tile reflects
    // it, and re-derive the setup checklist so its tile subtitle is current.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(authProvider.notifier).refreshUser();
      ref.read(setupStatusProvider.notifier).refresh();
      ref.read(updateStatusProvider.notifier).refresh();
      ref.read(externalAddressProvider.notifier).refresh();
      ref.read(outboundProxyProvider.notifier).refresh();
    });
  }

  @override
  Widget build(BuildContext context) {
    final authState = ref.watch(authProvider);
    final auth = authState.valueOrNull;
    final connection = auth?.connection;
    final user = auth?.user;
    final instances = connection?.instances ?? [];
    final setupStatus = ref.watch(setupStatusProvider);
    final updateStatus = ref.watch(updateStatusProvider);
    final externalAddress = ref.watch(externalAddressProvider);
    final outboundProxy = ref.watch(outboundProxyProvider);
    final aiSettings = ref.watch(aiSettingsProvider).valueOrNull;
    final appVersion = ref.watch(appVersionProvider).valueOrNull;
    final mediaServersVisible = connection?.mediaAccessGuideVisible ?? false;
    final audiobookshelfVisible = connection?.mediaServerInstances
            .any((instance) => instance.serviceType == 'audiobookshelf') ??
        false;
    final videoServersVisible = connection?.mediaServerInstances.any(
            (instance) =>
                VideoApps.serviceTypes.contains(instance.serviceType)) ??
        false;
    final gates = SettingsSearchGates(
      user: user,
      chaptarrEnabled: connection?.services.chaptarr ?? false,
      lidarrEnabled: connection?.services.lidarr ?? false,
      donateVisible: _donateVisible,
      phoneAppsVisible: phoneAppsVisible,
      appIconsVisible: appIconsVisible,
      mediaServersVisible: mediaServersVisible,
      audiobookshelfVisible: audiobookshelfVisible,
      videoServersVisible: videoServersVisible,
      appleTvRemote: connection?.appleTvRemote ?? false,
      requestQuotas: connection?.requestQuotas ?? false,
    );
    final searching = _query.trim().isNotEmpty;

    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.settingsTitle)),
      body: CenteredContent(
          child: ListView(
        // Build every child while a settings-search highlight needs to find
        // its anchor (see SettingsHighlight).
        cacheExtent: SettingsHighlight.cacheExtentFor(_activeHighlight),
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          // The one tile with live signal leads the screen: the checklist
          // count is amber/red/green state, not decoration.
          if (user?.isAdmin == true) _setupChecklistTile(context, setupStatus),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
            child: ListenableBuilder(
              listenable: _searchController,
              builder: (context, _) => TextField(
                controller: _searchController,
                onChanged: (v) => setState(() => _query = v),
                autocorrect: false,
                textInputAction: TextInputAction.search,
                decoration: InputDecoration(
                  hintText: context.l10n.settingsSearchAll,
                  prefixIcon: const Icon(Icons.search),
                  suffixIcon: _searchController.text.isEmpty
                      ? null
                      : IconButton(
                          tooltip: context.l10n.settingsClearSearch,
                          icon: const Icon(Icons.close_rounded),
                          onPressed: () {
                            _searchController.clear();
                            setState(() => _query = '');
                          },
                        ),
                ),
              ),
            ),
          ),
          if (searching)
            ..._searchResults(context, gates)
          else ...[
            // Server connection
            _SectionHeader(title: context.l10n.settingsServer),
            // One live row: which server, its address, and whether the
            // session is connected — the dot carries the status.
            _SettingsTile(
              icon: Icons.dns_outlined,
              title: connection?.serverName ?? 'Cantinarr',
              subtitle:
                  connection?.serverUrl ?? context.l10n.settingsNotConnected,
              trailing: Icon(
                Icons.circle,
                size: 12,
                color: auth?.isAuthenticated == true
                    ? AppTheme.available
                    : AppTheme.error,
              ),
            ),
            // Every role gets this — it is the only way off a server the
            // user no longer wants (there is no other path back to the
            // connect screen while a session exists).
            _SettingsTile(
              icon: Icons.logout,
              title: context.l10n.settingsSignOut,
              subtitle: context.l10n.settingsDisconnectDevice,
              onTap: () => _confirmSignOut(context),
            ),

            const SizedBox(height: 16),

            SettingsHighlight(
              anchorId: 'root.language',
              highlightId: _activeHighlight,
              child: _SettingsTile(
                icon: Icons.language,
                title: context.l10n.settingsLanguage,
                subtitle: switch (ref.watch(appLocaleProvider)?.languageCode) {
                  'en' => 'English',
                  'fr' => 'Français',
                  _ => context.l10n.settingsSystemLanguage,
                },
                onTap: () => _showLanguageDialog(context),
              ),
            ),

            // Account
            _SectionHeader(title: context.l10n.settingsAccount),
            _SettingsTile(
              icon: Icons.person_outline,
              title: user?.username ?? context.l10n.settingsUnknown,
              subtitle: _accountSubtitle(context, user),
            ),
            _SettingsTile(
              icon: Icons.link,
              title: context.l10n.settingsLinkedSignIn,
              subtitle: context.l10n.settingsLinkedSignInDescription,
              onTap: () => context.push('/settings/sso-account'),
            ),
            if (appIconsVisible)
              _SettingsTile(
                icon: Icons.app_registration_outlined,
                title: context.l10n.settingsAppIcon,
                subtitle: context.l10n.settingsAppIconSubtitle,
                onTap: () => showAppSheet(context, builder: (_) => const AppIconSheet()),
              ),
            if (user?.canUsePassword == true)
              _SettingsTile(
                icon: Icons.lock_outline,
                title: context.l10n.settingsPassword,
                subtitle: user?.hasPassword == null
                    ? context.l10n.settingsSetPassword
                    : (user!.hasPassword!
                        ? context.l10n.settingsChangePassword
                        : context.l10n.settingsAddPassword),
                onTap: () => context.push('/settings/password'),
              ),
            if (user?.canUsePasskey == true)
              _SettingsTile(
                icon: Icons.fingerprint,
                title: context.l10n.settingsPasskeys,
                subtitle: context.l10n.settingsManagePasskeys,
                onTap: () => context.push('/settings/passkeys'),
              ),
            if (user?.hasPermission('ai:chat') == true)
              _SettingsTile(
                icon: Icons.auto_awesome_outlined,
                title: context.l10n.settingsAiAccess,
                subtitle: _aiAccessSubtitle(context, aiSettings),
                onTap: () => context.push('/settings/ai'),
              ),
            if (videoServersVisible)
              _SettingsTile(
                icon: Icons.video_library_outlined,
                title: context.l10n.settingsVideoApps,
                subtitle: context.l10n.settingsVideoAppsDescription,
                onTap: () => context.push('/settings/video-apps'),
              ),
            if (audiobookshelfVisible)
              _SettingsTile(
                icon: Icons.headphones_outlined,
                title: context.l10n.settingsListeningApps,
                subtitle: context.l10n.settingsListeningAppsDescription,
                onTap: () => context.push('/settings/listening-apps'),
              ),
            if (ref.watch(requestQuotasSupportedProvider))
              _SettingsTile(
                icon: Icons.data_usage,
                title: context.l10n.settingsRequestAllowance,
                onTap: () => context.push('/settings/request-allowance'),
              ),

            const SizedBox(height: 16),

            // Modules (dynamic, instance-based)
            _SectionHeader(title: context.l10n.settingsModules),
            // Discover leads: it mirrors the drawer (above the libraries)
            // and stays anchored while the instance list below it changes.
            if (user?.isAdmin == true)
              _SettingsTile(
                icon: Icons.explore_outlined,
                title: context.l10n.settingsDiscover,
                subtitle: context.l10n.settingsDiscoverDescription,
                onTap: () => context.push('/settings/discovery'),
              ),
            if (instances.isEmpty)
              _SettingsTile(
                icon: Icons.info_outline,
                title: context.l10n.settingsNoInstances,
                subtitle: context.l10n.settingsConnectService,
              ),
            ...instances.map((inst) => _SettingsTile(
                  icon: _serviceIcon(inst.serviceType),
                  title: inst.name,
                  subtitle:
                      '${_serviceLabel(inst.serviceType)}${inst.isDefault ? context.l10n.settingsDefault2 : ''}',
                  trailing: const Icon(
                    Icons.circle,
                    size: 12,
                    color: AppTheme.available,
                  ),
                  // Admins edit the instance. The one instance a requester
                  // can open is a media server, whose tile leads to the
                  // access guide (their account and where to sign in).
                  onTap: user?.isAdmin == true
                      ? () => context.push(
                            '/settings/instance/${inst.id}',
                            extra: {
                              'service_type': inst.serviceType,
                              'name': inst.name,
                              'is_default': inst.isDefault,
                            },
                          )
                      : mediaServerServiceTypes.contains(inst.serviceType)
                          ? () => context.push('/media-servers')
                          : null,
                )),
            if (user?.isAdmin != true)
              _SettingsTile(
                icon: Icons.flag_outlined,
                title: context.l10n.settingsMyReports,
                subtitle: context.l10n.settingsMyReportsDescription,
                onTap: () => context.push('/issues'),
              ),
            if (user?.isAdmin == true)
              Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                child: OutlinedButton.icon(
                  onPressed: () async {
                    final result =
                        await context.push<bool>('/settings/instance/new');
                    if (result != true || !context.mounted) return;
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(
                          content: Text(context.l10n.settingsInstanceAdded)),
                    );
                  },
                  icon: const Icon(Icons.add),
                  label: Text(context.l10n.settingsAddInstance),
                ),
              ),

            // Admin section
            if (user?.isAdmin == true) ...[
              const SizedBox(height: 16),
              _SectionHeader(title: context.l10n.settingsAdmin),
              // People and access first: users (inviting lives there now),
              // the address their invite links are built from, their
              // devices, then the AI/config stack.
              _SettingsTile(
                icon: Icons.people_outline,
                title: context.l10n.settingsUsers,
                subtitle: context.l10n.settingsManageUsers,
                onTap: () => context.push('/settings/users'),
              ),
              _SettingsTile(
                  icon: Icons.play_arrow,
                  title: context.l10n.settingsPlexSignIn,
                  subtitle: context.l10n.settingsPlexSignInDescription,
                  onTap: () => context.push('/settings/plex-auth')),
              _SettingsTile(
                icon: Icons.login,
                title: context.l10n.settingsSingleSignOn,
                subtitle: context.l10n.settingsSingleSignOnDescription,
                onTap: () => context.push('/settings/oidc'),
              ),
              _SettingsTile(
                icon: Icons.public,
                title: context.l10n.settingsExternalAddress,
                subtitle: (externalAddress?.isNotEmpty ?? false)
                    ? externalAddress!
                    : context.l10n.settingsInviteAddress,
                onTap: () =>
                    _showExternalAddressDialog(context, externalAddress ?? ''),
              ),
              _SettingsTile(
                icon: Icons.vpn_lock_outlined,
                title: context.l10n.settingsOutboundProxy,
                subtitle: (outboundProxy?.url.isNotEmpty ?? false)
                    ? outboundProxy!.url
                    : context.l10n.settingsOutboundProxyDescription,
                onTap: () => _showOutboundProxyDialog(
                  context,
                  outboundProxy ?? OutboundProxySettings.empty,
                ),
              ),
              _SettingsTile(
                icon: Icons.hub_outlined,
                title: context.l10n.settingsSeerrApi,
                subtitle: context.l10n.settingsSeerrApiDescription,
                onTap: () => context.push('/settings/seerr-api'),
              ),
              _SettingsTile(
                icon: Icons.devices,
                title: context.l10n.settingsConnectedDevices,
                subtitle: context.l10n.settingsManageDevices,
                onTap: () => context.push('/settings/devices'),
              ),
              if (connection?.appleTvRemote == true)
                _SettingsTile(
                  icon: Icons.tv,
                  title: context.l10n.settingsAppleTVs,
                  subtitle: context.l10n.settingsAppleTVsDescription,
                  onTap: () => context.push('/settings/apple-tvs'),
                ),
              _SettingsTile(
                icon: Icons.key_outlined,
                title: context.l10n.settingsProvidersCredentials,
                subtitle: context.l10n.settingsIncludedProviders,
                onTap: () => context.push('/settings/credentials'),
              ),
              _SettingsTile(
                icon: Icons.handyman_outlined,
                title: context.l10n.settingsAiTools,
                subtitle: context.l10n.settingsAiToolsDescription,
                onTap: () => context.push('/settings/ai-tools'),
              ),
              _SettingsTile(
                icon: Icons.auto_fix_high_outlined,
                title: context.l10n.settingsAiRemediation,
                subtitle: context.l10n.settingsAiRemediationDescription,
                onTap: () => context.push('/settings/ai-remediation'),
              ),
              _SettingsTile(
                icon: Icons.rule_outlined,
                title: context.l10n.settingsAgentApprovals,
                subtitle: context.l10n.settingsAgentApprovalsDescription,
                onTap: () => context.push('/settings/agent-approval-rules'),
              ),
              _SettingsTile(
                icon: Icons.manage_history_outlined,
                title: context.l10n.settingsConfigurationHistory,
                subtitle: context.l10n.settingsConfigurationHistoryDescription,
                onTap: () => context.push('/settings/change-history'),
              ),
              _SettingsTile(
                icon: Icons.tune,
                title: context.l10n.settingsRequestSettings,
                subtitle: context.l10n.settingsRequestSettingsDescription,
                onTap: () => context.push('/settings/request-settings'),
              ),
              _SettingsTile(
                icon: Icons.open_in_new,
                title: context.l10n.settingsUpdatePortal,
                subtitle: (updateStatus?.managementUrl.isNotEmpty ?? false)
                    ? updateStatus!.managementUrl
                    : context.l10n.settingsUpdatePortalDescription,
                onTap: () => _showManagementUrlDialog(
                  context,
                  updateStatus?.managementUrl ?? '',
                ),
              ),
            ],

            const SizedBox(height: 16),

            // Notifications
            SettingsHighlight(
              anchorId: SettingsAnchors.rootNotifications,
              highlightId: _activeHighlight,
              child: _SectionHeader(title: context.l10n.settingsNotifications),
            ),
            _SettingsTile(
              icon: Icons.notifications_outlined,
              title: context.l10n.settingsPushNotifications,
              subtitle: context.l10n.settingsPushNotificationsDescription,
              onTap: () => context.push('/settings/push-notifications'),
            ),
            _SettingsTile(
              icon: Icons.notifications_active_outlined,
              title: context.l10n.settingsDiscordNotifications,
              subtitle: context.l10n.settingsDiscordNotificationsDescription,
              onTap: () => context.push('/settings/discord-notifications'),
            ),

            if (user?.isAdmin == true) ...[
              const SizedBox(height: 16),
              _SectionHeader(title: context.l10n.settingsAttentionMenu),
              SettingsHighlight(
                anchorId: SettingsAnchors.rootAttentionApprovals,
                highlightId: _activeHighlight,
                child: const AttentionMenuVisibilitySwitch(
                  item: AttentionMenuItem.approvals,
                  opensQueue: true,
                ),
              ),
              SettingsHighlight(
                anchorId: SettingsAnchors.rootAttentionIssues,
                highlightId: _activeHighlight,
                child: const AttentionMenuVisibilitySwitch(
                  item: AttentionMenuItem.issues,
                  opensQueue: true,
                ),
              ),
              SettingsHighlight(
                anchorId: SettingsAnchors.rootAttentionAgentFixes,
                highlightId: _activeHighlight,
                child: const AttentionMenuVisibilitySwitch(
                  item: AttentionMenuItem.agentFixes,
                  opensQueue: true,
                ),
              ),
              SettingsHighlight(
                anchorId: SettingsAnchors.rootAttentionProfileApprovals,
                highlightId: _activeHighlight,
                child: const AttentionMenuVisibilitySwitch(
                  item: AttentionMenuItem.profileApprovals,
                  opensQueue: true,
                ),
              ),
            ],

            const SizedBox(height: 16),

            // Guides. Only while a media server is shared with this account
            // (or a Plex server can be asked for): the guide's content comes
            // from the server, so without one there is nothing to open.
            if (mediaServersVisible) ...[
              _SectionHeader(title: context.l10n.settingsGuides),
              _SettingsTile(
                icon: Icons.live_tv_outlined,
                title: context.l10n.settingsMediaServerAccess,
                subtitle: context.l10n.settingsMediaServerAccessDescription,
                onTap: () => context.push('/media-servers'),
              ),
            ],

            const SizedBox(height: 16),

            // About
            _SectionHeader(title: context.l10n.settingsAbout),
            _SettingsTile(
              icon: Icons.info_outline,
              title: 'Cantinarr',
              subtitle: appVersion?.label ?? '',
              onTap: () => showAppSheet(
                context,
                builder: (_) => const AboutSheet(),
              ),
            ),
            if (phoneAppsVisible)
              _SettingsTile(
                icon: Icons.smartphone,
                title: context.l10n.settingsPhoneApp,
                subtitle: context.l10n.settingsPhoneAppDescription,
                onTap: () => showAppSheet(
                  context,
                  builder: (_) => const PhoneAppsSheet(),
                ),
              ),
            _SettingsTile(
              icon: Icons.code,
              title: 'GitHub',
              subtitle: context.l10n.settingsSourceDescription,
              onTap: () => launchUrl(
                Uri.parse(_githubUrl),
                mode: LaunchMode.externalApplication,
              ),
            ),
            _SettingsTile(
              icon: Icons.forum_outlined,
              title: 'Discord',
              subtitle: context.l10n.settingsDiscordDescription,
              onTap: () => launchUrl(
                Uri.parse(_discordUrl),
                mode: LaunchMode.externalApplication,
              ),
            ),
            _SettingsTile(
              icon: Icons.how_to_vote_outlined,
              title: context.l10n.settingsRequestFeature,
              subtitle: context.l10n.settingsRoadmapDescription,
              onTap: () => launchUrl(
                Uri.parse(_roadmapUrl),
                mode: LaunchMode.externalApplication,
              ),
            ),
            if (_donateVisible)
              _SettingsTile(
                icon: Icons.favorite_outline,
                title: context.l10n.settingsDonate,
                subtitle: context.l10n.settingsDonateDescription,
                onTap: () => launchUrl(
                  Uri.parse(_donateUrl),
                  mode: LaunchMode.externalApplication,
                ),
              ),

            const SizedBox(height: 32),
          ],
        ],
      )),
    );
  }

  /// The replace-the-list search results: matched entries as tiles whose
  /// subtitle says where each setting lives, or an empty state that owns up
  /// to gating.
  List<Widget> _searchResults(BuildContext context, SettingsSearchGates gates) {
    final results =
        searchSettingsIndex(_query, gates, localizations: context.l10n);
    if (results.isEmpty) {
      return [
        Padding(
          padding: const EdgeInsets.fromLTRB(24, 48, 24, 24),
          child: Column(
            children: [
              const Icon(Icons.search_off_rounded,
                  size: 40, color: AppTheme.textMuted),
              const SizedBox(height: 12),
              Text(
                context.l10n.settingsMessageNoSettingsMatchValue1(
                    (_query.trim()).toString()),
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 6),
              Text(
                context.l10n.settingsSearchHelp,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                textAlign: TextAlign.center,
              ),
            ],
          ),
        ),
      ];
    }
    return [
      for (final entry in results)
        _SettingsTile(
          icon: entry.icon,
          title: entry.title,
          subtitle: entry.breadcrumb,
          onTap: () => _openSearchResult(context, entry),
        ),
      const SizedBox(height: 32),
    ];
  }

  void _openSearchResult(BuildContext context, SettingsSearchEntry entry) {
    FocusScope.of(context).unfocus();
    if (entry.route != '/settings') {
      // The query stays behind the push, so back returns to these results.
      context.push(
        Uri(
          path: entry.route,
          queryParameters:
              entry.anchorId == null ? null : {'highlight': entry.anchorId},
        ).toString(),
      );
      return;
    }
    // Action tiles on the root screen run their own handler; results with an
    // anchor dismiss search and reveal their row in place.
    switch (entry.id) {
      case 'root.app-icon':
        showAppSheet(context, builder: (_) => const AppIconSheet());
        return;
      case 'root.language':
        _showLanguageDialog(context);
        return;
      case 'root.sign-out':
        _confirmSignOut(context);
        return;
      case 'root.external-address':
        _showExternalAddressDialog(
          context,
          ref.read(externalAddressProvider) ?? '',
        );
        return;
      case 'root.outbound-proxy':
        _showOutboundProxyDialog(
          context,
          ref.read(outboundProxyProvider) ?? OutboundProxySettings.empty,
        );
        return;
      case 'root.update-portal':
        _showManagementUrlDialog(
          context,
          ref.read(updateStatusProvider)?.managementUrl ?? '',
        );
        return;
      case 'root.about':
        showAppSheet(context, builder: (_) => const AboutSheet());
        return;
      case 'root.github':
        launchUrl(Uri.parse(_githubUrl), mode: LaunchMode.externalApplication);
        return;
      case 'root.discord':
        launchUrl(Uri.parse(_discordUrl), mode: LaunchMode.externalApplication);
        return;
      case 'root.roadmap':
        launchUrl(Uri.parse(_roadmapUrl), mode: LaunchMode.externalApplication);
        return;
      case 'root.donate':
        launchUrl(Uri.parse(_donateUrl), mode: LaunchMode.externalApplication);
        return;
    }
    _searchController.clear();
    setState(() {
      _query = '';
      _pendingRootHighlight = entry.anchorId;
    });
  }

  Future<void> _showLanguageDialog(BuildContext context) async {
    final selected = ref.read(appLocaleProvider)?.languageCode ?? 'system';
    final choice = await showDialog<String>(
      context: context,
      builder: (dialogContext) => SimpleDialog(
        title: Text(dialogContext.l10n.settingsLanguage),
        children: [
          for (final option in [
            ('system', dialogContext.l10n.settingsSystemLanguage),
            ('en', 'English'),
            ('fr', 'Français'),
          ])
            SimpleDialogOption(
              onPressed: () => Navigator.pop(dialogContext, option.$1),
              child: Row(children: [
                Expanded(child: Text(option.$2)),
                if (selected == option.$1)
                  const Icon(Icons.check, color: AppTheme.accent),
              ]),
            ),
          Padding(
            padding: const EdgeInsets.fromLTRB(24, 12, 24, 8),
            child: Text(dialogContext.l10n.settingsDeviceLanguageHelp,
                style: const TextStyle(color: AppTheme.textSecondary)),
          ),
        ],
      ),
    );
    if (choice == null || !mounted) return;
    await ref.read(appLocaleProvider.notifier).setLocale(
          choice == 'system' ? null : Locale(choice),
        );
  }

  Future<void> _confirmSignOut(BuildContext context) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n.settingsSignOutTitle),
        content: Text(
          context.l10n.settingsSignOutWarning,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(context.l10n.settingsCancel),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.error,
            ),
            onPressed: () => Navigator.pop(context, true),
            child: Text(context.l10n.settingsSignOut),
          ),
        ],
      ),
    );

    if (confirmed != true) return;
    // No navigation here: clearing the session flips the router's
    // refreshListenable and the redirect lands on the connect screen.
    await ref.read(authProvider.notifier).logout();
  }

  void _showExternalAddressDialog(BuildContext context, String current) {
    final controller = TextEditingController(text: current);
    bool saving = false;

    showDialog(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => UnsavedChangesGuard(
          isDialog: true,
          hasChanges: () => controller.text != current,
          isSaving: saving,
          child: AlertDialog(
            title: Text(context.l10n.settingsExternalAddress),
            content: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  context.l10n.settingsExternalAddressHelp,
                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: controller,
                  decoration: InputDecoration(
                    labelText: context.l10n.settingsExternalAddressLabel,
                    hintText: 'https://cantinarr.example.com',
                    prefixIcon: Icon(Icons.public),
                  ),
                  keyboardType: TextInputType.url,
                  autocorrect: false,
                  textInputAction: TextInputAction.done,
                ),
              ],
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(dialogContext).maybePop(),
                child: Text(context.l10n.settingsCancel),
              ),
              ElevatedButton(
                onPressed: saving
                    ? null
                    : () async {
                        setDialogState(() => saving = true);
                        try {
                          await ref
                              .read(externalAddressProvider.notifier)
                              .set(controller.text.trim());
                          if (dialogContext.mounted) {
                            Navigator.of(dialogContext).pop();
                          }
                        } catch (e) {
                          setDialogState(() => saving = false);
                          if (dialogContext.mounted) {
                            ScaffoldMessenger.of(dialogContext).showSnackBar(
                              SnackBar(
                                  content: Text(context.l10n
                                      .settingsMessageFailedToSaveValue1(
                                          (e).toString()))),
                            );
                          }
                        }
                      },
                child: saving
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : Text(context.l10n.settingsSave),
              ),
            ],
          ),
        ),
      ),
    );
  }

  void _showOutboundProxyDialog(
    BuildContext context,
    OutboundProxySettings current,
  ) {
    showDialog(
      context: context,
      builder: (_) => _OutboundProxyDialog(current: current),
    );
  }

  void _showManagementUrlDialog(BuildContext context, String current) {
    final controller = TextEditingController(text: current);
    bool saving = false;

    showDialog(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => UnsavedChangesGuard(
          isDialog: true,
          hasChanges: () => controller.text != current,
          isSaving: saving,
          child: AlertDialog(
            title: Text(context.l10n.settingsUpdatePortal),
            content: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  context.l10n.settingsUpdatePortalHelp,
                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: controller,
                  decoration: InputDecoration(
                    labelText: context.l10n.settingsPortalUrl,
                    hintText: 'http://tower.local/Docker',
                    prefixIcon: Icon(Icons.open_in_new),
                  ),
                  keyboardType: TextInputType.url,
                  autocorrect: false,
                  textInputAction: TextInputAction.done,
                ),
              ],
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(dialogContext).maybePop(),
                child: Text(context.l10n.settingsCancel),
              ),
              ElevatedButton(
                onPressed: saving
                    ? null
                    : () async {
                        setDialogState(() => saving = true);
                        try {
                          await ref
                              .read(updateStatusProvider.notifier)
                              .setManagementUrl(controller.text.trim());
                          if (dialogContext.mounted) {
                            Navigator.of(dialogContext).pop();
                          }
                        } catch (e) {
                          setDialogState(() => saving = false);
                          if (dialogContext.mounted) {
                            ScaffoldMessenger.of(dialogContext).showSnackBar(
                              SnackBar(
                                  content: Text(context.l10n
                                      .settingsMessageFailedToSaveValue1(
                                          (e).toString()))),
                            );
                          }
                        }
                      },
                child: saving
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : Text(context.l10n.settingsSave),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// The Outbound Proxy editor. A widget of its own rather than the sibling
/// dialogs' StatefulBuilder so its three controllers are disposed with the
/// dialog, after the close transition, and so a test result has state to
/// live in.
class _OutboundProxyDialog extends ConsumerStatefulWidget {
  final OutboundProxySettings current;

  const _OutboundProxyDialog({required this.current});

  @override
  ConsumerState<_OutboundProxyDialog> createState() =>
      _OutboundProxyDialogState();
}

class _OutboundProxyDialogState extends ConsumerState<_OutboundProxyDialog> {
  late final _addressController =
      TextEditingController(text: widget.current.url);
  late final _usernameController =
      TextEditingController(text: widget.current.username);

  /// Always starts empty: the server never returns the stored password.
  final _passwordController = TextEditingController();
  bool _saving = false;
  bool _testing = false;

  /// The last test's verdict line; null until a test ran, and null again the
  /// moment any field changes, so a stale pass is never mistaken for a fresh
  /// one.
  String? _testResult;
  bool _testSucceeded = false;

  @override
  void dispose() {
    _addressController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  String get _url => _addressController.text.trim();
  String get _username => _usernameController.text.trim();

  /// On the fields' onChanged rather than the controllers: a controller also
  /// notifies for focus and selection moves, which must not retire a result.
  void _onEdited(String _) => setState(() => _testResult = null);

  Future<void> _test() async {
    setState(() {
      _testing = true;
      _testResult = null;
    });
    try {
      await ref.read(outboundProxyServiceProvider).test(
            url: _url,
            username: _username,
            password: _passwordController.text,
          );
      if (!mounted) return;
      setState(() {
        _testing = false;
        _testSucceeded = true;
        _testResult = context.l10n.settingsProxyWorks;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _testing = false;
        _testSucceeded = false;
        _testResult = apiErrorMessage(e);
      });
    }
  }

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      await ref.read(outboundProxyProvider.notifier).set(
            url: _url,
            username: _username,
            password: _passwordController.text,
          );
      if (mounted) Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(context.l10n.settingsMessageFailedToSaveValue1(
                (apiErrorMessage(e)).toString()))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final busy = _saving || _testing;
    return UnsavedChangesGuard(
      isDialog: true,
      hasChanges: () =>
          _addressController.text != widget.current.url ||
          _usernameController.text != widget.current.username ||
          _passwordController.text.isNotEmpty,
      isSaving: _saving,
      child: _buildDialog(context, busy),
    );
  }

  Widget _buildDialog(BuildContext context, bool busy) {
    return AlertDialog(
      title: Text(context.l10n.settingsOutboundProxy),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              context.l10n.settingsProxyHelp,
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _addressController,
              decoration: InputDecoration(
                labelText: context.l10n.settingsProxyAddress,
                hintText: 'http://proxy:8118',
                prefixIcon: Icon(Icons.vpn_lock_outlined),
              ),
              keyboardType: TextInputType.url,
              autocorrect: false,
              textInputAction: TextInputAction.next,
              onChanged: _onEdited,
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _usernameController,
              decoration: InputDecoration(
                labelText: context.l10n.settingsOptionalUsername,
              ),
              autocorrect: false,
              textInputAction: TextInputAction.next,
              onChanged: _onEdited,
            ),
            const SizedBox(height: 12),
            TextField(
              controller: _passwordController,
              decoration: InputDecoration(
                labelText: context.l10n.settingsPassword,
                helperText: widget.current.hasPassword
                    ? context.l10n.settingsSavedPassword
                    : null,
              ),
              obscureText: true,
              autocorrect: false,
              enableSuggestions: false,
              textInputAction: TextInputAction.done,
              onChanged: _onEdited,
            ),
            if (_testing || _testResult != null) ...[
              const SizedBox(height: 12),
              if (_testing)
                const Center(
                  child: SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: AppTheme.accent),
                  ),
                )
              else
                Text(
                  _testResult!,
                  style: TextStyle(
                    color: _testSucceeded ? AppTheme.available : AppTheme.error,
                    fontSize: 13,
                  ),
                  textAlign: TextAlign.center,
                ),
            ],
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).maybePop(),
          child: Text(context.l10n.settingsCancel),
        ),
        // Nothing to test without an address; the server would only say so.
        TextButton(
          onPressed: busy || _url.isEmpty ? null : _test,
          child: Text(context.l10n.settingsTest),
        ),
        ElevatedButton(
          onPressed: busy ? null : _save,
          child: _saving
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(context.l10n.settingsSave),
        ),
      ],
    );
  }
}

IconData _serviceIcon(String serviceType) {
  switch (serviceType) {
    case 'radarr':
      return Icons.movie_outlined;
    case 'sonarr':
      return Icons.tv_outlined;
    case 'chaptarr':
      return Icons.menu_book;
    case 'lidarr':
      return Icons.library_music_outlined;
    case 'sabnzbd':
    case 'qbittorrent':
    case 'nzbget':
    case 'transmission':
    case 'deluge':
    case 'rutorrent':
      return Icons.download_outlined;
    case 'tautulli':
    case 'tracearr':
      return Icons.monitor_heart_outlined;
    case 'tdarr':
      return Icons.sync;
    case 'audiobookshelf':
      return Icons.headphones_outlined;
    case 'jellyfin':
    case 'emby':
      return Icons.live_tv_outlined;
    default:
      return Icons.dns_outlined;
  }
}

String _serviceLabel(String serviceType) {
  switch (serviceType) {
    case 'radarr':
      return 'Radarr';
    case 'sonarr':
      return 'Sonarr';
    case 'chaptarr':
      return 'Chaptarr';
    case 'lidarr':
      return 'Lidarr';
    case 'sabnzbd':
      return 'SABnzbd';
    case 'qbittorrent':
      return 'qBittorrent';
    case 'nzbget':
      return 'NZBGet';
    case 'transmission':
      return 'Transmission';
    case 'deluge':
      return 'Deluge';
    case 'rutorrent':
      return 'ruTorrent';
    case 'tautulli':
      return 'Tautulli';
    case 'tracearr':
      return 'Tracearr';
    case 'tdarr':
      return 'Tdarr';
    case 'audiobookshelf':
      return 'Audiobookshelf';
    case 'jellyfin':
      return 'Jellyfin';
    case 'emby':
      return 'Emby';
    default:
      return serviceType;
  }
}

/// The role line under the username. A kids account says so, with its
/// limits when the profile carries them, in the account's own words.
String _accountSubtitle(BuildContext context, UserProfile? user) {
  if (user == null) return context.l10n.settingsUser;
  if (user.isAdmin) return context.l10n.settingsAdministrator;
  if (user.child) {
    final limits = user.contentLimits;
    if (limits == null) return context.l10n.settingsKidsAccount;
    return context.l10n.settingsMessageKidsAccountMoviesUpToValue1ShowsUp(
        (limits.maxMovieRating).toString(), (limits.maxTvRating).toString());
  }
  return context.l10n.settingsUser;
}

String _aiAccessSubtitle(BuildContext context, AiSettings? settings) {
  if (settings == null) return context.l10n.settingsChooseAi;
  final effective = settings.effective;
  final provider = effective.provider.isEmpty
      ? ''
      : settings.providerLabel(effective.provider);
  if (effective.source == AiAccessSource.personal) {
    return effective.available
        ? context.l10n.settingsMessagePersonalValue1((provider).toString())
        : context.l10n.settingsPersonalAiAttention;
  }
  if (effective.source == AiAccessSource.shared) {
    return effective.available
        ? context.l10n.settingsMessageIncludedValue1((provider).toString())
        : context.l10n.settingsIncludedAiUnavailable;
  }
  if (settings.shared.granted) {
    return context.l10n.settingsIncludedAiSetup;
  }
  return context.l10n.settingsAddPersonalProvider;
}

const _githubUrl = 'https://github.com/windoze95/cantinarr';
const _discordUrl = 'https://discord.gg/zAgRwGwmVB';
const _roadmapUrl = 'https://cantinarr.com/roadmap/';
const _donateUrl = 'https://github.com/sponsors/windoze95';

/// Apple and Google both treat links to external payment for the developer as
/// grounds for store rejection, so the Donate tile ships only on the web
/// bundle (the self-hosted surface) and desktop builds — never in the
/// iOS/Android store binaries. The GitHub tile is fine everywhere.
bool get _donateVisible =>
    kIsWeb ||
    (defaultTargetPlatform != TargetPlatform.iOS &&
        defaultTargetPlatform != TargetPlatform.android);

class _SectionHeader extends StatelessWidget {
  final String title;
  const _SectionHeader({required this.title});

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(20, 14, 20, 8),
      child: Row(
        children: [
          Container(
            width: 18,
            height: 2,
            decoration: BoxDecoration(
              color: AppTheme.accent,
              borderRadius: BorderRadius.circular(99),
            ),
          ),
          const SizedBox(width: 9),
          Text(
            title.toUpperCase(),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 11,
              fontWeight: FontWeight.w800,
              letterSpacing: 1.25,
            ),
          ),
        ],
      ),
    );
  }
}

/// The checklist remains available here after its menu reminder is cleared.
Widget _setupChecklistTile(BuildContext context, SetupStatus? status) {
  void open() => context.push('/setup');
  if (status == null) {
    return _SettingsTile(
      icon: Icons.checklist_outlined,
      title: context.l10n.settingsSetupChecklist,
      subtitle: context.l10n.settingsConfiguredFeatures,
      onTap: open,
    );
  }
  final tail = context.l10n.settingsMessageOfValue1FeaturesConfigured(
      (status.effectiveTotal).toString());
  if (status.isComplete) {
    return _SettingsTile(
      icon: Icons.checklist_outlined,
      title: context.l10n.settingsSetupChecklist,
      subtitle: settingsSetupSummary(context, status),
      subtitleSpans: [
        TextSpan(
            text: settingsSetupSummary(context, status),
            style: const TextStyle(color: AppTheme.available)),
      ],
      onTap: open,
    );
  }
  return _SettingsTile(
    icon: Icons.checklist_outlined,
    title: context.l10n.settingsSetupChecklist,
    subtitle: '${status.configured}$tail',
    subtitleSpans: [
      TextSpan(
        text: '${status.configured}',
        style: const TextStyle(
            color: AppTheme.accent, fontWeight: FontWeight.w700),
      ),
      TextSpan(text: tail),
    ],
    onTap: open,
  );
}

class _SettingsTile extends StatelessWidget {
  final IconData icon;
  final String title;
  final String? subtitle;

  /// Rendered instead of [subtitle] when the copy needs more than one colour.
  /// [subtitle] stays the plain-text equivalent of the same sentence.
  final List<InlineSpan>? subtitleSpans;
  final VoidCallback? onTap;
  final Widget? trailing;

  const _SettingsTile({
    required this.icon,
    required this.title,
    this.subtitle,
    this.subtitleSpans,
    this.onTap,
    this.trailing,
  });

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 3),
      child: Material(
        color: AppTheme.surfaceVariant.withValues(alpha: 0.72),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          side: const BorderSide(color: AppTheme.border),
        ),
        clipBehavior: Clip.antiAlias,
        child: ListTile(
          leading: Container(
            width: 38,
            height: 38,
            decoration: BoxDecoration(
              color: AppTheme.surfaceRaised,
              borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
              border: Border.all(color: AppTheme.border),
            ),
            child: Icon(icon, color: AppTheme.textSecondary, size: 20),
          ),
          title: Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontWeight: FontWeight.w600,
            ),
          ),
          subtitle: subtitle == null && subtitleSpans == null
              ? null
              : subtitleSpans == null
                  ? Text(
                      subtitle!,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                      ),
                    )
                  : Text.rich(
                      TextSpan(
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 13,
                        ),
                        children: subtitleSpans,
                      ),
                    ),
          trailing: trailing ??
              (onTap != null
                  ? const Icon(
                      Icons.arrow_forward_ios_rounded,
                      size: 15,
                      color: AppTheme.textMuted,
                    )
                  : null),
          onTap: onTap,
        ),
      ),
    );
  }
}
