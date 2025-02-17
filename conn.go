package nlmt

import (
	"bytes"
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// nconn (network conn) is the embedded struct in conn and lconn connections. It
// adds IPVersion, socket options and some helpers to net.UDPConn.
type nconn struct {
	conn        *net.UDPConn
	ipVer       IPVersion
	ip4conn     *ipv4.PacketConn
	ip6conn     *ipv6.PacketConn
	dscp        int
	dscpError   error
	dscpSupport bool
	ttl         int
	df          DF
	timeSource  TimeSource
}

func (n *nconn) init(conn *net.UDPConn, ipVer IPVersion, ts TimeSource) {
	n.conn = conn
	n.ipVer = ipVer
	n.df = DFDefault
	n.timeSource = ts

	// create x/net conns for socket options
	if n.ipVer&IPv4 != 0 {
		n.ip4conn = ipv4.NewPacketConn(n.conn)
		n.dscpError = n.ip4conn.SetTOS(1)
		n.ip4conn.SetTOS(0)
	} else {
		n.ip6conn = ipv6.NewPacketConn(n.conn)
		n.dscpError = n.ip6conn.SetTrafficClass(1)
		n.ip6conn.SetTrafficClass(0)
		n.ip6conn.SetControlMessage(ipv6.FlagTrafficClass, true)
	}

	n.dscpSupport = (n.dscpError == nil)
}

func (n *nconn) setDSCP(dscp int) (err error) {
	if n.dscp == dscp {
		return
	}
	if n.ip4conn != nil {
		err = n.ip4conn.SetTOS(dscp)
	} else {
		err = n.ip6conn.SetTrafficClass(dscp)
	}
	if err == nil {
		n.dscp = dscp
	}
	return
}

func (n *nconn) setTTL(ttl int) (err error) {
	if n.ttl == ttl {
		return
	}
	if n.ip4conn != nil {
		err = n.ip4conn.SetTTL(ttl)
	} else {
		err = n.ip6conn.SetHopLimit(ttl)
	}
	if err == nil {
		n.ttl = ttl
	}
	return
}

func (n *nconn) setReceiveDstAddr(b bool) (err error) {
	if n.ip4conn != nil {
		err = n.ip4conn.SetControlMessage(ipv4.FlagDst, b)
	} else {
		err = n.ip6conn.SetControlMessage(ipv6.FlagDst, b)
	}
	return
}

func (n *nconn) setDF(df DF) (err error) {
	if n.df == df {
		return
	}
	err = setSockoptDF(n.conn, df)
	if err == nil {
		n.df = df
	}
	return
}

func (n *nconn) localAddr() *net.UDPAddr {
	if n.conn == nil {
		return nil
	}
	a := n.conn.LocalAddr()
	if a == nil {
		return nil
	}
	return a.(*net.UDPAddr)
}

func (n *nconn) close() error {
	return n.conn.Close()
}

// cconn is used for client connections
type cconn struct {
	*nconn
	cfg    *ClientConfig
	ctoken ctoken
}

func dial(ctx context.Context, cfg *ClientConfig) (cc *cconn, err error) {
	// resolve (could support trying multiple addresses in succession)
	cfg.LocalAddress = addPort(cfg.LocalAddress, DefaultLocalPort)
	laddr, err := net.ResolveUDPAddr(cfg.IPVersion.udpNetwork(),
		cfg.LocalAddress)
	if err != nil {
		return
	}

	// add default port, if necessary, and resolve server
	cfg.RemoteAddress = addPort(cfg.RemoteAddress, DefaultPort)
	raddr, err := net.ResolveUDPAddr(cfg.IPVersion.udpNetwork(),
		cfg.RemoteAddress)
	if err != nil {
		return
	}

	// dial, using explicit network from remote address
	cfg.IPVersion = IPVersionFromUDPAddr(raddr)
	conn, err := net.DialUDP(cfg.IPVersion.udpNetwork(), laddr, raddr)
	if err != nil {
		return
	}

	// set resolved local and remote addresses back to Config
	cfg.LocalAddr = conn.LocalAddr()
	cfg.RemoteAddr = conn.RemoteAddr()
	cfg.LocalAddress = cfg.LocalAddr.String()
	cfg.RemoteAddress = cfg.RemoteAddr.String()

	// create cconn
	cc = &cconn{nconn: &nconn{}, cfg: cfg}
	cc.init(conn, cfg.IPVersion, cfg.TimeSource)

	// open connection to server
	err = cc.open(ctx)
	if isErrorCode(ServerClosed, err) {
		cc = nil
		err = nil
		return
	}

	return
}

func (c *cconn) open(ctx context.Context) (err error) {
	// validate open timeouts
	for _, to := range c.cfg.OpenTimeouts {
		if to < minOpenTimeout {
			err = Errorf(OpenTimeoutTooShort,
				"open timeout %s must be >= %s", to, minOpenTimeout)
			return
		}
	}

	errC := make(chan error)
	params := &c.cfg.Params

	// start receiving open replies and drop anything else
	go func() {
		var rerr error
		defer func() {
			errC <- rerr
		}()

		orp := newPacket(0, maxHeaderLen, c.cfg.HMACKey)

		for {
			if rerr = c.receive(orp); rerr != nil && !isErrorCode(ServerClosed, rerr) {
				return
			}
			if orp.flags()&flOpen == 0 {
				continue
			}
			if rerr = orp.addFields(fopenReply, false); rerr != nil {
				return
			}
			if orp.flags()&flClose == 0 && orp.ctoken() == 0 {
				rerr = Errorf(ConnTokenZero, "received invalid zero conn token")
				return
			}
			var sp *Params
			sp, rerr = parseParams(orp.payload())
			if rerr != nil {
				return
			}
			*params = *sp
			c.ctoken = orp.ctoken()
			if orp.flags()&flClose != 0 {
				c.close()
			}
			return
		}
	}()

	// start sending open requests
	sp := newPacket(0, maxHeaderLen, c.cfg.HMACKey)
	defer func() {
		if err != nil {
			c.close()
		}
	}()
	sp.setFlagBits(flOpen)
	if c.cfg.NoTest {
		sp.setFlagBits(flClose)
	}
	sp.setPayload(params.bytes())
	sp.updateHMAC()
	var received bool
	for _, to := range c.cfg.OpenTimeouts {
		err = c.send(sp)
		if err != nil {
			return
		}
		select {
		case <-time.After(to):
		case err = <-errC:
			received = true
			return
		case <-ctx.Done():
			err = ctx.Err()
			return
		}
	}
	if !received {
		defer c.nconn.close()
		err = Errorf(OpenTimeout, "no reply from server")
	}
	return
}

func (c *cconn) send(p *packet) (err error) {
	if err = c.setDSCP(p.dscp); err != nil {
		return
	}
	var n int
	n, err = c.conn.Write(p.bytes())
	p.tsent = c.timeSource.Now(BothClocks)
	p.trcvd = Time{}
	if err != nil {
		return
	}
	if n < p.length() {
		err = Errorf(ShortWrite, "only %d/%d bytes were sent", n, p.length())
	}
	return
}

func (c *cconn) receive(p *packet) (err error) {
	var n int
	if c.ip6conn != nil {
		var cm *ipv6.ControlMessage
		n, cm, _, err = c.ip6conn.ReadFrom(p.readTo())
		if cm != nil {
			//p.dscp = cm.TrafficClass >> 2
			p.ecn = cm.TrafficClass & 0x3
		}
	} else {
		n, err = c.conn.Read(p.readTo())
	}

	p.trcvd = c.timeSource.Now(BothClocks)
	p.tsent = Time{}
	p.dscp = 0
	if err != nil {
		return
	}
	if err = p.readReset(n); err != nil {
		return
	}
	if !p.reply() {
		err = Errorf(ExpectedReplyFlag, "reply flag not set")
		return
	}
	if p.flags()&flClose != 0 {
		err = Errorf(ServerClosed, "server closed connection")
		c.close()
	}
	return
}

func (c *cconn) newPacket() *packet {
	p := newPacket(0, c.cfg.Length, c.cfg.HMACKey)
	p.setConnToken(c.ctoken)
	p.raddr = c.conn.RemoteAddr().(*net.UDPAddr)
	return p
}

func (c *cconn) remoteAddr() *net.UDPAddr {
	if c.conn == nil {
		return nil
	}
	a := c.conn.RemoteAddr()
	if a == nil {
		return nil
	}
	return a.(*net.UDPAddr)
}

func (c *cconn) close() (err error) {
	defer func() {
		err = c.nconn.close()
	}()

	// send one close packet if necessary
	if c.ctoken != 0 {
		cp := newPacket(0, maxHeaderLen, c.cfg.HMACKey)
		if err = cp.setFields(fcloseRequest, true); err != nil {
			return
		}
		cp.setFlagBits(flClose)
		cp.setConnToken(c.ctoken)
		cp.updateHMAC()
		err = c.send(cp)
	}
	return
}

// lconn is used for server listeners
type lconn struct {
	*nconn
	cm4      ipv4.ControlMessage
	cm6      ipv6.ControlMessage
	setSrcIP bool
}

// listen creates an lconn by listening on a UDP address.
func listen(laddr *net.UDPAddr, setSrcIP bool, ts TimeSource) (l *lconn, err error) {
	ipVer := IPVersionFromUDPAddr(laddr)
	var conn *net.UDPConn
	if conn, err = net.ListenUDP(ipVer.udpNetwork(), laddr); err != nil {
		return
	}
	l = &lconn{nconn: &nconn{}, setSrcIP: setSrcIP && laddr.IP.IsUnspecified()}
	l.init(conn, ipVer, ts)
	return
}

// listenAll creates lconns on multiple addresses, with separate lconns for IPv4
// and IPv6, so that socket options can be set correctly, which is not possible
// with a dual stack conn.
func listenAll(ipVer IPVersion, addrs []string, setSrcIP bool,
	ts TimeSource) (lconns []*lconn, err error) {
	laddrs, err := resolveListenAddrs(addrs, ipVer)
	if err != nil {
		return
	}
	lconns = make([]*lconn, 0, 16)
	for _, laddr := range laddrs {
		var l *lconn
		l, err = listen(laddr, setSrcIP, ts)
		if err != nil {
			return
		}
		lconns = append(lconns, l)
	}
	if len(lconns) == 0 {
		err = Errorf(NoSuitableAddressFound, "no suitable %s address found", ipVer)
		return
	}
	return
}

func (l *lconn) send(p *packet) (err error) {
	p.updateHMAC()
	if err = l.setDSCP(p.dscp); err != nil {
		return
	}
	var n int
	if !l.setSrcIP {
		n, err = l.conn.WriteToUDP(p.bytes(), p.raddr)
	} else if l.ip4conn != nil {
		l.cm4.Src = p.srcIP
		n, err = l.ip4conn.WriteTo(p.bytes(), &l.cm4, p.raddr)
	} else {
		l.cm6.Src = p.srcIP
		n, err = l.ip6conn.WriteTo(p.bytes(), &l.cm6, p.raddr)
	}
	p.tsent = l.timeSource.Now(BothClocks)
	p.trcvd = Time{}
	if err != nil {
		return
	}
	if n < p.length() {
		err = Errorf(ShortWrite, "only %d/%d bytes were sent", n, p.length())
	}
	return
}

func (l *lconn) receive(p *packet) (err error) {
	var n int
	p.ecn = 0
	if !l.setSrcIP {
		n, p.raddr, err = l.conn.ReadFromUDP(p.readTo())
		p.dstIP = nil
	} else if l.ip4conn != nil {
		var cm *ipv4.ControlMessage
		var src net.Addr
		n, cm, src, err = l.ip4conn.ReadFrom(p.readTo())
		if src != nil {
			p.raddr = src.(*net.UDPAddr)
		}
		if cm != nil {
			p.dstIP = cm.Dst
		} else {
			p.dstIP = nil
		}
	} else {
		var cm *ipv6.ControlMessage
		var src net.Addr
		n, cm, src, err = l.ip6conn.ReadFrom(p.readTo())
		if src != nil {
			p.raddr = src.(*net.UDPAddr)
		}
		if cm != nil {
			p.dstIP = cm.Dst
			//p.dscp = cm.TrafficClass >> 2
			p.ecn = cm.TrafficClass // For test purposes
		} else {
			p.dstIP = nil
		}
	}

	p.srcIP = nil
	p.dscp = 0
	p.trcvd = l.timeSource.Now(BothClocks)
	p.tsent = Time{}
	if err != nil {
		return
	}
	if err = p.readReset(n); err != nil {
		return
	}
	if p.reply() {
		err = Errorf(UnexpectedReplyFlag, "unexpected reply flag set")
		return
	}
	return
}

// parseIfaceListenAddr parses an interface listen address into an interface
// name and service. ok is false if the string does not use the syntax
// %iface:service, where :service is optional.
func parseIfaceListenAddr(addr string) (iface, service string, ok bool) {
	if !strings.HasPrefix(addr, "%") {
		return
	}
	parts := strings.Split(addr[1:], ":")
	switch len(parts) {
	case 2:
		service = parts[1]
		if len(service) == 0 {
			return
		}
		fallthrough
	case 1:
		iface = parts[0]
		if len(iface) == 0 {
			return
		}
		ok = true
		return
	}
	return
}

// resolveIfaceListenAddr resolves an interface name and service (port name
// or number) into a slice of UDP addresses.
func resolveIfaceListenAddr(ifaceName string, service string,
	ipVer IPVersion) (laddrs []*net.UDPAddr, err error) {
	// get interfaces
	var ifaces []net.Interface
	ifaces, err = net.Interfaces()
	if err != nil {
		return
	}

	// resolve service to port
	var port int
	if service != "" {
		port, err = net.LookupPort(ipVer.udpNetwork(), service)
		if err != nil {
			return
		}
	} else {
		port = DefaultPortInt
	}

	// helper to get IP and zone from interface address
	ifaceIP := func(a net.Addr) (ip net.IP, zone string, ok bool) {
		switch v := a.(type) {
		case *net.IPNet:
			{
				ip = v.IP
				ok = true
			}
		case *net.IPAddr:
			{
				ip = v.IP
				zone = v.Zone
				ok = true
			}
		}
		return
	}

	// helper to test if IP is one we can listen on
	isUsableIP := func(ip net.IP) bool {
		if IPVersionFromIP(ip)&ipVer == 0 {
			return false
		}
		if !ip.IsLinkLocalUnicast() && !ip.IsGlobalUnicast() && !ip.IsLoopback() {
			return false
		}
		return true
	}

	// get addresses
	laddrs = make([]*net.UDPAddr, 0, 16)
	ifaceFound := false
	ifaceUp := false
	for _, iface := range ifaces {
		if !glob(ifaceName, iface.Name) {
			continue
		}
		ifaceFound = true
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		ifaceUp = true
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, a := range ifaceAddrs {
			ip, zone, ok := ifaceIP(a)
			if ok && isUsableIP(ip) {
				if ip.IsLinkLocalUnicast() && zone == "" {
					zone = iface.Name
				}
				udpAddr := &net.UDPAddr{IP: ip, Port: port, Zone: zone}
				laddrs = append(laddrs, udpAddr)
			}
		}
	}

	if !ifaceFound {
		err = Errorf(NoMatchingInterfaces, "%s does not match any interfaces", ifaceName)
	} else if !ifaceUp {
		err = Errorf(NoMatchingInterfacesUp, "no interfaces matching %s are up", ifaceName)
	}

	return
}

// resolveListenAddr resolves a listen address string into a slice of UDP
// addresses.
func resolveListenAddr(addr string, ipVer IPVersion) (laddrs []*net.UDPAddr,
	err error) {
	laddrs = make([]*net.UDPAddr, 0, 2)
	for _, v := range ipVer.Separate() {
		addr = addPort(addr, DefaultPort)
		laddr, err := net.ResolveUDPAddr(v.udpNetwork(), addr)
		if err != nil {
			continue
		}
		if laddr.IP == nil {
			laddr.IP = v.ZeroIP()
		}
		laddrs = append(laddrs, laddr)
	}
	return
}

// resolveListenAddrs resolves a slice of listen address strings into a slice
// of UDP addresses.
func resolveListenAddrs(addrs []string, ipVer IPVersion) (laddrs []*net.UDPAddr,
	err error) {
	// resolve addresses
	laddrs = make([]*net.UDPAddr, 0, 16)
	for _, addr := range addrs {
		var la []*net.UDPAddr
		iface, service, ok := parseIfaceListenAddr(addr)
		if ok {
			la, err = resolveIfaceListenAddr(iface, service, ipVer)
		} else {
			la, err = resolveListenAddr(addr, ipVer)
		}
		if err != nil {
			return
		}
		laddrs = append(laddrs, la...)
	}
	// sort addresses
	sort.Slice(laddrs, func(i, j int) bool {
		if bytes.Compare(laddrs[i].IP, laddrs[j].IP) < 0 {
			return true
		}
		if laddrs[i].Port < laddrs[j].Port {
			return true
		}
		return laddrs[i].Zone < laddrs[j].Zone
	})
	// remove duplicates
	udpAddrsEqual := func(a *net.UDPAddr, b *net.UDPAddr) bool {
		if !a.IP.Equal(b.IP) {
			return false
		}
		if a.Port != b.Port {
			return false
		}
		return a.Zone == b.Zone
	}
	for i := 1; i < len(laddrs); i++ {
		if udpAddrsEqual(laddrs[i], laddrs[i-1]) {
			laddrs = append(laddrs[:i], laddrs[i+1:]...)
			i--
		}
	}
	// check for combination of specified and unspecified IP addresses
	m := make(map[int]int)
	for _, la := range laddrs {
		if la.IP.IsUnspecified() {
			m[la.Port] = m[la.Port] | 1
		} else {
			m[la.Port] = m[la.Port] | 2
		}
	}
	for k, v := range m {
		if v > 2 {
			err = Errorf(UnspecifiedWithSpecifiedAddresses,
				"invalid combination of unspecified and specified IP addresses port %d", k)
			break
		}
	}
	return
}
