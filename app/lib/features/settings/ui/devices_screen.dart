import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:intl/intl.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/storage/secure_storage.dart';
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
  String? _currentDeviceId;
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
      final currentId = await ref
          .read(storageServiceProvider)
          .read(key: StorageKeys.deviceId);
      setState(() {
        _devices = devices;
        _currentDeviceId = currentId;
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = context.l10n.settingsFailedToLoadDevices;
        _isLoading = false;
      });
    }
  }

  Future<void> _revokeDevice(DeviceInfo device) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n.settingsRevokeDevice),
        content: Text(
          context.l10n.settingsMessageRevokeValue1Value2(
                  (device.deviceName).toString(),
                  (device.username).toString()) +
              context.l10n.settingsTheDeviceWillLoseAccessWithin15Minutes,
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
            child: Text(context.l10n.settingsRevoke),
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
          SnackBar(content: Text(context.l10n.settingsDeviceRevoked)),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(context.l10n
                  .settingsMessageFailedToRevokeDeviceValue1((e).toString()))),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.settingsConnectedDevices)),
      body: CenteredContent(child: _buildBody()),
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
              child: Text(context.l10n.settingsRetry),
            ),
          ],
        ),
      );
    }

    final devices = _devices ?? [];
    if (devices.isEmpty) {
      return Center(
        child: Text(
          context.l10n.settingsNoConnectedDevices,
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
              ...userDevices.map((device) {
                final isCurrent = device.id == _currentDeviceId;
                return Dismissible(
                  key: Key(device.id),
                  direction: DismissDirection.endToStart,
                  background: Container(
                    alignment: Alignment.centerRight,
                    padding: const EdgeInsets.only(right: 20),
                    color: AppTheme.error,
                    child: const Icon(
                      Icons.delete,
                      color: AppTheme.background,
                    ),
                  ),
                  confirmDismiss: (_) async {
                    await _revokeDevice(device);
                    return false; // We handle removal via _loadDevices
                  },
                  child: ListTile(
                    leading: Icon(
                      _deviceIcon(device.deviceName),
                      color:
                          isCurrent ? AppTheme.accent : AppTheme.textSecondary,
                    ),
                    title: Row(
                      children: [
                        Flexible(
                          child: Text(
                            device.deviceName,
                            style: const TextStyle(
                              color: AppTheme.textPrimary,
                              fontWeight: FontWeight.w500,
                            ),
                          ),
                        ),
                        if (isCurrent) ...[
                          const SizedBox(width: 8),
                          _thisDeviceBadge(),
                        ],
                      ],
                    ),
                    subtitle: Text(
                      context.l10n.settingsMessageLastSeenValue1(
                          (_formatTime(device.lastSeenAt)).toString()),
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
                );
              }),
            ],
          );
        },
      ),
    );
  }

  Widget _thisDeviceBadge() {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.16),
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text(
        context.l10n.settingsThisDevice,
        style: TextStyle(
          color: AppTheme.accent,
          fontSize: 11,
          fontWeight: FontWeight.w700,
        ),
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

      if (diff.inMinutes < 1) return context.l10n.settingsJustNow;
      if (diff.inMinutes < 60)
        return context.l10n
            .settingsMessageValue1MAgo((diff.inMinutes).toString());
      if (diff.inHours < 24)
        return context.l10n
            .settingsMessageValue1HAgo((diff.inHours).toString());
      if (diff.inDays < 7)
        return context.l10n.settingsMessageValue1DAgo((diff.inDays).toString());

      return DateFormat.yMMMd().format(dt);
    } catch (_) {
      return isoTime;
    }
  }
}
