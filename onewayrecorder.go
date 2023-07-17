package nlmt

import (
	"sync"
	"time"
)

// Recorder is used to record data during the test. It is available to the
// Handler during the test for display of basic statistics, and may be used
// later to create a Result for further statistical analysis and storage.
// Recorder is accessed concurrently while the test is running, so its OWRLock and
// OWRUnlock methods must be used during read access to prevent race conditions.
// When RecorderHandler is called, it is already locked and must not be locked
// again. It is not possible to lock Recorder externally for write, since
// all recording should be done internally.
type OneWayRecorder struct {
	Start                 Time                  `json:"start_time"`
	FirstReceived         Time                  `json:"-"`
	LastReceived          Time                  `json:"-"`
	SendDelayStats        DurationStats         `json:"delay"`
	PacketsReceived       ReceivedCount         `json:"packets_received"`
	ExpectedPacketsSent   uint                  `json:"expected_packets_received"`
	Interval              time.Duration         `json:"interval"`
	BytesReceived         uint64                `json:"bytes_received"`
	Duplicates            uint                  `json:"duplicates"`
	LatePackets           uint                  `json:"late_packets"`
	PacketLength          uint                  `json:"packet_length"`
	Wait                  time.Duration         `json:"wait"`
	OneWayTripData        []OneWayTripData      `json:"-"`
	OneWayRecorderHandler OneWayRecorderHandler `json:"-"`
	lastSeqno             Seqno
	timeSource            TimeSource
	mtx                   sync.RWMutex
}

// OneWayTripData contains the information recorded for each round trip during
// the test.
type OneWayTripData struct {
	Client         Timestamp `json:"client"`
	Server         Timestamp `json:"server"`
	receivedWindow ReceivedWindow
	Ecn            int
}

// OWRLock locks the Recorder for reading.
func (r *Recorder) OWRLock() {
	r.mtx.RLock()
}

// OWRUnlock unlocks the Recorder for reading.
func (r *Recorder) OWRUnlock() {
	r.mtx.RUnlock()
}

func newOneWayRecorder(owtrips uint, pcktlen uint, interval time.Duration, ts TimeSource, h OneWayRecorderHandler) (rec *OneWayRecorder, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = Errorf(AllocateResultsPanic,
				"failed to allocate results buffer for %d oneway trips (%s)",
				owtrips, r)
		}
	}()
	rec = &OneWayRecorder{
		OneWayTripData:        make([]OneWayTripData, 0, owtrips),
		OneWayRecorderHandler: h,
		timeSource:            ts,
		PacketLength:          pcktlen,
		Interval:              interval,
		Start:                 ts.Now(Monotonic),
	}
	return
}

func (r *OneWayRecorder) recordReceive(p *packet, sts *Timestamp) bool {
	r.mtx.Lock()
	defer r.mtx.Unlock()

	// get the sequence number
	seqno := p.seqno()

	// make OneWayTripData
	for len(r.OneWayTripData) <= int(seqno)+1 {
		r.OneWayTripData = append(r.OneWayTripData, OneWayTripData{})
	}

	// valid result
	owtd := &r.OneWayTripData[seqno]
	var powtd *OneWayTripData
	if seqno > 0 {
		powtd = &r.OneWayTripData[seqno-1]
	}

	// check for lateness
	late := seqno < r.lastSeqno

	// Transfer ECN from packet so it will be dumped
	owtd.Ecn = p.ecn

	// check for duplicate (don't update stats for duplicates)
	if !owtd.Server.Receive.IsZero() {
		r.Duplicates++

		// remove the appended onewaytripdata
		r.OneWayTripData = r.OneWayTripData[:len(r.OneWayTripData)-1]

		// call recorder handler
		if r.OneWayRecorderHandler != nil {
			r.OneWayRecorderHandler.OneWayOnReceived(p.seqno(), owtd, powtd, late, true)
		}
		return true
	}

	// record late packet
	if late {
		r.LatePackets++
	}
	r.lastSeqno = seqno

	// update times
	owtd.Client.Send = sts.Send
	owtd.Server.Receive = p.trcvd

	// update one-way delay stats
	if !owtd.Server.BestReceive().IsWallZero() {
		r.SendDelayStats.push(owtd.SendDelay())
	}

	// set received times
	if r.FirstReceived.IsZero() {
		r.FirstReceived = p.trcvd
	}
	r.LastReceived = p.trcvd

	// set received window
	if p.hasReceivedWindow() {
		owtd.receivedWindow = p.receivedWindow()
	}

	// update bytes received
	r.BytesReceived += uint64(p.length())

	// call recorder handler
	if r.OneWayRecorderHandler != nil {
		r.OneWayRecorderHandler.OneWayOnReceived(p.seqno(), owtd, powtd, late, false)
	}

	return true
}

// Arrived returns true if a packet was received from the sender.
func (ts *OneWayTripData) Arrived() bool {
	return !ts.Server.Receive.IsZero()
}

// SendIPDVSince returns the send instantaneous packet delay variation since the
// specified RoundTripData.
func (ts *OneWayTripData) SendIPDVSince(pts *OneWayTripData) (d time.Duration) {
	d = InvalidDuration
	if ts.IsTimestamped() && pts.IsTimestamped() {
		if ts.IsMonoTimestamped() && pts.IsMonoTimestamped() {
			d = ts.SendMonoDiff() - pts.SendMonoDiff()
		} else if ts.IsWallTimestamped() && pts.IsWallTimestamped() {
			d = ts.SendWallDiff() - pts.SendWallDiff()
		}
	}
	return
}

// SendDelay returns the estimated one-way send delay, valid only if wall clock timestamps
// are available and the server's system time has been externally synchronized.
func (ts *OneWayTripData) SendDelay() time.Duration {
	if !ts.IsWallTimestamped() {
		return InvalidDuration
	}
	return time.Duration(ts.Server.BestReceive().Wall - ts.Client.Send.Wall)
}

// SendMonoDiff returns the difference in send values from the monotonic clock.
// This is useful for measuring send IPDV (jitter), but not for absolute send delay.
func (ts *OneWayTripData) SendMonoDiff() time.Duration {
	return ts.Server.BestReceive().Mono - ts.Client.Send.Mono
}

// SendWallDiff returns the difference in send values from the wall
// clock. This is useful for measuring receive IPDV (jitter), but not for
// absolute send delay. Because the wall clock is used, it is subject to wall
// clock variability.
func (ts *OneWayTripData) SendWallDiff() time.Duration {
	return time.Duration(ts.Server.BestReceive().Wall - ts.Client.Send.Wall)
}

// IsTimestamped returns true if the server returned any timestamp.
func (ts *OneWayTripData) IsTimestamped() bool {
	return ts.IsReceiveTimestamped() || ts.IsSendTimestamped()
}

// IsMonoTimestamped returns true if the server returned any timestamp with a
// valid monotonic clock value.
func (ts *OneWayTripData) IsMonoTimestamped() bool {
	return !ts.Server.Receive.IsMonoZero() || !ts.Client.Send.IsMonoZero()
}

// IsWallTimestamped returns true if the server returned any timestamp with a
// valid wall clock value.
func (ts *OneWayTripData) IsWallTimestamped() bool {
	return !ts.Server.Receive.IsWallZero() || !ts.Client.Send.IsWallZero()
}

// IsReceiveTimestamped returns true if the server returned a receive timestamp.
func (ts *OneWayTripData) IsReceiveTimestamped() bool {
	return !ts.Server.Receive.IsZero()
}

// IsSendTimestamped returns true if the server returned a send timestamp.
func (ts *OneWayTripData) IsSendTimestamped() bool {
	return !ts.Client.Send.IsZero()
}

// IsBothTimestamped returns true if the server returned both a send and receive
// timestamp.
func (ts *OneWayTripData) IsBothTimestamped() bool {
	return ts.IsReceiveTimestamped() && ts.IsSendTimestamped()
}

// RecorderHandler is called when the Recorder records a sent or received
// packet.
type OneWayRecorderHandler interface {
	// OnReceived is called when a packet is received.
	OneWayOnReceived(seqno Seqno, owtd *OneWayTripData, pred *OneWayTripData, late bool, dup bool)
}
