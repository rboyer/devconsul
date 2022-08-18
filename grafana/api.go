package grafana

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/go-hclog"
)

const baseURL = "http://localhost:3000"

type Client struct {
	logger hclog.Logger
	client *http.Client
}

func NewClient(logger hclog.Logger) (*Client, error) {
	return &Client{
		logger: logger,
		client: cleanhttp.DefaultClient(),
	}, nil
}

type DashboardStub struct {
	ID        int      `json:"id"`
	UID       string   `json:"uid"`
	Title     string   `json:"title"`
	URI       string   `json:"uri"`
	URL       string   `json:"url"`
	Slug      string   `json:"slug"`
	Type      string   `json:"type"`
	Tags      []string `json:"tags"`
	IsStarred bool     `json:"isStarred"`
}

func (c *Client) ListDashboards() ([]*DashboardStub, error) {
	grafanaURL := baseURL + "/api/search?query=%"

	resp, err := c.client.Get(grafanaURL)
	if err != nil {
		return nil, err
	}
	if resp.Body == nil {
		return nil, fmt.Errorf("body not populated")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)

	var list []*DashboardStub
	if err := dec.Decode(&list); err != nil {
		return nil, err
	}

	var out []*DashboardStub
	for _, item := range list {
		if item.Type != "dash-db" {
			continue
		}

		name := strings.TrimPrefix(item.URI, "db/")
		if name == item.URI {
			c.logger.Warn("skipping grafana board with strange URI", "uid", item.UID, "title", item.Title, "uri", item.URI)
			continue
		}

		out = append(out, item)
	}

	return out, nil
}

func (c *Client) GetRawDashboard(uid string) (map[string]any, error) {
	dashURI := baseURL + "/api/dashboards/uid/" + uid

	resp, err := c.client.Get(dashURI)
	if err != nil {
		return nil, err
	}
	if resp.Body == nil {
		return nil, fmt.Errorf("body not populated")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)

	var raw struct {
		Dashboard map[string]any `json:"dashboard"`
	}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}

	return raw.Dashboard, nil
}
