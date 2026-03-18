import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/radarr_api_service.dart';

/// Shows upcoming movie releases from Radarr's calendar.
class RadarrCalendarScreen extends ConsumerStatefulWidget {
  const RadarrCalendarScreen({super.key});

  @override
  ConsumerState<RadarrCalendarScreen> createState() =>
      _RadarrCalendarScreenState();
}

class _RadarrCalendarScreenState extends ConsumerState<RadarrCalendarScreen> {
  List<Map<String, dynamic>> _events = [];
  bool _isLoading = true;
  String? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _loadCalendar());
  }

  Future<void> _loadCalendar() async {
    final instanceState = ref.read(instanceProvider);
    final instanceId = instanceState.activeRadarrInstance?.id;
    if (instanceId == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Radarr instance configured';
      });
      return;
    }

    setState(() => _isLoading = true);
    try {
      final backendDio = ref.read(backendClientProvider);
      final service =
          RadarrApiService(backendDio: backendDio, instanceId: instanceId);
      final now = DateTime.now();
      final start = now.subtract(const Duration(days: 7));
      final end = now.add(const Duration(days: 30));
      final events = await service.getCalendar(
        start: start.toIso8601String(),
        end: end.toIso8601String(),
      );
      setState(() {
        _events = events;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      setState(() {
        _isLoading = false;
        _error = 'Failed to load calendar: $e';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(instanceProvider.select((s) => s.activeRadarrInstanceId),
        (_, __) => _loadCalendar());

    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(_error!,
                style: const TextStyle(color: AppTheme.textSecondary)),
            const SizedBox(height: 16),
            ElevatedButton(
                onPressed: _loadCalendar, child: const Text('Retry')),
          ],
        ),
      );
    }
    if (_events.isEmpty) {
      return const Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.calendar_today_outlined,
                size: 48, color: AppTheme.textSecondary),
            SizedBox(height: 12),
            Text('No upcoming releases',
                style: TextStyle(
                    color: AppTheme.textSecondary, fontSize: 16)),
          ],
        ),
      );
    }

    return RefreshIndicator(
      onRefresh: _loadCalendar,
      color: AppTheme.accent,
      child: ListView.builder(
        padding: const EdgeInsets.symmetric(vertical: 8),
        itemCount: _events.length,
        itemBuilder: (context, index) {
          final event = _events[index];
          final title = event['title'] as String? ?? 'Unknown';
          final date = event['inCinemas'] as String? ??
              event['digitalRelease'] as String? ??
              event['physicalRelease'] as String? ??
              '';
          final hasFile = event['hasFile'] as bool? ?? false;

          return ListTile(
            leading: Icon(
              hasFile ? Icons.check_circle : Icons.calendar_today,
              color: hasFile ? AppTheme.available : AppTheme.accent,
            ),
            title: Text(title,
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500)),
            subtitle: Text(
              date.isNotEmpty ? date.substring(0, 10) : 'TBA',
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 13),
            ),
          );
        },
      ),
    );
  }
}
