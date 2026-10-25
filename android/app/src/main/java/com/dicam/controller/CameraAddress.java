package com.dicam.controller;

final class CameraAddress {
    static final String DEFAULT = "192.168.1.1";
    static String normalize(String input) {
        String value = input.trim();
        String[] parts = value.split("\\.", -1);
        if (parts.length != 4) throw new IllegalArgumentException("IPv4 required");
        for (String part : parts) {
            if (!part.matches("0|[1-9][0-9]{0,2}") || Integer.parseInt(part) > 255)
                throw new IllegalArgumentException("Invalid IPv4");
        }
        int first = Integer.parseInt(parts[0]);
        if (first == 0 || first >= 224 || value.equals("255.255.255.255"))
            throw new IllegalArgumentException("Unicast address required");
        return value;
    }
}
