package workshop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

// flexInt decodes numbers that Steam sometimes sends as strings.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	var n json.Number
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		n = json.Number(s)
	} else {
		n = json.Number(b)
	}
	if n == "" {
		*f = 0
		return nil
	}
	v, err := n.Int64()
	if err != nil {
		return err
	}
	*f = flexInt(v)
	return nil
}

type apiDetails struct {
	PublishedFileID string  `json:"publishedfileid"`
	Result          int     `json:"result"`
	Creator         string  `json:"creator"`
	ConsumerAppID   int     `json:"consumer_app_id"`
	Title           string  `json:"title"`
	Description     string  `json:"description"`
	FileDescription string  `json:"file_description"`
	FileURL         string  `json:"file_url"`
	FileSize        flexInt `json:"file_size"`
	PreviewURL      string  `json:"preview_url"`
	TimeCreated     int64   `json:"time_created"`
	TimeUpdated     int64   `json:"time_updated"`
	Visibility      int     `json:"visibility"`
	Tags            []struct {
		Tag string `json:"tag"`
	} `json:"tags"`
}

func (d *apiDetails) item() Item {
	id, _ := strconv.ParseUint(d.PublishedFileID, 10, 64)
	desc := d.Description
	if desc == "" {
		desc = d.FileDescription
	}
	it := Item{
		ID:          id,
		Title:       d.Title,
		Description: desc,
		Creator:     d.Creator,
		PreviewURL:  d.PreviewURL,
		FileURL:     d.FileURL,
		FileSize:    int64(d.FileSize),
		Created:     d.TimeCreated,
		Updated:     d.TimeUpdated,
		Visibility:  d.Visibility,
	}
	for _, t := range d.Tags {
		it.Tags = append(it.Tags, t.Tag)
	}
	return it
}

// GetDetails fetches public details of Workshop items; no API key needed.
func GetDetails(ctx context.Context, ids ...uint64) ([]Item, error) {
	form := url.Values{}
	form.Set("itemcount", strconv.Itoa(len(ids)))
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), strconv.FormatUint(id, 10))
	}

	var out struct {
		Response struct {
			Result  int          `json:"result"`
			Details []apiDetails `json:"publishedfiledetails"`
		} `json:"response"`
	}

	if err := apiCall(ctx, http.MethodPost, "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/", form, &out); err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(out.Response.Details))
	for _, d := range out.Response.Details {
		if d.Result != 1 {
			id, _ := strconv.ParseUint(d.PublishedFileID, 10, 64)
			return nil, fmt.Errorf("workshop item %d not found or not accessible (result %d)", id, d.Result)
		}
		if d.ConsumerAppID != 0 && d.ConsumerAppID != AppID {
			return nil, fmt.Errorf("workshop item %s is not a Garry's Mod item (app %d)", d.PublishedFileID, d.ConsumerAppID)
		}
		items = append(items, d.item())
	}
	return items, nil
}

// GetUserFiles lists Garry's Mod items published by steamID64 (requires a Web API key).
func GetUserFiles(ctx context.Context, key, steamID64 string) ([]Item, error) {
	var items []Item

	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("key", key)
		q.Set("steamid", steamID64)
		q.Set("appid", strconv.Itoa(AppID))
		q.Set("page", strconv.Itoa(page))
		q.Set("numperpage", "100")
		q.Set("return_details", "1")
		q.Set("return_tags", "1")
		q.Set("type", "0")

		var out struct {
			Response struct {
				Total   int          `json:"total"`
				Details []apiDetails `json:"publishedfiledetails"`
			} `json:"response"`
		}

		if err := apiCall(ctx, http.MethodGet, "https://api.steampowered.com/IPublishedFileService/GetUserFiles/v1/", q, &out); err != nil {
			return nil, err
		}

		for _, d := range out.Response.Details {
			items = append(items, d.item())
		}

		if len(out.Response.Details) == 0 || len(items) >= out.Response.Total {
			return items, nil
		}
	}
}

func apiCall(ctx context.Context, method, endpoint string, values url.Values, out any) error {
	var req *http.Request
	var err error

	if method == http.MethodGet {
		req, err = http.NewRequestWithContext(ctx, method, endpoint+"?"+values.Encode(), nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, endpoint, strings.NewReader(values.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "gworx")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("steam web api: %s: %s", resp.Status, body)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

// FetchBytes downloads a small resource (e.g. a preview image).
func FetchBytes(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}
