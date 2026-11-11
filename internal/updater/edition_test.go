package updater

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sPROFFEs/PrAImate/internal/version"
)

type releaseTransport func(*http.Request) (*http.Response, error)

func (f releaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEditionDoesNotSelectAnotherEditionsArchive(t *testing.T) {
	other := "praimate-server"
	if version.Edition == "server" {
		other = "praimate"
	}
	rel := &Release{TagName: version.ReleaseTagPrefix + "1.3.0", Assets: []Asset{
		{Name: other + "-linux-amd64.tar.gz"},
		{Name: version.AssetPrefix + "-linux-amd64.tar.gz"},
	}}
	asset, err := assetForPlatform(rel, "linux", "amd64")
	if err != nil || asset.Name != version.AssetPrefix+"-linux-amd64.tar.gz" {
		t.Fatalf("selected another edition: %#v, %v", asset, err)
	}
	rel.Assets = rel.Assets[:1]
	if _, err := assetForPlatform(rel, "linux", "amd64"); err == nil {
		t.Fatal("accepted another edition when the matching archive was absent")
	}
}

func TestFetchLatestStaysInEdition(t *testing.T) {
	previous := httpClient
	t.Cleanup(func() { httpClient = previous })
	httpClient = &http.Client{Transport: releaseTransport(func(req *http.Request) (*http.Response, error) {
		var payload any
		if version.Edition == "server" {
			if !strings.HasSuffix(req.URL.Path, "/releases") {
				t.Fatalf("wrong channel: %s", req.URL)
			}
			payload = []map[string]any{
				{"tag_name": "v99.0.0"},
				{"tag_name": "server-v1.4.0", "draft": true},
				{"tag_name": "server-v1.2.0", "prerelease": true},
				{"tag_name": "server-v1.3.0", "prerelease": true},
				{"tag_name": "v1.3.1", "assets": []map[string]string{{"name": "praimate-server-linux-amd64.tar.gz"}}},
			}
		} else {
			if !strings.HasSuffix(req.URL.Path, "/releases/latest") {
				t.Fatalf("wrong channel: %s", req.URL)
			}
			payload = map[string]any{"tag_name": "v1.3.0"}
		}
		raw, _ := json.Marshal(payload)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}, nil
	})}
	release, err := FetchLatest()
	want := version.ReleaseTagPrefix + "1.3.0"
	if version.Edition == "server" {
		want = "v1.3.1"
	}
	if err != nil || release.TagName != want {
		t.Fatalf("wrong release: %#v, %v", release, err)
	}
	if !IsNewer(version.ReleaseTagPrefix+"1.3.0", "1.2.26") {
		t.Fatal("edition tag was not compared as a version")
	}
}
