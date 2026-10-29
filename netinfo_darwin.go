package main

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework CoreWLAN -framework Foundation
#import <CoreWLAN/CoreWLAN.h>
#include <string.h>

// wifi_info fills in the Wi-Fi interface name and RSSI. It returns 0 when
// there is no Wi-Fi interface. RSSI is 0 when not associated. Reading RSSI
// does not need Location Services; only the SSID does, so we never ask for it.
static int wifi_info(char *name, int len, int *rssi) {
	@autoreleasepool {
		CWInterface *iface = [[CWWiFiClient sharedWiFiClient] interface];
		if (iface == nil) return 0;
		NSString *n = [iface interfaceName];
		if (n != nil) strlcpy(name, [n UTF8String], len);
		*rssi = (int)[iface rssiValue];
		return 1;
	}
}
*/
import "C"

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"strings"
	"unsafe"
)

// netInfo is a snapshot of the local network configuration.
type netInfo struct {
	hasIP      bool   // any non-loopback interface with an address
	iface      string // interface carrying the default route
	gateway    net.IP // default gateway, nil when the route has none (e.g. VPN)
	nameserver string // first configured DNS server
	wifiIface  string
	rssi       int // dBm, 0 when unknown or not associated
}

// linkLabel names the link node in the chain.
func (n netInfo) linkLabel() string {
	switch {
	case n.iface == "":
		return "Link"
	case n.iface == n.wifiIface:
		return "Wi-Fi"
	case strings.HasPrefix(n.iface, "utun"), strings.HasPrefix(n.iface, "ipsec"), strings.HasPrefix(n.iface, "ppp"):
		return "VPN"
	case strings.HasPrefix(n.iface, "en"):
		return "Ethernet"
	default:
		return n.iface
	}
}

func (n netInfo) onWiFi() bool { return n.iface != "" && n.iface == n.wifiIface }

func readNetInfo() netInfo {
	var n netInfo
	n.hasIP = hasNonLoopbackIP()
	n.iface, n.gateway = defaultRoute()
	n.nameserver = firstNameserver()

	var buf [64]C.char
	var rssi C.int
	if C.wifi_info(&buf[0], C.int(len(buf)), &rssi) == 1 {
		n.wifiIface = C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
		n.rssi = int(rssi)
	}
	return n
}

func hasNonLoopbackIP() bool {
	ifaces, err := net.Interfaces()
	if err != nil {
		return false
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				return true
			}
		}
	}
	return false
}

// defaultRoute parses `route -n get default`, which is simpler and more
// reliable than walking the routing socket ourselves.
func defaultRoute() (iface string, gw net.IP) {
	out, err := exec.Command("/sbin/route", "-n", "get", "default").Output()
	if err != nil {
		return "", nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "interface":
			iface = v
		case "gateway":
			gw = net.ParseIP(v)
		}
	}
	return iface, gw
}

func firstNameserver() string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			return fields[1]
		}
	}
	return ""
}
