package ydbdriver

import (
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// New must yield a value that satisfies the neutral contract for every
// transport — this is the seam m-cli's VistaEngine holds.
func TestNew_SatisfiesTransport(t *testing.T) {
	for _, tr := range []string{
		mdriver.TransportLocal, mdriver.TransportDocker, mdriver.TransportRemote,
	} {
		var got mdriver.Transport = New(Config{Transport: tr})
		if got == nil {
			t.Errorf("New(%q) = nil, want a Transport", tr)
		}
	}
}
