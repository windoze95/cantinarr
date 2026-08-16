import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/storage/preferences.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../../core/widgets/attention_menu_visibility_switch.dart';
import '../../../core/widgets/settings_highlight.dart';
import '../../ai_assistant/data/ai_settings_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/settings_search_index.dart';
import '../data/setup_status_service.dart';
import '../logic/app_version_provider.dart';
import '../logic/setup_status_provider.dart';
import '../logic/update_status_provider.dart';
import '../settings_anchors.dart';
import 'about_sheet.dart';

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
    final aiSettings = ref.watch(aiSettingsProvider).valueOrNull;
    final appVersion = ref.watch(appVersionProvider).valueOrNull;
    final gates = SettingsSearchGates(
      user: user,
      chaptarrEnabled: connection?.services.chaptarr ?? false,
      donateVisible: _donateVisible,
    );
    final searching = _query.trim().isNotEmpty;

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
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
                  hintText: 'Search all settings',
                  prefixIcon: const Icon(Icons.search),
                  suffixIcon: _searchController.text.isEmpty
                      ? null
                      : IconButton(
                          tooltip: 'Clear settings search',
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
            const _SectionHeader(title: 'Server'),
            // One live row: which server, its address, and whether the
            // session is connected — the dot carries the status.
            _SettingsTile(
              icon: Icons.dns_outlined,
              title: connection?.serverName ?? 'Cantinarr',
              subtitle: connection?.serverUrl ?? 'Not connected',
              trailing: Icon(
                Icons.circle,
                size: 12,
                color: auth?.isAuthenticated == true
                    ? AppTheme.available
                    : AppTheme.error,
              ),
            ),

            const SizedBox(height: 16),

            // Account
            const _SectionHeader(title: 'Account'),
            _SettingsTile(
              icon: Icons.person_outline,
              title: user?.username ?? 'Unknown',
              subtitle: user?.isAdmin == true ? 'Administrator' : 'User',
            ),
            if (user?.canUsePassword == true)
              _SettingsTile(
                icon: Icons.lock_outline,
                title: 'Password',
                subtitle: user?.hasPassword == null
                    ? 'Set a password for sign-in & MCP'
                    : (user!.hasPassword!
                        ? 'Change your sign-in password'
                        : 'Add a password for sign-in & MCP'),
                onTap: () => context.push('/settings/password'),
              ),
            if (user?.canUsePasskey == true)
              _SettingsTile(
                icon: Icons.fingerprint,
                title: 'Passkeys',
                subtitle: 'Manage passkey sign-in methods',
                onTap: () => context.push('/settings/passkeys'),
              ),
            if (user?.hasPermission('ai:chat') == true)
              _SettingsTile(
                icon: Icons.auto_awesome_outlined,
                title: 'AI Access',
                subtitle: _aiAccessSubtitle(aiSettings),
                onTap: () => context.push('/settings/ai'),
              ),

            const SizedBox(height: 16),

            // Modules (dynamic, instance-based)
            const _SectionHeader(title: 'Modules'),
            // Discover leads: it mirrors the drawer (above the libraries)
            // and stays anchored while the instance list below it changes.
            if (user?.isAdmin == true)
              _SettingsTile(
                icon: Icons.explore_outlined,
                title: 'Discover',
                subtitle: 'Row sources, language filter, TMDB and Trakt',
                onTap: () => context.push('/settings/discovery'),
              ),
            if (instances.isEmpty)
              const _SettingsTile(
                icon: Icons.info_outline,
                title: 'No instances configured',
                subtitle: 'Add a Radarr or Sonarr instance to get started',
              ),
            ...instances.map((inst) => _SettingsTile(
                  icon: _serviceIcon(inst.serviceType),
                  title: inst.name,
                  subtitle:
                      '${_serviceLabel(inst.serviceType)}${inst.isDefault ? ' (Default)' : ''}',
                  trailing: const Icon(
                    Icons.circle,
                    size: 12,
                    color: AppTheme.available,
                  ),
                  onTap: user?.isAdmin == true
                      ? () => context.push(
                            '/settings/instance/${inst.id}',
                            extra: {
                              'service_type': inst.serviceType,
                              'name': inst.name,
                              'is_default': inst.isDefault,
                            },
                          )
                      : null,
                )),
            if (user?.isAdmin != true)
              _SettingsTile(
                icon: Icons.flag_outlined,
                title: 'My reports',
                subtitle: 'Problems you reported and how they ended',
                onTap: () => context.push('/issues'),
              ),
            if (user?.isAdmin == true)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                child: OutlinedButton.icon(
                  onPressed: () async {
                    final result =
                        await context.push<bool>('/settings/instance/new');
                    if (result != true || !context.mounted) return;
                    ScaffoldMessenger.of(context).showSnackBar(
                      const SnackBar(content: Text('Instance added.')),
                    );
                  },
                  icon: const Icon(Icons.add),
                  label: const Text('Add Instance'),
                ),
              ),

            // Admin section
            if (user?.isAdmin == true) ...[
              const SizedBox(height: 16),
              const _SectionHeader(title: 'Admin'),
              // People and access first: invite, manage, their devices,
              // their Plex — then the AI/config stack.
              _SettingsTile(
                icon: Icons.link,
                title: 'Generate Connect Link',
                subtitle: 'Create a link to invite a new user',
                onTap: () => _showGenerateConnectLinkDialog(context),
              ),
              _SettingsTile(
                icon: Icons.people_outline,
                title: 'Users',
                subtitle: 'Manage accounts, roles, and invites',
                onTap: () => context.push('/settings/users'),
              ),
              _SettingsTile(
                icon: Icons.devices,
                title: 'Connected Devices',
                subtitle: 'Manage all connected devices',
                onTap: () => context.push('/settings/devices'),
              ),
              _SettingsTile(
                icon: Icons.play_circle_outline,
                title: 'Plex Invites',
                subtitle: 'Link Plex for one-tap and automatic invites',
                onTap: () => context.push('/settings/plex'),
              ),
              _SettingsTile(
                icon: Icons.key_outlined,
                title: 'Providers & Credentials',
                subtitle: 'Included AI providers and models',
                onTap: () => context.push('/settings/credentials'),
              ),
              _SettingsTile(
                icon: Icons.handyman_outlined,
                title: 'AI Tools',
                subtitle: 'Enable or disable assistant tools',
                onTap: () => context.push('/settings/ai-tools'),
              ),
              _SettingsTile(
                icon: Icons.auto_fix_high_outlined,
                title: 'AI Remediation',
                subtitle: 'Problem reporting and auto-fix assistant',
                onTap: () => context.push('/settings/ai-remediation'),
              ),
              _SettingsTile(
                icon: Icons.rule_outlined,
                title: 'Agent Auto-Approvals',
                subtitle: 'Standing rules that approve repeat fixes',
                onTap: () => context.push('/settings/agent-approval-rules'),
              ),
              _SettingsTile(
                icon: Icons.manage_history_outlined,
                title: 'Configuration History',
                subtitle: 'Review AI/MCP profile and custom-format changes',
                onTap: () => context.push('/settings/change-history'),
              ),
              _SettingsTile(
                icon: Icons.tune,
                title: 'Request Settings',
                subtitle: 'Approval, season, and quality defaults',
                onTap: () => context.push('/settings/request-settings'),
              ),
              _SettingsTile(
                icon: Icons.open_in_new,
                title: 'Update Portal',
                subtitle: (updateStatus?.managementUrl.isNotEmpty ?? false)
                    ? updateStatus!.managementUrl
                    : 'Link your container manager for the update banner',
                onTap: () => _showManagementUrlDialog(
                  context,
                  updateStatus?.managementUrl ?? '',
                ),
              ),
            ],

            if (user?.isAdmin == true) ...[
              const SizedBox(height: 16),
              const _SectionHeader(title: 'Needs attention menu'),
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

            // Notifications
            const _SectionHeader(title: 'Notifications'),
            _SettingsTile(
              icon: Icons.notifications_outlined,
              title: 'Notification Preferences',
              subtitle: 'Choose which push notifications you receive',
              onTap: () => context.push('/settings/notifications'),
            ),
            SettingsHighlight(
              anchorId: SettingsAnchors.rootRequestUpdates,
              highlightId: _activeHighlight,
              child: SwitchListTile(
                value: ref.watch(requestNotificationsEnabledProvider),
                onChanged: (v) =>
                    ref.read(requestNotificationsEnabledProvider.notifier).set(v),
                activeThumbColor: AppTheme.accent,
                secondary: const Icon(Icons.notifications_active_outlined,
                    color: AppTheme.textSecondary),
                title: const Text('Request updates',
                    style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w500)),
                subtitle: const Text(
                    'Show an in-app banner when a request is approved or denied',
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
              ),
            ),

            const SizedBox(height: 16),

            // Guides
            const _SectionHeader(title: 'Guides'),
            // The row opens the guide; its switch governs the menu entry.
            SettingsHighlight(
              anchorId: SettingsAnchors.rootShowPlexGuide,
              highlightId: _activeHighlight,
              child: ListTile(
                leading: const Icon(Icons.play_circle_outline,
                    color: AppTheme.textSecondary),
                title: const Text('Watch on Plex',
                    style: TextStyle(
                        color: AppTheme.textPrimary,
                        fontWeight: FontWeight.w500)),
                subtitle: const Text('Show the guide in the menu',
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
                trailing: Switch(
                  value: ref.watch(plexGuideEnabledProvider),
                  onChanged: (v) =>
                      ref.read(plexGuideEnabledProvider.notifier).set(v),
                  activeThumbColor: AppTheme.accent,
                ),
                onTap: () => context.push('/plex-guide'),
              ),
            ),

            const SizedBox(height: 16),

            // About
            const _SectionHeader(title: 'About'),
            _SettingsTile(
              icon: Icons.info_outline,
              title: 'Cantinarr',
              subtitle: appVersion?.label ?? '',
              onTap: () => showAppSheet(
                context,
                builder: (_) => const AboutSheet(),
              ),
            ),
            _SettingsTile(
              icon: Icons.code,
              title: 'GitHub',
              subtitle: 'Source code, issues, and releases',
              onTap: () => launchUrl(
                Uri.parse(_githubUrl),
                mode: LaunchMode.externalApplication,
              ),
            ),
            _SettingsTile(
              icon: Icons.how_to_vote_outlined,
              title: 'Request a feature',
              subtitle: 'Vote on the roadmap — no account needed',
              onTap: () => launchUrl(
                Uri.parse(_roadmapUrl),
                mode: LaunchMode.externalApplication,
              ),
            ),
            if (_donateVisible)
              _SettingsTile(
                icon: Icons.favorite_outline,
                title: 'Donate',
                subtitle: 'Support Cantinarr on GitHub Sponsors',
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
    final results = searchSettingsIndex(_query, gates);
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
                'No settings match "${_query.trim()}"',
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
                textAlign: TextAlign.center,
              ),
              const SizedBox(height: 6),
              const Text(
                'Try another word. Results only include settings your '
                'account can see.',
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
          queryParameters: entry.anchorId == null
              ? null
              : {'highlight': entry.anchorId},
        ).toString(),
      );
      return;
    }
    // Action tiles on the root screen run their own handler; results with an
    // anchor dismiss search and reveal their row in place.
    switch (entry.id) {
      case 'root.connect-link':
        _showGenerateConnectLinkDialog(context);
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
        launchUrl(Uri.parse(_githubUrl),
            mode: LaunchMode.externalApplication);
        return;
      case 'root.roadmap':
        launchUrl(Uri.parse(_roadmapUrl),
            mode: LaunchMode.externalApplication);
        return;
      case 'root.donate':
        launchUrl(Uri.parse(_donateUrl),
            mode: LaunchMode.externalApplication);
        return;
    }
    _searchController.clear();
    setState(() {
      _query = '';
      _pendingRootHighlight = entry.anchorId;
    });
  }

  void _showGenerateConnectLinkDialog(BuildContext context) {
    final nameController = TextEditingController();
    String? generatedLink;
    bool isGenerating = false;

    showDialog(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('Generate Connect Link'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (generatedLink == null) ...[
                TextField(
                  controller: nameController,
                  decoration: const InputDecoration(
                    labelText: 'Name',
                    hintText: 'e.g. Mom, Dad, Roommate',
                    prefixIcon: Icon(Icons.person_outline),
                  ),
                  textCapitalization: TextCapitalization.words,
                  textInputAction: TextInputAction.done,
                ),
              ] else ...[
                const Text(
                  'Share this link:',
                  style: TextStyle(color: AppTheme.textSecondary),
                ),
                const SizedBox(height: 12),
                Container(
                  padding: const EdgeInsets.all(12),
                  decoration: BoxDecoration(
                    color: AppTheme.accent.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(8),
                  ),
                  child: SelectableText(
                    generatedLink!,
                    style: const TextStyle(fontSize: 12),
                  ),
                ),
              ],
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(),
              child: Text(generatedLink != null ? 'Done' : 'Cancel'),
            ),
            if (generatedLink == null)
              ElevatedButton(
                onPressed: isGenerating
                    ? null
                    : () async {
                        final name = nameController.text.trim();
                        if (name.isEmpty) return;
                        setDialogState(() => isGenerating = true);
                        try {
                          final resp = await ref
                              .read(authProvider.notifier)
                              .generateConnectToken(name);
                          setDialogState(() {
                            generatedLink = resp.link;
                            isGenerating = false;
                          });
                        } catch (e) {
                          setDialogState(() => isGenerating = false);
                          if (dialogContext.mounted) {
                            ScaffoldMessenger.of(dialogContext).showSnackBar(
                              SnackBar(
                                  content: Text('Failed to generate link: $e')),
                            );
                          }
                        }
                      },
                child: isGenerating
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Generate'),
              ),
            if (generatedLink != null)
              ElevatedButton.icon(
                onPressed: () {
                  Clipboard.setData(ClipboardData(text: generatedLink!));
                  ScaffoldMessenger.of(dialogContext).showSnackBar(
                    const SnackBar(content: Text('Link copied!')),
                  );
                },
                icon: const Icon(Icons.copy, size: 18),
                label: const Text('Copy'),
              ),
          ],
        ),
      ),
    );
  }

  void _showManagementUrlDialog(BuildContext context, String current) {
    final controller = TextEditingController(text: current);
    bool saving = false;

    showDialog(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) => AlertDialog(
          title: const Text('Update Portal'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Text(
                'Optional. When set, the "update available" banner links here so '
                'you can apply the update in your own container manager (e.g. an '
                'Unraid Docker page or Portainer). The link opens on your '
                'devices, so use an address they can reach — a cluster-internal '
                'name only the server resolves won\'t work from a phone. Leave '
                'blank to clear.',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: controller,
                decoration: const InputDecoration(
                  labelText: 'Portal URL',
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
              onPressed: () => Navigator.of(dialogContext).pop(),
              child: const Text('Cancel'),
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
                            SnackBar(content: Text('Failed to save: $e')),
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
                  : const Text('Save'),
            ),
          ],
        ),
      ),
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
    case 'sabnzbd':
    case 'qbittorrent':
    case 'nzbget':
    case 'transmission':
      return Icons.download_outlined;
    case 'tautulli':
      return Icons.monitor_heart_outlined;
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
    case 'sabnzbd':
      return 'SABnzbd';
    case 'qbittorrent':
      return 'qBittorrent';
    case 'nzbget':
      return 'NZBGet';
    case 'transmission':
      return 'Transmission';
    case 'tautulli':
      return 'Tautulli';
    default:
      return serviceType;
  }
}

String _aiAccessSubtitle(AiSettings? settings) {
  if (settings == null) return 'Choose personal or included AI';
  final effective = settings.effective;
  final provider = effective.provider.isEmpty
      ? ''
      : settings.providerLabel(effective.provider);
  if (effective.source == AiAccessSource.personal) {
    return effective.available
        ? 'Personal · $provider'
        : 'Personal AI needs attention';
  }
  if (effective.source == AiAccessSource.shared) {
    return effective.available
        ? 'Included · $provider'
        : 'Included AI unavailable';
  }
  if (settings.shared.granted) {
    return 'Included access needs server setup';
  }
  return 'Add a personal provider';
}

const _githubUrl = 'https://github.com/windoze95/cantinarr';
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

/// The Setup Checklist tile. The count carries the state, because this tile is
/// the only trace of the checklist once an admin mutes the drawer reminder:
/// amber while anything is unconfigured, red when the server is missing
/// something it cannot work without, green once everything is done. Colouring
/// the number in place rather than hanging another badge off the row keeps a
/// screen of near-identical tiles readable.
Widget _setupChecklistTile(BuildContext context, SetupStatus? status) {
  void open() => context.push('/setup');
  if (status == null) {
    return _SettingsTile(
      icon: Icons.checklist_outlined,
      title: 'Setup Checklist',
      subtitle: 'See which features are configured',
      onTap: open,
    );
  }
  final tail = ' of ${status.total} features configured';
  final countColor = status.missingCoreCapability
      ? AppTheme.danger
      : status.remaining > 0
          ? AppTheme.warning
          : AppTheme.available;
  return _SettingsTile(
    icon: Icons.checklist_outlined,
    title: 'Setup Checklist',
    subtitle: '${status.configured}$tail',
    subtitleSpans: [
      TextSpan(
        text: '${status.configured}',
        style: TextStyle(color: countColor, fontWeight: FontWeight.w700),
      ),
      TextSpan(text: tail),
    ],
    onTap: open,
  );
}

class _SettingsTile extends StatelessWidget {
  final IconData icon;
  final String title;
  final String subtitle;

  /// Rendered instead of [subtitle] when the copy needs more than one colour.
  /// [subtitle] stays the plain-text equivalent of the same sentence.
  final List<InlineSpan>? subtitleSpans;
  final VoidCallback? onTap;
  final Widget? trailing;

  const _SettingsTile({
    required this.icon,
    required this.title,
    required this.subtitle,
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
          subtitle: subtitleSpans == null
              ? Text(
                  subtitle,
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
