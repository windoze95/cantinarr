import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/tmdb_models.dart';

/// Bottom sheet for filtering discover results by genre, year, etc.
class FilterSheet extends StatefulWidget {
  final List<Genre> genres;
  final Set<int> selectedGenreIds;
  final int? selectedYear;
  final ValueChanged<Set<int>> onGenresChanged;
  final ValueChanged<int?> onYearChanged;

  const FilterSheet({
    super.key,
    required this.genres,
    required this.selectedGenreIds,
    this.selectedYear,
    required this.onGenresChanged,
    required this.onYearChanged,
  });

  @override
  State<FilterSheet> createState() => _FilterSheetState();
}

class _FilterSheetState extends State<FilterSheet> {
  late Set<int> _selectedGenres;
  int? _selectedYear;

  @override
  void initState() {
    super.initState();
    _selectedGenres = Set.from(widget.selectedGenreIds);
    _selectedYear = widget.selectedYear;
  }

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.6,
      minChildSize: 0.3,
      maxChildSize: 0.9,
      expand: false,
      builder: (context, scrollController) => Container(
        decoration: const BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: Column(
          children: [
            // Handle bar
            Container(
              margin: const EdgeInsets.symmetric(vertical: 12),
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: AppTheme.textSecondary,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
            // Title + Apply
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  const Text('Filters',
                      style: TextStyle(
                          fontSize: 20, fontWeight: FontWeight.bold)),
                  TextButton(
                    onPressed: () {
                      widget.onGenresChanged(_selectedGenres);
                      widget.onYearChanged(_selectedYear);
                      Navigator.pop(context);
                    },
                    child: const Text('Apply'),
                  ),
                ],
              ),
            ),
            const Divider(color: AppTheme.border),

            // Genres
            Expanded(
              child: ListView(
                controller: scrollController,
                padding: const EdgeInsets.all(16),
                children: [
                  const Text('Genres',
                      style: TextStyle(
                          fontSize: 16, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: widget.genres.map((genre) {
                      final selected = _selectedGenres.contains(genre.id);
                      return FilterChip(
                        label: Text(genre.name),
                        selected: selected,
                        onSelected: (val) {
                          setState(() {
                            if (val) {
                              _selectedGenres.add(genre.id);
                            } else {
                              _selectedGenres.remove(genre.id);
                            }
                          });
                        },
                        selectedColor: AppTheme.accent.withValues(alpha: 0.2),
                        checkmarkColor: AppTheme.accent,
                        backgroundColor: AppTheme.surfaceVariant,
                        labelStyle: TextStyle(
                          color: selected
                              ? AppTheme.accent
                              : AppTheme.textPrimary,
                        ),
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(20),
                          side: BorderSide(
                            color: selected
                                ? AppTheme.accent
                                : AppTheme.border,
                          ),
                        ),
                      );
                    }).toList(),
                  ),
                  const SizedBox(height: 24),

                  // Year filter
                  const Text('Year',
                      style: TextStyle(
                          fontSize: 16, fontWeight: FontWeight.w600)),
                  const SizedBox(height: 12),
                  Wrap(
                    spacing: 8,
                    runSpacing: 8,
                    children: [
                      _yearChip(null, 'Any'),
                      for (int y = DateTime.now().year; y >= 2000; y--)
                        _yearChip(y, y.toString()),
                    ],
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _yearChip(int? year, String label) {
    final selected = _selectedYear == year;
    return ChoiceChip(
      label: Text(label),
      selected: selected,
      onSelected: (_) => setState(() => _selectedYear = year),
      selectedColor: AppTheme.accent.withValues(alpha: 0.2),
      backgroundColor: AppTheme.surfaceVariant,
      labelStyle: TextStyle(
        color: selected ? AppTheme.accent : AppTheme.textPrimary,
      ),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(20),
        side:
            BorderSide(color: selected ? AppTheme.accent : AppTheme.border),
      ),
    );
  }
}
