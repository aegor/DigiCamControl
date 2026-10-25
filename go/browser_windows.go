package main

/*
#cgo LDFLAGS: -lshell32 -lole32
#include <windows.h>
#include <shellapi.h>
#include <objbase.h>
#include <stdlib.h>

static int dc_open_browser(const char *url) {
    WCHAR wide[2048];
    if (!MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, url, -1, wide, 2048)) return -1;
    // Initialize COM on this C call's thread for shell extension handlers.
    HRESULT hr = CoInitializeEx(NULL, COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) return -2;
    INT_PTR result = (INT_PTR)ShellExecuteW(NULL, L"open", wide, NULL, NULL, SW_SHOWNORMAL);
    if (SUCCEEDED(hr)) CoUninitialize();
    return result > 32 ? 0 : (int)result + 1;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

func openBrowser(address string) error {
	value := C.CString(address)
	defer C.free(unsafe.Pointer(value))
	if code := C.dc_open_browser(value); code != 0 {
		if code < 0 {
			return fmt.Errorf("Windows browser initialization failed (%d)", int(code))
		}
		return fmt.Errorf("Windows browser association failed (ShellExecute code %d)", int(code)-1)
	}
	return nil
}
