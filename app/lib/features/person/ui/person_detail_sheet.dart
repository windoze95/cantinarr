import 'package:cached_network_image/cached_network_image.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:shimmer/shimmer.dart';
import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../../discover/data/discover_api_service.dart';
import '../../discover/data/tmdb_models.dart';
import '../logic/person_detail_provider.dart';

void showPersonDetailSheet(
  BuildContext context, {
  required int personId,
  required String personName,
  String? profilePath,
}) {
  showModalBottomSheet(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    builder: (_) => _PersonDetailSheet(
      personId: personId,
      personName: personName,
      profilePath: profilePath,
    ),
  );
}

class _PersonDetailSheet extends ConsumerStatefulWidget {
  final int personId;
  final String personName;
  final String? profilePath;

  const _PersonDetailSheet({
    required this.personId,
    required this.personName,
    this.profilePath,
  });

  @override
  ConsumerState<_PersonDetailSheet> createState() => _PersonDetailSheetState();
}

class _PersonDetailSheetState extends ConsumerState<_PersonDetailSheet> {
  late final PersonDetailNotifier _notifier;

  @override
  void initState() {
    super.initState();
    _notifier = PersonDetailNotifier(
      api: ref.read(discoverServiceProvider),
      id: widget.personId,
    );
    _notifier.load();
    _notifier.addListener(_onStateChange);
  }

  void _onStateChange() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _notifier.removeListener(_onStateChange);
    _notifier.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final state = _notifier.state;

    return DraggableScrollableSheet(
      initialChildSize: 0.75,
      minChildSize: 0.4,
      maxChildSize: 0.95,
      expand: false,
      builder: (context, scrollController) => Container(
        decoration: const BoxDecoration(
          color: AppTheme.surface,
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
        child: Stack(
          children: [
            ListView(
              controller: scrollController,
              padding: EdgeInsets.zero,
              children: [
            // Drag handle
            Center(
              child: Container(
                margin: const EdgeInsets.symmetric(vertical: 12),
                width: 40,
                height: 4,
                decoration: BoxDecoration(
                  color: AppTheme.textSecondary,
                  borderRadius: BorderRadius.circular(2),
                ),
              ),
            ),

            // Header
            _buildHeader(state),

            const Divider(color: AppTheme.border, height: 24),

            // Biography
            if (state.isLoading && state.person == null)
              _buildShimmerBio()
            else if (state.person?.biography != null &&
                state.person!.biography!.isNotEmpty)
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16),
                child: Text(
                  state.person!.biography!,
                  maxLines: 6,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                    height: 1.4,
                  ),
                ),
              ),

            if (state.error != null) _buildError(state.error!),

            const SizedBox(height: 16),

            // Filter row
            if (state.allCredits.isNotEmpty || !state.isLoading)
              _buildFilterRow(state),

            const SizedBox(height: 8),

            // Credits by year
            if (state.isLoading && state.allCredits.isEmpty)
              _buildShimmerCredits()
            else
              ..._buildCreditsByYear(state),

            const SizedBox(height: 48),
          ],
        ),
            // Bottom fade gradient
            Positioned(
              left: 0,
              right: 0,
              bottom: 0,
              height: 40,
              child: IgnorePointer(
                child: DecoratedBox(
                  decoration: BoxDecoration(
                    gradient: LinearGradient(
                      begin: Alignment.topCenter,
                      end: Alignment.bottomCenter,
                      colors: [
                        AppTheme.surface.withValues(alpha: 0),
                        AppTheme.surface,
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildHeader(PersonDetailState state) {
    final imageUrl = widget.profilePath != null
        ? AppConfig.tmdbPoster(widget.profilePath, width: 185)
        : '';
    final person = state.person;

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          // Profile photo
          ClipRRect(
            borderRadius: BorderRadius.circular(12),
            child: SizedBox(
              width: 100,
              height: 100,
              child: imageUrl.isNotEmpty
                  ? CachedNetworkImage(
                      imageUrl: imageUrl,
                      fit: BoxFit.cover,
                      placeholder: (_, __) => Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.person,
                              color: AppTheme.textSecondary, size: 32),
                        ),
                      ),
                      errorWidget: (_, __, ___) => Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.person,
                              color: AppTheme.textSecondary, size: 32),
                        ),
                      ),
                    )
                  : Container(
                      color: AppTheme.surfaceVariant,
                      child: const Center(
                        child: Icon(Icons.person,
                            color: AppTheme.textSecondary, size: 32),
                      ),
                    ),
            ),
          ),
          const SizedBox(width: 14),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  widget.personName,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.bold,
                  ),
                ),
                if (person != null) ...[
                  if (person.age != null) ...[
                    const SizedBox(height: 4),
                    Text(
                      person.deathday != null
                          ? 'Died at age ${person.age}'
                          : 'Age ${person.age}',
                      style: TextStyle(
                        color: person.deathday != null
                            ? AppTheme.error
                            : AppTheme.textSecondary,
                        fontSize: 13,
                      ),
                    ),
                  ],
                  if (person.birthday != null) ...[
                    const SizedBox(height: 2),
                    Text(
                      'Born ${person.birthday}',
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                  ],
                  if (person.deathday != null) ...[
                    const SizedBox(height: 2),
                    Text(
                      'Died ${person.deathday}',
                      style: const TextStyle(
                        color: AppTheme.error,
                        fontSize: 12,
                      ),
                    ),
                  ],
                  if (person.placeOfBirth != null) ...[
                    const SizedBox(height: 2),
                    Text(
                      person.placeOfBirth!,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                  ],
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildFilterRow(PersonDetailState state) {
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          _filterChip('ALL', PersonCreditFilter.all, state.filter),
          const SizedBox(width: 8),
          _filterChip('MOVIES', PersonCreditFilter.movies, state.filter),
          const SizedBox(width: 8),
          _filterChip('TV SHOWS', PersonCreditFilter.tvShows, state.filter),
          const Spacer(),
          PopupMenuButton<PersonCreditSort>(
            icon: const Icon(Icons.sort, color: AppTheme.textSecondary, size: 20),
            color: AppTheme.surfaceVariant,
            onSelected: _notifier.setSort,
            itemBuilder: (_) => [
              _sortItem(PersonCreditSort.date, 'Date', state),
              _sortItem(PersonCreditSort.title, 'Title', state),
              _sortItem(PersonCreditSort.rating, 'Rating', state),
            ],
          ),
        ],
      ),
    );
  }

  Widget _filterChip(
      String label, PersonCreditFilter value, PersonCreditFilter current) {
    final selected = value == current;
    return ChoiceChip(
      label: Text(label, style: const TextStyle(fontSize: 12)),
      selected: selected,
      onSelected: (_) => _notifier.setFilter(value),
      selectedColor: AppTheme.accent.withValues(alpha: 0.2),
      backgroundColor: AppTheme.surfaceVariant,
      labelStyle: TextStyle(
        color: selected ? AppTheme.accent : AppTheme.textPrimary,
        fontSize: 12,
      ),
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(20),
        side: BorderSide(color: selected ? AppTheme.accent : AppTheme.border),
      ),
      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
      visualDensity: VisualDensity.compact,
    );
  }

  PopupMenuEntry<PersonCreditSort> _sortItem(
      PersonCreditSort value, String label, PersonDetailState state) {
    final isActive = state.sort == value;
    return PopupMenuItem(
      value: value,
      child: Row(
        children: [
          Text(label,
              style: TextStyle(
                color: isActive ? AppTheme.accent : AppTheme.textPrimary,
              )),
          if (isActive) ...[
            const Spacer(),
            Icon(
              state.sortAscending ? Icons.arrow_upward : Icons.arrow_downward,
              size: 16,
              color: AppTheme.accent,
            ),
          ],
        ],
      ),
    );
  }

  List<Widget> _buildCreditsByYear(PersonDetailState state) {
    final byYear = state.creditsByYear;
    final years = byYear.keys.toList();
    final widgets = <Widget>[];

    for (final year in years) {
      widgets.add(
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 12, 16, 6),
          child: Text(
            year,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 16,
              fontWeight: FontWeight.bold,
            ),
          ),
        ),
      );
      for (final credit in byYear[year]!) {
        widgets.add(_buildCreditRow(credit));
      }
    }
    return widgets;
  }

  Widget _buildCreditRow(PersonCredit credit) {
    final posterUrl = AppConfig.tmdbPoster(credit.posterPath, width: 154);
    final role = credit.character ?? credit.job;

    return GestureDetector(
      onTap: () {
        Navigator.pop(context);
        context.push('/detail/${credit.mediaType}/${credit.id}');
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            // Poster
            ClipRRect(
              borderRadius: BorderRadius.circular(6),
              child: SizedBox(
                width: 60,
                height: 90,
                child: credit.posterPath != null
                    ? CachedNetworkImage(
                        imageUrl: posterUrl,
                        fit: BoxFit.cover,
                        placeholder: (_, __) => Container(
                            color: AppTheme.surfaceVariant),
                        errorWidget: (_, __, ___) => Container(
                          color: AppTheme.surfaceVariant,
                          child: const Center(
                            child: Icon(Icons.movie_outlined,
                                color: AppTheme.textSecondary, size: 18),
                          ),
                        ),
                      )
                    : Container(
                        color: AppTheme.surfaceVariant,
                        child: const Center(
                          child: Icon(Icons.movie_outlined,
                              color: AppTheme.textSecondary, size: 18),
                        ),
                      ),
              ),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    credit.title,
                    maxLines: 2,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  const SizedBox(height: 3),
                  Row(
                    children: [
                      if (credit.year != null)
                        Text(
                          credit.year!,
                          style: const TextStyle(
                            color: AppTheme.accent,
                            fontSize: 12,
                          ),
                        ),
                      if (credit.voteAverage != null &&
                          credit.voteAverage! > 0) ...[
                        const SizedBox(width: 8),
                        const Icon(Icons.star_rounded,
                            size: 13, color: AppTheme.accent),
                        const SizedBox(width: 2),
                        Text(
                          credit.voteAverage!.toStringAsFixed(1),
                          style: const TextStyle(
                            color: AppTheme.textSecondary,
                            fontSize: 11,
                          ),
                        ),
                      ],
                    ],
                  ),
                  if (role != null && role.isNotEmpty) ...[
                    const SizedBox(height: 2),
                    Text(
                      role,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 12,
                      ),
                    ),
                  ],
                  if (credit.overview != null &&
                      credit.overview!.isNotEmpty) ...[
                    const SizedBox(height: 3),
                    Text(
                      credit.overview!,
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 11,
                        height: 1.3,
                      ),
                    ),
                  ],
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildError(String error) {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        children: [
          Text(error,
              style:
                  const TextStyle(color: AppTheme.error, fontSize: 13)),
          const SizedBox(height: 8),
          TextButton(
            onPressed: _notifier.load,
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }

  Widget _buildShimmerBio() {
    return Shimmer.fromColors(
      baseColor: AppTheme.surfaceVariant,
      highlightColor: AppTheme.surface.withValues(alpha: 0.5),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: List.generate(
            4,
            (i) => Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Container(
                height: 12,
                width: i == 3 ? 160 : double.infinity,
                decoration: BoxDecoration(
                  color: AppTheme.surfaceVariant,
                  borderRadius: BorderRadius.circular(4),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildShimmerCredits() {
    return Shimmer.fromColors(
      baseColor: AppTheme.surfaceVariant,
      highlightColor: AppTheme.surface.withValues(alpha: 0.5),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16),
        child: Column(
          children: List.generate(
            4,
            (_) => Padding(
              padding: const EdgeInsets.only(bottom: 12),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Container(
                    width: 60,
                    height: 90,
                    decoration: BoxDecoration(
                      color: AppTheme.surfaceVariant,
                      borderRadius: BorderRadius.circular(6),
                    ),
                  ),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Container(
                          height: 13,
                          width: 140,
                          decoration: BoxDecoration(
                            color: AppTheme.surfaceVariant,
                            borderRadius: BorderRadius.circular(4),
                          ),
                        ),
                        const SizedBox(height: 6),
                        Container(
                          height: 10,
                          width: 60,
                          decoration: BoxDecoration(
                            color: AppTheme.surfaceVariant,
                            borderRadius: BorderRadius.circular(4),
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
