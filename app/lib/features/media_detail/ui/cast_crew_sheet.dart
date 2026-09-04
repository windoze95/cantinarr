import 'package:flutter/material.dart';

import '../../../core/config/app_config.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../discover/data/tmdb_models.dart';
import '../../person/ui/person_detail_sheet.dart';
import '../logic/title_facts.dart';

/// Everyone credited on a title: the cast in billing order, then the crew
/// by department. Tapping a row closes this sheet and opens that person's
/// own sheet on the title page underneath.
void showCastCrewSheet(
  BuildContext context, {
  required String title,
  required List<CastMember> cast,
  required List<CrewMember> crew,
}) {
  // Not `showAppSheet`: like the person sheet, this body is a
  // [DraggableScrollableSheet] that resizes as the user drags, so it, not
  // the theme, owns the card and the handle.
  showModalBottomSheet(
    context: context,
    isScrollControlled: true,
    backgroundColor: Colors.transparent,
    showDragHandle: false,
    builder: (_) => _CastCrewSheet(
      title: title,
      entries: _entriesFor(cast, crew),
      onPerson: (person) => showPersonDetailSheet(
        context,
        personId: person.id,
        personName: person.name,
        profilePath: person.profilePath,
      ),
    ),
  );
}

sealed class _Entry {
  const _Entry();
}

class _Heading extends _Entry {
  final String text;
  const _Heading(this.text);
}

class _Person extends _Entry {
  final int id;
  final String name;
  final String? role;
  final String? profilePath;
  const _Person({
    required this.id,
    required this.name,
    this.role,
    this.profilePath,
  });
}

List<_Entry> _entriesFor(List<CastMember> cast, List<CrewMember> crew) => [
      if (cast.isNotEmpty) const _Heading('Cast'),
      for (final member in cast)
        _Person(
          id: member.id,
          name: member.name,
          role: member.character,
          profilePath: member.profilePath,
        ),
      for (final section in crewSections(crew)) ...[
        _Heading(section.department),
        for (final member in section.members)
          _Person(
            id: member.id,
            name: member.name,
            role: member.job.isEmpty ? null : member.job,
            profilePath: member.profilePath,
          ),
      ],
    ];

class _CastCrewSheet extends StatelessWidget {
  final String title;
  final List<_Entry> entries;
  final ValueChanged<_Person> onPerson;

  const _CastCrewSheet({
    required this.title,
    required this.entries,
    required this.onPerson,
  });

  @override
  Widget build(BuildContext context) {
    return DraggableScrollableSheet(
      initialChildSize: 0.75,
      minChildSize: 0.4,
      maxChildSize: 0.95,
      expand: false,
      builder: (context, scrollController) => Container(
        decoration: const BoxDecoration(
          color: AppTheme.surfaceRaised,
          borderRadius: BorderRadius.vertical(
            top: Radius.circular(AppTheme.radiusXl),
          ),
        ),
        child: ListView.builder(
          controller: scrollController,
          padding: const EdgeInsets.only(bottom: 32),
          itemCount: entries.length + 1,
          itemBuilder: (context, index) {
            if (index == 0) return _header();
            return switch (entries[index - 1]) {
              _Heading(:final text) => Padding(
                  padding: const EdgeInsets.fromLTRB(16, 12, 16, 6),
                  child: Text(
                    text,
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 16,
                      fontWeight: FontWeight.bold,
                    ),
                  ),
                ),
              final _Person person => _PersonRow(
                  person: person,
                  onTap: () {
                    Navigator.of(context).pop();
                    onPerson(person);
                  },
                ),
            };
          },
        ),
      ),
    );
  }

  Widget _header() => Column(
        children: [
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
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text(
                  'Cast & crew',
                  style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.bold,
                  ),
                ),
                const SizedBox(height: 2),
                Text(
                  title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppTheme.textSecondary,
                    fontSize: 13,
                  ),
                ),
              ],
            ),
          ),
          const Divider(color: AppTheme.border, height: 24),
        ],
      );
}

class _PersonRow extends StatelessWidget {
  final _Person person;
  final VoidCallback onTap;

  const _PersonRow({required this.person, required this.onTap});

  @override
  Widget build(BuildContext context) {
    final path = person.profilePath;
    return InkWell(
      onTap: onTap,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
        child: Row(
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(8),
              child: SizedBox(
                width: 40,
                height: 60,
                child: CachedImage(
                  url: path == null
                      ? null
                      : AppConfig.tmdbPoster(path, width: 185),
                  fit: BoxFit.cover,
                  icon: Icons.person,
                  iconSize: 18,
                ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    person.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                  if (person.role case final role?)
                    Text(
                      role,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                      ),
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
