package main

import (
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// setupDNS points Go's resolver at real DNS servers when the system has no
// /etc/resolv.conf. That is the case on Android: a pure-Go binary there would
// otherwise ask 127.0.0.1:53 and fail every lookup. The Android app passes the
// active network's DNS servers in SMSC_DNS (comma-separated); public resolvers
// are the fallback.
func setupDNS(log zerolog.Logger) {
	if _, err := os.Stat("/etc/resolv.conf"); err == nil && os.Getenv("SMSC_DNS") == "" {
		return
	}
	servers := dnsServers(os.Getenv("SMSC_DNS"))
	log.Debug().Strs("servers", servers).Msg("using custom DNS servers")
	d := &net.Dialer{Timeout: 4 * time.Second}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var errs []error
			for _, s := range servers {
				c, err := d.DialContext(ctx, network, s)
				if err == nil {
					return c, nil
				}
				errs = append(errs, err)
			}
			return nil, errors.Join(errs...)
		},
	}
}

func dnsServers(list string) []string {
	var out []string
	for _, s := range strings.Split(list, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(strings.Trim(s, "[]"), "53")
		}
		out = append(out, s)
	}
	return append(out, "1.1.1.1:53", "8.8.8.8:53")
}

// setupTimeZone applies $TZ on Android, where Go always starts in UTC
// because it can't read the system time zone setting.
func setupTimeZone() {
	if runtime.GOOS != "android" {
		return
	}
	if loc, err := time.LoadLocation(os.Getenv("TZ")); err == nil && os.Getenv("TZ") != "" {
		time.Local = loc
	}
}
