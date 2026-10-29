// 与 shared/models.ts 对应的最小子集，只取 App 用得到的字段。

String? _str(dynamic v) => v == null ? null : (v is String ? (v.isEmpty ? null : v) : v.toString());
double? _num(dynamic v) => v is num ? v.toDouble() : (v is String ? double.tryParse(v) : null);
int _int(dynamic v) => v is num ? v.toInt() : (v is String ? int.tryParse(v) ?? 0 : 0);

class Anime {
  Anime(this.raw);

  factory Anime.fromJson(Map<String, dynamic> json) => Anime(json);

  /// 原始 JSON：收藏 / 订阅接口要整条 HomepageAnime 原样传回去。
  final Map<String, dynamic> raw;

  String get animeId => raw['animeId'].toString();
  String get title => _str(raw['title']) ?? '未命名';
  String? get imageUrl => _str(raw['imageUrl']);
  String? get searchKeyword => _str(raw['searchKeyword']);
  double? get rating => _num(raw['rating']);
  bool get isOnAir => raw['isOnAir'] == true;
  String? get broadcastAt => _str(raw['broadcastAt']);

  /// 星期几，0 是周日（和 web 端一致：优先用放送时刻推，拿不到再用后端算的 airDay）。
  int? get weekday {
    final at = broadcastAt == null ? null : DateTime.tryParse(broadcastAt!)?.toLocal();
    if (at != null) return at.weekday % 7;
    final day = raw['airDay'];
    return day is num ? day.toInt() % 7 : null;
  }
}

class AnimeEpisode {
  AnimeEpisode({required this.title, this.number, this.airDate});

  factory AnimeEpisode.fromJson(Map<String, dynamic> json) => AnimeEpisode(
        title: _str(json['title']) ?? '',
        number: _str(json['episodeNumber']),
        airDate: _str(json['airDate']),
      );

  final String title;
  final String? number;
  final String? airDate;

  int? get numberValue => number == null ? null : double.tryParse(number!)?.toInt();
}

class ResourceItem {
  ResourceItem(this.raw);

  final Map<String, dynamic> raw;

  String get id => raw['id'].toString();
  String get title => _str(raw['title']) ?? '';
  String? get subgroup => _str(raw['subgroup']);
  String? get magnetUrl => _str(raw['magnetUrl']);
  String? get torrentUrl => _str(raw['torrentUrl']);
  String? get pageUrl => _str(raw['pageUrl']);
  String? get sizeText => _str(raw['sizeText']);
  String? get publishedAt => _str(raw['publishedAt']);
  int? get seeders => raw['seeders'] is num ? (raw['seeders'] as num).toInt() : null;
  int? get episode => raw['episode'] is num ? (raw['episode'] as num).toInt() : null;
  String? get resolution => _str(raw['resolution']);
}

class PlatformScore {
  PlatformScore({required this.name, this.score, this.scoreMax = 10});

  final String name;
  final double? score;
  final double scoreMax;
}

class ProviderStatus {
  ProviderStatus({required this.name, required this.ok, required this.pending, this.error});

  final String name;
  final bool ok;
  final bool pending;
  final String? error;
}

class AnimeDetail {
  AnimeDetail({
    required this.anime,
    required this.titles,
    required this.summary,
    required this.episodes,
    required this.downloads,
    required this.tags,
    required this.rank,
    required this.totalEpisodes,
    required this.platforms,
    required this.providers,
    required this.pending,
    required this.downloadError,
  });

  factory AnimeDetail.fromJson(Map<String, dynamic> json) {
    List<T> list<T>(String key, T Function(Map<String, dynamic>) map) =>
        (json[key] as List? ?? const []).whereType<Map<String, dynamic>>().map(map).toList();
    return AnimeDetail(
      anime: Anime.fromJson(json['anime'] as Map<String, dynamic>),
      titles: (json['titles'] as List? ?? const []).map((e) => e.toString()).toList(),
      summary: _str(json['summary']) ?? _str(json['intro']),
      episodes: list('episodes', AnimeEpisode.fromJson),
      downloads: list('downloads', ResourceItem.new),
      tags: (json['tags'] as List? ?? const []).map((e) => e.toString()).toList(),
      rank: json['rank'] is num ? (json['rank'] as num).toInt() : null,
      totalEpisodes: json['totalEpisodes'] is num ? (json['totalEpisodes'] as num).toInt() : null,
      platforms: list(
          'platforms',
          (p) => PlatformScore(
              name: _str(p['displayName']) ?? _str(p['id']) ?? '',
              score: _num(p['score']),
              scoreMax: _num(p['scoreMax']) ?? 10)),
      providers: list(
          'providerStatuses',
          (p) => ProviderStatus(
              name: _str(p['displayName']) ?? _str(p['providerId']) ?? '',
              ok: p['ok'] == true,
              pending: p['pending'] == true,
              error: _str(p['error']))),
      pending: json['detailPending'] == true,
      downloadError: _str(json['downloadError']),
    );
  }

  final Anime anime;
  final List<String> titles;
  final String? summary;
  final List<AnimeEpisode> episodes;
  final List<ResourceItem> downloads;
  final List<String> tags;
  final int? rank;
  final int? totalEpisodes;
  final List<PlatformScore> platforms;
  final List<ProviderStatus> providers;
  final bool pending;
  final String? downloadError;

  /// 基本信息或某个资源源还没回来，需要过一会儿再拉一次。
  bool get stillLoading => pending || providers.any((p) => p.pending);
}

class FavoriteSubscription {
  FavoriteSubscription({required this.id, required this.autoDownload});

  final int id;
  final bool autoDownload;
}

class FavoriteAnime {
  FavoriteAnime({required this.animeId, required this.subscriptions});

  factory FavoriteAnime.fromJson(Map<String, dynamic> json) {
    final subs = (json['subscriptions'] as List? ?? const [])
        .whereType<Map<String, dynamic>>()
        .map((s) => FavoriteSubscription(id: _int(s['id']), autoDownload: s['autoDownload'] == true))
        .toList();
    if (subs.isEmpty && json['subscriptionId'] is num) {
      subs.add(FavoriteSubscription(
          id: _int(json['subscriptionId']), autoDownload: json['subscriptionAutoDownload'] == true));
    }
    return FavoriteAnime(animeId: json['animeId'].toString(), subscriptions: subs);
  }

  final String animeId;
  final List<FavoriteSubscription> subscriptions;

  bool get subscribed => subscriptions.isNotEmpty;
}

class DownloadJob {
  DownloadJob(this.raw);

  factory DownloadJob.fromJson(Map<String, dynamic> json) => DownloadJob(json);

  final Map<String, dynamic> raw;

  String get gid => raw['gid'].toString();
  String get title => _str(raw['feedItemTitle']) ?? fileName ?? '未命名任务';
  String get status => _str(raw['status']) ?? 'queued';
  String? get imageUrl => _str(raw['feedItemImageUrl']);
  String? get thumbnailUrl => _str(raw['thumbnailUrl']);
  int get totalLength => _int(raw['totalLength']);
  int get completedLength => _int(raw['completedLength']);
  int get downloadSpeed => _int(raw['downloadSpeed']);
  String? get errorMessage => _str(raw['errorMessage']) ?? _str(raw['issueMessage']);
  String? get filePath => _str(raw['filePath']);
  String? get fileName {
    final path = filePath;
    if (path == null) return null;
    return path.split(RegExp(r'[\\/]')).last;
  }

  DateTime? get createdAt => DateTime.tryParse(_str(raw['createdAt']) ?? '');

  bool get isComplete => status == 'complete';
  bool get isPaused => status == 'paused';
  bool get isError => status == 'error';
  bool get isRemoved => status == 'removed';
  double get progress => totalLength <= 0 ? 0 : (completedLength / totalLength).clamp(0, 1).toDouble();
}

class SubtitleTrack {
  SubtitleTrack({required this.id, required this.label, this.codec});

  final String id;
  final String label;
  final String? codec;

  bool get isAss => codec == 'ass' || codec == 'ssa';
}

class PlaybackInfo {
  PlaybackInfo({
    required this.gid,
    required this.fileName,
    required this.deliveryMode,
    required this.duration,
    required this.subtitles,
  });

  factory PlaybackInfo.fromJson(Map<String, dynamic> json) => PlaybackInfo(
        gid: json['gid'].toString(),
        fileName: _str(json['fileName']) ?? '',
        deliveryMode: _str(json['deliveryMode']) ?? 'direct',
        duration: _num(json['durationSeconds']),
        subtitles: (json['subtitleTracks'] as List? ?? const [])
            .whereType<Map<String, dynamic>>()
            .map((t) => SubtitleTrack(
                id: t['id'].toString(), label: _str(t['label']) ?? '字幕 ${t['id']}', codec: _str(t['codec'])))
            .toList(),
      );

  final String gid;
  final String fileName;
  final String deliveryMode;
  final double? duration;
  final List<SubtitleTrack> subtitles;

  bool get usesHls => deliveryMode.startsWith('hls');
}
