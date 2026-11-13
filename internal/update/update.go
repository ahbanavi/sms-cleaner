// Package update checks GitHub for a newer release of sms-cleaner.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info is what the UI shows about updates.
type Info struct {
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	Newer   bool   `json:"newer"`
	// Download is the APK on Android and the release page elsewhere.
	Download string `json:"download,omitempty"`
	Page     string `json:"page,omitempty"`
	Checked  int64  `json:"checked,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Checker asks GitHub for the latest release at most once per Interval.
// Development builds (a version that isn't x.y.z) never report an update.
type Checker struct {
	Repo     string // owner/name
	Current  string
	Client   *http.Client
	Interval time.Duration
	// API is the GitHub API base URL; tests point it elsewhere.
	API string

	mu      sync.Mutex
	info    Info
	running bool
}

// Status returns the last result without waiting, starting a refresh in the
// background when it is older than Interval.
func (c *Checker) Status() Info {
	c.mu.Lock()
	defer c.mu.Unlock()
	info := c.info
	info.Current = c.Current
	if parse(c.Current) == nil {
		return info
	}
	interval := c.Interval
	if interval == 0 {
		interval = 6 * time.Hour
	}
	if !c.running && time.Since(time.Unix(c.info.Checked, 0)) > interval {
		c.running = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			fresh := c.fetch(ctx)
			c.mu.Lock()
			c.info, c.running = fresh, false
			c.mu.Unlock()
		}()
	}
	return info
}

func (c *Checker) fetch(ctx context.Context) Info {
	info := Info{Current: c.Current, Checked: time.Now().Unix()}
	api := c.API
	if api == "" {
		api = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+c.Repo+"/releases/latest", nil)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sms-cleaner/"+c.Current)
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		info.Error = err.Error()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		info.Error = "GitHub answered " + resp.Status
		return info
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Page   string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		info.Error = "reading the release: " + err.Error()
		return info
	}
	info.Latest = strings.TrimPrefix(rel.Tag, "v")
	info.Page = rel.Page
	info.Newer = newer(info.Latest, c.Current)
	info.Download = rel.Page
	if runtime.GOOS == "android" {
		for _, a := range rel.Assets {
			// A release without the signing key carries "-debug-key" and can't update this install.
			if strings.HasSuffix(a.Name, ".apk") && !strings.Contains(a.Name, "-debug-key") {
				info.Download = a.URL
			}
		}
	}
	return info
}

// parse reads "1.2.3" (or "v1.2.3"); nil for anything else.
func parse(v string) []int {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

func newer(latest, current string) bool {
	a, b := parse(latest), parse(current)
	if a == nil || b == nil {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// String is a short description for logs.
func (i Info) String() string {
	return fmt.Sprintf("current=%s latest=%s newer=%v", i.Current, i.Latest, i.Newer)
}
