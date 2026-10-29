import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'screens/downloads.dart';
import 'screens/home.dart';
import 'screens/login.dart';
import 'screens/settings.dart';
import 'session.dart';
import 'theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  SystemChrome.setPreferredOrientations([DeviceOrientation.portraitUp]);
  final session = await AppSession.load();
  runApp(BgmcatApp(session: session));
}

/// 让子树拿到同一个 [AppSession]，并在登录状态变化时重建。
class SessionScope extends InheritedNotifier<AppSession> {
  const SessionScope({super.key, required AppSession session, required super.child}) : super(notifier: session);

  static AppSession of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<SessionScope>()!.notifier!;

  /// 只取不订阅：回调里用，避免播放进度一写整棵树跟着重建。
  static AppSession read(BuildContext context) =>
      context.getInheritedWidgetOfExactType<SessionScope>()!.notifier!;
}

class BgmcatApp extends StatelessWidget {
  const BgmcatApp({super.key, required this.session});

  final AppSession session;

  @override
  Widget build(BuildContext context) {
    return SessionScope(
      session: session,
      child: MaterialApp(
        title: '猫追番',
        debugShowCheckedModeBanner: false,
        theme: buildTheme(),
        locale: const Locale('zh', 'CN'),
        home: ListenableBuilder(
          listenable: session,
          builder: (context, _) => session.loggedIn ? const AppShell() : const LoginScreen(),
        ),
      ),
    );
  }
}

class AppShell extends StatefulWidget {
  const AppShell({super.key});

  @override
  State<AppShell> createState() => _AppShellState();
}

class _AppShellState extends State<AppShell> {
  int _index = 0;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: IndexedStack(
        index: _index,
        children: [
          const HomeScreen(),
          DownloadsScreen(active: _index == 1),
          const SettingsScreen(),
        ],
      ),
      bottomNavigationBar: NavigationBar(
        selectedIndex: _index,
        onDestinationSelected: (i) => setState(() => _index = i),
        destinations: const [
          NavigationDestination(icon: Icon(Icons.live_tv_outlined), selectedIcon: Icon(Icons.live_tv), label: '新番'),
          NavigationDestination(
              icon: Icon(Icons.download_outlined), selectedIcon: Icon(Icons.download), label: '缓存'),
          NavigationDestination(
              icon: Icon(Icons.settings_outlined), selectedIcon: Icon(Icons.settings), label: '设置'),
        ],
      ),
    );
  }
}
