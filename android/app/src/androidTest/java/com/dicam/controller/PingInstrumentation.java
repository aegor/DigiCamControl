package com.dicam.controller;

import android.app.Instrumentation;
import android.net.*;
import android.os.Bundle;

/** Explicit device integration check; never opens PTP or sends recording commands. */
public final class PingInstrumentation extends Instrumentation {
    private String host;
    @Override public void onCreate(Bundle args) {
        super.onCreate(args);
        host = args == null ? null : args.getString("camera_ip");
        start();
    }
    @Override public void onStart() {
        Bundle result = new Bundle();
        try {
            if (host == null) throw new IllegalArgumentException("Supply -e camera_ip <camera IPv4>");
            host = CameraAddress.normalize(host);
            ConnectivityManager cm = getTargetContext().getSystemService(ConnectivityManager.class);
            Network wifi = null;
            for (Network network : cm.getAllNetworks()) {
                NetworkCapabilities caps = cm.getNetworkCapabilities(network);
                if (caps != null && caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI)) { wifi = network; break; }
            }
            if (wifi == null) throw new IllegalStateException("No Wi-Fi network");
            String legacy;
            Process process = new ProcessBuilder("/system/bin/ping", "-n", "-c", "1", "-I",
                    cm.getLinkProperties(wifi).getInterfaceName(), host).redirectErrorStream(true).start();
            try {
                legacy = process.waitFor(900, java.util.concurrent.TimeUnit.MILLISECONDS)
                        ? "legacy subprocess exit=" + process.exitValue() : "legacy subprocess timeout";
            } finally { process.destroyForcibly(); }
            android.util.Log.i("DiCamPing", legacy);
            CameraPing ping = new CameraPing(host, wifi);
            for (int i = 0; i < 3; i++) {
                if (!ping.reachable()) throw new IllegalStateException(ping.failureDetail());
            }
            result.putString("stream", legacy + "; three app-process Wi-Fi ICMP checks passed, UID=" + android.os.Process.myUid() + "\n");
            finish(-1, result);
        } catch (Exception e) {
            result.putString("stream", "FAIL: " + e + "\n");
            finish(0, result);
        }
    }
}
