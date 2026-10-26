package client

import (
	"encoding/json"
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

// Page reads the page of the list at path that cursor names, the first for "". A refused cursor
// is discarded and the first page read instead (§8.2).
func (c *Client) Page(path, cursor string) (*Page, error) {
	p, err := c.page(path, cursor)
	if cursor != "" && c.restart(path, err) {
		return c.page(path, "")
	}
	return p, err
}

func (c *Client) page(path, cursor string) (*Page, error) {
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

// restart reports whether err is a 400 cursor-invalid and nothing else, and then says the list
// is read again. Get has already handled an epoch change the answer showed (§8.5); when its
// GET /server re-read failed, err joins that failure and the read fails instead.
func (c *Client) restart(path string, err error) bool {
	if p, ok := err.(*Problem); !ok || p.Code() != "cursor-invalid" {
		return false
	}
	fmt.Fprintf(c.Notices, "%s: GET /api/v1%s: the cursor is no longer valid; reading the list again from its start\n", c.Context.Where(), path)
	return true
}

// List reads every page of the list at path and returns its items in order, reading the list
// again from its start after a refused cursor. After three restarts the refusal is returned.
func (c *Client) List(path string) ([]json.RawMessage, error) {
	items, next := []json.RawMessage{}, ""
	for restarts := 0; ; {
		page, err := c.page(path, next)
		if next != "" && restarts < 3 && c.restart(path, err) {
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
