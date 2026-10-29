import 'dart:async';

import 'package:flutter/material.dart';

import '../api.dart';
import '../main.dart';
import '../models.dart';
import '../theme.dart';
import '../util.dart';
import '../widgets.dart';
import 'player.dart';

/// 已缓存的一部番：同一张封面（或认出来的同一个番名）的任务归在一起。
class CachedGroup {
  CachedGroup(this.key, this.name, this.imageUrl);

  final String key;
  final String name;
  final String? imageUrl;
  final List<DownloadJob> jobs = [];

  int get totalBytes => jobs.fold(0, (sum, j) => sum + j.totalLength);
}

List<CachedGroup> groupCachedJobs(List<DownloadJob> jobs) {
  final groups = <String, CachedGroup>{};
  for (final job in jobs) {
    final name = animeNameOf(job.title);
    final key = job.imageUrl ?? name;
    groups.putIfAbsent(key, () => CachedGroup(key, name, job.imageUrl)).jobs.add(job);
  }
  for (final group in groups.values) {
    group.jobs.sort((a, b) => (episodeOf(a.title) ?? 9999).compareTo(episodeOf(b.title) ?? 9999));
  }
  final list = groups.values.toList();
  // 最近缓存的番排前面
  DateTime latest(CachedGroup g) =>
      g.jobs.map((j) => j.createdAt ?? DateTime(2000)).reduce((a, b) => a.isAfter(b) ? a : b);
  list.sort((a, b) => latest(b).compareTo(latest(a)));
  return list;
}

class DownloadsScreen extends StatefulWidget {
  const DownloadsScreen({super.key, required this.active});

  /// 只有切到这个页签时才轮询进度。
  final bool active;

  @override
  State<DownloadsScreen> createState() => _DownloadsScreenState();
}

class _DownloadsScreenState extends State<DownloadsScreen> {
  List<DownloadJob>? _jobs;
  String? _error;
  Timer? _timer;
  final Set<String> _busy = {};

  ApiClient get _api => SessionScope.read(context).api!;

  @override
  void initState() {
    super.initState();
    _syncTimer();
  }

  @override
  void didUpdateWidget(DownloadsScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.active != widget.active) _syncTimer();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  void _syncTimer() {
    _timer?.cancel();
    if (!widget.active) return;
    WidgetsBinding.instance.addPostFrameCallback((_) => _refresh());
    _timer = Timer.periodic(const Duration(seconds: 2), (_) => _refresh());
  }

  Future<void> _refresh() async {
    if (!mounted) return;
    try {
      final jobs = await _api.downloads();
      if (!mounted) return;
      setState(() {
        _jobs = jobs;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      SessionScope.read(context).handleError(e);
      setState(() => _error = e.toString());
    }
  }

  Future<void> _togglePause(DownloadJob job) async {
    setState(() => _busy.add(job.gid));
    try {
      if (job.isPaused || job.isError) {
        await _api.resumeDownload(job.gid);
      } else {
        await _api.pauseDownload(job.gid);
      }
      await _refresh();
    } catch (e) {
      if (mounted) showToast(context, e.toString(), error: true);
    } finally {
      if (mounted) setState(() => _busy.remove(job.gid));
    }
  }

  Future<void> _remove(DownloadJob job) async {
    final deleteFiles = await confirmRemove(context, job);
    if (deleteFiles == null) return;
    try {
      await _api.removeDownload(job.gid, deleteLocalFiles: deleteFiles);
      await _refresh();
      if (mounted) showToast(context, '已删除');
    } catch (e) {
      if (mounted) showToast(context, e.toString(), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final jobs = _jobs;
    return Scaffold(
      appBar: AppBar(title: const Text('离线缓存')),
      body: jobs == null
          ? (_error != null
              ? ErrorView(message: _error!, onRetry: _refresh)
              : const Center(child: CircularProgressIndicator()))
          : RefreshIndicator(onRefresh: _refresh, child: _buildList(jobs)),
    );
  }

  Widget _buildList(List<DownloadJob> jobs) {
    final visible = jobs.where((j) => !j.isRemoved).toList();
    final downloading = visible.where((j) => !j.isComplete).toList()
      ..sort((a, b) => (b.createdAt ?? DateTime(2000)).compareTo(a.createdAt ?? DateTime(2000)));
    final groups = groupCachedJobs(visible.where((j) => j.isComplete).toList());

    if (visible.isEmpty) {
      return ListView(children: const [
        SizedBox(height: 160),
        Icon(Icons.download_for_offline_outlined, size: 48, color: AppColors.muted),
        SizedBox(height: 12),
        Center(child: Text('还没有缓存，去番剧详情里挑个资源下载吧', style: TextStyle(color: AppColors.muted))),
      ]);
    }

    return CustomScrollView(
      physics: const AlwaysScrollableScrollPhysics(),
      slivers: [
        if (downloading.isNotEmpty) ...[
          SliverToBoxAdapter(child: SectionTitle('正在缓存', trailing: Text('${downloading.length} 个', style: const TextStyle(color: AppColors.muted)))),
          SliverList.builder(
            itemCount: downloading.length,
            itemBuilder: (context, index) => _DownloadingTile(
              job: downloading[index],
              api: _api,
              busy: _busy.contains(downloading[index].gid),
              onToggle: () => _togglePause(downloading[index]),
              onRemove: () => _remove(downloading[index]),
            ),
          ),
        ],
        SliverToBoxAdapter(
          child: SectionTitle('已缓存', trailing: Text('${groups.length} 部', style: const TextStyle(color: AppColors.muted))),
        ),
        if (groups.isEmpty)
          const SliverToBoxAdapter(
            child: Padding(
              padding: EdgeInsets.all(32),
              child: Center(child: Text('缓存完成的番会出现在这里', style: TextStyle(color: AppColors.muted))),
            ),
          )
        else
          SliverPadding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            sliver: SliverGrid(
              gridDelegate: const SliverGridDelegateWithMaxCrossAxisExtent(
                maxCrossAxisExtent: 140,
                mainAxisSpacing: 16,
                crossAxisSpacing: 12,
                childAspectRatio: 0.5,
              ),
              delegate: SliverChildBuilderDelegate(
                (context, index) {
                  final group = groups[index];
                  return GestureDetector(
                    onTap: () => Navigator.of(context).push(MaterialPageRoute(
                      builder: (_) => CachedGroupScreen(groupKey: group.key, initial: group),
                    )),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Stack(
                          children: [
                            CoverImage(url: _api.resolveImage(group.imageUrl), aspectRatio: 2 / 3),
                            Positioned(
                              right: 6,
                              bottom: 6,
                              child: Container(
                                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                                decoration: BoxDecoration(color: Colors.black54, borderRadius: BorderRadius.circular(6)),
                                child: Text('${group.jobs.length} 集',
                                    style: const TextStyle(fontSize: 11, color: Colors.white)),
                              ),
                            ),
                          ],
                        ),
                        const SizedBox(height: 8),
                        Text(group.name,
                            maxLines: 2,
                            overflow: TextOverflow.ellipsis,
                            style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w600, height: 1.25)),
                        Text(formatBytes(group.totalBytes), style: const TextStyle(fontSize: 12, color: AppColors.muted)),
                      ],
                    ),
                  );
                },
                childCount: groups.length,
              ),
            ),
          ),
        const SliverToBoxAdapter(child: SizedBox(height: 24)),
      ],
    );
  }
}

/// 删除确认：返回 null 表示取消，true / false 表示是否连本机文件一起删。
Future<bool?> confirmRemove(BuildContext context, DownloadJob job) {
  return showModalBottomSheet<bool>(
    context: context,
    showDragHandle: true,
    builder: (context) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 0, 20, 12),
            child: Text(job.title, maxLines: 2, overflow: TextOverflow.ellipsis, style: const TextStyle(color: AppColors.muted)),
          ),
          ListTile(
            leading: const Icon(Icons.delete_forever_outlined, color: AppColors.err),
            title: const Text('删除任务和文件', style: TextStyle(color: AppColors.err)),
            onTap: () => Navigator.pop(context, true),
          ),
          ListTile(
            leading: const Icon(Icons.playlist_remove),
            title: const Text('只删除任务，保留文件'),
            onTap: () => Navigator.pop(context, false),
          ),
          ListTile(
            leading: const Icon(Icons.close),
            title: const Text('取消'),
            onTap: () => Navigator.pop(context),
          ),
        ],
      ),
    ),
  );
}

class _DownloadingTile extends StatelessWidget {
  const _DownloadingTile({
    required this.job,
    required this.api,
    required this.busy,
    required this.onToggle,
    required this.onRemove,
  });

  final DownloadJob job;
  final ApiClient api;
  final bool busy;
  final VoidCallback onToggle;
  final VoidCallback onRemove;

  @override
  Widget build(BuildContext context) {
    final paused = job.isPaused;
    final error = job.isError;
    final waiting = job.status == 'queued' || job.status == 'waiting';
    String status;
    Color statusColor = AppColors.muted;
    if (error) {
      status = job.errorMessage ?? '下载出错';
      statusColor = AppColors.err;
    } else if (paused) {
      status = '已暂停';
    } else if (waiting) {
      status = '等待中';
    } else {
      status = formatSpeed(job.downloadSpeed);
      statusColor = AppColors.accent;
    }
    final sizeText = job.totalLength > 0
        ? '${formatBytes(job.completedLength)} / ${formatBytes(job.totalLength)}'
        : '正在获取文件信息';
    return InkWell(
      onTap: busy ? null : onToggle,
      onLongPress: onRemove,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 8, 8),
        child: Row(
          children: [
            SizedBox(width: 112, child: CoverImage(url: api.resolveImage(job.imageUrl), aspectRatio: 16 / 10, radius: 8)),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(job.title, maxLines: 2, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 13.5, height: 1.3)),
                  const SizedBox(height: 8),
                  ClipRRect(
                    borderRadius: BorderRadius.circular(2),
                    child: LinearProgressIndicator(
                      value: job.progress,
                      minHeight: 3,
                      backgroundColor: AppColors.panel,
                      color: paused || error ? AppColors.muted : AppColors.accent,
                    ),
                  ),
                  const SizedBox(height: 6),
                  Row(
                    children: [
                      Expanded(
                        child: Text(status,
                            maxLines: 1, overflow: TextOverflow.ellipsis, style: TextStyle(fontSize: 12, color: statusColor)),
                      ),
                      Text(sizeText, style: const TextStyle(fontSize: 12, color: AppColors.muted)),
                    ],
                  ),
                ],
              ),
            ),
            IconButton(
              tooltip: paused || error ? '继续' : '暂停',
              onPressed: busy ? null : onToggle,
              icon: busy
                  ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2))
                  : Icon(paused || error ? Icons.play_arrow_rounded : Icons.pause_rounded),
            ),
          ],
        ),
      ),
    );
  }
}

/// 一部番已缓存的分集列表。
class CachedGroupScreen extends StatefulWidget {
  const CachedGroupScreen({super.key, required this.groupKey, required this.initial});

  final String groupKey;
  final CachedGroup initial;

  @override
  State<CachedGroupScreen> createState() => _CachedGroupScreenState();
}

class _CachedGroupScreenState extends State<CachedGroupScreen> {
  late CachedGroup _group = widget.initial;

  ApiClient get _api => SessionScope.read(context).api!;

  Future<void> _refresh() async {
    try {
      final jobs = await _api.downloads();
      final groups = groupCachedJobs(jobs.where((j) => j.isComplete && !j.isRemoved).toList());
      final group = groups.where((g) => g.key == widget.groupKey).firstOrNull;
      if (!mounted) return;
      if (group == null) {
        Navigator.of(context).pop();
      } else {
        setState(() => _group = group);
      }
    } catch (e) {
      if (mounted) showToast(context, e.toString(), error: true);
    }
  }

  Future<void> _remove(DownloadJob job) async {
    final deleteFiles = await confirmRemove(context, job);
    if (deleteFiles == null) return;
    try {
      await _api.removeDownload(job.gid, deleteLocalFiles: deleteFiles);
      await _refresh();
    } catch (e) {
      if (mounted) showToast(context, e.toString(), error: true);
    }
  }

  @override
  Widget build(BuildContext context) {
    final session = SessionScope.of(context);
    return Scaffold(
      appBar: AppBar(title: Text(_group.name, maxLines: 1, overflow: TextOverflow.ellipsis)),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView.separated(
          padding: const EdgeInsets.symmetric(vertical: 8),
          itemCount: _group.jobs.length,
          separatorBuilder: (_, _) => const SizedBox(height: 4),
          itemBuilder: (context, index) {
            final job = _group.jobs[index];
            final episode = episodeOf(job.title);
            final record = session.record(job.gid);
            String watched = '未观看';
            if (record != null) {
              watched = record.fraction > 0.95 || !record.resumable && record.position > 8
                  ? '已看完'
                  : '看到 ${formatDuration(Duration(seconds: record.position.round()))}';
            }
            return InkWell(
              onTap: () => Navigator.of(context).push(MaterialPageRoute(builder: (_) => PlayerScreen(job: job))),
              onLongPress: () => _remove(job),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
                child: Row(
                  children: [
                    SizedBox(
                      width: 140,
                      child: Stack(
                        children: [
                          CoverImage(
                            url: _api.resolveImage('/api/downloads/${Uri.encodeComponent(job.gid)}/thumbnail'),
                            aspectRatio: 16 / 9,
                            radius: 8,
                          ),
                          if (record != null)
                            Positioned(
                              left: 0,
                              right: 0,
                              bottom: 0,
                              child: ClipRRect(
                                borderRadius: const BorderRadius.vertical(bottom: Radius.circular(8)),
                                child: LinearProgressIndicator(value: record.fraction, minHeight: 3, backgroundColor: Colors.black45),
                              ),
                            ),
                        ],
                      ),
                    ),
                    const SizedBox(width: 12),
                    Expanded(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          Text(episode == null ? job.title : '第 $episode 集',
                              maxLines: 2,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
                          const SizedBox(height: 4),
                          Text(job.fileName ?? job.title,
                              maxLines: 1, overflow: TextOverflow.ellipsis, style: const TextStyle(fontSize: 11.5, color: AppColors.muted)),
                          const SizedBox(height: 4),
                          Text('${formatBytes(job.totalLength)} · $watched',
                              style: TextStyle(
                                  fontSize: 12,
                                  color: record != null && record.resumable ? AppColors.accent : AppColors.muted)),
                        ],
                      ),
                    ),
                  ],
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}
