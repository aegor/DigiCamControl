package main

/*
#cgo LDFLAGS: -liphlpapi
#include <winsock2.h>
#include <windows.h>
#include <iphlpapi.h>
#include <icmpapi.h>

static DWORD dc_ping(DWORD source, DWORD destination, DWORD timeout, DWORD *status) {
    HANDLE handle = IcmpCreateFile();
    if (handle == INVALID_HANDLE_VALUE) return GetLastError();
    // Extra room for the ICMP error and IO_STATUS_BLOCK required by the API.
    struct { ICMP_ECHO_REPLY header; char extra[64]; } reply = {0};
    char payload[] = "dicam";
    DWORD count = IcmpSendEcho2Ex(handle, NULL, NULL, NULL, source, destination,
        payload, sizeof(payload), NULL, &reply, sizeof(reply), timeout);
    DWORD error = count ? 0 : GetLastError();
    if (count) *status = reply.header.Status;
    IcmpCloseHandle(handle);
    return error;
}
*/
import "C"

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"time"
)

func pingCamera(parent context.Context, host, bind string) (bool, error) {
	if parent.Err() != nil {
		return false, nil
	}
	destination := net.ParseIP(host).To4()
	if destination == nil {
		return false, fmt.Errorf("Windows ICMP requires an IPv4 camera address: %q", host)
	}
	var source uint32
	if bind != "" {
		ip := net.ParseIP(bind).To4()
		if ip == nil {
			return false, fmt.Errorf("Windows ICMP requires an IPv4 bind address: %q", bind)
		}
		source = binary.LittleEndian.Uint32(ip)
	}
	timeout := 900 * time.Millisecond
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return false, nil
	}
	// Synchronous API bounds shutdown latency to 900 ms; no detached request
	// can outlive its handle or reply buffer when the context is cancelled.
	millis := (timeout + time.Millisecond - 1) / time.Millisecond
	var status C.DWORD
	code := C.dc_ping(C.DWORD(source), C.DWORD(binary.LittleEndian.Uint32(destination)), C.DWORD(millis), &status)
	if parent.Err() != nil {
		return false, nil
	}
	if code != 0 {
		// IP_STATUS errors indicate no successful echo, not tool failures.
		if code >= 11001 && code <= 11050 {
			return false, nil
		}
		return false, fmt.Errorf("ICMP: %w", syscall.Errno(code))
	}
	return status == 0, nil
}
