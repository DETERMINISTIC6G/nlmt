package nlmt

// Common error codes.
const (
	ShortWrite Code = -1 * (iota + 1)
	InvalidDFString
	FieldsLengthTooLarge
	FieldsCapacityTooLarge
	InvalidStampAtString
	InvalidStampAtInt
	InvalidAllowStampString
	InvalidClockString
	InvalidClockInt
	BadMagic
	NoHMAC
	BadHMAC
	UnexpectedHMAC
	NonexclusiveMidpointTStamp
	InconsistentClocks
	DFNotSupported
	InvalidFlagBitsSet
	ShortParamBuffer
	ParamOverflow
	InvalidParamValue
	ProtocolVersionMismatch
)

// Server error codes.
const (
	NoMatchingInterfaces Code = -1 * (iota + 1*1024)
	NoMatchingInterfacesUp
	UnspecifiedWithSpecifiedAddresses
	UnexpectedReplyFlag
	NoSuitableAddressFound
	InvalidConnToken
	ShortInterval
	LargeRequest
	AddressMismatch
	SyslogNotSupported
	InvalidSyslogURI
)

// Client error codes.
const (
	InvalidWinAvgWindow Code = -1 * (iota + 2*1024)
	InvalidExpAvgAlpha
	AllocateResultsPanic
	UnexpectedOpenFlag
	DFError
	TTLError
	ExpectedReplyFlag
	ShortReply
	StampAtMismatch
	ClockMismatch
	UnexpectedSequenceNumber
	InvalidSleepFactor
	InvalidWaitString
	InvalidWaitFactor
	InvalidWaitDuration
	NoSuchAverager
	NoSuchFiller
	NoSuchTimer
	NoSuchTimeSource
	NoSuchWaiter
	IntervalNonPositive
	DurationNonPositive
	ConnTokenZero
	ServerClosed
	OpenTimeout
	InvalidServerRestriction
	InvalidReceivedStatsInt
	InvalidReceivedStatsString
	OpenTimeoutTooShort
	ServerFillTooLong
	UnexpectedInitChannelClose
)

// Error is an IRTT error.
type Error struct {
	*Event
}

// Errorf returns a new Error.
func Errorf(code Code, format string, detail ...interface{}) *Error {
	return &Error{Eventf(code, nil, nil, format, detail...)}
}

func (e *Error) Error() string {
	return e.Event.String()
}

func isErrorCode(code Code, err error) (matches bool) {
	if e, ok := err.(*Error); ok {
		matches = (e.Code == code)
	}
	return
}
