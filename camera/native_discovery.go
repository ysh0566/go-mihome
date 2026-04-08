package camera

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	miHomeCS2Port         = 32108
	miHomeCS2ProbeTimeout = 250 * time.Millisecond
	miHomeCS2ProbeWorkers = 64
)

func discoverMiHomeCS2Hosts(ctx context.Context, preferredHost string) ([]string, error) {
	preferredIP := net.ParseIP(strings.TrimSpace(preferredHost)).To4()
	if preferredIP == nil {
		return nil, fmt.Errorf("xiaomi native preferred host %q is not IPv4", preferredHost)
	}
	network, localHosts := miHomeDiscoveryNetwork(preferredIP)
	if network == nil {
		return nil, fmt.Errorf("xiaomi native discovery network unavailable for %s", preferredHost)
	}

	skip := map[string]struct{}{
		strings.TrimSpace(preferredHost): {},
	}
	for _, host := range localHosts {
		skip[host] = struct{}{}
	}

	candidates := make([]string, 0, 254)
	for ip := cloneIPv4(network.IP); ip != nil && network.Contains(ip); incrementIPv4(ip) {
		if broadcastIPv4(network, ip) {
			continue
		}
		host := ip.String()
		if _, excluded := skip[host]; excluded {
			continue
		}
		candidates = append(candidates, host)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("xiaomi native discovery has no scan candidates")
	}

	resultCh := make(chan string, len(candidates))
	workCh := make(chan string)
	var wg sync.WaitGroup
	workers := miHomeCS2ProbeWorkers
	if workers > len(candidates) {
		workers = len(candidates)
	}
	if workers <= 0 {
		workers = 1
	}
	for idx := 0; idx < workers; idx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for host := range workCh {
				if miHomeCS2ProbeHost(ctx, host) {
					resultCh <- host
				}
			}
		}()
	}
	go func() {
		for _, host := range candidates {
			select {
			case <-ctx.Done():
				close(workCh)
				wg.Wait()
				close(resultCh)
				return
			case workCh <- host:
			}
		}
		close(workCh)
		wg.Wait()
		close(resultCh)
	}()

	hosts := make([]string, 0, 4)
	for host := range resultCh {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	if len(hosts) == 0 {
		return nil, fmt.Errorf("xiaomi native discovery found no CS2 hosts in %s", network.String())
	}
	return hosts, nil
}

func miHomeCS2ProbeHost(ctx context.Context, host string) bool {
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()

	addr := &net.UDPAddr{IP: net.ParseIP(host).To4(), Port: miHomeCS2Port}
	if addr.IP == nil {
		return false
	}
	deadline := time.Now().Add(miHomeCS2ProbeTimeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	_ = conn.SetDeadline(deadline)
	if _, err := conn.WriteToUDP([]byte{miHomeCS2Magic, miHomeCS2MsgLanSearch, 0, 0}, addr); err != nil {
		return false
	}

	buffer := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return false
		}
		n, source, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return false
		}
		if source == nil || !source.IP.Equal(addr.IP) || n < 2 {
			continue
		}
		return buffer[1] == miHomeCS2MsgPunchPkt
	}
}

func miHomeDiscoveryNetwork(preferredIP net.IP) (*net.IPNet, []string) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, nil
	}
	localHosts := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet == nil {
			continue
		}
		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}
		localHosts = append(localHosts, ip.String())
		if ipNet.Contains(preferredIP) {
			return &net.IPNet{IP: ip.Mask(ipNet.Mask), Mask: ipNet.Mask}, localHosts
		}
	}
	mask := net.CIDRMask(24, 32)
	return &net.IPNet{IP: preferredIP.Mask(mask), Mask: mask}, localHosts
}

func cloneIPv4(ip net.IP) net.IP {
	if ip == nil {
		return nil
	}
	value := ip.To4()
	if value == nil {
		return nil
	}
	return append(net.IP(nil), value...)
}

func incrementIPv4(ip net.IP) {
	if len(ip) != net.IPv4len {
		return
	}
	for idx := len(ip) - 1; idx >= 0; idx-- {
		ip[idx]++
		if ip[idx] != 0 {
			return
		}
	}
}

func broadcastIPv4(network *net.IPNet, ip net.IP) bool {
	if network == nil || ip == nil {
		return false
	}
	if !network.Contains(ip) {
		return false
	}
	broadcast := make(net.IP, net.IPv4len)
	base := network.IP.To4()
	if base == nil {
		return false
	}
	for idx := 0; idx < net.IPv4len; idx++ {
		broadcast[idx] = base[idx] | ^network.Mask[idx]
	}
	return ip.Equal(base) || ip.Equal(broadcast)
}
