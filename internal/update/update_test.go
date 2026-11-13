package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"1.0.10", "1.0.9", true},
		{"1.1.0", "1.0.99", true},
		{"1.0.3", "1.0.3", false},
		{"1.0.2", "1.0.3", false},
		{"v2.0.0", "1.9.9", true},
		{"1.0.3", "dev", false},
		{"garbage", "1.0.0", false},
	}
	for _, c := range cases {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name":"v1.0.7","html_url":"https://example.test/rel","assets":[
			{"name":"sms-cleaner-1.0.7-mapping.txt","browser_download_url":"https://example.test/m"},
			{"name":"sms-cleaner-1.0.7.apk","browser_download_url":"https://example.test/apk"}]}`))
	}))
	defer srv.Close()
	c := &Checker{Repo: "o/r", Current: "1.0.3", API: srv.URL}
	info := c.fetch(context.Background())
	if info.Error != "" || info.Latest != "1.0.7" || !info.Newer || info.Page != "https://example.test/rel" {
		t.Fatalf("unexpected %+v", info)
	}
	// Development builds never ask.
	dev := (&Checker{Repo: "o/r", Current: "dev", API: srv.URL}).Status()
	if dev.Newer || dev.Checked != 0 {
		t.Fatalf("dev build checked for updates: %+v", dev)
	}
}
