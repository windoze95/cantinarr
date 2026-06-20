import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/data/auth_service.dart';
import '../../auth/logic/auth_provider.dart';

/// Admin screen for managing user accounts: change roles, remove users, and
/// see who still has an outstanding connect-link invite.
class UsersScreen extends ConsumerStatefulWidget {
  const UsersScreen({super.key});

  @override
  ConsumerState<UsersScreen> createState() => _UsersScreenState();
}

class _UsersScreenState extends ConsumerState<UsersScreen> {
  List<UserSummary>? _users;
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    _loadUsers();
  }

  Future<void> _loadUsers() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final users = await ref.read(authProvider.notifier).listUsers();
      setState(() {
        _users = users;
        _isLoading = false;
      });
    } catch (e) {
      setState(() {
        _error = 'Failed to load users';
        _isLoading = false;
      });
    }
  }

  Future<void> _changeRole(UserSummary user, String newRole) async {
    if (newRole == user.role) return;
    try {
      await ref.read(authProvider.notifier).updateUserRole(user.id, newRole);
      await _loadUsers();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(
              newRole == 'admin'
                  ? '${user.username} is now an admin'
                  : '${user.username} is now a user',
            ),
          ),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_friendlyError(e, 'Failed to change role'))),
        );
      }
    }
  }

  Future<void> _deleteUser(UserSummary user) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Remove User'),
        content: Text(
          'Remove "${user.username}"? This deletes their account, devices, '
          'and any pending invites. This cannot be undone.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          ElevatedButton(
            style: ElevatedButton.styleFrom(backgroundColor: AppTheme.error),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Remove'),
          ),
        ],
      ),
    );

    if (confirmed != true) return;

    try {
      await ref.read(authProvider.notifier).deleteUser(user.id);
      await _loadUsers();
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Removed ${user.username}')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(_friendlyError(e, 'Failed to remove user'))),
        );
      }
    }
  }

  String _friendlyError(Object e, String fallback) {
    final msg = e.toString();
    // Surface the backend's error message when present.
    final match = RegExp(r'"error":"([^"]+)"').firstMatch(msg);
    return match != null ? match.group(1)! : fallback;
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Users')),
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
            ElevatedButton(onPressed: _loadUsers, child: const Text('Retry')),
          ],
        ),
      );
    }

    final users = _users ?? [];
    if (users.isEmpty) {
      return const Center(
        child: Text(
          'No users yet',
          style: TextStyle(color: AppTheme.textSecondary),
        ),
      );
    }

    final currentUserId = ref.read(authProvider).valueOrNull?.user?.id;

    return RefreshIndicator(
      onRefresh: _loadUsers,
      child: ListView.separated(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: users.length,
        separatorBuilder: (_, __) =>
            const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (context, index) {
          final user = users[index];
          return _UserTile(
            user: user,
            isSelf: user.id == currentUserId,
            onChangeRole: (role) => _changeRole(user, role),
            onDelete: () => _deleteUser(user),
          );
        },
      ),
    );
  }
}

class _UserTile extends StatelessWidget {
  const _UserTile({
    required this.user,
    required this.isSelf,
    required this.onChangeRole,
    required this.onDelete,
  });

  final UserSummary user;
  final bool isSelf;
  final ValueChanged<String> onChangeRole;
  final VoidCallback onDelete;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: user.isAdmin
            ? AppTheme.accent.withValues(alpha: 0.2)
            : AppTheme.surfaceVariant,
        child: Icon(
          user.isAdmin ? Icons.admin_panel_settings : Icons.person,
          color: user.isAdmin ? AppTheme.accent : AppTheme.textSecondary,
          size: 20,
        ),
      ),
      title: Row(
        children: [
          Flexible(
            child: Text(
              user.username,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontWeight: FontWeight.w600,
              ),
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (isSelf) ...[
            const SizedBox(width: 8),
            _Tag(label: 'You', color: AppTheme.accent),
          ],
        ],
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Wrap(
          spacing: 6,
          runSpacing: 4,
          children: [
            _Tag(
              label: user.isAdmin ? 'Admin' : 'User',
              color: user.isAdmin ? AppTheme.accent : AppTheme.textSecondary,
            ),
            if (user.hasPendingInvite)
              _Tag(label: 'Invited', color: AppTheme.requested),
            _Tag(
              label: user.deviceCount == 1
                  ? '1 device'
                  : '${user.deviceCount} devices',
              color: user.deviceCount > 0
                  ? AppTheme.available
                  : AppTheme.unavailable,
            ),
          ],
        ),
      ),
      trailing: _buildMenu(context),
    );
  }

  Widget _buildMenu(BuildContext context) {
    return PopupMenuButton<String>(
      icon: const Icon(Icons.more_vert, color: AppTheme.textSecondary),
      onSelected: (value) {
        switch (value) {
          case 'make_admin':
            onChangeRole('admin');
            break;
          case 'make_user':
            onChangeRole('user');
            break;
          case 'delete':
            onDelete();
            break;
        }
      },
      itemBuilder: (context) => [
        if (!user.isAdmin)
          const PopupMenuItem(
            value: 'make_admin',
            child: ListTile(
              leading: Icon(Icons.arrow_upward),
              title: Text('Make admin'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (user.isAdmin && !isSelf)
          const PopupMenuItem(
            value: 'make_user',
            child: ListTile(
              leading: Icon(Icons.arrow_downward),
              title: Text('Make user'),
              contentPadding: EdgeInsets.zero,
            ),
          ),
        if (!isSelf)
          const PopupMenuItem(
            value: 'delete',
            child: ListTile(
              leading: Icon(Icons.delete_outline, color: AppTheme.error),
              title: Text('Remove', style: TextStyle(color: AppTheme.error)),
              contentPadding: EdgeInsets.zero,
            ),
          ),
      ],
    );
  }
}

class _Tag extends StatelessWidget {
  const _Tag({required this.label, required this.color});

  final String label;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
