import 'package:cantinarr/l10n/auth_messages.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
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
          _error = context.l10n.authLoadPasskeysError;
          _isLoading = false;
        });
      }
    }
  }

  Future<void> _addPasskey() async {
    final created = await context.push<bool>('/settings/passkeys/new');
    if (created == true && mounted) {
      _loadPasskeys();
    }
  }

  Future<void> _deletePasskey(PasskeyInfoResponse passkey) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(context.l10n.authRemovePasskey),
        content: Text(context.l10n.authRemovePasskeyPrompt(passkey.name)),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(false),
            child: Text(context.l10n.authCancel),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: AppTheme.error),
            onPressed: () => Navigator.of(context).pop(true),
            child: Text(context.l10n.authRemove),
          ),
        ],
      ),
    );

    if (confirmed != true || !mounted) return;

    try {
      await ref.read(authProvider.notifier).deletePasskey(passkey.id);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(context.l10n.authPasskeyRemoved)),
        );
        _loadPasskeys();
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
              content: Text(context.l10n.authRemovePasskeyError(e.toString()))),
        );
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(context.l10n.authPasskeys)),
      body: CenteredContent(
          child: _isLoading
              ? const Center(child: CircularProgressIndicator())
              : _error != null
                  ? Center(
                      child: Column(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          Text(localizeAuthMessage(context, _error!),
                              style: const TextStyle(color: AppTheme.error)),
                          const SizedBox(height: 16),
                          ElevatedButton(
                              onPressed: _loadPasskeys,
                              child: Text(context.l10n.authRetry)),
                        ],
                      ),
                    )
                  : _buildList()),
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
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.fingerprint, size: 48, color: AppTheme.textSecondary),
            SizedBox(height: 16),
            Text(context.l10n.authNoPasskeys,
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
            SizedBox(height: 8),
            Text(context.l10n.authAddPasskeyHint,
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
            child: const Icon(Icons.delete, color: AppTheme.background),
          ),
          confirmDismiss: (_) async {
            await _deletePasskey(passkey);
            return false; // We handle the removal ourselves
          },
          child: ListTile(
            leading:
                const Icon(Icons.fingerprint, color: AppTheme.accent, size: 28),
            title: Text(passkey.name,
                style: const TextStyle(
                    color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
            subtitle: Text(
              context.l10n.authPasskeyAdded(_formatDate(passkey.createdAt)) +
                  (passkey.lastUsedAt != null
                      ? context.l10n
                          .authPasskeyLastUsed(_formatDate(passkey.lastUsedAt!))
                      : ''),
              style:
                  const TextStyle(color: AppTheme.textSecondary, fontSize: 13),
            ),
            trailing: IconButton(
              icon: const Icon(Icons.delete_outline,
                  color: AppTheme.textSecondary),
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

      if (diff.inDays == 0) return context.l10n.authToday;
      if (diff.inDays == 1) return context.l10n.authYesterday;
      if (diff.inDays < 30) return context.l10n.authDaysAgo(diff.inDays);
      return MaterialLocalizations.of(context).formatMediumDate(date);
    } catch (_) {
      return isoDate;
    }
  }
}
