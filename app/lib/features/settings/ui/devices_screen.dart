import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';

/// Admin screen showing all connected devices with revoke capability.
class DevicesScreen extends ConsumerStatefulWidget {
  const DevicesScreen({super.key});

  @override
  ConsumerState<DevicesScreen> createState() => _DevicesScreenState();
}

class _DevicesScreenState extends ConsumerState<DevicesScreen> {
  List<DeviceInfo>? _devices;
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadDevices();
  }

  Future<void> _loadDevices() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final devices = await ref.read(authProvider.notifier).listDevices();
      setState(() {
        _devices = devices;
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = 'Failed to load devices';
        _isLoading = false;
      });
    }
  }

  Future<void> _revokeDevice(DeviceInfo device) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Revoke Device'),
        content: Text(
          'Revoke "${device.deviceName}" (${device.username})? '
          'The device will lose access within 15 minutes.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(
              backgroundColor: AppTheme.error,
            ),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Revoke'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    try {
      await ref.read(authProvider.notifier).revokeDevice(device.id);
      await _loadDevices();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Device revoked')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to revoke device: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Connected Devices')),
      body: _buildBody(),
    );
  }

  Widget _buildBody() {
    if (_isLoading) {
      return const Center(child: CircularProgressIndicator());
    }

    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_error!, style: const TextStyle(color: AppTheme.error)),
            const SizedBox(height: 16),
            ElevatedButton(
              onPressed: _loadDevices,
              child: const Text('Retry'),
            ),
          ],
        ),
      );
    }

    final devices = _devices ?? [];
    if (devices.isEmpty) {
      return const Center(
        child: Text(
          'No connected devices',
          style: TextStyle(color: AppTheme.textSecondary),
        ),
      );
    }

    // Group devices by username
    final grouped = <String, List<DeviceInfo>>{};
    for (final device in devices) {
      grouped.putIfAbsent(device.username, () => []).add(device);
    }

    return RefreshIndicator(
      onRefresh: _loadDevices,
      child: ListView.builder(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: grouped.length,
        itemBuilder: (context, index) {
          final username = grouped.keys.elementAt(index);
          final userDevices = grouped[username]!;

          return Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Padding(
                padding:
                    const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
                child: Text(
                  username.toUpperCase(),
                  style: const TextStyle(
                    color: AppTheme.accent,
                    fontSize: 12,
                    fontWeight: FontWeight.w700,
                    letterSpacing: 1.2,
                  ),
                ),
              ),
              ...userDevices.map((device) => Dismissible(
                    key: Key(device.id),
                    direction: DismissDirection.endToStart,
                    background: Container(
                      alignment: Alignment.centerRight,
                      padding: const EdgeInsets.only(right: 20),
                      color: AppTheme.error,
                      child: const Icon(Icons.delete, color: Colors.white),
                    ),
                    confirmDismiss: (_) async {
                      await _revokeDevice(device);
                      return false; // We handle removal via _loadDevices
                    },
                    child: ListTile(
                      leading: Icon(
                        _deviceIcon(device.deviceName),
                        color: AppTheme.textSecondary,
                      ),
                      title: Text(
                        device.deviceName,
                        style: const TextStyle(
                          color: AppTheme.textPrimary,
                          fontWeight: FontWeight.w500,
                        ),
                      ),
                      subtitle: Text(
                        'Last seen ${_formatTime(device.lastSeenAt)}',
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 13,
                        ),
                      ),
                      trailing: IconButton(
                        icon: const Icon(Icons.close,
                            color: AppTheme.textSecondary, size: 20),
                        onPressed: () => _revokeDevice(device),
                      ),
                    ),
                  )),
            ],
          );
        },
      ),
    );
  }

  IconData _deviceIcon(String name) {
    final lower = name.toLowerCase();
    if (lower.contains('iphone') || lower.contains('ipad')) {
      return Icons.phone_iphone;
    }
    if (lower.contains('android')) return Icons.phone_android;
    if (lower.contains('mac')) return Icons.laptop_mac;
    if (lower.contains('windows')) return Icons.laptop_windows;
    if (lower.contains('admin')) return Icons.admin_panel_settings;
    return Icons.devices;
  }

  String _formatTime(String isoTime) {
    try {
      final dt = DateTime.parse(isoTime);
      final now = DateTime.now();
      final diff = now.difference(dt);

      if (diff.inMinutes < 1) return 'just now';
      if (diff.inMinutes < 60) return '${diff.inMinutes}m ago';
      if (diff.inHours < 24) return '${diff.inHours}h ago';
      if (diff.inDays < 7) return '${diff.inDays}d ago';

      return DateFormat.yMMMd().format(dt);
    } catch (_) {
      return isoTime;
    }
  }
}
