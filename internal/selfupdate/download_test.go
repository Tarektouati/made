package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestClientGetResponse(t *testing.T) {
	interrupted := errors.New("response interrupted")
	for _, c := range []struct {
		name, want, wantErr string
		body                io.Reader
		status              int
		limit               int64
	}{
		{name: "empty", body: strings.NewReader(""), status: http.StatusOK},
		{name: "exact limit", body: strings.NewReader("data"), status: http.StatusOK, limit: 4, want: "data"},
		{name: "over limit", body: strings.NewReader("data!"), status: http.StatusOK, limit: 4, wantErr: "larger than 4 bytes"},
		{name: "interrupted body", body: io.MultiReader(strings.NewReader("part"), iotest.ErrReader(interrupted)), status: http.StatusOK, limit: 20, wantErr: interrupted.Error()},
		{name: "HTTP error", body: strings.NewReader("denied"), status: http.StatusForbidden, limit: 20, wantErr: "403 Forbidden"},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := &trackedBody{Reader: c.body}
			client := Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || r.URL.String() != "https://releases.invalid/asset" {
					t.Errorf("request = %s %s, want GET https://releases.invalid/asset", r.Method, r.URL)
				}
				return &http.Response{StatusCode: c.status, Status: fmt.Sprintf("%d %s", c.status, http.StatusText(c.status)), Body: body}, nil
			})}}
			got, err := client.get(context.Background(), "https://releases.invalid/asset", c.limit)
			if c.wantErr == "" {
				if err != nil || string(got) != c.want {
					t.Errorf("get = (%q, %v), want (%q, nil)", got, err, c.want)
				}
			} else if err == nil || !strings.Contains(err.Error(), c.wantErr) || got != nil {
				t.Errorf("get = (%q, %v), want nil body and %q", got, err, c.wantErr)
			}
			if c.name == "interrupted body" && !errors.Is(err, interrupted) {
				t.Errorf("get error = %v, want the original read error", err)
			}
			if !body.closed {
				t.Error("response body was not closed")
			}
		})
	}
}

func TestClientGetRequestErrors(t *testing.T) {
	transportErr := errors.New("transport failed")
	for _, c := range []struct {
		name, url string
		calls     int
	}{
		{name: "invalid URL", url: "://invalid", calls: 0},
		{name: "transport", url: "https://releases.invalid/asset", calls: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			client := Client{HTTP: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, transportErr
			})}}
			got, err := client.get(context.Background(), c.url, 4)
			if err == nil || got != nil {
				t.Errorf("get = (%q, %v), want nil body and an error", got, err)
			}
			if calls != c.calls {
				t.Errorf("transport called %d times, want %d", calls, c.calls)
			}
			if c.calls != 0 && !errors.Is(err, transportErr) {
				t.Errorf("get error = %v, want the original transport error", err)
			}
		})
	}
}

func TestClientGetRedirects(t *testing.T) {
	for _, c := range []struct {
		name, location, wantErr string
		calls                   int
	}{
		{name: "HTTPS", location: "https://releases.invalid/next", calls: 2},
		{name: "downgrade", location: "http://releases.invalid/next", calls: 1, wantErr: "refusing redirect from https"},
		{name: "loop", location: "https://releases.invalid/asset", calls: 10, wantErr: "stopped after 10 redirects"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var bodies []*trackedBody
			client := Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := &trackedBody{Reader: strings.NewReader("data")}
				bodies = append(bodies, body)
				if c.wantErr == "" && r.URL.Path == "/next" {
					return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
				}
				return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {c.location}}, Body: body}, nil
			})}}
			got, err := client.get(context.Background(), "https://releases.invalid/asset", 4)
			if c.wantErr == "" {
				if err != nil || string(got) != "data" {
					t.Errorf("get = (%q, %v), want (data, nil)", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), c.wantErr) || got != nil {
				t.Errorf("get = (%q, %v), want nil body and %q", got, err, c.wantErr)
			}
			if len(bodies) != c.calls {
				t.Errorf("transport called %d times, want %d", len(bodies), c.calls)
			}
			for i, body := range bodies {
				if !body.closed {
					t.Errorf("response body %d was not closed", i)
				}
			}
		})
	}
}

func TestLatestTagRequestErrors(t *testing.T) {
	transportErr := errors.New("transport failed")
	for _, c := range []struct {
		name, base string
		calls      int
	}{
		{name: "invalid URL", base: "://invalid", calls: 0},
		{name: "default host transport error", calls: 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := 0
			client := Client{BaseURL: c.base, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if want := DefaultBaseURL + "/" + Repo + "/releases/latest"; r.URL.String() != want {
					t.Errorf("request URL = %s, want %s", r.URL, want)
				}
				return nil, transportErr
			})}}
			got, err := client.LatestTag(context.Background())
			if err == nil || got != "" {
				t.Errorf("LatestTag = (%q, %v), want an empty tag and an error", got, err)
			}
			if calls != c.calls {
				t.Errorf("transport called %d times, want %d", calls, c.calls)
			}
			if c.calls != 0 && !errors.Is(err, transportErr) {
				t.Errorf("LatestTag error = %v, want the original transport error", err)
			}
		})
	}
}
