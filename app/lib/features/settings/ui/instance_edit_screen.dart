import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
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

  Future<void> _save() async {
    final validationError = _validate();
    if (validationError != null) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(validationError)),
      );
      return;
    }

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
          isDefault: _isDefault,
        );
      } else {
        await service.createInstance(
          serviceType: _serviceType,
          name: _nameController.text.trim(),
          url: _urlController.text.trim(),
          apiKey: _apiKeyController.text.trim(),
          username: _usernameController.text.trim(),
          password: _passwordController.text,
          isDefault: _isDefault,
        );
      }

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(
                  widget.isEditing ? 'Instance updated' : 'Instance created')),
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
      body: ListView(
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
                });
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
          if (_serviceType == 'chaptarr') ...[
            const SizedBox(height: 16),
            TextField(
              controller: _usernameController,
              decoration: const InputDecoration(
                labelText: 'Web username (optional)',
                hintText: 'Chaptarr web login — shows cover art in search',
              ),
              autocorrect: false,
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _passwordController,
              decoration: InputDecoration(
                labelText: 'Web password (optional)',
                hintText: widget.isEditing
                    ? 'Leave blank to keep existing'
                    : 'Chaptarr web login password',
              ),
              obscureText: true,
            ),
          ],
          const SizedBox(height: 16),

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
        ],
      ),
    );
  }
}
