// Package docker lists running containers via the Docker socket so the
// dashboard can suggest tiles for them.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
)

type Client struct {
	http *http.Client
	base string
}

// New connects via DOCKER_HOST (unix:// or tcp://, e.g. a socket proxy) or
// the default socket. It returns nil if no Docker endpoint is available.
func New() *Client {
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	switch {
	case strings.HasPrefix(host, "unix://"):
		path := strings.TrimPrefix(host, "unix://")
		if _, err := os.Stat(path); err != nil {
			return nil
		}
		tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		}}
		return &Client{http: &http.Client{Transport: tr, Timeout: 5 * time.Second}, base: "http://docker"}
	case strings.HasPrefix(host, "tcp://"):
		return &Client{http: &http.Client{Timeout: 5 * time.Second}, base: "http://" + strings.TrimPrefix(host, "tcp://")}
	}
	return nil
}

type Container struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Image   string            `json:"image"`
	ImageID string            `json:"-"`
	State   string            `json:"state"`
	Status  string            `json:"status"`
	Ports   []int             `json:"ports"` // published host ports
	Labels  map[string]string `json:"-"`
}

// Suggestion is a ready-to-use tile proposal.
type Suggestion struct {
	Container   Container `json:"container"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Icon        string    `json:"icon"`
	Integration string    `json:"integration"`
	Group       string    `json:"group"`
	Source      string    `json:"source"` // docker | proxmox
}

func (c *Client) Containers(ctx context.Context) ([]Container, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.base+"/containers/json?all=1", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Docker nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Docker antwortet mit Status %d", resp.StatusCode)
	}
	var raw []struct {
		ID      string            `json:"Id"`
		Names   []string          `json:"Names"`
		Image   string            `json:"Image"`
		ImageID string            `json:"ImageID"`
		State   string            `json:"State"`
		Status  string            `json:"Status"`
		Labels  map[string]string `json:"Labels"`
		Ports   []struct {
			PrivatePort int    `json:"PrivatePort"`
			PublicPort  int    `json:"PublicPort"`
			Type        string `json:"Type"`
		} `json:"Ports"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Container, 0, len(raw))
	for _, r := range raw {
		ct := Container{ID: r.ID[:12], Image: r.Image, ImageID: r.ImageID, State: r.State, Status: r.Status, Labels: r.Labels}
		if len(r.Names) > 0 {
			ct.Name = strings.TrimPrefix(r.Names[0], "/")
		}
		seen := map[int]bool{}
		// Order ports so the container's usual web port comes first.
		in, _ := integrations.Match(r.Image)
		for _, p := range r.Ports {
			if p.PublicPort == 0 || p.Type != "tcp" || seen[p.PublicPort] {
				continue
			}
			seen[p.PublicPort] = true
			if in != nil && p.PrivatePort == in.Def().Port {
				ct.Ports = append([]int{p.PublicPort}, ct.Ports...)
			} else {
				ct.Ports = append(ct.Ports, p.PublicPort)
			}
		}
		out = append(out, ct)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Suggest builds tile proposals. host is the hostname the browser used to
// reach the dashboard, so published ports become clickable links.
func (c *Client) Suggest(ctx context.Context, host string) ([]Suggestion, error) {
	cts, err := c.Containers(ctx)
	if err != nil {
		return nil, err
	}
	out := []Suggestion{}
	for _, ct := range cts {
		if ct.Labels["heimdeck.hide"] == "true" || strings.Contains(strings.ToLower(ct.Image), "heimdeck") {
			continue
		}
		s := Suggestion{Container: ct, Name: titleCase(ct.Name), Integration: "link", Source: "docker"}
		if in, ok := integrations.Match(ct.Image); ok {
			d := in.Def()
			s.Integration, s.Icon, s.Group = d.ID, d.Icon, d.Category
			s.Name = strings.Split(d.Name, " /")[0]
		}
		if len(ct.Ports) > 0 {
			s.URL = fmt.Sprintf("http://%s:%d", host, ct.Ports[0])
		}
		// Labels on the container override everything.
		for k, dst := range map[string]*string{"name": &s.Name, "url": &s.URL, "icon": &s.Icon, "group": &s.Group, "type": &s.Integration} {
			if v := ct.Labels["heimdeck."+k]; v != "" {
				*dst = v
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func titleCase(s string) string {
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	words := strings.Fields(s)
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
