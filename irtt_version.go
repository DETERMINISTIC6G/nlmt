package nlmt

import "runtime"

func runVersion(args []string) {
	printf("irttf version: %s", Version)
	printf("protocol version: %d", ProtocolVersion)
	printf("json format version: %d", JSONFormatVersion)
	printf("go version: %s on %s/%s", runtime.Version(),
		runtime.GOOS, runtime.GOARCH)
}
