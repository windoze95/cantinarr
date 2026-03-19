import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../data/auth_service.dart';
import '../logic/auth_provider.dart';

/// Settings sub-screen for managing registered passkeys.
class PasskeyManagementScreen extends ConsumerStatefulWidget {
  const PasskeyManagementScreen({super.key});

  @override
  ConsumerState<PasskeyManagementScreen> createState() =>
      _PasskeyManagementScreenState();
}

class _PasskeyManagementScreenState
    extends ConsumerState<PasskeyManagementScreen> {
  List<PasskeyInfoResponse>? _passkeys;
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadPasskeys();
  }

  Future<void> _loadPasskeys() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final passkeys = await ref.read(authProvider.notifier).listPasskeys();
      if (mounted) {
        setState(() {
          _passkeys = passkeys;
          _isLoading = false;
        });
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = 'Failed to load passkeys';
          _isLoading = false;
        });
      }
    }
  }

  Future<void> _addPasskey() async {
    final nameController = TextEditingController(text: 'Passkey');
    final name = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Add Passkey'),
        content: TextField(
          controller: nameController,
          decoration: const InputDecoration(
            labelText: 'Name',
            hintText: 'e.g. MacBook, iPhone',
            prefixIcon: Icon(Icons.label_outline),
          ),
          textCapitalization: TextCapitalization.words,
          autofocus: true,
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            onPressed: () {
              final name = nameController.text.trim();
              if (name.isNotEmpty) Navigator.of(context).pop(name);
            },
            child: const Text('Continue'),
          ),
        ],
      ),
    );

    if (name == null || !mounted) return;

    try {
      await ref.read(authProvider.notifier).registerPasskey(name);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Passkey registered')),
        );
        _loadPasskeys();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to register passkey: $e')),
        );
      }
    }
  }

  Future<void> _deletePasskey(PasskeyInfoResponse passkey) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Remove Passkey'),
        content: Text('Remove "${passkey.name}"? You won\'t be able to use it to sign in anymore.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: AppTheme.error),
            onPressed: () => Navigator.of(context).pop(true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );

    if (confirmed != true || !mounted) return;

    try {
      await ref.read(authProvider.notifier).deletePasskey(passkey.id);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Passkey removed')),
        );
        _loadPasskeys();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to remove passkey: $e')),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Passkeys')),
      body: _isLoading
          ? const Center(child: CircularProgressIndicator())
          : _error != null
              ? Center(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(_error!,
                          style: const TextStyle(color: AppTheme.error)),
                      const SizedBox(height: 16),
                      ElevatedButton(
                          onPressed: _loadPasskeys,
                          child: const Text('Retry')),
                    ],
                  ),
                )
              : _buildList(),
      floatingActionButton: FloatingActionButton(
        onPressed: _addPasskey,
        backgroundColor: AppTheme.accent,
        foregroundColor: AppTheme.background,
        child: const Icon(Icons.add),
      ),
    );
  }

  Widget _buildList() {
    if (_passkeys == null || _passkeys!.isEmpty) {
      return const Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.fingerprint, size: 48, color: AppTheme.textSecondary),
            SizedBox(height: 16),
            Text('No passkeys registered',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
            SizedBox(height: 8),
            Text('Tap + to add a passkey',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 14)),
          ],
        ),
      );
    }

    return ListView.builder(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: _passkeys!.length,
      itemBuilder: (context, index) {
        final passkey = _passkeys![index];
        return Dismissible(
          key: Key(passkey.id),
          direction: DismissDirection.endToStart,
          background: Container(
            alignment: Alignment.centerRight,
            padding: const EdgeInsets.only(right: 20),
            color: AppTheme.error,
            child: const Icon(Icons.delete, color: Colors.white),
          ),
          confirmDismiss: (_) async {
            await _deletePasskey(passkey);
            return false; // We handle the removal ourselves
          },
          child: ListTile(
            leading: const Icon(Icons.fingerprint,
                color: AppTheme.accent, size: 28),
            title: Text(passkey.name,
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500)),
            subtitle: Text(
              'Added ${_formatDate(passkey.createdAt)}'
              '${passkey.lastUsedAt != null ? ' \u00b7 Last used ${_formatDate(passkey.lastUsedAt!)}' : ''}',
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 13),
            ),
            trailing: IconButton(
              icon:
                  const Icon(Icons.delete_outline, color: AppTheme.textSecondary),
              onPressed: () => _deletePasskey(passkey),
            ),
          ),
        );
      },
    );
  }

  String _formatDate(String isoDate) {
    try {
      final date = DateTime.parse(isoDate);
      final now = DateTime.now();
      final diff = now.difference(date);

      if (diff.inDays == 0) return 'today';
      if (diff.inDays == 1) return 'yesterday';
      if (diff.inDays < 30) return '${diff.inDays}d ago';
      return '${date.month}/${date.day}/${date.year}';
    } catch (_) {
      return isoDate;
    }
  }
}
