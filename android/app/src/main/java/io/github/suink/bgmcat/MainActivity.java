package io.github.suink.bgmcat;

import android.annotation.SuppressLint;
import android.app.Activity;
import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.pm.ActivityInfo;
import android.content.pm.PackageInfo;
import android.graphics.Color;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.view.KeyEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.WindowManager;
import android.webkit.CookieManager;
import android.webkit.JavascriptInterface;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.window.OnBackInvokedCallback;
import android.window.OnBackInvokedDispatcher;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URLEncoder;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public class MainActivity extends Activity {
    private static final String ASSET_CONNECT_URL = "file:///android_asset/connect.html";
    private static final String PREFS_NAME = "bgmcat_client_prefs";
    private static final String KEY_PRIMARY_URL = "primary_url";
    private static final String KEY_BACKUP_URL = "backup_url";
    private static final String KEY_LABEL = "label";
    private static final String KEY_PROFILES = "profiles_json";

    private final ExecutorService executor = Executors.newCachedThreadPool();

    private FrameLayout rootLayout;
    private WebView webView;
    private FrameLayout fullscreenContainer;
    private View customVideoView;
    private WebChromeClient.CustomViewCallback customViewCallback;
    private OnBackInvokedCallback backCallback;

    private volatile String activeServerOrigin = "";
    private volatile String activeServerLabel = "";

    @SuppressLint({"SetJavaScriptEnabled", "JavascriptInterface"})
    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        rootLayout = new FrameLayout(this);
        rootLayout.setBackgroundColor(Color.parseColor("#0E1015"));

        webView = new WebView(this);
        webView.setBackgroundColor(Color.parseColor("#0E1015"));
        rootLayout.addView(webView, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
        ));

        fullscreenContainer = new FrameLayout(this);
        fullscreenContainer.setBackgroundColor(Color.BLACK);
        fullscreenContainer.setVisibility(View.GONE);
        rootLayout.addView(fullscreenContainer, new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT,
                ViewGroup.LayoutParams.MATCH_PARENT
        ));

        setContentView(rootLayout);

        CookieManager cookieManager = CookieManager.getInstance();
        cookieManager.setAcceptCookie(true);
        cookieManager.setAcceptThirdPartyCookies(webView, true);

        WebSettings settings = webView.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setDatabaseEnabled(true);
        settings.setMediaPlaybackRequiresUserGesture(false);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_COMPATIBILITY_MODE);
        settings.setLoadWithOverviewMode(true);
        settings.setUseWideViewPort(true);
        settings.setSupportZoom(false);
        settings.setBuiltInZoomControls(false);
        settings.setDisplayZoomControls(false);
        settings.setUserAgentString(settings.getUserAgentString() + " BgmcatAndroid/" + readVersionName());

        webView.addJavascriptInterface(new AndroidBridge(), "bgmcatAndroid");

        webView.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                Uri uri = request.getUrl();
                if (uri == null) return false;
                String scheme = uri.getScheme() != null ? uri.getScheme().toLowerCase() : "";
                if ("file".equals(scheme)) {
                    return false;
                }
                if (!"http".equals(scheme) && !"https".equals(scheme)) {
                    openSystemExternal(uri);
                    return true;
                }
                String origin = originOf(uri.toString());
                if (!activeServerOrigin.isEmpty() && origin.equalsIgnoreCase(activeServerOrigin)) {
                    return false;
                }
                // Allow GitHub / Google OAuth login redirects in-app when initiated on main frame,
                // while opening pure external reference links in system browser.
                String path = uri.getPath() != null ? uri.getPath() : "";
                String host = uri.getHost() != null ? uri.getHost().toLowerCase() : "";
                if (host.contains("github.com") && path.startsWith("/login/oauth")) {
                    return false;
                }
                openSystemExternal(uri);
                return true;
            }

            @Override
            public void onPageFinished(WebView view, String url) {
                super.onPageFinished(view, url);
                CookieManager.getInstance().flush();
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                super.onReceivedError(view, request, error);
                if (request != null && request.isForMainFrame()) {
                    Uri reqUri = request.getUrl();
                    if (reqUri != null && !"file".equalsIgnoreCase(reqUri.getScheme())) {
                        String msg = "服务器连接中断（" + reqUri.getHost() + "），请检查网络或切换备用地址";
                        loadConnectScreen(msg);
                    }
                }
            }
        });

        webView.setWebChromeClient(new WebChromeClient() {
            @Override
            public void onShowCustomView(View view, CustomViewCallback callback) {
                if (customVideoView != null) {
                    callback.onCustomViewHidden();
                    return;
                }
                customVideoView = view;
                customViewCallback = callback;
                fullscreenContainer.addView(view, new FrameLayout.LayoutParams(
                        ViewGroup.LayoutParams.MATCH_PARENT,
                        ViewGroup.LayoutParams.MATCH_PARENT
                ));
                fullscreenContainer.setVisibility(View.VISIBLE);
                webView.setVisibility(View.GONE);
                getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
                setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE);
                setImmersiveFullscreen(true);
            }

            @Override
            public void onHideCustomView() {
                exitVideoFullscreen();
            }
        });

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            backCallback = this::handleAppBack;
            getOnBackInvokedDispatcher().registerOnBackInvokedCallback(
                    OnBackInvokedDispatcher.PRIORITY_DEFAULT,
                    backCallback
            );
        }

        autoConnectOrShowLauncher();
    }

    private void autoConnectOrShowLauncher() {
        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
        String primary = prefs.getString(KEY_PRIMARY_URL, "");
        String backup = prefs.getString(KEY_BACKUP_URL, "");
        String label = prefs.getString(KEY_LABEL, "");
        if (primary == null || primary.trim().isEmpty()) {
            webView.loadUrl(ASSET_CONNECT_URL);
            return;
        }
        executor.execute(() -> {
            String target = selectReachableOrigin(primary, backup);
            runOnUiThread(() -> {
                if (target != null) {
                    activeServerOrigin = originOf(target);
                    activeServerLabel = (label != null && !label.trim().isEmpty()) ? label.trim() : hostLabel(target);
                    webView.loadUrl(target);
                } else {
                    loadConnectScreen("上次连接的服务器暂不可达，请确认电脑端已启动或检查穿透地址。");
                }
            });
        });
    }

    private void loadConnectScreen(String errorText) {
        if (errorText == null || errorText.isEmpty()) {
            webView.loadUrl(ASSET_CONNECT_URL);
            return;
        }
        String encoded = URLEncoder.encode(errorText, StandardCharsets.UTF_8);
        webView.loadUrl(ASSET_CONNECT_URL + "?error=" + encoded);
    }

    private void exitVideoFullscreen() {
        if (customVideoView == null) {
            return;
        }
        fullscreenContainer.removeView(customVideoView);
        fullscreenContainer.setVisibility(View.GONE);
        customVideoView = null;
        if (customViewCallback != null) {
            customViewCallback.onCustomViewHidden();
            customViewCallback = null;
        }
        webView.setVisibility(View.VISIBLE);
        getWindow().clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_UNSPECIFIED);
        setImmersiveFullscreen(false);
    }

    @SuppressWarnings("deprecation")
    private void setImmersiveFullscreen(boolean fullscreen) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            WindowInsetsController controller = getWindow().getInsetsController();
            if (controller != null) {
                if (fullscreen) {
                    controller.hide(WindowInsets.Type.statusBars() | WindowInsets.Type.navigationBars());
                    controller.setSystemBarsBehavior(WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
                } else {
                    controller.show(WindowInsets.Type.statusBars() | WindowInsets.Type.navigationBars());
                }
            }
        } else {
            View decor = getWindow().getDecorView();
            if (fullscreen) {
                decor.setSystemUiVisibility(
                        View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                                | View.SYSTEM_UI_FLAG_FULLSCREEN
                                | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                                | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                                | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                                | View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                );
            } else {
                decor.setSystemUiVisibility(View.SYSTEM_UI_FLAG_VISIBLE);
            }
        }
    }

    private void handleAppBack() {
        if (customVideoView != null) {
            exitVideoFullscreen();
            return;
        }
        String current = webView != null ? webView.getUrl() : null;
        if (current != null && !current.startsWith("file:///android_asset/")) {
            // Let Bangumi Cat Vue app close open dialogs / player / anime drawer via Escape first.
            webView.evaluateJavascript(
                    "(function(){var open=document.querySelector('[role=\"dialog\"], .art-fullscreen-web, [data-playback-open], .anime-drawer-open');" +
                            "document.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true}));" +
                            "return Boolean(open);})()",
                    result -> {
                        if (!"true".equals(result)) {
                            if (webView.canGoBack()) {
                                webView.goBack();
                            } else {
                                moveTaskToBack(true);
                            }
                        }
                    }
            );
            return;
        }
        if (webView != null && webView.canGoBack()) {
            webView.goBack();
        } else {
            finish();
        }
    }

    @Override
    @SuppressWarnings("deprecation")
    public void onBackPressed() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
            handleAppBack();
        } else {
            super.onBackPressed();
        }
    }

    @Override
    public boolean onKeyDown(int keyCode, KeyEvent event) {
        if (keyCode == KeyEvent.KEYCODE_BACK && Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) {
            handleAppBack();
            return true;
        }
        return super.onKeyDown(keyCode, event);
    }

    @Override
    protected void onPause() {
        super.onPause();
        CookieManager.getInstance().flush();
    }

    @Override
    protected void onDestroy() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && backCallback != null) {
            getOnBackInvokedDispatcher().unregisterOnBackInvokedCallback(backCallback);
        }
        executor.shutdownNow();
        if (webView != null) {
            rootLayout.removeView(webView);
            webView.destroy();
            webView = null;
        }
        super.onDestroy();
    }

    private void openSystemExternal(Uri uri) {
        try {
            Intent intent = new Intent(Intent.ACTION_VIEW, uri);
            intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK);
            startActivity(intent);
        } catch (Exception ignored) {
        }
    }

    private String readVersionName() {
        try {
            PackageInfo info = getPackageManager().getPackageInfo(getPackageName(), 0);
            return info.versionName != null ? info.versionName : "0.2.1";
        } catch (Exception e) {
            return "0.2.1";
        }
    }

    private static String normalizeServerUrl(String raw) {
        if (raw == null) return "";
        String v = raw.trim();
        while (v.endsWith("/")) {
            v = v.substring(0, v.length() - 1);
        }
        if (v.isEmpty()) return "";
        String lower = v.toLowerCase();
        if (!lower.startsWith("http://") && !lower.startsWith("https://")) {
            if (lower.startsWith("localhost") || lower.matches("^\\d{1,3}(\\.\\d{1,3}){3}.*")) {
                v = "http://" + v;
            } else {
                v = "https://" + v;
            }
        }
        return v;
    }

    private static String originOf(String rawUrl) {
        try {
            URI u = URI.create(rawUrl.trim());
            String scheme = u.getScheme();
            String auth = u.getRawAuthority();
            if (scheme == null || auth == null) return "";
            return scheme.toLowerCase() + "://" + auth.toLowerCase();
        } catch (Exception e) {
            return "";
        }
    }

    private static String hostLabel(String rawUrl) {
        try {
            URI u = URI.create(rawUrl.trim());
            return u.getRawAuthority() != null ? u.getRawAuthority() : rawUrl;
        } catch (Exception e) {
            return rawUrl;
        }
    }

    private static boolean probeBangumiCatHealth(String baseUrl, int timeoutMs) {
        if (baseUrl == null || baseUrl.isEmpty()) return false;
        HttpURLConnection conn = null;
        try {
            URL url = new URL(baseUrl + "/api/health");
            conn = (HttpURLConnection) url.openConnection();
            conn.setRequestMethod("GET");
            conn.setConnectTimeout(timeoutMs);
            conn.setReadTimeout(timeoutMs);
            conn.setInstanceFollowRedirects(true);
            conn.setRequestProperty("Accept", "application/json");
            int code = conn.getResponseCode();
            if (code < 200 || code >= 300) {
                return false;
            }
            try (BufferedReader reader = new BufferedReader(new InputStreamReader(conn.getInputStream(), StandardCharsets.UTF_8))) {
                StringBuilder sb = new StringBuilder();
                String line;
                while ((line = reader.readLine()) != null && sb.length() < 4096) {
                    sb.append(line);
                }
                String body = sb.toString();
                return body.contains("\"ok\"") || body.contains("\"status\"");
            }
        } catch (Exception e) {
            return false;
        } finally {
            if (conn != null) {
                conn.disconnect();
            }
        }
    }

    private static String selectReachableOrigin(String primaryRaw, String backupRaw) {
        String primary = normalizeServerUrl(primaryRaw);
        String backup = normalizeServerUrl(backupRaw);
        if (!primary.isEmpty() && probeBangumiCatHealth(primary, 2000)) {
            return primary;
        }
        if (!backup.isEmpty() && probeBangumiCatHealth(backup, 4500)) {
            return backup;
        }
        return null;
    }

    private boolean isOnLocalAssetPage() {
        String url = webView != null ? webView.getUrl() : null;
        return url != null && url.startsWith("file:///android_asset/");
    }

    private void saveServerProfile(String primary, String backup, String label) {
        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
        JSONArray existing;
        try {
            existing = new JSONArray(prefs.getString(KEY_PROFILES, "[]"));
        } catch (Exception e) {
            existing = new JSONArray();
        }
        JSONArray updated = new JSONArray();
        try {
            JSONObject current = new JSONObject();
            current.put("id", primary);
            current.put("primaryUrl", primary);
            current.put("backupUrl", backup);
            current.put("label", label);
            updated.put(current);
            for (int i = 0; i < existing.length() && updated.length() < 8; i++) {
                JSONObject obj = existing.optJSONObject(i);
                if (obj == null) continue;
                if (!primary.equalsIgnoreCase(obj.optString("primaryUrl"))) {
                    updated.put(obj);
                }
            }
        } catch (Exception ignored) {
        }
        prefs.edit()
                .putString(KEY_PRIMARY_URL, primary)
                .putString(KEY_BACKUP_URL, backup)
                .putString(KEY_LABEL, label)
                .putString(KEY_PROFILES, updated.toString())
                .apply();
    }

    private final class AndroidBridge {
        @JavascriptInterface
        public boolean isAndroidApp() {
            return true;
        }

        @JavascriptInterface
        public String getAppVersion() {
            return readVersionName();
        }

        @JavascriptInterface
        public String getActiveServerLabel() {
            return activeServerLabel;
        }

        @JavascriptInterface
        public void openServerSwitcher() {
            runOnUiThread(() -> {
                if (webView != null) {
                    webView.loadUrl(ASSET_CONNECT_URL + "?manage=1");
                }
            });
        }

        @JavascriptInterface
        public String getSavedConfigJson() {
            final String[] holder = new String[]{""};
            final Object lock = new Object();
            runOnUiThread(() -> {
                try {
                    if (isOnLocalAssetPage()) {
                        SharedPreferences prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
                        JSONObject out = new JSONObject();
                        out.put("primaryUrl", prefs.getString(KEY_PRIMARY_URL, ""));
                        out.put("backupUrl", prefs.getString(KEY_BACKUP_URL, ""));
                        out.put("label", prefs.getString(KEY_LABEL, ""));
                        out.put("profiles", new JSONArray(prefs.getString(KEY_PROFILES, "[]")));
                        holder[0] = out.toString();
                    }
                } catch (Exception ignored) {
                } finally {
                    synchronized (lock) {
                        lock.notifyAll();
                    }
                }
            });
            synchronized (lock) {
                try {
                    lock.wait(1000);
                } catch (InterruptedException ignored) {
                }
            }
            return holder[0];
        }

        @JavascriptInterface
        public void removeProfile(String id) {
            if (id == null) return;
            runOnUiThread(() -> {
                if (!isOnLocalAssetPage()) return;
                SharedPreferences prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE);
                try {
                    JSONArray arr = new JSONArray(prefs.getString(KEY_PROFILES, "[]"));
                    JSONArray next = new JSONArray();
                    for (int i = 0; i < arr.length(); i++) {
                        JSONObject item = arr.optJSONObject(i);
                        if (item == null) continue;
                        if (!id.equalsIgnoreCase(item.optString("id")) && !id.equalsIgnoreCase(item.optString("primaryUrl"))) {
                            next.put(item);
                        }
                    }
                    prefs.edit().putString(KEY_PROFILES, next.toString()).apply();
                } catch (Exception ignored) {
                }
            });
        }

        @JavascriptInterface
        public void probeAndConnect(String primaryRaw, String backupRaw, String labelRaw) {
            runOnUiThread(() -> {
                if (!isOnLocalAssetPage()) return;
                final String primary = normalizeServerUrl(primaryRaw);
                final String backup = normalizeServerUrl(backupRaw);
                final String label = labelRaw != null ? labelRaw.trim() : "";
                executor.execute(() -> {
                    String chosen = selectReachableOrigin(primary, backup);
                    runOnUiThread(() -> {
                        if (webView == null) return;
                        if (chosen == null) {
                            String msg = "无法连接到 " + primary + (backup.isEmpty() ? "" : " 或备用地址") + "（请确认电脑端已启动并在设置中开启局域网访问或穿透）";
                            String escaped = JSONObject.quote(msg);
                            webView.evaluateJavascript("window.__onProbeResult && window.__onProbeResult(false, " + escaped + ");", null);
                            return;
                        }
                        saveServerProfile(primary, backup, label);
                        activeServerOrigin = originOf(chosen);
                        activeServerLabel = !label.isEmpty() ? label : hostLabel(chosen);
                        webView.loadUrl(chosen);
                    });
                });
            });
        }
    }
}
