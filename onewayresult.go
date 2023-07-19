package nlmt

import (
	"encoding/json"
	"math"
	"time"
)

// Result is returned from Run.
type OneWayResult struct {
	VersionInfo  *VersionInfo  `json:"version"`
	SystemInfo   *SystemInfo   `json:"system_info"`
	Config       *ServerConfig `json:"config"`
	SendErr      error         `json:"send_err,omitempty"`
	ReceiveErr   error         `json:"receive_err,omitempty"`
	*OneWayStats `json:"stats"`
	OneWayTrips  []OneWayTrip `json:"oneway_trips"`
	PacketLength uint         `json:"packet_length"`
}

func newOneWayResult(rec *OneWayRecorder, cfg *ServerConfig) *OneWayResult {
	stats := &OneWayStats{OneWayRecorder: rec}
	r := &OneWayResult{
		VersionInfo:  NewVersionInfo(),
		SystemInfo:   NewSystemInfo(),
		Config:       cfg,
		OneWayStats:  stats,
		PacketLength: rec.PacketLength,
	}

	// calculate total duration (monotonic time since start)
	r.Duration = cfg.TimeSource.Now(Monotonic).Sub(rec.Start)

	// create RoundTrips array
	r.OneWayTrips = make([]OneWayTrip, len(rec.OneWayTripData))
	for i := 0; i < len(r.OneWayTrips); i++ {
		rt := &r.OneWayTrips[i]
		rt.Seqno = Seqno(i)
		rt.OneWayTripData = &r.OneWayTripData[i]
		// calculate IPDV
		rt.SendIPDV = InvalidDuration
		if i > 0 {
			rtp := &r.OneWayTrips[i-1]
			if rt.Arrived() && rtp.Arrived() {
				rt.SendIPDV = rt.SendIPDVSince(rtp.OneWayTripData)
			}
		}
	}

	// do median calculations (could figure out a rolling median one day)
	r.visitStats(&r.SendDelayStats, false, func(rt *OneWayTrip) time.Duration {
		return rt.SendDelay()
	})

	// send IPDV
	r.visitStats(&r.SendIPDVStats, true, func(rt *OneWayTrip) time.Duration {
		return AbsDuration(rt.SendIPDV)
	})

	// set packets sent and received
	r.PacketsReceived = ReceivedCount(r.SendDelayStats.N + r.Duplicates)

	// calculate send rate
	if r.PacketsReceived > 0 {
		r.ReceiveRate = calculateBitrate(r.BytesReceived, r.LastReceived.Sub(r.FirstReceived))
	}

	// calculate expected number of packets
	if r.PacketsReceived > 0 {
		r.ExpectedPacketsSent = uint(math.Ceil(float64(r.Duration.Milliseconds()) / float64(r.Interval.Milliseconds())))
	}

	// calculate packet loss percent
	if r.SendDelayStats.N > 0 {
		r.PacketLossPercent = 100.0 * float64(uint32(r.lastSeqno)+1-uint32(r.SendDelayStats.N)) /
			(float64(r.lastSeqno) + 1)
	} else {
		r.PacketLossPercent = float64(100)
	}

	// calculate duplicate percent
	if r.PacketsReceived > 0 {
		r.DuplicatePercent = 100 * float64(r.Duplicates) / float64(r.PacketsReceived)
	}

	// calculate late packets percent
	if r.PacketsReceived > 0 {
		r.LatePacketsPercent = 100 * float64(r.LatePackets) / float64(r.PacketsReceived)
	}

	return r
}

// visitStats visits each OneWayTrip, optionally pushes to a DurationStats, and
// at the end, sets the median value on the DurationStats.
func (r *OneWayResult) visitStats(ds *DurationStats, push bool,
	fn func(*OneWayTrip) time.Duration) {
	fs := make([]float64, 0, len(r.OneWayTrips))
	for i := 0; i < len(r.OneWayTrips); i++ {
		d := fn(&r.OneWayTrips[i])
		if d != InvalidDuration {
			if push {
				ds.push(d)
			}
			fs = append(fs, float64(d))
		}
	}
	if len(fs) > 0 {
		ds.setMedian(median(fs))
	}
}

// OneWayTrip stores the Timestamps and statistics for a single oneway trip.
type OneWayTrip struct {
	Seqno           Seqno `json:"seqno"`
	*OneWayTripData `json:"timestamps"`
	SendIPDV        time.Duration `json:"-"`
}

// MarshalJSON implements the json.Marshaler interface.
func (rt *OneWayTrip) MarshalJSON() ([]byte, error) {
	type Alias OneWayTrip

	delay := make(map[string]interface{})
	if rt.SendDelay() != InvalidDuration {
		delay["send"] = rt.SendDelay()
	}

	ipdv := make(map[string]interface{})
	if rt.SendIPDV != InvalidDuration {
		ipdv["send"] = rt.SendIPDV
	}

	j := &struct {
		*Alias
		Delay map[string]interface{} `json:"delay"`
		IPDV  map[string]interface{} `json:"ipdv"`
	}{
		Alias: (*Alias)(rt),
		Delay: delay,
		IPDV:  ipdv,
	}
	return json.Marshal(j)
}

// Stats are the statistics in the Result.
type OneWayStats struct {
	*OneWayRecorder
	Duration           time.Duration `json:"duration"`
	PacketLossPercent  float64       `json:"packet_loss_percent"`
	DuplicatePercent   float64       `json:"duplicate_percent"`
	LatePacketsPercent float64       `json:"late_packets_percent"`
	SendIPDVStats      DurationStats `json:"ipdv_send"`
	ReceiveRate        Bitrate       `json:"receive_rate"`
}
