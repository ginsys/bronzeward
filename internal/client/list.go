package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Page is one answer of a list route (client-contracts.md §2 pagination): its items, and the
// cursor of the next page, empty on the last.
type Page struct {
	Items []json.RawMessage `json:"items"`
	Next  string            `json:"next,omitempty"`
	Body  []byte            `json:"-"`
}

// Page reads the page of the list at path that cursor names, the first for "".
func (c *Client) Page(path, cursor string) (*Page, error) {
	if cursor != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + "cursor=" + url.QueryEscape(cursor)
	}
	r, err := c.Get(path)
	if err != nil {
		return nil, err
	}
	p := &Page{Body: r.Body}
	if err := json.Unmarshal(r.Body, p); err != nil {
		return nil, c.Context.fail(fmt.Errorf("GET %s: %w", path, err))
	}
	return p, nil
}

// List reads every page of the list at path and returns its items in order. A 400
// cursor-invalid discards the cursor and reads the list again from its start; Get has already
// handled the epoch change when the answer showed one (§8.2, §8.5). After three restarts the
// refusal is returned.
func (c *Client) List(path string) ([]json.RawMessage, error) {
	items, next := []json.RawMessage{}, ""
	for restarts := 0; ; {
		page, err := c.Page(path, next)
		var p *Problem
		if errors.As(err, &p) && p.Code() == "cursor-invalid" && next != "" && restarts < 3 {
			fmt.Fprintf(c.Notices, "%s: GET /api/v1%s: the cursor is no longer valid; reading the list again from its start\n", c.Context.Where(), path)
			items, next, restarts = []json.RawMessage{}, "", restarts+1
			continue
		}
		if err != nil {
			return nil, err
		}
		items = append(items, page.Items...)
		if next = page.Next; next == "" {
			return items, nil
		}
	}
}
