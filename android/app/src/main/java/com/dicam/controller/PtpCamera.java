package com.dicam.controller;

import java.io.*;
import java.net.*;
import java.nio.*;
import java.nio.charset.StandardCharsets;
import java.security.SecureRandom;
import javax.net.SocketFactory;

/** iCatch PTP/IP wire protocol, shared with the Go implementation. One command thread. */
final class PtpCamera implements AutoCloseable {
    static final int MAX_PACKET = 16 * 1024 * 1024;
    record Packet(int kind, byte[] body) {}
    record Response(int code, byte[] data) {}
    private final SocketFactory factory;
    private Socket command, events;
    private boolean closed;
    private int transaction;
    PtpCamera(SocketFactory factory) { this.factory = factory; }
    static ByteBuffer le(int size) { return ByteBuffer.allocate(size).order(ByteOrder.LITTLE_ENDIAN); }
    static ByteBuffer wrap(byte[] bytes) { return ByteBuffer.wrap(bytes).order(ByteOrder.LITTLE_ENDIAN); }
    static void write(OutputStream out, int kind, byte[] body) throws IOException {
        out.write(le(8).putInt(body.length + 8).putInt(kind).array());
        out.write(body);
        out.flush();
    }
    static Packet read(InputStream in) throws IOException {
        DataInputStream data = new DataInputStream(in);
        byte[] header = new byte[8];
        data.readFully(header);
        ByteBuffer h = wrap(header);
        int size = h.getInt(), kind = h.getInt();
        if (size < 8 || size > MAX_PACKET) throw new IOException("Invalid PTP packet length");
        byte[] body = new byte[size - 8];
        data.readFully(body);
        return new Packet(kind, body);
    }
    private Socket socket(String host, int port, boolean event) throws IOException {
        Socket s = factory.createSocket();
        synchronized (this) {
            if (closed) { s.close(); throw new IOException("Closed"); }
            if (event) events = s; else command = s;
        }
        s.connect(new InetSocketAddress(host, port), 3000);
        s.setSoTimeout(3000);
        s.setTcpNoDelay(true);
        return s;
    }
    void connect(String host, int port) throws IOException {
        try {
            Socket cmd = socket(host, port, false);
            byte[] guid = new byte[16];
            new SecureRandom().nextBytes(guid);
            byte[] name = "localhost\0".getBytes(StandardCharsets.UTF_16LE);
            write(cmd.getOutputStream(), 1, le(20 + name.length).put(guid).put(name).putInt(0x10000).array());
            Packet ack = read(cmd.getInputStream());
            if (ack.kind != 2 || ack.body.length < 4) throw new IOException("PTP handshake rejected");
            Socket ev = socket(host, port, true);
            write(ev.getOutputStream(), 3, java.util.Arrays.copyOf(ack.body, 4));
            if (read(ev.getInputStream()).kind != 4) throw new IOException("PTP event handshake rejected");
            Response result = raw(0x1002, new int[]{1}, null);
            if (result.code == 0x201e || result.code == 0x2023) {
                raw(0x1003, new int[0], null);
                result = raw(0x1002, new int[]{1}, null);
            }
            check(result);
            ev.setSoTimeout(0);
            Thread drain = new Thread(() -> {
                try { while (!ev.isClosed()) read(ev.getInputStream()); }
                catch (IOException ignored) { /* Same as Go: event loss alone does not reset commands. */ }
            }, "camera-events");
            drain.setDaemon(true);
            drain.start();
        } catch (IOException e) { close(); throw e; }
    }
    Response raw(int op, int[] params, byte[] data) throws IOException {
        Socket cmd;
        synchronized (this) {
            if (closed || command == null) throw new IOException("Camera disconnected");
            cmd = command;
        }
        int tx = op == 0x1002 ? (transaction = 0) : ++transaction;
        ByteBuffer request = le(10 + params.length * 4).putInt(data == null ? 1 : 2)
                .putShort((short) op).putInt(tx);
        for (int param : params) request.putInt(param);
        write(cmd.getOutputStream(), 6, request.array());
        if (data != null) {
            write(cmd.getOutputStream(), 9, le(12).putInt(tx).putLong(data.length).array());
            write(cmd.getOutputStream(), 12, le(4 + data.length).putInt(tx).put(data).array());
        }
        ByteArrayOutputStream received = new ByteArrayOutputStream();
        long deadline = System.nanoTime() + 3_000_000_000L;
        while (true) {
            long remaining = deadline - System.nanoTime();
            if (remaining <= 0) throw new SocketTimeoutException("PTP transaction timeout");
            cmd.setSoTimeout((int) Math.max(1, remaining / 1_000_000));
            Packet packet = read(cmd.getInputStream());
            ByteBuffer b = wrap(packet.body);
            switch (packet.kind) {
                case 9:
                    if (b.remaining() < 12 || b.getInt() != tx) throw new IOException("Invalid StartData");
                    long size = b.getLong();
                    if (size < 0 || size > MAX_PACKET) throw new IOException("PTP data too large");
                    break;
                case 10: case 12:
                    if (b.remaining() < 4 || b.getInt() != tx || received.size() + b.remaining() > MAX_PACKET)
                        throw new IOException("Invalid PTP data");
                    received.write(packet.body, 4, packet.body.length - 4);
                    break;
                case 7:
                    if (b.remaining() < 6) throw new IOException("Short PTP response");
                    int code = Short.toUnsignedInt(b.getShort());
                    if (b.getInt() != tx) throw new IOException("Wrong PTP transaction");
                    return new Response(code, received.toByteArray());
                case 8: break;
                default: throw new IOException("Unexpected PTP packet " + packet.kind);
            }
        }
    }
    private static byte[] check(Response response) throws IOException {
        if (response.code != 0x2001) throw new IOException(String.format("PTP error 0x%04x", response.code));
        return response.data;
    }
    int mode() throws IOException {
        byte[] data = check(raw(0x1015, new int[]{0xd604}, null));
        if (data.length < 2) throw new IOException("Short camera mode");
        return Short.toUnsignedInt(wrap(data).getShort());
    }
    void record(boolean on) throws IOException {
        check(raw(0x1016, new int[]{0xd604}, le(2).putShort((short)(on ? 17 : 1)).array()));
    }
    @Override public synchronized void close() {
        closed = true;
        for (Socket socket : new Socket[]{command, events}) {
            if (socket != null) try { socket.close(); } catch (IOException ignored) {}
        }
    }
}
