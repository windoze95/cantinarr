import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../theme/app_theme.dart';

/// Groups [items] by the calendar day of [dateOf], days ascending, each day's
/// items ordered by time ascending.
List<MapEntry<DateTime, List<T>>> groupItemsByDay<T>(
  Iterable<T> items,
  DateTime Function(T item) dateOf,
) {
  final byDay = <DateTime, List<T>>{};
  for (final item in items) {
    final d = dateOf(item);
    byDay.putIfAbsent(DateTime(d.year, d.month, d.day), () => []).add(item);
  }
  final entries = byDay.entries.toList()
    ..sort((a, b) => a.key.compareTo(b.key));
  for (final entry in entries) {
    entry.value.sort((a, b) => dateOf(a).compareTo(dateOf(b)));
  }
  return entries;
}

/// Date separator shown above each day's group in a day-sectioned list
/// ("Today", "Tomorrow", a near weekday, or the full date).
class DaySectionHeader extends StatelessWidget {
  final DateTime day;
  final DateTime today;

  const DaySectionHeader({super.key, required this.day, required this.today});

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
