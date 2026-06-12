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

  bool get _isQbittorrent => _serviceType == 'qbittorrent';
  bool get _isDownloadClient =>
      _serviceType == 'sabnzbd' || _serviceType == 'qbittorrent';

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
    if (_isQbittorrent) {
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
      case 'sabnzbd':
        return 'http://192.168.1.100:8080';
      case 'qbittorrent':
        return 'http://192.168.1.100:8081';
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
            SingleChildScrollView(
              scrollDirection: Axis.horizontal,
              child: SegmentedButton<String>(
                showSelectedIcon: false,
                segments: const [
                  ButtonSegment(value: 'radarr', label: Text('Radarr')),
                  ButtonSegment(value: 'sonarr', label: Text('Sonarr')),
                  ButtonSegment(value: 'sabnzbd', label: Text('SABnzbd')),
                  ButtonSegment(
                      value: 'qbittorrent', label: Text('qBittorrent')),
                ],
                selected: {_serviceType},
                onSelectionChanged: (value) => setState(() {
                  _serviceType = value.first;
                  _testResult = null;
                }),
              ),
            ),
            const SizedBox(height: 24),
          ],

          TextField(
            controller: _nameController,
            decoration: InputDecoration(
              labelText: 'Name',
              hintText: _isDownloadClient
                  ? 'e.g. SABnzbd, qBittorrent'
                  : 'e.g. Movies, 4K Movies',
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

          // qBittorrent authenticates with username/password; everything
          // else uses an API key. Credentials are write-only: when editing,
          // blank keeps the existing value.
          if (_isQbittorrent) ...[
            TextField(
              controller: _usernameController,
              decoration: const InputDecoration(
                labelText: 'Username',
                hintText: 'qBittorrent Web UI username',
              ),
              autocorrect: false,
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _passwordController,
              decoration: InputDecoration(
                labelText: 'Password',
                hintText: widget.isEditing
                    ? 'Leave blank to keep existing'
                    : 'qBittorrent Web UI password',
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
                        : 'Your Radarr/Sonarr API key'),
              ),
              obscureText: true,
            ),
          const SizedBox(height: 16),

          SwitchListTile(
            title: const Text('Default Instance',
                style: TextStyle(color: AppTheme.textPrimary)),
            subtitle: Text(
                _isDownloadClient
                    ? 'Use this as the default download client'
                    : 'Use this as the default for media requests',
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13)),
            value: _isDefault,
            onChanged: (value) => setState(() => _isDefault = value),
            activeTrackColor: AppTheme.accent,
          ),

          const SizedBox(height: 24),

          // Test connection button (Radarr/Sonarr only — the device calls
          // the arr server directly). Download clients are validated by the
          // backend when saving.
          if (!_isDownloadClient) ...[
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
