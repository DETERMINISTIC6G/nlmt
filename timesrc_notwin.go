// +build !windows

package nlmt

// NewDefaultTimeSource returns a WindowsTimeSource for Windows and GoTimeSource
// for everything else.
func NewDefaultTimeSource() TimeSource {
	return NewGoTimeSource()
}
