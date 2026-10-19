package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var containerID = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

// Action starts, stops or restarts a container.
func (c *Client) Action(ctx context.Context, id, action string) error {
	if !containerID.MatchString(id) || (action != "start" && action != "stop" && action != "restart") {
		return errors.New("ungültige Aktion")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/containers/"+id+"/"+action, nil)
	// Stopping can take up to the container's grace period.
	client := *c.http
	client.Timeout = 60 * time.Second
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Docker nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 204 || resp.StatusCode == 304:
		return nil
	case resp.StatusCode == 403:
		return errors.New("Docker erlaubt keine Änderungen – bei einem Socket-Proxy POST=1 und CONTAINERS=1 setzen")
	}
	var m struct {
		Message string `json:"message"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&m)
	return fmt.Errorf("Docker: %s (%d)", m.Message, resp.StatusCode)
}

// imageDigests returns the registry digests a local image was pulled with.
func (c *Client) imageDigests(ctx context.Context, imageID string) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/images/"+imageID+"/json", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var img struct {
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&img); err != nil {
		return nil, err
	}
	var out []string
	for _, d := range img.RepoDigests {
		if _, dig, ok := strings.Cut(d, "@"); ok {
			out = append(out, dig)
		}
	}
	return out, nil
}

// ---- update check ----

// Updates compares the digest of each container's image with the one the
// registry currently has for the same tag. Results are cached for six hours;
// a check runs in the background so the dashboard never waits for registries.
type Updates struct {
	mu       sync.Mutex
	result   map[string]bool // image reference → newer version available
	checked  time.Time
	running  bool
	registry *http.Client
}

func NewUpdates() *Updates {
	return &Updates{result: map[string]bool{}, registry: &http.Client{Timeout: 15 * time.Second}}
}

const updateEvery = 6 * time.Hour

// Available reports the last known result and starts a new check if it is due.
func (u *Updates) Available(c *Client, cts []Container) map[string]bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.running && time.Since(u.checked) > updateEvery {
		u.running = true
		go u.check(c, cts)
	}
	out := make(map[string]bool, len(u.result))
	for k, v := range u.result {
		out[k] = v
	}
	return out
}

// Checked tells whether a check has finished at least once.
func (u *Updates) Checked() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return !u.checked.IsZero()
}

func (u *Updates) check(c *Client, cts []Container) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res := map[string]bool{}
	seen := map[string]bool{}
	sem := make(chan struct{}, 4)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ct := range cts {
		if seen[ct.Image] || strings.HasPrefix(ct.Image, "sha256:") || strings.Contains(ct.Image, "@") {
			continue // pinned to a digest or built locally
		}
		seen[ct.Image] = true
		wg.Add(1)
		go func(ct Container) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			local, err := c.imageDigests(ctx, ct.ImageID)
			if err != nil || len(local) == 0 {
				return
			}
			remote, err := u.remoteDigest(ctx, ct.Image)
			if err != nil || remote == "" {
				return
			}
			newer := true
			for _, d := range local {
				if d == remote {
					newer = false
				}
			}
			mu.Lock()
			res[ct.Image] = newer
			mu.Unlock()
		}(ct)
	}
	wg.Wait()
	u.mu.Lock()
	u.result, u.checked, u.running = res, time.Now(), false
	u.mu.Unlock()
}

// parseRef splits "lscr.io/linuxserver/sonarr:latest" into registry host,
// repository and tag, following Docker's defaults (Docker Hub, library/, latest).
func parseRef(ref string) (host, repo, tag string) {
	host, repo = "registry-1.docker.io", ref
	if i := strings.Index(ref, "/"); i > 0 && (strings.ContainsAny(ref[:i], ".:") || ref[:i] == "localhost") {
		host, repo = ref[:i], ref[i+1:]
	}
	if host == "docker.io" || host == "index.docker.io" {
		host = "registry-1.docker.io"
	}
	tag = "latest"
	if i := strings.LastIndex(repo, ":"); i > strings.LastIndex(repo, "/") {
		repo, tag = repo[:i], repo[i+1:]
	}
	if host == "registry-1.docker.io" && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return host, repo, tag
}

const manifestTypes = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json"

// remoteDigest asks the registry for the digest of a tag (HEAD, which does
// not count against Docker Hub's pull limit). Anonymous tokens are fetched
// from the realm the registry names in its 401 answer.
func (u *Updates) remoteDigest(ctx context.Context, ref string) (string, error) {
	host, repo, tag := parseRef(ref)
	target := "https://" + host + "/v2/" + repo + "/manifests/" + tag
	do := func(token string) (*http.Response, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, target, nil)
		req.Header.Set("Accept", manifestTypes)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return u.registry.Do(req)
	}
	resp, err := do("")
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	if resp.StatusCode == 401 {
		token, err := u.token(ctx, resp.Header.Get("WWW-Authenticate"))
		if err != nil {
			return "", err
		}
		if resp, err = do(token); err != nil {
			return "", err
		}
		resp.Body.Close()
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("registry status %d", resp.StatusCode)
	}
	return resp.Header.Get("Docker-Content-Digest"), nil
}

var challengeRe = regexp.MustCompile(`(\w+)="([^"]*)"`)

func (u *Updates) token(ctx context.Context, challenge string) (string, error) {
	if !strings.HasPrefix(strings.ToLower(challenge), "bearer ") {
		return "", errors.New("unsupported registry auth")
	}
	params := map[string]string{}
	for _, m := range challengeRe.FindAllStringSubmatch(challenge, -1) {
		params[m[1]] = m[2]
	}
	if !strings.HasPrefix(params["realm"], "https://") {
		return "", errors.New("unsupported registry auth")
	}
	q := url.Values{}
	for _, k := range []string{"service", "scope"} {
		if params[k] != "" {
			q.Set(k, params[k])
		}
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, params["realm"]+"?"+q.Encode(), nil)
	resp, err := u.registry.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var t struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return "", err
	}
	if t.Token == "" {
		t.Token = t.AccessToken
	}
	return t.Token, nil
}
