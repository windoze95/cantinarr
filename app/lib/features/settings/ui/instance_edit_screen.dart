import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/models/backend_connection.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';
import '../data/instance_api_service.dart';

/// Form for creating or editing a service instance.
class InstanceEditScreen extends ConsumerStatefulWidget {
  final String? instanceId;
  final String? initialServiceType;
  final String? initialName;
  final String? initialUrl;
  final String? initialApiKey;
  final String? initialUsername;
  final bool initialIsDefault;

  const InstanceEditScreen({
    super.key,
    this.instanceId,
    this.initialServiceType,
    this.initialName,
    this.initialUrl,
    this.initialApiKey,
    this.initialUsername,
    this.initialIsDefault = false,
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
  String _serviceType = 'radarr';
  bool _isDefault = false;
  bool _isSaving = false;
  bool _isTesting = false;
  String? _testResult;
  String? _webhookToken;

  /// Fresh instance list from the server — the login-time copy in the auth
  /// state can be stale, and both the first-of-type auto-default and the
  /// default-takeover confirmation depend on what actually exists right now.
  List<ServiceInstance> _instances = const [];
  bool _instancesLoaded = false;

  // User-assignment section state: all accounts, their current per-user pin
  // for this service type (user id → instance id, possibly a sibling
  // instance), the working selection, and the selection as last saved.
  List<UserSummary>? _users;
  Map<int, String> _pins = const {};
  Set<int> _assignedUserIds = <int>{};
  Set<int> _savedAssignedUserIds = <int>{};
  String? _userSelectError;

  static const _serviceTypes = <(String, String)>[
    ('radarr', 'Radarr'),
    ('sonarr', 'Sonarr'),
    ('chaptarr', 'Chaptarr'),
    ('sabnzbd', 'SABnzbd'),
    ('qbittorrent', 'qBittorrent'),
    ('nzbget', 'NZBGet'),
    ('transmission', 'Transmission'),
    ('tautulli', 'Tautulli'),
  ];

  /// Types that authenticate with username/password instead of an API key.
  bool get _usesUserPass =>
      _serviceType == 'qbittorrent' ||
      _serviceType == 'nzbget' ||
      _serviceType == 'transmission';

  /// Transmission auth is optional (only when the daemon requires it).
  bool get _credentialsOptional => _serviceType == 'transmission';

  bool get _isDownloadClient =>
      _serviceType == 'sabnzbd' ||
      _serviceType == 'qbittorrent' ||
      _serviceType == 'nzbget' ||
      _serviceType == 'transmission';

  /// Only the v3 arr services support a device-direct connection test (it hits
  /// `/api/v3/system/status`); the rest — including Chaptarr, which is `/api/v1`
  /// — are validated by the backend when saving.
  bool get _supportsDirectTest =>
      _serviceType == 'radarr' || _serviceType == 'sonarr';

  bool get _isChaptarr => _serviceType == 'chaptarr';

  /// Source types feed requests and dashboard statuses, so they support
  /// per-user assignment; download clients and Tautulli are global-only.
  bool get _supportsUserAssignment =>
      _serviceType == 'radarr' || _serviceType == 'sonarr' || _isChaptarr;

  /// Chaptarr has no global default — its instances are only ever assigned
  /// directly to users — so it always shows the user-select. The other source
  /// types show it when this instance is NOT the global default, as a
  /// per-user override of that default.
  bool get _showUserSelect =>
      _supportsUserAssignment && (_isChaptarr || !_isDefault);

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
    _serviceType = widget.initialServiceType ?? 'radarr';
    _isDefault = widget.initialIsDefault;
    if (widget.isEditing) _loadDetails();
    _loadDirectory();
  }

  /// Loads the fresh instance list plus the users and their current pins for
  /// the user-assignment section.
  Future<void> _loadDirectory() async {
    try {
      final service =
          InstanceApiService(backendDio: ref.read(backendClientProvider));
      final instancesFuture = service.listInstances();
      final usersFuture = ref.read(authProvider.notifier).listUsers();
      final instances = await instancesFuture;
      final users = await usersFuture;
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
    if (widget.isEditing || !_instancesLoaded || _isChaptarr) return;
    _isDefault = !_instances.any((i) => i.serviceType == _serviceType);
  }

  /// Fetches the per-user pins for the selected service type. The endpoint is
  /// instance-scoped but answers for the whole type, so when creating we can
  /// ask via any existing sibling; a type with no instances can have no pins.
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
      final pins = await service.getInstanceUsers(anchorId);
      if (!mounted) return;
      setState(() {
        _pins = pins;
        _assignedUserIds = widget.isEditing
            ? pins.entries
                .where((e) => e.value == widget.instanceId)
                .map((e) => e.key)
                .toSet()
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
        _webhookToken = details['webhook_token'] as String?;
      });
    } catch (_) {
      // Best-effort prefill; the form still works with manual entry.
    }
  }

  @override
  void dispose() {
    _nameController.dispose();
    _urlController.dispose();
    _apiKeyController.dispose();
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _testConnection() async {
    setState(() {
      _isTesting = true;
      _testResult = null;
    });

    final backendDio = ref.read(backendClientProvider);
    final service = InstanceApiService(backendDio: backendDio);
    final success = await service.testConnection(
      _urlController.text.trim(),
      _apiKeyController.text.trim(),
    );

    setState(() {
      _isTesting = false;
      _testResult = success ? 'Connection successful!' : 'Connection failed';
    });
  }

  String? _validate() {
    if (_nameController.text.trim().isEmpty ||
        _urlController.text.trim().isEmpty) {
      return 'Name and URL are required';
    }
    // When editing, blank credentials keep the existing ones.
    if (widget.isEditing) return null;
    if (_usesUserPass) {
      if (_credentialsOptional) return null;
      if (_usernameController.text.trim().isEmpty ||
          _passwordController.text.isEmpty) {
        return 'Username and password are required';
      }
    } else if (_apiKeyController.text.trim().isEmpty) {
      return 'API key is required';
    }
    return null;
  }

  String _errorMessage(Object e) {
    if (e is DioException) {
      final data = e.response?.data;
      if (data is Map<String, dynamic> && data['error'] is String) {
        return data['error'] as String;
      }
      return e.message ?? e.toString();
    }
    return e.toString();
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
    if (!_isDefault || _isChaptarr || sibling == null) return true;
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

  /// Selected users who are currently pinned to a sibling instance, grouped
  /// by that sibling's name. Saving moves them off it — for Chaptarr that
  /// also moves their Books access.
  Map<String, List<String>> get _pendingUserMoves {
    final moves = <String, List<String>>{};
    for (final user in _users ?? const <UserSummary>[]) {
      if (!_assignedUserIds.contains(user.id)) continue;
      final pinnedTo = _pins[user.id];
      if (pinnedTo == null || pinnedTo == widget.instanceId) continue;
      moves.putIfAbsent(_instanceName(pinnedTo), () => []).add(user.username);
    }
    return moves;
  }

  static String _joinNames(List<String> names) {
    if (names.length == 1) return names.first;
    return '${names.sublist(0, names.length - 1).join(', ')} and ${names.last}';
  }

  /// Assigning a user who is pinned to a sibling instance removes them there
  /// — spell out exactly who is removed from which instance, and let the
  /// admin back out, before anything is saved.
  Future<bool> _confirmUserMoves() async {
    final moves = _pendingUserMoves;
    if (moves.isEmpty) return true;
    final newName = _nameController.text.trim();
    final total = moves.values.fold<int>(0, (n, names) => n + names.length);
    final String description;
    if (moves.length == 1) {
      final entry = moves.entries.first;
      description = 'This removes ${_joinNames(entry.value)} from '
          '"${entry.key}" and assigns them to "$newName".';
    } else {
      final lines = moves.entries
          .map((e) => '• ${_joinNames(e.value)} — from "${e.key}"')
          .join('\n');
      description = 'This removes $total users from their current instances '
          'and assigns them to "$newName":\n\n$lines';
    }
    final note = _isChaptarr
        ? 'Their Books access will come from "$newName" instead.'
        : 'Their requests and dashboard statuses will use "$newName" instead.';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('Reassign $total user${total == 1 ? '' : 's'}?'),
        content: Text('$description\n\n$note'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Reassign'),
          ),
        ],
      ),
    );
    return confirmed == true;
  }

  /// Per-user assignment: for Chaptarr this IS the access model (selected
  /// users get Books through this instance); for Radarr/Sonarr it pins the
  /// selected users to this instance as an override of the global default.
  List<Widget> _buildUserSelect() {
    final users = _users;
    return [
      const SizedBox(height: 16),
      Text(
        _isChaptarr ? 'Assigned Users' : 'Per-User Default',
        style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 13,
            fontWeight: FontWeight.w600),
      ),
      const SizedBox(height: 4),
      Text(
        _isChaptarr
            ? 'Chaptarr instances are assigned per user: selected users get '
                'Books access through this instance. Unselecting a user '
                'removes their access.'
            : 'Selected users use this instance for requests and dashboard '
                'statuses instead of the default $_serviceLabel instance.',
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
    // Surface where the user is assigned today, so selecting them here is a
    // visible move rather than a silent one.
    final movingFrom = pinnedTo != null && pinnedTo != widget.instanceId
        ? _instanceName(pinnedTo)
        : null;
    return CheckboxListTile(
      dense: true,
      contentPadding: EdgeInsets.zero,
      controlAffinity: ListTileControlAffinity.leading,
      activeColor: AppTheme.accent,
      title: Text(user.username,
          style: const TextStyle(color: AppTheme.textPrimary)),
      subtitle: movingFrom != null
          ? Text('Currently assigned to "$movingFrom"',
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

    // Chaptarr never carries the global default flag (the server enforces
    // this too); its instances are only assigned per user below.
    final isDefault = !_isChaptarr && _isDefault;
    // Apply assignments only when the section is visible and the selection
    // actually changed — a hidden section must never silently rewrite pins.
    final applyAssignments = _showUserSelect &&
        _userSelectError == null &&
        _users != null &&
        !_sameSelection(_assignedUserIds, _savedAssignedUserIds);
    final assignedIds = _assignedUserIds.toList()..sort();
    // Pulling users off a sibling instance needs the same explicit sign-off
    // as a default takeover.
    if (applyAssignments && !await _confirmUserMoves()) return;
    if (!mounted) return;

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
        );
        if (applyAssignments) {
          try {
            await service.updateInstanceUsers(widget.instanceId!, assignedIds);
          } catch (e) {
            // The instance itself saved; stay here so Save can retry the
            // assignments (re-updating the instance is idempotent).
            setState(() => _isSaving = false);
            if (mounted) {
              ScaffoldMessenger.of(context).showSnackBar(
                SnackBar(
                    content: Text('Instance saved, but assigning users '
                        'failed: ${_errorMessage(e)}')),
              );
            }
            return;
          }
        }
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
      );
      // The instance exists now, so a failed assignment must not re-run
      // create: surface it and let the admin retry from the edit screen.
      String? assignmentError;
      if (applyAssignments) {
        try {
          await service.updateInstanceUsers(created.id, assignedIds);
        } catch (e) {
          assignmentError = _errorMessage(e);
        }
      }
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(assignmentError == null
                  ? 'Instance created'
                  : 'Instance created, but assigning users failed: '
                      '$assignmentError — edit the instance to retry')),
        );
        context.pop(true); // Return true to signal refresh needed
      }
    } catch (e) {
      setState(() => _isSaving = false);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to save: ${_errorMessage(e)}')),
        );
      }
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
          SnackBar(content: Text('Failed to delete: ${_errorMessage(e)}')),
        );
      }
    }
  }

  /// The ready-to-paste webhook URL for this arr instance: the app's backend
  /// base + the webhook route + the per-instance token the details fetch
  /// returned. Null until the token loads (or for non-arr services).
  String? get _webhookUrl {
    final token = _webhookToken;
    final id = widget.instanceId;
    if (token == null || token.isEmpty || id == null) return null;
    final base = ref
        .read(backendClientProvider)
        .options
        .baseUrl
        .replaceAll(RegExp(r'/+$'), '');
    return '$base/api/webhooks/arr/$id?token=$token';
  }

  Future<void> _copyWebhookUrl(String url) async {
    await Clipboard.setData(ClipboardData(text: url));
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(content: Text('Webhook URL copied')),
    );
  }

  String get _urlHint {
    switch (_serviceType) {
      case 'sonarr':
        return 'http://192.168.1.100:8989';
      case 'chaptarr':
        return 'http://192.168.1.100:8787';
      case 'sabnzbd':
        return 'http://192.168.1.100:8080';
      case 'qbittorrent':
        return 'http://192.168.1.100:8081';
      case 'nzbget':
        return 'http://192.168.1.100:6789';
      case 'transmission':
        return 'http://192.168.1.100:9091';
      case 'tautulli':
        return 'http://192.168.1.100:8181';
      default:
        return 'http://192.168.1.100:7878';
    }
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
              dropdownColor: AppTheme.surfaceVariant,
              items: _serviceTypes
                  .map((t) => DropdownMenuItem(
                        value: t.$1,
                        child: Text(t.$2),
                      ))
                  .toList(),
              onChanged: (value) {
                if (value == null) return;
                setState(() {
                  _serviceType = value;
                  _testResult = null;
                  // The selection and pins belong to the previous type.
                  _pins = const {};
                  _assignedUserIds = <int>{};
                  _savedAssignedUserIds = <int>{};
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
              hintText: _isDownloadClient
                  ? 'e.g. SABnzbd, qBittorrent'
                  : (_serviceType == 'tautulli'
                      ? 'e.g. Tautulli'
                      : 'e.g. Movies, 4K Movies'),
            ),
          ),
          const SizedBox(height: 16),

          TextField(
            controller: _urlController,
            decoration: InputDecoration(
              labelText: 'URL',
              hintText: _urlHint,
            ),
            keyboardType: TextInputType.url,
          ),
          const SizedBox(height: 16),

          // qBittorrent, NZBGet and Transmission authenticate with
          // username/password; everything else uses an API key. Credentials
          // are write-only: when editing, blank keeps the existing value.
          if (_usesUserPass) ...[
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
            TextField(
              controller: _passwordController,
              decoration: InputDecoration(
                labelText:
                    _credentialsOptional ? 'Password (optional)' : 'Password',
                hintText: widget.isEditing
                    ? 'Leave blank to keep existing'
                    : (_credentialsOptional
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
                hintText: widget.isEditing
                    ? 'Leave blank to keep existing'
                    : (_serviceType == 'sabnzbd'
                        ? 'Your SABnzbd API key'
                        : (_serviceType == 'tautulli'
                            ? 'Your Tautulli API key'
                            : (_serviceType == 'chaptarr'
                                ? 'Your Chaptarr API key'
                                : 'Your Radarr/Sonarr API key'))),
              ),
              obscureText: true,
            ),
          // Chaptarr also takes an optional web login: its cover images are
          // served behind the web session (not the API key), so these let the
          // backend fetch search-result cover art.
          const SizedBox(height: 16),

          // Chaptarr has no global default: its instances are assigned
          // directly to users below instead.
          if (!_isChaptarr)
            SwitchListTile(
              title: const Text('Default Instance',
                  style: TextStyle(color: AppTheme.textPrimary)),
              subtitle: Text(
                  _isDownloadClient
                      ? 'Use this as the default download client'
                      : (_serviceType == 'tautulli'
                          ? 'Use this as the default Tautulli instance'
                          : 'Use this as the default for media requests'),
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 13)),
              value: _isDefault,
              onChanged: (value) => setState(() => _isDefault = value),
              activeTrackColor: AppTheme.accent,
            ),

          if (_showUserSelect) ..._buildUserSelect(),

          const SizedBox(height: 24),

          // Test connection button (Radarr/Sonarr only — the device calls
          // the arr server directly). Download clients and Tautulli are
          // validated by the backend when saving.
          if (_supportsDirectTest) ...[
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
                  color: _testResult!.contains('successful')
                      ? AppTheme.available
                      : AppTheme.error,
                  fontSize: 13,
                ),
                textAlign: TextAlign.center,
              ),
            ],
          ] else
            const Text(
              'The connection is verified by the server when you save.',
              style: TextStyle(color: AppTheme.textSecondary, fontSize: 12),
              textAlign: TextAlign.center,
            ),

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

          // Webhook setup (Radarr/Sonarr, editing only): without it, changes
          // made directly in the arr are only caught on the next poll or
          // refresh trigger; with it they reach Cantinarr instantly.
          if (_webhookUrl != null) ...[
            const SizedBox(height: 32),
            const Text('Instant updates',
                style: TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    fontWeight: FontWeight.w600)),
            const SizedBox(height: 8),
            Text(
              'Paste this URL into ${_serviceType == 'sonarr' ? 'Sonarr' : 'Radarr'} '
              '→ Settings → Connect → Webhook (method POST) so imports, deletes '
              'and adds made directly in ${_serviceType == 'sonarr' ? 'Sonarr' : 'Radarr'} '
              'reach Cantinarr the moment they happen.',
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 12),
            ),
            const SizedBox(height: 8),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
              decoration: BoxDecoration(
                color: AppTheme.surfaceVariant,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      _webhookUrl!,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                        fontFamily: 'monospace',
                      ),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.copy,
                        size: 18, color: AppTheme.textSecondary),
                    tooltip: 'Copy webhook URL',
                    onPressed: () => _copyWebhookUrl(_webhookUrl!),
                  ),
                ],
              ),
            ),
          ],
        ],
      )),
    );
  }
}
