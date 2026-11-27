package launch

import (
	"context"
	"net"
	"strings"
	"testing"
)

func TestProbe(t *testing.T) {
	rs := newRemoteServer(t)
	u := rs.url(t)
	ctx := context.Background()

	r := Probe(ctx, TargetGetter, u+"/", 1)
	if !r.Reachable || r.Error != "" || r.APIVersion != 1 || r.Identity != "stage" || r.Version != "1.3.0" {
		t.Fatalf("ok %+v", r)
	}
	if r := Probe(ctx, TargetGetter, u, 2); !r.Reachable || !strings.Contains(r.Error, "needs v2") || r.Hint == "" {
		t.Fatalf("api mismatch %+v", r)
	}
	rs.code.Store(403)
	r = Probe(ctx, TargetGetter, u, 1)
	if !r.Reachable || r.Command != "STAGE_ALLOWED_ORIGINS="+u || !strings.Contains(r.Error, "not allowed") || !strings.Contains(r.Hint, "STAGE_ALLOWED_ORIGINS") {
		t.Fatalf("refused %+v", r)
	}
	rs.code.Store(500)
	if r := Probe(ctx, TargetGetter, u, 1); !r.Reachable || r.Error == "" {
		t.Fatalf("500 %+v", r)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	_ = ln.Close()
	if r := Probe(ctx, TargetGetter, dead, 1); r.Reachable || r.Error == "" || !strings.Contains(r.Hint, "HTTPS proxy") || r.Command != "" {
		t.Fatalf("dead %+v", r)
	}
	if r := Probe(ctx, TargetGetter, "box.lan:7380", 1); r.Reachable || !strings.Contains(r.Error, "scheme") {
		t.Fatalf("invalid %+v", r)
	}
}
