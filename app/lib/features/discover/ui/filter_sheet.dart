import 'package:flutter/material.dart';

import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/tmdb_models.dart';
import '../logic/browse_query.dart';
import 'tag_picker_field.dart';

/// The live lookups the sheet needs while open: the service list for a
/// region the user just picked, and the suggestions behind the keyword and
/// studio fields. Callbacks, so the sheet stays a plain widget and its tests
/// need no network stand-in.
class FilterLookups {
  const FilterLookups({
    required this.providersFor,
    required this.searchKeywords,
    required this.searchCompanies,
  });

  final Future<List<WatchProvider>> Function(String region) providersFor;
  final Future<List<TaggedId>> Function(String query) searchKeywords;
  final Future<List<TaggedId>> Function(String query) searchCompanies;

  /// Lookups that answer nothing, for a caller with no network.
  static final none = FilterLookups(
    providersFor: (_) async => const [],
    searchKeywords: (_) async => const [],
    searchCompanies: (_) async => const [],
  );
}

/// The Browse page's filter sheet: genres, a release-year range, a minimum
/// rating, an original language, streaming services in a region, keywords,
/// and studios. Resolves to the chosen [BrowseFilters] on Apply, to
/// [BrowseFilters.none] on Clear, and to null when dismissed.
///
/// Every list is optional: one that failed to load hides its section while
/// any value already in [initial] still flows through to the result, so a
/// deep link survives a lookup outage. The sheet's card and drag handle come
/// from the theme through [showAppSheet]; this body paints neither.
class FilterSheet extends StatefulWidget {
  const FilterSheet({
    super.key,
    required this.genres,
    required this.initial,
    this.languages = const [],
    this.regions = const [],
    this.providers = const [],
    this.region = 'US',
    this.lookups,
  });

  final List<Genre> genres;
  final BrowseFilters initial;
  final List<TmdbLanguage> languages;
  final List<WatchRegion> regions;

  /// The services for [region], already loaded by the screen.
  final List<WatchProvider> providers;

  /// The region the sheet opens on: the applied one, else the device's.
  final String region;
  final FilterLookups? lookups;

  /// The year range's lower bound; the upper bound is next year.
  static const earliestYear = 1900;
  static const ratingOptions = [6, 7, 8];

  /// The entry value that stands for "no language filter".
  static const anyLanguage = '';

  static Future<BrowseFilters?> show(
    BuildContext context, {
    required List<Genre> genres,
    required BrowseFilters initial,
    List<TmdbLanguage> languages = const [],
    List<WatchRegion> regions = const [],
    List<WatchProvider> providers = const [],
    String region = 'US',
    FilterLookups? lookups,
  }) =>
      showAppSheet<BrowseFilters>(
        context,
        builder: (_) => FilterSheet(
          genres: genres,
          initial: initial,
          languages: languages,
          regions: regions,
          providers: providers,
          region: region,
          lookups: lookups,
        ),
      );

  @override
  State<FilterSheet> createState() => _FilterSheetState();
}

class _FilterSheetState extends State<FilterSheet> {
  late final int _latestYear = DateTime.now().year + 1;
  late Set<int> _genres;
  late RangeValues _years;
  int? _minRating;
  String? _language;
  late String _region;
  late List<WatchProvider> _providers;
  late Set<int> _providerIds;
  bool _providersLoading = false;
  bool _providersFailed = false;

  /// Bumped per region change so a slow answer for an earlier region never
  /// replaces the list for the one now chosen.
  int _providersGeneration = 0;
  late List<TaggedId> _keywords;
  late List<TaggedId> _companies;

  FilterLookups get _lookups => widget.lookups ?? FilterLookups.none;

  @override
  void initState() {
    super.initState();
    _genres = {...widget.initial.genreIds};
    _years = RangeValues(
      (widget.initial.yearFrom ?? FilterSheet.earliestYear).toDouble(),
      (widget.initial.yearTo ?? _latestYear).toDouble(),
    );
    _minRating = widget.initial.minRating;
    _language = widget.initial.language;
    _region = widget.initial.watchRegion ?? widget.region;
    _providers = widget.providers;
    _providerIds = {...widget.initial.providerIds};
    _keywords = widget.initial.keywords;
    _companies = widget.initial.companies;
  }

  BrowseFilters get _filters {
    final from = _years.start.round();
    final to = _years.end.round();
    return BrowseFilters(
      // In the list's order when it is known, so the same choice always
      // reads the same way; a deep link's values survive a failed lookup.
      genreIds: _ordered(_genres, [for (final g in widget.genres) g.id]),
      yearFrom: from > FilterSheet.earliestYear ? from : null,
      yearTo: to < _latestYear ? to : null,
      minRating: _minRating,
      language: _language,
      providerIds:
          _ordered(_providerIds, [for (final p in _providers) p.providerId]),
      // A region means nothing without a service to look up in it.
      watchRegion: _providerIds.isEmpty ? null : _region,
      keywords: _keywords,
      companies: _companies,
    );
  }

  static List<int> _ordered(Set<int> chosen, List<int> known) => known.isEmpty
      ? chosen.toList()
      : [
          for (final id in known)
            if (chosen.contains(id)) id,
        ];

  String get _yearLabel {
    final from = _years.start.round();
    final to = _years.end.round();
    if (from == FilterSheet.earliestYear && to == _latestYear) return 'Any year';
    if (from == to) return '$from';
    return '$from to $to';
  }

  Future<void> _setRegion(String code) async {
    if (code == _region) return;
    final generation = ++_providersGeneration;
    setState(() {
      _region = code;
      _providersLoading = true;
      _providersFailed = false;
    });
    try {
      final providers = await _lookups.providersFor(code);
      if (!mounted || generation != _providersGeneration) return;
      final available = {for (final p in providers) p.providerId};
      setState(() {
        _providers = providers;
        _providersLoading = false;
        // A service the new region does not carry cannot be filtered on there.
        _providerIds.removeWhere((id) => !available.contains(id));
      });
    } catch (_) {
      if (!mounted || generation != _providersGeneration) return;
      // The list is unknown, not empty: keep what was chosen.
      setState(() {
        _providersLoading = false;
        _providersFailed = true;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final textTheme = Theme.of(context).textTheme;
    final showStreaming = widget.regions.isNotEmpty ||
        _providers.isNotEmpty ||
        _providerIds.isNotEmpty;
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
          if (widget.languages.isNotEmpty) ...[
            _heading('Language'),
            DropdownMenu<String>(
              key: const ValueKey('language-menu'),
              expandedInsets: EdgeInsets.zero,
              enableFilter: true,
              requestFocusOnTap: true,
              menuHeight: 320,
              initialSelection: _language ?? FilterSheet.anyLanguage,
              dropdownMenuEntries: [
                const DropdownMenuEntry(
                  value: FilterSheet.anyLanguage,
                  label: 'Any language',
                ),
                for (final language in widget.languages)
                  DropdownMenuEntry(
                    value: language.code,
                    label: language.englishName,
                  ),
              ],
              onSelected: (code) => setState(() {
                _language =
                    code == null || code == FilterSheet.anyLanguage ? null : code;
              }),
            ),
          ],
          if (showStreaming) ...[
            _heading('Streaming on'),
            if (widget.regions.isNotEmpty) ...[
              DropdownMenu<String>(
                key: const ValueKey('region-menu'),
                expandedInsets: EdgeInsets.zero,
                enableFilter: true,
                requestFocusOnTap: true,
                menuHeight: 320,
                initialSelection: _region,
                dropdownMenuEntries: [
                  for (final region in widget.regions)
                    DropdownMenuEntry(value: region.code, label: region.name),
                ],
                onSelected: (code) {
                  if (code != null) _setRegion(code);
                },
              ),
              const SizedBox(height: 10),
            ],
            if (_providersLoading)
              const Padding(
                padding: EdgeInsets.only(bottom: 10),
                child: LinearProgressIndicator(minHeight: 2),
              ),
            if (_providersFailed)
              Padding(
                padding: const EdgeInsets.only(bottom: 10),
                child: Text(
                  'Streaming services could not be loaded.',
                  style: textTheme.bodySmall?.copyWith(color: AppTheme.textMuted),
                ),
              ),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final provider in _providers)
                  _chip(
                    provider.providerName,
                    selected: _providerIds.contains(provider.providerId),
                    avatar: _logo(provider),
                    onSelected: (selected) => setState(() {
                      if (selected) {
                        _providerIds.add(provider.providerId);
                      } else {
                        _providerIds.remove(provider.providerId);
                      }
                    }),
                  ),
              ],
            ),
          ],
          _heading('Keywords'),
          TagPickerField(
            key: const ValueKey('keyword-field'),
            kind: 'keyword',
            values: _keywords,
            hint: 'Add a keyword',
            failureMessage: 'Keywords could not be searched.',
            search: _lookups.searchKeywords,
            onChanged: (values) => setState(() => _keywords = values),
          ),
          _heading('Studios'),
          TagPickerField(
            key: const ValueKey('studio-field'),
            kind: 'studio',
            values: _companies,
            hint: 'Add a studio',
            failureMessage: 'Studios could not be searched.',
            search: _lookups.searchCompanies,
            onChanged: (values) => setState(() => _companies = values),
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

  Widget _logo(WatchProvider provider) => ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: CachedImage(
          url: AppConfig.tmdbLogo(provider.logoPath),
          width: 24,
          height: 24,
          icon: Icons.tv_outlined,
          iconSize: 14,
        ),
      );

  Widget _chip(
    String label, {
    required bool selected,
    required ValueChanged<bool> onSelected,
    Widget? avatar,
  }) {
    return FilterChip(
      label: Text(label),
      avatar: avatar,
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
