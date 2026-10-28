package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type kuttClient struct {
	base   string
	apiKey string
	http   *http.Client
}

type kuttLink struct {
	Address  string `json:"address"`
	Target   string `json:"target"`
	Banned   bool   `json:"banned"`
	ExpireIn string `json:"expire_in"`
}

type kuttLinksResponse struct {
	Data []kuttLink `json:"data"`
}

func (c *kuttClient) linkByAddress(ctx context.Context, address string) (*kuttLink, error) {
	u := c.base + "/api/v2/links?limit=50&search=" + url.QueryEscape(address)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kutt api: %s", resp.Status)
	}
	var body kuttLinksResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	for i := range body.Data {
		if body.Data[i].Address == address {
			return &body.Data[i], nil
		}
	}
	return nil, errors.New("link not found")
}

func linkExpiryUnix(expireIn string) (int64, error) {
	if expireIn == "" {
		return time.Now().Unix() + 14*86400, nil
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, expireIn); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, errors.New("bad expire_in")
}
