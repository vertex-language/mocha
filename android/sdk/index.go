package sdk

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// DefaultIndexURL is Google's SDK repository index.
const DefaultIndexURL = "https://dl.google.com/android/repository/repository2-3.xml"

// stableChannel is channel-0. The index also carries beta, dev and canary
// channels, whose platform packages would otherwise be indistinguishable.
const stableChannel = "channel-0"

// maxIndexSize bounds the index read. It is a couple of megabytes in practice.
const maxIndexSize = 64 << 20

// Platform is one stable platform stub available for download.
type Platform struct {
	API         int
	Revision    Revision
	DisplayName string
	URL         string // absolute, resolved against the index URL
	Size        int64
	SHA1        string
}

// Revision is a platform package revision.
type Revision struct{ Major, Minor, Micro int }

func (r Revision) String() string {
	return fmt.Sprintf("%d.%d.%d", r.Major, r.Minor, r.Micro)
}

// List returns the stable platforms the repository offers, ascending by API
// level. Preview platforms are excluded: their api-level names the release
// under development, so including them would collide with the real package at
// the same level.
func (c *Cache) List(ctx context.Context) ([]Platform, error) {
	data, err := c.index(ctx)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(c.indexURL())
	if err != nil {
		return nil, err
	}

	var doc indexXML
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("sdk: parsing %s: %w", c.indexURL(), err)
	}

	byAPI := map[int]Platform{}
	for _, p := range doc.Packages {
		if !isPlatformPath(p.Path) {
			continue
		}
		// A preview platform. Its api-level is the level being developed.
		if p.Details.Codename != "" {
			continue
		}
		if p.ChannelRef.Ref != "" && p.ChannelRef.Ref != stableChannel {
			continue
		}
		if p.Details.APILevel <= 0 {
			continue
		}
		a, ok := pickArchive(p.Archives)
		if !ok {
			continue
		}
		ref, err := url.Parse(a.Complete.URL)
		if err != nil {
			continue
		}
		cur := Platform{
			API:         p.Details.APILevel,
			Revision:    Revision{p.Revision.Major, p.Revision.Minor, p.Revision.Micro},
			DisplayName: p.DisplayName,
			URL:         base.ResolveReference(ref).String(),
			Size:        a.Complete.Size,
			SHA1:        a.Complete.Checksum.Value,
		}
		// The index may carry more than one package per level across
		// revisions. Highest revision wins.
		if prev, dup := byAPI[cur.API]; dup && !newer(cur.Revision, prev.Revision) {
			continue
		}
		byAPI[cur.API] = cur
	}

	out := make([]Platform, 0, len(byAPI))
	for _, p := range byAPI {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].API < out[j].API })
	return out, nil
}

// Lookup returns the stable platform at api.
func (c *Cache) Lookup(ctx context.Context, api int) (Platform, error) {
	all, err := c.List(ctx)
	if err != nil {
		return Platform{}, err
	}
	for _, p := range all {
		if p.API == api {
			return p, nil
		}
	}
	return Platform{}, fmt.Errorf("api %d: %w", api, ErrNoSuchPlatform)
}

func newer(a, b Revision) bool {
	if a.Major != b.Major {
		return a.Major > b.Major
	}
	if a.Minor != b.Minor {
		return a.Minor > b.Minor
	}
	return a.Micro > b.Micro
}

// isPlatformPath matches "platforms;android-24". Package paths are
// semicolon-separated, and every other package type — sources, system images,
// add-ons — shares the namespace.
func isPlatformPath(path string) bool {
	rest, ok := cutPrefix(path, "platforms;android-")
	if !ok || rest == "" {
		return false
	}
	// A numeric tail; anything else is a codename, already excluded above but
	// cheap to reject here too.
	_, err := strconv.Atoi(rest)
	return err == nil
}

func cutPrefix(s, p string) (string, bool) {
	if len(s) >= len(p) && s[:len(p)] == p {
		return s[len(p):], true
	}
	return "", false
}

// pickArchive returns the archive to download. Platform packages are
// OS-independent and ship exactly one archive; an entry carrying a host-os is
// something else and is skipped rather than guessed at.
func pickArchive(as []archiveXML) (archiveXML, bool) {
	for _, a := range as {
		if a.HostOS == "" && a.Complete.URL != "" {
			return a, true
		}
	}
	return archiveXML{}, false
}

func (c *Cache) indexURL() string {
	if c.IndexURL != "" {
		return c.IndexURL
	}
	return DefaultIndexURL
}

// index returns the raw index, from the cache when fresh.
func (c *Cache) index(ctx context.Context) ([]byte, error) {
	maxAge := c.IndexMaxAge
	if maxAge == 0 {
		maxAge = DefaultIndexMaxAge
	}
	path := filepath.Join(c.Root, "index", "repository.xml")

	if maxAge > 0 {
		if fi, err := os.Stat(path); err == nil && time.Since(fi.ModTime()) < maxAge {
			if b, err := os.ReadFile(path); err == nil {
				return b, nil
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.indexURL(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("sdk: fetching %s: %w", c.indexURL(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sdk: fetching %s: %s", c.indexURL(), resp.Status)
	}

	b, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexSize))
	if err != nil {
		return nil, err
	}
	if maxAge > 0 {
		// A cache write failure is not a fetch failure.
		_ = writeFileAtomic(path, b, 0o644)
	}
	return b, nil
}

// --- wire types -------------------------------------------------------------
//
// Element names are matched without namespaces on purpose. The repository
// namespace is versioned (…/repository2/01 through /03), so binding it would
// make this parser fail on the next index revision for no benefit. Likewise the
// platform package is recognised by its path and api-level rather than by
// xsi:type, whose value is a QName whose prefix the document is free to choose.

type indexXML struct {
	XMLName  xml.Name     `xml:"sdk-repository"`
	Packages []packageXML `xml:"remotePackage"`
}

type packageXML struct {
	Path        string `xml:"path,attr"`
	DisplayName string `xml:"display-name"`
	ChannelRef  struct {
		Ref string `xml:"ref,attr"`
	} `xml:"channelRef"`
	Details struct {
		APILevel int    `xml:"api-level"`
		Codename string `xml:"codename"`
	} `xml:"type-details"`
	Revision struct {
		Major int `xml:"major"`
		Minor int `xml:"minor"`
		Micro int `xml:"micro"`
	} `xml:"revision"`
	Archives []archiveXML `xml:"archives>archive"`
}

type archiveXML struct {
	HostOS   string `xml:"host-os"`
	Complete struct {
		Size     int64 `xml:"size"`
		Checksum struct {
			// Present from repo-common-02; absent means SHA-1.
			Type  string `xml:"type,attr"`
			Value string `xml:",chardata"`
		} `xml:"checksum"`
		URL string `xml:"url"`
	} `xml:"complete"`
}