import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../main.dart';
import '../models.dart';
import '../session.dart';
import '../theme.dart';
import '../util.dart';
import '../widgets.dart';
import 'detail.dart';
import 'player.dart';

class HomeScreen extends StatefulWidget {
  const HomeScreen({super.key});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  final _searchController = TextEditingController();
  List<Anime>? _home;
  List<Anime>? _searchResults;
  List<DownloadJob> _downloads = [];
  String? _error;
  bool _searching = false;
  String _activeKeyword = '';

  /// null 表示「全部」，否则是 0（周日）到 6。
  int? _weekday = DateTime.now().weekday % 7;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _searchController.dispose();
    super.dispose();
  }

  ApiClient get _api => SessionScope.read(context).api!;

  Future<void> _load() async {
    setState(() => _error = null);
    try {
      final results = await Future.wait([_api.homepage(), _api.downloads().catchError((_) => <DownloadJob>[])]);
      if (!mounted) return;
      setState(() {
        _home = results[0] as List<Anime>;
        _downloads = results[1] as List<DownloadJob>;
      });
    } catch (e) {
      if (!mounted) return;
      SessionScope.read(context).handleError(e);
      setState(() => _error = e.toString());
    }
  }

  Future<void> _search(String keyword) async {
    keyword = keyword.trim();
    if (keyword.isEmpty) {
      setState(() {
        _searchResults = null;
        _activeKeyword = '';
      });
      return;
    }
    setState(() {
      _searching = true;
      _activeKeyword = keyword;
    });
    try {
      final items = await _api.search(keyword);
      if (mounted && _activeKeyword == keyword) setState(() => _searchResults = items);
    } catch (e) {
      if (mounted) showToast(context, e.toString(), error: true);
    } finally {
      if (mounted) setState(() => _searching = false);
    }
  }

  void _openAnime(Anime anime) {
    Navigator.of(context).push(MaterialPageRoute(builder: (_) => DetailScreen(anime: anime)));
  }

  Future<void> _openPlayer(DownloadJob job) async {
    await Navigator.of(context).push(MaterialPageRoute(builder: (_) => PlayerScreen(job: job)));
    if (mounted) setState(() {});
  }

  List<Anime> get _visibleHome {
    final list = _home ?? const <Anime>[];
    if (_weekday == null) return list;
    return list.where((a) => a.weekday == _weekday).toList();
  }

  /// 看了一半的已缓存剧集，最近看的排前面。
  List<(DownloadJob, PlaybackRecord)> _continueWatching(AppSession session) {
    final byGid = {for (final d in _downloads) d.gid: d};
    final entries = <(DownloadJob, PlaybackRecord)>[];
    session.history.forEach((gid, record) {
      final job = byGid[gid];
      if (job != null && job.isComplete && record.resumable) entries.add((job, record));
    });
    entries.sort((a, b) => b.$2.updatedAt.compareTo(a.$2.updatedAt));
    return entries.take(10).toList();
  }

  @override
  Widget build(BuildContext context) {
    final session = SessionScope.of(context);
    final searching = _searchResults != null || _activeKeyword.isNotEmpty;
    return Scaffold(
      body: SafeArea(
        bottom: false,
        child: RefreshIndicator(
          onRefresh: searching ? () => _search(_activeKeyword) : _load,
          child: CustomScrollView(
            physics: const AlwaysScrollableScrollPhysics(),
            slivers: [
              SliverToBoxAdapter(child: _buildHeader()),
              if (searching) ..._buildSearchResults() else ..._buildHome(session),
              const SliverToBoxAdapter(child: SizedBox(height: 24)),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildHeader() {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('新番', style: TextStyle(fontSize: 28, fontWeight: FontWeight.w800)),
          const SizedBox(height: 12),
          TextField(
            controller: _searchController,
            textInputAction: TextInputAction.search,
            onSubmitted: _search,
            onChanged: (value) {
              if (value.isEmpty) _search('');
            },
            decoration: InputDecoration(
              hintText: '搜索番剧',
              prefixIcon: const Icon(Icons.search),
              isDense: true,
              suffixIcon: _searchController.text.isEmpty && _activeKeyword.isEmpty
                  ? null
                  : IconButton(
                      tooltip: '清除',
                      icon: const Icon(Icons.close),
                      onPressed: () {
                        _searchController.clear();
                        FocusScope.of(context).unfocus();
                        _search('');
                      },
                    ),
            ),
          ),
        ],
      ),
    );
  }

  List<Widget> _buildSearchResults() {
    if (_searching && _searchResults == null) {
      return [const SliverFillRemaining(hasScrollBody: false, child: Center(child: CircularProgressIndicator()))];
    }
    final results = _searchResults ?? const <Anime>[];
    return [
      SliverToBoxAdapter(
        child: SectionTitle('「$_activeKeyword」的搜索结果',
            trailing: _searching
                ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                : Text('${results.length} 部', style: const TextStyle(color: AppColors.muted))),
      ),
      if (results.isEmpty && !_searching)
        const SliverFillRemaining(
          hasScrollBody: false,
          child: Center(child: Text('没找到相关番剧', style: TextStyle(color: AppColors.muted))),
        )
      else
        _posterGrid(results),
    ];
  }

  List<Widget> _buildHome(AppSession session) {
    if (_home == null) {
      if (_error != null) {
        return [SliverFillRemaining(hasScrollBody: false, child: ErrorView(message: _error!, onRetry: _load))];
      }
      return [const SliverFillRemaining(hasScrollBody: false, child: Center(child: CircularProgressIndicator()))];
    }
    final continueWatching = _continueWatching(session);
    final visible = _visibleHome;
    return [
      if (continueWatching.isNotEmpty) ...[
        const SliverToBoxAdapter(child: SectionTitle('继续观看')),
        SliverToBoxAdapter(child: _buildContinueRow(continueWatching)),
      ],
      SliverToBoxAdapter(child: _buildWeekdayBar()),
      if (visible.isEmpty)
        const SliverToBoxAdapter(
          child: Padding(
            padding: EdgeInsets.all(48),
            child: Center(child: Text('这天没有新番', style: TextStyle(color: AppColors.muted))),
          ),
        )
      else
        _posterGrid(visible),
    ];
  }

  Widget _buildContinueRow(List<(DownloadJob, PlaybackRecord)> entries) {
    final api = _api;
    return SizedBox(
      height: 176,
      child: ListView.separated(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.symmetric(horizontal: 16),
        itemCount: entries.length,
        separatorBuilder: (_, _) => const SizedBox(width: 12),
        itemBuilder: (context, index) {
          final (job, record) = entries[index];
          final remaining = Duration(seconds: (record.duration - record.position).round());
          final episode = episodeOf(job.title);
          return GestureDetector(
            onTap: () => _openPlayer(job),
            child: SizedBox(
              width: 200,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Stack(
                    children: [
                      CoverImage(
                        url: api.resolveImage('/api/downloads/${Uri.encodeComponent(job.gid)}/thumbnail') ??
                            api.resolveImage(job.imageUrl),
                        aspectRatio: 16 / 9,
                      ),
                      Positioned(
                        left: 0,
                        right: 0,
                        bottom: 0,
                        child: ClipRRect(
                          borderRadius: const BorderRadius.vertical(bottom: Radius.circular(10)),
                          child: LinearProgressIndicator(
                            value: record.fraction,
                            minHeight: 3,
                            backgroundColor: Colors.black45,
                          ),
                        ),
                      ),
                      const Positioned.fill(
                        child: Center(child: Icon(Icons.play_circle_fill, size: 36, color: Colors.white70)),
                      ),
                    ],
                  ),
                  const SizedBox(height: 6),
                  Text(animeNameOf(job.title), maxLines: 1, overflow: TextOverflow.ellipsis,
                      style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600)),
                  Text(
                    '${episode == null ? '' : '第 $episode 集 · '}剩余 ${formatDuration(remaining)}',
                    style: const TextStyle(fontSize: 12, color: AppColors.muted),
                  ),
                ],
              ),
            ),
          );
        },
      ),
    );
  }

  Widget _buildWeekdayBar() {
    final today = DateTime.now().weekday % 7;
    // 从周一排到周日，和日历习惯一致
    const order = [1, 2, 3, 4, 5, 6, 0];
    Widget chip(String label, int? value) {
      final selected = _weekday == value;
      return Padding(
        padding: const EdgeInsets.only(right: 8),
        child: ChoiceChip(
          label: Text(label),
          selected: selected,
          labelStyle: TextStyle(
            color: selected ? AppColors.accent : AppColors.inkSecondary,
            fontWeight: selected ? FontWeight.w700 : FontWeight.w500,
          ),
          onSelected: (_) => setState(() => _weekday = value),
        ),
      );
    }

    return SizedBox(
      height: 56,
      child: ListView(
        scrollDirection: Axis.horizontal,
        padding: const EdgeInsets.fromLTRB(16, 12, 8, 4),
        children: [
          chip('全部', null),
          for (final day in order) chip(day == today ? '今天' : weekdayLabels[day], day),
        ],
      ),
    );
  }

  Widget _posterGrid(List<Anime> items) {
    return SliverPadding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      sliver: SliverGrid(
        gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
          maxCrossAxisExtent: 140,
          mainAxisSpacing: 16,
          crossAxisSpacing: 12,
          childAspectRatio: 0.52,
        ),
        delegate: SliverChildBuilderDelegate(
          (context, index) => _PosterTile(anime: items[index], onTap: () => _openAnime(items[index])),
          childCount: items.length,
        ),
      ),
    );
  }
}

class _PosterTile extends StatelessWidget {
  const _PosterTile({required this.anime, required this.onTap});

  final Anime anime;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final rating = anime.rating;
    return GestureDetector(
      onTap: onTap,
      behavior: HitTestBehavior.opaque,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Stack(
            children: [
              CoverImage(url: anime.imageUrl, aspectRatio: 2 / 3),
              if (rating != null && rating > 0)
                Positioned(
                  right: 6,
                  top: 6,
                  child: Container(
                    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                    decoration: BoxDecoration(color: Colors.black54, borderRadius: BorderRadius.circular(6)),
                    child: Text(rating.toStringAsFixed(1),
                        style: const TextStyle(fontSize: 11, fontWeight: FontWeight.w700, color: Color(0xFFFFC857))),
                  ),
                ),
            ],
          ),
          const SizedBox(height: 8),
          Text(anime.title,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600, height: 1.25)),
        ],
      ),
    );
  }
}
