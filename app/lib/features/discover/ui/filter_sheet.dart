import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/tmdb_models.dart';
import '../logic/browse_query.dart';

/// The Browse page's filter sheet: genres, a release-year range, and a
/// minimum rating. Resolves to the chosen [BrowseFilters] on Apply, to
/// [BrowseFilters.none] on Clear, and to null when dismissed.
///
/// The sheet's card and drag handle come from the theme through
/// [showAppSheet]; this body paints neither.
class FilterSheet extends StatefulWidget {
  const FilterSheet({
    super.key,
    required this.genres,
    required this.initial,
  });

  /// The genres to offer; an empty list (the lookup failed) hides the
  /// section and leaves any genre already in [initial] untouched.
  final List<Genre> genres;
  final BrowseFilters initial;

  /// The year range's lower bound; the upper bound is next year.
  static const earliestYear = 1900;
  static const ratingOptions = [6, 7, 8];

  static Future<BrowseFilters?> show(
    BuildContext context, {
    required List<Genre> genres,
    required BrowseFilters initial,
  }) =>
      showAppSheet<BrowseFilters>(
        context,
        builder: (_) => FilterSheet(genres: genres, initial: initial),
      );

  @override
  State<FilterSheet> createState() => _FilterSheetState();
}

class _FilterSheetState extends State<FilterSheet> {
  late final int _latestYear = DateTime.now().year + 1;
  late Set<int> _genres;
  late RangeValues _years;
  int? _minRating;

  @override
  void initState() {
    super.initState();
    _genres = {...widget.initial.genreIds};
    _years = RangeValues(
      (widget.initial.yearFrom ?? FilterSheet.earliestYear).toDouble(),
      (widget.initial.yearTo ?? _latestYear).toDouble(),
    );
    _minRating = widget.initial.minRating;
  }

  BrowseFilters get _filters {
    final from = _years.start.round();
    final to = _years.end.round();
    return BrowseFilters(
      // In the genre list's order when it is known, so the same choice always
      // reads the same way; a deep link's genres survive a failed lookup.
      genreIds: widget.genres.isEmpty
          ? _genres.toList()
          : [
              for (final genre in widget.genres)
                if (_genres.contains(genre.id)) genre.id,
            ],
      yearFrom: from > FilterSheet.earliestYear ? from : null,
      yearTo: to < _latestYear ? to : null,
      minRating: _minRating,
    );
  }

  String get _yearLabel {
    final from = _years.start.round();
    final to = _years.end.round();
    if (from == FilterSheet.earliestYear && to == _latestYear) return 'Any year';
    if (from == to) return '$from';
    return '$from to $to';
  }

  @override
  Widget build(BuildContext context) {
    final textTheme = Theme.of(context).textTheme;
    return AppSheet(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Expanded(
                child: Text(
                  'Filters',
                  style: textTheme.titleLarge,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              TextButton(
                onPressed: () =>
                    Navigator.of(context).pop(BrowseFilters.none),
                child: const Text('Clear'),
              ),
              const SizedBox(width: 8),
              FilledButton(
                onPressed: () => Navigator.of(context).pop(_filters),
                child: const Text('Apply'),
              ),
            ],
          ),
          if (widget.genres.isNotEmpty) ...[
            _heading('Genres'),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final genre in widget.genres)
                  _chip(
                    genre.name,
                    selected: _genres.contains(genre.id),
                    onSelected: (selected) => setState(() {
                      if (selected) {
                        _genres.add(genre.id);
                      } else {
                        _genres.remove(genre.id);
                      }
                    }),
                  ),
              ],
            ),
          ],
          _heading('Released'),
          Text(
            _yearLabel,
            style: textTheme.bodyMedium?.copyWith(color: AppTheme.textSecondary),
          ),
          RangeSlider(
            values: _years,
            min: FilterSheet.earliestYear.toDouble(),
            max: _latestYear.toDouble(),
            divisions: _latestYear - FilterSheet.earliestYear,
            labels: RangeLabels(
              '${_years.start.round()}',
              '${_years.end.round()}',
            ),
            onChanged: (values) => setState(() => _years = values),
          ),
          _heading('Minimum rating'),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              _chip(
                'Any',
                selected: _minRating == null,
                onSelected: (_) => setState(() => _minRating = null),
              ),
              for (final rating in FilterSheet.ratingOptions)
                _chip(
                  '$rating+',
                  selected: _minRating == rating,
                  onSelected: (_) => setState(() => _minRating = rating),
                ),
            ],
          ),
          const SizedBox(height: 8),
        ],
      ),
    );
  }

  Widget _heading(String text) => Padding(
        padding: const EdgeInsets.only(top: 20, bottom: 10),
        child: Text(
          text,
          style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600),
        ),
      );

  Widget _chip(
    String label, {
    required bool selected,
    required ValueChanged<bool> onSelected,
  }) {
    return FilterChip(
      label: Text(label),
      selected: selected,
      onSelected: onSelected,
      showCheckmark: false,
      selectedColor: AppTheme.accent.withValues(alpha: 0.2),
      backgroundColor: AppTheme.surfaceVariant,
      labelStyle: TextStyle(
        color: selected ? AppTheme.accent : AppTheme.textPrimary,
      ),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(20),
        side: BorderSide(color: selected ? AppTheme.accent : AppTheme.border),
      ),
    );
  }
}
