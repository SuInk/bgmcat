package io.github.suink.bgmcat;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.File;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.nio.charset.StandardCharsets;
import java.util.ArrayDeque;
import java.util.Map;
import java.util.concurrent.CopyOnWriteArraySet;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

/**
 * Runs the bundled Go backend (jniLibs/&lt;abi&gt;/libbgmcat.so) as a child process and keeps
 * it alive with a foreground service, the same way desktop/main.cjs runs it under Electron:
 * {@code --desktop} mode, loopback only, and the backend exits when its stdin closes.
 */
public final class BackendService extends Service {
    static final String ACTION_START = "io.github.suink.bgmcat.action.START_BACKEND";
    static final String ACTION_STOP = "io.github.suink.bgmcat.action.STOP_BACKEND";

    /** A fixed port keeps the WebView origin, and so its local storage, stable across launches. */
    private static final String PREFERRED_LISTEN = "127.0.0.1:39123";
    private static final String FALLBACK_LISTEN = "127.0.0.1:0";
    private static final String READY_PREFIX = "BGMCAT_DESKTOP_READY ";
    private static final long READY_TIMEOUT_SECONDS = 30;
    private static final long STOP_TIMEOUT_SECONDS = 7;
    private static final String CHANNEL_ID = "bgmcat_backend";
    private static final int NOTIFICATION_ID = 1;

    enum Phase { STOPPED, STARTING, READY, FAILED }

    static final class State {
        final Phase phase;
        final String origin;
        final String token;
        final String error;
        /** True when the user asked to quit, so the UI should close instead of offering a retry. */
        final boolean userStopped;

        State(Phase phase, String origin, String token, String error, boolean userStopped) {
            this.phase = phase;
            this.origin = origin;
            this.token = token;
            this.error = error;
            this.userStopped = userStopped;
        }
    }

    interface Listener {
        void onBackendState(State state);
    }

    private static final CopyOnWriteArraySet<Listener> listeners = new CopyOnWriteArraySet<>();
    private static volatile State state = new State(Phase.STOPPED, "", "", "", false);

    private final Object lock = new Object();
    private Process process;
    private boolean stopping;

    static State current() {
        return state;
    }

    static void addListener(Listener listener) {
        listeners.add(listener);
    }

    static void removeListener(Listener listener) {
        listeners.remove(listener);
    }

    private static void publish(State next) {
        state = next;
        for (Listener listener : listeners) {
            listener.onBackendState(next);
        }
    }

    /** Starts the backend, or does nothing if it is already starting or running. */
    static void start(Context context) {
        Intent intent = new Intent(context, BackendService.class).setAction(ACTION_START);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            context.startForegroundService(intent);
        } else {
            context.startService(intent);
        }
    }

    static void stop(Context context) {
        if (state.phase == Phase.STOPPED) return;
        context.startService(new Intent(context, BackendService.class).setAction(ACTION_STOP));
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        String action = intent != null ? intent.getAction() : ACTION_START;
        if (ACTION_STOP.equals(action)) {
            new Thread(() -> shutdown(true), "bgmcat-backend-stop").start();
            return START_NOT_STICKY;
        }
        enterForeground();
        synchronized (lock) {
            if (process != null || state.phase == Phase.STARTING) {
                return START_NOT_STICKY;
            }
            stopping = false;
            publish(new State(Phase.STARTING, "", "", "", false));
        }
        new Thread(this::launch, "bgmcat-backend").start();
        // The activity starts the backend again whenever it needs one, so a dead
        // process should not be resurrected by the system with no UI around it.
        return START_NOT_STICKY;
    }

    @Override
    public void onDestroy() {
        // The system can destroy the service without a stop request; never leave the child behind.
        shutdown(false);
        super.onDestroy();
    }

    private void launch() {
        String error;
        try {
            error = launchOn(PREFERRED_LISTEN);
            if (error != null && error.contains("address already in use")) {
                error = launchOn(FALLBACK_LISTEN);
            }
        } catch (Exception e) {
            error = "无法启动内置服务：" + e.getMessage();
        }
        if (error != null) {
            synchronized (lock) {
                if (stopping) return;
                process = null;
            }
            publish(new State(Phase.FAILED, "", "", error, false));
            stopForegroundCompat();
            stopSelf();
        }
    }

    /** Returns null once the backend is ready, else a user-facing error. */
    private String launchOn(String listen) throws Exception {
        File binary = new File(getApplicationInfo().nativeLibraryDir, "libbgmcat.so");
        if (!binary.canExecute()) {
            return "安装包里没有当前设备架构（" + Build.SUPPORTED_ABIS[0] + "）的内置服务";
        }
        File data = new File(getFilesDir(), "server");
        if (!data.isDirectory() && !data.mkdirs()) {
            return "无法创建数据目录";
        }
        ProcessBuilder builder = new ProcessBuilder(
                binary.getAbsolutePath(), "--desktop", "--data", data.getAbsolutePath(), "--listen", listen);
        Map<String, String> env = builder.environment();
        env.put("HOME", getFilesDir().getAbsolutePath());
        env.put("TMPDIR", getCacheDir().getAbsolutePath());
        builder.directory(getFilesDir());

        Process child = builder.start();
        synchronized (lock) {
            if (stopping) {
                child.destroy();
                return null;
            }
            process = child;
        }
        // Kept only in memory: the backend prints no secrets to stderr, but nothing here
        // needs to outlive the process either (desktop/main.cjs discards stderr entirely).
        ArrayDeque<String> stderrTail = new ArrayDeque<>();
        Thread stderrReader = new Thread(() -> drain(child.getErrorStream(), stderrTail), "bgmcat-backend-stderr");
        stderrReader.start();

        final String[] ready = new String[2];
        CountDownLatch readyLatch = new CountDownLatch(1);
        new Thread(() -> {
            try (BufferedReader reader = new BufferedReader(new InputStreamReader(child.getInputStream(), StandardCharsets.UTF_8))) {
                String line;
                while ((line = reader.readLine()) != null) {
                    if (readyLatch.getCount() > 0 && line.startsWith(READY_PREFIX)) {
                        try {
                            JSONObject json = new JSONObject(line.substring(READY_PREFIX.length()));
                            ready[0] = json.optString("url");
                            ready[1] = json.optString("token");
                        } catch (Exception ignored) {
                        }
                        readyLatch.countDown();
                    }
                }
            } catch (Exception ignored) {
            } finally {
                readyLatch.countDown();
            }
        }, "bgmcat-backend-stdout").start();

        boolean signalled = readyLatch.await(READY_TIMEOUT_SECONDS, TimeUnit.SECONDS);
        String origin = ready[0];
        String token = ready[1];
        if (signalled && origin != null && !origin.isEmpty() && token != null && !token.isEmpty()) {
            publish(new State(Phase.READY, origin, token, "", false));
            new Thread(() -> watch(child), "bgmcat-backend-watch").start();
            return null;
        }
        child.destroy();
        if (!child.waitFor(STOP_TIMEOUT_SECONDS, TimeUnit.SECONDS)) {
            child.destroyForcibly();
        }
        stderrReader.join(1000);
        synchronized (lock) {
            if (process == child) process = null;
        }
        String detail;
        synchronized (stderrTail) {
            detail = stderrTail.isEmpty() ? "" : stderrTail.peekLast();
        }
        if (!signalled) {
            return "内置服务启动超过 " + READY_TIMEOUT_SECONDS + " 秒仍未就绪";
        }
        return detail.isEmpty() ? "内置服务未能启动" : "内置服务未能启动：" + detail;
    }

    private void watch(Process child) {
        try {
            child.waitFor();
        } catch (InterruptedException ignored) {
        }
        synchronized (lock) {
            if (process != child) return;
            process = null;
            if (stopping) return;
        }
        publish(new State(Phase.FAILED, "", "", "内置服务意外停止，任务和设置已保存在本机", false));
        stopForegroundCompat();
        stopSelf();
    }

    /** Closes the backend's stdin (its shutdown signal), waits briefly, then force-kills. */
    private void shutdown(boolean userRequested) {
        Process child;
        synchronized (lock) {
            stopping = true;
            child = process;
            process = null;
        }
        if (child != null) {
            try {
                child.getOutputStream().close();
            } catch (Exception ignored) {
            }
            try {
                if (!child.waitFor(STOP_TIMEOUT_SECONDS, TimeUnit.SECONDS)) {
                    child.destroyForcibly();
                }
            } catch (InterruptedException e) {
                child.destroyForcibly();
            }
        }
        // onDestroy follows every stop; it must not overwrite a failure or the user's quit.
        if (userRequested || (state.phase != Phase.FAILED && state.phase != Phase.STOPPED)) {
            publish(new State(Phase.STOPPED, "", "", "", userRequested));
        }
        if (userRequested) {
            stopForegroundCompat();
            stopSelf();
        }
    }

    private static void drain(InputStream stream, ArrayDeque<String> tail) {
        try (BufferedReader reader = new BufferedReader(new InputStreamReader(stream, StandardCharsets.UTF_8))) {
            String line;
            while ((line = reader.readLine()) != null) {
                synchronized (tail) {
                    if (tail.size() >= 20) tail.removeFirst();
                    tail.addLast(line);
                }
            }
        } catch (Exception ignored) {
        }
    }

    private void enterForeground() {
        NotificationManager manager = getSystemService(NotificationManager.class);
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O && manager != null) {
            NotificationChannel channel = new NotificationChannel(
                    CHANNEL_ID, getString(R.string.backend_channel_name), NotificationManager.IMPORTANCE_LOW);
            channel.setDescription(getString(R.string.backend_channel_desc));
            channel.setShowBadge(false);
            manager.createNotificationChannel(channel);
        }
        int immutable = Build.VERSION.SDK_INT >= Build.VERSION_CODES.M ? PendingIntent.FLAG_IMMUTABLE : 0;
        PendingIntent open = PendingIntent.getActivity(this, 0,
                new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP),
                PendingIntent.FLAG_UPDATE_CURRENT | immutable);
        PendingIntent quit = PendingIntent.getService(this, 1,
                new Intent(this, BackendService.class).setAction(ACTION_STOP),
                PendingIntent.FLAG_UPDATE_CURRENT | immutable);
        Notification.Builder builder = Build.VERSION.SDK_INT >= Build.VERSION_CODES.O
                ? new Notification.Builder(this, CHANNEL_ID)
                : new Notification.Builder(this);
        Notification notification = builder
                .setSmallIcon(R.drawable.ic_stat_bgmcat)
                .setContentTitle(getString(R.string.backend_notification_title))
                .setContentText(getString(R.string.backend_notification_text))
                .setContentIntent(open)
                .setOngoing(true)
                .setShowWhen(false)
                .addAction(new Notification.Action.Builder(null, getString(R.string.backend_notification_quit), quit).build())
                .build();
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
        } else {
            startForeground(NOTIFICATION_ID, notification);
        }
    }

    @SuppressWarnings("deprecation")
    private void stopForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(STOP_FOREGROUND_REMOVE);
        } else {
            stopForeground(true);
        }
    }
}
