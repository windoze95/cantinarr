import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../data/ai_tools_service.dart';

/// Admin screen for enabling/disabling AI assistant tools.
class AiToolsScreen extends ConsumerStatefulWidget {
  const AiToolsScreen({super.key});

  @override
  ConsumerState<AiToolsScreen> createState() => _AiToolsScreenState();
}

class _AiToolsScreenState extends ConsumerState<AiToolsScreen> {
  late final AiToolsService _service;
  List<AiToolInfo>? _tools;
  AiDebugStatus _debug = const AiDebugStatus(enabled: false);
  bool _isLoading = true;
  String? _error;
  bool _debugPending = false;

  /// Tools with a toggle in flight (switch disabled while pending).
  final Set<String> _pending = {};

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _service = AiToolsService(
        backendDio: ref.read(backendClientProvider),
      );
      _loadTools();
    });
  }

  Future<void> _loadTools() async {
    setState(() {
      _isLoading = _tools == null;
      _error = null;
    });
    try {
      final status = await _service.getStatus();
      if (!mounted) return;
      setState(() {
        _tools = status.tools;
        _debug = status.debug;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.toString();
        _isLoading = false;
      });
    }
  }

  Future<void> _setDebug(bool enabled, {int hours = 1}) async {
    if (_debugPending) return;
    setState(() => _debugPending = true);
    try {
      final debug = await _service.setDebug(enabled: enabled, hours: hours);
      if (!mounted) return;
      setState(() {
        _debug = debug;
        _debugPending = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() => _debugPending = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to update AI debug logging: $e')),
      );
    }
  }

  Future<void> _toggleTool(AiToolInfo tool, bool enabled) async {
    final tools = _tools;
    if (tools == null || _pending.contains(tool.name)) return;

    // Optimistic update
    final idx = tools.indexWhere((t) => t.name == tool.name);
    if (idx < 0) return;
    setState(() {
      tools[idx] = tool.copyWith(enabled: enabled);
      _pending.add(tool.name);
    });

    try {
      await _service.setEnabled(tool.name, enabled);
      if (!mounted) return;
      setState(() => _pending.remove(tool.name));
    } catch (e) {
      if (!mounted) return;
      // Revert on failure
      setState(() {
        final revertIdx = _tools?.indexWhere((t) => t.name == tool.name) ?? -1;
        if (revertIdx >= 0) {
          _tools![revertIdx] = tool.copyWith(enabled: !enabled);
        }
        _pending.remove(tool.name);
      });
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(
            content: Text(
                'Failed to update ${tool.displayName}: ${e.toString().length > 80 ? '${e.toString().substring(0, 80)}...' : e}')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('AI Tools')),
      body: _isLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent))
          : _error != null && _tools == null
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
                            onPressed: _loadTools, child: const Text('Retry')),
                      ],
                    ),
                  ),
                )
              : RefreshIndicator(
                  color: AppTheme.accent,
                  onRefresh: _loadTools,
                  child: ListView(
                    physics: const AlwaysScrollableScrollPhysics(),
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    children: [
                      const Padding(
                        padding: EdgeInsets.fromLTRB(16, 8, 16, 12),
                        child: Text(
                          'Control which tools the AI assistant can use. Disabled tools are hidden from the assistant entirely.',
                          style: TextStyle(
                              color: AppTheme.textSecondary, fontSize: 13),
                        ),
                      ),
                      _DebugLoggingTile(
                        status: _debug,
                        pending: _debugPending,
                        onToggle: (enabled) => _setDebug(enabled),
                        onExtend: () => _setDebug(true),
                      ),
                      const Divider(color: AppTheme.border),
                      if (_tools?.isEmpty ?? false)
                        const Padding(
                          padding: EdgeInsets.all(24),
                          child: Center(
                            child: Text(
                              'No AI tools reported by the server.',
                              style: TextStyle(color: AppTheme.textSecondary),
                            ),
                          ),
                        ),
                      ...?_tools?.map((tool) => _ToolTile(
                            tool: tool,
                            pending: _pending.contains(tool.name),
                            onChanged: (v) => _toggleTool(tool, v),
                          )),
                      const SizedBox(height: 32),
                    ],
                  ),
                ),
    );
  }
}

class _DebugLoggingTile extends StatelessWidget {
  final AiDebugStatus status;
  final bool pending;
  final ValueChanged<bool> onToggle;
  final VoidCallback onExtend;

  const _DebugLoggingTile({
    required this.status,
    required this.pending,
    required this.onToggle,
    required this.onExtend,
  });

  @override
  Widget build(BuildContext context) {
    final until = status.enabledUntil;
    final untilLabel = until == null
        ? null
        : 'Ends ${TimeOfDay.fromDateTime(until).format(context)}';

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Container(
        decoration: BoxDecoration(
          color: AppTheme.surfaceVariant,
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: AppTheme.border),
        ),
        child: Column(
          children: [
            SwitchListTile(
              value: status.enabled,
              onChanged: pending ? null : onToggle,
              activeThumbColor: AppTheme.accent,
              title: const Text(
                'AI Debug Logging',
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontWeight: FontWeight.w600,
                ),
              ),
              subtitle: Text(
                [
                  status.remainingLabel,
                  if (untilLabel != null) untilLabel,
                ].join(' • '),
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
              child: Row(
                children: [
                  const Expanded(
                    child: Text(
                      'Temporarily logs AI prompts, tool arguments, and tool results on the server. It shuts off automatically.',
                      style: TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    ),
                  ),
                  const SizedBox(width: 12),
                  OutlinedButton.icon(
                    onPressed: pending ? null : onExtend,
                    icon: const Icon(Icons.add, size: 16),
                    label: Text(status.enabled ? '+1 hour' : '1 hour'),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

class _ToolTile extends StatelessWidget {
  final AiToolInfo tool;
  final bool pending;
  final ValueChanged<bool> onChanged;

  const _ToolTile({
    required this.tool,
    required this.pending,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    return SwitchListTile(
      value: tool.enabled,
      onChanged: pending ? null : onChanged,
      activeThumbColor: AppTheme.accent,
      title: Row(
        children: [
          Flexible(
            child: Text(
              tool.displayName,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontWeight: FontWeight.w500,
              ),
            ),
          ),
          if (tool.adminOnly) ...[
            const SizedBox(width: 8),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: AppTheme.accent.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
                border:
                    Border.all(color: AppTheme.accent.withValues(alpha: 0.4)),
              ),
              child: const Text(
                'Admin only',
                style: TextStyle(
                  color: AppTheme.accent,
                  fontSize: 10,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ],
        ],
      ),
      subtitle: tool.description.isEmpty
          ? null
          : Padding(
              padding: const EdgeInsets.only(top: 2),
              child: Text(
                tool.description,
                style: const TextStyle(
                    color: AppTheme.textSecondary, fontSize: 13),
              ),
            ),
    );
  }
}
