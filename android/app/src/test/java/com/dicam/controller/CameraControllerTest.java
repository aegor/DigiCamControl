package com.dicam.controller;

import org.junit.Test;
import static org.junit.Assert.*;
import java.io.*;
import java.net.*;
import java.nio.ByteBuffer;
import java.util.concurrent.*;
import javax.net.SocketFactory;
import java.util.concurrent.atomic.AtomicBoolean;

public class CameraControllerTest {
    @Test public void pingLossClosesSessionAndReconnectReadsActualMode() throws Exception {
        try (ServerSocket server = new ServerSocket(0, 2, InetAddress.getLoopbackAddress())) {
            server.setSoTimeout(6000);
            ExecutorService device = Executors.newSingleThreadExecutor();
            BlockingQueue<Integer> states = new LinkedBlockingQueue<>();
            AtomicBoolean reachable = new AtomicBoolean(true);
            Future<?> fake = device.submit(() -> {
                for (int session = 0; session < 2; session++) {
                    try (Socket command = server.accept()) {
                        command.setSoTimeout(4000);
                        assertEquals(1, PtpCamera.read(command.getInputStream()).kind());
                        PtpCamera.write(command.getOutputStream(), 2, PtpCamera.le(4).putInt(42).array());
                        try (Socket event = server.accept()) {
                            PtpCamera.read(event.getInputStream());
                            PtpCamera.write(event.getOutputStream(), 4, new byte[0]);
                            for (int op : new int[]{0x1002, 0x1015}) {
                                ByteBuffer b = PtpCamera.wrap(PtpCamera.read(command.getInputStream()).body());
                                b.getInt(); assertEquals(op, Short.toUnsignedInt(b.getShort()));
                                int tx = b.getInt();
                                if (op == 0x1015) PtpCamera.write(command.getOutputStream(), 12,
                                        PtpCamera.le(6).putInt(tx).putShort((short)(session == 0 ? 1 : 17)).array());
                                PtpCamera.write(command.getOutputStream(), 7,
                                        PtpCamera.le(6).putShort((short)0x2001).putInt(tx).array());
                            }
                            // No periodic PTP requests: a lost ping must close the old socket.
                            assertEquals(-1, command.getInputStream().read());
                        }
                    } catch (IOException e) { throw new UncheckedIOException(e); }
                }
            });
            try (CameraController controller = new CameraController("127.0.0.1", server.getLocalPort(),
                    SocketFactory.getDefault(), reachable::get,
                    (online, mode, message) -> states.add(online ? mode : -1))) {
                assertEquals(Integer.valueOf(1), states.poll(4, TimeUnit.SECONDS));
                reachable.set(false);
                assertEquals(Integer.valueOf(-1), states.poll(4, TimeUnit.SECONDS));
                reachable.set(true);
                assertEquals(Integer.valueOf(17), states.poll(4, TimeUnit.SECONDS));
            } finally { device.shutdown(); }
            fake.get(5, TimeUnit.SECONDS);
        }
    }
}
