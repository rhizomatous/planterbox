package main

import "testing"

func TestForwardsParsing(t *testing.T) {
	for _, tc := range []struct {
		in                 string
		wantListen, wantUp string
		wantErr            bool
	}{
		{in: ":3000=plbx-demo:3000", wantListen: ":3000", wantUp: "plbx-demo:3000"},
		{in: "127.0.0.1:80=plbx-demo:8080", wantListen: "127.0.0.1:80", wantUp: "plbx-demo:8080"},
		// an address with no upstream would listen and carry nothing, and an
		// upstream with no address would listen on everything.
		{in: ":3000", wantErr: true},
		{in: ":3000=", wantErr: true},
		{in: "=plbx-demo:3000", wantErr: true},
		{in: "", wantErr: true},
	} {
		t.Run(tc.in, func(t *testing.T) {
			var f forwards
			err := f.Set(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Set(%q) = nil, want an error", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tc.in, err)
			}
			if len(f) != 1 || f[0].listen != tc.wantListen || f[0].upstream != tc.wantUp {
				t.Errorf("Set(%q) = %+v, want %s -> %s", tc.in, f, tc.wantListen, tc.wantUp)
			}
		})
	}
}

// Several forwards accumulate, because a sandbox publishes more than one port.
func TestForwardsAccumulate(t *testing.T) {
	var f forwards
	for _, in := range []string{":80=a:80", ":443=b:443"} {
		if err := f.Set(in); err != nil {
			t.Fatalf("Set(%q): %v", in, err)
		}
	}
	if len(f) != 2 {
		t.Fatalf("len = %d, want 2", len(f))
	}
	if got := f.String(); got != ":80=a:80,:443=b:443" {
		t.Errorf("String() = %q, want both forwards", got)
	}
}
