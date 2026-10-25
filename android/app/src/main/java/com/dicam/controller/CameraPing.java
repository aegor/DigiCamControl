package com.dicam.controller;

import android.net.Network;
import android.os.SystemClock;
import android.system.ErrnoException;
import android.system.Os;
import android.system.OsConstants;
import android.system.StructPollfd;
import java.io.FileDescriptor;
import java.io.IOException;
import java.net.InetAddress;

/** ICMP stays inside the app process and uses the same Wi-Fi network as PTP/RTSP. */
final class CameraPing implements CameraController.Probe {
    private final String host;
    private final Network network;
    private volatile String detail = "ICMP: проверка ещё не выполнена";
    CameraPing(String host, Network network) { this.host = host; this.network = network; }
    @Override public String failureDetail() { return detail; }
    @Override public boolean reachable() {
        FileDescriptor socket = null;
        try {
            // Linux ping sockets (SOCK_DGRAM), not privileged raw sockets.
            int socketType = OsConstants.SOCK_DGRAM;
            if (android.os.Build.VERSION.SDK_INT >= 29) socketType |= OsConstants.SOCK_NONBLOCK;
            socket = Os.socket(OsConstants.AF_INET, socketType, OsConstants.IPPROTO_ICMP);
            if (network != null) network.bindSocket(socket);
            InetAddress destination = InetAddress.getByName(host);
            Os.connect(socket, destination, 0);
            // Match desktop ping -c 1: each fresh socket starts at sequence zero.
            // The kernel assigns a distinct ICMP identifier to correlate the reply.
            int seq = 0;
            byte[] request = IcmpEcho.request(seq);
            long deadline = SystemClock.elapsedRealtime() + 900;
            Os.sendto(socket, request, 0, request.length, 0, destination, 0);
            byte[] reply = new byte[2048];
            StructPollfd ready = new StructPollfd();
            ready.fd = socket;
            ready.events = (short)OsConstants.POLLIN;
            while (!Thread.currentThread().isInterrupted()) {
                long remaining = deadline - SystemClock.elapsedRealtime();
                if (remaining <= 0) break;
                if (Os.poll(new StructPollfd[]{ready}, (int)remaining) == 0) break;
                int count = Os.recvfrom(socket, reply, 0, reply.length, 0, null);
                if (IcmpEcho.isReply(reply, count, seq)) { detail = "ICMP: OK"; return true; }
            }
            detail = "ICMP: нет ответа от " + host + " за 900 мс";
        } catch (ErrnoException e) {
            if (e.errno == OsConstants.EAGAIN)
                detail = "ICMP: нет ответа от " + host + " за 900 мс";
            else detail = "ICMP: " + e.getMessage();
        } catch (IOException | SecurityException e) {
            detail = "ICMP: " + e.getClass().getSimpleName() + ": " + e.getMessage();
        } finally {
            if (socket != null) try { Os.close(socket); } catch (ErrnoException ignored) {}
        }
        return false;
    }
}
