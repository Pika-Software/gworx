package workshop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseID(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint64
	}{
		{"104603291", 104603291},
		{"  104603291\n", 104603291},
		{"https://steamcommunity.com/sharedfiles/filedetails/?id=104603291", 104603291},
		{"https://steamcommunity.com/sharedfiles/filedetails/?l=german&id=104603291&x=1", 104603291},
		{"steam://url/CommunityFilePage/104603291", 104603291},
	} {
		if got, err := ParseID(tc.in); err != nil || got != tc.want {
			t.Errorf("ParseID(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}

	for _, in := range []string{
		"",
		"abc",
		"https://example.com/",
		"99999999999999999999", // overflows uint64
	} {
		if got, err := ParseID(in); err == nil {
			t.Errorf("ParseID(%q) = %d, want an error", in, got)
		}
	}
}

func TestGModTags(t *testing.T) {
	got := GModTags("servercontent", []string{"fun", "npc"})
	if want := []string{"Addon", "ServerContent", "Fun", "NPC"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if got := GModTags("", nil); !slices.Equal(got, []string{"Addon"}) {
		t.Errorf("empty: got %q", got)
	}
}

func TestFetchBytesRejectsOverLimit(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Write(bytesRepeat(100))
	}))
	defer srv.Close()

	if _, err := FetchBytes(context.Background(), srv.URL, 16); err == nil {
		t.Fatal("expected truncated body to fail")
	}
	got, err := FetchBytes(context.Background(), srv.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 100 {
		t.Fatalf("len = %d", len(got))
	}
	if ua != "gworx" {
		t.Fatalf("User-Agent = %q", ua)
	}
}

func TestGetPreviewURLsKeepsLaterBatches(t *testing.T) {
	old := httpClient
	var calls int
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(r.Body)
		code := http.StatusInternalServerError
		payload := "no"
		if strings.Contains(string(body), "=101") {
			code = http.StatusOK
			payload = `{"response":{"publishedfiledetails":[{"publishedfileid":"101","result":1,"preview_url":"http://img/101"}]}}`
		}
		return &http.Response{
			StatusCode: code,
			Status:     http.StatusText(code),
			Body:       io.NopCloser(strings.NewReader(payload)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
	t.Cleanup(func() { httpClient = old })

	ids := make([]uint64, 101)
	for i := range ids {
		ids[i] = uint64(i + 1)
	}
	urls, err := GetPreviewURLs(context.Background(), ids...)
	if calls != 2 {
		t.Fatalf("batches = %d, want 2", calls)
	}
	if err == nil {
		t.Fatal("expected the failed batch to surface")
	}
	if urls[101] != "http://img/101" {
		t.Fatalf("later batch dropped: %v", urls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestItemDescriptionPrefersLonger(t *testing.T) {
	full := strings.Repeat("描述", 400)
	short := "A short English blurb"
	got := (&apiDetails{PublishedFileID: "1", Description: short, FileDescription: full}).item()
	if got.Description != full {
		t.Fatalf("kept %d runes, want the full file_description", utf8.RuneCountInString(got.Description))
	}
	got = (&apiDetails{PublishedFileID: "1", Description: full, FileDescription: short}).item()
	if got.Description != full {
		t.Fatal("kept the shorter file_description")
	}
}

func TestCStringRepairsSplitRune(t *testing.T) {
	// 描 is e6 8f 8f; cut after the first byte, the way an 8000-byte cap does.
	got := cString([]byte("привет\xe6"))
	if !utf8.ValidString(got) {
		t.Fatalf("invalid utf-8 %q", got)
	}
	if !strings.HasPrefix(got, "привет") {
		t.Fatalf("lost text: %q", got)
	}
	if cString([]byte("ok\x00hidden")) != "ok" {
		t.Fatal("did not stop at the NUL")
	}
}

func bytesRepeat(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return b
}
