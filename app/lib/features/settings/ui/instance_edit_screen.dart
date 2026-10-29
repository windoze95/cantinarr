import '../../media_availability/ui/availability_scan_section.dart';
import '../../media_access/ui/media_access_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'dart:async';

import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/api_error_message.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/unsaved_changes_guard.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../../media_access/data/media_access_service.dart';
import '../../media_access/data/listening_apps.dart';
import '../../media_access/data/video_apps.dart';
import '../../media_access/ui/video_app_field.dart';
import '../../media_access/ui/listening_app_fields.dart';
import '../../discover/data/trending_books_service.dart';
import '../data/instance_api_service.dart';
import '../data/audiobook_library_access.dart';
import 'audiobook_user_libraries.dart';
import '../data/hardcover_connection.dart';
import 'hardcover_connection_dialog.dart';
import '../logic/arr_path_match.dart';
import '../logic/plex_invites_provider.dart';
import 'instance_users_screen.dart';

/// Form for creating or editing a service instance.
/// qBittorrent's two credential shapes: the WebUI sign-in, which every
/// version has, or an API key, which 5.2 and newer issue under
/// Options > WebUI.
enum _QbitAuth { password, apiKey }

class InstanceEditScreen extends ConsumerStatefulWidget {
  final String? instanceId;
  final String? initialServiceType;
  final String? initialName;
  final String? initialUrl;
  final String? initialApiKey;
  final String? initialUsername;
  final bool initialIsDefault;

  /// A catalog setup link refreshes config after this pushed route closes,
  /// so a router refresh cannot replace the match while it is being popped.
  final bool refreshConfigAfterReturn;

  /// Opens a NEW instance form with the service-type selector unchosen,
  /// showing this prompt as its disabled placeholder until one is picked.
  /// For the setup checklist's download-client row: it names a category of
  /// four services, and preselecting any one of them would be a guess the
  /// admin then has to correct — the same correction the Radarr default used
  /// to force on every row. Ignored when editing.
  final String? serviceTypePrompt;

  const InstanceEditScreen({
    super.key,
    this.instanceId,
    this.initialServiceType,
    this.initialName,
    this.initialUrl,
    this.initialApiKey,
    this.initialUsername,
    this.initialIsDefault = false,
    this.refreshConfigAfterReturn = false,
    this.serviceTypePrompt,
  });

  bool get isEditing => instanceId != null;

  @override
  ConsumerState<InstanceEditScreen> createState() => _InstanceEditScreenState();
}

class _InstanceEditScreenState extends ConsumerState<InstanceEditScreen> {
  bool get _isAbs => _serviceType == 'audiobookshelf';
  Map<int, AudiobookLibraryPolicy> _absPolicies = {};
  final Set<int> _absEditedPolicies = {};
  List<String> _absSavedDefaultIds = [];
  String _absSavedPublicAddress = '';
  ListeningApps? _absSavedListeningApps;
  bool _absAccessLoaded = false;
  bool _absSyncPending = false;
  final _detailsDraft = SettingsDraft();
  final _mediaDraft = SettingsDraft();
  final _mappingsDraft = SettingsDraft();
  final _defaultDraft = SettingsDraft();
  int? _savedPlexPinId;
  bool _tdarrHasKey = false;
  bool _clearTdarrKey = false;

  Map<String, Object?> get _detailValues => {
        'type': _serviceType,
        'name': _nameController.text,
        'url': _urlController.text,
        'apiKey': _apiKeyController.text,
        'username': _usernameController.text,
        'password': _passwordController.text,
        'qbitAuth': _qbitAuth.name,
        'clearTdarrKey': _isTdarr && _clearTdarrKey,
        'tagRequests': _tagRequests,
        'autoAddUsers': _autoAddUsers,
      };
  Object get _mediaValues => [
        _publicAddressController.text,
        _selectedLibraryIds.toList()..sort(),
        _plexMachineId,
        _plexAutoApprove,
        _listeningApps?.toJson(),
        _videoApps?.toJson(),
        _availabilityScan?.toJson(),
      ];
  Object get _mappingValues => [
        for (final mapping in _mediaPathMappings)
          [mapping.arrPath.text, mapping.cantinarrPath.text],
      ];
  bool get _hasUnsavedChanges => (_absEditedPolicies.isNotEmpty ||
      _detailsDraft.hasChanges(_detailValues) ||
      _defaultDraft.hasChanges(_isDefault) ||
      _mediaDraft.hasChanges(_mediaValues) ||
      _mappingsDraft.hasChanges(_mappingValues) ||
      !_sameSelection(_assignedUserIds, _savedAssignedUserIds) ||
      (_plexPinId != _savedPlexPinId && _plexAccount.isNotEmpty));

  void _markInstanceSaved() {
    _detailsDraft.markSaved(_detailValues);
    _defaultDraft.markSaved(_isDefault);
    _mediaDraft.markSaved(_mediaValues);
    _mappingsDraft.markSaved(_mappingValues);
    _savedPlexPinId = _plexPinId;
  }

  void _finishEditing() {
    _markInstanceSaved();
    _savedAssignedUserIds = Set.of(_assignedUserIds);
    if (context.canPop()) {
      context.pop(true);
    } else {
      context.go('/settings');
    }
  }

  late final TextEditingController _nameController;
  late final TextEditingController _urlController;
  late final TextEditingController _apiKeyController;
  late final TextEditingController _usernameController;
  late final TextEditingController _passwordController;
  late final TextEditingController _publicAddressController;
  ListeningApps? _listeningApps;
  VideoApps? _videoApps;
  String _serviceType = 'radarr';
  bool _isDefault = false;
  bool _autoAddUsers = true;
  bool _assignmentDetailsLoaded = false;
  bool get _assignmentModel =>
      ref.read(authProvider).valueOrNull?.connection?.instanceAssignments ??
      false;
  bool get _newAssignments => _assignmentModel && _supportsMediaDownloads;
  bool _tagRequests = false;
  bool _tagRequestsLoaded = false;
  bool get _supportsRequesterTags =>
      const ['radarr', 'sonarr', 'chaptarr', 'lidarr'].contains(_serviceType) &&
      (ref.read(authProvider).valueOrNull?.connection?.requesterTagging ??
          false);
  bool _isSaving = false;
  bool _creationComplete = false;
  bool _isTesting = false;
  String? _testResult;
  bool _testSucceeded = false;
  bool _isConfiguringWebhook = false;
  String? _webhookResult;
  Color _webhookResultColor = AppTheme.textSecondary;

  // Hardcover section state (Chaptarr, editing only). The token is
  // write-only: the server reports connected-or-not and never the token, so
  // the field is always empty on open and cleared again after a save.
  late final TextEditingController _hardcoverController;
  bool _hardcoverSupported = false;
  bool _hardcoverConnected = false;
  bool _hardcoverOAuthAvailable = false;
  bool _hardcoverUseToken = false;
  bool _hardcoverReconnect = false;
  String _hardcoverMethod = 'none';
  bool _isSavingHardcover = false;
  String? _hardcoverResult;
  Color _hardcoverResultColor = AppTheme.textSecondary;

  // Completed-media path mappings belong to this exact arr instance. The
  // deployment roots remain server-owned; this form only routes arr paths into
  // those already-authorized read-only folders.
  final List<_MediaPathMappingFields> _mediaPathMappings = [];
  List<String>? _mediaRoots;
  String? _mediaRootsError;
  String? _mediaMappingsError;
  bool _mediaMappingsLoaded = false;
  bool _mediaMappingApiSupported = false;
  bool _mediaMappingsDirty = false;

  // Library folders the saved arr instance reports live (edit mode only) —
  // the only prefixes a mapping's arr path can ever match. Suggestions and
  // mismatch warnings derive strictly from a successful read, so a proxy
  // hiccup never paints working mappings with alarms.
  List<String> _arrRootFolders = const [];
  bool _arrRootFoldersKnown = false;
  String? _arrRootFoldersError;
  String? _arrRootFoldersFetchedFor;

  /// Fresh instance list from the server — the login-time copy in the auth
  /// state can be stale, and both the first-of-type auto-default and the
  /// default-takeover confirmation depend on what actually exists right now.
  List<ServiceInstance> _instances = const [];
  bool _instancesLoaded = false;

  // User-access section state: all accounts, their current per-user pin for
  // this service type (user id → instance id, possibly a sibling instance),
  // the working selection, and the selection as last saved. A user counts as
  // having access here when either a pin or a grant row names this instance.
  List<UserSummary>? _users;
  Map<int, MediaServerAccountRow> _mediaAccounts = {};
  bool _mediaAccountsFailed = false;
  bool _mediaAccountsLoaded = false;
  bool get _supportsAccountManagement =>
      ref.read(authProvider).valueOrNull?.connection?.mediaAccountManagement ??
      false;
  Map<int, String> _pins = const {};
  Set<int> _assignedUserIds = <int>{};
  Set<int> _savedAssignedUserIds = <int>{};
  String? _userSelectError;

  // Media-server (Jellyfin, Emby) section state: the libraries the server reports
  // right now (null until read; the admin adds and removes libraries on the
  // server itself, so the list is only ever a live read, never stored), the
  // ids shared with this server's accounts, and whether the section was
  // touched. On edit, an untouched section sends nothing so the stored copy
  // survives.
  List<MediaServerLibrary>? _mediaServerLibraries;
  bool _mediaServerLibrariesLoading = false;
  String? _mediaServerLibrariesError;
  Set<String> _selectedLibraryIds = <String>{};
  bool _mediaServerConfigDirty = false;
  AvailabilityScanConfig? _availabilityScan;
  bool _availabilityScanDirty = false;

  // Plex section state: the PIN link that yields the instance's token (held
  // server-side and referenced by pin id on save), the linked account's
  // name, its owned servers for the picker, the chosen server, and the
  // auto-approve switch. When editing, the stored token counts as linked.
  bool _plexLinking = false;
  int? _plexPinId;
  String? _plexLinkUrl;
  Timer? _plexPollTimer;
  String _plexAccount = '';
  bool _plexLinkedStored = false;
  List<PlexServerChoice>? _plexServers;
  bool _plexServersLoading = false;
  String? _plexServersError;
  String _plexMachineId = '';
  bool _plexAutoApprove = false;

  /// Which credential shape a qBittorrent form is on, and the shape the
  /// stored instance is on when editing, so a switch knows it must carry
  /// the new shape's fields instead of "leave blank to keep existing".
  _QbitAuth _qbitAuth = _QbitAuth.password;
  _QbitAuth _storedQbitAuth = _QbitAuth.password;

  static const _serviceTypes = <(String, String)>[
    ('radarr', 'Radarr'),
    ('sonarr', 'Sonarr'),
    ('chaptarr', 'Chaptarr'),
    ('lidarr', 'Lidarr'),
    ('sabnzbd', 'SABnzbd'),
    ('qbittorrent', 'qBittorrent'),
    ('nzbget', 'NZBGet'),
    ('transmission', 'Transmission'),
    ('deluge', 'Deluge'),
    ('rutorrent', 'ruTorrent'),
    ('tautulli', 'Tautulli'),
    ('tracearr', 'Tracearr'),
    ('tdarr', 'Tdarr'),
    ('jellyfin', 'Jellyfin'),
    ('emby', 'Emby'),
    ('audiobookshelf', 'Audiobookshelf'),
    ('plex', 'Plex'),
  ];

  /// Types that authenticate with username/password instead of an API key.
  /// qBittorrent can do either, chosen by the toggle above its fields;
  /// Deluge rides the same payload shape with an empty username.
  bool get _usesUserPass =>
      _serviceType == 'nzbget' ||
      _serviceType == 'transmission' ||
      _serviceType == 'deluge' ||
      _serviceType == 'rutorrent' ||
      (_serviceType == 'qbittorrent' && _qbitAuth == _QbitAuth.password);

  /// Editing a qBittorrent instance onto its other credential shape: the
  /// stored credential is about to be dropped, so the new one is required
  /// and "leave blank to keep existing" no longer applies.
  bool get _qbitSwitchingShape =>
      widget.isEditing &&
      _serviceType == 'qbittorrent' &&
      _qbitAuth != _storedQbitAuth;

  /// Transmission and ruTorrent auth is optional: Transmission only when
  /// the daemon requires it, ruTorrent only when its web server sits
  /// behind HTTP Basic authentication.
  bool get _credentialsOptional =>
      _serviceType == 'transmission' || _serviceType == 'rutorrent';

  /// Deluge's web UI has a password and no username, so the form shows one
  /// field.
  bool get _passwordOnly => _serviceType == 'deluge';

  bool get _isDownloadClient =>
      _serviceType == 'sabnzbd' ||
      _serviceType == 'qbittorrent' ||
      _serviceType == 'nzbget' ||
      _serviceType == 'transmission' ||
      _serviceType == 'deluge' ||
      _serviceType == 'rutorrent';

  bool get _supportsWebhook =>
      _serviceType == 'radarr' ||
      _serviceType == 'sonarr' ||
      _isChaptarr ||
      _isLidarr;

  bool get _isChaptarr => _serviceType == 'chaptarr';

  bool get _isLidarr => _serviceType == 'lidarr';
  bool get _isTdarr => _serviceType == 'tdarr';

  /// Media servers (Jellyfin, Emby, Plex): users sign in there to watch, so
  /// the form carries a sign-in address and a shared-library choice instead
  /// of media downloads or instant updates.
  bool get _isMediaServer => mediaServerServiceTypes.contains(_serviceType);

  /// Watch-history providers (Tautulli, Tracearr): admin-only monitoring
  /// with a global default, the plain name + URL + API key form.
  bool get _isWatchHistory => watchHistoryServiceTypes.contains(_serviceType);

  /// Plex has no URL or API key to type: the credential is a plex.tv account
  /// linked with a PIN, and the server to share is picked from the ones that
  /// account owns.
  bool get _isPlex => _serviceType == 'plex';

  /// Whether the Plex form holds a usable credential: an approved link, or
  /// (when editing) the token already stored.
  bool get _plexHasCredential =>
      (_plexPinId != null && _plexAccount.isNotEmpty) || _plexLinkedStored;

  /// Types with no global default: their instances reach users only through
  /// access grants, so the default toggle is hidden and never sent.
  bool get _grantOnly =>
      _isMediaServer || (!_assignmentModel && (_isChaptarr || _isLidarr));

  bool get _supportsMediaDownloads =>
      _serviceType == 'radarr' ||
      _serviceType == 'sonarr' ||
      _isChaptarr ||
      _isLidarr;

  bool get _shouldSubmitMediaPathMappings =>
      _supportsMediaDownloads &&
      _mediaMappingApiSupported &&
      _mediaMappingsLoaded &&
      (!widget.isEditing || _mediaMappingsDirty);

  /// Source types feed requests and dashboard statuses, so they support
  /// per-user assignment, and a media server's grant is what lets a user
  /// create an account there; download clients, Tautulli and Tracearr are
  /// global-only.
  bool get _supportsUserAssignment =>
      _serviceType == 'radarr' ||
      _serviceType == 'sonarr' ||
      _isChaptarr ||
      _isLidarr ||
      _isMediaServer;

  /// Chaptarr and media servers have no global default — their instances are
  /// only ever assigned directly to users — so they always show the
  /// user-select. The other source types show it when this instance is NOT
  /// the global default, as a per-user override of that default.
  bool get _showUserSelect =>
      _supportsUserAssignment &&
      !_newAssignments &&
      (_grantOnly || !_isDefault);

  /// The type selector is still on its disabled placeholder (see
  /// [InstanceEditScreen.serviceTypePrompt]): the form asked for a choice
  /// and nothing has been picked yet, so no type-dependent affordance may
  /// render, and nothing may be tested or saved.
  bool get _serviceTypeUnchosen => !widget.isEditing && _serviceType.isEmpty;

  String get _serviceLabel {
    for (final t in _serviceTypes) {
      if (t.$1 == _serviceType) return t.$2;
    }
    return _serviceType;
  }

  late final AuthNotifier _configAuth;

  @override
  void initState() {
    super.initState();
    _configAuth = ref.read(authProvider.notifier)..deferConfigRefresh();
    _nameController = TextEditingController(text: widget.initialName ?? '');
    _urlController = TextEditingController(text: widget.initialUrl ?? '');
    _apiKeyController = TextEditingController(text: widget.initialApiKey ?? '');
    _usernameController =
        TextEditingController(text: widget.initialUsername ?? '');
    _passwordController = TextEditingController();
    _publicAddressController = TextEditingController()
      ..addListener(_markMediaServerConfigDirty);
    _hardcoverController = TextEditingController();
    // A prompted new-instance form opens on the selector's disabled
    // placeholder ('') instead of a guessed type; every type-dependent
    // affordance stays hidden until a real one is picked (see
    // [_serviceTypeUnchosen]).
    _serviceType = (!widget.isEditing && widget.serviceTypePrompt != null)
        ? ''
        : (widget.initialServiceType ?? 'radarr');
    if (_isPlex && !widget.isEditing && _publicAddressController.text.isEmpty) {
      // Everyone signs in to Plex at the same place.
      _publicAddressController.text = 'https://app.plex.tv';
    }
    _isDefault = widget.initialIsDefault;
    _markInstanceSaved();
    if (widget.isEditing) {
      // A direct route may not include the service type. Load it before
      // choosing the grants/library-assignment endpoint for this editor.
      _loadDetails().then((_) {
        if (mounted) _loadDirectory();
      });
    } else {
      _loadDirectory();
    }
    _loadMediaRoots();
    _loadArrRootFolders();
    _loadWebhookStatus();
    _loadHardcoverStatus();
  }

  /// Reads the live instant-updates state so the section says whether the
  /// webhook is actually on. The server derives it from the arr's own Connect
  /// list — a stored flag would keep claiming "configured" after an admin
  /// deleted the record there. Older servers without the route answer 404/405
  /// and the section simply shows no status line.
  Future<void> _loadWebhookStatus() async {
    if (!widget.isEditing || !_supportsWebhook) return;
    String? result;
    var color = AppTheme.textSecondary;
    try {
      final status =
          await InstanceApiService(backendDio: ref.read(backendClientProvider))
              .webhookStatus(widget.instanceId!);
      if (!status.supported) return;
      if (status.configured) {
        result = context.l10n.settingsInstantUpdatesAreOn;
        color = AppTheme.available;
      } else {
        result = switch (status.state) {
          'stale' => context.l10n
              .settingsMessageTheValue1WebhookPointsAtADifferentCantinarr(
                  (_serviceLabel).toString()),
          'credential_missing' => context.l10n
              .settingsMessageTheValue1WebhookExistsButTheServerNo(
                  (_serviceLabel).toString()),
          'no_public_url' => context.l10n
              .settingsMessageTheServerCannotDetermineTheAddressTheValue1(
                  (_serviceLabel).toString()),
          _ => context.l10n.settingsInstantUpdatesAreNotConfiguredYet,
        };
      }
    } on DioException catch (e) {
      final code = e.response?.statusCode ?? 0;
      // An older server has no status route; unknown is not worth a line.
      if (code == 404 || code == 405) return;
      // Blindness, not absence: the state could not be read, which is a
      // different answer than "not configured".
      result = context.l10n.settingsMessageCouldNotCheckInstantUpdatesValue1(
          (apiErrorMessage(e)).toString());
    } catch (_) {
      return;
    }
    // A manual configure that already reported wins over this snapshot.
    if (!mounted || _isConfiguringWebhook || _webhookResult != null) return;
    setState(() {
      _webhookResult = result;
      _webhookResultColor = color;
    });
  }

  /// Reads the library folders this saved instance reports through the arr
  /// proxy. Unsaved instances have nothing to proxy to, so this is edit-only.
  Future<void> _loadArrRootFolders() async {
    if (!widget.isEditing || !_supportsMediaDownloads) return;
    final serviceType = _serviceType;
    _arrRootFoldersFetchedFor = serviceType;
    try {
      final folders = await InstanceApiService(
        backendDio: ref.read(backendClientProvider),
      ).listArrRootFolders(
        instanceId: widget.instanceId!,
        serviceType: serviceType,
      );
      if (!mounted || serviceType != _serviceType) return;
      setState(() {
        _arrRootFolders = folders;
        _arrRootFoldersKnown = true;
        _arrRootFoldersError = null;
      });
    } catch (_) {
      if (!mounted || serviceType != _serviceType) return;
      setState(() => _arrRootFoldersError = context.l10n
          .settingsMessageCouldNotReadTheLibraryFoldersValue1Reports(
              (_serviceLabel).toString()));
    }
  }

  Future<void> _loadMediaRoots() async {
    try {
      final roots = await InstanceApiService(
        backendDio: ref.read(backendClientProvider),
      ).listMediaRoots();
      if (!mounted) return;
      setState(() {
        _mediaRoots = roots;
        _mediaRootsError = null;
        if (!widget.isEditing) {
          _mediaMappingApiSupported = true;
          _mediaMappingsLoaded = true;
        }
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _mediaRoots = null;
        _mediaRootsError =
            context.l10n.settingsCouldNotLoadTheServerMediaFolders;
      });
    }
  }

  /// Loads the fresh instance list plus the users and their current pins for
  /// the user-assignment section.
  Future<void> _loadDirectory() async {
    try {
      final service =
          InstanceApiService(backendDio: ref.read(backendClientProvider));
      final instancesFuture = service.listInstances();
      final usersFuture = ref.read(authProvider.notifier).listUsers();
      // Attach listeners to both futures now: awaiting them sequentially
      // would leave the second future's error unhandled when the first
      // await throws, leaking it as an unhandled zone error. Future.wait
      // (eagerError: false) waits for both, throws the first error, and
      // drops the rest — the single catch below stays correct.
      final results = await Future.wait<Object>([instancesFuture, usersFuture]);
      final instances = results[0] as List<ServiceInstance>;
      final users = results[1] as List<UserSummary>;
      users.sort((a, b) =>
          a.username.toLowerCase().compareTo(b.username.toLowerCase()));
      if (!mounted) return;
      setState(() {
        _instances = instances;
        _instancesLoaded = true;
        _users = users;
        _applyAutoDefault();
      });
      await _loadPins();
      if (_isMediaServer && widget.isEditing && _supportsAccountManagement) {
        try {
          final accounts =
              await ref.read(mediaAccessServiceProvider).listAccounts();
          if (!mounted) return;
          setState(() {
            _mediaAccounts = {
              for (final a in accounts)
                if (a.instanceId == widget.instanceId) a.userId: a
            };
            _mediaAccountsFailed = false;
            _mediaAccountsLoaded = true;
          });
        } catch (_) {
          if (mounted) setState(() => _mediaAccountsFailed = true);
        }
      }
    } catch (_) {
      if (!mounted) return;
      setState(() => _userSelectError = context.l10n.settingsCouldNotLoadUsers);
    }
  }

  /// New automation instances start as default when the service has none.
  /// Older servers retain their first-instance-only behavior.
  /// Mutates state; call from within setState.
  void _applyAutoDefault() {
    if (widget.isEditing || !_instancesLoaded || _grantOnly) return;
    _isDefault = !_instances.any((i) =>
        i.serviceType == _serviceType && (!_assignmentModel || i.isDefault));
    _defaultDraft.markSaved(_isDefault);
  }

  /// Fetches the per-user pins and access grants for the selected service
  /// type. Both endpoints are instance-scoped but answer for the whole type,
  /// so when creating we can ask via any existing sibling; a type with no
  /// instances can have no rows. A checkbox starts checked when either row
  /// kind names this instance — a legacy pin is an assignment too, and
  /// showing it unchecked would turn the next save into a silent revocation.
  Future<void> _loadPins() async {
    if (!_supportsUserAssignment) return;
    if (_isAbs && widget.isEditing) {
      try {
        final access =
            await AudiobookLibraryAccessService(ref.read(backendClientProvider))
                .get(widget.instanceId!);
        if (!mounted) return;
        setState(() {
          _assignedUserIds = access.userIds;
          _savedAssignedUserIds = Set.of(access.userIds);
          _absPolicies = access.policies;
          _absSavedDefaultIds = access.defaultLibraryIds;
          _absAccessLoaded = true;
          _userSelectError = null;
        });
      } catch (_) {
        if (mounted) {
          setState(() => _userSelectError = context
              .l10n.settingsCouldNotLoadAudiobookshelfLibraryAssignments);
        }
      }
      return;
    }
    String? anchorId = widget.instanceId;
    if (anchorId == null) {
      for (final i in _instances) {
        if (i.serviceType == _serviceType) {
          anchorId = i.id;
          break;
        }
      }
    }
    if (anchorId == null) {
      if (!mounted) return;
      setState(() {
        _pins = const {};
        _assignedUserIds = <int>{};
        _savedAssignedUserIds = <int>{};
      });
      return;
    }
    try {
      final service =
          InstanceApiService(backendDio: ref.read(backendClientProvider));
      // Media servers have no per-user default pins (access is the grant
      // alone), so only the grant rows are read for them.
      final pins = _isMediaServer || _newAssignments
          ? const <int, String>{}
          : await service.getInstanceUsers(anchorId);
      final grants = await service.getInstanceGrantUsers(anchorId);
      if (!mounted) return;
      setState(() {
        _pins = pins;
        _assignedUserIds = widget.isEditing
            ? {
                ...pins.entries
                    .where((e) => e.value == widget.instanceId)
                    .map((e) => e.key),
                ...grants.entries
                    .where((e) => e.value.contains(widget.instanceId))
                    .map((e) => e.key),
              }
            : <int>{};
        _savedAssignedUserIds = Set.of(_assignedUserIds);
        _userSelectError = null;
      });
    } catch (_) {
      if (!mounted) return;
      setState(() =>
          _userSelectError = context.l10n.settingsCouldNotLoadUserAssignments);
    }
  }

  void _retryDirectory() {
    setState(() => _userSelectError = null);
    _loadDirectory();
  }

  /// The config payload only carries id/type/name, so fetch the full record
  /// (url, username) to prefill the form when editing.
  Future<void> _loadDetails() async {
    try {
      final service =
          InstanceApiService(backendDio: ref.read(backendClientProvider));
      final details = await service.getInstanceDetails(widget.instanceId!);
      if (!mounted || details == null) return;
      setState(() {
        _serviceType = details['service_type'] as String? ?? _serviceType;
        if (_nameController.text.isEmpty) {
          _nameController.text = details['name'] as String? ?? '';
        }
        if (_urlController.text.isEmpty) {
          _urlController.text = details['url'] as String? ?? '';
        }
        if (_usernameController.text.isEmpty) {
          _usernameController.text = details['username'] as String? ?? '';
        }
        _isDefault = details['is_default'] as bool? ?? _isDefault;
        _autoAddUsers = details['auto_add_users'] as bool? ?? false;
        _assignmentDetailsLoaded = true;
        _tagRequests = details['tag_requests'] as bool? ?? false;
        _tagRequestsLoaded = true;
        _defaultDraft.markSaved(_isDefault);
        _storedQbitAuth = details['has_api_key'] == true
            ? _QbitAuth.apiKey
            : _QbitAuth.password;
        _qbitAuth = _storedQbitAuth;
        _tdarrHasKey = _isTdarr && details['has_api_key'] == true;
        // Loading must not count a value typed during the request as saved.
        _detailsDraft.markSaved({
          ..._detailValues,
          'name': details['name'] as String? ?? widget.initialName ?? '',
          'url': details['url'] as String? ?? widget.initialUrl ?? '',
          'username':
              details['username'] as String? ?? widget.initialUsername ?? '',
          'apiKey': widget.initialApiKey ?? '',
          'password': '',
        });
        if (_isMediaServer) {
          final raw = details['media_server_config'];
          final config = raw is Map
              ? MediaServerConfig.fromJson(Map<String, dynamic>.from(raw))
              : const MediaServerConfig();
          if (_publicAddressController.text.isEmpty) {
            _publicAddressController.text = config.publicAddress;
          }
          _selectedLibraryIds = config.libraryIds.toSet();
          _availabilityScan = config.availabilityScan;
          _availabilityScanDirty = false;
          _listeningApps = config.listeningApps;
          _videoApps = config.videoApps;
          if (_isAbs) {
            _absSavedDefaultIds = config.libraryIds;
            _absSavedPublicAddress = config.publicAddress;
            _absSavedListeningApps = config.listeningApps;
          }
          if (_isPlex) {
            _plexLinkedStored = true;
            _plexMachineId = config.machineIdentifier;
            _plexAutoApprove = config.autoApprove;
          }
          // Hydration is not an edit: only a touch after this sends the
          // config back, so an untouched section keeps the server's copy.
          _mediaServerConfigDirty = false;
          _mediaDraft.markSaved([
            config.publicAddress,
            config.libraryIds.toList()..sort(),
            _isPlex ? config.machineIdentifier : '',
            _isPlex && config.autoApprove,
            config.listeningApps?.toJson(),
            config.videoApps?.toJson(),
            config.availabilityScan?.toJson(),
          ]);
        }
        if (details.containsKey('media_path_mappings')) {
          final rawMappings = details['media_path_mappings'] as List? ?? [];
          _replaceMediaPathMappings(rawMappings
              .whereType<Map>()
              .map((raw) => MediaPathMapping.fromJson(
                    Map<String, dynamic>.from(raw),
                  ))
              .toList(growable: false));
          _mediaMappingApiSupported = true;
          _mediaMappingsLoaded = true;
          _mediaMappingsError = null;
        } else {
          _mediaMappingsError = context
              .l10n.settingsUpdateTheCantinarrServerToConfigureMediaDownloads;
        }
      });
      // Deep links may open the editor without a service type; retry the
      // reported-folders read once the record names the real one.
      if (_arrRootFoldersFetchedFor != _serviceType) _loadArrRootFolders();
      // The stored key is the credential: with the id in the body a blank
      // key falls back to it, so the libraries list without retyping it.
      if (_isPlex) _loadPlexServers();
      if (_isMediaServer) _loadMediaServerLibraries();
    } catch (_) {
      // Connection fields remain manually editable, but mapping data must not
      // be guessed: omitting it on Save preserves the server's current rules.
      if (!mounted) return;
      setState(() => _mediaMappingsError =
          context.l10n.settingsCouldNotLoadThisInstanceSMediaPath);
    }
  }

  void _replaceMediaPathMappings(List<MediaPathMapping> mappings) {
    for (final mapping in _mediaPathMappings) {
      mapping.dispose();
    }
    _mediaPathMappings
      ..clear()
      ..addAll(mappings.map((mapping) => _MediaPathMappingFields.fromMapping(
            mapping,
            onChanged: _markMediaMappingsDirty,
          )));
    _mediaMappingsDirty = false;
    _mappingsDraft.markSaved(_mappingValues);
  }

  void _markMediaMappingsDirty() {
    _mediaMappingsDirty = true;
  }

  void _markMediaServerConfigDirty() {
    _mediaServerConfigDirty = true;
  }

  /// Reads the libraries the media server reports right now. When creating,
  /// the read needs the typed URL and key, so it runs after a passing
  /// connection test; when editing it runs on load through the stored key.
  /// A failed read is a notice with Retry, never a blocker: saving without
  /// it keeps the stored library choice (edit) or shares everything (create).
  Future<void> _loadMediaServerLibraries() async {
    if (!_isMediaServer) return;
    // A Plex read needs a linked account and a chosen server; until then
    // there is nothing to list.
    if (_isPlex && (!_plexHasCredential || _plexMachineId.isEmpty)) return;
    final serviceType = _serviceType;
    setState(() {
      _mediaServerLibrariesLoading = true;
      _mediaServerLibrariesError = null;
    });
    try {
      final probe = await InstanceApiService(
        backendDio: ref.read(backendClientProvider),
      ).listMediaServerLibraries(
        id: widget.instanceId,
        serviceType: serviceType,
        url: _urlController.text.trim(),
        apiKey: _apiKeyController.text.trim(),
        plexLinkPin: _isPlex && _plexAccount.isNotEmpty ? _plexPinId : null,
        machineIdentifier: _isPlex ? _plexMachineId : '',
      );
      if (!mounted || serviceType != _serviceType) return;
      setState(() {
        _mediaServerLibraries = probe.libraries;
        _mediaServerLibrariesLoading = false;
      });
    } catch (e) {
      if (!mounted || serviceType != _serviceType) return;
      setState(() {
        _mediaServerLibrariesLoading = false;
        _mediaServerLibrariesError = context.l10n
            .settingsMessageCouldnTLoadTheLibrariesThisServerReports(
                (apiErrorMessage(e)).toString());
      });
    }
  }

  void _addMediaPathMapping() {
    setState(() {
      _mediaPathMappings.add(_MediaPathMappingFields(
        onChanged: _markMediaMappingsDirty,
      ));
      _mediaMappingsDirty = true;
    });
  }

  /// Starts a mapping from a live reported folder, reusing the first row
  /// whose arr path is still blank before adding a new one.
  void _useReportedArrPath(String path) {
    for (final mapping in _mediaPathMappings) {
      if (mapping.arrPath.text.trim().isEmpty) {
        mapping.arrPath.text = path;
        setState(() {});
        return;
      }
    }
    setState(() {
      _mediaPathMappings.add(_MediaPathMappingFields(
        arrPath: path,
        onChanged: _markMediaMappingsDirty,
      ));
      _mediaMappingsDirty = true;
    });
  }

  void _removeMediaPathMapping(int index) {
    setState(() {
      _mediaPathMappings.removeAt(index).dispose();
      _mediaMappingsDirty = true;
    });
  }

  List<MediaPathMapping> _currentMediaPathMappings() => [
        for (final mapping in _mediaPathMappings)
          MediaPathMapping(
            arrPath: mapping.arrPath.text.trim(),
            cantinarrPath: mapping.cantinarrPath.text.trim(),
          ),
      ];

  @override
  void dispose() {
    // Finish removing the editor before a refresh reparses the route stack.
    final auth = _configAuth;
    WidgetsBinding.instance.endOfFrame.then((_) async {
      try {
        await auth.resumeConfigRefresh();
      } catch (_) {
        // Reconnect/resume retries without turning a saved instance into an error.
      }
    });
    _nameController.dispose();
    _urlController.dispose();
    _apiKeyController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    _publicAddressController.dispose();
    _hardcoverController.dispose();
    for (final mapping in _mediaPathMappings) {
      mapping.dispose();
    }
    _plexPollTimer?.cancel();
    super.dispose();
  }

  /// Starts the Plex PIN link: plex.tv opens in the browser for the admin to
  /// approve; the form polls until it is, then lists the account's servers.
  Future<void> _beginPlexLink() async {
    try {
      final start = await InstanceApiService(
        backendDio: ref.read(backendClientProvider),
      ).beginPlexLink();
      if (!mounted) return;
      setState(() {
        _plexLinking = true;
        _plexPinId = start.pinId;
        _plexLinkUrl = start.url;
        _plexAccount = '';
        _plexServers = null;
        _plexServersError = null;
      });
      // Poll while the admin approves in the browser; the link expires
      // server-side, so a forgotten form just times out quietly.
      _plexPollTimer?.cancel();
      _plexPollTimer = Timer.periodic(
        const Duration(seconds: 3),
        (_) => _checkPlexLink(silent: true),
      );
    } on DioException catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(_plexLinkFailure(e))));
      return;
    } catch (_) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(context.l10n.settingsCouldNotReachPlexTvTryAgain)));
      return;
    }
    // A browser that will not open is not a failed link: the Reopen
    // button stays, and the poll is already running.
    try {
      await launchUrl(Uri.parse(_plexLinkUrl!),
          mode: LaunchMode.externalApplication);
    } catch (_) {}
  }

  Future<void> _checkPlexLink({bool silent = false}) async {
    final pinId = _plexPinId;
    if (pinId == null) return;
    try {
      final state = await InstanceApiService(
        backendDio: ref.read(backendClientProvider),
      ).checkPlexLink(pinId);
      if (!mounted) return;
      if (!state.linked) {
        if (!silent) {
          ScaffoldMessenger.of(context).showSnackBar(SnackBar(
              content: Text(
                  context.l10n.settingsNotApprovedYetFinishSigningInOnPlex)));
        }
        return;
      }
      _plexPollTimer?.cancel();
      setState(() {
        _plexLinking = false;
        _plexAccount = state.account;
        _testResult = null;
        _mediaServerConfigDirty = true;
      });
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(context.l10n
              .settingsMessageLinkedAsValue1PickTheServerToShare(
                  (state.account).toString()))));
      _loadPlexServers();
    } on DioException catch (e) {
      if (!silent && mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(_plexLinkFailure(e))));
      }
    } catch (_) {
      if (!silent && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
            content: Text(context.l10n.settingsCouldNotReachPlexTvTryAgain)));
      }
    }
  }

  /// Says which side failed. A 404 is a server from before Plex instances
  /// (the arr proxy's wildcard answers it), a 502 is the server unable to
  /// reach plex.tv, and no status at all is no answer from the server.
  String _plexLinkFailure(DioException e) {
    switch (e.response?.statusCode) {
      case 404:
        return context.l10n.settingsThisServerDoesnTHavePlexLinkingYet;
      case 502:
        return context.l10n.settingsYourServerCouldnTReachPlexTvCheck;
      case null:
        return context.l10n.settingsNoAnswerFromTheServerCheckYourConnection;
      default:
        return context.l10n.settingsCouldNotReachPlexTvTryAgain;
    }
  }

  void _cancelPlexLink() {
    _plexPollTimer?.cancel();
    setState(() {
      _plexLinking = false;
      _plexPinId = null;
      _plexLinkUrl = null;
    });
  }

  /// Lists the linked account's owned servers for the picker: through the
  /// approved pin when one was just linked, else the stored token. A lone
  /// server is picked outright.
  Future<void> _loadPlexServers() async {
    if (!_plexHasCredential) return;
    setState(() {
      _plexServersLoading = true;
      _plexServersError = null;
    });
    try {
      final servers = await InstanceApiService(
        backendDio: ref.read(backendClientProvider),
      ).listPlexServers(
        id: widget.instanceId,
        plexLinkPin: _plexAccount.isNotEmpty ? _plexPinId : null,
        url: _urlController.text.trim(),
      );
      if (!mounted) return;
      setState(() {
        _plexServers = servers;
        _plexServersLoading = false;
        if (_plexMachineId.isEmpty && servers.length == 1) {
          _plexMachineId = servers.single.machineIdentifier;
          _mediaServerConfigDirty = true;
        }
      });
      if (_plexMachineId.isNotEmpty) _loadMediaServerLibraries();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _plexServersLoading = false;
        _plexServersError = context.l10n
            .settingsMessageCouldnTListTheAccountSServersValue1(
                (apiErrorMessage(e)).toString());
      });
    }
  }

  void _pickPlexServer(String machineIdentifier) {
    if (machineIdentifier == _plexMachineId) return;
    setState(() {
      _plexMachineId = machineIdentifier;
      _mediaServerConfigDirty = true;
      _testResult = null;
      // The libraries belong to the previous server.
      _mediaServerLibraries = null;
      _mediaServerLibrariesError = null;
      _selectedLibraryIds = <String>{};
    });
    _loadMediaServerLibraries();
  }

  Future<void> _testConnection() async {
    if (_serviceTypeUnchosen) {
      setState(() {
        _testSucceeded = false;
        _testResult = context.l10n.settingsChooseAServiceTypeFirst;
      });
      return;
    }
    setState(() {
      _isTesting = true;
      _testResult = null;
    });

    // The server performs the check: it is what dials instance URLs in
    // production, so cluster-internal names this device cannot resolve still
    // test truthfully, and blank credentials fall back to the stored ones.
    try {
      final backendDio = ref.read(backendClientProvider);
      final service = InstanceApiService(backendDio: backendDio);
      await service.testConnection(
        id: widget.instanceId,
        serviceType: _serviceType,
        url: _urlController.text.trim(),
        apiKey: _apiKeyController.text.trim(),
        clearApiKey: _isTdarr && _clearTdarrKey,
        username: _usernameController.text.trim(),
        password: _passwordController.text,
        plexLinkPin: _isPlex && _plexAccount.isNotEmpty ? _plexPinId : null,
        machineIdentifier: _isPlex ? _plexMachineId : '',
      );
      if (!mounted) return;
      setState(() {
        _isTesting = false;
        _testSucceeded = true;
        _testResult = context.l10n.settingsConnectionSuccessful;
      });
      // A passing test proves the typed URL and key, which is exactly what
      // the library read needs when creating.
      if (_isMediaServer) _loadMediaServerLibraries();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isTesting = false;
        _testSucceeded = false;
        _testResult = apiErrorMessage(e);
      });
    }
  }

  String? _validate() {
    if (_availabilityScan?.enabled == true && _availabilityScan!.libraryIds.isEmpty) {
      return context.l10n.availabilityChooseLibraries;
    }
    if (_isAbs) {
      if (widget.isEditing && !_absAccessLoaded) {
        return context.l10n.settingsLoadTheLibraryAssignmentsBeforeSaving;
      }
      for (final id in _absEditedPolicies) {
        final policy = _absPolicies[id]!;
        if (policy.mode == 'selected' && policy.libraryIds.isEmpty) {
          return context.l10n.settingsChooseAtLeastOneLibraryForEachIndividual;
        }
      }
    }
    if (_serviceTypeUnchosen) {
      return context.l10n.settingsChooseAServiceType;
    }
    if (_isPlex) {
      if (_nameController.text.trim().isEmpty)
        return context.l10n.settingsNameIsRequired;
      if (!_plexHasCredential)
        return context.l10n.settingsLinkAPlexAccountFirst;
      if (_plexMachineId.isEmpty)
        return context.l10n.settingsPickThePlexServerToShare;
    } else if (_nameController.text.trim().isEmpty ||
        _urlController.text.trim().isEmpty) {
      return context.l10n.settingsNameAndUrlAreRequired;
    }
    if (_shouldSubmitMediaPathMappings) {
      for (final mapping in _mediaPathMappings) {
        if (mapping.arrPath.text.trim().isEmpty ||
            mapping.cantinarrPath.text.trim().isEmpty) {
          return context.l10n.settingsBothPathsAreRequiredForEveryMediaMapping;
        }
      }
    }
    if (_isMediaServer) {
      // The address is handed to users verbatim as a link, so it must be one.
      final address = _publicAddressController.text.trim();
      if (address.isNotEmpty &&
          !address.startsWith('http://') &&
          !address.startsWith('https://')) {
        return context.l10n.settingsEnterAnHttpOrHttpsAddressForUsers;
      }
    }
    // When editing, blank credentials keep the existing ones. Plex's is
    // the link, checked above. A qBittorrent instance moved onto its other
    // credential shape has nothing stored to keep, so that shape's fields
    // are required as on a new instance.
    if ((widget.isEditing && !_qbitSwitchingShape) || _isPlex) return null;
    if (_usesUserPass) {
      if (_credentialsOptional) return null;
      if (_passwordOnly) {
        if (_passwordController.text.isEmpty)
          return context.l10n.settingsPasswordIsRequired;
      } else if (_usernameController.text.trim().isEmpty ||
          _passwordController.text.isEmpty) {
        return context.l10n.settingsUsernameAndPasswordAreRequired;
      }
    } else if (!_isTdarr && _apiKeyController.text.trim().isEmpty) {
      return context.l10n.settingsApiKeyIsRequired;
    }
    return null;
  }

  /// The sibling instance currently holding the global default for the
  /// selected type, if any (excludes the instance being edited).
  ServiceInstance? get _currentDefaultSibling {
    for (final i in _instances) {
      if (i.serviceType == _serviceType &&
          i.isDefault &&
          i.id != widget.instanceId) {
        return i;
      }
    }
    return null;
  }

  /// Making this instance the default displaces the current one — spell out
  /// exactly which instance the default moves from and to, and let the admin
  /// back out, before anything is saved.
  Future<bool> _confirmDefaultTakeover() async {
    final sibling = _currentDefaultSibling;
    if (!_isDefault || _grantOnly || sibling == null) return true;
    final label = _serviceLabel;
    final newName = _nameController.text.trim();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n
            .settingsMessageChangeDefaultValue1Instance((label).toString())),
        content: Text(
          _newAssignments
              ? context.l10n
                  .settingsMessageSetValue1AsThePreferredRequestDestinationFor(
                      (newName).toString())
              : context.l10n
                  .settingsMessageValue1IsCurrentlyTheDefaultValue2InstanceSaving(
                      (sibling.name).toString(),
                      (label).toString(),
                      (sibling.name).toString(),
                      (newName).toString(),
                      (newName).toString()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(context.l10n.settingsCancel),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(context.l10n.settingsMakeDefault),
          ),
        ],
      ),
    );
    return confirmed == true;
  }

  Future<bool> _confirmDefaultRemoval() async {
    if (!_newAssignments ||
        !widget.isEditing ||
        _isDefault ||
        !_instances.any((i) => i.id == widget.instanceId && i.isDefault)) {
      return true;
    }
    return await showDialog<bool>(
            context: context,
            builder: (context) => AlertDialog(
                  title: Text(context.l10n.settingsRemoveTheGlobalDefault),
                  content: Text(context.l10n
                      .settingsUsersWithoutAPersonalPreferenceWillUseTheir),
                  actions: [
                    TextButton(
                        onPressed: () => Navigator.pop(context, false),
                        child: Text(context.l10n.settingsCancel)),
                    TextButton(
                        onPressed: () => Navigator.pop(context, true),
                        child: Text(context.l10n.settingsRemoveDefault))
                  ],
                )) ==
        true;
  }

  bool _sameSelection(Set<int> a, Set<int> b) =>
      a.length == b.length && a.containsAll(b);

  String _instanceName(String id) {
    for (final i in _instances) {
      if (i.id == id) return i.name;
    }
    return id;
  }

  /// Per-user access: selected users can use this instance. Access is
  /// additive — a user granted this library beside their default gets a
  /// per-request choice between them — and unselecting removes their access
  /// to exactly this instance (their default and sibling grants stay put).
  List<Widget> _buildUserSelect() {
    final users = _users;
    return [
      const SizedBox(height: 16),
      Text(
        _grantOnly && !_isMediaServer
            ? context.l10n.settingsAssignedUsers
            : context.l10n.settingsUserAccess2,
        style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 13,
            fontWeight: FontWeight.w600),
      ),
      const SizedBox(height: 4),
      Text(
        _isChaptarr
            ? context
                .l10n.settingsChaptarrInstancesAreAssignedPerUserSelectedUsers
            : _isLidarr
                ? context
                    .l10n.settingsLidarrInstancesAreAssignedPerUserSelectedUsers
                : _isMediaServer && _supportsAccountManagement
                    ? context
                        .l10n.settingsSelectedUsersCanUseThisServerInCantinarr
                    : _isPlex
                        ? context
                            .l10n.settingsSelectedUsersGetThisServerUnderWatchOn
                        : _isMediaServer
                            ? context.l10n
                                .settingsMessageSelectedUsersGetThisServerUnderWatchOn(
                                    (_serviceLabel).toString())
                            : context.l10n
                                .settingsMessageSelectedUsersCanUseThisLibraryForRequests(
                                    (_serviceLabel).toString()),
        style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
      ),
      const SizedBox(height: 8),
      if (_userSelectError != null)
        Row(
          children: [
            Expanded(
              child: Text(_userSelectError!,
                  style: const TextStyle(color: AppTheme.error, fontSize: 13)),
            ),
            TextButton(
              onPressed: _retryDirectory,
              child: Text(context.l10n.settingsRetry),
            ),
          ],
        )
      else if (users == null)
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 12),
          child: Center(
            child: SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(
                  strokeWidth: 2, color: AppTheme.accent),
            ),
          ),
        )
      else if (users.isEmpty)
        Text(context.l10n.settingsNoUsersYet2,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13))
      else
        ...users.expand((user) {
          final account = _mediaAccounts[user.id];
          final policy =
              _absPolicies[user.id] ?? const AudiobookLibraryPolicy();
          final accountKnown = !widget.isEditing ||
              (_mediaAccountsLoaded && !_mediaAccountsFailed);
          return [
            _userTile(user),
            if (_isAbs && _assignedUserIds.contains(user.id))
              AudiobookUserLibraries(
                key: ValueKey('abs-libraries-${user.id}'),
                username: user.username,
                policy: policy,
                libraries: _mediaServerLibraries ?? const [],
                defaultLibraryIds: _selectedLibraryIds,
                enabled: accountKnown &&
                    (account == null ||
                        (account.manageAccess && !account.administrator)),
                unavailableReason: !accountKnown
                    ? context
                        .l10n.settingsLoadAccountManagementToChangeLibraries
                    : account?.administrator == true
                        ? context.l10n
                            .settingsAdministratorAccountsCannotBeChangedHere
                        : null,
                keepExisting: account != null &&
                    !account.createdByCantinarr &&
                    !policy.managesLibraries &&
                    !_absEditedPolicies.contains(user.id),
                onChanged: (value) => setState(() {
                  _absPolicies[user.id] = value;
                  _absEditedPolicies.add(user.id);
                }),
              ),
          ];
        }),
    ];
  }

  Widget _userTile(UserSummary user) {
    final pinnedTo = _pins[user.id];
    // Surface the user's default library when it is a sibling, so the admin
    // can see access here is IN ADDITION to it, not a move off it.
    final defaultElsewhere = pinnedTo != null && pinnedTo != widget.instanceId
        ? _instanceName(pinnedTo)
        : null;
    return CheckboxListTile(
      dense: true,
      contentPadding: EdgeInsets.zero,
      controlAffinity: ListTileControlAffinity.leading,
      activeColor: AppTheme.accent,
      title: Text(user.username,
          style: const TextStyle(color: AppTheme.textPrimary)),
      subtitle: _isMediaServer && _supportsAccountManagement
          ? Text(
              _mediaAccountsFailed
                  ? context.l10n.settingsAccountManagementCouldNotBeLoaded
                  : !_mediaAccountsLoaded && widget.isEditing
                      ? context.l10n.settingsLoadingAccountManagement
                      : _mediaAccounts.containsKey(user.id)
                          ? '${mediaAccessLabel(context, _mediaAccounts[user.id]!.managementLabel)} · ${mediaAccessLabel(context, _mediaAccounts[user.id]!.accessLabel)}'
                          : context.l10n.settingsNoLinkedAccount,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12))
          : defaultElsewhere != null
              ? Text(
                  context.l10n.settingsMessageDefaultLibraryValue1(
                      (defaultElsewhere).toString()),
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 12))
              : null,
      value: _assignedUserIds.contains(user.id),
      onChanged: (checked) => setState(() {
        if (checked == true) {
          _assignedUserIds.add(user.id);
        } else {
          _assignedUserIds.remove(user.id);
        }
      }),
    );
  }

  Future<void> _saveAbsAccess(String instanceId) async {
    final access =
        await AudiobookLibraryAccessService(ref.read(backendClientProvider))
            .save(instanceId,
                userIds: _assignedUserIds,
                defaultLibraryIds: _selectedLibraryIds,
                policies: {
          for (final id in _absEditedPolicies) id: _absPolicies[id]!
        });
    if (!mounted) return;
    setState(() {
      _absPolicies = access.policies;
      _absEditedPolicies.clear();
      _absSavedDefaultIds = access.defaultLibraryIds;
      _absSavedPublicAddress = _publicAddressController.text.trim();
      _absSavedListeningApps = _listeningApps;
      _savedAssignedUserIds = Set.of(access.userIds);
      _absSyncPending = access.syncPending;
    });
  }

  Future<void> _openUserImport() async {
    final added = _assignedUserIds.difference(_savedAssignedUserIds);
    final removed = _savedAssignedUserIds.difference(_assignedUserIds);
    final editedPolicies = {
      for (final userId in _absEditedPolicies)
        if (_absPolicies[userId] != null) userId: _absPolicies[userId]!,
    };
    await context.push(Uri(
      path: '/settings/users/import',
      queryParameters: {'instanceId': widget.instanceId!},
    ).toString());
    if (!mounted) return;
    await _loadDirectory();
    if (!mounted) return;
    // Refresh imported accounts while preserving edits still in this form.
    setState(() {
      _assignedUserIds = {..._assignedUserIds, ...added}..removeAll(removed);
      _absPolicies.addAll(editedPolicies);
    });
  }

  Future<void> _offerUserImport(String instanceId) async {
    // This runs only after creation has persisted. Dismissing or returning
    // from import must never submit the new-instance form a second time.
    _markInstanceSaved();
    _savedAssignedUserIds = Set.of(_assignedUserIds);
    setState(() => _isSaving = false);
    final importNow = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => AlertDialog(
        title: Text(context.l10n.settingsMediaServerConnected),
        content: Text(
          context.l10n.settingsMessageValue1IsConnectedImportItsUsersToLink(
              (_nameController.text.trim()).toString()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogContext).pop(false),
            child: Text(context.l10n.settingsNotNow),
          ),
          ElevatedButton.icon(
            onPressed: () => Navigator.of(dialogContext).pop(true),
            icon: const Icon(Icons.group_add_outlined),
            label: Text(context.l10n.settingsImportUsers),
          ),
        ],
      ),
    );
    if (!mounted) return;
    if (importNow == true) {
      context.pushReplacement(Uri(
        path: '/settings/users/import',
        queryParameters: {'instanceId': instanceId},
      ).toString());
    } else {
      _finishEditing();
    }
  }

  Future<void> _save() async {
    if (_creationComplete) return;
    final validationError = _validate();
    if (validationError != null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(validationError)),
      );
      return;
    }
    if (!await _confirmDefaultRemoval()) return;
    if (!mounted || !await _confirmDefaultTakeover()) return;
    if (!mounted) return;

    // Media servers and legacy grant-only services have no global default.
    final isDefault = !_grantOnly && _isDefault;
    // Apply assignments only when the section is visible and the selection
    // actually changed — a hidden section must never silently rewrite pins.
    final applyAssignments = _showUserSelect &&
        _userSelectError == null &&
        _users != null &&
        !_sameSelection(_assignedUserIds, _savedAssignedUserIds);
    final assignedIds = _assignedUserIds.toList()..sort();
    final mediaPathMappings =
        _shouldSubmitMediaPathMappings ? _currentMediaPathMappings() : null;
    // Media-server settings travel whole: always on create, and on edit only
    // when the section was touched, so an unrelated edit never rewrites the
    // stored address or library choice (null = keep).
    final mediaServerConfig =
        _isMediaServer && (!widget.isEditing || _mediaServerConfigDirty)
            ? MediaServerConfig(
                availabilityScan: _availabilityScan,
                publicAddress: _publicAddressController.text.trim(),
                libraryIds: _isAbs && widget.isEditing
                    ? _absSavedDefaultIds
                    : _selectedLibraryIds.toList(growable: false),
                listeningApps: _isAbs ? _listeningApps : null,
                videoApps: VideoApps.serviceTypes.contains(_serviceType)
                    ? _videoApps
                    : null,
                machineIdentifier: _isPlex ? _plexMachineId : '',
                autoApprove: _isPlex && _plexAutoApprove,
              )
            : null;
    // A Plex instance saves with the approved pin; the server swaps in the
    // token it holds for it. Editing without a relink keeps the stored one.
    final plexLinkPin = _isPlex && _plexAccount.isNotEmpty ? _plexPinId : null;

    setState(() => _isSaving = true);

    try {
      final backendDio = ref.read(backendClientProvider);
      final service = InstanceApiService(backendDio: backendDio);

      if (widget.isEditing) {
        // Library-only edits can be queued even while ABS is unreachable.
        if (!_isAbs ||
            _detailsDraft.hasChanges(_detailValues) ||
            _listeningApps?.ios != _absSavedListeningApps?.ios ||
            _listeningApps?.android != _absSavedListeningApps?.android ||
            _publicAddressController.text.trim() != _absSavedPublicAddress) {
          await service.updateInstance(
            id: widget.instanceId!,
            name: _nameController.text.trim(),
            url: _urlController.text.trim(),
            apiKey: _apiKeyController.text.trim(),
            clearApiKey: _isTdarr && _clearTdarrKey,
            username: _usernameController.text.trim(),
            password: _passwordController.text,
            isDefault: isDefault,
            tagRequests: _supportsRequesterTags && _tagRequestsLoaded
                ? _tagRequests
                : null,
            autoAddUsers: _newAssignments && _assignmentDetailsLoaded
                ? _autoAddUsers
                : null,
            mediaPathMappings: mediaPathMappings,
            mediaServerConfig: mediaServerConfig,
            plexLinkPin: plexLinkPin,
          );
        }
        if (_isAbs) {
          await _saveAbsAccess(widget.instanceId!);
        }
        _markInstanceSaved();
        if (applyAssignments && !_isAbs) {
          try {
            await service.updateInstanceGrantUsers(
                widget.instanceId!, assignedIds);
          } catch (e) {
            // The instance itself saved; stay here so Save can retry the
            // assignments (re-updating the instance is idempotent).
            if (!mounted) return;
            setState(() => _isSaving = false);
            ScaffoldMessenger.of(context).showSnackBar(
              SnackBar(
                  content: Text(context.l10n
                      .settingsMessageInstanceSavedButAssigningUsersFailedValue1(
                          (apiErrorMessage(e)).toString()))),
            );
            return;
          }
        }
        await _refreshConfigAfterSave();
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(
                content: Text(_absSyncPending
                    ? context.l10n
                        .settingsLibraryAccessSavedChangesArePendingCantinarrWill
                    : context.l10n.settingsInstanceUpdated)),
          );
          if (_absSyncPending) {
            setState(() => _isSaving = false);
          } else {
            _finishEditing();
          }
        }
        return;
      }

      final created = await service.createInstance(
        serviceType: _serviceType,
        name: _nameController.text.trim(),
        url: _urlController.text.trim(),
        apiKey: _apiKeyController.text.trim(),
        username: _usernameController.text.trim(),
        password: _passwordController.text,
        isDefault: isDefault,
        tagRequests: _supportsRequesterTags ? _tagRequests : null,
        autoAddUsers: _newAssignments ? _autoAddUsers : null,
        mediaPathMappings: mediaPathMappings,
        mediaServerConfig: mediaServerConfig,
        plexLinkPin: plexLinkPin,
      );
      _creationComplete = true;
      // The instance exists now, so a failed assignment must not re-run
      // create: surface it and let the admin retry from the edit screen.
      String? assignmentError;
      if (applyAssignments || _isAbs) {
        try {
          if (_isAbs) {
            await _saveAbsAccess(created.id);
          } else {
            await service.updateInstanceGrantUsers(created.id, assignedIds);
          }
        } catch (e) {
          assignmentError = apiErrorMessage(e);
        }
      }
      // Instant updates are on by default: install the server-managed webhook
      // right away, so the feature never depends on the admin finding the
      // button later. A failure (commonly a callback the arr cannot reach)
      // must not undo or obscure the successful create — it is reported and
      // retried from the edit screen.
      String? webhookError;
      var webhookConfigured = false;
      if (_supportsWebhook) {
        try {
          await service.configureWebhook(created.id);
          webhookConfigured = true;
        } catch (e) {
          webhookError = apiErrorMessage(e);
        }
      }
      await _refreshConfigAfterSave();
      if (mounted) {
        final problems = <String>[
          if (assignmentError != null)
            context.l10n.settingsMessageAssigningUsersFailedValue1(
                (assignmentError).toString()),
          if (webhookError != null)
            context.l10n.settingsMessageInstantUpdatesCouldnTBeConfiguredValue1(
                (webhookError).toString()),
        ];
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(problems.isNotEmpty
                  ? context.l10n
                      .settingsMessageInstanceCreatedButValue1EditTheInstanceTo(
                          (problems.join('; ')).toString())
                  : webhookConfigured
                      ? context
                          .l10n.settingsInstanceCreatedInstantUpdatesConfigured
                      : context.l10n.settingsInstanceCreated)),
        );
        if (_isMediaServer && problems.isEmpty) {
          await _offerUserImport(created.id);
        } else {
          _finishEditing();
        }
      }
    } catch (e) {
      if (!mounted) return;
      setState(() => _isSaving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(context.l10n.settingsMessageFailedToSaveValue1(
                (apiErrorMessage(e)).toString()))),
      );
    }
  }

  Future<void> _refreshConfigAfterSave() async {
    if (widget.refreshConfigAfterReturn) return;
    final activeBefore = ref.read(instanceProvider);
    if (_isMediaServer) {
      // A grant can settle a waiting Plex user (the share goes out off the
      // request), so the drawer's waiting count is re-read here and again
      // whenever the drawer opens.
      ref.read(plexInvitesWaitingProvider.notifier).refresh();
    }
    try {
      await ref.read(authProvider.notifier).refreshConfig();
      if (!mounted) return;
      final refreshed = ref.read(instanceProvider);
      final notifier = ref.read(instanceProvider.notifier);
      final radarrId = activeBefore.activeRadarrInstanceId;
      if (radarrId != null &&
          refreshed.radarrInstances
              .any((instance) => instance.id == radarrId)) {
        notifier.setActiveRadarrInstance(radarrId);
      }
      final sonarrId = activeBefore.activeSonarrInstanceId;
      if (sonarrId != null &&
          refreshed.sonarrInstances
              .any((instance) => instance.id == sonarrId)) {
        notifier.setActiveSonarrInstance(sonarrId);
      }
      final chaptarrId = activeBefore.activeChaptarrInstanceId;
      if (chaptarrId != null &&
          refreshed.chaptarrInstances
              .any((instance) => instance.id == chaptarrId)) {
        notifier.setActiveChaptarrInstance(chaptarrId);
      }
      final lidarrId = activeBefore.activeLidarrInstanceId;
      if (lidarrId != null &&
          refreshed.lidarrInstances
              .any((instance) => instance.id == lidarrId)) {
        notifier.setActiveLidarrInstance(lidarrId);
      }
      final downloadId = activeBefore.activeDownloadInstanceId;
      if (downloadId != null &&
          refreshed.downloadInstances
              .any((instance) => instance.id == downloadId)) {
        notifier.setActiveDownloadInstance(downloadId);
      }
      final watchHistoryId = activeBefore.activeWatchHistoryInstanceId;
      if (watchHistoryId != null &&
          refreshed.watchHistoryInstances
              .any((instance) => instance.id == watchHistoryId)) {
        notifier.setActiveWatchHistoryInstance(watchHistoryId);
      }
      final tdarrId = activeBefore.activeTdarrInstanceId;
      if (tdarrId != null &&
          refreshed.tdarrInstances.any((i) => i.id == tdarrId)) {
        notifier.setActiveTdarrInstance(tdarrId);
      }
    } catch (_) {
      // The instance itself is already saved. The normal resume/config refresh
      // will reconcile capability metadata if this best-effort refresh fails.
    }
  }

  Future<void> _delete() async {
    if (!widget.isEditing) return;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n.settingsDeleteInstance),
        content: Text(_newAssignments
            ? context
                .l10n.settingsDeleteThisInstanceAndItsUserAssignmentsAffected
            : context.l10n.settingsAreYouSureYouWantToDeleteThis),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(context.l10n.settingsCancel),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(context.l10n.settingsDelete,
                style: TextStyle(color: AppTheme.error)),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    try {
      final backendDio = ref.read(backendClientProvider);
      final service = InstanceApiService(backendDio: backendDio);
      await service.deleteInstance(widget.instanceId!);

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(context.l10n.settingsInstanceDeleted)),
        );
        _finishEditing();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(context.l10n.settingsMessageFailedToDeleteValue1(
                  (apiErrorMessage(e)).toString()))),
        );
      }
    }
  }

  Future<void> _configureWebhook() async {
    final id = widget.instanceId;
    if (id == null) return;
    setState(() {
      _isConfiguringWebhook = true;
      _webhookResult = null;
    });
    try {
      final service =
          InstanceApiService(backendDio: ref.read(backendClientProvider));
      await service.configureWebhook(id);
      if (!mounted) return;
      setState(() {
        _isConfiguringWebhook = false;
        _webhookResult = context.l10n.settingsInstantUpdatesAreConfigured;
        _webhookResultColor = AppTheme.available;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isConfiguringWebhook = false;
        _webhookResult = apiErrorMessage(e);
        _webhookResultColor = AppTheme.error;
      });
    }
  }

  /// Reads whether this Chaptarr instance is connected to Hardcover. The
  /// server answers connected-or-not from the stored slot and never returns
  /// the token. Older servers without the route answer 404/405 and the
  /// section stays hidden.
  Future<void> _loadHardcoverStatus() async {
    if (!widget.isEditing || !_isChaptarr) return;
    try {
      final status =
          await InstanceApiService(backendDio: ref.read(backendClientProvider))
              .hardcoverStatus(widget.instanceId!);
      if (!mounted || !status.supported) return;
      setState(() {
        _hardcoverSupported = true;
        _hardcoverConnected = status.configured;
        _hardcoverOAuthAvailable = status.oauthAvailable;
        _hardcoverReconnect = status.reconnectRequired;
        _hardcoverMethod = status.method;
      });
    } catch (_) {
      // Unknown is not worth a section: an older server, or a read that
      // failed, must not render as "not connected".
    }
  }

  Future<void> _connectHardcover() async {
    final id = widget.instanceId;
    if (id == null) return;
    final service =
        InstanceApiService(backendDio: ref.read(backendClientProvider));
    setState(() {
      _isSavingHardcover = true;
      _hardcoverResult = null;
    });
    try {
      final flow = await showDialog<HardcoverDeviceFlow>(
        context: context,
        barrierDismissible: false,
        builder: (_) =>
            HardcoverConnectionDialog(service: service, instanceId: id),
      );
      if (!mounted) return;
      // Cancellation can race a successful server commit. Always read status
      // again, including when the dialog returned without a connection.
      final status = await service.hardcoverStatus(id);
      if (!mounted) return;
      ref.invalidate(trendingBooksForInstanceProvider(id));
      setState(() {
        _hardcoverConnected = status.configured;
        _hardcoverMethod = status.method;
        _hardcoverReconnect = status.reconnectRequired;
        _hardcoverResult = flow?.status == 'connected'
            ? context.l10n.settingsHardcoverIsConnected
            : null;
        _hardcoverResultColor = AppTheme.available;
      });
      if (flow?.status == 'connected' &&
          status.connectionId == flow!.connectionId) {
        await _offerHardcoverForOtherInstances(service, '',
            connectionId: flow.connectionId);
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _hardcoverResult = apiErrorMessage(e);
          _hardcoverResultColor = AppTheme.error;
        });
      }
    } finally {
      if (mounted) setState(() => _isSavingHardcover = false);
    }
  }

  Future<void> _saveHardcoverToken() async {
    final id = widget.instanceId;
    if (id == null) return;
    final token = _hardcoverController.text.trim();
    if (token.isEmpty) {
      setState(() {
        _hardcoverResult =
            context.l10n.settingsPasteTheApiTokenFromHardcoverFirst;
        _hardcoverResultColor = AppTheme.error;
      });
      return;
    }
    setState(() {
      _isSavingHardcover = true;
      _hardcoverResult = null;
    });
    try {
      final service = InstanceApiService(
        backendDio: ref.read(backendClientProvider),
      );
      final status = await service.saveHardcoverToken(id, token);
      if (!mounted) return;
      ref.invalidate(trendingBooksForInstanceProvider(id));
      _hardcoverController.clear();
      setState(() {
        _hardcoverConnected = status.configured;
        _hardcoverMethod = status.method;
        _hardcoverReconnect = status.reconnectRequired;
        _hardcoverResult = context.l10n.settingsHardcoverIsConnected;
        _hardcoverResultColor = AppTheme.available;
      });
      if (status.configured) {
        await _offerHardcoverForOtherInstances(service, token);
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _hardcoverResult = apiErrorMessage(e);
        _hardcoverResultColor = AppTheme.error;
      });
    } finally {
      if (mounted) setState(() => _isSavingHardcover = false);
    }
  }

  Future<void> _offerHardcoverForOtherInstances(
    InstanceApiService service,
    String token, {
    String? connectionId,
  }) async {
    // Re-read the directory after saving: an instance may have been added or
    // removed while the editor was open. Only the instances named in the
    // confirmation receive the connection. OAuth uses explicit revisions;
    // API tokens use the existing write-only endpoint.
    final List<ServiceInstance> instances;
    try {
      instances = await service.listInstances();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _hardcoverResult = context.l10n
            .settingsMessageHardcoverIsConnectedToThisInstanceButThe(
                (apiErrorMessage(e)).toString());
        _hardcoverResultColor = AppTheme.error;
      });
      return;
    }
    if (!mounted) return;
    final others = instances
        .where(
          (instance) =>
              instance.serviceType == 'chaptarr' &&
              instance.id != widget.instanceId,
        )
        .toList(growable: false);
    if (others.isEmpty) return;
    final revisions = <String, int>{};
    final failures = <String>[];
    if (connectionId != null) {
      for (final instance in others) {
        try {
          revisions[instance.id] =
              (await service.hardcoverStatus(instance.id)).revision;
        } catch (e) {
          failures.add('${instance.name}: ${apiErrorMessage(e)}');
        }
      }
    }
    if (!mounted) return;
    setState(() => _isSavingHardcover = false);
    final applyToAll = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        scrollable: true,
        title: Text(context.l10n.settingsUseHardcoverForAllChaptarrInstances),
        content: Text(
          context.l10n.settingsMessageAlsoUseThisValue1ForValue2AnyHardcover(
              (connectionId == null
                      ? context.l10n.settingsToken
                      : context.l10n.settingsConnection)
                  .toString(),
              (others.map((instance) => '• ${instance.name}').join('\n'))
                  .toString()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(context.l10n.settingsOnlyThisInstance),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(context.l10n.settingsApplyToAll),
          ),
        ],
      ),
    );
    if (!mounted || applyToAll != true) return;
    setState(() {
      _isSavingHardcover = true;
      _hardcoverResult =
          context.l10n.settingsConnectingHardcoverToTheOtherChaptarrInstances;
    });
    if (connectionId != null) {
      if (revisions.isNotEmpty) {
        final results = await service.applyHardcoverConnection(
            widget.instanceId!, connectionId, revisions);
        for (final instance
            in others.where((item) => revisions.containsKey(item.id))) {
          final matches =
              results.where((result) => result.instanceId == instance.id);
          if (matches.length == 1 && matches.single.applied) {
            if (mounted) {
              ref.invalidate(trendingBooksForInstanceProvider(instance.id));
            }
          } else {
            failures.add(
                '${instance.name}: ${matches.length == 1 ? matches.single.error : context.l10n.settingsNoResultWasReturnedTryAgain}');
          }
        }
      }
    } else {
      // Older servers still accept write-only API tokens one instance at a time.
      for (final instance in others) {
        try {
          await service.saveHardcoverToken(instance.id, token);
          if (mounted) {
            ref.invalidate(trendingBooksForInstanceProvider(instance.id));
          }
        } catch (e) {
          failures.add('${instance.name}: ${apiErrorMessage(e)}');
        }
      }
    }
    if (!mounted) return;
    setState(() {
      _hardcoverResult = failures.isEmpty
          ? context.l10n
              .settingsMessageHardcoverIsConnectedToAllValue1ChaptarrInstances(
                  (others.length + 1).toString())
          : context.l10n
              .settingsMessageValue1ForValue2OfValue3ChaptarrInstancesCould(
                  (connectionId == null
                          ? context.l10n.settingsTokenSaved
                          : context.l10n.settingsConnected)
                      .toString(),
                  (others.length + 1 - failures.length).toString(),
                  (others.length + 1).toString(),
                  (failures.join('\n')).toString());
      _hardcoverResultColor =
          failures.isEmpty ? AppTheme.available : AppTheme.error;
    });
  }

  Future<void> _clearHardcoverToken() async {
    final id = widget.instanceId;
    if (id == null) return;
    setState(() {
      _isSavingHardcover = true;
      _hardcoverResult = null;
    });
    try {
      await InstanceApiService(backendDio: ref.read(backendClientProvider))
          .clearHardcoverToken(id);
      if (!mounted) return;
      ref.invalidate(trendingBooksForInstanceProvider(id));
      setState(() {
        _isSavingHardcover = false;
        _hardcoverConnected = false;
        _hardcoverMethod = 'none';
        _hardcoverReconnect = false;
        _hardcoverUseToken = false;
        _hardcoverResult = context.l10n.settingsHardcoverIsDisconnected;
        _hardcoverResultColor = AppTheme.textSecondary;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isSavingHardcover = false;
        _hardcoverResult = apiErrorMessage(e);
        _hardcoverResultColor = AppTheme.error;
      });
    }
  }

  /// Service-name examples: the Cantinarr server is what dials this URL, so
  /// the container/cluster DNS name is the canonical form for the primary
  /// (compose/k8s) distribution. LAN IPs and FQDNs work just as well.
  String get _urlHint {
    switch (_serviceType) {
      case '':
        return 'http://service-name:port';
      case 'sonarr':
        return 'http://sonarr:8989';
      case 'chaptarr':
        return 'http://chaptarr:8787';
      case 'lidarr':
        return 'http://lidarr:8686';
      case 'sabnzbd':
        return 'http://sabnzbd:8080';
      case 'qbittorrent':
        return 'http://qbittorrent:8081';
      case 'nzbget':
        return 'http://nzbget:6789';
      case 'transmission':
        return 'http://transmission:9091';
      case 'deluge':
        return 'http://deluge:8112';
      case 'rutorrent':
        return 'http://rutorrent:8080';
      case 'tautulli':
        return 'http://tautulli:8181';
      case 'tracearr':
        return 'http://tracearr:3000';
      case 'tdarr':
        return 'http://tdarr:8266';
      case 'audiobookshelf':
        return 'http://audiobookshelf:80';
      case 'jellyfin':
        return 'http://jellyfin:8096';
      case 'emby':
        return 'http://emby:8096';
      default:
        return 'http://radarr:7878';
    }
  }

  String get _nameHint {
    if (_isDownloadClient) return context.l10n.settingsEGSabnzbdQbittorrent;
    switch (_serviceType) {
      case 'tautulli':
        return context.l10n.settingsEGTautulli;
      case 'tracearr':
        return context.l10n.settingsEGTracearr;
      case 'tdarr':
        return context.l10n.settingsEGTdarr;
      case 'audiobookshelf':
        return context.l10n.settingsEGHomeAudiobookshelf;
      case 'jellyfin':
        return context.l10n.settingsEGHomeJellyfin;
      case 'emby':
        return context.l10n.settingsEGHomeEmby;
      case 'plex':
        return context.l10n.settingsEGCantinaPlex;
      default:
        return context.l10n.settingsEGMovies4KMovies;
    }
  }

  /// qBittorrent's credential shape. Switching clears the other shape's
  /// fields so a save carries exactly one, which is what tells the server
  /// to drop the stored other.
  Widget _buildQbitAuthToggle() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SegmentedButton<_QbitAuth>(
          segments: [
            ButtonSegment(
              value: _QbitAuth.password,
              label: Text(context.l10n.settingsUsernamePassword),
            ),
            ButtonSegment(
              value: _QbitAuth.apiKey,
              label: Text(context.l10n.settingsApiKey),
            ),
          ],
          selected: {_qbitAuth},
          showSelectedIcon: false,
          onSelectionChanged: (selection) => setState(() {
            _qbitAuth = selection.first;
            if (_qbitAuth == _QbitAuth.apiKey) {
              _usernameController.clear();
              _passwordController.clear();
            } else {
              _apiKeyController.clear();
            }
          }),
        ),
        const SizedBox(height: 6),
        Text(
          _qbitAuth == _QbitAuth.apiKey
              ? context.l10n.settingsApiKeysNeedQbittorrent52OrNewer
              : context.l10n.settingsTheWebuiSignInWorksOnEveryQbittorrent,
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        ),
      ],
    );
  }

  String get _apiKeyHint {
    if (widget.isEditing && !_qbitSwitchingShape) {
      return context.l10n.settingsLeaveBlankToKeepExisting;
    }
    switch (_serviceType) {
      case 'qbittorrent':
        return context.l10n.settingsYourQbittorrentApiKeyOptionsWebui52;
      case 'sabnzbd':
        return context.l10n.settingsYourSabnzbdApiKey;
      case 'tautulli':
        return context.l10n.settingsYourTautulliApiKey;
      case 'tracearr':
        return context.l10n.settingsYourTracearrApiKeySettingsGeneralStartsWith;
      case 'tdarr':
        return context.l10n.settingsToolsApiKeysOnlyWhenAuthenticationIsEnabled;
      case 'chaptarr':
        return context.l10n.settingsYourChaptarrApiKey;
      case 'lidarr':
        return context.l10n.settingsYourLidarrApiKey;
      case 'audiobookshelf':
        return context.l10n.settingsYourAudiobookshelfAdministratorApiKey;
      case 'jellyfin':
        return context.l10n.settingsYourJellyfinApiKeyDashboardApiKeys;
      case 'emby':
        return context.l10n.settingsYourEmbyApiKeySettingsAdvancedApiKeys;
      default:
        return context.l10n.settingsYourRadarrSonarrApiKey;
    }
  }

  String get _defaultSubtitle {
    if (_newAssignments)
      return context
          .l10n.settingsPreferredRequestDestinationForUsersAssignedToThis;
    if (_isDownloadClient)
      return context.l10n.settingsUseThisAsTheDefaultDownloadClient;
    if (_isWatchHistory) {
      return context.l10n.settingsMessageUseThisAsTheDefaultValue1Instance(
          (_serviceLabel).toString());
    }
    return context.l10n.settingsUseThisAsTheDefaultForMediaRequests;
  }

  /// The server's own library kind, humanized. Jellyfin and Emby report
  /// 'movies', 'tvshows', 'music', 'books', 'homevideos', 'musicvideos',
  /// 'boxsets', 'photos', 'playlists'; a mixed movies-and-shows library
  /// reports none.
  String _collectionTypeLabel(String collectionType) {
    switch (collectionType.toLowerCase()) {
      case '':
        return context.l10n.settingsMixedContent;
      case 'movies':
        return context.l10n.settingsMovies;
      case 'tvshows':
        return context.l10n.settingsShows;
      case 'music':
        return context.l10n.settingsMusic;
      case 'musicvideos':
        return context.l10n.settingsMusicVideos;
      case 'homevideos':
        return context.l10n.settingsHomeVideosAndPhotos;
      case 'books':
        return context.l10n.settingsBooks;
      case 'boxsets':
        return context.l10n.settingsCollections;
      case 'photos':
        return context.l10n.settingsPhotos;
      case 'playlists':
        return context.l10n.settingsPlaylists;
      case 'livetv':
        return context.l10n.settingsLiveTv;
      default:
        return collectionType[0].toUpperCase() + collectionType.substring(1);
    }
  }

  /// The Plex credential: link a plex.tv account with a PIN (the token stays
  /// on the server; the form only ever holds the pin id), then pick which of
  /// the account's owned servers this instance shares.
  Widget _buildPlexAccountSection() {
    final linked = _plexHasCredential;
    final servers = _plexServers;
    Widget serverTile(PlexServerChoice server) => Material(
          type: MaterialType.transparency,
          child: ListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            leading: Icon(
              server.machineIdentifier == _plexMachineId
                  ? Icons.radio_button_checked
                  : Icons.radio_button_off,
              color: server.machineIdentifier == _plexMachineId
                  ? AppTheme.accent
                  : AppTheme.textSecondary,
            ),
            title: Text(server.name,
                style: const TextStyle(color: AppTheme.textPrimary)),
            onTap: () => _pickPlexServer(server.machineIdentifier),
          ),
        );
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                width: 38,
                height: 38,
                decoration: BoxDecoration(
                  color: AppTheme.accent.withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(10),
                ),
                child: const Icon(Icons.link, color: AppTheme.accent, size: 21),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Text(
                  context.l10n.settingsPlexAccount,
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Container(
                padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
                decoration: BoxDecoration(
                  color: (linked ? AppTheme.available : AppTheme.textSecondary)
                      .withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  linked
                      ? context.l10n.settingsLinked
                      : context.l10n.settingsNotLinked,
                  style: TextStyle(
                    color: linked ? AppTheme.available : AppTheme.textSecondary,
                    fontSize: 11,
                    fontWeight: FontWeight.w700,
                  ),
                ),
              ),
            ],
          ),
          const SizedBox(height: 12),
          if (_plexLinking) ...[
            Row(
              children: [
                SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(
                      strokeWidth: 2, color: AppTheme.accent),
                ),
                SizedBox(width: 12),
                Expanded(
                  child: Text(
                    context.l10n.settingsWaitingForApprovalSignInOnThePlex,
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                  ),
                ),
              ],
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 12,
              runSpacing: 8,
              children: [
                OutlinedButton(
                  onPressed: () => _checkPlexLink(),
                  child: Text(context.l10n.settingsIVeApprovedCheckNow),
                ),
                if (_plexLinkUrl != null)
                  OutlinedButton(
                    onPressed: () => launchUrl(Uri.parse(_plexLinkUrl!),
                        mode: LaunchMode.externalApplication),
                    child: Text(context.l10n.settingsReopenPlexTv),
                  ),
                TextButton(
                  onPressed: _cancelPlexLink,
                  child: Text(context.l10n.settingsCancel),
                ),
              ],
            ),
          ] else ...[
            Text(
              linked
                  ? (_plexAccount.isNotEmpty
                      ? context.l10n.settingsMessageLinkedAsValue1(
                          (_plexAccount).toString())
                      : context.l10n.settingsALinkedAccountIsStoredRelinkToUse)
                  : context.l10n.settingsLinkThePlexTvAccountThatOwnsThe,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
            const SizedBox(height: 8),
            OutlinedButton.icon(
              onPressed: _beginPlexLink,
              icon: const Icon(Icons.link, size: 18),
              label: Text(linked
                  ? context.l10n.settingsRelinkPlexAccount
                  : context.l10n.settingsLinkPlexAccount),
            ),
          ],
          if (linked) ...[
            const SizedBox(height: 16),
            Text(
              context.l10n.settingsServerToShare,
              style: TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 13,
                  fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 4),
            if (_plexServersLoading)
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 8),
                child: SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2),
                ),
              )
            else if (_plexServersError != null)
              Row(
                children: [
                  Expanded(
                    child: Text(_plexServersError!,
                        style: const TextStyle(
                            color: AppTheme.error, fontSize: 13)),
                  ),
                  TextButton(
                    onPressed: _loadPlexServers,
                    child: Text(context.l10n.settingsRetry),
                  ),
                ],
              )
            else if (servers == null)
              const SizedBox.shrink()
            else if (servers.isEmpty)
              Text(
                context.l10n.settingsThisAccountOwnsNoPlexMediaServerThe,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              )
            else
              for (final server in servers) serverTile(server),
            if (servers != null &&
                _plexMachineId.isNotEmpty &&
                !servers.any((s) => s.machineIdentifier == _plexMachineId))
              serverTile(PlexServerChoice(
                  name: context.l10n.settingsMessageStoredServerValue1(
                      (_plexMachineId).toString()),
                  machineIdentifier: _plexMachineId)),
          ],
        ],
      ),
    );
  }

  /// Plex: whether sharing a Plex email is enough to be granted this server.
  Widget _buildPlexAutoApproveTile() {
    return SwitchListTile(
      title: Text(context.l10n.settingsAutoApproveAccessRequests,
          style: TextStyle(color: AppTheme.textPrimary)),
      subtitle: Text(
        context.l10n.settingsAnyoneWhoSharesAPlexEmailIsGranted,
        style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
      ),
      value: _plexAutoApprove,
      onChanged: (value) => setState(() {
        _plexAutoApprove = value;
        _mediaServerConfigDirty = true;
      }),
      contentPadding: EdgeInsets.zero,
    );
  }

  /// Which libraries a new account on this media server may see. Drawn only
  /// from a live read, so before one succeeds the section says what would
  /// load it instead of guessing; a stored id the server no longer reports
  /// stays checked as "Unknown library" until the admin drops it.
  Widget _buildSharedLibrariesSection() {
    final libraries = _mediaServerLibraries;
    final selectedCount = _selectedLibraryIds.length;
    final unknownIds = libraries == null
        ? const <String>[]
        : _selectedLibraryIds
            .where((id) => !libraries.any((library) => library.id == id))
            .toList(growable: false);
    final statusBadge = Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(
        color: (selectedCount == 0 ? AppTheme.textSecondary : AppTheme.accent)
            .withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        selectedCount == 0
            ? context.l10n.settingsAll
            : context.l10n
                .settingsMessageValue1Selected((selectedCount).toString()),
        style: TextStyle(
          color: selectedCount == 0 ? AppTheme.textSecondary : AppTheme.accent,
          fontSize: 11,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
    Widget titleRow({required bool includeStatus}) => Row(
          children: [
            Container(
              width: 38,
              height: 38,
              decoration: BoxDecoration(
                color: AppTheme.accent.withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(10),
              ),
              child: const Icon(
                Icons.video_library_outlined,
                color: AppTheme.accent,
                size: 21,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                _isAbs
                    ? context.l10n.settingsDefaultLibraries
                    : context.l10n.settingsSharedLibraries,
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            if (includeStatus) statusBadge,
          ],
        );
    // Each tile gets its own transparent Material: the section's decorated
    // container sits between the tiles and the page Material, and Flutter
    // asserts (in debug builds) that a ListTile's ink would be hidden there.
    Widget libraryTile({
      required String id,
      required String title,
      required String subtitle,
    }) =>
        Material(
          type: MaterialType.transparency,
          child: CheckboxListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            controlAffinity: ListTileControlAffinity.leading,
            activeColor: AppTheme.accent,
            title: Text(title,
                style: const TextStyle(color: AppTheme.textPrimary)),
            subtitle: Text(subtitle,
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 12)),
            value: _selectedLibraryIds.contains(id),
            onChanged: (checked) => setState(() {
              if (checked == true) {
                _selectedLibraryIds.add(id);
              } else {
                _selectedLibraryIds.remove(id);
              }
              _mediaServerConfigDirty = true;
            }),
          ),
        );
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          LayoutBuilder(
            builder: (context, constraints) {
              final largeText = MediaQuery.textScalerOf(context).scale(1) > 1.3;
              if (constraints.maxWidth < 300 || largeText) {
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    titleRow(includeStatus: false),
                    const SizedBox(height: 8),
                    statusBadge,
                  ],
                );
              }
              return titleRow(includeStatus: true);
            },
          ),
          const SizedBox(height: 10),
          Text(
            _isAbs
                ? context.l10n
                    .settingsChooseTheDefaultLibrariesForAccountsCantinarrCreates
                : context.l10n
                    .settingsOptionalChooseWhichLibrariesTheseAccountsCanSee,
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 12),
          if (_mediaServerLibrariesLoading)
            const Center(
              child: Padding(
                padding: EdgeInsets.symmetric(vertical: 10),
                child: SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: AppTheme.accent,
                  ),
                ),
              ),
            )
          else if (_mediaServerLibrariesError != null)
            _MediaMappingNotice(
              icon: Icons.sync_problem_outlined,
              message: _mediaServerLibrariesError!,
              action: TextButton(
                onPressed: _loadMediaServerLibraries,
                child: Text(context.l10n.settingsRetry),
              ),
            )
          else if (libraries == null)
            _MediaMappingNotice(
              icon: Icons.wifi_tethering,
              message:
                  context.l10n.settingsTestTheConnectionToLoadTheLibrariesThis,
            )
          else ...[
            if (libraries.isEmpty)
              _MediaMappingNotice(
                icon: Icons.video_library_outlined,
                message: context
                    .l10n.settingsThisServerReportsNoLibrariesYetEveryLibrary,
              ),
            for (final library in libraries)
              libraryTile(
                id: library.id,
                title: library.name,
                subtitle: _collectionTypeLabel(library.collectionType),
              ),
            for (final id in unknownIds)
              libraryTile(
                id: id,
                title: context.l10n.settingsUnknownLibrary,
                subtitle:
                    context.l10n.settingsNoLongerReportedByTheServerUncheckTo,
              ),
          ],
        ],
      ),
    );
  }

  Widget _buildMediaDownloadsSection() {
    final roots = _mediaRoots;
    final canAdd = _mediaMappingsLoaded &&
        _mediaMappingApiSupported &&
        (roots?.isNotEmpty ?? false);
    final mappingCount = _mediaPathMappings.length;
    final statusBadge = Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(
        color: (mappingCount == 0 ? AppTheme.textSecondary : AppTheme.accent)
            .withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(999),
      ),
      child: Text(
        mappingCount == 0
            ? context.l10n.settingsOff
            : '$mappingCount '
                '${mappingCount == 1 ? 'mapping' : 'mappings'}',
        style: TextStyle(
          color: mappingCount == 0 ? AppTheme.textSecondary : AppTheme.accent,
          fontSize: 11,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
    Widget titleRow({required bool includeStatus}) => Row(
          children: [
            Container(
              width: 38,
              height: 38,
              decoration: BoxDecoration(
                color: AppTheme.accent.withValues(alpha: 0.12),
                borderRadius: BorderRadius.circular(10),
              ),
              child: const Icon(
                Icons.download_for_offline_outlined,
                color: AppTheme.accent,
                size: 21,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                context.l10n.settingsMediaDownloads,
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            if (includeStatus) statusBadge,
          ],
        );
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          LayoutBuilder(
            builder: (context, constraints) {
              final largeText = MediaQuery.textScalerOf(context).scale(1) > 1.3;
              if (constraints.maxWidth < 300 || largeText) {
                return Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    titleRow(includeStatus: false),
                    const SizedBox(height: 8),
                    statusBadge,
                  ],
                );
              }
              return titleRow(includeStatus: true);
            },
          ),
          const SizedBox(height: 10),
          Text(
            context.l10n
                .settingsMessageOptionalMapEachLibraryPathReportedByValue1(
                    (_serviceLabel).toString()),
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 12,
              height: 1.4,
            ),
          ),
          const SizedBox(height: 12),
          if (roots == null && _mediaRootsError == null)
            const Center(
              child: Padding(
                padding: EdgeInsets.symmetric(vertical: 10),
                child: SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(
                    strokeWidth: 2,
                    color: AppTheme.accent,
                  ),
                ),
              ),
            ),
          if (_mediaRootsError != null)
            _MediaMappingNotice(
              icon: Icons.sync_problem_outlined,
              message: _mediaRootsError!,
              action: TextButton(
                onPressed: () {
                  setState(() => _mediaRootsError = null);
                  _loadMediaRoots();
                },
                child: Text(context.l10n.settingsRetry),
              ),
            ),
          if (_mediaMappingsError != null)
            _MediaMappingNotice(
              icon: Icons.sync_problem_outlined,
              message: _mediaMappingsError!,
              action: widget.isEditing
                  ? TextButton(
                      onPressed: () {
                        setState(() => _mediaMappingsError = null);
                        _loadDetails();
                      },
                      child: Text(context.l10n.settingsRetry),
                    )
                  : null,
            ),
          if (roots != null && roots.isEmpty)
            _MediaMappingNotice(
              icon: Icons.folder_off_outlined,
              message: context
                  .l10n.settingsNoServerMediaFoldersAreAvailableMountThem,
            ),
          if (roots != null && roots.isNotEmpty) ...[
            Text(
              context.l10n.settingsMessageAllowedCantinarrValue1(
                  (roots.length == 1
                          ? context.l10n.settingsRoot
                          : context.l10n.settingsRoots)
                      .toString()),
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 11,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 5),
            for (final root in roots)
              Padding(
                padding: const EdgeInsets.only(bottom: 3),
                child: Row(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    const Padding(
                      padding: EdgeInsets.only(top: 3),
                      child: Icon(
                        Icons.folder_open_outlined,
                        size: 14,
                        color: AppTheme.textSecondary,
                      ),
                    ),
                    const SizedBox(width: 6),
                    Expanded(
                      child: SelectableText(
                        root,
                        style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontSize: 12,
                        ),
                      ),
                    ),
                  ],
                ),
              ),
            const SizedBox(height: 10),
          ],
          if (widget.isEditing && canAdd) ...[
            if (_arrRootFoldersError != null)
              _MediaMappingNotice(
                icon: Icons.sync_problem_outlined,
                message: _arrRootFoldersError!,
                action: TextButton(
                  onPressed: () {
                    setState(() => _arrRootFoldersError = null);
                    _loadArrRootFolders();
                  },
                  child: Text(context.l10n.settingsRetry),
                ),
              ),
            if (_arrRootFoldersKnown && _arrRootFolders.isNotEmpty) ...[
              Text(
                context.l10n.settingsMessageReportedByValue1TapToMap(
                    (_serviceLabel).toString()),
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 11,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(height: 6),
              Wrap(
                spacing: 6,
                runSpacing: 6,
                children: [
                  for (final folder in _arrRootFolders)
                    ActionChip(
                      avatar: const Icon(
                        Icons.folder_open_outlined,
                        size: 14,
                        color: AppTheme.accent,
                      ),
                      label: Text(folder),
                      labelStyle: const TextStyle(
                        color: AppTheme.textPrimary,
                        fontSize: 12,
                      ),
                      onPressed: () => _useReportedArrPath(folder),
                    ),
                ],
              ),
              const SizedBox(height: 10),
            ],
          ],
          if (_mediaMappingsLoaded) ...[
            if (_mediaPathMappings.isEmpty)
              Padding(
                padding: EdgeInsets.symmetric(vertical: 6),
                child: Text(
                  context.l10n.settingsNoPathsMappedDownloadsAreOffForThis,
                  style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 12,
                  ),
                ),
              ),
            for (var i = 0; i < _mediaPathMappings.length; i++) ...[
              _buildMediaPathMappingCard(i),
              if (i != _mediaPathMappings.length - 1)
                const SizedBox(height: 10),
            ],
            const SizedBox(height: 10),
            OutlinedButton.icon(
              onPressed: canAdd ? _addMediaPathMapping : null,
              icon: const Icon(Icons.add_link_rounded, size: 18),
              label: Text(context.l10n.settingsAddPath),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildMediaPathMappingCard(int index) {
    final mapping = _mediaPathMappings[index];
    final rootHint = _mediaRoots?.isNotEmpty == true ? _mediaRoots!.first : '';
    return Container(
      key: ObjectKey(mapping),
      padding: const EdgeInsets.fromLTRB(12, 8, 8, 12),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  context.l10n
                      .settingsMessagePathMappingValue1((index + 1).toString()),
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              IconButton(
                tooltip: context.l10n.settingsRemovePathMapping,
                visualDensity: VisualDensity.compact,
                onPressed: () => _removeMediaPathMapping(index),
                icon: const Icon(
                  Icons.close_rounded,
                  size: 18,
                  color: AppTheme.textSecondary,
                ),
              ),
            ],
          ),
          LayoutBuilder(
            builder: (context, constraints) {
              final source = TextField(
                controller: mapping.arrPath,
                decoration: InputDecoration(
                  labelText: context.l10n
                      .settingsMessageValue1Path((_serviceLabel).toString()),
                  hintText: _isChaptarr ? '/ebooks' : '/media/library',
                ),
                autocorrect: false,
              );
              final target = TextField(
                controller: mapping.cantinarrPath,
                decoration: InputDecoration(
                  labelText: context.l10n.settingsCantinarrPath,
                  hintText: rootHint.isEmpty ? '/media/library' : rootHint,
                ),
                autocorrect: false,
              );
              final wide = constraints.maxWidth >= 560;
              final arrow = Container(
                width: 34,
                height: 34,
                decoration: BoxDecoration(
                  color: AppTheme.accent.withValues(alpha: 0.1),
                  shape: BoxShape.circle,
                ),
                child: Icon(
                  wide
                      ? Icons.arrow_forward_rounded
                      : Icons.arrow_downward_rounded,
                  size: 18,
                  color: AppTheme.accent,
                ),
              );
              if (wide) {
                return Row(
                  crossAxisAlignment: CrossAxisAlignment.center,
                  children: [
                    Expanded(child: source),
                    Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 10),
                      child: arrow,
                    ),
                    Expanded(child: target),
                  ],
                );
              }
              return Column(
                children: [
                  source,
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    child: arrow,
                  ),
                  target,
                ],
              );
            },
          ),
          if (_arrRootFoldersKnown && _arrRootFolders.isNotEmpty)
            ValueListenableBuilder<TextEditingValue>(
              valueListenable: mapping.arrPath,
              builder: (context, value, _) {
                final text = value.text.trim();
                final unrelated = text.isNotEmpty &&
                    !_arrRootFolders.any(
                      (folder) => arrPathRelatesToReportedRoot(text, folder),
                    );
                if (!unrelated) return const SizedBox.shrink();
                return Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Padding(
                        padding: EdgeInsets.only(top: 1),
                        child: Icon(
                          Icons.warning_amber_rounded,
                          size: 15,
                          color: AppTheme.warning,
                        ),
                      ),
                      const SizedBox(width: 6),
                      Expanded(
                        child: Text(
                          context.l10n
                              .settingsMessageValue1DoesNotReportAnyLibraryFolderUnder(
                                  (_serviceLabel).toString(),
                                  (_serviceLabel).toString()),
                          style: const TextStyle(
                            color: AppTheme.warning,
                            fontSize: 11.5,
                            height: 1.35,
                          ),
                        ),
                      ),
                    ],
                  ),
                );
              },
            ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) => UnsavedChangesGuard(
        hasChanges: () => _hasUnsavedChanges,
        isSaving: _isSaving,
        child: _buildPage(context),
      );

  Widget _buildPage(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.isEditing
            ? context.l10n.settingsEditInstance
            : context.l10n.settingsAddInstance),
        actions: [
          if (widget.isEditing)
            IconButton(
              icon: const Icon(Icons.delete_outline, color: AppTheme.error),
              onPressed: _delete,
            ),
        ],
      ),
      body: CenteredContent(
          child: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          if (widget.isEditing && _isMediaServer) ...[
            Align(
              alignment: Alignment.centerLeft,
              child: OutlinedButton.icon(
                onPressed: _isSaving ? null : _openUserImport,
                icon: const Icon(Icons.group_add_outlined),
                label: Text(context.l10n.settingsImportUsers),
              ),
            ),
            Padding(
              padding: EdgeInsets.only(top: 4, bottom: 16),
              child: Text(
                context
                    .l10n.settingsLinkExistingMediaAccountsToCantinarrUsersThen,
                style: TextStyle(color: AppTheme.textSecondary),
              ),
            ),
          ],
          // Service type (only for new instances)
          if (!widget.isEditing) ...[
            Text(context.l10n.settingsServiceType,
                style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600)),
            const SizedBox(height: 8),
            // With 7 service types a segmented control no longer fits on a
            // phone, so use a dropdown instead.
            DropdownButtonFormField<String>(
              initialValue: _serviceType,
              isExpanded: true,
              dropdownColor: AppTheme.surfaceVariant,
              items: [
                // The prompted form opens on this disabled placeholder: the
                // checklist row named a category (download clients), not a
                // member, and a real value must be chosen before saving.
                if (widget.serviceTypePrompt != null)
                  DropdownMenuItem<String>(
                    value: '',
                    enabled: false,
                    child: Text(widget.serviceTypePrompt!),
                  ),
                ..._serviceTypes.map(
                  (t) => DropdownMenuItem(
                    value: t.$1,
                    child: Text(t.$2),
                  ),
                ),
              ],
              onChanged: (value) {
                if (value == null) return;
                setState(() {
                  _serviceType = value;
                  _absPolicies = {};
                  _absEditedPolicies.clear();
                  _absAccessLoaded = false;
                  _testResult = null;
                  // A username typed under another type has no field on
                  // a password-only type, so it must not ride into the
                  // save the way the qBittorrent toggle drops hidden
                  // fields.
                  if (_passwordOnly) _usernameController.clear();
                  // The selection and pins belong to the previous type.
                  _pins = const {};
                  _assignedUserIds = <int>{};
                  _savedAssignedUserIds = <int>{};
                  // So does whatever a media server reported.
                  _mediaServerLibraries = null;
                  _mediaServerLibrariesError = null;
                  _mediaServerLibrariesLoading = false;
                  _selectedLibraryIds = <String>{};
                  _availabilityScan = null;
                  _availabilityScanDirty = false;
                  // And the Plex link, which belongs to nothing else.
                  _plexPollTimer?.cancel();
                  _plexLinking = false;
                  _plexPinId = null;
                  _plexLinkUrl = null;
                  _plexAccount = '';
                  _plexServers = null;
                  _plexServersError = null;
                  _plexMachineId = '';
                  _plexAutoApprove = false;
                  if (value == 'plex' &&
                      _publicAddressController.text.trim().isEmpty) {
                    // Everyone signs in to Plex at the same place.
                    _publicAddressController.text = 'https://app.plex.tv';
                  }
                  _applyAutoDefault();
                });
                _loadPins();
              },
            ),
            const SizedBox(height: 24),
          ],

          TextField(
            controller: _nameController,
            decoration: InputDecoration(
              labelText: context.l10n.settingsName,
              hintText: _nameHint,
            ),
          ),
          const SizedBox(height: 16),

          // Plex is reached through plex.tv, never a URL the admin types.
          if (!_isPlex) ...[
            TextField(
              controller: _urlController,
              decoration: InputDecoration(
                labelText: 'URL',
                hintText: _urlHint,
                helperText: context
                    .l10n.settingsReachedFromTheCantinarrServerNotFromThis,
              ),
              keyboardType: TextInputType.url,
            ),
            const SizedBox(height: 16),
          ],

          // Credentials need a real type before they can ask for the right
          // shape (API key vs username/password), so the prompted form shows
          // nothing here until one is picked.
          //
          // NZBGet authenticates with username/password, and so do
          // Transmission and ruTorrent, optionally, since either may run
          // with authentication off; Deluge with only its web UI password;
          // qBittorrent with either username/password or, on 5.2 and newer,
          // an API key, chosen by the toggle; Plex links a plex.tv account
          // with a PIN; everything else uses an API key. Credentials are
          // write-only: when editing, blank keeps the existing value.
          if (_serviceType == 'qbittorrent') ...[
            _buildQbitAuthToggle(),
            const SizedBox(height: 16),
          ],
          if (_serviceTypeUnchosen)
            const SizedBox.shrink()
          else if (_isPlex)
            _buildPlexAccountSection()
          else if (_usesUserPass) ...[
            if (!_passwordOnly) ...[
              TextField(
                controller: _usernameController,
                decoration: InputDecoration(
                  labelText: _credentialsOptional
                      ? context.l10n.settingsOptionalUsername
                      : context.l10n.settingsUsername,
                  hintText: _credentialsOptional
                      ? context.l10n.settingsOnlyIfAuthenticationIsEnabled
                      : context.l10n.settingsWebUiUsername,
                ),
                autocorrect: false,
              ),
              const SizedBox(height: 16),
            ],
            TextField(
              controller: _passwordController,
              decoration: InputDecoration(
                labelText: _passwordOnly
                    ? context.l10n.settingsWebUiPassword
                    : (_credentialsOptional
                        ? context.l10n.settingsPasswordOptional
                        : context.l10n.settingsPassword),
                hintText: widget.isEditing && !_qbitSwitchingShape
                    ? context.l10n.settingsLeaveBlankToKeepExisting
                    : (_passwordOnly
                        ? context.l10n.settingsThePasswordDelugeSWebUiAsksFor
                        : _credentialsOptional
                            ? context.l10n.settingsOnlyIfAuthenticationIsEnabled
                            : context.l10n.settingsWebUiPassword),
              ),
              obscureText: true,
            ),
          ] else
            TextField(
              controller: _apiKeyController,
              enabled: !(_isTdarr && _clearTdarrKey),
              decoration: InputDecoration(
                labelText: _isTdarr
                    ? context.l10n.settingsApiKeyOptional
                    : context.l10n.settingsApiKey2,
                hintText: _apiKeyHint,
              ),
              obscureText: true,
            ),
          if (_isTdarr) ...[
            const SizedBox(height: 12),
            Text(context.l10n.settingsUseTheTdarrServerApiUrlNormallyPort,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 12)),
            if (widget.isEditing && _tdarrHasKey)
              CheckboxListTile(
                contentPadding: EdgeInsets.zero,
                title: Text(context.l10n.settingsRemoveSavedApiKey),
                subtitle: Text(context
                    .l10n.settingsForATdarrServerWithAuthenticationDisabled),
                value: _clearTdarrKey,
                onChanged: (value) => setState(() {
                  _clearTdarrKey = value ?? false;
                  if (_clearTdarrKey) _apiKeyController.clear();
                  _testSucceeded = false;
                  _testResult = null;
                }),
              ),
          ],
          // User links use an address the admin chose explicitly, either
          // entered here or copied from the connection URL.
          if (_isMediaServer) ...[
            const SizedBox(height: 16),
            TextField(
              controller: _publicAddressController,
              decoration: InputDecoration(
                labelText: context.l10n.settingsAddressUsersOpen,
                hintText: 'https://$_serviceType.example.com',
                helper: Text(context.l10n
                    .settingsMessageTheAddressUsersCanReachInABrowser(
                        (_serviceType == 'audiobookshelf'
                                ? context.l10n.settingsListen
                                : context.l10n.settingsWatch)
                            .toString())),
              ),
              keyboardType: TextInputType.url,
              autocorrect: false,
            ),
            if (!_isPlex)
              ValueListenableBuilder<TextEditingValue>(
                valueListenable: _urlController,
                builder: (context, value, _) => Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton.icon(
                    onPressed: value.text.trim().isEmpty
                        ? null
                        : () =>
                            _publicAddressController.text = value.text.trim(),
                    icon: const Icon(Icons.copy),
                    label: Text(context.l10n.settingsUseSameUrl),
                  ),
                ),
              ),
            const SizedBox(height: 24),
            _buildSharedLibrariesSection(),
            if (VideoApps.serviceTypes.contains(_serviceType) &&
                ref.watch(authProvider).valueOrNull?.connection?.mediaLibraryAvailability == true) ...[
              const SizedBox(height: 24),
              AvailabilityScanSection(
                key: ValueKey('availability-$_serviceType'),
                instanceId: widget.instanceId,
                value: _availabilityScan ?? const AvailabilityScanConfig(),
                initialLibraries: _mediaServerLibraries,
                unsaved: _availabilityScanDirty || _mediaServerConfigDirty || _detailsDraft.hasChanges(_detailValues),
                onChanged: (value) => setState(() {
                  _availabilityScan = value;
                  _availabilityScanDirty = true;
                  _mediaServerConfigDirty = true;
                }),
              ),
            ],
            if (VideoApps.serviceTypes.contains(_serviceType)) ...[
              const SizedBox(height: 24),
              Text(context.l10n.settingsDefaultVideoApp,
                  style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              Text(context.l10n.settingsUsedOnIphoneAndIpadUnlessAUser),
              const SizedBox(height: 16),
              VideoAppField(
                serviceType: _serviceType,
                value: _videoApps ?? const VideoApps(),
                inheritDefaults: false,
                onChanged: (value) => setState(() {
                  _videoApps = value;
                  _mediaServerConfigDirty = true;
                }),
              ),
            ],
            if (_serviceType == 'audiobookshelf') ...[
              const SizedBox(height: 24),
              Text(context.l10n.settingsDefaultListeningApps,
                  style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 8),
              Text(context.l10n.settingsUsedUnlessAUserChoosesTheirOwnApps),
              const SizedBox(height: 16),
              ListeningAppFields(
                value: _listeningApps ?? const ListeningApps(),
                inheritDefaults: false,
                onChanged: (value) => setState(() {
                  _listeningApps = value;
                  _mediaServerConfigDirty = true;
                }),
              ),
            ],
            if (_isPlex) ...[
              const SizedBox(height: 16),
              _buildPlexAutoApproveTile(),
            ],
          ],
          if (_supportsMediaDownloads) ...[
            const SizedBox(height: 24),
            _buildMediaDownloadsSection(),
          ],
          const SizedBox(height: 16),

          // Media servers and legacy Chaptarr/Lidarr have no global default:
          // instances are assigned directly to users below instead. The
          // prompted form hides the toggle entirely until a type is chosen.
          if (!_serviceTypeUnchosen && !_grantOnly)
            SwitchListTile(
              title: Text(context.l10n.settingsDefaultInstance,
                  style: TextStyle(color: AppTheme.textPrimary)),
              subtitle: Text(_defaultSubtitle,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 13)),
              value: _isDefault,
              onChanged: (value) => setState(() => _isDefault = value),
              activeTrackColor: AppTheme.accent,
            ),

          if (_newAssignments) ...[
            SwitchListTile(
              title: Text(context.l10n.settingsAutomaticallyAddNewUsers),
              subtitle: Text(context
                  .l10n.settingsAssignAccountsCreatedAfterThisSettingIsSaved),
              value: _autoAddUsers,
              onChanged:
                  _isSaving || (widget.isEditing && !_assignmentDetailsLoaded)
                      ? null
                      : (value) => setState(() => _autoAddUsers = value),
            ),
            ListTile(
              title: Text(context.l10n.settingsManageUsers2),
              subtitle: Text(!widget.isEditing
                  ? context.l10n.settingsSaveThisInstanceToAssignExistingUsers
                  : _userSelectError != null
                      ? context
                          .l10n.settingsCouldNotLoadAssignmentCountOpenToRetry
                      : _users == null
                          ? context.l10n.settingsLoadingAssignments
                          : context.l10n.settingsMessageValue1AssignedUsers(
                              (_users!
                                      .where((u) =>
                                          _assignedUserIds.contains(u.id))
                                      .length)
                                  .toString())),
              trailing: const Icon(Icons.chevron_right),
              onTap: !widget.isEditing || _isSaving
                  ? null
                  : () async {
                      await Navigator.of(context).push(MaterialPageRoute<void>(
                          builder: (_) => InstanceUsersScreen(
                              instanceId: widget.instanceId!,
                              instanceName: _nameController.text.trim())));
                      if (mounted) await _loadPins();
                    },
            ),
          ],
          if (_showUserSelect) ..._buildUserSelect(),

          if (_supportsRequesterTags)
            SwitchListTile(
              title: Text(context.l10n.settingsTagRequestsWithRequester),
              subtitle: Text(
                context.l10n
                    .settingsMessageTagNewRequestsOnValue1ExistingTagsStay(
                        (switch (_serviceType) {
                  'chaptarr' => context.l10n
                      .settingsTheChaptarrAuthorSeparatelyForEbooksAndAudiobooks,
                  'lidarr' =>
                    context.l10n.settingsTheLidarrArtistSharedByAllTheirAlbums,
                  'sonarr' => context.l10n.settingsTheSonarrSeries,
                  _ => context.l10n.settingsTheRadarrMovie,
                })
                            .toString()),
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13),
              ),
              value: _tagRequests,
              onChanged: _isSaving || (widget.isEditing && !_tagRequestsLoaded)
                  ? null
                  : (value) => setState(() => _tagRequests = value),
              activeTrackColor: AppTheme.accent,
            ),

          const SizedBox(height: 24),

          // Test connection button — the server performs the check for every
          // service type, so it works for URLs only the server can resolve.
          OutlinedButton.icon(
            onPressed: _isTesting ? null : _testConnection,
            icon: _isTesting
                ? const SizedBox(
                    width: 16,
                    height: 16,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: AppTheme.accent),
                  )
                : const Icon(Icons.wifi_tethering),
            label: Text(context.l10n.settingsTestConnection),
          ),
          if (_testResult != null) ...[
            const SizedBox(height: 8),
            Text(
              _testResult!,
              style: TextStyle(
                color: _testSucceeded ? AppTheme.available : AppTheme.error,
                fontSize: 13,
              ),
              textAlign: TextAlign.center,
            ),
          ],

          const SizedBox(height: 32),

          // Save button
          ElevatedButton(
            onPressed: _isSaving || _creationComplete ? null : _save,
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.accent,
              foregroundColor: Colors.black,
              padding: const EdgeInsets.symmetric(vertical: 16),
              shape: RoundedRectangleBorder(
                  borderRadius: BorderRadius.circular(12)),
            ),
            child: _isSaving
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: Colors.black),
                  )
                : Text(_creationComplete
                    ? context.l10n.settingsInstanceCreated
                    : widget.isEditing
                        ? context.l10n.settingsSaveChanges
                        : context.l10n.settingsAddInstance),
          ),

          // Hardcover (Chaptarr, editing only). The token is verified by the
          // server and stored encrypted there; it is never read back.
          if (widget.isEditing && _isChaptarr && _hardcoverSupported) ...[
            const SizedBox(height: 32),
            const Text('Hardcover',
                style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600)),
            const SizedBox(height: 8),
            Text(
              _hardcoverReconnect
                  ? context.l10n.settingsHardcoverNeedsYouToSignInAgainReconnect
                  : _hardcoverConnected
                      ? context.l10n
                          .settingsMessageHardcoverIsConnectedValue1YouCanReplaceThis(
                              (_hardcoverMethod == 'oauth'
                                      ? context.l10n.settingsWithOauth
                                      : context.l10n.settingsWithAnApiToken)
                                  .toString())
                      : context.l10n
                          .settingsConnectHardcoverToShowTrendingBooksInCantinarr,
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
            const SizedBox(height: 8),
            if (!_hardcoverOAuthAvailable || _hardcoverUseToken) ...[
              TextField(
                controller: _hardcoverController,
                enabled: !_isSavingHardcover,
                obscureText: true,
                enableSuggestions: false,
                autocorrect: false,
                decoration: InputDecoration(
                  labelText: context.l10n.settingsHardcoverApiToken,
                  hintText: _hardcoverConnected
                      ? context.l10n.settingsConnectedPasteATokenToReplaceIt
                      : context.l10n.settingsPasteTheTokenFromHardcover,
                  helperText: context
                      .l10n.settingsHardcoverSettingsApiAllowPublicCatalogReads,
                  helperMaxLines: 3,
                ),
              ),
              const SizedBox(height: 8),
            ],
            Wrap(spacing: 8, runSpacing: 8, children: [
              OutlinedButton.icon(
                onPressed: _isSavingHardcover
                    ? null
                    : _hardcoverOAuthAvailable && !_hardcoverUseToken
                        ? _connectHardcover
                        : _saveHardcoverToken,
                icon: _isSavingHardcover
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(
                            strokeWidth: 2, color: AppTheme.accent))
                    : const Icon(Icons.link),
                label: Text(_hardcoverOAuthAvailable && !_hardcoverUseToken
                    ? _hardcoverReconnect
                        ? context.l10n.settingsReconnectHardcover
                        : _hardcoverConnected
                            ? context.l10n.settingsReplaceHardcoverConnection
                            : context.l10n.settingsConnectHardcover
                    : _hardcoverConnected
                        ? context.l10n.settingsReplaceHardcoverToken
                        : context.l10n.settingsConnectHardcover),
              ),
              if (_hardcoverConnected)
                TextButton(
                    onPressed: _isSavingHardcover ? null : _clearHardcoverToken,
                    child: Text(context.l10n.settingsDisconnect)),
            ]),
            if (_hardcoverOAuthAvailable)
              Align(
                  alignment: Alignment.centerLeft,
                  child: TextButton(
                    onPressed: _isSavingHardcover
                        ? null
                        : () => setState(
                            () => _hardcoverUseToken = !_hardcoverUseToken),
                    child: Text(_hardcoverUseToken
                        ? context.l10n.settingsSignInWithHardcoverInstead
                        : context.l10n.settingsUseAnApiTokenInstead),
                  )),
            if (_hardcoverResult != null) ...[
              const SizedBox(height: 8),
              Text(
                _hardcoverResult!,
                style: TextStyle(
                  color: _hardcoverResultColor,
                  fontSize: 12,
                ),
                textAlign: TextAlign.center,
              ),
            ],
          ],

          // Webhook setup (source instances, editing only). Cantinarr installs
          // its own Connect record; the callback credential never reaches the
          // app or clipboard.
          if (widget.isEditing && _supportsWebhook) ...[
            const SizedBox(height: 32),
            Text(context.l10n.settingsInstantUpdates,
                style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600)),
            const SizedBox(height: 8),
            Text(
              context.l10n.settingsMessageCantinarrCanCreateOrRefreshItsConnectWebhook(
                  (_serviceLabel).toString(),
                  (_isChaptarr
                          ? context.l10n
                              .settingsBooksThatFinishDownloadingAreAnnouncedTheMoment
                          : context.l10n
                              .settingsImportsDeletesAndAddsMadeThereWillReach)
                      .toString()),
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
            const SizedBox(height: 8),
            OutlinedButton.icon(
              onPressed: _isConfiguringWebhook ? null : _configureWebhook,
              icon: _isConfiguringWebhook
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(
                          strokeWidth: 2, color: AppTheme.accent),
                    )
                  : const Icon(Icons.sync),
              label: Text(context.l10n.settingsConfigureInstantUpdates),
            ),
            if (_webhookResult != null) ...[
              const SizedBox(height: 8),
              Text(
                _webhookResult!,
                style: TextStyle(
                  color: _webhookResultColor,
                  fontSize: 12,
                ),
                textAlign: TextAlign.center,
              ),
            ],
          ],
        ],
      )),
    );
  }
}

class _MediaPathMappingFields {
  final TextEditingController arrPath;
  final TextEditingController cantinarrPath;

  _MediaPathMappingFields({
    String arrPath = '',
    String cantinarrPath = '',
    required VoidCallback onChanged,
  })  : arrPath = TextEditingController(text: arrPath),
        cantinarrPath = TextEditingController(text: cantinarrPath) {
    this.arrPath.addListener(onChanged);
    this.cantinarrPath.addListener(onChanged);
  }

  factory _MediaPathMappingFields.fromMapping(
    MediaPathMapping mapping, {
    required VoidCallback onChanged,
  }) =>
      _MediaPathMappingFields(
        arrPath: mapping.arrPath,
        cantinarrPath: mapping.cantinarrPath,
        onChanged: onChanged,
      );

  void dispose() {
    arrPath.dispose();
    cantinarrPath.dispose();
  }
}

class _MediaMappingNotice extends StatelessWidget {
  final IconData icon;
  final String message;
  final Widget? action;

  const _MediaMappingNotice({
    required this.icon,
    required this.message,
    this.action,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(12, 9, 8, 9),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(9),
        border: Border.all(color: AppTheme.border),
      ),
      child: Row(
        children: [
          Icon(icon, size: 18, color: AppTheme.textSecondary),
          const SizedBox(width: 9),
          Expanded(
            child: Text(
              message,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                height: 1.35,
              ),
            ),
          ),
          if (action != null) action!,
        ],
      ),
    );
  }
}
