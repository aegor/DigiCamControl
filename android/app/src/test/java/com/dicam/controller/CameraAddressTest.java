package com.dicam.controller;
import org.junit.Test;
import static org.junit.Assert.*;
public class CameraAddressTest {
    @Test public void acceptsCameraIPv4() {
        assertEquals("192.168.1.1", CameraAddress.normalize(" 192.168.1.1 "));
        assertEquals("10.0.2.2", CameraAddress.normalize("10.0.2.2"));
    }
    @Test public void rejectsServerUrlsAndInvalidAddresses() {
        for (String input : new String[]{"http://192.168.1.10:5002", "192.168.1.1:554", "256.1.1.1", "1.2.3", "224.0.0.1", "1.2.3.04", "1..2.3", ""})
            assertThrows(input, IllegalArgumentException.class, () -> CameraAddress.normalize(input));
    }
}
