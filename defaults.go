package nlmt

import (
	"time"
)

// Common defaults.
const (
	DefaultIPVersion  = DualStack
	DefaultPort       = "2112"
	DefaultPortInt    = 2112
	DefaultTTL        = 0
	DefaultThreadLock = false
	DefaultOutputDir  = "."
	DefaultGroup      = "default"
)

// Client defaults.
const (
	DefaultDuration                = 1 * time.Minute
	DefaultInterval                = 100 * time.Millisecond
	DefaultIntervalOffset          = -1 * time.Second
	MinWaitingTime                 = 5 * time.Millisecond
	DefaultLength                  = 0
	DefaultReceivedStats           = ReceivedStatsBoth
	DefaultStampAt                 = AtBoth
	DefaultTripMode                = TMRound
	DefaultMultiply                = 1
	DefaultClock                   = BothClocks
	DefaultDSCP                    = 0
	DefaultLoose                   = false
	DefaultLocalAddress            = ":0"
	DefaultLocalPort               = "0"
	DefaultDF                      = DFDefault
	DefaultCompTimerMinErrorFactor = 0.0
	DefaultCompTimerMaxErrorFactor = 2.0
	DefaultHybridTimerSleepFactor  = 0.95
	DefaultAverageWindow           = 5
	DefaultExponentialAverageAlpha = 0.1
)

// DefaultOpenTimeouts are the default timeouts used when the client opens a
// connection to the server.
var DefaultOpenTimeouts = Durations([]time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
})

// DefaultCompTimerAverage is the default timer error averaging algorithm for
// the CompTimer.
var DefaultCompTimerAverage = NewDefaultExponentialAverager()

// DefaultWait is the default client wait time for the final responses after all
// packets have been sent.
var DefaultWait = &WaitMaxRTT{time.Duration(4) * time.Second, 3}

// DefaultTimer is the default timer implementation, CompTimer.
var DefaultTimer = NewCompTimer(DefaultCompTimerAverage)

// DefaultTimeSource is the default TimeSource implementation (WindowsTimeSource
// for Windows and GoTimeSource for everything else).
var DefaultTimeSource = NewDefaultTimeSource()

// client's shared memory clock settings
var DefaultFrameSourcePath = ""
var DefaultFrameSource *FrameSource = nil
var DefaultFrameDuration = 10 * time.Millisecond
var DefaultIntervalFrames = 1
var DefaultDurationFrames = 2000

// output file for each connection
var DefaultOutputJSON = false
var DefaultOutputJSONAddr = ""

// q is the type (client or server) x is the ip and port, y: year, m: month, d: day, h: hour, t: minute, w:second
var DefaultJSONAddrFormat = "q_x_ymd_htw.json.gz"

// DefaultFillPattern is the default fill pattern.
var DefaultFillPattern = []byte("irtt")

// DefaultServerFiller it the default filler for the server, PatternFiller.
var DefaultServerFiller = NewDefaultPatternFiller()

// Server defaults.
const (
	DefaultMaxDuration   = time.Duration(0)
	DefaultMinInterval   = 10 * time.Millisecond
	DefaultMaxLength     = 0
	DefaultServerTimeout = 1 * time.Minute
	DefaultPacketBurst   = 5
	DefaultAllowStamp    = DualStamps
	DefaultAllowDSCP     = true
	DefaultSetSrcIP      = false
	DefaultSetECN        = false
)

// DefaultBindAddrs are the default bind addresses.
var DefaultBindAddrs = []string{":2112"}

// DefaultAllowFills are the default allowed fill prefixes.
var DefaultAllowFills = []string{"rand"}

// server duplicates and drops for testing (0.0-1.0)
const serverDupsPercent = 0
const serverDropsPercent = 0

// grace period for connection closure due to timeout
const timeoutGrace = 5 * time.Second

// factor of timeout used for maximum interval
const maxIntervalTimeoutFactor = 4

// max test duration grace period
const maxDurationGrace = 2 * time.Second

// ignore server restrictions (for testing hard limits)
const ignoreServerRestrictions = false

// settings for testing
const clientDropsPercent = 0

// minOpenTimeout sets the minimum time open() will wait before sending the
// next packet. This prevents clients from requesting a timeout that sends
// packets to the server too quickly.
const minOpenTimeout = 200 * time.Millisecond

// maximum initial length of pattern filler buffer
const patternMaxInitLen = 4 * 1024

// maxMTU is the MTU used if it could not be determined by autodetection.
const maxMTU = 64 * 1024

// minimum valid MTU per RFC 791
const minValidMTU = 68

// number of sconns to check to remove on each add (2 seems to be the least
// aggresive number where the map size still levels off over time, but I use 5
// to clean up unused sconns more quickly)
const checkExpiredCount = 5

// initial capacity for sconns map
const sconnsInitSize = 32

// maximum length of server fill string
const maxServerFillLen = 32

// length of group string
const maxGroupLen = 24

// minRestrictedInterval is the minimum restricted interval that the client will
// accept from the server.
const minRestrictedInterval = 1 * time.Second
