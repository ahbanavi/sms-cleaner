package gm

import "testing"

func TestParseCookies(t *testing.T) {
	want := map[string]string{"SID": "s1", "HSID": "h1", "SSID": "x1", "APISID": "a1", "SAPISID": "sa1"}
	inputs := map[string]string{
		"bash curl": `curl 'https://messages.google.com/web/config?c=1' -H 'accept: */*' -b 'NID=9; SID=s1; HSID=h1; SSID=x1; APISID=a1; SAPISID=sa1; OSID=o1' -H 'x: y'`,
		"header":    `Cookie: SID=s1; HSID=h1; SSID=x1; APISID=a1; SAPISID=sa1`,
		"json":      `{"SID":"s1","HSID":"h1","SSID":"x1","APISID":"a1","SAPISID":"sa1","junk":"z"}`,
		"array":     `[{"name":"SID","value":"s1","domain":".google.com"},{"name":"HSID","value":"h1","domain":".google.com"},{"name":"SSID","value":"x1","domain":".google.com"},{"name":"APISID","value":"a1","domain":".google.com"},{"name":"SAPISID","value":"sa1","domain":".google.com"}]`,
		"cmd curl":  `curl ^"https://messages.google.com/web/config^" -b ^"SID=s1; HSID=h1; SSID=x1; APISID=a1; SAPISID=sa1^"`,
		"curl -H":   `curl 'https://messages.google.com/web/config' -H 'cookie: SID=s1; HSID=h1; SSID=x1; APISID=a1; SAPISID=sa1'`,
	}
	for name, in := range inputs {
		got, err := ParseCookies(in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", name, k, got[k], v)
			}
		}
		if _, ok := got["NID"]; ok {
			t.Errorf("%s: kept unrelated cookie", name)
		}
	}
	if _, err := ParseCookies(`SID=1; HSID=2`); err == nil {
		t.Error("expected missing-cookie error")
	}
}
