package com.dicam.controller;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.SharedPreferences;
import android.net.*;
import android.os.*;
import android.view.*;
import android.widget.*;
import org.videolan.libvlc.LibVLC;
import org.videolan.libvlc.Media;
import org.videolan.libvlc.MediaPlayer;
import org.videolan.libvlc.util.VLCVideoLayout;
import java.util.ArrayList;
import java.util.Arrays;
import javax.net.SocketFactory;

/** All camera traffic originates on the phone: PTP/IP control and RTSP video. */
public final class MainActivity extends Activity {
    private final Handler handler = new Handler(Looper.getMainLooper());
    private CameraController controller;
    private LibVLC vlc;
    private MediaPlayer player;
    private Media media;
    private VLCVideoLayout playerView;
    private TextView address, status, recording;
    private Button record, stop;
    private SharedPreferences preferences;
    private ConnectivityManager connectivity;
    private Network wifi;
    private boolean active, connected, videoReady;
    private int generation;
    private long lastPosition, lastProgress;
    private String host;
    private String connectionDetail = "Подключение ещё не выполнено";
    private AlertDialog cameraDialog;
    private final Runnable retryVideo = this::startVideo;
    private final Runnable watchdog = this::checkVideo;
    private final ConnectivityManager.NetworkCallback networkCallback = new ConnectivityManager.NetworkCallback() {
        @Override public void onAvailable(Network network) {
            handler.post(() -> {
                if (!active || network.equals(wifi)) return;
                wifi = network;
                connectCamera();
            });
        }
        @Override public void onLost(Network network) {
            handler.post(() -> {
                if (!network.equals(wifi)) return;
                wifi = null;
                if (active) connectCamera();
            });
        }
    };
    @Override public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
        setContentView(R.layout.activity_main);
        preferences = getSharedPreferences("dicam", MODE_PRIVATE);
        host = preferences.getString("camera_ip", CameraAddress.DEFAULT);
        try { host = CameraAddress.normalize(host); }
        catch (IllegalArgumentException e) { host = CameraAddress.DEFAULT; }
        playerView = findViewById(R.id.player);
        vlc = new LibVLC(this, new ArrayList<>(Arrays.asList("--rtsp-tcp", "--network-caching=250", "--no-audio")));
        address = findViewById(R.id.camera_address);
        status = findViewById(R.id.status);
        status.setOnClickListener(v -> showDiagnostics());
        recording = findViewById(R.id.recording);
        record = findViewById(R.id.record_button);
        stop = findViewById(R.id.stop_button);
        findViewById(R.id.camera_button).setOnClickListener(v -> showCameraDialog());
        record.setOnClickListener(v -> { if (controller != null) controller.record(true); });
        stop.setOnClickListener(v -> { if (controller != null) controller.record(false); });
        connectivity = getSystemService(ConnectivityManager.class);
        configureInsets();
    }
    private SocketFactory sockets() {
        // Explicit Wi-Fi sockets also work when Android keeps mobile data as its default network.
        return wifi == null ? SocketFactory.getDefault() : wifi.getSocketFactory();
    }
    private void connectCamera() {
        if (!active) return;
        generation++;
        int attempt = generation;
        if (controller != null) controller.close();
        releaseVideo();
        connectivity.bindProcessToNetwork(wifi);
        connected = false;
        updateControls(0);
        status.setText(R.string.connecting);
        address.setText("DiCam · " + host);
        connectionDetail = "Ожидание ICMP и PTP";
        controller = new CameraController(host, sockets(), new CameraPing(host, wifi), (online, mode, detail) -> handler.post(() -> {
            if (!active || attempt != generation) return;
            connectionDetail = online ? "ICMP: OK; PTP: подключено" : detail;
            boolean wasConnected = connected;
            connected = online;
            updateControls(mode);
            if (!online) {
                android.util.Log.w("DiCam", "PTP disconnected: " + detail);
                releaseVideo();
                status.setText(R.string.lost_connection);
            } else {
                if (!wasConnected) startVideo();
                status.setText(videoReady ? R.string.connected : R.string.video_reconnecting);
            }
        }));
    }
    private void showDiagnostics() {
        LinkProperties link = wifi == null ? null : connectivity.getLinkProperties(wifi);
        String permission = Build.VERSION.SDK_INT >= 37
                ? (checkSelfPermission("android.permission.ACCESS_LOCAL_NETWORK") == android.content.pm.PackageManager.PERMISSION_GRANTED
                    ? "разрешена" : "запрещена — проверьте разрешения приложения в настройках Android")
                : "INTERNET (target SDK 36)";
        String info = "Версия: 0.2.1\nЛокальная сеть: " + permission + "\nAndroid: " + Build.VERSION.RELEASE + " (API " + Build.VERSION.SDK_INT + ")"
                + "\nКамера: " + host + "\nWi-Fi: "
                + (link == null ? "не найден" : link.getInterfaceName() + " " + link.getLinkAddresses())
                + "\n" + connectionDetail;
        TextView message = new TextView(this);
        message.setText(info);
        message.setTextIsSelectable(true);
        int padding = (int)(20 * getResources().getDisplayMetrics().density);
        message.setPadding(padding, padding, padding, padding);
        new AlertDialog.Builder(this).setTitle(R.string.diagnostics).setView(message)
                .setPositiveButton(android.R.string.ok, null).show();
    }
    private void updateControls(int mode) {
        record.setEnabled(connected && mode != 17);
        stop.setEnabled(connected);
        recording.setVisibility(connected && mode == 17 ? View.VISIBLE : View.INVISIBLE);
    }
    private void startVideo() {
        if (!active || !connected) return;
        releaseVideo();
        status.setText(R.string.video_reconnecting);
        MediaPlayer next = new MediaPlayer(vlc);
        player = next;
        next.attachViews(playerView, null, false, false);
        next.setEventListener(event -> {
            if (player != next) return;
            if (event.type == MediaPlayer.Event.EncounteredError || event.type == MediaPlayer.Event.EndReached)
                handler.post(() -> { if (player == next) scheduleVideoRetry(); });
        });
        String url = "rtsp://" + host + ":554/MJPG?W=720&H=400&Q=50&BR=5000000";
        media = new Media(vlc, Uri.parse(url));
        media.setHWDecoderEnabled(true, false);
        media.addOption(":rtsp-tcp");
        media.addOption(":network-caching=250");
        media.addOption(":no-audio");
        next.setMedia(media);
        next.play();
        lastPosition = -1;
        lastProgress = SystemClock.elapsedRealtime();
        handler.postDelayed(watchdog, 1000);
    }
    private void checkVideo() {
        if (!active || !connected || player == null) return;
        org.videolan.libvlc.interfaces.IMedia.Stats stats = media.getStats();
        long position = stats == null ? 0 : stats.displayedPictures;
        long now = SystemClock.elapsedRealtime();
        if (position > 0 && !videoReady) {
            videoReady = true;
            status.setText(R.string.connected);
        }
        if (position != lastPosition) { lastProgress = now; lastPosition = position; }
        if (now - lastProgress > 8000) { scheduleVideoRetry(); return; }
        handler.postDelayed(watchdog, 1000);
    }
    private void scheduleVideoRetry() {
        releaseVideo();
        if (active && connected) {
            status.setText(R.string.video_reconnecting);
            handler.postDelayed(retryVideo, 1000);
        }
    }
    private void releaseVideo() {
        handler.removeCallbacks(retryVideo);
        handler.removeCallbacks(watchdog);
        videoReady = false;
        if (player != null) {
            MediaPlayer old = player;
            player = null;
            old.setEventListener(null);
            old.stop();
            old.detachViews();
            old.release();
            media.release();
            media = null;
        }
    }
    private void showCameraDialog() {
        if (cameraDialog != null && cameraDialog.isShowing()) return;
        LinearLayout form = new LinearLayout(this);
        form.setOrientation(LinearLayout.VERTICAL);
        int padding = (int)(20 * getResources().getDisplayMetrics().density);
        form.setPadding(padding, padding / 2, padding, 0);
        EditText input = new EditText(this);
        input.setSingleLine(true);
        input.setInputType(android.text.InputType.TYPE_CLASS_PHONE);
        input.setText(host);
        input.setSelectAllOnFocus(true);
        form.addView(input);
        TextView help = new TextView(this);
        help.setText(R.string.camera_help);
        form.addView(help);
        cameraDialog = new AlertDialog.Builder(this).setTitle(R.string.camera_title).setView(form)
                .setNegativeButton(R.string.cancel, null).setNeutralButton(R.string.diagnostics, (d, which) -> showDiagnostics())
                .setPositiveButton(R.string.connect, null).create();
        cameraDialog.setOnShowListener(d -> cameraDialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(v -> {
            try {
                host = CameraAddress.normalize(input.getText().toString());
                preferences.edit().putString("camera_ip", host).apply();
                cameraDialog.dismiss();
                connectCamera();
            } catch (IllegalArgumentException e) { input.setError(getString(R.string.invalid_address)); }
        }));
        cameraDialog.show();
    }
    @SuppressWarnings("deprecation")
    private void configureInsets() {
        View root = findViewById(R.id.root);
        if (Build.VERSION.SDK_INT >= 30) {
            getWindow().setDecorFitsSystemWindows(false);
            root.setOnApplyWindowInsetsListener((view, insets) -> {
                android.graphics.Insets safe = insets.getInsets(WindowInsets.Type.systemBars()
                        | WindowInsets.Type.displayCutout() | WindowInsets.Type.ime());
                view.setPadding(safe.left, safe.top, safe.right, safe.bottom);
                return insets;
            });
        } else {
            root.setOnApplyWindowInsetsListener((view, insets) -> {
                view.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(),
                        insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
                return insets;
            });
        }
    }

    @SuppressWarnings("deprecation")
    @Override public void onWindowFocusChanged(boolean hasFocus) {
        super.onWindowFocusChanged(hasFocus);
        if (!hasFocus) return;
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController bars = getWindow().getInsetsController();
            if (bars != null) {
                bars.hide(WindowInsets.Type.systemBars());
                bars.setSystemBarsBehavior(WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
            }
        } else {
            getWindow().getDecorView().setSystemUiVisibility(View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                    | View.SYSTEM_UI_FLAG_FULLSCREEN | View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                    | View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN | View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                    | View.SYSTEM_UI_FLAG_LAYOUT_STABLE);
        }
    }
    @Override protected void onStart() {
        super.onStart();
        active = true;
        wifi = null;
        for (Network network : connectivity.getAllNetworks()) {
            NetworkCapabilities caps = connectivity.getNetworkCapabilities(network);
            if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) { wifi = network; break; }
        }
        connectivity.registerNetworkCallback(new NetworkRequest.Builder()
                .addTransportType(NetworkCapabilities.TRANSPORT_WIFI)
                .removeCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET).build(), networkCallback);
        connectCamera();
    }
    @Override protected void onStop() {
        active = false;
        generation++;
        connectivity.unregisterNetworkCallback(networkCallback);
        if (controller != null) { controller.close(); controller = null; }
        releaseVideo();
        handler.removeCallbacksAndMessages(null);
        connectivity.bindProcessToNetwork(null);
        super.onStop();
    }
    @Override protected void onDestroy() {
        if (cameraDialog != null) cameraDialog.dismiss();
        vlc.release();
        super.onDestroy();
    }
}
