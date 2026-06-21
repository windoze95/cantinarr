import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:intl/intl.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';
import '../../radarr/data/radarr_api_service.dart';
import '../../sonarr/data/sonarr_api_service.dart';
import '../data/release_event.dart';

/// Dashboard "Releases" tab — a unified view of what drops when, aggregating
/// the default Radarr (movie) and Sonarr (episode) calendars.
///
/// Defaults to a date-separated list of upcoming releases, with a toggleable
/// month-calendar view whose selected day filters the scrollable list below.
class DashboardReleasesTab extends ConsumerStatefulWidget {
  const DashboardReleasesTab({super.key});

  @override
  ConsumerState<DashboardReleasesTab> createState() =>
      _DashboardReleasesTabState();
}

enum _ReleasesView { list, calendar }

class _DashboardReleasesTabState extends ConsumerState<DashboardReleasesTab> {
  List<ReleaseEvent> _events = [];
  bool _isLoading = true;
  bool _loadError = false;
  int _loadToken = 0;

  _ReleasesView _view = _ReleasesView.list;
  late DateTime _focusedMonth;
  late DateTime _selectedDay;

  @override
  void initState() {
    super.initState();
    final now = DateTime.now();
    _focusedMonth = DateTime(now.year, now.month);
    _selectedDay = DateTime(now.year, now.month, now.day);
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  String _instanceSignature(AuthState? auth) {
    final conn = auth?.connection;
    return '${conn?.defaultRadarrInstance?.id ?? ''}'
        '|${conn?.defaultSonarrInstance?.id ?? ''}';
  }

  Future<void> _load() async {
    final token = ++_loadToken;
    final conn = ref.read(authProvider).valueOrNull?.connection;
    final radarr = conn?.defaultRadarrInstance;
    final sonarr = conn?.defaultSonarrInstance;

    if (radarr == null && sonarr == null) {
      if (mounted && token == _loadToken) {
        setState(() {
          _events = [];
          _isLoading = false;
          _loadError = false;
        });
      }
      return;
    }

    if (mounted) setState(() => _isLoading = _events.isEmpty);

    final dio = ref.read(backendClientProvider);
    final now = DateTime.now();
    // Start a little before the current month so the calendar's first weeks are
    // populated; reach a few months ahead for the upcoming list.
    final start = DateTime(now.year, now.month, 1)
        .subtract(const Duration(days: 7))
        .toIso8601String();
    final end = now.add(const Duration(days: 120)).toIso8601String();

    final events = <ReleaseEvent>[];
    var anyError = false;

    if (radarr != null) {
      try {
        final service =
            RadarrApiService(backendDio: dio, instanceId: radarr.id);
        final raw = await service.getCalendar(start: start, end: end);
        for (final entry in raw) {
          final event = releaseEventFromRadarr(entry, now: now);
          if (event != null) events.add(event);
        }
      } catch (_) {
        anyError = true;
      }
    }

    if (sonarr != null) {
      try {
        final service =
            SonarrApiService(backendDio: dio, instanceId: sonarr.id);
        final series = await service.getSeries();
        final seriesById = {for (final s in series) s.id: s};
        final raw = await service.getCalendar(start: start, end: end);
        for (final entry in raw) {
          final event = releaseEventFromSonarr(entry, seriesById);
          if (event != null) events.add(event);
        }
      } catch (_) {
        anyError = true;
      }
    }

    if (!mounted || token != _loadToken) return;
    events.sort((a, b) => a.date.compareTo(b.date));
    setState(() {
      _events = events;
      _isLoading = false;
      _loadError = anyError && events.isEmpty;
    });
  }

  void _changeMonth(int delta) {
    final month = DateTime(_focusedMonth.year, _focusedMonth.month + delta);
    final today = _dateOnly(DateTime.now());
    setState(() {
      _focusedMonth = month;
      // Keep the selection (and the list below) within the visible month.
      _selectedDay = (today.year == month.year && today.month == month.month)
          ? today
          : DateTime(month.year, month.month, 1);
    });
  }

  @override
  Widget build(BuildContext context) {
    // Reload if the default Radarr/Sonarr instances appear or change.
    ref.listen<String>(
      authProvider.select((a) => _instanceSignature(a.valueOrNull)),
      (prev, next) {
        if (prev != next) _load();
      },
    );

    final conn = ref.watch(authProvider).valueOrNull?.connection;
    final hasInstances = conn?.defaultRadarrInstance != null ||
        conn?.defaultSonarrInstance != null;

    if (_isLoading) {
      return const Center(
        child: CircularProgressIndicator(color: AppTheme.accent),
      );
    }

    if (!hasInstances) {
      return const _StatusMessage(
        icon: Icons.event_outlined,
        title: 'Nothing to schedule yet',
        message: 'Connect a Radarr or Sonarr service to see when your '
            'movies and shows drop.',
      );
    }

    if (_loadError) {
      return _StatusMessage(
        icon: Icons.error_outline,
        title: "Couldn't load releases",
        message: 'Check your connection and try again.',
        onRetry: _load,
      );
    }

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
          child: Align(
            alignment: Alignment.centerRight,
            child: _ViewToggle(
              view: _view,
              onChanged: (v) => setState(() => _view = v),
            ),
          ),
        ),
        Expanded(
          child: _view == _ReleasesView.list
              ? _buildList()
              : _buildCalendar(),
        ),
      ],
    );
  }

  Widget _buildList() {
    final today = _dateOnly(DateTime.now());
    final upcoming = _events.where((e) => !e.day.isBefore(today));
    final groups = groupReleasesByDay(upcoming);

    if (groups.isEmpty) {
      return RefreshIndicator(
        onRefresh: _load,
        color: AppTheme.accent,
        child: ListView(
          children: const [
            SizedBox(height: 120),
            _StatusMessage(
              icon: Icons.event_busy_outlined,
              title: 'No upcoming releases',
              message: 'Monitored movies and episodes will appear here.',
            ),
          ],
        ),
      );
    }

    // Flatten day groups into a single list of headers + tiles.
    final items = <Object>[];
    for (final group in groups) {
      items.add(group.key);
      items.addAll(group.value);
    }

    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView.builder(
        padding: const EdgeInsets.only(top: 4, bottom: 24),
        itemCount: items.length,
        itemBuilder: (context, index) {
          final item = items[index];
          if (item is DateTime) {
            return _DateHeader(day: item, today: today);
          }
          return _ReleaseTile(event: item as ReleaseEvent);
        },
      ),
    );
  }

  Widget _buildCalendar() {
    final today = _dateOnly(DateTime.now());
    final daysWithEvents = _events
        .where((e) =>
            e.day.year == _focusedMonth.year &&
            e.day.month == _focusedMonth.month)
        .map((e) => e.day)
        .toSet();
    final dayEvents = _events.where((e) => e.day == _selectedDay).toList()
      ..sort((a, b) => a.date.compareTo(b.date));

    // Calendar and day list scroll together so the grid never overflows short
    // viewports (e.g. landscape); on tall screens it simply sits at the top.
    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: CustomScrollView(
        physics: const AlwaysScrollableScrollPhysics(),
        slivers: [
          SliverToBoxAdapter(
            child: _MonthCalendar(
              month: _focusedMonth,
              selectedDay: _selectedDay,
              today: today,
              daysWithEvents: daysWithEvents,
              onDaySelected: (day) => setState(() => _selectedDay = day),
              onMonthDelta: _changeMonth,
            ),
          ),
          const SliverToBoxAdapter(
            child: Divider(color: AppTheme.border, height: 1),
          ),
          if (dayEvents.isEmpty)
            SliverFillRemaining(
              hasScrollBody: false,
              child: _StatusMessage(
                icon: Icons.event_available_outlined,
                title: 'Nothing on '
                    '${DateFormat('MMM d').format(_selectedDay)}',
                message: 'Pick another day to see what drops.',
              ),
            )
          else
            SliverPadding(
              padding: const EdgeInsets.only(top: 8, bottom: 24),
              sliver: SliverList.builder(
                itemCount: dayEvents.length,
                itemBuilder: (context, index) =>
                    _ReleaseTile(event: dayEvents[index]),
              ),
            ),
        ],
      ),
    );
  }
}

DateTime _dateOnly(DateTime d) => DateTime(d.year, d.month, d.day);

/// Compact two-segment switch between the list and calendar views.
class _ViewToggle extends StatelessWidget {
  final _ReleasesView view;
  final ValueChanged<_ReleasesView> onChanged;

  const _ViewToggle({required this.view, required this.onChanged});

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border),
      ),
      padding: const EdgeInsets.all(2),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          _segment(_ReleasesView.list, Icons.view_agenda_outlined, 'List'),
          _segment(
              _ReleasesView.calendar, Icons.calendar_month_outlined, 'Calendar'),
        ],
      ),
    );
  }

  Widget _segment(_ReleasesView value, IconData icon, String label) {
    final selected = view == value;
    return GestureDetector(
      onTap: () => onChanged(value),
      behavior: HitTestBehavior.opaque,
      child: AnimatedContainer(
        duration: const Duration(milliseconds: 150),
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
        decoration: BoxDecoration(
          color: selected
              ? AppTheme.accent.withValues(alpha: 0.18)
              : Colors.transparent,
          borderRadius: BorderRadius.circular(8),
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(
              icon,
              size: 16,
              color: selected ? AppTheme.accent : AppTheme.textSecondary,
            ),
            const SizedBox(width: 6),
            Text(
              label,
              style: TextStyle(
                color: selected ? AppTheme.accent : AppTheme.textSecondary,
                fontSize: 13,
                fontWeight: selected ? FontWeight.w600 : FontWeight.w500,
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// Date separator shown above each day's group in the list view.
class _DateHeader extends StatelessWidget {
  final DateTime day;
  final DateTime today;

  const _DateHeader({required this.day, required this.today});

  @override
  Widget build(BuildContext context) {
    final diff = day.difference(today).inDays;
    final String label;
    String? secondary;
    if (diff == 0) {
      label = 'Today';
      secondary = DateFormat('EEE, MMM d').format(day);
    } else if (diff == 1) {
      label = 'Tomorrow';
      secondary = DateFormat('EEE, MMM d').format(day);
    } else if (diff > 1 && diff < 7) {
      label = DateFormat('EEEE').format(day);
      secondary = DateFormat('MMM d').format(day);
    } else {
      label = DateFormat('EEE, MMM d').format(day);
    }

    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 18, 16, 6),
      child: Row(
        children: [
          Text(
            label,
            style: TextStyle(
              color: diff == 0 ? AppTheme.accent : AppTheme.textPrimary,
              fontSize: 15,
              fontWeight: FontWeight.w700,
            ),
          ),
          if (secondary != null) ...[
            const SizedBox(width: 8),
            Text(
              secondary,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
              ),
            ),
          ],
          const SizedBox(width: 12),
          const Expanded(child: Divider(color: AppTheme.border)),
        ],
      ),
    );
  }
}

/// A single release row: poster, title, episode/release info and status.
class _ReleaseTile extends StatelessWidget {
  final ReleaseEvent event;

  const _ReleaseTile({required this.event});

  @override
  Widget build(BuildContext context) {
    final isTv = event.mediaType == ReleaseMediaType.tv;
    final tmdbId = event.tmdbId;
    final timeLabel = isTv ? DateFormat('h:mm a').format(event.date) : null;

    return InkWell(
      onTap: tmdbId != null
          ? () => context.push('/detail/${isTv ? 'tv' : 'movie'}/$tmdbId')
          : null,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            _poster(isTv),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    event.title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 15,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 3),
                  Row(
                    children: [
                      Icon(
                        isTv ? Icons.tv : Icons.movie_outlined,
                        size: 13,
                        color: AppTheme.textSecondary,
                      ),
                      const SizedBox(width: 5),
                      Expanded(
                        child: Text(
                          event.subtitle ?? (isTv ? 'Episode' : 'Movie'),
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 13,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
            const SizedBox(width: 8),
            _trailing(timeLabel),
          ],
        ),
      ),
    );
  }

  Widget _poster(bool isTv) {
    final placeholder = Container(
      color: AppTheme.surfaceVariant,
      child: Center(
        child: Icon(
          isTv ? Icons.tv : Icons.movie_outlined,
          color: AppTheme.textSecondary,
          size: 20,
        ),
      ),
    );

    return ClipRRect(
      borderRadius: BorderRadius.circular(6),
      child: SizedBox(
        width: 44,
        height: 66,
        child: event.posterUrl != null
            ? CachedNetworkImage(
                imageUrl: event.posterUrl!,
                fit: BoxFit.cover,
                placeholder: (_, __) => placeholder,
                errorWidget: (_, __, ___) => placeholder,
              )
            : placeholder,
      ),
    );
  }

  Widget _trailing(String? timeLabel) {
    final children = <Widget>[
      if (timeLabel != null)
        Text(
          timeLabel,
          style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 12,
          ),
        ),
      if (event.hasFile)
        Padding(
          padding: EdgeInsets.only(top: timeLabel != null ? 4 : 0),
          child: const Icon(
            Icons.check_circle,
            size: 16,
            color: AppTheme.available,
          ),
        ),
    ];
    if (children.isEmpty) return const SizedBox.shrink();
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.end,
      children: children,
    );
  }
}

/// A simple month grid with day markers and selection, plus month navigation.
class _MonthCalendar extends StatelessWidget {
  final DateTime month;
  final DateTime selectedDay;
  final DateTime today;
  final Set<DateTime> daysWithEvents;
  final ValueChanged<DateTime> onDaySelected;
  final ValueChanged<int> onMonthDelta;

  const _MonthCalendar({
    required this.month,
    required this.selectedDay,
    required this.today,
    required this.daysWithEvents,
    required this.onDaySelected,
    required this.onMonthDelta,
  });

  static const _weekdayLabels = ['S', 'M', 'T', 'W', 'T', 'F', 'S'];

  @override
  Widget build(BuildContext context) {
    final monthStart = DateTime(month.year, month.month, 1);
    final daysInMonth = DateTime(month.year, month.month + 1, 0).day;
    // weekday: Mon=1..Sun=7 — shift so the grid starts on Sunday.
    final leading = monthStart.weekday % 7;
    final cellCount = leading + daysInMonth;

    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(8, 4, 8, 0),
          child: Row(
            children: [
              IconButton(
                icon: const Icon(Icons.chevron_left,
                    color: AppTheme.textSecondary),
                onPressed: () => onMonthDelta(-1),
                tooltip: 'Previous month',
              ),
              Expanded(
                child: Center(
                  child: Text(
                    DateFormat('MMMM yyyy').format(monthStart),
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 16,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ),
              IconButton(
                icon: const Icon(Icons.chevron_right,
                    color: AppTheme.textSecondary),
                onPressed: () => onMonthDelta(1),
                tooltip: 'Next month',
              ),
            ],
          ),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8),
          child: Row(
            children: [
              for (final label in _weekdayLabels)
                Expanded(
                  child: Center(
                    child: Text(
                      label,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                        fontWeight: FontWeight.w600,
                      ),
                    ),
                  ),
                ),
            ],
          ),
        ),
        const SizedBox(height: 4),
        Padding(
          padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
          child: GridView.builder(
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
              crossAxisCount: 7,
              childAspectRatio: 1.05,
            ),
            itemCount: cellCount,
            itemBuilder: (context, index) {
              if (index < leading) return const SizedBox.shrink();
              final dayNum = index - leading + 1;
              final day = DateTime(month.year, month.month, dayNum);
              return _DayCell(
                dayNumber: dayNum,
                isSelected: day == selectedDay,
                isToday: day == today,
                hasEvents: daysWithEvents.contains(day),
                onTap: () => onDaySelected(day),
              );
            },
          ),
        ),
      ],
    );
  }
}

class _DayCell extends StatelessWidget {
  final int dayNumber;
  final bool isSelected;
  final bool isToday;
  final bool hasEvents;
  final VoidCallback onTap;

  const _DayCell({
    required this.dayNumber,
    required this.isSelected,
    required this.isToday,
    required this.hasEvents,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final Color numberColor;
    if (isSelected) {
      numberColor = AppTheme.background;
    } else if (isToday) {
      numberColor = AppTheme.accent;
    } else {
      numberColor = AppTheme.textPrimary;
    }

    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(20),
      child: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          Container(
            width: 32,
            height: 32,
            alignment: Alignment.center,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: isSelected ? AppTheme.accent : Colors.transparent,
              border: isToday && !isSelected
                  ? Border.all(color: AppTheme.accent, width: 1.5)
                  : null,
            ),
            child: Text(
              '$dayNumber',
              style: TextStyle(
                color: numberColor,
                fontSize: 14,
                fontWeight:
                    isToday || isSelected ? FontWeight.w700 : FontWeight.w400,
              ),
            ),
          ),
          const SizedBox(height: 2),
          Container(
            width: 5,
            height: 5,
            decoration: BoxDecoration(
              shape: BoxShape.circle,
              color: hasEvents ? AppTheme.accent : Colors.transparent,
            ),
          ),
        ],
      ),
    );
  }
}

/// Centered icon + message used for empty, no-instance and error states.
class _StatusMessage extends StatelessWidget {
  final IconData icon;
  final String title;
  final String message;
  final Future<void> Function()? onRetry;

  const _StatusMessage({
    required this.icon,
    required this.title,
    required this.message,
    this.onRetry,
  });

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 48, color: AppTheme.textSecondary),
            const SizedBox(height: 16),
            Text(
              title,
              textAlign: TextAlign.center,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 17,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              message,
              textAlign: TextAlign.center,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 14,
              ),
            ),
            if (onRetry != null) ...[
              const SizedBox(height: 20),
              ElevatedButton(
                onPressed: onRetry,
                child: const Text('Retry'),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
