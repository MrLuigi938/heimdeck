package integrations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// AlbumSource is offered by photo services whose albums can be a dashboard
// background that changes picture by picture (Immich, PhotoPrism).
type AlbumSource interface {
	// Albums lists the albums with their size, for the picker.
	Albums(ctx context.Context, in *Instance) ([]Choice, error)
	// AlbumImages returns picture addresses that go through Heimdeck's image
	// proxy, so the browser never needs the service's key.
	AlbumImages(ctx context.Context, in *Instance, album string) ([]string, error)
}

var (
	ppUIDRe = regexp.MustCompile(`^[a-z0-9]{16}$`)
)

// maxAlbumImages keeps a background album to a sensible size.
const maxAlbumImages = 300

func (i immich) Albums(ctx context.Context, in *Instance) ([]Choice, error) {
	var albums []struct {
		ID         string `json:"id"`
		AlbumName  string `json:"albumName"`
		AssetCount int    `json:"assetCount"`
	}
	if err := i.call(ctx, in, "GET", "/albums", nil, nil, &albums); err != nil {
		return nil, err
	}
	out := []Choice{}
	for _, a := range albums {
		out = append(out, Choice{ID: a.ID, Name: a.AlbumName, Value: in.T("%d Bilder", a.AssetCount)})
	}
	return out, nil
}

func (i immich) AlbumImages(ctx context.Context, in *Instance, album string) ([]string, error) {
	if !uuidRe.MatchString(album) {
		return nil, in.Errorf("nicht gefunden")
	}
	return remember(in, "immich-album\x00"+in.BaseURL+"\x00"+album, 10*time.Minute, func() ([]string, error) {
		var a struct {
			Assets []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"assets"`
		}
		if err := i.call(ctx, in, "GET", "/albums/"+album, nil, nil, &a); err != nil {
			return nil, err
		}
		out := []string{}
		for _, asset := range a.Assets {
			if asset.Type == "IMAGE" && uuidRe.MatchString(asset.ID) && len(out) < maxAlbumImages {
				out = append(out, in.Proxy("/api/assets/"+asset.ID+"/thumbnail?size=preview"))
			}
		}
		return out, nil
	})
}

func (p photoprism) Albums(ctx context.Context, in *Instance) ([]Choice, error) {
	var albums []struct {
		UID        string `json:"UID"`
		Title      string `json:"Title"`
		PhotoCount int    `json:"PhotoCount"`
	}
	if err := p.get(ctx, in, "/albums", url.Values{"count": {"200"}, "type": {"album"}, "order": {"name"}}, &albums); err != nil {
		return nil, err
	}
	out := []Choice{}
	for _, a := range albums {
		out = append(out, Choice{ID: a.UID, Name: a.Title, Value: in.T("%d Bilder", a.PhotoCount)})
	}
	return out, nil
}

func (p photoprism) AlbumImages(ctx context.Context, in *Instance, album string) ([]string, error) {
	if !ppUIDRe.MatchString(album) {
		return nil, in.Errorf("nicht gefunden")
	}
	return remember(in, "pp-album\x00"+in.BaseURL+"\x00"+album, 10*time.Minute, func() ([]string, error) {
		var photos []ppPhoto
		if err := p.get(ctx, in, "/photos", url.Values{"count": {fmt.Sprint(maxAlbumImages)}, "s": {album}, "merged": {"true"}, "photo": {"true"}}, &photos); err != nil {
			return nil, err
		}
		out := []string{}
		for _, ph := range photos {
			if ppHashRe.MatchString(ph.Hash) {
				out = append(out, in.Proxy("/api/v1/t/"+ph.Hash+"/fit_1920"))
			}
		}
		return out, nil
	})
}
