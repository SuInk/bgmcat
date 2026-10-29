import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:video_player/video_player.dart';
import 'package:wakelock_plus/wakelock_plus.dart';

import '../api.dart';
import '../ass.dart';
import '../main.dart';
import '../models.dart';
import '../session.dart';
import '../theme.dart';
import '../util.dart';
import '../widgets.dart';
import 'detail.dart';

class PlayerScreen extends StatefulWidget {
  const PlayerScreen({super.key, required this.job, this.anime});

  final DownloadJob job;

  /// 从详情页进来时已经知道是哪部番；从缓存页、首页进来时为空，播放页自己去认。
  final Anime? anime;

  @override
  State<PlayerScreen> createState() => _PlayerScreenState();
}

class _PlayerScreenState extends State<PlayerScreen> {
  late DownloadJob _job = widget.job;
  PlaybackInfo? _info;
  VideoPlayerController? _controller;
  String? _error;
  bool _initializing = true;

  AssSubtitle? _subtitle;
  SubtitleTrack? _track;
  bool _subtitleLoading = false;

  bool _controlsVisible = true;
  bool _fullscreen = false;
  Timer? _hideTimer;
  Timer? _saveTimer;
  double? _dragValue;

  /// 左下角「已从 xx:xx 继续播放」小条，几秒后自己消失。
  String? _resumeHint;
  Timer? _resumeHintTimer;

  /// 同一部番的下载任务（含还在下的），用来标出哪些集能直接播。
  List<DownloadJob> _siblings = [];
  AnimeDetail? _animeDetail;
  final _episodeScroll = ScrollController();
  bool _episodeScrolled = false;
  late AppSession _session;
  late ApiClient _api;

  @override
  void initState() {
    super.initState();
    _session = SessionScope.read(context);
    _api = _session.api!;
    _open(widget.job);
    _loadEpisodes();
  }

  @override
  void dispose() {
    _saveProgress();
    _hideTimer?.cancel();
    _saveTimer?.cancel();
    _resumeHintTimer?.cancel();
    _controller?.removeListener(_onTick);
    _controller?.dispose();
    _episodeScroll.dispose();
    WakelockPlus.disable();
    _restoreSystemUi();
    super.dispose();
  }

  Future<void> _loadEpisodes() async {
    try {
      final jobs = await _api.downloads();
      final cover = widget.job.imageUrl;
      final name = animeNameOf(widget.job.title);
      final siblings = jobs
          .where((j) => !j.isRemoved && (cover != null ? j.imageUrl == cover : animeNameOf(j.title) == name))
          .toList();
      if (mounted) setState(() => _siblings = siblings);
    } catch (_) {
      // 拿不到任务列表就只标当前这一集
    }
    try {
      final animeId = widget.anime?.animeId ?? await _resolveAnimeId();
      if (animeId == null) return;
      final detail = await _api.animeDetail(animeId);
      if (mounted) setState(() => _animeDetail = detail);
    } catch (_) {
      // 番剧信息拿不到时，选集退回只列已缓存的
    }
  }

  /// 下载任务没记番剧 id：先从 bgm.tv 封面地址里取条目号（…/cover/l/xx/xx/622288_xxx.jpg），
  /// 取不到再按标题里的番名搜一次，封面对得上的优先。
  Future<String?> _resolveAnimeId() async {
    final cover = widget.job.imageUrl;
    final match = cover == null ? null : RegExp(r'bgm\.tv/.*/(\d+)_[^/]+\.\w+$').firstMatch(cover);
    if (match != null) return match.group(1);
    final results = await _api.search(animeNameOf(widget.job.title));
    if (results.isEmpty) return null;
    return (results.where((a) => a.imageUrl == cover).firstOrNull ?? results.first).animeId;
  }

  Future<void> _open(DownloadJob job) async {
    _saveProgress();
    final old = _controller;
    old?.removeListener(_onTick);
    setState(() {
      _job = job;
      _controller = null;
      _info = null;
      _error = null;
      _initializing = true;
      _subtitle = null;
      _track = null;
      _resumeHint = null;
    });
    await old?.dispose();

    try {
      final info = await _api.playbackInfo(job.gid);
      if (!mounted || _job.gid != job.gid) return;
      final url = info.usesHls ? _api.hlsUrl(job.gid) : _api.streamUrl(job.gid);
      final controller = VideoPlayerController.networkUrl(Uri.parse(url));
      setState(() {
        _info = info;
        _controller = controller;
      });
      // 有字幕默认开第一条（优先简体），和 web 播放器一致
      final preferred = info.subtitles.where((t) => t.isAss && t.label.contains('简')).firstOrNull ??
          info.subtitles.where((t) => t.isAss).firstOrNull;
      if (preferred != null) unawaited(_selectSubtitle(preferred));

      await controller.initialize();
      if (!mounted || _controller != controller) return;
      controller.addListener(_onTick);

      final record = _session.record(job.gid);
      if (record != null && record.resumable) {
        await controller.seekTo(Duration(milliseconds: (record.position * 1000).round()));
        _showResumeHint('已从 ${formatDuration(Duration(seconds: record.position.round()))} 继续播放');
      }
      await controller.play();
      WakelockPlus.enable();
      _saveTimer?.cancel();
      _saveTimer = Timer.periodic(const Duration(seconds: 5), (_) => _saveProgress());
      _scheduleHide();
    } catch (e) {
      if (!mounted) return;
      _session.handleError(e);
      setState(() => _error = e is ApiException ? e.message : '视频加载失败：$e');
    } finally {
      if (mounted) setState(() => _initializing = false);
    }
  }

  void _onTick() {
    if (!mounted) return;
    final value = _controller?.value;
    if (value != null && value.hasError && _error == null) {
      setState(() => _error = '播放出错：${value.errorDescription ?? '未知错误'}');
      return;
    }
    setState(() {});
  }

  void _saveProgress() {
    final controller = _controller;
    if (controller == null || !controller.value.isInitialized) return;
    final position = controller.value.position.inMilliseconds / 1000;
    if (position <= 0) return;
    final duration = controller.value.duration.inMilliseconds / 1000;
    _session.saveProgress(
      _job.gid,
      PlaybackRecord(
        position: position,
        duration: duration > 0 ? duration : (_info?.duration ?? 0),
        updatedAt: DateTime.now().millisecondsSinceEpoch,
        title: _job.title,
        imageUrl: _job.imageUrl,
      ),
    );
  }

  Future<void> _selectSubtitle(SubtitleTrack? track) async {
    if (track == null) {
      setState(() {
        _track = null;
        _subtitle = null;
      });
      return;
    }
    final gid = _job.gid;
    setState(() {
      _track = track;
      _subtitleLoading = true;
    });
    try {
      final text = await _api.assSubtitle(gid, track.id);
      final parsed = AssSubtitle.parse(text);
      if (mounted && _job.gid == gid && _track == track) setState(() => _subtitle = parsed);
    } catch (e) {
      if (mounted && _track == track) {
        setState(() => _track = null);
        showToast(context, '字幕加载失败：$e', error: true);
      }
    } finally {
      if (mounted) setState(() => _subtitleLoading = false);
    }
  }

  void _showResumeHint(String text) {
    _resumeHintTimer?.cancel();
    setState(() => _resumeHint = text);
    _resumeHintTimer = Timer(const Duration(seconds: 5), () {
      if (mounted) setState(() => _resumeHint = null);
    });
  }

  void _scheduleHide() {
    _hideTimer?.cancel();
    _hideTimer = Timer(const Duration(seconds: 4), () {
      if (mounted && (_controller?.value.isPlaying ?? false)) setState(() => _controlsVisible = false);
    });
  }

  void _toggleControls() {
    setState(() => _controlsVisible = !_controlsVisible);
    if (_controlsVisible) _scheduleHide();
  }

  void _togglePlay() {
    final controller = _controller;
    if (controller == null || !controller.value.isInitialized) return;
    if (controller.value.isPlaying) {
      controller.pause();
      WakelockPlus.disable();
      _saveProgress();
    } else {
      controller.play();
      WakelockPlus.enable();
    }
    _scheduleHide();
  }

  Future<void> _seekRelative(int seconds) async {
    final controller = _controller;
    if (controller == null || !controller.value.isInitialized) return;
    final target = controller.value.position + Duration(seconds: seconds);
    await controller.seekTo(target < Duration.zero ? Duration.zero : target);
    _scheduleHide();
  }

  Future<void> _setFullscreen(bool value) async {
    setState(() => _fullscreen = value);
    if (value) {
      await SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
      await SystemChrome.setPreferredOrientations([DeviceOrientation.landscapeLeft, DeviceOrientation.landscapeRight]);
    } else {
      await _restoreSystemUi();
    }
    _scheduleHide();
  }

  Future<void> _restoreSystemUi() async {
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.edgeToEdge);
    await SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  }

  String get _title {
    final episode = episodeOf(_job.title);
    final name = animeNameOf(_job.title);
    return episode == null ? name : '$name 第 $episode 集';
  }

  @override
  Widget build(BuildContext context) {
    final video = _buildVideo();
    if (_fullscreen) {
      return PopScope(
        canPop: false,
        onPopInvokedWithResult: (didPop, _) {
          if (!didPop) _setFullscreen(false);
        },
        child: Scaffold(backgroundColor: Colors.black, body: video),
      );
    }
    return Scaffold(
      backgroundColor: AppColors.background,
      body: Column(
        children: [
          Container(
            color: Colors.black,
            padding: EdgeInsets.only(top: MediaQuery.paddingOf(context).top),
            child: AspectRatio(aspectRatio: 16 / 9, child: video),
          ),
          Expanded(child: _buildInfoPanel()),
        ],
      ),
    );
  }

  Widget _buildVideo() {
    final controller = _controller;
    final ready = controller != null && controller.value.isInitialized;
    final value = controller?.value;
    final position = value?.position ?? Duration.zero;
    final subtitle = _subtitle;
    final cues = subtitle?.at(position) ?? const <AssCue>[];

    return GestureDetector(
      behavior: HitTestBehavior.opaque,
      onTap: _toggleControls,
      onDoubleTap: _togglePlay,
      child: Stack(
        fit: StackFit.expand,
        children: [
          Container(color: Colors.black),
          if (ready)
            Center(
              child: AspectRatio(
                aspectRatio: value!.aspectRatio > 0 ? value.aspectRatio : 16 / 9,
                child: Stack(
                  fit: StackFit.expand,
                  children: [
                    VideoPlayer(controller),
                    // 字幕画在视频画面的矩形里，\pos 定位才能对得上
                    if (subtitle != null && cues.isNotEmpty) AssLayer(subtitle: subtitle, cues: cues, liftBottom: _controlsVisible ? 44 : 0),
                  ],
                ),
              ),
            ),
          if (_error != null)
            Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(_error!, textAlign: TextAlign.center, style: const TextStyle(color: Colors.white70)),
                    const SizedBox(height: 12),
                    FilledButton.tonal(onPressed: () => _open(_job), child: const Text('重试')),
                  ],
                ),
              ),
            )
          else if (_initializing || (value?.isBuffering ?? false))
            const Center(child: CircularProgressIndicator(color: Colors.white70)),
          if (_info?.usesHls == true && _initializing)
            const Positioned(
              left: 0,
              right: 0,
              bottom: 24,
              child: Text('首次播放需要服务器实时转码，请稍候…',
                  textAlign: TextAlign.center, style: TextStyle(color: Colors.white54, fontSize: 12)),
            ),
          AnimatedOpacity(
            opacity: _controlsVisible ? 1 : 0,
            duration: const Duration(milliseconds: 200),
            child: IgnorePointer(ignoring: !_controlsVisible, child: _buildControls(ready)),
          ),
          if (_resumeHint != null)
            Positioned(
              left: 12,
              bottom: _controlsVisible ? 56 : 12,
              child: Container(
                padding: const EdgeInsets.only(left: 12),
                decoration: BoxDecoration(color: Colors.black.withValues(alpha: 0.72), borderRadius: BorderRadius.circular(6)),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Text(_resumeHint!, style: const TextStyle(color: Colors.white, fontSize: 12)),
                    TextButton(
                      style: TextButton.styleFrom(
                          visualDensity: VisualDensity.compact, foregroundColor: AppColors.accent),
                      onPressed: () {
                        _controller?.seekTo(Duration.zero);
                        _resumeHintTimer?.cancel();
                        setState(() => _resumeHint = null);
                      },
                      child: const Text('从头播放', style: TextStyle(fontSize: 12)),
                    ),
                  ],
                ),
              ),
            ),
        ],
      ),
    );
  }

  Widget _buildControls(bool ready) {
    final controller = _controller;
    final value = controller?.value;
    final duration = ready ? value!.duration : Duration(seconds: (_info?.duration ?? 0).round());
    final position = ready ? value!.position : Duration.zero;
    final maxMs = duration.inMilliseconds.toDouble();
    final sliderValue = (_dragValue ?? position.inMilliseconds.toDouble()).clamp(0.0, maxMs <= 0 ? 1.0 : maxMs);
    final playing = value?.isPlaying ?? false;
    final buffered = ready && value!.buffered.isNotEmpty ? value.buffered.last.end : Duration.zero;
    final topInset = _fullscreen ? 12.0 : 0.0;

    return DecoratedBox(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topCenter,
          end: Alignment.bottomCenter,
          colors: [Color(0xAA000000), Colors.transparent, Colors.transparent, Color(0xAA000000)],
          stops: [0, 0.28, 0.65, 1],
        ),
      ),
      child: Column(
        children: [
          // 标题放在画面上方（B 站样式）
          Padding(
            padding: EdgeInsets.fromLTRB(_fullscreen ? 24 : 0, topInset, 8, 0),
            child: Row(
              children: [
                IconButton(
                  tooltip: '返回',
                  color: Colors.white,
                  icon: const Icon(Icons.arrow_back_ios_new, size: 20),
                  onPressed: () => _fullscreen ? _setFullscreen(false) : Navigator.of(context).maybePop(),
                ),
                Expanded(
                  child: Text(_title,
                      maxLines: 1,
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(color: Colors.white, fontSize: 15, fontWeight: FontWeight.w600)),
                ),
              ],
            ),
          ),
          Expanded(
            child: Center(
              child: !ready
                  ? const SizedBox.shrink()
                  : Row(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        IconButton(
                            tooltip: '后退 10 秒',
                            iconSize: 30,
                            color: Colors.white,
                            onPressed: () => _seekRelative(-10),
                            icon: const Icon(Icons.replay_10)),
                        const SizedBox(width: 24),
                        IconButton(
                          tooltip: playing ? '暂停' : '播放',
                          iconSize: 48,
                          color: Colors.white,
                          onPressed: _togglePlay,
                          icon: Icon(playing ? Icons.pause_circle_filled : Icons.play_circle_fill),
                        ),
                        const SizedBox(width: 24),
                        IconButton(
                            tooltip: '快进 10 秒',
                            iconSize: 30,
                            color: Colors.white,
                            onPressed: () => _seekRelative(10),
                            icon: const Icon(Icons.forward_10)),
                      ],
                    ),
            ),
          ),
          Padding(
            padding: EdgeInsets.fromLTRB(_fullscreen ? 24 : 4, 0, _fullscreen ? 24 : 4, _fullscreen ? 12 : 0),
            child: Row(
              children: [
                IconButton(
                  tooltip: playing ? '暂停' : '播放',
                  color: Colors.white,
                  onPressed: ready ? _togglePlay : null,
                  icon: Icon(playing ? Icons.pause : Icons.play_arrow),
                ),
                Text(formatDuration(Duration(milliseconds: sliderValue.round())),
                    style: const TextStyle(color: Colors.white, fontSize: 12, fontFeatures: [FontFeature.tabularFigures()])),
                Expanded(
                  child: SliderTheme(
                    data: SliderTheme.of(context).copyWith(
                      trackHeight: 2.5,
                      thumbShape: const RoundSliderThumbShape(enabledThumbRadius: 6),
                      overlayShape: const RoundSliderOverlayShape(overlayRadius: 12),
                      activeTrackColor: AppColors.accent,
                      inactiveTrackColor: Colors.white24,
                      secondaryActiveTrackColor: Colors.white54,
                      thumbColor: Colors.white,
                    ),
                    child: Slider(
                      min: 0,
                      max: maxMs <= 0 ? 1 : maxMs,
                      value: sliderValue,
                      secondaryTrackValue: buffered.inMilliseconds.toDouble().clamp(0.0, maxMs <= 0 ? 1.0 : maxMs),
                      onChangeStart: ready ? (_) => _hideTimer?.cancel() : null,
                      onChanged: ready ? (v) => setState(() => _dragValue = v) : null,
                      onChangeEnd: ready
                          ? (v) async {
                              await controller!.seekTo(Duration(milliseconds: v.round()));
                              if (mounted) setState(() => _dragValue = null);
                              _scheduleHide();
                            }
                          : null,
                    ),
                  ),
                ),
                Text(formatDuration(duration),
                    style: const TextStyle(color: Colors.white, fontSize: 12, fontFeatures: [FontFeature.tabularFigures()])),
                if ((_info?.subtitles.isNotEmpty ?? false))
                  IconButton(
                    tooltip: '字幕',
                    color: _track != null ? AppColors.accent : Colors.white,
                    onPressed: _pickSubtitle,
                    icon: _subtitleLoading
                        ? const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                        : Icon(_track != null ? Icons.subtitles : Icons.subtitles_off_outlined),
                  ),
                IconButton(
                  tooltip: _fullscreen ? '退出全屏' : '全屏',
                  color: Colors.white,
                  onPressed: () => _setFullscreen(!_fullscreen),
                  icon: Icon(_fullscreen ? Icons.fullscreen_exit : Icons.fullscreen),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Future<void> _pickSubtitle() async {
    _hideTimer?.cancel();
    final tracks = _info?.subtitles ?? const <SubtitleTrack>[];
    final selected = await showModalBottomSheet<Object>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Padding(
              padding: EdgeInsets.only(bottom: 8),
              child: Text('字幕', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700)),
            ),
            ListTile(
              title: const Text('关闭字幕'),
              trailing: _track == null ? const Icon(Icons.check, color: AppColors.accent) : null,
              onTap: () => Navigator.pop(context, 'off'),
            ),
            for (final track in tracks)
              ListTile(
                enabled: track.isAss,
                title: Text(track.label),
                subtitle: track.isAss ? null : Text('${track.codec ?? '未知'} 格式暂不支持'),
                trailing: _track?.id == track.id ? const Icon(Icons.check, color: AppColors.accent) : null,
                onTap: () => Navigator.pop(context, track),
              ),
          ],
        ),
      ),
    );
    if (selected == 'off') {
      await _selectSubtitle(null);
    } else if (selected is SubtitleTrack) {
      await _selectSubtitle(selected);
    }
    _scheduleHide();
  }

  Widget _buildInfoPanel() {
    final info = _info;
    return ListView(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 24),
      children: [
        Text(_title, style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w700)),
        const SizedBox(height: 6),
        Text(_job.title, style: const TextStyle(fontSize: 12.5, color: AppColors.muted, height: 1.4)),
        if (info != null) ...[
          const SizedBox(height: 10),
          Wrap(spacing: 6, runSpacing: 6, children: [
            _tag(switch (info.deliveryMode) {
              'hls-transcode' => '服务器转码',
              'hls-remux' => '服务器转封装',
              _ => '直接播放',
            }),
            _tag(formatBytes(_job.totalLength)),
            if (info.subtitles.isNotEmpty) _tag('${info.subtitles.length} 条字幕'),
          ]),
        ],
        ..._buildEpisodes(),
      ],
    );
  }

  /// 选集：有番剧信息时列全部集数（B 站那种横向卡片），已缓存的点了直接切，
  /// 没缓存的提示去详情页下载；拿不到番剧信息时只列已缓存的。
  List<Widget> _buildEpisodes() {
    final jobsByEpisode = <int, DownloadJob>{};
    for (final job in _siblings) {
      final n = episodeOf(job.title);
      if (n == null) continue;
      final existing = jobsByEpisode[n];
      if (existing == null || (job.isComplete && !existing.isComplete) || job.gid == _job.gid) jobsByEpisode[n] = job;
    }
    final titles = <int, String>{};
    final airDates = <int, DateTime?>{};
    final numbers = <int>[];
    final detail = _animeDetail;
    if (detail != null) {
      for (final ep in detail.episodes) {
        final n = ep.numberValue;
        if (n == null || numbers.contains(n)) continue;
        numbers.add(n);
        titles[n] = ep.title;
        airDates[n] = DateTime.tryParse(ep.airDate ?? '');
      }
      if (numbers.isEmpty && (detail.totalEpisodes ?? 0) > 0) {
        numbers.addAll(List.generate(detail.totalEpisodes!, (i) => i + 1));
      }
    }
    for (final n in jobsByEpisode.keys) {
      if (!numbers.contains(n)) numbers.add(n);
    }
    numbers.sort();
    if (numbers.isEmpty) return const [];

    final current = episodeOf(_job.title);
    // 番剧信息到了以后把正在播的那集滚到可见位置，只滚一次，免得每帧重建都把列表拽回去
    if (!_episodeScrolled && detail != null && current != null) {
      _episodeScrolled = true;
      final index = numbers.indexOf(current);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!_episodeScroll.hasClients || index < 0) return;
        final target = (index * 124.0 - 40).clamp(0.0, _episodeScroll.position.maxScrollExtent);
        _episodeScroll.jumpTo(target);
      });
    }
    final cached = jobsByEpisode.values.where((j) => j.isComplete).length;
    final total = detail?.totalEpisodes ?? numbers.length;
    final now = DateTime.now();

    return [
      const SizedBox(height: 22),
      Row(
        children: [
          const Text('选集', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700)),
          const SizedBox(width: 8),
          Text('全 $total 集 · 已缓存 $cached 集', style: const TextStyle(fontSize: 12, color: AppColors.muted)),
          const Spacer(),
          if (detail != null)
            TextButton(
              style: TextButton.styleFrom(visualDensity: VisualDensity.compact),
              onPressed: () => _openDetail(detail.anime),
              child: const Text('番剧详情'),
            ),
        ],
      ),
      const SizedBox(height: 8),
      SizedBox(
        height: 68,
        child: ListView.separated(
          controller: _episodeScroll,
          scrollDirection: Axis.horizontal,
          itemCount: numbers.length,
          separatorBuilder: (_, _) => const SizedBox(width: 8),
          itemBuilder: (context, index) {
            final n = numbers[index];
            final job = jobsByEpisode[n];
            final airDate = airDates[n];
            return _episodeCard(
              number: n,
              title: titles[n],
              job: job,
              playing: n == current,
              notAired: job == null && airDate != null && airDate.isAfter(now),
              detail: detail,
            );
          },
        ),
      ),
    ];
  }

  Widget _episodeCard({
    required int number,
    required String? title,
    required DownloadJob? job,
    required bool playing,
    required bool notAired,
    required AnimeDetail? detail,
  }) {
    final cached = job != null && job.isComplete;
    final downloading = job != null && !job.isComplete && !job.isError;
    final watched = cached && (_session.record(job.gid)?.fraction ?? 0) > 0.9;
    String status;
    if (playing) {
      status = '正在播放';
    } else if (cached) {
      status = watched ? '已看完' : '已缓存';
    } else if (downloading) {
      status = '缓存中 ${(job.progress * 100).round()}%';
    } else if (notAired) {
      status = '未播出';
    } else {
      status = '未缓存';
    }
    final enabled = cached || playing;
    return SizedBox(
      width: 116,
      child: Material(
        color: playing ? AppColors.accent.withValues(alpha: 0.16) : AppColors.panel,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(10),
          side: BorderSide(color: playing ? AppColors.accent : Colors.transparent, width: 1.2),
        ),
        child: InkWell(
          borderRadius: BorderRadius.circular(10),
          onTap: () {
            if (playing) return;
            if (cached) {
              _open(job);
            } else if (detail != null && !notAired) {
              showToast(context, downloading ? '第 $number 集还在缓存' : '第 $number 集还没缓存，可以去番剧详情里下载');
            }
          },
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [
                Text(
                  title == null || title.isEmpty ? '第 $number 集' : '$number  $title',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: TextStyle(
                    fontSize: 13,
                    fontWeight: FontWeight.w600,
                    color: playing ? AppColors.accent : (enabled ? AppColors.ink : AppColors.muted),
                  ),
                ),
                Row(
                  children: [
                    if (playing) const Icon(Icons.graphic_eq, size: 13, color: AppColors.accent),
                    if (!playing && cached) const Icon(Icons.check_circle, size: 12, color: AppColors.accent),
                    if (playing || cached) const SizedBox(width: 4),
                    Text(
                      status,
                      style: TextStyle(fontSize: 11.5, color: playing || cached ? AppColors.accent : AppColors.muted),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }

  void _openDetail(Anime anime) {
    // 从详情页进来的就直接回去，别叠两层详情
    if (widget.anime != null) {
      Navigator.of(context).maybePop();
      return;
    }
    Navigator.of(context).push(MaterialPageRoute(builder: (_) => DetailScreen(anime: anime)));
  }

  Widget _tag(String text) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 3),
        decoration: BoxDecoration(color: AppColors.panel, borderRadius: BorderRadius.circular(6)),
        child: Text(text, style: const TextStyle(fontSize: 12, color: AppColors.inkSecondary)),
      );
}

/// 按 ASS 的 PlayRes 坐标把字幕画到视频画面上：有 \pos 的（屏幕文字翻译）放到原位，
/// 其余按对齐方位叠在底部 / 顶部 / 中间。
class AssLayer extends StatelessWidget {
  const AssLayer({super.key, required this.subtitle, required this.cues, this.liftBottom = 0});

  final AssSubtitle subtitle;
  final List<AssCue> cues;

  /// 控制条露出来时底部字幕往上让开，别压在进度条上。
  final double liftBottom;

  @override
  Widget build(BuildContext context) {
    return IgnorePointer(
      child: LayoutBuilder(builder: (context, constraints) {
        final scaleX = constraints.maxWidth / subtitle.playResX;
        final scaleY = constraints.maxHeight / subtitle.playResY;
        // 手机竖屏画面很小，字号按比例算下来看不清，兜个底
        double size(AssCue cue) => (cue.fontSize > 0 ? cue.fontSize * scaleY : constraints.maxHeight / 18).clamp(9.0, 64.0);
        final margin = constraints.maxHeight * 0.04;

        final positioned = cues.where((c) => c.pos != null);
        final flowing = cues.where((c) => c.pos == null);
        Widget column(Iterable<AssCue> group) => Column(
              mainAxisSize: MainAxisSize.min,
              children: [for (final cue in group) _AssText(cue: cue, size: size(cue))],
            );

        return Stack(
          children: [
            for (final cue in positioned)
              Positioned(
                left: cue.pos!.dx * scaleX,
                top: cue.pos!.dy * scaleY,
                child: FractionalTranslation(
                  translation: Offset(
                    switch (cue.align % 3) { 1 => 0.0, 2 => -0.5, _ => -1.0 },
                    cue.align >= 7 ? 0.0 : (cue.align >= 4 ? -0.5 : -1.0),
                  ),
                  child: ConstrainedBox(
                    constraints: BoxConstraints(maxWidth: constraints.maxWidth),
                    child: _AssText(cue: cue, size: size(cue)),
                  ),
                ),
              ),
            Positioned(left: margin, right: margin, bottom: margin + liftBottom, child: column(flowing.where((c) => c.align <= 3))),
            Positioned(left: margin, right: margin, top: margin, child: column(flowing.where((c) => c.top))),
            Positioned.fill(
              child: Center(child: column(flowing.where((c) => c.align >= 4 && c.align <= 6))),
            ),
          ],
        );
      }),
    );
  }
}

class _AssText extends StatelessWidget {
  const _AssText({required this.cue, required this.size});

  final AssCue cue;
  final double size;

  @override
  Widget build(BuildContext context) {
    final align = switch (cue.align % 3) { 1 => TextAlign.left, 0 => TextAlign.right, _ => TextAlign.center };
    final style = TextStyle(fontSize: size, fontWeight: FontWeight.w600, height: 1.25);
    final fill = Text(cue.text, textAlign: align, style: style.copyWith(color: cue.color));
    if (!cue.outline) return fill;
    return Stack(
      children: [
        Text(
          cue.text,
          textAlign: align,
          style: style.copyWith(
            foreground: Paint()
              ..style = PaintingStyle.stroke
              ..strokeWidth = (size / 7).clamp(1.5, 5.0)
              ..strokeJoin = StrokeJoin.round
              ..color = Colors.black,
          ),
        ),
        fill,
      ],
    );
  }
}
