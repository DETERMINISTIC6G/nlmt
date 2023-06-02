// +build !profile

package nlmt

const profileEnabled = false

func startProfile(path string) interface {
	Stop()
} {
	return nil
}
