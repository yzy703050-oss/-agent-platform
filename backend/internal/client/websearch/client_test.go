package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackedBody struct {
	io.Reader
	closed     bool
	afterClose func()
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func (b *trackedBody) Close() error {
	b.closed = true
	if b.afterClose != nil {
		b.afterClose()
	}
	return nil
}
func htmlResult(title, link, summary string) string {
	return fmt.Sprintf(`<div class="web-result"><h2 class="result__title"><a href="%s">%s</a></h2><a class="result__snippet">%s</a></div>`, link, title, summary)
}
func TestClientOfficialParserAndBounds(t *testing.T) {
	var body *trackedBody
	cli, err := New(context.Background(), roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://html.duckduckgo.com/html/" || r.Method != http.MethodPost {
			t.Fatal("unexpected endpoint")
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("q") != "Eino" || r.Form.Get("df") != "w" {
			t.Fatal("request not mapped")
		}
		html := `<div id="links">`
		for i := 0; i < 7; i++ {
			html += htmlResult(strings.Repeat("中", 250), fmt.Sprintf("https://example.org/%d", i), strings.Repeat("文", 1000))
		}
		body = &trackedBody{Reader: strings.NewReader(html + `</div>`)}
		return &http.Response{StatusCode: 200, Body: body}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := cli.Search(context.Background(), Request{Query: "Eino", TimeRange: "w"})
	if err != nil || response.Status != "ok" || len(response.Results) != 5 || !body.closed {
		t.Fatalf("response=%+v closed=%v err=%v", response, body.closed, err)
	}
	if utf8.RuneCountInString(response.Results[0].Title) != 200 || utf8.RuneCountInString(response.Results[0].Summary) != 800 {
		t.Fatal("field limits missing")
	}
	raw, _ := json.Marshal(response)
	if len(raw) > 16384 {
		t.Fatalf("response too large: %d", len(raw))
	}
	// A replacement provider must obey the same tool envelope, including JSON escaping overhead.
	input := Response{Status: "ok", Results: []Result{{Title: "unsafe", URL: "javascript:alert(1)"}, {Title: "credential", URL: "https://u:p@example.org/"}}}
	for i := 0; i < 8; i++ {
		input.Results = append(input.Results, Result{Title: strings.Repeat("<", 200), URL: "https://example.org/" + strings.Repeat("a", 1800) + fmt.Sprint(i), Summary: strings.Repeat("<", 800)})
	}
	input.Results = append(input.Results, input.Results[2])
	input.Results = append(input.Results, Result{Title: "too-long", URL: "https://example.org/" + strings.Repeat("b", 2050)})
	bounded := Normalize(input)
	raw, _ = json.Marshal(bounded)
	if len(raw) > 16384 || len(bounded.Results) == 0 || len(bounded.Results) >= 5 || bounded.Results[0].Title == "unsafe" {
		t.Fatalf("not bounded: count=%d bytes=%d", len(bounded.Results), len(raw))
	}
}
func TestClientFailureAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, html string
		status     int
		want       string
	}{
		{"no-results", `<div id="links"><div class="no-results">No results found.</div></div>`, 200, "no_results"},
		{"refused", "blocked", 202, "error"},
		{"challenge", `<html><form id="challenge-form">Verify you are human</form></html>`, 200, "error"},
		{"unexpected", `<html>maintenance</html>`, 200, "error"},
		{"oversize", strings.Repeat("a", (1<<20)+1), 200, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(tc.html)}
			cli, _ := New(context.Background(), roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Body: body}, nil
			}))
			got, err := cli.Search(context.Background(), Request{Query: "test"})
			if !body.closed {
				t.Fatal("response body leaked")
			}
			if tc.want == "error" {
				if !errors.Is(err, ErrUnavailable) {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err != nil || got.Status != tc.want {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
	broken := &trackedBody{Reader: brokenReader{}}
	brokenClient, _ := New(context.Background(), roundTripFunc(func(*http.Request) (*http.Response, error) { return &http.Response{StatusCode: 200, Body: broken}, nil }))
	if _, err := brokenClient.Search(context.Background(), Request{Query: "test"}); !errors.Is(err, ErrUnavailable) || !broken.closed {
		t.Fatal("truncated body was accepted or leaked", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var requests atomic.Int32
	cli, _ := New(context.Background(), roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		html := `<div id="links">` + htmlResult("Eino", "https://example.org/", "summary") + `</div><div class="nav-link"></div><form><input type="hidden" name="q" value="next"></form>`
		return &http.Response{StatusCode: 200, Body: &trackedBody{Reader: strings.NewReader(html), afterClose: cancel}}, nil
	}))
	started := time.Now()
	_, err := cli.Search(ctx, Request{Query: "test"})
	if !errors.Is(err, context.Canceled) || requests.Load() != 1 || time.Since(started) > 5*time.Second {
		t.Fatalf("cancel requests=%d duration=%v err=%v", requests.Load(), time.Since(started), err)
	}
}
