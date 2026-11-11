package workshop

import (
	"slices"
	"testing"
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
