package com.dicam.controller;
import org.junit.Test;
import static org.junit.Assert.*;
public class IcmpEchoTest {
    @Test public void validChecksumAndNetworkByteOrder() {
        byte[] request = IcmpEcho.request(0x1234);
        assertEquals(8, request[0]);
        assertEquals(0x12, request[6]);
        assertEquals(0x34, request[7]);
        assertEquals(0, IcmpEcho.checksum(request));
    }
    @Test public void rejectsUnrelatedAndTruncatedReplies() {
        byte[] reply = IcmpEcho.request(42);
        assertFalse(IcmpEcho.isReply(reply, reply.length, 42));
        reply[0] = 0;
        assertTrue(IcmpEcho.isReply(reply, reply.length, 42));
        assertFalse(IcmpEcho.isReply(reply, reply.length, 43));
        assertFalse(IcmpEcho.isReply(reply, 7, 42));
        reply[1] = 1;
        assertFalse(IcmpEcho.isReply(reply, reply.length, 42));
    }
}
