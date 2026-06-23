import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_settings_service.dart';
import '../../request/data/request_service.dart';
import '../../request/logic/pending_approvals_provider.dart';

/// Sentinel season-scope value used in the approve dialog meaning "keep the
/// exact seasons the user requested" (no coarse-scope override).
const _keepRequestedScope = '__keep_requested__';

/// Admin approval queue: approve (optionally modifying options) or deny
/// pending media requests.
class PendingRequestsScreen extends ConsumerStatefulWidget {
  const PendingRequestsScreen({super.key});

  @override
  ConsumerState<PendingRequestsScreen> createState() =>
      _PendingRequestsScreenState();
}

class _PendingRequestsScreenState
    extends ConsumerState<PendingRequestsScreen> {
  late final RequestSettingsService _service;
  List<PendingRequestItem>? _pending;
  AdminRequestSettings? _admin;
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = RequestSettingsService(
        backendDio: ref.read(backendClientProvider),
      );
      _load();
    });
  }

  String _friendlyError(Object e) {
    final m = RegExp(r'"error":"([^"]+)"').firstMatch(e.toString());
    return m != null ? m.group(1)! : 'Something went wrong';
  }

  Future<void> _load() async {
    setState(() {
      _isLoading = _pending == null;
      _error = null;
    });
    try {
      final pending = await _service.listPending();
      final admin = await _service.getAdminSettings();
      if (!mounted) return;
      setState(() {
        _pending = pending;
        _admin = admin;
        _isLoading = false;
      });
      // Keep the drawer + app-icon badges in sync with the queue we just loaded
      // (covers opening the screen and the reload after an approve/deny).
      ref.read(pendingApprovalsProvider.notifier).setCount(pending.length);
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _approve(PendingRequestItem item) async {
    final admin = _admin;
    if (admin == null) return;
    final profiles = item.isTv ? admin.sonarrProfiles : admin.radarrProfiles;

    // An explicit per-season request stores a JSON list in seasonScope, which
    // isn't one of the coarse dropdown values — represent it as a "keep
    // requested" option so the dropdown doesn't break and the admin can leave
    // the chosen seasons untouched.
    final isExplicit = SeasonScope.isExplicitList(item.seasonScope);
    String chosenScope = isExplicit
        ? _keepRequestedScope
        : (item.seasonScope.isNotEmpty ? item.seasonScope : SeasonScope.all);
    int? chosenProfile;

    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        return StatefulBuilder(
          builder: (dialogContext, setDialogState) {
            return AlertDialog(
              backgroundColor: AppTheme.surface,
              title: Text(
                'Approve ${item.title}',
                style: const TextStyle(color: AppTheme.textPrimary),
              ),
              content: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (item.isTv) ...[
                    const Text(
                      'Season scope',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 13),
                    ),
                    const SizedBox(height: 4),
                    DropdownButtonFormField<String>(
                      initialValue: chosenScope,
                      dropdownColor: AppTheme.surfaceVariant,
                      style: const TextStyle(color: AppTheme.textPrimary),
                      decoration: const InputDecoration(
                        border: OutlineInputBorder(),
                        isDense: true,
                      ),
                      items: [
                        if (isExplicit)
                          DropdownMenuItem<String>(
                            value: _keepRequestedScope,
                            child: Text(
                                'Keep requested (${SeasonScope.describe(item.seasonScope)})'),
                          ),
                        ...SeasonScope.choices.map((c) =>
                            DropdownMenuItem<String>(
                                value: c.value, child: Text(c.label))),
                      ],
                      onChanged: (v) {
                        if (v != null) {
                          setDialogState(() => chosenScope = v);
                        }
                      },
                    ),
                    const SizedBox(height: 16),
                  ],
                  const Text(
                    'Quality profile',
                    style:
                        TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                  ),
                  const SizedBox(height: 4),
                  DropdownButtonFormField<int?>(
                    initialValue: chosenProfile,
                    dropdownColor: AppTheme.surfaceVariant,
                    style: const TextStyle(color: AppTheme.textPrimary),
                    decoration: const InputDecoration(
                      border: OutlineInputBorder(),
                      isDense: true,
                    ),
                    items: [
                      const DropdownMenuItem<int?>(
                        value: null,
                        child: Text('Default'),
                      ),
                      ...profiles.map((p) => DropdownMenuItem<int?>(
                            value: p.id,
                            child: Text(p.name),
                          )),
                    ],
                    onChanged: (v) => setDialogState(() => chosenProfile = v),
                  ),
                ],
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.of(dialogContext).pop(false),
                  child: const Text('Cancel'),
                ),
                ElevatedButton(
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppTheme.available,
                    foregroundColor: Colors.white,
                  ),
                  onPressed: () => Navigator.of(dialogContext).pop(true),
                  child: const Text('Approve'),
                ),
              ],
            );
          },
        );
      },
    );

    if (confirmed != true) return;
    if (!mounted) return;
    try {
      await _service.approve(
        item.id,
        // The "keep requested" sentinel sends no override, so the server keeps
        // the explicit season list the user picked.
        seasonScope: item.isTv
            ? (chosenScope == _keepRequestedScope ? null : chosenScope)
            : null,
        qualityProfileId: chosenProfile,
      );
      if (!mounted) return;
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Approved ${item.title}')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  Future<void> _deny(PendingRequestItem item) async {
    final controller = TextEditingController();
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (dialogContext) {
        return AlertDialog(
          backgroundColor: AppTheme.surface,
          title: Text(
            'Deny ${item.title}',
            style: const TextStyle(color: AppTheme.textPrimary),
          ),
          content: TextField(
            controller: controller,
            autofocus: true,
            style: const TextStyle(color: AppTheme.textPrimary),
            decoration: const InputDecoration(
              labelText: 'Reason (optional)',
              labelStyle: TextStyle(color: AppTheme.textSecondary),
              border: OutlineInputBorder(),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(dialogContext).pop(false),
              child: const Text('Cancel'),
            ),
            ElevatedButton(
              style: ElevatedButton.styleFrom(
                backgroundColor: AppTheme.error,
                foregroundColor: Colors.white,
              ),
              onPressed: () => Navigator.of(dialogContext).pop(true),
              child: const Text('Deny'),
            ),
          ],
        );
      },
    );

    final reason = controller.text.trim();
    controller.dispose();
    if (confirmed != true) return;
    if (!mounted) return;
    try {
      await _service.deny(item.id, reason: reason.isEmpty ? null : reason);
      if (!mounted) return;
      await _load();
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Denied ${item.title}')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(_friendlyError(e))),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Approvals')),
      body: _isLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent))
          : _error != null && _pending == null
              ? Center(
                  child: Padding(
                    padding: const EdgeInsets.all(24),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(_error!,
                            style: const TextStyle(color: AppTheme.error),
                            textAlign: TextAlign.center),
                        const SizedBox(height: 12),
                        ElevatedButton(
                            onPressed: _load, child: const Text('Retry')),
                      ],
                    ),
                  ),
                )
              : RefreshIndicator(
                  color: AppTheme.accent,
                  onRefresh: _load,
                  child: (_pending?.isEmpty ?? true)
                      ? ListView(
                          physics: const AlwaysScrollableScrollPhysics(),
                          children: const [
                            SizedBox(height: 120),
                            Center(
                              child: Text(
                                'No pending requests.',
                                style:
                                    TextStyle(color: AppTheme.textSecondary),
                              ),
                            ),
                          ],
                        )
                      : ListView.separated(
                          physics: const AlwaysScrollableScrollPhysics(),
                          padding: const EdgeInsets.symmetric(vertical: 8),
                          itemCount: _pending!.length,
                          separatorBuilder: (_, __) =>
                              const Divider(color: AppTheme.border, height: 1),
                          itemBuilder: (context, index) {
                            final item = _pending![index];
                            return _PendingTile(
                              item: item,
                              onApprove: () => _approve(item),
                              onDeny: () => _deny(item),
                            );
                          },
                        ),
                ),
    );
  }
}

class _PendingTile extends StatelessWidget {
  final PendingRequestItem item;
  final VoidCallback onApprove;
  final VoidCallback onDeny;

  const _PendingTile({
    required this.item,
    required this.onApprove,
    required this.onDeny,
  });

  @override
  Widget build(BuildContext context) {
    final showScope = item.isTv && item.seasonScope.isNotEmpty;
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
      title: Text(
        item.title,
        style: const TextStyle(
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.bold,
        ),
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 2),
          Text(
            'Requested by ${item.username}',
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 13),
          ),
          const SizedBox(height: 6),
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              _chip(item.isTv ? 'TV' : 'Movie'),
              if (showScope) _chip(SeasonScope.describe(item.seasonScope)),
            ],
          ),
        ],
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            icon: const Icon(Icons.check_circle_outline),
            color: AppTheme.available,
            tooltip: 'Approve',
            onPressed: onApprove,
          ),
          IconButton(
            icon: const Icon(Icons.cancel_outlined),
            color: AppTheme.error,
            tooltip: 'Deny',
            onPressed: onDeny,
          ),
        ],
      ),
    );
  }

  Widget _chip(String label) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(4),
        border: Border.all(color: AppTheme.border),
      ),
      child: Text(
        label,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 11,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
