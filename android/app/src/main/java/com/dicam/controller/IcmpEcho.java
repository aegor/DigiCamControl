package com.dicam.controller;

/** IPv4 ICMP Echo header. The datagram socket assigns the identifier itself. */
final class IcmpEcho {
    static byte[] request(int sequence) {
        byte[] packet = new byte[16];
        packet[0] = 8;
        packet[6] = (byte)(sequence >>> 8);
        packet[7] = (byte)sequence;
        int checksum = checksum(packet);
        packet[2] = (byte)(checksum >>> 8);
        packet[3] = (byte)checksum;
        return packet;
    }
    static int checksum(byte[] packet) {
        int sum = 0;
        for (int i = 0; i < packet.length; i += 2) {
            sum += (packet[i] & 255) << 8;
            if (i + 1 < packet.length) sum += packet[i + 1] & 255;
        }
        while ((sum >>> 16) != 0) sum = (sum & 65535) + (sum >>> 16);
        return (~sum) & 65535;
    }
    static boolean isReply(byte[] packet, int length, int sequence) {
        return length >= 8 && packet[0] == 0 && packet[1] == 0
                && (packet[6] & 255) == ((sequence >>> 8) & 255)
                && (packet[7] & 255) == (sequence & 255);
    }
}
