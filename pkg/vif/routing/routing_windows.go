package routing

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"

	"github.com/datawire/dlib/dexec"
	"github.com/telepresenceio/telepresence/v2/pkg/iputil"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func GetRoutingTable(ctx context.Context) ([]Route, error) {
	table, err := winipcfg.GetIPForwardTable2(syscall.AF_UNSPEC)
	if err != nil {
		return nil, fmt.Errorf("unable to get routing table: %w", err)
	}
	routes := []Route{}
	for _, row := range table {
		dst := row.DestinationPrefix.IPNet()
		if dst.IP == nil || dst.Mask == nil {
			continue
		}
		gw := row.NextHop.IP()
		if gw == nil {
			continue
		}
		ifaceIdx := int(row.InterfaceIndex)
		iface, err := net.InterfaceByIndex(ifaceIdx)
		if err != nil {
			return nil, fmt.Errorf("unable to get interface at index %d: %w", ifaceIdx, err)
		}
		localIP, err := interfaceLocalIP(iface, dst.IP.To4() != nil)
		if err != nil {
			return nil, err
		}
		ip, mask := make(net.IP, len(dst.IP)), make(net.IPMask, len(dst.Mask))
		copy(ip, dst.IP)
		copy(mask, dst.Mask)
		routes = append(routes, Route{
			LocalIP: localIP,
			Gateway: gw,
			RoutedNet: &net.IPNet{
				IP:   ip,
				Mask: mask,
			},
			Interface: iface,
		})
	}
	return routes, nil
}

func GetRoute(ctx context.Context, routedNet *net.IPNet) (Route, error) {
	ip := routedNet.IP
	pshScript := fmt.Sprintf(`
$job = Find-NetRoute -RemoteIPAddress "%s" -AsJob | Wait-Job -Timeout 30
if ($job.State -ne 'Completed') {
	throw "timed out getting route after 30 seconds."
}
$obj = $job | Receive-Job
$obj.IPAddress
$obj.NextHop
$obj.InterfaceIndex[0]
`, ip)
	cmd := dexec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", pshScript)
	// cmd.DisableLogging = true
	out, err := cmd.Output()
	if err != nil {
		return Route{}, fmt.Errorf("unable to run 'Find-Netroute -RemoteIPAddress %s': %w", ip, err)
	}
	lines := strings.Split(string(out), "\n")
	localIP := iputil.Parse(strings.TrimSpace(lines[0]))
	if localIP == nil {
		return Route{}, fmt.Errorf("unable to parse IP from %s", lines[0])
	}
	gatewayIP := iputil.Parse(strings.TrimSpace(lines[1]))
	if gatewayIP == nil {
		return Route{}, fmt.Errorf("unable to parse gateway IP from %s", lines[1])
	}
	interfaceIndex, err := strconv.Atoi(strings.TrimSpace(lines[2]))
	if err != nil {
		return Route{}, fmt.Errorf("unable to parse interface index from %s: %w", lines[2], err)
	}
	iface, err := net.InterfaceByIndex(interfaceIndex)
	if err != nil {
		return Route{}, fmt.Errorf("unable to get interface for index %d: %w", interfaceIndex, err)
	}
	return Route{
		LocalIP:   localIP,
		Gateway:   gatewayIP,
		Interface: iface,
		RoutedNet: routedNet,
	}, nil
}
