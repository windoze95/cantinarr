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
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/instance_api_service.dart';
import '../logic/arr_path_match.dart';
import '../logic/plex_invites_provider.dart';

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
    this.serviceTypePrompt,
  });

  bool get isEditing => instanceId != null;

  @override
  ConsumerState<InstanceEditScreen> createState() => _InstanceEditScreenState();
}

class _InstanceEditScreenState extends ConsumerState<InstanceEditScreen> {
  late final TextEditingController _nameController;
  late final TextEditingController _urlController;
  late final TextEditingController _apiKeyController;
  late final TextEditingController _usernameController;
  late final TextEditingController _passwordController;
  late final TextEditingController _publicAddressController;
  String _serviceType = 'radarr';
  bool _isDefault = false;
  bool _isSaving = false;
  bool _isTesting = false;
  String? _testResult;
  bool _testSucceeded = false;
  bool _isConfiguringWebhook = false;
  String? _webhookResult;
  Color _webhookResultColor = AppTheme.textSecondary;

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
    ('jellyfin', 'Jellyfin'),
    ('emby', 'Emby'),
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
  bool get _grantOnly => _isChaptarr || _isLidarr || _isMediaServer;

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
      _supportsUserAssignment && (_grantOnly || !_isDefault);

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

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.initialName ?? '');
    _urlController = TextEditingController(text: widget.initialUrl ?? '');
    _apiKeyController = TextEditingController(text: widget.initialApiKey ?? '');
    _usernameController =
        TextEditingController(text: widget.initialUsername ?? '');
    _passwordController = TextEditingController();
    _publicAddressController = TextEditingController()
      ..addListener(_markMediaServerConfigDirty);
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
    if (widget.isEditing) _loadDetails();
    _loadMediaRoots();
    _loadArrRootFolders();
    _loadDirectory();
    _loadWebhookStatus();
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
        result = 'Instant updates are on.';
        color = AppTheme.available;
      } else {
        result = switch (status.state) {
          'stale' => 'The $_serviceLabel webhook points at a different '
              'Cantinarr address — configure to update it.',
          'credential_missing' => 'The $_serviceLabel webhook exists, but the '
              'server no longer holds its credential — configure to reissue '
              'it.',
          'no_public_url' => 'The server cannot determine the address the '
              '$_serviceLabel container can call back — set '
              'CANTINARR_ARR_CALLBACK_URL, then configure.',
          _ => 'Instant updates are not configured yet.',
        };
      }
    } on DioException catch (e) {
      final code = e.response?.statusCode ?? 0;
      // An older server has no status route; unknown is not worth a line.
      if (code == 404 || code == 405) return;
      // Blindness, not absence: the state could not be read, which is a
      // different answer than "not configured".
      result = 'Could not check instant updates: ${apiErrorMessage(e)}';
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
      setState(() => _arrRootFoldersError =
          'Could not read the library folders $_serviceLabel reports.');
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
        _mediaRootsError = 'Could not load the server media folders.';
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
      final results =
          await Future.wait<Object>([instancesFuture, usersFuture]);
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
    } catch (_) {
      if (!mounted) return;
      setState(() => _userSelectError = 'Could not load users');
    }
  }

  /// The default toggle starts ON when creating the first instance of a type —
  /// there is nothing else the type could default to — and OFF once siblings
  /// exist (the admin opts in explicitly, confirming the takeover on save).
  /// Mutates state; call from within setState.
  void _applyAutoDefault() {
    if (widget.isEditing || !_instancesLoaded || _grantOnly) return;
    _isDefault = !_instances.any((i) => i.serviceType == _serviceType);
  }

  /// Fetches the per-user pins and access grants for the selected service
  /// type. Both endpoints are instance-scoped but answer for the whole type,
  /// so when creating we can ask via any existing sibling; a type with no
  /// instances can have no rows. A checkbox starts checked when either row
  /// kind names this instance — a legacy pin is an assignment too, and
  /// showing it unchecked would turn the next save into a silent revocation.
  Future<void> _loadPins() async {
    if (!_supportsUserAssignment) return;
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
      final pins = _isMediaServer
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
      setState(() => _userSelectError = 'Could not load user assignments');
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
        _storedQbitAuth = details['has_api_key'] == true
            ? _QbitAuth.apiKey
            : _QbitAuth.password;
        _qbitAuth = _storedQbitAuth;
        if (_isMediaServer) {
          final raw = details['media_server_config'];
          final config = raw is Map
              ? MediaServerConfig.fromJson(Map<String, dynamic>.from(raw))
              : const MediaServerConfig();
          if (_publicAddressController.text.isEmpty) {
            _publicAddressController.text = config.publicAddress;
          }
          _selectedLibraryIds = config.libraryIds.toSet();
          if (_isPlex) {
            _plexLinkedStored = true;
            _plexMachineId = config.machineIdentifier;
            _plexAutoApprove = config.autoApprove;
          }
          // Hydration is not an edit: only a touch after this sends the
          // config back, so an untouched section keeps the server's copy.
          _mediaServerConfigDirty = false;
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
          _mediaMappingsError =
              'Update the Cantinarr server to configure media downloads.';
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
          'Could not load this instance’s media path mappings.');
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
        _mediaServerLibrariesError =
            "Couldn't load the libraries this server reports: "
            '${apiErrorMessage(e)}';
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
    _nameController.dispose();
    _urlController.dispose();
    _apiKeyController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    _publicAddressController.dispose();
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
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
          content: Text('Could not reach plex.tv. Try again.')));
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
          ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
              content:
                  Text('Not approved yet. Finish signing in on plex.tv.')));
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
          content: Text('Linked as ${state.account}. Pick the server to '
              'share.')));
      _loadPlexServers();
    } on DioException catch (e) {
      if (!silent && mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(_plexLinkFailure(e))));
      }
    } catch (_) {
      if (!silent && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
            content: Text('Could not reach plex.tv. Try again.')));
      }
    }
  }

  /// Says which side failed. A 404 is a server from before Plex instances
  /// (the arr proxy's wildcard answers it), a 502 is the server unable to
  /// reach plex.tv, and no status at all is no answer from the server.
  static String _plexLinkFailure(DioException e) {
    switch (e.response?.statusCode) {
      case 404:
        return "This server doesn't have Plex linking yet. Update the "
            'Cantinarr server, then try again.';
      case 502:
        return "Your server couldn't reach plex.tv. Check its internet "
            'access and try again.';
      case null:
        return 'No answer from the server. Check your connection and try '
            'again.';
      default:
        return 'Could not reach plex.tv. Try again.';
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
        _plexServersError =
            "Couldn't list the account's servers: ${apiErrorMessage(e)}";
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
        _testResult = 'Choose a service type first.';
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
        username: _usernameController.text.trim(),
        password: _passwordController.text,
        plexLinkPin: _isPlex && _plexAccount.isNotEmpty ? _plexPinId : null,
        machineIdentifier: _isPlex ? _plexMachineId : '',
      );
      if (!mounted) return;
      setState(() {
        _isTesting = false;
        _testSucceeded = true;
        _testResult = 'Connection successful!';
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
    if (_serviceTypeUnchosen) {
      return 'Choose a service type';
    }
    if (_isPlex) {
      if (_nameController.text.trim().isEmpty) return 'Name is required';
      if (!_plexHasCredential) return 'Link a Plex account first';
      if (_plexMachineId.isEmpty) return 'Pick the Plex server to share';
    } else if (_nameController.text.trim().isEmpty ||
        _urlController.text.trim().isEmpty) {
      return 'Name and URL are required';
    }
    if (_shouldSubmitMediaPathMappings) {
      for (final mapping in _mediaPathMappings) {
        if (mapping.arrPath.text.trim().isEmpty ||
            mapping.cantinarrPath.text.trim().isEmpty) {
          return 'Both paths are required for every media mapping';
        }
      }
    }
    if (_isMediaServer) {
      // The address is handed to users verbatim as a link, so it must be one.
      final address = _publicAddressController.text.trim();
      if (address.isNotEmpty &&
          !address.startsWith('http://') &&
          !address.startsWith('https://')) {
        return 'Sign-in address must start with http:// or https://';
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
        if (_passwordController.text.isEmpty) return 'Password is required';
      } else if (_usernameController.text.trim().isEmpty ||
          _passwordController.text.isEmpty) {
        return 'Username and password are required';
      }
    } else if (_apiKeyController.text.trim().isEmpty) {
      return 'API key is required';
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
        title: Text('Change default $label instance?'),
        content: Text(
          '"${sibling.name}" is currently the default $label instance. '
          'Saving will move the default from "${sibling.name}" to "$newName": '
          'requests and dashboard statuses for users without a per-user '
          'instance will switch to "$newName".',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Make Default'),
          ),
        ],
      ),
    );
    return confirmed == true;
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
        _grantOnly && !_isMediaServer ? 'Assigned Users' : 'User Access',
        style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 13,
            fontWeight: FontWeight.w600),
      ),
      const SizedBox(height: 4),
      Text(
        _isChaptarr
            ? 'Chaptarr instances are assigned per user: selected users get '
                'Books access through this instance (alongside any other '
                'Chaptarr instance they hold). Unselecting a user removes '
                'their access.'
            : _isLidarr
                ? 'Lidarr instances are assigned per user: selected users get '
                    'Music access through this instance (alongside any other '
                    'Lidarr instance they hold). Unselecting a user removes '
                    'their access.'
            : _isPlex
                ? 'Selected users get this server under Watch on Plex, '
                    'where they sign in with their own Plex account or share '
                    'its email; the share of the chosen libraries goes out '
                    'the moment they do. Select yourself too: the account '
                    'that owns the server is recognised as the owner, never '
                    'invited. Unselecting a user removes their share; '
                    'selecting them again shares it again.'
                : _isMediaServer
                    ? 'Selected users get this server under Watch on '
                        '$_serviceLabel, where they create their own account '
                        'or sign in with one they already have (administrator '
                        'accounts included, so select yourself too). '
                        'Unselecting a user turns their account off without '
                        'deleting it; selecting them again turns it back on. '
                        'Administrator accounts are never changed.'
                    : 'Selected users can use this library for requests alongside '
                    'their default $_serviceLabel library, choosing per '
                    'request. Unselecting a user removes their access to this '
                    'library.',
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
              child: const Text('Retry'),
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
        const Text('No users yet.',
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 13))
      else
        ...users.map(_userTile),
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
      subtitle: defaultElsewhere != null
          ? Text('Default library: "$defaultElsewhere"',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12))
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

  Future<void> _save() async {
    final validationError = _validate();
    if (validationError != null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(validationError)),
      );
      return;
    }
    if (!await _confirmDefaultTakeover()) return;
    if (!mounted) return;

    // Chaptarr and media servers never carry the global default flag (the
    // server enforces this too); their instances are only assigned per user
    // below.
    final isDefault = !_grantOnly && _isDefault;
    // Apply assignments only when the section is visible and the selection
    // actually changed — a hidden section must never silently rewrite pins.
    final applyAssignments = _showUserSelect &&
        _userSelectError == null &&
        _users != null &&
        !_sameSelection(_assignedUserIds, _savedAssignedUserIds);
    final assignedIds = _assignedUserIds.toList()..sort();
    final mediaPathMappings = _shouldSubmitMediaPathMappings
        ? _currentMediaPathMappings()
        : null;
    // Media-server settings travel whole: always on create, and on edit only
    // when the section was touched, so an unrelated edit never rewrites the
    // stored address or library choice (null = keep).
    final mediaServerConfig =
        _isMediaServer && (!widget.isEditing || _mediaServerConfigDirty)
            ? MediaServerConfig(
                publicAddress: _publicAddressController.text.trim(),
                libraryIds: _selectedLibraryIds.toList(growable: false),
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
        await service.updateInstance(
          id: widget.instanceId!,
          name: _nameController.text.trim(),
          url: _urlController.text.trim(),
          apiKey: _apiKeyController.text.trim(),
          username: _usernameController.text.trim(),
          password: _passwordController.text,
          isDefault: isDefault,
          mediaPathMappings: mediaPathMappings,
          mediaServerConfig: mediaServerConfig,
          plexLinkPin: plexLinkPin,
        );
        if (applyAssignments) {
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
                  content: Text('Instance saved, but assigning users '
                      'failed: ${apiErrorMessage(e)}')),
            );
            return;
          }
        }
        await _refreshConfigAfterSave();
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('Instance updated')),
          );
          context.pop(true); // Return true to signal refresh needed
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
        mediaPathMappings: mediaPathMappings,
        mediaServerConfig: mediaServerConfig,
          plexLinkPin: plexLinkPin,
      );
      // The instance exists now, so a failed assignment must not re-run
      // create: surface it and let the admin retry from the edit screen.
      String? assignmentError;
      if (applyAssignments) {
        try {
          await service.updateInstanceGrantUsers(created.id, assignedIds);
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
            'assigning users failed: $assignmentError',
          if (webhookError != null)
            "instant updates couldn't be configured: $webhookError",
        ];
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(problems.isNotEmpty
                  ? 'Instance created, but ${problems.join('; ')} '
                      '— edit the instance to retry'
                  : webhookConfigured
                      ? 'Instance created — instant updates configured'
                      : 'Instance created')),
        );
        context.pop(true); // Return true to signal refresh needed
      }
    } catch (e) {
      if (!mounted) return;
      setState(() => _isSaving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to save: ${apiErrorMessage(e)}')),
      );
    }
  }

  Future<void> _refreshConfigAfterSave() async {
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
          refreshed.radarrInstances.any((instance) => instance.id == radarrId)) {
        notifier.setActiveRadarrInstance(radarrId);
      }
      final sonarrId = activeBefore.activeSonarrInstanceId;
      if (sonarrId != null &&
          refreshed.sonarrInstances.any((instance) => instance.id == sonarrId)) {
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
        title: const Text('Delete Instance'),
        content: const Text('Are you sure you want to delete this instance?'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child:
                const Text('Delete', style: TextStyle(color: AppTheme.error)),
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
          const SnackBar(content: Text('Instance deleted')),
        );
        context.pop(true);
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to delete: ${apiErrorMessage(e)}')),
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
        _webhookResult = 'Instant updates are configured.';
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
      case 'jellyfin':
        return 'http://jellyfin:8096';
      case 'emby':
        return 'http://emby:8096';
      default:
        return 'http://radarr:7878';
    }
  }

  String get _nameHint {
    if (_isDownloadClient) return 'e.g. SABnzbd, qBittorrent';
    switch (_serviceType) {
      case 'tautulli':
        return 'e.g. Tautulli';
      case 'tracearr':
        return 'e.g. Tracearr';
      case 'jellyfin':
        return 'e.g. Home Jellyfin';
      case 'emby':
        return 'e.g. Home Emby';
      case 'plex':
        return 'e.g. Cantina Plex';
      default:
        return 'e.g. Movies, 4K Movies';
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
          segments: const [
            ButtonSegment(
              value: _QbitAuth.password,
              label: Text('Username & password'),
            ),
            ButtonSegment(
              value: _QbitAuth.apiKey,
              label: Text('API key'),
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
              ? 'API keys need qBittorrent 5.2 or newer; generate one under '
                  'Options > WebUI.'
              : 'The WebUI sign-in; works on every qBittorrent version.',
          style: const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
        ),
      ],
    );
  }

  String get _apiKeyHint {
    if (widget.isEditing && !_qbitSwitchingShape) {
      return 'Leave blank to keep existing';
    }
    switch (_serviceType) {
      case 'qbittorrent':
        return 'Your qBittorrent API key (Options > WebUI, 5.2 or newer)';
      case 'sabnzbd':
        return 'Your SABnzbd API key';
      case 'tautulli':
        return 'Your Tautulli API key';
      case 'tracearr':
        return 'Your Tracearr API key (Settings > General, starts with trr_pub_)';
      case 'chaptarr':
        return 'Your Chaptarr API key';
      case 'lidarr':
        return 'Your Lidarr API key';
      case 'jellyfin':
        return 'Your Jellyfin API key (Dashboard > API Keys)';
      case 'emby':
        return 'Your Emby API key (Settings > Advanced > API Keys)';
      default:
        return 'Your Radarr/Sonarr API key';
    }
  }

  String get _defaultSubtitle {
    if (_isDownloadClient) return 'Use this as the default download client';
    if (_isWatchHistory) {
      return 'Use this as the default $_serviceLabel instance';
    }
    return 'Use this as the default for media requests';
  }

  /// The server's own library kind, humanized. Jellyfin and Emby report
  /// 'movies', 'tvshows', 'music', 'books', 'homevideos', 'musicvideos',
  /// 'boxsets', 'photos', 'playlists'; a mixed movies-and-shows library
  /// reports none.
  static String _collectionTypeLabel(String collectionType) {
    switch (collectionType.toLowerCase()) {
      case '':
        return 'Mixed content';
      case 'movies':
        return 'Movies';
      case 'tvshows':
        return 'Shows';
      case 'music':
        return 'Music';
      case 'musicvideos':
        return 'Music videos';
      case 'homevideos':
        return 'Home videos and photos';
      case 'books':
        return 'Books';
      case 'boxsets':
        return 'Collections';
      case 'photos':
        return 'Photos';
      case 'playlists':
        return 'Playlists';
      case 'livetv':
        return 'Live TV';
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
              const Expanded(
                child: Text(
                  'Plex account',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 16,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              Container(
                padding:
                    const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
                decoration: BoxDecoration(
                  color: (linked ? AppTheme.available : AppTheme.textSecondary)
                      .withValues(alpha: 0.12),
                  borderRadius: BorderRadius.circular(999),
                ),
                child: Text(
                  linked ? 'Linked' : 'Not linked',
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
            const Row(
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
                    'Waiting for approval. Sign in on the plex.tv page that '
                    'just opened and approve the link.',
                    style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
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
                  child: const Text("I've approved, check now"),
                ),
                if (_plexLinkUrl != null)
                  OutlinedButton(
                    onPressed: () => launchUrl(Uri.parse(_plexLinkUrl!),
                        mode: LaunchMode.externalApplication),
                    child: const Text('Reopen plex.tv'),
                  ),
                TextButton(
                  onPressed: _cancelPlexLink,
                  child: const Text('Cancel'),
                ),
              ],
            ),
          ] else ...[
            Text(
              linked
                  ? (_plexAccount.isNotEmpty
                      ? 'Linked as $_plexAccount.'
                      : 'A linked account is stored. Relink to use another.')
                  : 'Link the plex.tv account that owns the server. Invites '
                      'are sent from it, and its token never leaves the '
                      'Cantinarr server.',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
            const SizedBox(height: 8),
            OutlinedButton.icon(
              onPressed: _beginPlexLink,
              icon: const Icon(Icons.link, size: 18),
              label: Text(linked ? 'Relink Plex account' : 'Link Plex account'),
            ),
          ],
          if (linked) ...[
            const SizedBox(height: 16),
            const Text(
              'Server to share',
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
                    child: const Text('Retry'),
                  ),
                ],
              )
            else if (servers == null)
              const SizedBox.shrink()
            else if (servers.isEmpty)
              const Text(
                'This account owns no Plex Media Server. The linked account '
                'must own the server it invites to.',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
              )
            else
              for (final server in servers) serverTile(server),
            if (servers != null &&
                _plexMachineId.isNotEmpty &&
                !servers.any((s) => s.machineIdentifier == _plexMachineId))
              serverTile(PlexServerChoice(
                  name: 'Stored server ($_plexMachineId)',
                  machineIdentifier: _plexMachineId)),
          ],
        ],
      ),
    );
  }

  /// Plex: whether sharing a Plex email is enough to be granted this server.
  Widget _buildPlexAutoApproveTile() {
    return SwitchListTile(
      title: const Text('Auto-approve access requests',
          style: TextStyle(color: AppTheme.textPrimary)),
      subtitle: const Text(
        'Anyone who shares a Plex email is granted this server and invited '
        'right away. Off: they wait until you select them under User Access.',
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
        selectedCount == 0 ? 'All' : '$selectedCount selected',
        style: TextStyle(
          color: selectedCount == 0
              ? AppTheme.textSecondary
              : AppTheme.accent,
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
            const Expanded(
              child: Text(
                'Shared libraries',
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
              final largeText =
                  MediaQuery.textScalerOf(context).scale(1) > 1.3;
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
          const Text(
            'Optional. Choose which libraries these accounts can see. '
            'Changing it updates the accounts Cantinarr created here; '
            'accounts you linked keep what they have. With nothing chosen, '
            'every library is shared, including ones you add later.',
            style: TextStyle(
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
                child: const Text('Retry'),
              ),
            )
          else if (libraries == null)
            const _MediaMappingNotice(
              icon: Icons.wifi_tethering,
              message:
                  'Test the connection to load the libraries this server '
                  'reports.',
            )
          else ...[
            if (libraries.isEmpty)
              const _MediaMappingNotice(
                icon: Icons.video_library_outlined,
                message: 'This server reports no libraries yet. Every library '
                    'you add later will be shared.',
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
                title: 'Unknown library',
                subtitle:
                    'No longer reported by the server. Uncheck to drop it.',
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
            ? 'Off'
            : '$mappingCount '
                '${mappingCount == 1 ? 'mapping' : 'mappings'}',
        style: TextStyle(
          color: mappingCount == 0
              ? AppTheme.textSecondary
              : AppTheme.accent,
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
            const Expanded(
              child: Text(
                'Media downloads',
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
              final largeText =
                  MediaQuery.textScalerOf(context).scale(1) > 1.3;
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
            'Optional. Map each library path reported by $_serviceLabel to '
            'where the same files are mounted inside Cantinarr. Only files '
            'covered by this instance’s mappings can be downloaded. Linux, '
            'Windows drive, and UNC source paths are supported.',
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
                child: const Text('Retry'),
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
                      child: const Text('Retry'),
                    )
                  : null,
            ),
          if (roots != null && roots.isEmpty)
            const _MediaMappingNotice(
              icon: Icons.folder_off_outlined,
              message: 'No server media folders are available. Mount them '
                  'read-only, set CANTINARR_MEDIA_ROOTS, and restart the server.',
            ),
          if (roots != null && roots.isNotEmpty) ...[
            Text(
              'Allowed Cantinarr ${roots.length == 1 ? 'root' : 'roots'}',
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
                  child: const Text('Retry'),
                ),
              ),
            if (_arrRootFoldersKnown && _arrRootFolders.isNotEmpty) ...[
              Text(
                'Reported by $_serviceLabel — tap to map',
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
              const Padding(
                padding: EdgeInsets.symmetric(vertical: 6),
                child: Text(
                  'No paths mapped — downloads are off for this instance.',
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
              label: const Text('Add path'),
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
                  'Path mapping ${index + 1}',
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 11,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ),
              IconButton(
                tooltip: 'Remove path mapping',
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
                  labelText: '$_serviceLabel path',
                  hintText: _isChaptarr ? '/ebooks' : '/media/library',
                ),
                autocorrect: false,
              );
              final target = TextField(
                controller: mapping.cantinarrPath,
                decoration: InputDecoration(
                  labelText: 'Cantinarr path',
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
                          '$_serviceLabel does not report any library folder '
                          'under this path, so downloads are unlikely to '
                          'match. Tap a reported folder above to use a path '
                          '$_serviceLabel actually sees.',
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
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.isEditing ? 'Edit Instance' : 'Add Instance'),
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
          // Service type (only for new instances)
          if (!widget.isEditing) ...[
            const Text('Service Type',
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
              labelText: 'Name',
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
                helperText:
                    'Reached from the Cantinarr server, not from this device.',
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
                  labelText:
                      _credentialsOptional ? 'Username (optional)' : 'Username',
                  hintText: _credentialsOptional
                      ? 'Only if authentication is enabled'
                      : 'Web UI username',
                ),
                autocorrect: false,
              ),
              const SizedBox(height: 16),
            ],
            TextField(
              controller: _passwordController,
              decoration: InputDecoration(
                labelText: _passwordOnly
                    ? 'Web UI password'
                    : (_credentialsOptional
                        ? 'Password (optional)'
                        : 'Password'),
                hintText: widget.isEditing && !_qbitSwitchingShape
                    ? 'Leave blank to keep existing'
                    : (_passwordOnly
                        ? "The password Deluge's web UI asks for"
                        : _credentialsOptional
                            ? 'Only if authentication is enabled'
                            : 'Web UI password'),
              ),
              obscureText: true,
            ),
          ] else
            TextField(
              controller: _apiKeyController,
              decoration: InputDecoration(
                labelText: 'API Key',
                hintText: _apiKeyHint,
              ),
              obscureText: true,
            ),
          // Media servers: where users are told to sign in (handed to them
          // verbatim, so only an address the admin typed is ever shown) and
          // which libraries a new account may see.
          if (_isMediaServer) ...[
            const SizedBox(height: 16),
            TextField(
              controller: _publicAddressController,
              decoration: InputDecoration(
                labelText: 'Sign-in address (optional)',
                hintText: 'https://$_serviceType.example.com',
                helperText: 'What your users open to sign in. Shown to them '
                    'in the app. Leave blank and they will need to ask you.',
                helperMaxLines: 3,
              ),
              keyboardType: TextInputType.url,
              autocorrect: false,
            ),
            const SizedBox(height: 24),
            _buildSharedLibrariesSection(),
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

          // Chaptarr and media servers have no global default: their
          // instances are assigned directly to users below instead. The
          // prompted form hides the toggle entirely until a type is chosen.
          if (!_serviceTypeUnchosen && !_grantOnly)
            SwitchListTile(
              title: const Text('Default Instance',
                  style: TextStyle(color: AppTheme.textPrimary)),
              subtitle: Text(_defaultSubtitle,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 13)),
              value: _isDefault,
              onChanged: (value) => setState(() => _isDefault = value),
              activeTrackColor: AppTheme.accent,
            ),

          if (_showUserSelect) ..._buildUserSelect(),

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
            label: const Text('Test Connection'),
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
            onPressed: _isSaving ? null : _save,
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
                : Text(widget.isEditing ? 'Save Changes' : 'Add Instance'),
          ),

          // Webhook setup (source instances, editing only). Cantinarr installs
          // its own Connect record; the callback credential never reaches the
          // app or clipboard.
          if (widget.isEditing && _supportsWebhook) ...[
            const SizedBox(height: 32),
            const Text('Instant updates',
                style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600)),
            const SizedBox(height: 8),
            Text(
              'Cantinarr can create or refresh its Connect → Webhook record in '
              '$_serviceLabel. ${_isChaptarr ? 'Books that finish downloading '
                  'are announced the moment they land, instead of on the next '
                  'check.' : 'Imports, deletes and adds made there will reach '
                  'Cantinarr immediately.'} '
              'The callback credential stays securely on the server.',
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
              label: const Text('Configure instant updates'),
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
  })
      : arrPath = TextEditingController(text: arrPath),
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
