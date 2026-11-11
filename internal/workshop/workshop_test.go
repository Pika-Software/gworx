package workshop

import "testing"

func TestParseID(t *testing.T) {
	for in, want := range map[string]uint64{
		"104603291": 104603291,
		"https://steamcommunity.com/sharedfiles/filedetails/?id=104603291":              104603291,
		"https://steamcommunity.com/sharedfiles/filedetails/?l=german&id=104603291&x=1": 104603291,
		"steam://url/CommunityFilePage/104603291":                                       104603291,
		"  104603291\n": 104603291,
	} {
		got, err := ParseID(in)
		if err != nil || got != want {
			t.Errorf("ParseID(%q) = %d, %v", in, got, err)
		}
	}
	if _, err := ParseID("https://example.com/"); err == nil {
		t.Error("expected error")
	}
}
