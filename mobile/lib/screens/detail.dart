import 'dart:async';
import 'dart:ui';

import 'package:flutter/material.dart';

import '../api.dart';
import '../main.dart';
import '../models.dart';
import '../theme.dart';
import '../util.dart';
import '../widgets.dart';
import 'player.dart';

class DetailScreen extends StatefulWidget {
  const DetailScreen({super.key, required this.anime});

  final Anime anime;

  @override
  State<DetailScreen> createState() => _DetailScreenState();
}

class _DetailScreenState extends State<DetailScreen> {
  AnimeDetail? _detail;
  FavoriteAnime? _favorite;
  List<DownloadJob> _jobs = [];
  String? _error;
  bool _summaryExpanded = false;
  bool _favoriteBusy = false;
  bool _subscribeBusy = false;
  final Set<String> _queued = {};

  String? _subgroup;
  String? _resolution;
  int? _episode;

  Timer? _pollTimer;
  int _polls = 0;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    super.dispose();
  }

  ApiClient get _api => SessionScope.read(context).api!;
  Anime get _anime => _detail?.anime ?? widget.anime;

  Future<void> _load() async {
    setState(() => _error = null);
    unawaited(_loadSideData());
    try {
      final detail = await _api.animeDetail(widget.anime.animeId);
      if (!mounted) return;
      setState(() => _detail = detail);
      // 慢的资源源会晚一点才回来，过几秒再拉一次补上
      _pollTimer?.cancel();
      if (detail.stillLoading && _polls < 8) {
        _polls++;
        _pollTimer = Timer(const Duration(seconds: 3), _load);
      }
    } catch (e) {
      if (!mounted) return;
      SessionScope.read(context).handleError(e);
      setState(() => _error = e.toString());
    }
  }

  Future<void> _loadSideData() async {
    try {
      final results = await Future.wait([_api.favorites(), _api.downloads()]);
      if (!mounted) return;
      final favorites = results[0] as List<FavoriteAnime>;
      setState(() {
        _favorite = favorites.where((f) => f.animeId == widget.anime.animeId).firstOrNull;
        _jobs = results[1] as List<DownloadJob>;
      });
    } catch (_) {
      // 收藏状态、已缓存标记只是锦上添花，拉不到不影响看详情
    }
  }

  /// 这部番的下载任务：创建时带的封面地址就是这部番的封面，用它对回来。
  List<DownloadJob> get _animeJobs {
    final cover = _anime.imageUrl;
    if (cover == null) return const [];
    return _jobs.where((j) => j.imageUrl == cover && !j.isRemoved).toList();
  }

  Future<void> _toggleFavorite() async {
    setState(() => _favoriteBusy = true);
    try {
      if (_favorite != null) {
        await _api.removeFavorite(_anime.animeId);
        if (mounted) showToast(context, '已取消收藏');
      } else {
        await _api.addFavorite(_anime);
        if (mounted) showToast(context, '已收藏');
      }
      await _loadSideData();
    } catch (e) {
      if (mounted) showToast(context, e.toString(), error: true);
    } finally {
      if (mounted) setState(() => _favoriteBusy = false);
    }
  }

  Future<void> _toggleSubscribe() async {
    final favorite = _favorite;
    if (favorite != null && favorite.subscribed) {
      final confirmed = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('取消追更？'),
          content: const Text('不再自动下载新的剧集，已经缓存的不受影响。'),
          actions: [
            TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('再想想')),
            FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('取消追更')),
          ],
        ),
      );
      if (confirmed != true) return;
    }
    setState(() => _subscribeBusy = true);
    try {
      if (favorite != null && favorite.subscribed) {
        for (final sub in favorite.subscriptions) {
          await _api.deleteSubscription(sub.id);
        }
        if (mounted) showToast(context, '已取消追更');
      } else {
        await _api.subscribe(_anime, keyword: _detail?.anime.searchKeyword);
        if (mounted) showToast(context, '已订阅，新剧集出来后会自动下载');
      }
      await _loadSideData();
    } catch (e) {
      if (mounted) showToast(context, e.toString(), error: true);
    } finally {
      if (mounted) setState(() => _subscribeBusy = false);
    }
  }

  Future<void> _download(ResourceItem item) async {
    setState(() => _queued.add(item.id));
    try {
      await _api.createDownload(item, _anime);
      if (mounted) showToast(context, '已加入缓存队列');
      unawaited(_loadSideData());
    } catch (e) {
      if (mounted) {
        setState(() => _queued.remove(item.id));
        showToast(context, e.toString(), error: true);
      }
    }
  }

  void _play(DownloadJob job) {
    Navigator.of(context).push(MaterialPageRoute(builder: (_) => PlayerScreen(job: job)));
  }

  @override
  Widget build(BuildContext context) {
    final detail = _detail;
    return Scaffold(
      body: detail == null && _error != null
          ? SafeArea(child: Column(children: [const BackButton(), Expanded(child: ErrorView(message: _error!, onRetry: _load))]))
          : CustomScrollView(
              slivers: [
                _buildHeader(detail),
                if (detail == null)
                  const SliverFillRemaining(hasScrollBody: false, child: Center(child: CircularProgressIndicator()))
                else ...[
                  SliverToBoxAdapter(child: _buildActions()),
                  if (detail.summary != null) SliverToBoxAdapter(child: _buildSummary(detail)),
                  if (detail.tags.isNotEmpty) SliverToBoxAdapter(child: _buildTags(detail)),
                  SliverToBoxAdapter(child: _buildEpisodes(detail)),
                  ..._buildResources(detail),
                  SliverToBoxAdapter(child: SizedBox(height: MediaQuery.paddingOf(context).bottom + 24)),
                ],
              ],
            ),
    );
  }

  Widget _buildHeader(AnimeDetail? detail) {
    final anime = _anime;
    final topPadding = MediaQuery.paddingOf(context).top;
    final bangumi = detail?.platforms.where((p) => p.score != null).firstOrNull;
    final score = bangumi?.score ?? anime.rating;
    final weekday = anime.weekday;
    final meta = <String>[
      if (weekday != null && anime.isOnAir) '每${weekdayLabels[weekday]}更新',
      if (detail?.totalEpisodes != null) '全 ${detail!.totalEpisodes} 集',
      if (detail?.rank != null && detail!.rank! > 0) 'Bangumi 排名 #${detail.rank}',
    ];
    return SliverAppBar(
      pinned: true,
      expandedHeight: 280 + topPadding * 0.2,
      backgroundColor: AppColors.background,
      title: null,
      flexibleSpace: FlexibleSpaceBar(
        collapseMode: CollapseMode.pin,
        background: Stack(
          fit: StackFit.expand,
          children: [
            if (anime.imageUrl != null)
              ImageFiltered(
                imageFilter: ImageFilter.blur(sigmaX: 24, sigmaY: 24),
                child: Image.network(anime.imageUrl!, fit: BoxFit.cover, errorBuilder: (_, _, _) => const SizedBox()),
              ),
            const DecoratedBox(
              decoration: BoxDecoration(
                gradient: LinearGradient(
                  begin: Alignment.topCenter,
                  end: Alignment.bottomCenter,
                  colors: [Color(0x8816131A), AppColors.background],
                ),
              ),
            ),
            Positioned(
              left: 16,
              right: 16,
              bottom: 12,
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.end,
                children: [
                  SizedBox(width: 120, child: CoverImage(url: anime.imageUrl, aspectRatio: 2 / 3)),
                  const SizedBox(width: 16),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(anime.title,
                            maxLines: 3,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(fontSize: 21, fontWeight: FontWeight.w800, height: 1.25)),
                        if (detail != null && detail.titles.length > 1)
                          Padding(
                            padding: const EdgeInsets.only(top: 4),
                            child: Text(detail.titles.firstWhere((t) => t != anime.title, orElse: () => ''),
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                style: const TextStyle(fontSize: 13, color: AppColors.muted)),
                          ),
                        const SizedBox(height: 10),
                        if (score != null && score > 0)
                          Row(
                            children: [
                              Text(score.toStringAsFixed(1),
                                  style: const TextStyle(
                                      fontSize: 26, fontWeight: FontWeight.w800, color: Color(0xFFFFC857))),
                              const SizedBox(width: 6),
                              const Text('Bangumi 评分', style: TextStyle(fontSize: 12, color: AppColors.muted)),
                            ],
                          ),
                        if (meta.isNotEmpty)
                          Padding(
                            padding: const EdgeInsets.only(top: 4),
                            child: Text(meta.join(' · '),
                                style: const TextStyle(fontSize: 12, color: AppColors.inkSecondary)),
                          ),
                      ],
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

  Widget _buildActions() {
    final favorited = _favorite != null;
    final subscribed = _favorite?.subscribed ?? false;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Row(
        children: [
          Expanded(
            child: FilledButton.icon(
              onPressed: _subscribeBusy ? null : _toggleSubscribe,
              style: subscribed
                  ? FilledButton.styleFrom(backgroundColor: AppColors.panel, foregroundColor: AppColors.ink)
                  : null,
              icon: _subscribeBusy
                  ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
                  : Icon(subscribed ? Icons.notifications_active : Icons.notifications_none),
              label: Text(subscribed ? '已追更' : '追更'),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: OutlinedButton.icon(
              onPressed: _favoriteBusy ? null : _toggleFavorite,
              icon: Icon(favorited ? Icons.favorite : Icons.favorite_border,
                  color: favorited ? const Color(0xFFFF6B8B) : null),
              label: Text(favorited ? '已收藏' : '收藏'),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildSummary(AnimeDetail detail) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 0),
      child: GestureDetector(
        onTap: () => setState(() => _summaryExpanded = !_summaryExpanded),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('简介', style: TextStyle(fontSize: 17, fontWeight: FontWeight.w700)),
            const SizedBox(height: 8),
            Text(
              detail.summary!.trim(),
              maxLines: _summaryExpanded ? null : 4,
              overflow: _summaryExpanded ? null : TextOverflow.ellipsis,
              style: const TextStyle(color: AppColors.inkSecondary, height: 1.6, fontSize: 14),
            ),
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(_summaryExpanded ? '收起' : '展开',
                  style: const TextStyle(color: AppColors.accent, fontSize: 13)),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildTags(AnimeDetail detail) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 0),
      child: Wrap(
        spacing: 6,
        runSpacing: 6,
        children: [
          for (final tag in detail.tags.take(10))
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
              decoration: BoxDecoration(color: AppColors.panel, borderRadius: BorderRadius.circular(12)),
              child: Text(tag, style: const TextStyle(fontSize: 12, color: AppColors.inkSecondary)),
            ),
        ],
      ),
    );
  }

  Widget _buildEpisodes(AnimeDetail detail) {
    final jobsByEpisode = <int, DownloadJob>{};
    for (final job in _animeJobs) {
      final n = episodeOf(job.title);
      if (n == null) continue;
      final existing = jobsByEpisode[n];
      if (existing == null || (job.isComplete && !existing.isComplete)) jobsByEpisode[n] = job;
    }
    final numbers = <int>[];
    for (final ep in detail.episodes) {
      final n = ep.numberValue;
      if (n != null && !numbers.contains(n)) numbers.add(n);
    }
    if (numbers.isEmpty && (detail.totalEpisodes ?? 0) > 0) {
      numbers.addAll(List.generate(detail.totalEpisodes!, (i) => i + 1));
    }
    if (numbers.isEmpty) return const SizedBox.shrink();
    final today = DateTime.now();
    final airDates = {
      for (final ep in detail.episodes)
        if (ep.numberValue != null) ep.numberValue!: DateTime.tryParse(ep.airDate ?? ''),
    };

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        SectionTitle('剧集',
            trailing: Text(_episode == null ? '点集数筛选资源，已缓存的直接播放' : '只看第 $_episode 集的资源',
                style: const TextStyle(color: AppColors.muted, fontSize: 12))),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16),
          child: GridView.count(
            crossAxisCount: 6,
            shrinkWrap: true,
            physics: const NeverScrollableScrollPhysics(),
            mainAxisSpacing: 8,
            crossAxisSpacing: 8,
            childAspectRatio: 1.15,
            children: [
              for (final n in numbers) _episodeCell(n, jobsByEpisode[n], airDates[n], today),
            ],
          ),
        ),
      ],
    );
  }

  Widget _episodeCell(int n, DownloadJob? job, DateTime? airDate, DateTime today) {
    final selected = _episode == n;
    final cached = job != null && job.isComplete;
    final downloading = job != null && !job.isComplete && !job.isError;
    final notAired = airDate != null && airDate.isAfter(today);
    final watched = job != null && (SessionScope.read(context).record(job.gid)?.fraction ?? 0) > 0.9;
    Color background = AppColors.panel;
    Color foreground = notAired ? AppColors.muted : AppColors.ink;
    if (selected) {
      background = AppColors.accent;
      foreground = AppColors.accentContrast;
    } else if (cached) {
      background = AppColors.accent.withValues(alpha: 0.18);
      foreground = AppColors.accent;
    }
    return Material(
      color: background,
      borderRadius: BorderRadius.circular(8),
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: () {
          if (cached && !selected) {
            _play(job);
          } else {
            setState(() => _episode = selected ? null : n);
          }
        },
        onLongPress: () => setState(() => _episode = selected ? null : n),
        child: Stack(
          children: [
            Center(
              child: Text('$n',
                  style: TextStyle(
                      fontWeight: FontWeight.w700,
                      color: foreground,
                      decoration: watched ? TextDecoration.lineThrough : null)),
            ),
            if (cached || downloading)
              Positioned(
                right: 3,
                top: 3,
                child: Icon(cached ? Icons.check_circle : Icons.downloading,
                    size: 11, color: selected ? AppColors.accentContrast : AppColors.accent),
              ),
          ],
        ),
      ),
    );
  }

  List<Widget> _buildResources(AnimeDetail detail) {
    final all = detail.downloads;
    final subgroups = <String>{for (final r in all) r.subgroup ?? '未知字幕组'}.toList();
    final resolutions = <String>{
      for (final r in all)
        if (resolutionOf(r.title, r.resolution) != null) resolutionOf(r.title, r.resolution)!,
    }.toList()
      ..sort((a, b) => int.parse(b.replaceAll('p', '')).compareTo(int.parse(a.replaceAll('p', ''))));
    final filtered = all.where((r) {
      if (_subgroup != null && (r.subgroup ?? '未知字幕组') != _subgroup) return false;
      if (_resolution != null && resolutionOf(r.title, r.resolution) != _resolution) return false;
      if (_episode != null && (r.episode ?? episodeOf(r.title)) != _episode) return false;
      return true;
    }).toList();
    final pendingProviders = detail.providers.where((p) => p.pending).map((p) => p.name).toList();
    final failedProviders = detail.providers.where((p) => !p.ok && !p.pending).toList();

    Widget filterRow(String label, List<String> options, String? value, ValueChanged<String?> onChanged) {
      return SizedBox(
        height: 44,
        child: ListView(
          scrollDirection: Axis.horizontal,
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
          children: [
            for (final option in [null, ...options])
              Padding(
                padding: const EdgeInsets.only(right: 8),
                child: ChoiceChip(
                  label: Text(option ?? '全部$label'),
                  selected: value == option,
                  labelStyle: TextStyle(
                      fontSize: 13, color: value == option ? AppColors.accent : AppColors.inkSecondary),
                  onSelected: (_) => onChanged(option),
                ),
              ),
          ],
        ),
      );
    }

    return [
      SliverToBoxAdapter(
        child: SectionTitle('资源',
            trailing: pendingProviders.isNotEmpty
                ? Row(mainAxisSize: MainAxisSize.min, children: [
                    const SizedBox(width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 1.5)),
                    const SizedBox(width: 6),
                    Text('${pendingProviders.join('、')} 还在找', style: const TextStyle(color: AppColors.muted, fontSize: 12)),
                  ])
                : Text('${filtered.length} 条', style: const TextStyle(color: AppColors.muted, fontSize: 12))),
      ),
      if (failedProviders.isNotEmpty || detail.downloadError != null)
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
            child: Text(
              detail.downloadError ?? failedProviders.map((p) => '${p.name} 暂时不可用').join('，'),
              style: const TextStyle(color: AppColors.warn, fontSize: 12),
            ),
          ),
        ),
      if (subgroups.length > 1)
        SliverToBoxAdapter(child: filterRow('字幕组', subgroups, _subgroup, (v) => setState(() => _subgroup = v))),
      if (resolutions.length > 1)
        SliverToBoxAdapter(
            child: filterRow('分辨率', resolutions, _resolution, (v) => setState(() => _resolution = v))),
      if (filtered.isEmpty)
        SliverToBoxAdapter(
          child: Padding(
            padding: const EdgeInsets.all(32),
            child: Center(
              child: Text(all.isEmpty ? (detail.stillLoading ? '正在搜索资源…' : '暂时没有找到资源') : '没有符合筛选条件的资源',
                  style: const TextStyle(color: AppColors.muted)),
            ),
          ),
        )
      else
        SliverList.separated(
          itemCount: filtered.length,
          separatorBuilder: (_, _) => const Divider(indent: 16, endIndent: 16),
          itemBuilder: (context, index) => _resourceTile(filtered[index]),
        ),
    ];
  }

  Widget _resourceTile(ResourceItem item) {
    final queued = _queued.contains(item.id);
    final resolution = resolutionOf(item.title, item.resolution);
    final published = DateTime.tryParse(item.publishedAt ?? '')?.toLocal();
    final meta = [
      if (item.subgroup != null) item.subgroup!,
      ?resolution,
      if (item.sizeText != null) item.sizeText!,
      if (published != null) '${published.month}-${published.day.toString().padLeft(2, '0')}',
      if (item.seeders != null) '${item.seeders} 做种',
    ];
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 10, 8, 10),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(item.title, maxLines: 2, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 13.5, height: 1.35)),
                const SizedBox(height: 4),
                Text(meta.join(' · '), style: const TextStyle(fontSize: 12, color: AppColors.muted)),
              ],
            ),
          ),
          const SizedBox(width: 8),
          queued
              ? const Padding(
                  padding: EdgeInsets.symmetric(horizontal: 12),
                  child: Icon(Icons.check_circle, color: AppColors.ok),
                )
              : TextButton(onPressed: () => _download(item), child: const Text('下载')),
        ],
      ),
    );
  }
}
