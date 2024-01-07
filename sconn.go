package nlmt

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path"
	"strings"
	"text/tabwriter"
	"time"
)

// sconn stores the state for a client's connection to the server
type sconn struct {
	*listener
	ctoken         ctoken
	raddr          *net.UDPAddr
	params         *Params
	filler         Filler
	created        time.Time
	firstUsed      time.Time
	lastUsed       time.Time
	packetBucket   float64
	lastSeqno      Seqno
	receivedCount  ReceivedCount
	receivedWindow ReceivedWindow
	rHandler       SConnHandler
	rwinValid      bool
	bytes          uint64
	rec            *OneWayRecorder
}

func newSconn(l *listener, raddr *net.UDPAddr) *sconn {
	return &sconn{
		listener:     l,
		raddr:        raddr,
		filler:       l.Filler,
		created:      time.Now(),
		lastSeqno:    InvalidSeqno,
		packetBucket: float64(l.PacketBurst),
	}
}

func accept(l *listener, p *packet) (sc *sconn, err error) {
	// create sconn
	sc = newSconn(l, p.raddr)

	// parse, restrict and set params
	var params *Params
	params, err = parseParams(p.payload())
	if err != nil {
		return
	}
	sc.restrictParams(params)
	sc.params = params

	// set filler
	if len(sc.params.ServerFill) > 0 &&
		sc.params.ServerFill != DefaultServerFiller.String() {
		sc.filler, err = NewFiller(sc.params.ServerFill)
		if err != nil {
			l.eventf(InvalidServerFill, p.raddr,
				"invalid server fill %s requested, defaulting to %s (%s)",
				sc.params.ServerFill, DefaultServerFiller.String(), err.Error())
			sc.filler = l.Filler
			sc.params.ServerFill = DefaultServerFiller.String()
		}
	}

	// determine state of connection
	if params.ProtocolVersion != ProtocolVersion {
		l.eventf(ProtocolVersionMismatch, p.raddr,
			"close connection, client version %d != server version %d",
			params.ProtocolVersion, ProtocolVersion)
		p.setFlagBits(flClose)
	} else if p.flags()&flClose != 0 {
		l.eventf(OpenClose, p.raddr, "open-close connection")
	} else {
		l.cmgr.put(sc)
		l.eventf(NewConn, p.raddr, "new connection, token=%016x, group=%s, trip-mode=%s", sc.ctoken, sc.params.Group, sc.params.TripMode)
	}

	// add recorder handler
	sc.rHandler = &sconnHandler{p.raddr, sc.listener.ServerConfig.netprintp, sc.listener.ServerConfig.Quiet, sc.listener.ServerConfig.ReallyQuiet}

	// create recorder if oneway
	if params.TripMode == TMOneWay {
		if sc.rec, err = newOneWayRecorder(pcount(sc.params.Duration, sc.params.Interval), uint(sc.params.Length), sc.params.Interval, sc.TimeSource,
			sc.rHandler); err != nil {
			return
		}
	}

	// prepare and send open reply
	if sc.SetSrcIP {
		p.srcIP = p.dstIP
	}
	p.setConnToken(sc.ctoken)
	p.setReply(true)
	p.setPayload(params.bytes())
	err = l.conn.send(p)
	return
}

func replaceXWithIPPortDateTime(addr *net.UDPAddr, format string, at string) string {

	ip := strings.Replace(addr.IP.String(), ".", "-", -1)
	port := fmt.Sprintf("%d", addr.Port)
	now := time.Now()

	replacements := map[string]string{
		"q": at,
		"x": ip + "_" + port,
		"y": fmt.Sprintf("%04d", now.Year()),
		"m": fmt.Sprintf("%02d", now.Month()),
		"d": fmt.Sprintf("%02d", now.Day()),
		"h": fmt.Sprintf("%02d", now.Hour()),
		"t": fmt.Sprintf("%02d", now.Minute()),
		"w": fmt.Sprintf("%02d", now.Second()),
	}

	for k, v := range replacements {
		format = strings.Replace(format, k, v, -1)
	}

	return format
}

func (sc *sconn) serve(p *packet) (closed bool, err error) {
	if !udpAddrsEqual(p.raddr, sc.raddr) {
		err = Errorf(AddressMismatch, "address mismatch (expected %s for %016x)",
			sc.raddr, p.ctoken())
		return
	}
	if p.flags()&flClose != 0 {
		closed = true
		err = sc.serveClose(p)

		if sc.params.TripMode == TMOneWay {
			// print results
			r := newOneWayResult(sc.rec, sc.listener.ServerConfig)
			if !*&sc.listener.ServerConfig.ReallyQuiet {
				printOneWayResult(r)
			}

			// write results to JSON
			if sc.listener.ServerConfig.OutputJSON {
				var fileAddr string
				if sc.listener.ServerConfig.OutputJSONAddr == "" {
					// create file address
					// combine output directory setting and group name to form outputdir
					outputDirStr := path.Join(sc.OutputDir, sc.params.Group, "server")

					// check output directory, create it if it does not exist
					_, errs := os.Stat(outputDirStr)
					if os.IsNotExist(errs) {
						// Directory does not exist, so create it
						err := os.MkdirAll(outputDirStr, os.ModePerm)
						if err != nil {
							fmt.Println("Error creating output directory:", err)
						}
					}

					// combine directory and filename to form a complete file address
					fileAddr = path.Join(outputDirStr, replaceXWithIPPortDateTime(sc.raddr, DefaultJSONAddrFormat, "se"))

				} else {
					fileAddr = sc.listener.ServerConfig.OutputJSONAddr
				}

				if err := writeOneWayResultJSON(r, fileAddr, false); err != nil {
					exitOnError(err, exitCodeRuntimeError)
				}
			}
		}

		return
	}

	if sc.params.TripMode == TMRound {
		closed, err = sc.serveEcho(p)
	} else if sc.params.TripMode == TMOneWay {
		closed, err = sc.serveNoEcho(p)
	}
	return
}

func (sc *sconn) serveClose(p *packet) (err error) {
	if err = p.addFields(fcloseRequest, false); err != nil {
		return
	}
	sc.eventf(CloseConn, p.raddr, "close connection, token=%016x", sc.ctoken)
	if scr := sc.cmgr.remove(sc.ctoken); scr == nil {
		sc.eventf(RemoveNoConn, p.raddr,
			"sconn not in connmgr, token=%016x", sc.ctoken)
	}
	return
}

func (sc *sconn) serveEcho(p *packet) (closed bool, err error) {
	// handle echo request
	if err = p.addFields(fechoRequest, false); err != nil {
		return
	}

	// check that request isn't too large
	if sc.MaxLength > 0 && p.length() > sc.MaxLength {
		err = Errorf(LargeRequest, "request too large (%d > %d)",
			p.length(), sc.MaxLength)
		return
	}

	// update first used
	now := time.Now()
	if sc.firstUsed.IsZero() {
		sc.firstUsed = now
	}

	// enforce minimum interval
	if sc.MinInterval > 0 {
		if !sc.lastUsed.IsZero() {
			earned := float64(now.Sub(sc.lastUsed)) / float64(sc.MinInterval)
			sc.packetBucket += earned
			if sc.packetBucket > float64(sc.PacketBurst) {
				sc.packetBucket = float64(sc.PacketBurst)
			}
		}
		if sc.packetBucket < 1 {
			sc.lastUsed = now
			err = Errorf(ShortInterval, "drop due to short packet interval")
			return
		}
		sc.packetBucket--
	}

	// set reply flag
	p.setReply(true)

	// update last used
	sc.lastUsed = now

	// slide received seqno window
	seqno := p.seqno()
	sinceLastSeqno := seqno - sc.lastSeqno
	if sinceLastSeqno > 0 {
		sc.receivedWindow <<= sinceLastSeqno
	}
	if sinceLastSeqno >= 0 { // new, duplicate or first packet
		sc.receivedWindow |= 0x1
		sc.rwinValid = true
	} else { // late packet
		sc.receivedWindow |= (0x1 << -sinceLastSeqno)
		sc.rwinValid = false
	}
	// update received count
	sc.receivedCount++
	// update seqno and last used times
	sc.lastSeqno = seqno

	// check if max test duration exceeded (but still return packet)
	if sc.MaxDuration > 0 && time.Since(sc.firstUsed) >
		sc.MaxDuration+maxDurationGrace {
		sc.eventf(ExceededDuration, p.raddr,
			"closing connection due to duration limit exceeded")
		sc.cmgr.remove(sc.ctoken)
		p.setFlagBits(flClose)
		closed = true
	}

	// set packet dscp value
	if sc.AllowDSCP && sc.conn.dscpSupport {
		p.dscp = sc.params.DSCP
	}

	// set source IP, if necessary
	if sc.SetSrcIP {
		p.srcIP = p.dstIP
	}

	// initialize test packet
	p.setLen(0)

	// set received stats
	if sc.params.ReceivedStats&ReceivedStatsCount != 0 {
		p.setReceivedCount(sc.receivedCount)
	}
	if sc.params.ReceivedStats&ReceivedStatsWindow != 0 {
		if sc.rwinValid {
			p.setReceivedWindow(sc.receivedWindow)
		} else {
			p.setReceivedWindow(0)
		}
	}

	// set timestamps
	at := sc.params.StampAt
	cl := sc.params.Clock
	if at != AtNone {
		var rt Time
		var st Time
		if at == AtMidpoint {
			mt := p.trcvd.Midpoint(sc.TimeSource.Now(cl))
			rt = mt
			st = mt
		} else {
			if at&AtReceive != 0 {
				rt = p.trcvd.KeepClocks(cl)
			}
			if at&AtSend != 0 {
				st = sc.TimeSource.Now(cl)
			}
		}
		p.setTimestamp(at, Timestamp{rt, st})
	} else {
		p.removeTimestamps()
	}

	// set length
	p.setLen(sc.params.Length)

	// fill payload
	if sc.filler != nil {
		if err = p.readPayload(sc.filler); err != nil {
			return
		}
	}

	// simulate dropped packets, if necessary
	if serverDropsPercent > 0 && rand.Float32() < serverDropsPercent {
		return
	}

	// simulate duplicates, if necessary
	if serverDupsPercent > 0 {
		for rand.Float32() < serverDupsPercent {
			if err = sc.conn.send(p); err != nil {
				return
			}
		}
	}

	// send reply
	err = sc.conn.send(p)
	return
}

func (sc *sconn) serveNoEcho(p *packet) (closed bool, err error) {

	// handle echo request
	if err = p.addFields(fechoRequest, false); err != nil {
		return
	}

	// check that request isn't too large
	if sc.MaxLength > 0 && p.length() > sc.MaxLength {
		err = Errorf(LargeRequest, "request too large (%d > %d)",
			p.length(), sc.MaxLength)
		return
	}

	// update first used
	now := time.Now()
	if sc.firstUsed.IsZero() {
		sc.firstUsed = now
	}

	// enforce minimum interval
	if sc.MinInterval > 0 {
		if !sc.lastUsed.IsZero() {
			earned := float64(now.Sub(sc.lastUsed)) / float64(sc.MinInterval)
			sc.packetBucket += earned
			if sc.packetBucket > float64(sc.PacketBurst) {
				sc.packetBucket = float64(sc.PacketBurst)
			}
		}
		if sc.packetBucket < 1 {
			sc.lastUsed = now
			err = Errorf(ShortInterval, "drop due to short packet interval")
			return
		}
		sc.packetBucket--
	}

	// update last used
	sc.lastUsed = now

	// slide received seqno window
	seqno := p.seqno()
	sinceLastSeqno := seqno - sc.lastSeqno
	if sinceLastSeqno > 0 {
		sc.receivedWindow <<= sinceLastSeqno
	}
	if sinceLastSeqno >= 0 { // new, duplicate or first packet
		sc.receivedWindow |= 0x1
		sc.rwinValid = true
	} else { // late packet
		sc.receivedWindow |= (0x1 << -sinceLastSeqno)
		sc.rwinValid = false
	}
	// update received count
	sc.receivedCount++
	// update seqno and last used times
	sc.lastSeqno = seqno

	// check if max test duration exceeded (but still return packet)
	if sc.MaxDuration > 0 && time.Since(sc.firstUsed) >
		sc.MaxDuration+maxDurationGrace {
		sc.eventf(ExceededDuration, p.raddr,
			"closing connection due to duration limit exceeded")
		sc.cmgr.remove(sc.ctoken)
		closed = true
	}

	// add expected received stats fields
	p.addReceivedStatsFields(ReceivedStatsBoth)

	// add expected timestamp fields
	p.addTimestampFields(AtSend, BothClocks)

	// get timestamps
	p.stampAt()
	p.clock()
	sts := p.timestamp()

	// record receive if all went well (may fail if seqno not found)
	ok := sc.rec.recordReceive(p, &sts)
	if !ok {
		err = Errorf(UnexpectedSequenceNumber, "unexpected reply sequence number %d", p.seqno())
		return
	}

	return
}

func (sc *sconn) expired() bool {
	if sc.Timeout == 0 {
		return false
	}
	return !sc.lastUsed.IsZero() &&
		time.Since(sc.lastUsed) > sc.Timeout+timeoutGrace
}

func (sc *sconn) restrictParams(p *Params) {
	if p.ProtocolVersion != ProtocolVersion {
		p.ProtocolVersion = ProtocolVersion
	}
	if sc.MaxDuration > 0 && p.Duration > sc.MaxDuration {
		p.Duration = sc.MaxDuration
	}
	if sc.MinInterval > 0 && p.Interval < sc.MinInterval {
		p.Interval = sc.MinInterval
	}
	if sc.Timeout > 0 && p.Interval > sc.Timeout/maxIntervalTimeoutFactor {
		p.Interval = sc.Timeout / maxIntervalTimeoutFactor
	}
	if sc.MaxLength > 0 && p.Length > sc.MaxLength {
		p.Length = sc.MaxLength
	}
	p.StampAt = sc.AllowStamp.Restrict(p.StampAt)
	if !sc.AllowDSCP || !sc.conn.dscpSupport {
		p.DSCP = 0
	}
	if len(p.ServerFill) > 0 && !globAny(sc.AllowFills, p.ServerFill) {
		p.ServerFill = DefaultServerFiller.String()
	}
	return
}

// SConnHandler is called with serverConnection events, as well as separately when
// packets are sent and received. See the documentation for Recorder for
// information on locking for concurrent access.
type SConnHandler interface {
	Handler

	OneWayRecorderHandler
}

func printOneWayResult(r *OneWayResult) {
	// set some stat variables for later brevity
	sds := r.SendDelayStats
	svs := r.SendIPDVStats

	if r.SendErr != nil {
		if r.SendErr != context.Canceled {
			printf("\nTerminated due to send error: %s", r.SendErr)
		}
	}
	printf("")

	printStats := func(title string, s DurationStats) {
		if s.N > 0 {
			var med string
			if m, ok := s.Median(); ok {
				med = rdur(m).String()
			}
			printf("%s\t%s\t%s\t%s\t%s\t%s\t", title, rdur(s.Min), rdur(s.Mean()),
				med, rdur(s.Max), rdur(s.Stddev()))
		}
	}

	setTabWriter(tabwriter.AlignRight)

	printf("\tMin\tMean\tMedian\tMax\tStddev\t")
	printf("\t---\t----\t------\t---\t------\t")
	printStats("send delay", sds)
	printf("\t\t\t\t\t\t")
	printStats("send IPDV", svs)
	printf("")
	printf("                duration: %s (wait %s)", rdur(r.Duration), rdur(r.Wait))
	printf("   packets sent/received: %d/%d (%.2f%% loss)", r.lastSeqno+1,
		r.PacketsReceived, r.PacketLossPercent)
	if r.Duplicates > 0 {
		printf("          *** DUPLICATES: %d (%.2f%%)", r.Duplicates,
			r.DuplicatePercent)
	}
	if r.LatePackets > 0 {
		printf("late (out-of-order) pkts: %d (%.2f%%)", r.LatePackets,
			r.LatePacketsPercent)
	}
	printf("     bytes received: %d", r.BytesReceived)
	printf("       receive rate: %s", r.ReceiveRate)
	printf("           packet length: %d bytes", r.PacketLength)

	flush()
}

func writeOneWayResultJSON(r *OneWayResult, output string, cancelled bool) error {
	var jout io.Writer

	var gz bool
	if output == "-" {
		if cancelled {
			return nil
		}
		jout = os.Stdout
	} else {
		gz = true
		if strings.HasSuffix(output, ".json") {
			gz = false
		} else if !strings.HasSuffix(output, ".json.gz") {
			if strings.HasSuffix(output, ".gz") {
				output = output[:len(output)-3] + ".json.gz"
			} else {
				output = output + ".json.gz"
			}
		}
		of, err := os.Create(output)
		if err != nil {
			exitOnError(err, exitCodeRuntimeError)
		}
		defer of.Close()
		jout = of
	}
	if gz {
		gzw := gzip.NewWriter(jout)
		defer func() {
			gzw.Flush()
			gzw.Close()
		}()
		jout = gzw
	}
	e := json.NewEncoder(jout)
	e.SetIndent("", "    ")
	return e.Encode(r)
}

type sconnHandler struct {
	raddr       *net.UDPAddr
	netprintp   *NetPrint
	quiet       bool
	reallyQuiet bool
}

func (sc *sconnHandler) OneWayOnReceived(seqno Seqno, owtd *OneWayTripData,
	powtd *OneWayTripData, late bool, dup bool) {
	if !sc.reallyQuiet {
		if dup {
			printf("DUP! seq=%d", seqno)
			return
		}

		if !sc.quiet {
			ipdv := "n/a"
			if powtd != nil {
				dv := owtd.SendIPDVSince(powtd)
				if dv != InvalidDuration {
					ipdv = rdur(AbsDuration(dv)).String()
				}
			}
			sd := ""
			rt := ""
			st := ""
			if owtd.SendDelay() != InvalidDuration {
				sd = fmt.Sprintf(" sd=%s", rdur(owtd.SendDelay()))
				st = fmt.Sprintf(" st=%d", owtd.Client.Send.Wall)
				rt = fmt.Sprintf(" rt=%d", owtd.Server.Receive.Wall)
			}
			sl := ""
			if late {
				sl = " (LATE)"
			}

			if sc.netprintp == nil {
				printf("[%s] seq=%d %s%s%s ipdv=%s%s", sc.raddr, seqno,
					sd, st, rt, ipdv, sl)
			} else {
				message := fmt.Sprintf("[%s] seq=%d %s%s%s ipdv=%s%s\n", sc.raddr, seqno,
					sd, st, rt, ipdv, sl)
				// Send the formatted string to the TCP socket
				err := sc.netprintp.WriteToConnection(message)
				if err != nil {
					fmt.Println("Error net printing result:", err)
					return
				}
			}
		}
	}
}

func (sc *sconnHandler) OnEvent(e *Event) {
	if !sc.reallyQuiet {
		printf("%s %s", sc.raddr, e)
	}
}
