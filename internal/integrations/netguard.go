package integrations

import (
	"errors"
	"net"
	"net/http"
	"syscall"
	"time"
)

// Tiles of external accounts must not reach into the home network: their
// requests may only go to public internet addresses. The check runs on the
// address actually dialled (after DNS), so a name that points to a LAN
// address – or changes to one later – is refused as well.

// ErrHomeNetwork is the error for a blocked address. The text is shown on the
// tile; it is the same for every blocked address, so it does not tell whether
// something is there.
var ErrHomeNetwork = errors.New("Adressen im Heimnetz sind für dieses Konto gesperrt")

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)} // carrier-grade NAT, Tailscale

// PublicIP reports whether an address is on the public internet: not
// loopback, private, link-local (cloud metadata), CGNAT/Tailscale, multicast
// or unspecified.
func PublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip) ||
		ip.Equal(net.IPv4bcast) || ip[0] == 0)
}

func publicOnly(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return ErrHomeNetwork
	}
	if !PublicIP(net.ParseIP(host)) {
		return ErrHomeNetwork
	}
	return nil
}

// NewClientFor is NewClient, limited to public addresses when external is set.
func NewClientFor(insecure, external bool) *http.Client {
	c := NewClient(insecure)
	if !external {
		return c
	}
	tr := c.Transport.(*http.Transport)
	tr.Proxy = nil // a proxy would dial for us – and could sit in the home network
	tr.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: publicOnly}).DialContext
	return c
}

// HomeNetworkBlocked reports whether an error came from the external check.
func HomeNetworkBlocked(err error) bool { return errors.Is(err, ErrHomeNetwork) }
