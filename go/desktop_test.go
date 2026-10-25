package main

import (
	"io"
	"testing"
)

func TestDesktopConfigIsLoopbackAndDoesNotOpenBrowser(t *testing.T) {
	c, err := parseConfig([]string{"--desktop", "--port", "0", "--host", "0.0.0.0"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if c.port != 0 || c.host != "127.0.0.1" || !c.noBrowser || !c.desktop {
		t.Fatalf("unsafe desktop config: %+v", c)
	}
	for _, args := range [][]string{{"--port", "0"}, {"--desktop", "--port", "-1"}, {"--desktop", "--port", "65536"}} {
		if _, err := parseConfig(args, io.Discard); err == nil {
			t.Fatalf("accepted invalid port: %v", args)
		}
	}
}
