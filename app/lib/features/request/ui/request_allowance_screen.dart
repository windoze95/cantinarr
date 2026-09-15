import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_quota.dart';
import '../logic/request_quota_provider.dart';

String allowanceDate(BuildContext context, DateTime value) {
  final local = value.toLocal();
  final l = MaterialLocalizations.of(context);
  return '${l.formatMediumDate(local)} at ${l.formatTimeOfDay(TimeOfDay.fromDateTime(local), alwaysUse24HourFormat: MediaQuery.alwaysUse24HourFormatOf(context))}';
}

class RequestAllowanceScreen extends ConsumerWidget {
  const RequestAllowanceScreen({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) => Scaffold(
    appBar: AppBar(title: const Text('Request allowance')),
    body: const CenteredContent(child: SingleChildScrollView(
      padding: EdgeInsets.all(16), child: RequestAllowanceSection(),
    )),
  );
}

/// Embedded in Request Defaults and each user's existing request settings.
/// Editing a complete rule has its own explicit Save action.
class RequestAllowanceSection extends ConsumerWidget {
  final bool editDefaults;
  final int? userId;
  final String? username;
  const RequestAllowanceSection({super.key, this.editDefaults = false,
    this.userId, this.username});

  bool get editable => editDefaults || userId != null;
  String get path => editDefaults ? '/api/admin/request-quotas' : userId != null
      ? '/api/admin/users/$userId/request-quotas' : '/api/me/request-quotas';

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!ref.watch(requestQuotasSupportedProvider)) return const SizedBox.shrink();
    final data = ref.watch(requestQuotaViewProvider(path));
    return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
      Text(editable ? 'Request allowances' : 'Your allowance',
          style: Theme.of(context).textTheme.titleMedium),
      const SizedBox(height: 8),
      Text(editDefaults
          ? 'Defaults for User and kids accounts. Admins are exempt. Each allowance is shared across libraries.'
          : 'Rolling windows count accepted requests, including requests awaiting approval. TV counts seasons; books count each format separately.',
          style: Theme.of(context).textTheme.bodySmall),
      const SizedBox(height: 8),
      data.when(
        loading: () => const LinearProgressIndicator(),
        error: (e, s) => Row(children: [
          const Expanded(child: Text('Request allowances could not be loaded.')),
          TextButton(onPressed: () => ref.invalidate(requestQuotaViewProvider(path)), child: const Text('Retry')),
        ]),
        data: (view) => Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          if (view.exempt) const Padding(padding: EdgeInsets.symmetric(vertical: 8),
              child: Text('Admins have unlimited requests. These rules apply if the account becomes a User.')),
          for (final a in view.allowances) ListTile(
            contentPadding: EdgeInsets.zero,
            title: Text(a.label),
            subtitle: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text('${userId != null ? a.source == 'default' ? 'Inherits User default: ' : 'User override: ' : ''}${a.ruleLabel}'),
              if (!editDefaults) ...[
                Text(view.exempt ? '${a.used} recent units · Admin exempt' :
                    a.count == null ? '${a.used} accepted in the last ${a.windowDays} days' :
                    '${a.used} used · ${a.remaining ?? 0} remaining'),
                if (a.nextReplenishesAt != null && !view.exempt && a.count != null)
                  Text('Next returns ${allowanceDate(context, a.nextReplenishesAt!)}'),
                if (a.fullyReplenishesAt != null && !view.exempt && a.count != null)
                  Text('Fully replenishes ${allowanceDate(context, a.fullyReplenishesAt!)}'),
                if (a.used == 0 && a.count != null && !view.exempt) const Text('Fully replenished'),
              ],
            ]),
            trailing: editable ? IconButton(tooltip: 'Edit ${a.label} allowance',
                icon: const Icon(Icons.edit_outlined), onPressed: () => _edit(context, ref, a)) : null,
          ),
          if (userId != null && !view.exempt) TextButton.icon(
            icon: const Icon(Icons.restart_alt), label: const Text('Reset selected allowances'),
            onPressed: view.allowances.any((a) => a.used > 0)
                ? () => _reset(context, ref, view) : null,
          ),
        ]),
      ),
    ]);
  }

  Future<void> _edit(BuildContext context, WidgetRef ref, RequestAllowance a) async {
    final saved = await showDialog<bool>(context: context, builder: (_) =>
        _AllowanceRuleDialog(allowance: a, path: path, userOverride: userId != null));
    if (saved == true) ref.invalidate(requestQuotaViewProvider(path));
  }

  Future<void> _reset(BuildContext context, WidgetRef ref, RequestQuotaView view) async {
    final selected = <String>{};
    final available = view.allowances.where((a) => a.used > 0).toList();
    final confirmed = await showDialog<bool>(context: context, builder: (dialogContext) =>
      StatefulBuilder(builder: (context, setState) => AlertDialog(
        title: Text('Reset allowance for ${username ?? 'this user'}?'),
        content: SingleChildScrollView(child: Column(mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start, children: [
          const Text('Choose the usage to clear. This reset is recorded in the administrator audit.'),
          for (final a in available) CheckboxListTile(
            contentPadding: EdgeInsets.zero,
            value: selected.contains(a.id), title: Text(a.label),
            subtitle: Text(a.count == null ? 'Clear ${a.used} recent units; unlimited remains unlimited.' :
                'Clear ${a.used} used units; restore ${a.count! - (a.remaining ?? 0)} units to give ${a.count} remaining.'),
            onChanged: (v) => setState(() { if (v == true) { selected.add(a.id); } else { selected.remove(a.id); } }),
          ),
        ])),
        actions: [
          TextButton(onPressed: () => Navigator.pop(dialogContext, false), child: const Text('Cancel')),
          FilledButton(onPressed: selected.isEmpty ? null : () => Navigator.pop(dialogContext, true), child: const Text('Reset selected')),
        ],
      )),
    );
    if (confirmed != true || !context.mounted) return;
    try {
      await ref.read(requestQuotaServiceProvider).reset(userId!, available.where((a) => selected.contains(a.id)).toList());
      ref.invalidate(requestQuotaViewProvider(path));
      if (context.mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Selected allowances reset')));
    } catch (e) {
      if (context.mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(_allowanceError(e))));
    }
  }
}

String _allowanceError(Object e) {
  if (e is DioException && e.response?.data is Map && e.response!.data['error'] is String) {
    return e.response!.data['error'] as String;
  }
  return 'Could not save the allowance. Please retry.';
}

class _AllowanceRuleDialog extends ConsumerStatefulWidget {
  final RequestAllowance allowance;
  final String path;
  final bool userOverride;
  const _AllowanceRuleDialog({required this.allowance, required this.path, required this.userOverride});
  @override
  ConsumerState<_AllowanceRuleDialog> createState() => _AllowanceRuleDialogState();
}

class _AllowanceRuleDialogState extends ConsumerState<_AllowanceRuleDialog> {
  late final TextEditingController _count;
  late String _mode;
  late int _days;
  bool _saving = false;
  String? _error;
  @override
  void initState() {
    super.initState();
    _count = TextEditingController(text: '${widget.allowance.count ?? 5}');
    _days = widget.allowance.windowDays;
    _mode = widget.userOverride && widget.allowance.source == 'default' ? 'inherit' :
        widget.allowance.count == null ? 'unlimited' : 'limited';
  }
  @override
  void dispose() { _count.dispose(); super.dispose(); }

  Future<void> _save() async {
    final count = int.tryParse(_count.text.trim());
    if (_mode == 'limited' && (count == null || count < 0 || count > 1000000)) {
      setState(() => _error = 'Enter a whole number from 0 to 1000000.'); return;
    }
    setState(() { _saving = true; _error = null; });
    try {
      await ref.read(requestQuotaServiceProvider).save(widget.path, {
        ...widget.allowance.keyJson, 'inherit': _mode == 'inherit',
        'count': _mode == 'limited' ? count : null, 'window_days': _days,
      });
      if (mounted) Navigator.pop(context, true);
    } catch (e) { if (mounted) setState(() { _saving = false; _error = _allowanceError(e); }); }
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: Text('${widget.allowance.label} allowance'),
    content: SingleChildScrollView(child: Column(mainAxisSize: MainAxisSize.min, children: [
      DropdownButtonFormField<String>(initialValue: _mode,
        decoration: const InputDecoration(labelText: 'Rule'),
        items: [
          if (widget.userOverride) const DropdownMenuItem(value: 'inherit', child: Text('Inherit User default')),
          const DropdownMenuItem(value: 'unlimited', child: Text('Unlimited')),
          const DropdownMenuItem(value: 'limited', child: Text('Set allowance')),
        ], onChanged: _saving ? null : (v) => setState(() => _mode = v!),
      ),
      if (_mode == 'limited') ...[
        const SizedBox(height: 12),
        TextField(controller: _count, enabled: !_saving, keyboardType: TextInputType.number,
            decoration: const InputDecoration(labelText: 'Units', helperText: 'Zero allows no new units.')),
        const SizedBox(height: 12),
        DropdownButtonFormField<int>(initialValue: _days,
          decoration: const InputDecoration(labelText: 'Rolling window'),
          items: [for (final d in [1, 7, 30]) DropdownMenuItem(value: d, child: Text('$d ${d == 1 ? 'day' : 'days'}'))],
          onChanged: _saving ? null : (v) => setState(() => _days = v!),
        ),
      ],
      if (_error != null) Padding(padding: const EdgeInsets.only(top: 12),
          child: Text(_error!, style: const TextStyle(color: AppTheme.error))),
    ])),
    actions: [
      TextButton(onPressed: _saving ? null : () => Navigator.pop(context), child: const Text('Cancel')),
      FilledButton(onPressed: _saving ? null : _save, child: Text(_saving ? 'Saving…' : 'Save allowance')),
    ],
  );
}
