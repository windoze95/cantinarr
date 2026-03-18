import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../data/sonarr_api_service.dart';

/// Shows upcoming episode releases from Sonarr's calendar.
class SonarrCalendarScreen extends ConsumerStatefulWidget {
  const SonarrCalendarScreen({super.key});

  @override
  ConsumerState<SonarrCalendarScreen> createState() =>
      _SonarrCalendarScreenState();
}

class _SonarrCalendarScreenState extends ConsumerState<SonarrCalendarScreen> {
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
    final instanceId = instanceState.activeSonarrInstance?.id;
    if (instanceId == null) {
      setState(() {
        _isLoading = false;
        _error = 'No Sonarr instance configured';
      });
      return;
    }

    setState(() => _isLoading = true);
    try {
      final backendDio = ref.read(backendClientProvider);
      final service =
          SonarrApiService(backendDio: backendDio, instanceId: instanceId);
      final now = DateTime.now();
      final start = now.subtract(const Duration(days: 7));
      final end = now.add(const Duration(days: 14));
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
    ref.listen(instanceProvider.select((s) => s.activeSonarrInstanceId),
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
            Text('No upcoming episodes',
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
          final seriesTitle = (event['series'] as Map<String, dynamic>?)?['title'] as String? ?? '';
          final episodeTitle = event['title'] as String? ?? '';
          final season = event['seasonNumber'] as int? ?? 0;
          final episode = event['episodeNumber'] as int? ?? 0;
          final airDate = event['airDateUtc'] as String? ?? '';
          final hasFile = event['hasFile'] as bool? ?? false;

          return ListTile(
            leading: Icon(
              hasFile ? Icons.check_circle : Icons.schedule,
              color: hasFile ? AppTheme.available : AppTheme.accent,
            ),
            title: Text(seriesTitle,
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500)),
            subtitle: Text(
              'S${season.toString().padLeft(2, '0')}E${episode.toString().padLeft(2, '0')} - $episodeTitle'
              '${airDate.isNotEmpty ? '\n${airDate.substring(0, 10)}' : ''}',
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 13),
            ),
            isThreeLine: airDate.isNotEmpty,
          );
        },
      ),
    );
  }
}
