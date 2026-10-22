package fetch

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// newGuardedTransport membuat http.Transport yang meresolve host sendiri
// dan menolak IP privat/loopback/link-local. Pengecekan dilakukan di
// DialContext (bukan sekadar di hostname sebelum request) supaya:
//   - redirect ke host lain tetap dicek, karena tiap hop membuka dial baru;
//   - DNS rebinding tertangkal, karena IP yang dicek adalah IP yang benar-
//     benar dipakai untuk connect, bukan hasil lookup terpisah.
func newGuardedTransport(allowPrivate bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		// Proxy sengaja nil: proxy dari env akan melewati guard ini.
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var allowed []net.IPAddr
			for _, ip := range ips {
				if allowPrivate || isPublicIP(ip.IP) {
					allowed = append(allowed, ip)
				}
			}
			if len(allowed) == 0 {
				return nil, fmt.Errorf("%w: %s", ErrBlocked, host)
			}
			var lastErr error
			for _, ip := range allowed {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          20,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
	}
}

// Rentang tambahan yang tidak tercakup oleh helper net.IP bawaan.
var blockedNets = mustCIDRs(
	"0.0.0.0/8",     // "this" network
	"100.64.0.0/10", // CGNAT (shared address space)
	"192.0.0.0/24",  // IETF protocol assignments
	"198.18.0.0/15", // benchmarking
	"240.0.0.0/4",   // reserved
	"255.255.255.255/32",
	"::/128",
	"64:ff9b::/96",  // NAT64 — bisa dipakai menjangkau IPv4 privat
	"2001:db8::/32", // dokumentasi
)

func mustCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// isPublicIP true hanya untuk alamat yang aman dihubungi dari server.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		ip = ip4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
		return false
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}
