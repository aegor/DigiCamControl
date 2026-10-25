package com.dicam.controller;

import org.junit.Test;
import static org.junit.Assert.*;
import java.io.*;
import java.net.*;
import java.nio.ByteBuffer;
import java.util.concurrent.*;
import javax.net.SocketFactory;

public class PtpCameraTest {
    @Test public void framingRejectsInvalidAndTruncatedPackets() throws Exception {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        PtpCamera.write(out, 6, new byte[]{1, 2});
        assertArrayEquals(new byte[]{10,0,0,0,6,0,0,0,1,2}, out.toByteArray());
        assertEquals(6, PtpCamera.read(new ByteArrayInputStream(out.toByteArray())).kind());
        for (int size : new int[]{0, 7, -1, PtpCamera.MAX_PACKET + 1}) {
            byte[] header = PtpCamera.le(8).putInt(size).putInt(6).array();
            assertThrows(IOException.class, () -> PtpCamera.read(new ByteArrayInputStream(header)));
        }
        assertThrows(EOFException.class, () -> PtpCamera.read(new ByteArrayInputStream(new byte[]{1})));
    }
    private static void response(Socket command, int tx, int code) throws IOException {
        PtpCamera.write(command.getOutputStream(), 7, PtpCamera.le(6).putShort((short)code).putInt(tx).array());
    }
    @Test public void realSocketHandshakeRecordStopAndMode() throws Exception {
        try (ServerSocket listener = new ServerSocket(0, 1, InetAddress.getLoopbackAddress())) {
            ExecutorService executor = Executors.newSingleThreadExecutor();
            Future<?> fake = executor.submit(() -> {
                try (Socket cmd = listener.accept()) {
                    cmd.setSoTimeout(4000);
                    PtpCamera.Packet init = PtpCamera.read(cmd.getInputStream());
                    assertEquals(1, init.kind());
                    assertEquals("localhost\0", new String(init.body(), 16, init.body().length - 20, java.nio.charset.StandardCharsets.UTF_16LE));
                    PtpCamera.write(cmd.getOutputStream(), 2, PtpCamera.le(4).putInt(123).array());
                    try (Socket ev = listener.accept()) {
                        ev.setSoTimeout(4000);
                        PtpCamera.Packet event = PtpCamera.read(ev.getInputStream());
                        assertEquals(3, event.kind());
                        assertEquals(123, PtpCamera.wrap(event.body()).getInt());
                        PtpCamera.write(ev.getOutputStream(), 4, new byte[0]);
                        // Exercise existing-session recovery before normal operations.
                        int mode = 1;
                        int[] ops = {0x1002, 0x1003, 0x1002, 0x1016, 0x1015, 0x1016, 0x1015};
                        for (int i = 0; i < ops.length; i++) {
                            PtpCamera.Packet packet = PtpCamera.read(cmd.getInputStream());
                            assertEquals(6, packet.kind());
                            ByteBuffer b = PtpCamera.wrap(packet.body());
                            int phase = b.getInt(), op = Short.toUnsignedInt(b.getShort()), tx = b.getInt();
                            assertEquals(ops[i], op);
                            if (op == 0x1016) {
                                assertEquals(2, phase);
                                assertEquals(0xd604, b.getInt());
                                PtpCamera.Packet start = PtpCamera.read(cmd.getInputStream());
                                assertEquals(9, start.kind());
                                assertEquals(2, PtpCamera.wrap(start.body()).getLong(4));
                                PtpCamera.Packet data = PtpCamera.read(cmd.getInputStream());
                                assertEquals(12, data.kind());
                                assertEquals(tx, PtpCamera.wrap(data.body()).getInt());
                                mode = Short.toUnsignedInt(PtpCamera.wrap(data.body()).getShort(4));
                                assertEquals(i == 3 ? 17 : 1, mode);
                            }
                            if (op == 0x1015) {
                                assertEquals(0xd604, b.getInt());
                                PtpCamera.write(cmd.getOutputStream(), 12, PtpCamera.le(6).putInt(tx).putShort((short)mode).array());
                            }
                            response(cmd, tx, i == 0 ? 0x201e : 0x2001);
                        }
                        cmd.getInputStream().read(); // Keep the event channel alive until client closes.
                    }
                } catch (IOException e) { throw new UncheckedIOException(e); }
            });
            try (PtpCamera camera = new PtpCamera(SocketFactory.getDefault())) {
                camera.connect("127.0.0.1", listener.getLocalPort());
                camera.record(true);
                assertEquals(17, camera.mode());
                camera.record(false);
                assertEquals(1, camera.mode());
            } finally { executor.shutdown(); }
            fake.get(5, TimeUnit.SECONDS);
        }
    }
    @Test public void closePreventsNewConnections() {
        PtpCamera camera = new PtpCamera(SocketFactory.getDefault());
        camera.close();
        assertThrows(IOException.class, () -> camera.connect("127.0.0.1", 15740));
    }
}
