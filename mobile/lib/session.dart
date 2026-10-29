import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'api.dart';

/// 一条播放记录：看到哪、总长多少、什么时候看的。键是下载任务的 gid。
class PlaybackRecord {
  PlaybackRecord({required this.position, required this.duration, required this.updatedAt, this.title, this.imageUrl});

  factory PlaybackRecord.fromJson(Map<String, dynamic> json) => PlaybackRecord(
        position: (json['position'] as num?)?.toDouble() ?? 0,
        duration: (json['duration'] as num?)?.toDouble() ?? 0,
        updatedAt: (json['updatedAt'] as num?)?.toInt() ?? 0,
        title: json['title'] as String?,
        imageUrl: json['imageUrl'] as String?,
      );

  final double position;
  final double duration;
  final int updatedAt;
  final String? title;
  final String? imageUrl;

  double get fraction => duration <= 0 ? 0 : (position / duration).clamp(0, 1).toDouble();

  /// 和 web 端同一套口径：不到 8 秒不算看过，离结尾不到 20 秒算看完。
  bool get resumable => position >= 8 && (duration <= 0 || duration - position > 20);

  Map<String, dynamic> toJson() => {
        'position': position,
        'duration': duration,
        'updatedAt': updatedAt,
        if (title != null) 'title': title,
        if (imageUrl != null) 'imageUrl': imageUrl,
      };
}

/// 登录状态与本机存储：服务器地址、访问口令、播放进度。
class AppSession extends ChangeNotifier {
  AppSession._(this._prefs);

  static const _serverKey = 'bgmcat.server';
  static const _tokenKey = 'bgmcat.token';
  static const _historyKey = 'bgmcat.playbackHistory';

  final SharedPreferences _prefs;
  ApiClient? _api;
  Map<String, PlaybackRecord> _history = {};

  static Future<AppSession> load() async {
    final prefs = await SharedPreferences.getInstance();
    final session = AppSession._(prefs);
    final server = prefs.getString(_serverKey);
    final token = prefs.getString(_tokenKey);
    if (server != null && token != null && server.isNotEmpty && token.isNotEmpty) {
      session._api = ApiClient(baseUrl: server, token: token);
    }
    try {
      final raw = jsonDecode(prefs.getString(_historyKey) ?? '{}') as Map<String, dynamic>;
      session._history = raw.map((k, v) => MapEntry(k, PlaybackRecord.fromJson(v as Map<String, dynamic>)));
    } catch (_) {
      session._history = {};
    }
    return session;
  }

  ApiClient? get api => _api;
  bool get loggedIn => _api != null;

  /// 上次填过的地址，退出登录后登录页还能预填。
  String get lastServer => _prefs.getString(_serverKey) ?? '';

  /// 先探活再校验口令，两种失败给出不同的提示。
  Future<void> login(String server, String token) async {
    final client = ApiClient(baseUrl: normalizeServerUrl(server), token: token.trim());
    await client.health();
    await client.tokenLogin();
    await _prefs.setString(_serverKey, client.baseUrl);
    await _prefs.setString(_tokenKey, client.token);
    _api = client;
    notifyListeners();
  }

  Future<void> logout() async {
    await _prefs.remove(_tokenKey);
    _api = null;
    notifyListeners();
  }

  /// 任何请求遇到 AUTH_INVALID（比如服务器上换了口令）都回到登录页。
  void handleError(Object error) {
    if (error is ApiException && error.isAuthInvalid && _api != null) {
      logout();
    }
  }

  PlaybackRecord? record(String gid) => _history[gid];

  Map<String, PlaybackRecord> get history => Map.unmodifiable(_history);

  Future<void> saveProgress(String gid, PlaybackRecord record) async {
    _history[gid] = record;
    // 播放器 dispose 时也会存一次，那时不能同步通知，推到下一个微任务
    scheduleMicrotask(notifyListeners);
    await _prefs.setString(_historyKey, jsonEncode(_history.map((k, v) => MapEntry(k, v.toJson()))));
  }
}
