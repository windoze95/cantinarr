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
  final bool initialIsDefault;

  const InstanceEditScreen({
    super.key,
    this.instanceId,
    this.initialServiceType,
    this.initialName,
    this.initialUrl,
    this.initialApiKey,
    this.initialIsDefault = false,
  });

  bool get isEditing => instanceId != null;

  @override
  ConsumerState<InstanceEditScreen> createState() =>
      _InstanceEditScreenState();
}

class _InstanceEditScreenState extends ConsumerState<InstanceEditScreen> {
  late final TextEditingController _nameController;
  late final TextEditingController _urlController;
  late final TextEditingController _apiKeyController;
  String _serviceType = 'radarr';
  bool _isDefault = false;
  bool _isSaving = false;
  bool _isTesting = false;
  String? _testResult;

  @override
  void initState() {
    super.initState();
    _nameController = TextEditingController(text: widget.initialName ?? '');
    _urlController = TextEditingController(text: widget.initialUrl ?? '');
    _apiKeyController =
        TextEditingController(text: widget.initialApiKey ?? '');
    _serviceType = widget.initialServiceType ?? 'radarr';
    _isDefault = widget.initialIsDefault;
  }

  @override
  void dispose() {
    _nameController.dispose();
    _urlController.dispose();
    _apiKeyController.dispose();
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

  Future<void> _save() async {
    if (_nameController.text.trim().isEmpty ||
        _urlController.text.trim().isEmpty ||
        _apiKeyController.text.trim().isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('All fields are required')),
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
          isDefault: _isDefault,
        );
      } else {
        await service.createInstance(
          serviceType: _serviceType,
          name: _nameController.text.trim(),
          url: _urlController.text.trim(),
          apiKey: _apiKeyController.text.trim(),
          isDefault: _isDefault,
        );
      }

      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(widget.isEditing
                  ? 'Instance updated'
                  : 'Instance created')),
        );
        context.pop(true); // Return true to signal refresh needed
      }
    } catch (e) {
      setState(() => _isSaving = false);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to save: $e')),
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
            child: const Text('Delete',
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
          const SnackBar(content: Text('Instance deleted')),
        );
        context.pop(true);
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to delete: $e')),
        );
      }
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
            SegmentedButton<String>(
              segments: const [
                ButtonSegment(value: 'radarr', label: Text('Radarr')),
                ButtonSegment(value: 'sonarr', label: Text('Sonarr')),
              ],
              selected: {_serviceType},
              onSelectionChanged: (value) =>
                  setState(() => _serviceType = value.first),
            ),
            const SizedBox(height: 24),
          ],

          TextField(
            controller: _nameController,
            decoration: const InputDecoration(
              labelText: 'Name',
              hintText: 'e.g. Movies, 4K Movies',
            ),
          ),
          const SizedBox(height: 16),

          TextField(
            controller: _urlController,
            decoration: const InputDecoration(
              labelText: 'URL',
              hintText: 'http://192.168.1.100:7878',
            ),
            keyboardType: TextInputType.url,
          ),
          const SizedBox(height: 16),

          TextField(
            controller: _apiKeyController,
            decoration: const InputDecoration(
              labelText: 'API Key',
              hintText: 'Your Radarr/Sonarr API key',
            ),
            obscureText: true,
          ),
          const SizedBox(height: 16),

          SwitchListTile(
            title: const Text('Default Instance',
                style: TextStyle(color: AppTheme.textPrimary)),
            subtitle: const Text(
                'Use this as the default for media requests',
                style: TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13)),
            value: _isDefault,
            onChanged: (value) => setState(() => _isDefault = value),
            activeTrackColor: AppTheme.accent,
          ),

          const SizedBox(height: 24),

          // Test connection button
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
