// +build windows nacl plan9

package nlmt

const syslogSupport = false

func newSyslogHandler(uriStr string) (Handler, error) {
	return nil, Errorf(SyslogNotSupported, "syslog not supported")
}
