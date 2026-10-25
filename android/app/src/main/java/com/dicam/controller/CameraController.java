package com.dicam.controller;

import java.io.IOException;
import java.util.concurrent.*;
import javax.net.SocketFactory;

/** Go worker lifecycle: independent ping monitor, serialized PTP, independent video retries. */
final class CameraController implements AutoCloseable {
    interface Listener { void status(boolean connected, int mode, String detail); }
    interface Probe {
        boolean reachable();
        default String failureDetail() { return "lost connection to camera"; }
    }
    private final ScheduledExecutorService worker = Executors.newSingleThreadScheduledExecutor();
    private final ScheduledExecutorService monitor = Executors.newSingleThreadScheduledExecutor();
    private final SocketFactory sockets;
    private final String host;
    private final int port;
    private final Listener listener;
    private final Probe probe;
    private PtpCamera camera;
    private boolean closed, reachable;
    private int mode;
    private long retryAt;
    CameraController(String host, SocketFactory sockets, Probe probe, Listener listener) {
        this(host, 15740, sockets, probe, listener);
    }
    CameraController(String host, int port, SocketFactory sockets, Probe probe, Listener listener) {
        this.host = host; this.port = port; this.sockets = sockets;
        this.probe = probe; this.listener = listener;
        monitor.scheduleAtFixedRate(this::checkConnection, 0, 1, TimeUnit.SECONDS);
        worker.scheduleWithFixedDelay(this::connectIfNeeded, 0, 100, TimeUnit.MILLISECONDS);
    }
    private void checkConnection() {
        boolean available = probe.reachable();
        synchronized (this) {
            if (closed) return;
            reachable = available;
            if (!available) disconnect(probe.failureDetail());
        }
    }
    private void connectIfNeeded() {
        PtpCamera next;
        synchronized (this) {
            if (closed || !reachable || camera != null || System.nanoTime() < retryAt) return;
            camera = next = new PtpCamera(sockets);
        }
        try {
            next.connect(host, port);
            int actualMode = next.mode(); // Same as Go: read once when opening the session.
            synchronized (this) {
                if (closed || !reachable || camera != next) { next.close(); return; }
                mode = actualMode;
                listener.status(true, mode, "");
            }
        } catch (IOException error) {
            synchronized (this) { if (camera == next) disconnect("PTP: " + error.getMessage()); }
        }
    }
    synchronized void record(boolean on) {
        if (closed) return;
        worker.execute(() -> {
            PtpCamera current;
            synchronized (this) {
                if (closed || !reachable || camera == null) return;
                current = camera;
            }
            try {
                current.record(on);
                synchronized (this) {
                    if (closed || current != camera) return;
                    mode = on ? 17 : 1;
                    listener.status(true, mode, "");
                }
            } catch (IOException error) {
                synchronized (this) { if (camera == current) disconnect("PTP: " + error.getMessage()); }
            }
        });
    }
    private void disconnect(String detail) {
        if (camera != null) { camera.close(); camera = null; }
        mode = 0;
        retryAt = System.nanoTime() + 1_000_000_000L;
        if (!closed) listener.status(false, 0, detail);
    }
    @Override public synchronized void close() {
        closed = true;
        if (camera != null) { camera.close(); camera = null; }
        monitor.shutdownNow();
        worker.shutdownNow();
    }
}
