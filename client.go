package nlmt

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"runtime"
	"sync"
	"time"
)

// Client is the Client. It must be created with NewClient. It may not be used
// concurrently.
type Client struct {
	*ClientConfig
	conn    *cconn
	rec     *Recorder
	closed  bool
	closedM sync.Mutex
	initCh  chan (bool)
}

// NewClient returns a new client.
func NewClient(cfg *ClientConfig) *Client {
	// create client
	c := *cfg
	c.Supplied = cfg
	return &Client{
		ClientConfig: &c,
		initCh:       make(chan (bool)),
	}
}

// Run runs the test and returns the Result. An error is returned if the test
// could not be started. If an error occurs during the test, the error is nil,
// partial results are returned and either or both of the SendErr or
// ReceiveErr fields of Result will be non-nil. Run may only be called once.
func (c *Client) Run(ctx context.Context) (r *Result, err error) {
	// validate config
	if err = c.validate(); err != nil {
		return
	}

	// notify about connecting
	c.eventf(Connecting, "connecting to %s", c.RemoteAddress)

	// dial server
	if c.conn, err = dial(ctx, c.ClientConfig); err != nil {
		return
	}
	defer c.close()

	// check parameter changes
	if err = c.checkParameters(); err != nil {
		return
	}

	// notify about connection status
	if c.conn != nil {
		c.eventf(Connected, "connection established")
	} else {
		c.eventf(ConnectedClosed, "connection accepted and closed")
		return
	}

	// return if NoTest is set
	if c.ClientConfig.NoTest {
		err = nil
		c.eventf(NoTest, "skipping test at user request")
		return
	}

	// ignore server restrictions for testing
	if ignoreServerRestrictions {
		fmt.Println("Ignoring server restrictions!")
		c.Params = c.Supplied.Params
	}

	// return error if DSCP can't be used
	if c.DSCP != 0 && !c.conn.dscpSupport {
		err = Errorf(NoDSCPSupport, "unable to set DSCP value (%s)", c.conn.dscpError)
		return
	}

	// set DF value on socket
	if c.DF != DefaultDF {
		if derr := c.conn.setDF(c.DF); derr != nil {
			err = Errorf(DFError, "unable to set do not fragment bit (%s)", derr)
			return
		}
	}

	// set TTL
	if c.TTL != DefaultTTL {
		if terr := c.conn.setTTL(c.TTL); terr != nil {
			err = Errorf(TTLError, "unable to set TTL %d (%s)", c.TTL, terr)
			return
		}
	}

	// create recorder
	if c.rec, err = newRecorder(pcount(c.Duration, c.Interval), c.TimeSource,
		c.Handler); err != nil {
		return
	}

	// wait group for goroutine completion
	wg := sync.WaitGroup{}

	// start receive
	var rerr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer c.close()
		rerr = c.receive()
		if rerr != nil && c.isClosed() {
			rerr = nil
		}
	}()

	// start send
	var serr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer c.close()
		serr = c.send(ctx)
		if serr == nil {
			err = c.wait(ctx)
		}
		if serr != nil && c.isClosed() {
			serr = nil
		}
	}()

	// wait for send and receive to complete
	wg.Wait()

	r = newResult(c.rec, c.ClientConfig, c.conn.localAddr(), serr, rerr)

	return
}

func (c *Client) close() {
	c.closedM.Lock()
	defer c.closedM.Unlock()
	if !c.closed {
		if c.conn != nil {
			c.conn.close()
		}
		c.closed = true
	}
}

func (c *Client) isClosed() bool {
	c.closedM.Lock()
	defer c.closedM.Unlock()
	return c.closed
}

// localAddr returns the local address (non-nil after server dialed).
func (c *Client) localAddr() *net.UDPAddr {
	if c.conn == nil {
		return nil
	}
	return c.conn.localAddr()
}

// remoteAddr returns the remote address (non-nil after server dialed).
func (c *Client) remoteAddr() *net.UDPAddr {
	if c.conn == nil {
		return nil
	}
	return c.conn.remoteAddr()
}

// checkParameters checks any changes after the server returned restricted
// parameters.
func (c *Client) checkParameters() (err error) {
	paramEvent := func(code Code, format string, detail ...interface{}) {
		if c.Loose {
			c.eventf(code, format, detail...)
		} else {
			err = Errorf(code, format, detail...)
		}
	}

	if c.ProtocolVersion != ProtocolVersion {
		err = Errorf(ProtocolVersionMismatch,
			"client version %d != server version %d", ProtocolVersion, c.ProtocolVersion)
		return
	}
	if c.Duration < c.Supplied.Duration {
		paramEvent(ServerRestriction, "server reduced duration from %s to %s",
			c.Supplied.Duration, c.Duration)
		if err != nil {
			return
		}
	}
	if c.Duration > c.Supplied.Duration {
		err = Errorf(InvalidServerRestriction,
			"server tried to change duration from %s to %s",
			c.Supplied.Duration, c.Duration)
		return
	}
	if c.Interval > c.Supplied.Interval {
		paramEvent(ServerRestriction, "server increased interval from %s to %s",
			c.Supplied.Interval, c.Interval)
		if err != nil {
			return
		}
	}
	if c.Interval < c.Supplied.Interval {
		if c.Interval < minRestrictedInterval {
			err = Errorf(InvalidServerRestriction,
				"server tried to reduce interval to < %s, from %s to %s",
				minRestrictedInterval, c.Supplied.Interval, c.Interval)
			return
		}
		paramEvent(ServerRestriction,
			"server reduced interval from %s to %s to avoid %s timeout",
			c.Supplied.Interval, c.Interval, c.Interval*maxIntervalTimeoutFactor)
		if err != nil {
			return
		}
	}
	if c.Length < c.Supplied.Length {
		paramEvent(ServerRestriction, "server reduced length from %d to %d",
			c.Supplied.Length, c.Length)
		if err != nil {
			return
		}
	}
	if c.Length > c.Supplied.Length {
		err = Errorf(InvalidServerRestriction,
			"server tried to increase length from %d to %d",
			c.Supplied.Length, c.Length)
		return
	}
	if c.StampAt != c.Supplied.StampAt {
		paramEvent(ServerRestriction, "server restricted timestamps from %s to %s",
			c.Supplied.StampAt, c.StampAt)
		if err != nil {
			return
		}
	}
	if c.Clock != c.Supplied.Clock {
		paramEvent(ServerRestriction, "server restricted clocks from %s to %s",
			c.Supplied.Clock, c.Clock)
		if err != nil {
			return
		}
	}
	if c.DSCP != c.Supplied.DSCP {
		paramEvent(ServerRestriction, "server doesn't support DSCP")
		if err != nil {
			return
		}
	}
	if c.ServerFill != c.Supplied.ServerFill {
		paramEvent(ServerRestriction,
			"server restricted fill from %s to %s", c.Supplied.ServerFill,
			c.ServerFill)
		if err != nil {
			return
		}
	}
	return
}

// WaitForIntervalOffset waits until the time's remainder is IntervalOffset when divided by Interval.
func WaitForIntervalOffset(Interval time.Duration, IntervalOffset time.Duration, minWaitTime time.Duration) {
	// Get the current time
	currentTime := time.Now()

	// Calculate the remainder of the current time when divided by the Interval
	remainder := currentTime.UnixNano() % int64(Interval)

	// Calculate the time to wait
	var waitTime time.Duration
	if remainder > int64(IntervalOffset) {
		waitTime = Interval - time.Duration(remainder) + IntervalOffset
	} else {
		waitTime = IntervalOffset - time.Duration(remainder)
	}

	// Check if waitTime is smaller than minWaitTime and adjust
	for waitTime < minWaitTime {
		waitTime += Interval
	}

	// Wait for the calculated time
	fmt.Printf("Waiting for %s, time is now: %d\n", waitTime, time.Now().UnixNano())
	time.Sleep(waitTime)

	// The time should now be a multiple of Interval + IntervalOffset
	fmt.Println("Done waiting. The time is now:", time.Now().UnixNano())
}

// send sends all packets for the test to the server (called in goroutine from Run)
func (c *Client) send(ctx context.Context) error {
	defer func() {
		close(c.initCh)
	}()

	if c.ThreadLock {
		runtime.LockOSThread()
	}

	MULTIPLY := c.Multiply
	var p []*packet
	var seqno []Seqno

	for i := 0; i < MULTIPLY; i++ {
		// include 0 timestamp in appropriate fields
		seqno = append(seqno, Seqno(i))
		p = append(p, c.conn.newPacket())

		if c.conn.dscpSupport {
			p[i].dscp = c.DSCP
		}
		p[i].addFields(fechoRequest, true)
		p[i].zeroReceivedStats(c.ReceivedStats)
		if c.TripMode == TMRound {
			p[i].stampZeroes(c.StampAt, c.Clock)
		} else {
			mt := c.TimeSource.Now(c.Clock)
			p[i].setTimestamp(AtSend, Timestamp{Time{}, mt})
		}
		p[i].setSeqno(seqno[i])

		// set packet len
		c.Length = p[i].setLen(c.Length)

		// fill the first packet, if necessary
		if c.Filler != nil {
			err := p[i].readPayload(c.Filler)
			if err != nil {
				return err
			}
		} else {
			p[i].zeroPayload()
		}

		// lastly, set the HMAC
		p[i].updateHMAC()

	}

	// notify receive
	c.initCh <- true

	// record the start time of the test
	t := c.TimeSource.Now(BothClocks)
	c.rec.Start = t

	// sleep until the start of next next frame
	var err error
	framesource, err := NewFrameSource(c.FrameSourcePath)
	if err != nil {
		return err
	}
	framenow := framesource.Now()
	framenow, err = framesource.Sleep(framenow + 2)
	if err != nil {
		return err
	}

	// store the start frame of the test and calculate endframe
	framestart := framenow
	frameend := framestart + c.DurationFrames

	// keep sending until the duration frames has passed
	for {

		// wait for offset duration
		if c.IntervalOffset > 0 {
			time.Sleep(c.IntervalOffset)
		}

		for j := 0; j < MULTIPLY; j++ {

			// send to network and record times right before and after
			tsend := c.rec.recordPreSend()

			if clientDropsPercent == 0 || rand.Float32() > clientDropsPercent {
				if c.TripMode == TMOneWay {
					mt := c.TimeSource.Now(c.Clock)
					p[j].setTimestamp(AtSend, Timestamp{Time{}, mt})
				}
				err = c.conn.send(p[j])
			} else {
				// simulate drop with an average send time
				time.Sleep(20 * time.Microsecond)
			}

			// return on error
			if err != nil {
				c.rec.removeLastStamps()
				return err
			}

			// record send call
			c.rec.recordPostSend(tsend, p[j].tsent, uint64(p[j].length()))
		}

		// prepare next packets (before sleep, so the next send time is as
		// precise as possible)
		for k := 0; k < MULTIPLY; k++ {
			seqno[k] += Seqno(MULTIPLY)
			p[k].setSeqno(seqno[k])
			if c.Filler != nil && !c.FillOne {
				err := p[k].readPayload(c.Filler)
				if err != nil {
					return err
				}
			}
			p[k].updateHMAC()
		}

		// set the current base frame we're at
		f := framesource.Now()

		// break if framenow is after the end of the test
		if f > frameend {
			break
		}

		// sleep for intervalframes
		framenow, err = framesource.Sleep(f + c.IntervalFrames)
		if err != nil {
			return err
		}

		// record timer error
		diffms := c.FrameDurationMS * float32((framenow-f)-c.IntervalFrames)
		c.rec.recordTimerErr(time.Duration(diffms * 1000000))
	}

	framesource.Close()
	return nil
}

// receive receives packets from the server (called in goroutine from Run)
func (c *Client) receive() error {
	if c.ThreadLock {
		runtime.LockOSThread()
	}

	if _, ok := <-c.initCh; !ok {
		return Errorf(UnexpectedInitChannelClose, "init channel closed unexpectedly")
	}

	p := c.conn.newPacket()

	for {
		// read a packet
		err := c.conn.receive(p)
		if err != nil {
			return err
		}

		// drop packets with open flag set
		if p.flags()&flOpen != 0 {
			return Errorf(UnexpectedOpenFlag, "unexpected open flag set")
		}

		// add expected echo reply fields
		p.addFields(fechoReply, false)

		// return an error if reply packet was too small
		if p.length() < c.Length {
			return Errorf(ShortReply, "received short reply (%d bytes)",
				p.length())
		}

		// add expected received stats fields
		p.addReceivedStatsFields(c.ReceivedStats)

		// add expected timestamp fields
		p.addTimestampFields(c.StampAt, c.Clock)

		// get timestamps and return an error if the timestamp setting is
		// different (server doesn't support timestamps)
		at := p.stampAt()
		if at != c.StampAt {
			return Errorf(StampAtMismatch, "server stamped at %s, but %s was requested",
				at, c.StampAt)
		}
		if at != AtNone {
			cl := p.clock()
			if cl != c.Clock {
				return Errorf(ClockMismatch, "server clock %s, but %s was requested", cl, c.Clock)
			}
		}
		sts := p.timestamp()

		// record receive if all went well (may fail if seqno not found)
		ok := c.rec.recordReceive(p, &sts)
		if !ok {
			return Errorf(UnexpectedSequenceNumber, "unexpected reply sequence number %d", p.seqno())
		}
	}
}

// wait waits for final packets
func (c *Client) wait(ctx context.Context) (err error) {
	// return if all packets have been received
	c.rec.RLock()
	if c.rec.RTTStats.N >= c.rec.SendCallStats.N {
		c.rec.RUnlock()
		return
	}
	c.rec.RUnlock()

	// wait
	dwait := c.Waiter.Wait(c.rec)
	if dwait > 0 {
		c.rec.Wait = dwait
		c.eventf(WaitForPackets, "waiting %s for final packets", rdur(dwait))
		select {
		case <-time.After(dwait):
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	return
}

func (c *Client) eventf(code Code, format string, detail ...interface{}) {
	if c.Handler != nil {
		c.Handler.OnEvent(Eventf(code, c.localAddr(), c.remoteAddr(), format, detail...))
	}
}

// ClientHandler is called with client events, as well as separately when
// packets are sent and received. See the documentation for Recorder for
// information on locking for concurrent access.
type ClientHandler interface {
	Handler

	RecorderHandler
}
